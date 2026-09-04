package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"net/http"
	"net/http/httptest"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/process"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/web"
)

func execCall(command string) provider.ToolCall {
	args, _ := json.Marshal(map[string]string{"command": command})
	return provider.ToolCall{ID: "c1", Name: "execute_command", Arguments: string(args)}
}

// fakeRun records executed commands and returns a fixed result.
func fakeRun(ran *[]string) func(context.Context, string) (string, int) {
	return func(_ context.Context, command string) (string, int) {
		*ran = append(*ran, command)
		return "ok", 0
	}
}

func TestHeadlessApprover_DeniesCommandByDefault(t *testing.T) {
	var ran []string
	resolve := headlessApprover(context.Background(), printOpts{}, nil, fakeRun(&ran), nil, nil, nil, nil, nil, nil, nil)

	result := resolve(execCall("echo hi"))
	if !strings.HasPrefix(result, "error:") || !strings.Contains(result, "--yes") {
		t.Fatalf("default must deny commands with guidance, got %q", result)
	}
	if len(ran) != 0 {
		t.Fatalf("command must not run, ran %v", ran)
	}
}

func TestHeadlessApprover_YesRunsCommand(t *testing.T) {
	var ran []string
	resolve := headlessApprover(context.Background(), printOpts{yes: true}, nil, fakeRun(&ran), nil, nil, nil, nil, nil, nil, nil)

	result := resolve(execCall("echo hi"))
	if len(ran) != 1 || ran[0] != "echo hi" {
		t.Fatalf("--yes must run the command, ran %v", ran)
	}
	if !strings.Contains(result, "exit code: 0") || !strings.Contains(result, "ok") {
		t.Fatalf("unexpected exec result %q", result)
	}
}

func TestHeadlessApprover_AllowlistRunsMatchingCommand(t *testing.T) {
	var ran []string
	resolve := headlessApprover(context.Background(), printOpts{}, []string{"go test"}, fakeRun(&ran), nil, nil, nil, nil, nil, nil, nil)

	if result := resolve(execCall("go test ./...")); strings.HasPrefix(result, "error:") {
		t.Fatalf("allowlisted command must run, got %q", result)
	}
	if result := resolve(execCall("go build")); !strings.HasPrefix(result, "error:") {
		t.Fatalf("non-matching command must be denied, got %q", result)
	}
	if len(ran) != 1 || ran[0] != "go test ./..." {
		t.Fatalf("expected only the allowlisted command to run, ran %v", ran)
	}
}

func TestHeadlessApprover_SafetyFlaggedDeniedEvenWithYes(t *testing.T) {
	var ran []string
	resolve := headlessApprover(context.Background(), printOpts{yes: true}, nil, fakeRun(&ran), nil, nil, nil, nil, nil, nil, nil)

	result := resolve(execCall("git reset --hard"))
	if !strings.HasPrefix(result, "error:") || !strings.Contains(result, "safety-flagged") {
		t.Fatalf("safety-flagged command must be denied under --yes, got %q", result)
	}
	if len(ran) != 0 {
		t.Fatalf("safety-flagged command must not run, ran %v", ran)
	}
}

func TestHeadlessApprover_InvalidCommandArguments(t *testing.T) {
	var ran []string
	resolve := headlessApprover(context.Background(), printOpts{yes: true}, nil, fakeRun(&ran), nil, nil, nil, nil, nil, nil, nil)

	tc := provider.ToolCall{ID: "c1", Name: "execute_command", Arguments: `{"command":""}`}
	if result := resolve(tc); !strings.HasPrefix(result, "error:") {
		t.Fatalf("empty command must be an error result, got %q", result)
	}
}

func TestHeadlessApprover_DeniesEditsByDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.txt")
	args, _ := json.Marshal(map[string]string{"path": path, "content": "hi"})
	tc := provider.ToolCall{ID: "c1", Name: "write_file", Arguments: string(args)}

	resolve := headlessApprover(context.Background(), printOpts{}, nil, fakeRun(&[]string{}), nil, nil, nil, nil, nil, nil, nil)
	if result := resolve(tc); !strings.HasPrefix(result, "error:") || !strings.Contains(result, "--yes") {
		t.Fatalf("default must deny edits with guidance, got %q", result)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file must not be written on deny")
	}
}

func TestHeadlessApprover_YesAppliesEdit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.txt")
	args, _ := json.Marshal(map[string]string{"path": path, "content": "hi"})
	tc := provider.ToolCall{ID: "c1", Name: "write_file", Arguments: string(args)}

	resolve := headlessApprover(context.Background(), printOpts{yes: true}, nil, fakeRun(&[]string{}), nil, nil, nil, nil, nil, nil, nil)
	if result := resolve(tc); strings.HasPrefix(result, "error:") {
		t.Fatalf("--yes must apply the edit, got %q", result)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "hi" {
		t.Fatalf("file not written: %v %q", err, data)
	}
}

func TestHeadlessApprover_UnknownGatedToolDenied(t *testing.T) {
	resolve := headlessApprover(context.Background(), printOpts{yes: true}, nil, fakeRun(&[]string{}), nil, nil, nil, nil, nil, nil, nil)
	tc := provider.ToolCall{ID: "c1", Name: "mystery_tool", Arguments: `{}`}
	if result := resolve(tc); !strings.HasPrefix(result, "error:") {
		t.Fatalf("unknown gated tool must be denied, got %q", result)
	}
}

func TestHeadlessGate(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"execute_command", true},
		{"write_file", true},
		{"edit_file", true},
		{"read_file", false},
		{"search", false},
		{"glob", false},
		{"list_directory", false},
	}
	for _, c := range cases {
		if got := headlessGate(c.name); got != c.want {
			t.Errorf("headlessGate(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestWriteJSONTranscript(t *testing.T) {
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "hi"},
		{Role: provider.RoleAssistant, Content: "checking", ToolCalls: []provider.ToolCall{{ID: "c1", Name: "read_file", Arguments: `{"path":"x"}`}}},
		{Role: provider.RoleTool, Content: "contents", ToolCallID: "c1"},
		{Role: provider.RoleAssistant, Content: "done"},
	}
	var sb strings.Builder
	if err := writeJSONTranscript(&sb, msgs, "done", provider.Usage{PromptTokens: 10, CompletionTokens: 5, CachedTokens: 7}, nil); err != nil {
		t.Fatalf("writeJSONTranscript: %v", err)
	}

	var got jsonTranscript
	if err := json.Unmarshal([]byte(sb.String()), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if !got.Success || got.Final != "done" || got.Error != "" {
		t.Fatalf("unexpected outcome fields: %+v", got)
	}
	// The cached share of the prompt is stated rather than folded away: it is
	// billed at a fraction of the rest, and a script pricing a night of runs
	// cannot recover it from the other two figures.
	if got.Usage.PromptTokens != 10 || got.Usage.CompletionTokens != 5 || got.Usage.CachedTokens != 7 {
		t.Fatalf("unexpected usage: %+v", got.Usage)
	}
	if len(got.Messages) != len(msgs) {
		t.Fatalf("expected %d messages, got %d", len(msgs), len(got.Messages))
	}
	if got.Messages[2].ToolCalls[0].Name != "read_file" || got.Messages[3].ToolCallID != "c1" {
		t.Fatalf("tool call plumbing lost: %+v", got.Messages)
	}

	sb.Reset()
	if err := writeJSONTranscript(&sb, nil, "", provider.Usage{}, fmt.Errorf("boom")); err != nil {
		t.Fatalf("writeJSONTranscript: %v", err)
	}
	if err := json.Unmarshal([]byte(sb.String()), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if got.Success || got.Error != "boom" {
		t.Fatalf("failure transcript wrong: %+v", got)
	}
}

func TestHeadlessApprover_WebFetchDeniedByDefault(t *testing.T) {
	webTools := web.NewToolset(web.NewFetcher(web.Policy{AllowPrivate: true}), nil)
	resolve := headlessApprover(context.Background(), printOpts{}, nil, fakeRun(&[]string{}), nil, nil, webTools, nil, nil, nil, nil)
	tc := provider.ToolCall{ID: "c1", Name: web.FetchToolName, Arguments: `{"url":"https://example.com/"}`}
	if result := resolve(tc); !strings.HasPrefix(result, "error:") || !strings.Contains(result, "--yes") {
		t.Fatalf("default must deny web fetch with guidance, got %q", result)
	}
}

func TestHeadlessApprover_WebFetchRunsWithYes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "fetched body")
	}))
	defer srv.Close()

	webTools := web.NewToolset(web.NewFetcher(web.Policy{AllowPrivate: true}), nil)
	resolve := headlessApprover(context.Background(), printOpts{yes: true}, nil, fakeRun(&[]string{}), nil, nil, webTools, nil, nil, nil, nil)
	tc := provider.ToolCall{ID: "c1", Name: web.FetchToolName, Arguments: `{"url":"` + srv.URL + `"}`}
	result := resolve(tc)
	if strings.HasPrefix(result, "error:") || !strings.Contains(result, "fetched body") {
		t.Fatalf("--yes must fetch, got %q", result)
	}
}

