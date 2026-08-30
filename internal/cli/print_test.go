package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"net/http"
	"net/http/httptest"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/process"
	"github.com/rfizzle/shhh/internal/provider"
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
	if err := writeJSONTranscript(&sb, msgs, "done", provider.Usage{PromptTokens: 10, CompletionTokens: 5}, nil); err != nil {
		t.Fatalf("writeJSONTranscript: %v", err)
	}

	var got jsonTranscript
	if err := json.Unmarshal([]byte(sb.String()), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if !got.Success || got.Final != "done" || got.Error != "" {
		t.Fatalf("unexpected outcome fields: %+v", got)
	}
	if got.Usage.PromptTokens != 10 || got.Usage.CompletionTokens != 5 {
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