func TestHeadlessApprover_WebFetchUnregisteredWithoutToolset(t *testing.T) {
	resolve := headlessApprover(context.Background(), printOpts{yes: true}, nil, fakeRun(&[]string{}), nil, nil, nil, nil, nil, nil, nil)
	tc := provider.ToolCall{ID: "c1", Name: web.FetchToolName, Arguments: `{"url":"https://example.com/"}`}
	if result := resolve(tc); !strings.HasPrefix(result, "error:") {
		t.Fatalf("web fetch without a toolset must be denied, got %q", result)
	}
}

func TestHeadlessApprover_MutationHookAppendsDiagnostics(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.go")
	args, _ := json.Marshal(map[string]string{"path": path, "content": "package main\n"})
	tc := provider.ToolCall{ID: "c1", Name: "write_file", Arguments: string(args)}

	var hookedName, hookedPath string
	hook := func(name string, raw json.RawMessage, result string) string {
		hookedName = name
		var a struct {
			Path string `json:"path"`
		}
		_ = json.Unmarshal(raw, &a)
		hookedPath = a.Path
		return result + "\n\nDiagnostics (fake) for f.go:\nf.go:1:1 error: boom"
	}
	resolve := headlessApprover(context.Background(), printOpts{yes: true}, nil, fakeRun(&[]string{}), nil, nil, nil, nil, hook, nil, nil)
	result := resolve(tc)
	if !strings.Contains(result, "Diagnostics (fake)") {
		t.Fatalf("approved edit result should carry the hook's diagnostics, got %q", result)
	}
	if hookedName != "write_file" || hookedPath != path {
		t.Fatalf("hook saw name=%q path=%q", hookedName, hookedPath)
	}
}

// A hook may put text in front of the result as well as after it: diagnostics
// an earlier edit stopped waiting for arrive with the next result the model
// reads, and the approved edit's own result has to survive underneath them.
func TestHeadlessApprover_MutationHookMayPrependToTheResult(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.go")
	args, _ := json.Marshal(map[string]string{"path": path, "content": "package main\n"})
	tc := provider.ToolCall{ID: "c1", Name: "write_file", Arguments: string(args)}

	hook := func(name string, raw json.RawMessage, result string) string {
		return "[diagnostics: other.go — 1 error]\nother.go:3:1 error: boom\n\n" + result
	}
	resolve := headlessApprover(context.Background(), printOpts{yes: true}, nil, fakeRun(&[]string{}), nil, nil, nil, nil, hook, nil, nil)
	result := resolve(tc)
	if !strings.HasPrefix(result, "[diagnostics: other.go — 1 error]") {
		t.Fatalf("a held block should open the result, got %q", result)
	}
	if !strings.Contains(result, path) {
		t.Fatalf("the write's own result should survive under it, got %q", result)
	}
}

// processStartCall builds a process-tool start call for approver tests.
func processStartCall(name, command string) provider.ToolCall {
	args, _ := json.Marshal(map[string]string{"action": "start", "name": name, "command": command})
	return provider.ToolCall{ID: "c1", Name: "process", Arguments: string(args)}
}

func newTestProcessSupervisor(t *testing.T) *process.Supervisor {
	t.Helper()
	t.Setenv("SHELL", "/bin/sh")
	sup, err := process.New(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("process.New: %v", err)
	}
	t.Cleanup(sup.Close)
	return sup
}

func TestHeadlessApprover_DeniesProcessStartByDefault(t *testing.T) {
	sup := newTestProcessSupervisor(t)
	resolve := headlessApprover(context.Background(), printOpts{}, nil, fakeRun(&[]string{}), nil, nil, nil, sup, nil, nil, nil)
	result := resolve(processStartCall("web", "echo hi"))
	if !strings.HasPrefix(result, "error:") || !strings.Contains(result, "--yes") {
		t.Fatalf("default must deny process starts with guidance, got %q", result)
	}
}

func TestHeadlessApprover_YesStartsProcess(t *testing.T) {
	sup := newTestProcessSupervisor(t)
	resolve := headlessApprover(context.Background(), printOpts{yes: true}, nil, fakeRun(&[]string{}), nil, nil, nil, sup, nil, nil, nil)
	result := resolve(processStartCall("web", "echo hi"))
	if !strings.Contains(result, "process web:") {
		t.Fatalf("--yes must start the process, got %q", result)
	}
}

func TestHeadlessApprover_AllowlistStartsMatchingProcess(t *testing.T) {
	sup := newTestProcessSupervisor(t)
	resolve := headlessApprover(context.Background(), printOpts{}, []string{"echo"}, fakeRun(&[]string{}), nil, nil, nil, sup, nil, nil, nil)
	result := resolve(processStartCall("web", "echo hi"))
	if !strings.Contains(result, "process web:") {
		t.Fatalf("an allowlisted command must start, got %q", result)
	}
}

func TestHeadlessApprover_SafetyFlaggedProcessStartDenied(t *testing.T) {
	sup := newTestProcessSupervisor(t)
	resolve := headlessApprover(context.Background(), printOpts{yes: true}, nil, fakeRun(&[]string{}), nil, nil, nil, sup, nil, nil, nil)
	result := resolve(processStartCall("wipe", "rm -rf /tmp/x"))
	if !strings.HasPrefix(result, "error:") || !strings.Contains(result, "interactive approval") {
		t.Fatalf("safety-flagged starts must be denied even with --yes, got %q", result)
	}
}

func TestPrintOptsRounds(t *testing.T) {
	cfg := config.Config{}
	cfg.Behavior.MaxToolRounds = 40
	cases := []struct {
		name string
		opts printOpts
		want int
	}{
		{"flag unset defers to config", printOpts{}, 40},
		{"flag unset and config empty defers to the default", printOpts{}, 0},
		{"zero removes the cap", printOpts{maxRoundsSet: true, maxRounds: 0}, agent.UnlimitedToolRounds},
		{"a number wins over config", printOpts{maxRoundsSet: true, maxRounds: 5}, 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := cfg
			if tc.want == 0 {
				c.Behavior.MaxToolRounds = 0
			}
			if got := tc.opts.rounds(c); got != tc.want {
				t.Fatalf("rounds = %d, want %d", got, tc.want)
			}
		})
	}
}

// --max-rounds no longer errors outside --print: a session takes it too, and
// `--max-rounds 0` is how one is told up front to run unattended, which is
// the one thing the in-session offers cannot do. A negative is still the
// value with nothing left to mean, zero having taken "no cap".
func TestCodeCmdMaxRoundsRejectsNegative(t *testing.T) {
	cmd := newCodeCmd()
	cmd.SetArgs([]string{"--print", "--max-rounds", "-1", "do a thing"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "cannot be negative") {
		t.Fatalf("err = %v, want one naming the negative", err)
	}
}

// The session resolves the flag through the same helper the headless run
// does, so `--max-rounds 0` means one thing on both sides of --print.
func TestChatSessionRoundsMatchHeadless(t *testing.T) {
	cfg := config.Config{}
	cfg.Behavior.MaxToolRounds = 40
	if got := maxRoundsFor(cfg, 0, false); got != 40 {
		t.Errorf("unset session = %d, want the config's 40", got)
	}
	if got := maxRoundsFor(cfg, 0, true); got != agent.UnlimitedToolRounds {
		t.Errorf("--max-rounds 0 in a session = %d, want no cap", got)
	}
	// A config file can say the same thing, for a machine that only ever runs
	// unattended: any negative reaches the agent as UnlimitedToolRounds.
	uncapped := config.Config{}
	uncapped.Behavior.MaxToolRounds = -1
	a := agent.New(nil, nil)
	a.SetMaxRounds(maxRoundsFor(uncapped, 0, false))
	if !a.Uncapped() {
		t.Error("a negative behavior.max_tool_rounds should leave the agent uncapped")
	}
}

// A headless run reports the same events an interactive session does, at the
// same codes and with the same position. A run recorded without its position
// cannot be told apart from one that circled for forty rounds.
func TestHeadlessObserver_EventShapes(t *testing.T) {
	db := fixtureStore(t)
	rec := startObserveRecorder(db, "print", "anthropic", "test-model", nil)
	rounds := 0
	obs := headlessObserver{rec: rec, rounds: func() int { return rounds }}

	rounds = 1
	obs.toolResult(toolResultOf("read_file", 5*time.Millisecond, "the file"))
	rounds = 2
	obs.toolResult(toolResultOf("read_file", time.Millisecond, "error: open x: no such file or directory"))
	obs.decision(observe.DecisionDeny, "headless-default")
	rounds = 3
	obs.toolResult(toolResultOf("search", time.Millisecond, "[repeat: this exact search call has now run 3 times]"))
	obs.summary(agent.SummaryVerdict{State: agent.SummaryOffTarget})
	obs.intervene(agent.Intervention{Kind: agent.InterveneSteer})
	obs.retry(agent.RetryNotice{Failure: &provider.Failure{Class: provider.ClassOverloaded}, Attempt: 1, Max: agent.MaxRetryAttempts})
	rec.turn(1, 3, time.Second, observe.TurnDone)
	rec.end()

	assertShapes(t, shapesOf(t, db, rec.sessionID()), []eventShape{
		{kind: storage.AgentEventTool, tool: "read_file", outcome: observe.OutcomeOK, turn: 1, round: 1, timed: true},
		{kind: storage.AgentEventTool, tool: "read_file", outcome: observe.OutcomeError,
			reason: observe.ClassNotFound, turn: 1, round: 2, timed: true},
		{kind: storage.AgentEventDecision, outcome: observe.DecisionDeny, reason: "headless-default", turn: 1, round: 2},
		{kind: storage.AgentEventTool, tool: "search", outcome: observe.OutcomeOK, turn: 1, round: 3, timed: true},
		{kind: storage.AgentEventSignal, outcome: observe.SignalRepeat, reason: "search", turn: 1, round: 3},
		{kind: storage.AgentEventSignal, outcome: observe.SignalSummary, reason: "off-target", turn: 1, round: 3},
		{kind: storage.AgentEventSignal, outcome: observe.SignalIntervene, reason: "steer", turn: 1, round: 3},
		{kind: storage.AgentEventSignal, outcome: observe.SignalRetry, reason: "overloaded", turn: 1, round: 3},
		{kind: storage.AgentEventTurn, outcome: observe.TurnDone, turn: 1, round: 3, timed: true},
	})
}

// A headless run's turn ends in the same closed set a session's does. The
// round cap in particular is the same event on both surfaces; spelling it
// `failed` here would read the whole headless population as having no capped
// turns at all.
func TestHeadlessTurnOutcome(t *testing.T) {
	for _, c := range []struct {
		name string
		err  error
		want string
	}{
		{"finished", nil, observe.TurnDone},
		{"round cap", fmt.Errorf("%w after 60 rounds", agent.ErrRoundCap), observe.TurnCapPaused},
		{"interrupted", agent.ErrInterrupted, observe.TurnCancelled},
		{"stream failure", errors.New("connection reset"), observe.TurnFailed},
	} {
		if got := headlessTurnOutcome(c.err); got != c.want {
			t.Errorf("%s: headlessTurnOutcome = %q, want %q", c.name, got, c.want)
		}
	}
}

// A headless run has no changeset, so the paths its calls wrote are the
// subtrahend the tree reading gets.
func TestWrittenByCalls_RecordsOnlySuccessfulMutations(t *testing.T) {
	w := &writtenByCalls{}
	resolve := w.wrap(func(tc provider.ToolCall) string {
		if tc.Name == "write_file" {
			return "written"
		}
		return "error: declined"
	})
	resolve(provider.ToolCall{Name: "write_file", Arguments: `{"path":"a.go","content":"x"}`})
	resolve(provider.ToolCall{Name: "edit_file", Arguments: `{"path":"b.go"}`})
	resolve(provider.ToolCall{Name: "read_file", Arguments: `{"path":"c.go"}`})
	if got := w.paths(); len(got) != 1 || got[0] != "a.go" {
		t.Errorf("paths = %v, want [a.go]", got)
	}
}

// A flag the run cannot honour stops it. --resume opens a picker, and a run
// with nobody in front of it can neither draw one nor be answered; accepting
// it and starting from nothing is the shape this refuses.
func TestHeadlessFlagCheck_PickerIsAUsageError(t *testing.T) {
	err := headlessFlagCheck(chatSession{resumePick: true})
	if err == nil || !strings.Contains(err.Error(), "--resume=<name>") {
		t.Fatalf("err = %v, want one naming the way to say it without a picker", err)
	}
	if err := headlessFlagCheck(chatSession{continueLast: true}); err != nil {
		t.Fatalf("--continue is honoured, got %v", err)
	}
	if err := headlessFlagCheck(chatSession{resumeName: "yesterday"}); err != nil {
		t.Fatalf("a named chat is honoured, got %v", err)
	}
}

// The picker reaches the refusal through the command, so the flag a person
// types is the one that is refused.
func TestCodeCmdPrintRefusesThePicker(t *testing.T) {
	cmd := newCodeCmd()
	cmd.SetArgs([]string{"--print", "--resume", "do a thing"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "nobody to pick with") {
		t.Fatalf("err = %v, want the picker refusal", err)
	}
}

// --resume with an empty value is neither a name nor a request for the
// picker, and starting fresh on it would be the silent no-op this story
// exists to remove.
func TestCodeCmdResumeNeedsAChat(t *testing.T) {
	cmd := newCodeCmd()
	cmd.SetArgs([]string{"--print", "--resume=", "do a thing"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--resume needs a chat") {
		t.Fatalf("err = %v, want one asking for a chat", err)
	}
}

// printStore is a store of this test's own, on a path nothing else writes.
func printStore(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// headlessSystem is the one message a run that continues nothing opens on.
func headlessSystem() []provider.Message {
	return []provider.Message{{Role: provider.RoleSystem, Content: "this run's prompt"}}
}

// What --continue was documented to do and did not: the stored conversation
// reaches the request, in front of the prompt, with the reading of the
// checkout between the two.
func TestOpenHeadlessChat_ContinueSendsTheStoredTranscript(t *testing.T) {
	db := printStore(t)
	stored := []provider.Message{
		{Role: provider.RoleSystem, Content: "the prompt it was saved under"},
		{Role: provider.RoleUser, Content: "rename the widget"},
		{Role: provider.RoleAssistant, Content: "renamed it"},
	}
	if err := db.SaveChat("yesterday", stored); err != nil {
		t.Fatalf("save: %v", err)
	}

	saved, msgs, err := openHeadlessChat(db, chatSession{continueLast: true}, headlessSystem(), "this run's prompt")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if saved.slot != "yesterday" {
		t.Fatalf("slot = %q, want the conversation it continued", saved.slot)
	}
	if msgs[0].Role != provider.RoleSystem || msgs[0].Content != "this run's prompt" {
		t.Fatalf("the system prompt is this run's, got %+v", msgs[0])
	}
	if saved.head == 0 {
		t.Fatal("a reopened conversation is told what the checkout looks like now")
	}
	// The reading sits between the prompt and everything the conversation
	// remembers, and the transcript follows it whole and in order.
	want := []string{"rename the widget", "renamed it"}
	got := msgs[1+saved.head:]
	if len(got) != len(want) {
		t.Fatalf("after the reading: %d messages, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Content != w {
			t.Fatalf("message %d = %q, want %q", i, got[i].Content, w)
		}
	}

	// And that is what the provider is actually asked, which is the half the
	// flag never reached.
	var sent []provider.Message
	a := agent.New(msgs, func(m []provider.Message, _ string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		sent = append([]provider.Message{}, m...)
		ch := make(chan provider.StreamEvent, 2)
		ch <- provider.StreamEvent{Token: "done"}
		ch <- provider.StreamEvent{Done: true}
		close(ch)
		return ch, func() {}, nil
	})
	if _, err := (&agent.Headless{Agent: a}).Run("carry on"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(sent) == 0 || sent[len(sent)-1].Content != "carry on" {
		t.Fatalf("the new prompt is last, got %d messages", len(sent))
	}
	var carried bool
	for _, m := range sent {
		if m.Content == "rename the widget" {
			carried = true
		}
	}
	if !carried {
		t.Fatal("the stored transcript must reach the request")
	}
}

// A run that was told to continue nothing carries nothing: the prompt is the
// whole of what the model is shown.
func TestOpenHeadlessChat_FreshRunCarriesNoConversation(t *testing.T) {
	db := printStore(t)
	if err := db.SaveChat("yesterday", []provider.Message{
		{Role: provider.RoleUser, Content: "rename the widget"}}); err != nil {
		t.Fatalf("save: %v", err)
	}

	saved, msgs, err := openHeadlessChat(db, chatSession{}, headlessSystem(), "this run's prompt")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Role != provider.RoleSystem {
		t.Fatalf("a fresh run opens on the system prompt alone, got %d messages", len(msgs))
	}
	if saved.slot == "" || saved.slot == "yesterday" {
		t.Fatalf("slot = %q, want one of this run's own", saved.slot)
	}
}

// Two runs started in the same second are two conversations. The slot is the
// store's to hand out for exactly this reason: read off the clock, both would
// mint the same name and the second save would be the only one left.
func TestOpenHeadlessChat_SameSecondRunsGetTheirOwnSlots(t *testing.T) {
	db := printStore(t)
	first, _, err := openHeadlessChat(db, chatSession{}, headlessSystem(), "p")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	second, _, err := openHeadlessChat(db, chatSession{}, headlessSystem(), "p")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if first.slot == second.slot {
		t.Fatalf("both runs took %q", first.slot)
	}
}

// A run leaves its conversation where reopening the most recent one finds it,
// without the reading it was opened with — that is built from the checkout
// each time, and a slot that kept one would show it as something the person
// said.
func TestHeadlessChat_SaveLeavesASlotToContinue(t *testing.T) {
	db := printStore(t)
	saved, msgs, err := openHeadlessChat(db, chatSession{}, headlessSystem(), "this run's prompt")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	msgs = append(msgs,
		provider.Message{Role: provider.RoleUser, Content: "fix the parser"},
		provider.Message{Role: provider.RoleAssistant, Content: "fixed"})
	saved.save(msgs)

	// The path a session takes when it is told to continue the last one.
	reopened, err := chatSession{continueLast: true}.resumeChat(db)
	if err != nil {
		t.Fatalf("continue: %v", err)
	}
	if reopened.slot != saved.slot {
		t.Fatalf("continued %q, want the run's slot %q", reopened.slot, saved.slot)
	}
	if len(reopened.messages) != len(msgs) {
		t.Fatalf("stored %d messages, want %d", len(reopened.messages), len(msgs))
	}
	if reopened.messages[len(reopened.messages)-1].Content != "fixed" {
		t.Fatalf("last message = %q, want the run's answer", reopened.messages[len(reopened.messages)-1].Content)
	}
	// A run parks nothing: the mark that brings a session back mid-turn must
	// not be written by one.
	if _, held, err := db.ChatHold(saved.slot); err != nil || held {
		t.Fatalf("hold = %v, %v; a run leaves none", held, err)
	}
}

// The reading a resume put in front of the conversation is left out of what
// the slot keeps, so opening it again is told about the tree once.
func TestHeadlessChat_SaveDropsTheReading(t *testing.T) {
	db := printStore(t)
	if err := db.SaveChat("yesterday", []provider.Message{
		{Role: provider.RoleSystem, Content: "old prompt"},
		{Role: provider.RoleUser, Content: "rename the widget"},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	saved, msgs, err := openHeadlessChat(db, chatSession{continueLast: true}, headlessSystem(), "this run's prompt")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	msgs = append(msgs, provider.Message{Role: provider.RoleUser, Content: "carry on"})
	saved.save(msgs)

	back, err := db.LoadChat("yesterday")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(back) != 3 {
		t.Fatalf("stored %d messages, want the prompt, the turn and the new one", len(back))
	}
	for _, m := range back {
		if strings.HasPrefix(m.Content, "[resume:") {
			t.Fatalf("the reading reached the slot: %q", m.Content)
		}
	}
}

// The run worth reopening is the one that failed, so the save is not the
// success path's. A turn that used up its rounds is written down whole.
func TestHeadlessChat_SaveOnTheRoundCapExit(t *testing.T) {
	db := printStore(t)
	saved, msgs, err := openHeadlessChat(db, chatSession{}, headlessSystem(), "this run's prompt")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	round := func() (<-chan provider.StreamEvent, context.CancelFunc, error) {
		ch := make(chan provider.StreamEvent, 1)
		ch <- provider.StreamEvent{ToolCalls: []provider.ToolCall{{ID: "c1", Name: "search"}}}
		close(ch)
		return ch, func() {}, nil
	}
	a := agent.New(msgs, func([]provider.Message, string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		return round()
	})
	a.SetExecutor(func(string, json.RawMessage) (string, error) { return "nothing found", nil })
	a.SetMaxRounds(1)

	if _, err := (&agent.Headless{Agent: a}).Run("look for it"); !errors.Is(err, agent.ErrRoundCap) {
		t.Fatalf("err = %v, want the round cap", err)
	}
	saved.save(a.Messages())

	back, err := db.LoadChat(saved.slot)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	var asked, answered bool
	for _, m := range back {
		if m.Content == "look for it" {
			asked = true
		}
		if m.Role == provider.RoleTool {
			answered = true
		}
	}
	if !asked || !answered {
		t.Fatalf("a capped run's conversation must be stored whole, got %d messages", len(back))
	}
}

// A run with no store keeps running. Persistence is not a precondition for
// answering a prompt, and a save that cannot happen is not a failure.
func TestHeadlessChat_NoStoreIsAQuietNoOp(t *testing.T) {
	saved, msgs, err := openHeadlessChat(nil, chatSession{}, headlessSystem(), "this run's prompt")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want the system prompt alone", len(msgs))
	}
	saved.save(msgs)
}

// --resume carries either a conversation or the request for the picker, and
// the two have to stay tellable apart: a run behind --print can honour the
// first and not the second.
func TestCodeCmdResumeCarriesAChatOrThePicker(t *testing.T) {
	named := newCodeCmd()
	if err := named.Flags().Parse([]string{"--resume=yesterday"}); err != nil {
		t.Fatalf("parse: %v", err)
	}
	got, _ := named.Flags().GetString("resume")
	if resumeNamed(got) != "yesterday" {
		t.Fatalf("--resume=<name> gave %q", resumeNamed(got))
	}

	for _, spelling := range [][]string{{"--resume"}, {"-r"}} {
		picker := newCodeCmd()
		if err := picker.Flags().Parse(spelling); err != nil {
			t.Fatalf("parse %v: %v", spelling, err)
		}
		got, _ := picker.Flags().GetString("resume")
		if got != resumeFromPicker || resumeNamed(got) != "" {
			t.Fatalf("%v gave %q, want the picker and no chat", spelling, got)
		}
	}
}

// A conversation named on the command line reaches the request the same way
// the most recent one does. It is the spelling a script has — there is no
// browser to pick from — so it is the one that must not quietly open
// something else.
func TestOpenHeadlessChat_ANamedChatIsWhatTheRunCarriesOn(t *testing.T) {
	db := printStore(t)
	if err := db.SaveChat("the one I want", []provider.Message{
		{Role: provider.RoleSystem, Content: "old prompt"},
		{Role: provider.RoleUser, Content: "the widget"},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	// Saved after it, so "the most recent" is the wrong answer here.
	if err := db.SaveChat("something else", []provider.Message{
		{Role: provider.RoleUser, Content: "the other thing"}}); err != nil {
		t.Fatalf("save: %v", err)
	}

	saved, msgs, err := openHeadlessChat(db,
		chatSession{resumeName: "the one I want"}, headlessSystem(), "this run's prompt")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if saved.slot != "the one I want" {
		t.Fatalf("slot = %q, want the chat that was named", saved.slot)
	}
	if msgs[len(msgs)-1].Content != "the widget" {
		t.Fatalf("last message = %q, want the named conversation's", msgs[len(msgs)-1].Content)
	}
}

// The spellings --resume answers to, through the parser a person's typing
// actually reaches. A value on its own token is not one of them: pflag reads
// the flag as unvalued and leaves the word where it lies, which on this
// command is the prompt — the same thing it meant before the flag could name
// a chat at all.
func TestCodeCmdResumeSpellings(t *testing.T) {
	cases := []struct {
		args   []string
		resume string
		rest   []string
	}{
		{[]string{"--resume=yesterday"}, "yesterday", nil},
		{[]string{"-r=yesterday"}, "yesterday", nil},
		{[]string{"--resume"}, resumeFromPicker, nil},
		{[]string{"-r"}, resumeFromPicker, nil},
		{[]string{"-r", "fix the bug"}, resumeFromPicker, []string{"fix the bug"}},
		{[]string{"--resume", "fix the bug"}, resumeFromPicker, []string{"fix the bug"}},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			cmd := newCodeCmd()
			if err := cmd.Flags().Parse(tc.args); err != nil {
				t.Fatalf("parse: %v", err)
			}
			got, _ := cmd.Flags().GetString("resume")
			if got != tc.resume {
				t.Errorf("resume = %q, want %q", got, tc.resume)
			}
			rest := cmd.Flags().Args()
			if len(rest) != len(tc.rest) {
				t.Fatalf("left %v as arguments, want %v", rest, tc.rest)
			}
			for i := range rest {
				if rest[i] != tc.rest[i] {
					t.Errorf("argument %d = %q, want %q", i, rest[i], tc.rest[i])
				}
			}
		})
	}
}

// passingSuites is a config whose named suites both come back clean.
const passingSuites = `{"on_close": "fast", "suites": {
	"default": {"checks": [{"name": "c", "exe": "sh", "args": ["-c", "true"]}]},
	"fast": {"checks": [{"name": "c", "exe": "sh", "args": ["-c", "true"]}]}}}`

func TestOnCloseGate_ReadsTheSuiteAndTheBudget(t *testing.T) {
	ws := t.TempDir()
	writeQualityConfig(t, ws, passingSuites)
	suite, retries, ok := onCloseGate(&quality.Runner{Workspace: ws})
	if !ok || suite != "fast" || retries != quality.DefaultCloseRetries {
		t.Fatalf("onCloseGate = %q, %d, %v", suite, retries, ok)
	}
}

func TestOnCloseGate_IsACleanNoOpWithoutAUsableConfig(t *testing.T) {
	missing := t.TempDir()
	if _, _, ok := onCloseGate(&quality.Runner{Workspace: missing}); ok {
		t.Error("a workspace with no config asked for an on-close run")
	}
	broken := t.TempDir()
	writeQualityConfig(t, broken, `{"suites": {`)
	if _, _, ok := onCloseGate(&quality.Runner{Workspace: broken}); ok {
		t.Error("a broken config asked for an on-close run")
	}
	named := t.TempDir()
	writeQualityConfig(t, named, `{"suites": {"fast": {"checks": [{"name": "c", "exe": "sh", "args": ["-c", "true"]}]}}}`)
	if _, _, ok := onCloseGate(&quality.Runner{Workspace: named}); ok {
		t.Error("a config that names no on_close suite asked for a run")
	}
	if _, _, ok := onCloseGate(nil); ok {
		t.Error("a session with no gate asked for a run")
	}
}

func TestHeadlessCloseGate_RunsNothingForAChangesetWithNoWorkInIt(t *testing.T) {
	ws := t.TempDir()
	writeQualityConfig(t, ws, passingSuites)
	for _, tc := range []struct {
		name  string
		paths []string
	}{
		{"nothing written", nil},
		{"the state directory only", []string{filepath.Join(".shhh", "todo", "x.md")}},
	} {
		g := &headlessCloseGate{
			ctx: context.Background(), gate: &quality.Runner{Workspace: ws},
			suite: "fast", retries: 1, written: func() []string { return tc.paths },
		}
		if fb := g.close("done"); fb != "" {
			t.Errorf("%s: handed back %q", tc.name, fb)
		}
		if g.last != nil {
			t.Errorf("%s: ran the suite anyway", tc.name)
		}
		if err := g.err(); err != nil {
			t.Errorf("%s: err = %v, want nil", tc.name, err)
		}
	}
}

func TestHeadlessCloseGate_HandsBackAFailureUntilTheBudgetIsSpent(t *testing.T) {
	ws := t.TempDir()
	writeQualityConfig(t, ws, `{"on_close": "fast", "suites": {
		"fast": {"checks": [{"name": "c", "exe": "sh", "args": ["-c", "exit 3"]}]}}}`)
	g := &headlessCloseGate{
		ctx: context.Background(), gate: &quality.Runner{Workspace: ws},
		suite: "fast", retries: 1, written: func() []string { return []string{"a.go"} },
	}
	first := g.close("done")
	if !strings.Contains(first, "FAIL") {
		t.Fatalf("first hand-back = %q, want the runner's own text", first)
	}
	if sum, ok := quality.Summarize(first); !ok || sum.Suite != "fast" {
		t.Fatalf("the hand-back is not a formatted result: %q", first)
	}
	if second := g.close("done"); second != "" {
		t.Errorf("second hand-back = %q, want none once the budget is spent", second)
	}
	err := g.err()
	if err == nil || !strings.Contains(err.Error(), "fail") {
		t.Fatalf("err = %v, want a failing verdict", err)
	}
}

func TestHeadlessCloseGate_APassEndsTheTurnAndTheExitCode(t *testing.T) {
	ws := t.TempDir()
	writeQualityConfig(t, ws, passingSuites)
	g := &headlessCloseGate{
		ctx: context.Background(), gate: &quality.Runner{Workspace: ws},
		suite: "fast", retries: 1, written: func() []string { return []string{"a.go"} },
	}
	if fb := g.close("done"); fb != "" {
		t.Errorf("a pass handed back %q", fb)
	}
	if err := g.err(); err != nil {
		t.Errorf("err = %v, want nil after a pass", err)
	}
}

func TestHeadlessCloseGate_BlockedIsNeverAPass(t *testing.T) {
	ws := t.TempDir()
	writeQualityConfig(t, ws, `{"on_close": "fast", "suites": {
		"fast": {"checks": [{"name": "c", "exe": "definitely-not-on-this-path"}]}}}`)
	g := &headlessCloseGate{
		ctx: context.Background(), gate: &quality.Runner{Workspace: ws},
		suite: "fast", retries: 0, written: func() []string { return []string{"a.go"} },
	}
	g.close("done")
	err := g.err()
	if err == nil || !strings.Contains(err.Error(), string(quality.VerdictBlocked)) {
		t.Fatalf("err = %v, want blocked", err)
	}
}

// The unattended run's window-recovery step is built from what anything can
// say about the model it runs on, and from nothing at all where nothing can
// say: a step against a guessed window would throw away a conversation that
// had most of its room left.
func TestHeadlessCompactor_NeedsAWindowSomethingCanName(t *testing.T) {
	defs := []provider.Tool{{Name: "read_file", Description: "read a file"}}

	c := headlessCompactor(t.Context(), config.Config{}, &sessionEnv{modelName: "llama-3.1"}, nil, nil, defs)
	if c == nil {
		t.Fatal("no step for a model whose window the family table publishes")
	}
	if c.Window != 128_000 || c.Model != "llama-3.1" {
		t.Fatalf("step built with window %d for %q", c.Window, c.Model)
	}
	// The definitions are on every request and in no message, so a run that
	// left them out would think it had a toolset's worth of room it has not.
	if c.ToolTokens <= 0 {
		t.Fatal("the toolset costs the estimate nothing")
	}
	if c.Stream != nil {
		t.Fatal("a summary was routed off the conversation's model with none configured")
	}

	if c := headlessCompactor(t.Context(), config.Config{}, &sessionEnv{modelName: "a-private-build"}, nil, nil, defs); c != nil {
		t.Fatalf("a step was built against a window nothing could name: %+v", c)
	}
}

// A configured summary model takes the request, but never one whose window
// is smaller than the conversation it is being handed: the moment a
// compaction is asked for is the moment there is no room to fail.
func TestHeadlessCompactor_SummaryModelMustFitTheConversation(t *testing.T) {
	env := &sessionEnv{modelName: "llama-3.1"}

	roomy := config.Config{}
	roomy.Summary.Model = "gemma-4"
	if c := headlessCompactor(t.Context(), roomy, env, nil, nil, nil); c == nil || c.Stream == nil {
		t.Fatalf("the configured summary model did not take the request: %+v", c)
	}

	cramped := config.Config{}
	cramped.Summary.Model = "phi"
	if c := headlessCompactor(t.Context(), cramped, env, nil, nil, nil); c == nil || c.Stream != nil {
		t.Fatalf("a summary model too small to read the request took it: %+v", c)
	}

	unknown := config.Config{}
	unknown.Summary.Model = "a-private-build"
	if c := headlessCompactor(t.Context(), unknown, env, nil, nil, nil); c == nil || c.Stream != nil {
		t.Fatalf("a summary model nothing can vouch for took the request: %+v", c)
	}
}

// toolResultOf is one executed call as the loop hands it to the front-end:
// the call it answers, what came back, and how long it took.
func toolResultOf(tool string, d time.Duration, result string) agent.ToolResult {
	return agent.ToolResult{
		Call:     provider.ToolCall{ID: "call-" + tool, Name: tool},
		Result:   result,
		Duration: d,
	}
}

// --json is the older spelling and goes on meaning what it meant. Naming both
// spellings and disagreeing is refused rather than resolved, because either
// answer is somebody's script reading the wrong stream.
func TestResolveOutput(t *testing.T) {
	for _, c := range []struct {
		name  string
		named string
		alias bool
		want  string
	}{
		{"nothing given streams the answer", "", false, outputText},
		{"--json is --output json", "", true, outputJSON},
		{"--output json outright", outputJSON, false, outputJSON},
		{"--output json beside its own alias", outputJSON, true, outputJSON},
		{"--output jsonl", outputJSONL, false, outputJSONL},
		{"--output text outright", outputText, false, outputText},
	} {
		got, err := resolveOutput(c.named, c.alias)
		if err != nil || got != c.want {
			t.Errorf("%s: resolveOutput(%q, %v) = %q, %v; want %q", c.name, c.named, c.alias, got, err, c.want)
		}
	}
	if _, err := resolveOutput(outputJSONL, true); err == nil {
		t.Error("--json beside --output jsonl resolved to something instead of a usage error")
	}
	if _, err := resolveOutput("yaml", false); err == nil {
		t.Error("a shape nothing writes was accepted")
	}
}

func TestCodeCmdOutputRefusesAShapeItCannotWrite(t *testing.T) {
	cmd := newCodeCmd()
	cmd.SetArgs([]string{"--output", "yaml", "do a thing"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "jsonl") {
		t.Fatalf("err = %v, want one naming the shapes a run can write", err)
	}
}

// Every code in the closed set, from the same inputs the run projects it
// from: the outcome its turn was recorded under, the gate's last verdict and
// whether the policy's last word was a refusal.
func TestHeadlessExitCode(t *testing.T) {
	for _, c := range []struct {
		name       string
		err        error
		gateFailed bool
		refused    bool
		want       int
	}{
		{"a finished turn", nil, false, false, exitDone},
		{"the round cap", fmt.Errorf("%w after 40 rounds", agent.ErrRoundCap), false, false, exitRoundCap},
		{"an interrupt", agent.ErrInterrupted, false, false, exitInterrupted},
		{"a provider that stopped answering", fmt.Errorf("overloaded"), false, false, exitProvider},
		{"a failing suite", nil, true, false, exitGate},
		{"a refusal that ended the turn", nil, false, true, exitRefused},
		// The two readings only apply to a turn that finished. A run that was
		// interrupted mid-edit says it was interrupted, whatever the suite
		// went on to think of the half-written tree.
		{"an interrupt outranks the suite", agent.ErrInterrupted, true, true, exitInterrupted},
		{"the cap outranks a refusal", agent.ErrRoundCap, false, true, exitRoundCap},
		// A verdict about the tree as it now stands is the more actionable of
		// the two facts a finished turn can carry.
		{"the suite outranks a refusal", nil, true, true, exitGate},
	} {
		got := headlessExitCode(headlessTurnOutcome(c.err), c.gateFailed, c.refused)
		if got != c.want {
			t.Errorf("%s: exit = %d, want %d", c.name, got, c.want)
		}
	}
}

// The code has to survive the whole return path — the command tree, the
// dressing that prints the error, main — or the closed set is a set nothing
// outside the process can read.
func TestExitCodeCarriesTheCodeOutToTheProcess(t *testing.T) {
	if got := ExitCode(nil); got != 0 {
		t.Errorf("no error exits %d, want 0", got)
	}
	// Anything that is not an unattended run's ending is a 1: a command that
	// could not run is one fact, however it failed.
	if got := ExitCode(errors.New("config: no such file")); got != 1 {
		t.Errorf("an ordinary failure exits %d, want 1", got)
	}
	err := error(exitError{code: exitRoundCap, err: fmt.Errorf("%w after 40 rounds", agent.ErrRoundCap)})
	if got := ExitCode(fmt.Errorf("running the command: %w", err)); got != exitRoundCap {
		t.Errorf("a wrapped coded error exits %d, want %d", got, exitRoundCap)
	}
	// The reason is still readable through it, which is what the stderr line
	// a person reads is built from.
	if !errors.Is(err, agent.ErrRoundCap) {
		t.Error("the code hid what happened")
	}
}

// A denial the run went on from is not the run being refused. The verdict
// that matters is the one still standing when the model stopped.
func TestLastVerdictIsTheOneThatEndedTheTurn(t *testing.T) {
	var seen []string
	l := &lastVerdict{}
	note := l.wrap(func(decision, reason string) { seen = append(seen, decision+"/"+reason) })

	if l.refused() {
		t.Error("a run that was never asked anything reports a refusal")
	}
	note(observe.DecisionDeny, observe.ReasonHeadlessDefault)
	if !l.refused() {
		t.Error("a standing denial is not reported")
	}
	note(observe.DecisionAllow, observe.ReasonHeadlessYes)
	if l.refused() {
		t.Error("a denial the run went on from is still reported as the ending")
	}
	// Every verdict still reaches the record on its way past; remembering one
	// is not a second place a decision has to be reported to.
	want := []string{
		observe.DecisionDeny + "/" + observe.ReasonHeadlessDefault,
		observe.DecisionAllow + "/" + observe.ReasonHeadlessYes,
	}
	if strings.Join(seen, ",") != strings.Join(want, ",") {
		t.Errorf("the record was told %v, want %v", seen, want)
	}
}

// scriptedStream answers each request with the next round of a script, which
// is the whole of what a provider is to the loop.
func scriptedStream(rounds ...[]provider.StreamEvent) agent.StreamFunc {
	round := 0
	return func([]provider.Message, string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		ch := make(chan provider.StreamEvent, 8)
		var events []provider.StreamEvent
		if round < len(rounds) {
			events = rounds[round]
		}
		round++
		go func() {
			defer close(ch)
			for _, ev := range events {
				ch <- ev
			}
		}()
		return ch, func() {}, nil
	}
}

// replayJSONL rebuilds the conversation from the stream, the way a consumer
// that watched the run would have to. Text arrives in pieces and the calls of
// a round arrive before any of their results, so an assistant message is
// closed off by the first result that answers it, or by the close line.
func replayJSONL(t *testing.T, lines string) []jsonMessage {
	t.Helper()
	var out []jsonMessage
	var text strings.Builder
	var calls []jsonToolCall
	flush := func() {
		if text.Len() == 0 && len(calls) == 0 {
			return
		}
		out = append(out, jsonMessage{Role: string(provider.RoleAssistant), Content: text.String(), ToolCalls: calls})
		text.Reset()
		calls = nil
	}
	for _, line := range strings.Split(strings.TrimSpace(lines), "\n") {
		var ev jsonEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("stream line is not JSON: %v (%q)", err, line)
		}
		switch ev.Kind {
		case observe.EventText:
			text.WriteString(ev.Text)
		case observe.EventToolCall:
			calls = append(calls, jsonToolCall{ID: ev.ID, Name: ev.Tool, Arguments: ev.Arguments})
		case observe.EventToolResult:
			flush()
			out = append(out, jsonMessage{Role: string(provider.RoleTool), Content: ev.Result, ToolCallID: ev.ID})
		case observe.EventClose:
			flush()
		}
	}
	return out
}

// The stream and the transcript are two readings of one run, so what a
// consumer watching the events builds has to be what the transcript states at
// the end. Anything less and a script has to read both.
func TestJSONLStreamReplaysToTheTranscript(t *testing.T) {
	call := provider.ToolCall{ID: "c1", Name: "read_file", Arguments: `{"path":"x"}`}
	a := agent.New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, scriptedStream(
		[]provider.StreamEvent{
			{Token: "look"},
			{Token: "ing"},
			{Usage: &provider.Usage{PromptTokens: 10, CompletionTokens: 2, CachedTokens: 8}},
			{ToolCalls: []provider.ToolCall{call}},
		},
		[]provider.StreamEvent{{Token: "done"}, {Done: true}},
	))
	a.SetExecutor(func(string, json.RawMessage) (string, error) { return "contents", nil })

	var lines strings.Builder
	events := newJSONLStream(&lines)
	obs := headlessObserver{rounds: a.Rounds, stream: events}
	h := &agent.Headless{
		Agent:        a,
		Gate:         func(provider.ToolCall) bool { return false },
		OnText:       obs.text,
		OnToolCall:   func(tc provider.ToolCall) { obs.call(tc) },
		OnToolResult: obs.toolResult,
		OnUsage:      func(u *provider.Usage) { obs.usage(*u) },
	}

	final, err := h.Run("read x")
	if err != nil || final != "done" {
		t.Fatalf("run = %q, %v", final, err)
	}
	events.closed(obs.pos(), headlessTurnOutcome(err), headlessExitCode(headlessTurnOutcome(err), false, false),
		final, provider.Usage{PromptTokens: 10, CompletionTokens: 2, CachedTokens: 8}, nil)

	// The prompt and the system message are what the run opened on, not
	// something it did; the stream carries the turn.
	want := jsonMessages(a.Messages())[2:]
	got := replayJSONL(t, lines.String())
	if !reflect.DeepEqual(got, want) {
		t.Errorf("replayed conversation\n got %+v\nwant %+v", got, want)
	}

	// Every code field on the stream is one of the record's own words, and
	// the close line is the last of them.
	var kinds []string
	var closing jsonEvent
	for _, line := range strings.Split(strings.TrimSpace(lines.String()), "\n") {
		var ev jsonEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("stream line is not JSON: %v", err)
		}
		kinds = append(kinds, ev.Kind)
		closing = ev
	}
	wantKinds := []string{observe.EventText, observe.EventText, observe.EventUsage,
		observe.EventToolCall, observe.EventToolResult, observe.EventText, observe.EventClose}
	if strings.Join(kinds, ",") != strings.Join(wantKinds, ",") {
		t.Errorf("event kinds = %v, want %v", kinds, wantKinds)
	}
	if closing.Outcome != observe.TurnDone || closing.Exit == nil || *closing.Exit != exitDone {
		t.Errorf("close line = %+v, want the record's outcome and the exit code beside it", closing)
	}
	if closing.Usage == nil || closing.Usage.CachedTokens != 8 {
		t.Errorf("close usage = %+v, want the cached share stated", closing.Usage)
	}
}

// A run that asked for no stream writes none, and every call on the way there
// is a clean no-op rather than a nil dereference on the surface with nobody
// watching it happen.
func TestJSONLStreamIsAQuietNoOpWhenNobodyAskedForOne(t *testing.T) {
	obs := headlessObserver{rounds: func() int { return 1 }}
	obs.text("hello")
	obs.call(provider.ToolCall{ID: "c1", Name: "read_file"})
	obs.toolResult(toolResultOf("read_file", time.Millisecond, "contents"))
	obs.decision(observe.DecisionDeny, observe.ReasonHeadlessDefault)
	obs.usage(provider.Usage{PromptTokens: 1})
	obs.signal(observe.SignalRetry, "overloaded")
	obs.stream.closed(obs.pos(), observe.TurnDone, exitDone, "done", provider.Usage{}, nil)
}

// treeRepo is a checkout with one commit and a clean tree. Config is pinned
// so a developer's own hooks and identity never reach the test.
func treeRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	ws := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", ws}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(ws, "a.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-q", "-m", "init")
	return ws
}

// The block that tells an unattended turn its tree moved ends by naming the
// likeliest author, because it takes the session's own reading rather than
// building a second one that knows less.
func TestHeadlessTree_CarriesTheSiblingClause(t *testing.T) {
	ws := treeRepo(t)
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	// Another running session in this checkout. The parent is the one such
	// process a test can name portably.
	other, err := db.StartAgentSession("code", "openai", "gpt-test")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := db.StampAgentSession(other, storage.AgentProvenance{
		Project: fingerprint(projectFingerprintRoot())}); err != nil {
		t.Fatalf("stamp: %v", err)
	}
	if _, err := db.SQL().Exec(
		`UPDATE agent_sessions SET pid = ? WHERE id = ?`, os.Getppid(), other); err != nil {
		t.Fatalf("place: %v", err)
	}

	own := &writtenByCalls{}
	c := headlessTree(config.Config{}, readSibling(db), own)
	if c == nil {
		t.Fatal("the reading is on by default and the run got none")
	}
	if c.Own == nil {
		t.Fatal("the run's own writes are the subtrahend and were not handed over")
	}
	// Only the directory is the test's: everything else is what the run
	// itself would have been given.
	c.Dir = ws

	a := agent.New(nil, nil)
	a.SetTreeCheck(*c)
	if err := os.WriteFile(filepath.Join(ws, "b.txt"), []byte("somebody else\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	n, ok := a.NextTreeNotice(false)
	if !ok {
		t.Fatal("a changed tree told the run nothing")
	}
	if !strings.Contains(n.Message, "another session is open in this checkout") {
		t.Fatalf("the block did not name the likeliest author:\n%s", n.Message)
	}
}

// A reading the config turned off stays off, subtrahend and all.
func TestHeadlessTree_StaysOffWhereTheConfigSaysSo(t *testing.T) {
	off := false
	cfg := config.Config{}
	cfg.Behavior.TreeCheck = &off
	if c := headlessTree(cfg, readSibling(nil), &writtenByCalls{}); c != nil {
		t.Fatalf("got %+v, want no reading at all", c)
	}
}

// The handler is the run's, and the run's alone. A signal that arrives after
// it comes down has to reach whatever was watching the signal before the run
// — the default disposition in a real process — because that is what kills a
// run somebody has told twice.
//
// The test keeps a registration of its own for the whole of it, so the
// signals it sends itself are delivered to a channel rather than ending the
// test binary, and so the handler going down cannot take the process with it.
func TestInterruptOnSignal_TheHandlerLivesForTheRunOnly(t *testing.T) {
	held := make(chan os.Signal, 2)
	signal.Notify(held, os.Interrupt)
	defer signal.Stop(held)

	interrupted := make(chan struct{}, 2)
	stop := interruptOnSignal(func() { interrupted <- struct{}{} })
	raise(t, os.Interrupt)
	select {
	case <-interrupted:
	case <-time.After(10 * time.Second):
		t.Fatal("a signal during the run never reached the turn's interrupt")
	}
	awaitSignal(t, held)

	// Once the teardown has returned there is no watcher left, so the second
	// signal below cannot reach the turn however long anything waits for it.
	// Waiting for the test's own channel is what says the signal was
	// delivered at all, which is what makes the silence below mean something.
	stop()
	raise(t, os.Interrupt)
	awaitSignal(t, held)
	select {
	case <-interrupted:
		t.Fatal("a signal after the run interrupted a turn that had already ended")
	default:
	}
}

// awaitSignal waits for the test's own registration to be handed the signal
// the test sent itself.
func awaitSignal(t *testing.T, held <-chan os.Signal) {
	t.Helper()
	select {
	case <-held:
	case <-time.After(10 * time.Second):
		t.Fatal("the signal the test sent itself was never delivered")
	}
}

// raise sends the test process a signal, which is the only way to exercise a
// handler: a channel written to by hand would test the goroutine and not the
// registration that feeds it.
func raise(t *testing.T, sig os.Signal) {
	t.Helper()
	p, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatalf("finding this process to signal it: %v", err)
	}
	if err := p.Signal(sig); err != nil {
		t.Fatalf("signalling this process: %v", err)
	}
}
