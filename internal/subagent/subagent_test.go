package subagent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/tools"
)

// streamStep is one scripted provider response: assistant text and/or tool
// calls, with optional usage.
type streamStep struct {
	text  string
	calls []provider.ToolCall
	usage *provider.Usage
	// fail, when set, is returned instead of a stream, so a test can put a
	// child in front of a provider that never answered.
	fail *provider.Failure
}

// scriptedEnv builds an EnvFactory whose children replay steps in order. The
// stream respects the child context, so cancellation behaves like a real
// provider stream.
type scriptedEnv struct {
	mu    sync.Mutex
	steps []streamStep

	gated      map[string]bool
	execOut    string
	execCode   int
	ranCommand atomic.Bool
}

func (s *scriptedEnv) factory() EnvFactory {
	return func(ctx context.Context, spec Spec) (Env, error) {
		stream := func(msgs []provider.Message, _ string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
			if ctx.Err() != nil {
				return nil, nil, ctx.Err()
			}
			s.mu.Lock()
			if len(s.steps) == 0 {
				s.mu.Unlock()
				return nil, nil, errors.New("scripted stream exhausted")
			}
			step := s.steps[0]
			s.steps = s.steps[1:]
			s.mu.Unlock()

			if step.fail != nil {
				return nil, nil, step.fail
			}
			ch := make(chan provider.StreamEvent, 3)
			if step.text != "" {
				ch <- provider.StreamEvent{Token: step.text}
			}
			if len(step.calls) > 0 {
				ch <- provider.StreamEvent{ToolCalls: step.calls, Usage: step.usage}
			} else {
				ch <- provider.StreamEvent{Done: true, Usage: step.usage}
			}
			close(ch)
			_, cancel := context.WithCancel(context.Background())
			return ch, cancel, nil
		}
		return Env{
			SystemPrompt: "test system prompt",
			Stream:       stream,
			Executor: func(name string, args json.RawMessage) (string, error) {
				return "auto:" + name, nil
			},
			ExecuteGated: func(name string, args json.RawMessage) (string, error) {
				return "gated:" + name, nil
			},
			RunCommand: func(ctx context.Context, command string) (string, int) {
				s.ranCommand.Store(true)
				return s.execOut, s.execCode
			},
			Gated: s.gated,
		}, nil
	}
}

func newTestSupervisor(t *testing.T, env *scriptedEnv) *Supervisor {
	t.Helper()
	sup := New(context.Background(), Options{Root: t.TempDir(), NewEnv: env.factory()})
	t.Cleanup(sup.Close)
	return sup
}

func execTool(t *testing.T, sup *Supervisor, name, args string) string {
	t.Helper()
	exec := sup.WrapExecutor(func(string, json.RawMessage) (string, error) {
		return "", errors.New("unexpected passthrough")
	})
	out, err := exec(name, json.RawMessage(args))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return out
}

// nextAsk drains events until an approval request arrives.
func nextAsk(t *testing.T, sup *Supervisor) *Ask {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-sup.Events():
			if ev.Kind == EventAsk {
				return ev.Ask
			}
		case <-deadline:
			t.Fatal("no approval request arrived")
		}
	}
}

func TestSpawnAndReport(t *testing.T) {
	env := &scriptedEnv{steps: []streamStep{{text: "my findings", usage: &provider.Usage{PromptTokens: 10, CompletionTokens: 5}}}}
	sup := newTestSupervisor(t, env)

	out := execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"survey the code"}`)
	if !strings.Contains(out, "Spawned researcher-1") {
		t.Fatalf("unexpected spawn result: %s", out)
	}

	report := execTool(t, sup, ReportToolName, `{"name":"researcher-1"}`)
	if !strings.Contains(report, "my findings") {
		t.Fatalf("report missing findings: %s", report)
	}
	if !strings.Contains(report, "done") {
		t.Fatalf("report missing status: %s", report)
	}

	overview := execTool(t, sup, ReportToolName, `{}`)
	if !strings.Contains(overview, "researcher-1") || !strings.Contains(overview, "survey the code") {
		t.Fatalf("unexpected overview: %s", overview)
	}
}

func TestReportUnknownAgent(t *testing.T) {
	sup := newTestSupervisor(t, &scriptedEnv{})
	exec := sup.WrapExecutor(nil)
	if _, err := exec(ReportToolName, json.RawMessage(`{"name":"ghost"}`)); err == nil {
		t.Fatal("expected an error for an unknown agent")
	}
}

func TestSpawnValidation(t *testing.T) {
	sup := newTestSupervisor(t, &scriptedEnv{})
	exec := sup.WrapExecutor(nil)
	if _, err := exec(SpawnToolName, json.RawMessage(`{"role":"admin","task":"x"}`)); err == nil {
		t.Fatal("expected an error for an unknown role")
	}
	if _, err := exec(SpawnToolName, json.RawMessage(`{"role":"researcher","task":"  "}`)); err == nil {
		t.Fatal("expected an error for an empty task")
	}
	if _, err := exec(SpawnToolName, json.RawMessage(`{"role":"researcher","task":"x","name":"bad name!"}`)); err == nil {
		t.Fatal("expected an error for an invalid name")
	}
}

// gatedCommandSteps scripts one gated command round followed by a final
// message.
func gatedCommandSteps(command string) []streamStep {
	return []streamStep{
		{calls: []provider.ToolCall{{ID: "c1", Name: tools.ExecCommandName, Arguments: `{"command":"` + command + `"}`}}},
		{text: "task complete"},
	}
}

func TestApprovalRoutingApprove(t *testing.T) {
	env := &scriptedEnv{
		steps:    gatedCommandSteps("echo hi"),
		gated:    map[string]bool{tools.ExecCommandName: true},
		execOut:  "hi",
		execCode: 0,
	}
	sup := newTestSupervisor(t, env)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"run something"}`)

	ask := nextAsk(t, sup)
	if ask.Kind != AskCommand || ask.Agent != "researcher-1" {
		t.Fatalf("unexpected ask: %+v", ask)
	}
	if !strings.Contains(ask.Title, "echo hi") {
		t.Fatalf("ask title missing command: %s", ask.Title)
	}
	ask.Respond(true)

	report := execTool(t, sup, ReportToolName, `{"name":"researcher-1"}`)
	if !strings.Contains(report, "task complete") {
		t.Fatalf("report missing final text: %s", report)
	}
	if !env.ranCommand.Load() {
		t.Fatal("approved command never ran")
	}
}

func TestApprovalRoutingDecline(t *testing.T) {
	env := &scriptedEnv{
		steps: gatedCommandSteps("echo hi"),
		gated: map[string]bool{tools.ExecCommandName: true},
	}
	sup := newTestSupervisor(t, env)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"run something"}`)

	nextAsk(t, sup).Respond(false)

	report := execTool(t, sup, ReportToolName, `{"name":"researcher-1"}`)
	if !strings.Contains(report, "task complete") {
		t.Fatalf("child should continue after a decline: %s", report)
	}
	if env.ranCommand.Load() {
		t.Fatal("declined command must not run")
	}
}

func TestPlanCeilingDeniesWithoutAsking(t *testing.T) {
	env := &scriptedEnv{
		steps: gatedCommandSteps("make build"),
		gated: map[string]bool{tools.ExecCommandName: true},
	}
	sup := newTestSupervisor(t, env)
	sup.SetParentMode(agent.ModePlan)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"try to build"}`)

	report := execTool(t, sup, ReportToolName, `{"name":"researcher-1"}`)
	if !strings.Contains(report, "task complete") {
		t.Fatalf("unexpected report: %s", report)
	}
	if env.ranCommand.Load() {
		t.Fatal("plan-mode ceiling must refuse the command outright")
	}
	// The refusal never routed to the user.
	for {
		select {
		case ev := <-sup.Events():
			if ev.Kind == EventAsk {
				t.Fatal("plan-mode denial must not ask the user")
			}
			continue
		default:
		}
		break
	}
}

func TestAutoModeCeilingAllowsWithoutAsking(t *testing.T) {
	env := &scriptedEnv{
		steps:   gatedCommandSteps("echo hi"),
		gated:   map[string]bool{tools.ExecCommandName: true},
		execOut: "hi",
	}
	sup := New(context.Background(), Options{
		Root:             t.TempDir(),
		NewEnv:           env.factory(),
		CommandAllowlist: []string{"echo"},
	})
	t.Cleanup(sup.Close)
	sup.SetParentMode(agent.ModeAuto)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"run something"}`)

	report := execTool(t, sup, ReportToolName, `{"name":"researcher-1"}`)
	if !strings.Contains(report, "task complete") {
		t.Fatalf("unexpected report: %s", report)
	}
	if !env.ranCommand.Load() {
		t.Fatal("allowlisted command should run without asking")
	}
}

func TestTokenBudgetCancelsChild(t *testing.T) {
	env := &scriptedEnv{
		steps: []streamStep{
			{
				calls: []provider.ToolCall{{ID: "r1", Name: "read_file", Arguments: `{"path":"x"}`}},
				usage: &provider.Usage{PromptTokens: 5000, CompletionTokens: 100},
			},
		},
	}
	sup := newTestSupervisor(t, env)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"read a lot","max_tokens":1000}`)

	// Nothing is scripted past the overrun, so the handoff fails too.
	// A handoff that cannot be produced must leave the real reason standing
	// rather than replacing it with whatever went wrong second.
	report := execTool(t, sup, ReportToolName, `{"name":"researcher-1"}`)
	if !strings.Contains(report, "token budget") {
		t.Fatalf("expected a token-budget failure, got: %s", report)
	}
	if !strings.Contains(report, "no final report was produced") {
		t.Fatalf("a child that could not hand off must say so: %s", report)
	}
}

// TestTokenBudgetHandsOffBeforeItStops: the budget is still a hard
// stop, but the child says where it got to on the way out, so the parent has
// something to act on rather than a spend figure.
func TestTokenBudgetHandsOffBeforeItStops(t *testing.T) {
	env := &scriptedEnv{
		steps: []streamStep{
			{
				calls: []provider.ToolCall{{ID: "r1", Name: "read_file", Arguments: `{"path":"x"}`}},
				usage: &provider.Usage{PromptTokens: 5000, CompletionTokens: 100},
			},
			{text: "got as far as the parser; the lexer is untouched"},
		},
	}
	sup := newTestSupervisor(t, env)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"read a lot","max_tokens":1000}`)

	report := execTool(t, sup, ReportToolName, `{"name":"researcher-1"}`)
	if !strings.Contains(report, "token budget") {
		t.Fatalf("the budget must still stop the child: %s", report)
	}
	if !strings.Contains(report, "got as far as the parser") {
		t.Fatalf("the handoff must reach the parent: %s", report)
	}
}

// TestTokenBudgetOnTheFinalResponseKeepsTheReport is the other half:
// addUsage measures after the fact, so a child can finish its turn and only
// then be found to have overspent. That child did the work and the session
// paid for it, so it must stop for the budget with its own report in hand —
// not as a "cancelled" agent that produced nothing, which is what a killed
// one is.
func TestTokenBudgetOnTheFinalResponseKeepsTheReport(t *testing.T) {
	env := &scriptedEnv{
		steps: []streamStep{
			{
				text:  "the parser is the bottleneck; here is what to change",
				usage: &provider.Usage{PromptTokens: 5000, CompletionTokens: 100},
			},
		},
	}
	sup := newTestSupervisor(t, env)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"find the bottleneck","max_tokens":1000}`)

	report := execTool(t, sup, ReportToolName, `{"name":"researcher-1"}`)
	if !strings.Contains(report, "token budget") {
		t.Fatalf("the budget is what stopped it, and is what it must say: %s", report)
	}
	if strings.Contains(report, "cancelled") {
		t.Fatalf("nobody cancelled this agent; it finished and overspent: %s", report)
	}
	if !strings.Contains(report, "the parser is the bottleneck") {
		t.Fatalf("the finished report must reach the parent: %s", report)
	}
}

// TestRoundLimitChecksInAndCarriesOn is the heart of it: the round limit
// is a checkpoint, not a failure. The child takes stock and keeps going on
// the same conversation, and the budget grows so the next stop is further
// away than the last.
func TestRoundLimitChecksInAndCarriesOn(t *testing.T) {
	env := &scriptedEnv{
		steps: []streamStep{
			{calls: []provider.ToolCall{{ID: "r1", Name: "read_file", Arguments: `{"path":"a"}`}}},
			{calls: []provider.ToolCall{{ID: "r2", Name: "read_file", Arguments: `{"path":"b"}`}}},
			{text: "finished after taking stock"},
		},
	}
	sup := newTestSupervisor(t, env)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"a long job","max_rounds":1}`)

	report := execTool(t, sup, ReportToolName, `{"name":"researcher-1"}`)
	if !strings.Contains(report, "finished after taking stock") {
		t.Fatalf("a child at its round limit must carry on: %s", report)
	}
	if strings.Contains(report, "round limit") {
		t.Fatalf("the round limit must not fail the child: %s", report)
	}

	var st Status
	for _, s := range sup.Snapshot() {
		if s.Name == "researcher-1" {
			st = s
		}
	}
	if st.CheckIns != 1 {
		t.Fatalf("expected exactly one check-in, got %d", st.CheckIns)
	}
	// The second turn ran two rounds against a budget of one, which it could
	// only do because the check-in doubled it.
	if st.ToolCalls != 2 {
		t.Fatalf("expected both tool calls to run, got %d", st.ToolCalls)
	}
}

// TestSpawnDefaultsToNoRoundLimit: an ordinary child runs to completion
// without pausing, and the surfaces that price a spawn say so rather
// than printing a negative number.
func TestSpawnDefaultsToNoRoundLimit(t *testing.T) {
	args, err := parseSpawnArgs(nil, json.RawMessage(`{"role":"researcher","task":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if args.maxRounds > 0 {
		t.Fatalf("the default spawn must be unbounded, got %d", args.maxRounds)
	}
	summary, err := SpawnSummary(nil, json.RawMessage(`{"role":"researcher","task":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(summary, "no round limit") {
		t.Fatalf("the approval preview must say the child is unbounded: %s", summary)
	}
	summary, err = SpawnSummary(nil, json.RawMessage(`{"role":"researcher","task":"x","max_rounds":30}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(summary, "checks in every 30 rounds") {
		t.Fatalf("a named interval must read as a rhythm, not a ceiling: %s", summary)
	}
}

func TestCancelAllUnblocksAsks(t *testing.T) {
	env := &scriptedEnv{
		steps: gatedCommandSteps("echo hi"),
		gated: map[string]bool{tools.ExecCommandName: true},
	}
	sup := newTestSupervisor(t, env)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"run something"}`)

	_ = nextAsk(t, sup) // child is now blocked waiting on the user
	sup.CancelAll()

	report := execTool(t, sup, ReportToolName, `{"name":"researcher-1"}`)
	if !strings.Contains(report, "cancelled") {
		t.Fatalf("expected a cancelled child, got: %s", report)
	}
	if env.ranCommand.Load() {
		t.Fatal("cancelled command must not run")
	}
}

func TestSpawnSummary(t *testing.T) {
	s, err := SpawnSummary(nil, json.RawMessage(`{"role":"writer","task":"refactor the loop"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s, "writer") || !strings.Contains(s, "refactor the loop") {
		t.Fatalf("unexpected summary: %s", s)
	}
	if _, err := SpawnSummary(nil, json.RawMessage(`{"role":"nope","task":"x"}`)); err == nil {
		t.Fatal("expected an error for an invalid role")
	}
}

func TestParseSpawnArgsClampsBudgets(t *testing.T) {
	args, err := parseSpawnArgs(nil, json.RawMessage(`{"role":"researcher","task":"x","max_rounds":999,"max_tokens":99999999}`))
	if err != nil {
		t.Fatal(err)
	}
	// The token budget is a ceiling and clamps; the check-in interval is not
	// one and is honoured as asked.
	if args.maxRounds != 999 {
		t.Fatalf("max_rounds should be taken as given: %d", args.maxRounds)
	}
	if args.maxTokens != MaxTokensCeiling {
		t.Fatalf("max_tokens not clamped: %d", args.maxTokens)
	}
	args, err = parseSpawnArgs(nil, json.RawMessage(`{"role":"researcher","task":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if args.maxRounds != DefaultMaxRounds || args.maxTokens != DefaultMaxTokens {
		t.Fatalf("defaults not applied: %d %d", args.maxRounds, args.maxTokens)
	}
}

// waitState polls until the named child reaches the wanted state.
func waitState(t *testing.T, sup *Supervisor, name string, want State) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if st, ok := sup.Get(name); ok && st.State == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	st, _ := sup.Get(name)
	t.Fatalf("agent %s never reached %s (last: %s)", name, want, st.State)
}

// resumableEnv blocks the first stream until cancelled (respecting the
// per-request cancel func, like a real provider), then serves scripted final
// responses.
func resumableEnv(finals ...string) EnvFactory {
	var mu sync.Mutex
	first := true
	return func(ctx context.Context, spec Spec) (Env, error) {
		stream := func(msgs []provider.Message, _ string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
			mu.Lock()
			if first {
				first = false
				mu.Unlock()
				ch := make(chan provider.StreamEvent)
				sctx, cancel := context.WithCancel(ctx)
				go func() {
					<-sctx.Done()
					close(ch)
				}()
				return ch, cancel, nil
			}
			var text string
			if len(finals) > 0 {
				text = finals[0]
				finals = finals[1:]
			}
			mu.Unlock()
			ch := make(chan provider.StreamEvent, 2)
			ch <- provider.StreamEvent{Token: text}
			ch <- provider.StreamEvent{Done: true}
			close(ch)
			_, cancel := context.WithCancel(context.Background())
			return ch, cancel, nil
		}
		return Env{SystemPrompt: "sys", Stream: stream}, nil
	}
}

func transcriptHas(entries []TranscriptEntry, kind EntryKind, substr string) bool {
	for _, e := range entries {
		if e.Kind != kind {
			continue
		}
		if strings.Contains(e.Text, substr) || strings.Contains(e.Result, substr) || strings.Contains(e.Tool, substr) {
			return true
		}
	}
	return false
}

func TestCancelTurnIdleThenSteerResumes(t *testing.T) {
	sup := New(context.Background(), Options{Root: t.TempDir(), NewEnv: resumableEnv("resumed and finished")})
	t.Cleanup(sup.Close)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"long survey"}`)

	waitState(t, sup, "researcher-1", StateRunning)
	if err := sup.CancelTurn("researcher-1"); err != nil {
		t.Fatal(err)
	}
	waitState(t, sup, "researcher-1", StateIdle)
	if err := sup.CancelTurn("researcher-1"); err == nil {
		t.Fatal("cancelling an idle turn must error")
	}

	if err := sup.Steer("researcher-1", "continue please"); err != nil {
		t.Fatal(err)
	}
	report := execTool(t, sup, ReportToolName, `{"name":"researcher-1"}`)
	if !strings.Contains(report, "resumed and finished") {
		t.Fatalf("steering after a cancelled turn should resume, got: %s", report)
	}

	entries := sup.Transcript("researcher-1")
	if !transcriptHas(entries, EntryUser, "long survey") {
		t.Fatal("transcript missing the task entry")
	}
	if !transcriptHas(entries, EntrySystem, "Turn cancelled") {
		t.Fatal("transcript missing the cancellation note")
	}
	if !transcriptHas(entries, EntryUser, "continue please") {
		t.Fatal("transcript missing the steering entry")
	}
	if !transcriptHas(entries, EntryAssistant, "resumed and finished") {
		t.Fatal("transcript missing the final assistant entry")
	}

	if err := sup.Steer("researcher-1", "too late"); err == nil {
		t.Fatal("steering a finished agent must error")
	}
}

func TestKillFailsChildAndKeepsTranscript(t *testing.T) {
	sup := New(context.Background(), Options{Root: t.TempDir(), NewEnv: resumableEnv()})
	t.Cleanup(sup.Close)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"long survey"}`)

	waitState(t, sup, "researcher-1", StateRunning)
	if err := sup.Kill("researcher-1"); err != nil {
		t.Fatal(err)
	}
	waitState(t, sup, "researcher-1", StateFailed)
	if st, _ := sup.Get("researcher-1"); st.Detail != "cancelled" {
		t.Fatalf("unexpected detail: %s", st.Detail)
	}
	if !transcriptHas(sup.Transcript("researcher-1"), EntrySystem, "Killed by the user") {
		t.Fatal("transcript missing the kill note")
	}
	if err := sup.Kill("researcher-1"); err == nil {
		t.Fatal("killing a finished agent must error")
	}
}

func TestTranscriptRecordsToolRounds(t *testing.T) {
	env := &scriptedEnv{steps: []streamStep{
		{text: "let me look", calls: []provider.ToolCall{{ID: "r1", Name: "read_file", Arguments: `{"path":"x"}`}}},
		{text: "all done"},
	}}
	sup := newTestSupervisor(t, env)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"survey"}`)
	execTool(t, sup, ReportToolName, `{"name":"researcher-1"}`)

	entries := sup.Transcript("researcher-1")
	if !transcriptHas(entries, EntryUser, "survey") {
		t.Fatal("missing task entry")
	}
	if !transcriptHas(entries, EntryAssistant, "let me look") {
		t.Fatal("missing per-round assistant text")
	}
	if !transcriptHas(entries, EntryTool, "auto:read_file") {
		t.Fatal("missing settled tool entry")
	}
	for _, e := range entries {
		if e.Kind == EntryTool && e.Pending {
			t.Fatal("tool entry left pending after its result")
		}
	}
	if !transcriptHas(entries, EntryAssistant, "all done") {
		t.Fatal("missing final assistant entry")
	}
	if sup.StreamingText("researcher-1") != "" {
		t.Fatal("streaming text must be flushed at completion")
	}
}

func TestSetAgentModeClampedToCeiling(t *testing.T) {
	env := &scriptedEnv{steps: []streamStep{{text: "done"}}}
	sup := newTestSupervisor(t, env) // parent ceiling defaults to manual
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"x"}`)

	eff, err := sup.SetAgentMode("researcher-1", agent.ModeAuto)
	if err != nil {
		t.Fatal(err)
	}
	if eff != agent.ModeManual {
		t.Fatalf("mode not clamped to the manual ceiling, got %s", eff)
	}
	sup.SetParentMode(agent.ModeAuto)
	eff, err = sup.SetAgentMode("researcher-1", agent.ModeAcceptEdits)
	if err != nil {
		t.Fatal(err)
	}
	if eff != agent.ModeAcceptEdits {
		t.Fatalf("mode under the ceiling must stick, got %s", eff)
	}
	if got, ok := sup.AgentMode("researcher-1"); !ok || got != agent.ModeAcceptEdits {
		t.Fatalf("AgentMode = %s, %v", got, ok)
	}
}

func TestNoteQueuedSteeringAndWorktreeDiff(t *testing.T) {
	sup := New(context.Background(), Options{Root: t.TempDir(), NewEnv: resumableEnv()})
	t.Cleanup(sup.Close)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"x"}`)
	waitState(t, sup, "researcher-1", StateRunning)

	if err := sup.Note("researcher-1", TranscriptEntry{Kind: EntrySystem, Text: "a scoped note"}); err != nil {
		t.Fatal(err)
	}
	if !transcriptHas(sup.Transcript("researcher-1"), EntrySystem, "a scoped note") {
		t.Fatal("note not appended")
	}
	if err := sup.Note("ghost", TranscriptEntry{}); err == nil {
		t.Fatal("noting an unknown agent must error")
	}

	if err := sup.Steer("researcher-1", "queued mid-turn"); err != nil {
		t.Fatal(err)
	}
	if n := sup.QueuedSteering("researcher-1"); n != 1 {
		t.Fatalf("QueuedSteering = %d, want 1", n)
	}

	if _, err := sup.WorktreeDiff("researcher-1"); err == nil {
		t.Fatal("researchers have no isolated workspace to diff")
	}

	if p, ok := sup.Parent("researcher-1"); !ok || p != "" {
		t.Fatalf("Parent = %q, %v; want orchestrator", p, ok)
	}
}

func TestSteerDuringFinalStreamStartsNextTurn(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var turnCount atomic.Int32
	factory := func(ctx context.Context, spec Spec) (Env, error) {
		stream := func(msgs []provider.Message, _ string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
			n := turnCount.Add(1)
			ch := make(chan provider.StreamEvent, 2)
			if n == 1 {
				go func() {
					close(started)
					select {
					case <-release:
					case <-ctx.Done():
						close(ch)
						return
					}
					ch <- provider.StreamEvent{Token: "first done"}
					ch <- provider.StreamEvent{Done: true}
					close(ch)
				}()
			} else {
				ch <- provider.StreamEvent{Token: "second done"}
				ch <- provider.StreamEvent{Done: true}
				close(ch)
			}
			_, cancel := context.WithCancel(context.Background())
			return ch, cancel, nil
		}
		return Env{SystemPrompt: "sys", Stream: stream}, nil
	}
	sup := New(context.Background(), Options{Root: t.TempDir(), NewEnv: factory})
	t.Cleanup(sup.Close)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"x"}`)

	<-started
	if err := sup.Steer("researcher-1", "one more thing"); err != nil {
		t.Fatal(err)
	}
	close(release)

	report := execTool(t, sup, ReportToolName, `{"name":"researcher-1"}`)
	if !strings.Contains(report, "second done") {
		t.Fatalf("steering during the final stream must start a fresh turn, got: %s", report)
	}
	entries := sup.Transcript("researcher-1")
	if !transcriptHas(entries, EntryAssistant, "first done") || !transcriptHas(entries, EntryAssistant, "second done") {
		t.Fatal("transcript missing one of the turns' assistant entries")
	}
	if !transcriptHas(entries, EntryUser, "one more thing") {
		t.Fatal("transcript missing the steering entry")
	}
}

// spawnRaw calls spawn_agent and returns its error instead of failing.
func spawnRaw(sup *Supervisor, args string) (string, error) {
	exec := sup.WrapExecutor(func(string, json.RawMessage) (string, error) {
		return "", errors.New("unexpected passthrough")
	})
	return exec(SpawnToolName, json.RawMessage(args))
}

// verdictProvider answers every classifier request with a scripted decision.
type verdictProvider struct {
	decision string
	reason   string
	calls    atomic.Int32
}

func (p *verdictProvider) StreamCompletion(ctx context.Context, msgs []provider.Message, opts provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
	p.calls.Add(1)
	ch := make(chan provider.StreamEvent, 1)
	ch <- provider.StreamEvent{
		ToolCalls: []provider.ToolCall{{
			ID:        "d1",
			Name:      agent.DecisionToolName,
			Arguments: `{"decision":"` + p.decision + `","reason":"` + p.reason + `"}`,
		}},
		Done: true,
	}
	close(ch)
	return ch, nil
}

func (p *verdictProvider) Name() string { return "verdict" }

// TestChildReadOnlyCommandNeverAsks: inspection commands auto-run for a child
// in the strictest prompting mode, exactly as they do for the parent.
func TestChildReadOnlyCommandNeverAsks(t *testing.T) {
	env := &scriptedEnv{
		steps:   gatedCommandSteps("git status"),
		gated:   map[string]bool{tools.ExecCommandName: true},
		execOut: "clean",
	}
	sup := newTestSupervisor(t, env)
	sup.SetParentMode(agent.ModeManual)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"check the tree"}`)

	report := execTool(t, sup, ReportToolName, `{"name":"researcher-1"}`)
	if !strings.Contains(report, "task complete") {
		t.Fatalf("unexpected report: %s", report)
	}
	if !env.ranCommand.Load() {
		t.Fatal("a read-only command should run without asking the user")
	}
}

// TestChildAutoModeUsesClassifier: in auto mode a child's unlisted command is
// judged by the same classifier the parent uses, instead of prompting.
func TestChildAutoModeUsesClassifier(t *testing.T) {
	env := &scriptedEnv{
		steps:   gatedCommandSteps("go test ./..."),
		gated:   map[string]bool{tools.ExecCommandName: true},
		execOut: "PASS",
	}
	judge := &verdictProvider{decision: "allow", reason: "runs the requested tests"}
	sup := New(context.Background(), Options{
		Root:       t.TempDir(),
		NewEnv:     env.factory(),
		Classifier: agent.NewClassifier(judge, agent.ClassifierConfig{Model: "judge"}),
	})
	t.Cleanup(sup.Close)
	sup.SetParentMode(agent.ModeAuto)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"run the tests"}`)

	report := execTool(t, sup, ReportToolName, `{"name":"researcher-1"}`)
	if !strings.Contains(report, "task complete") {
		t.Fatalf("unexpected report: %s", report)
	}
	if !env.ranCommand.Load() {
		t.Fatal("the classifier approved the command; it should have run")
	}
	if judge.calls.Load() == 0 {
		t.Fatal("the child should consult the classifier in auto mode")
	}
	var noted bool
	for _, e := range sup.Transcript("researcher-1") {
		if e.Kind == EntrySystem && strings.Contains(e.Text, "Auto-approved (classifier") {
			noted = true
		}
	}
	if !noted {
		t.Fatal("the child transcript should record the classifier approval")
	}
}

// TestChildClassifierDenyRefusesWithoutAsking: a denial comes back as a tool
// error, never as a prompt.
func TestChildClassifierDenyRefusesWithoutAsking(t *testing.T) {
	env := &scriptedEnv{
		steps: gatedCommandSteps("go install ./cmd/tool"),
		gated: map[string]bool{tools.ExecCommandName: true},
	}
	judge := &verdictProvider{decision: "deny", reason: "installing tools was not requested"}
	sup := New(context.Background(), Options{
		Root:       t.TempDir(),
		NewEnv:     env.factory(),
		Classifier: agent.NewClassifier(judge, agent.ClassifierConfig{Model: "judge"}),
	})
	t.Cleanup(sup.Close)
	sup.SetParentMode(agent.ModeAuto)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"install something"}`)

	execTool(t, sup, ReportToolName, `{"name":"researcher-1"}`)
	if env.ranCommand.Load() {
		t.Fatal("a denied command must not run")
	}
}

// TestParentGrantsReachChildren: a session grant ([a]) the user gave the
// parent is not re-asked once per child.
func TestParentGrantsReachChildren(t *testing.T) {
	env := &scriptedEnv{
		steps:   gatedCommandSteps("go test ./..."),
		gated:   map[string]bool{tools.ExecCommandName: true},
		execOut: "PASS",
	}
	sup := newTestSupervisor(t, env)
	sup.SetParentMode(agent.ModeManual)
	sup.SetParentGrants(agent.Grants{AllCommands: true})
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"run the tests"}`)

	execTool(t, sup, ReportToolName, `{"name":"researcher-1"}`)
	if !env.ranCommand.Load() {
		t.Fatal("a session command grant should carry into children")
	}
}

// TestChildModelResolution: ModelFor picks the child's model, a spawn
// argument overrides it, and the choice reaches both the Env and the roster.
func TestChildModelResolution(t *testing.T) {
	env := &scriptedEnv{steps: []streamStep{{text: "done"}, {text: "done"}}}
	var seen []string
	var mu sync.Mutex
	base := env.factory()
	sup := New(context.Background(), Options{
		Root: t.TempDir(),
		NewEnv: func(ctx context.Context, spec Spec) (Env, error) {
			mu.Lock()
			seen = append(seen, spec.Model)
			mu.Unlock()
			return base(ctx, spec)
		},
		ModelFor: func(role Role, requested string) string {
			if requested != "" {
				return requested
			}
			return "role-default"
		},
	})
	t.Cleanup(sup.Close)

	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"a"}`)
	out := execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"b","model":"tiny-model"}`)
	if !strings.Contains(out, "tiny-model") {
		t.Fatalf("the spawn result should name the model: %s", out)
	}
	execTool(t, sup, ReportToolName, `{"name":"researcher-1"}`)
	execTool(t, sup, ReportToolName, `{"name":"researcher-2"}`)

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 || seen[0] != "role-default" || seen[1] != "tiny-model" {
		t.Fatalf("models handed to the env factory = %v, want [role-default tiny-model]", seen)
	}
	if st, ok := sup.Get("researcher-2"); !ok || st.Model != "tiny-model" {
		t.Fatalf("roster should record the child's model, got %+v", st)
	}
}

// TestWriterPathClaimsConflict: two live writers cannot claim overlapping
// paths, so their patches cannot collide.
func TestWriterPathClaimsConflict(t *testing.T) {
	repo := initTestRepo(t)
	env := &scriptedEnv{steps: []streamStep{{text: "done"}, {text: "done"}, {text: "done"}}}
	sup := New(context.Background(), Options{Root: repo, NewEnv: env.factory()})
	t.Cleanup(sup.Close)

	if _, err := spawnRaw(sup, `{"role":"writer","task":"a","paths":["internal/ui/**"]}`); err != nil {
		t.Fatalf("first writer should spawn: %v", err)
	}
	_, err := spawnRaw(sup, `{"role":"writer","task":"b","paths":["internal/ui/chat/model.go"]}`)
	if err == nil || !strings.Contains(err.Error(), "already claims") {
		t.Fatalf("an overlapping claim should be refused, got %v", err)
	}
	// A disjoint claim is fine.
	if _, err := spawnRaw(sup, `{"role":"writer","task":"c","paths":["docs/**"]}`); err != nil {
		t.Fatalf("a disjoint claim should spawn: %v", err)
	}
	// Paths are for writers only.
	if _, err := spawnRaw(sup, `{"role":"researcher","task":"d","paths":["docs/**"]}`); err == nil {
		t.Fatal("a researcher may not claim paths")
	}
}

func TestPathsOverlap(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"internal/ui/**", "internal/ui/chat/model.go", true},
		{"internal/ui/chat/**", "internal/ui/**", true},
		{"internal/ui/**", "internal/agent/**", false},
		{"README.md", "README.md", true},
		{"docs/a.md", "docs/b.md", false},
		{"./docs/**", "docs/guide.md", true},
	}
	for _, c := range cases {
		if got := pathsOverlap(c.a, c.b); got != c.want {
			t.Errorf("pathsOverlap(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// A child has less watching it than a session: it runs uncapped by default,
// takes no readings unless asked to, and has nobody in front of it. Its
// check-in is often the only question it will ever be put, so it comes
// sooner than a session's.
func TestChildCheckInInterval_IsShorterThanASession(t *testing.T) {
	if ChildCheckInInterval >= agent.DefaultCheckInInterval {
		t.Errorf("a child's interval (%d) should be shorter than a session's (%d)",
			ChildCheckInInterval, agent.DefaultCheckInInterval)
	}
	if DefaultMaxRounds != agent.UnlimitedToolRounds {
		t.Fatal("this reasoning assumes a child runs uncapped by default")
	}
}

// A child is built on two paths — a spawn and a retry — and a setting that
// reaches only the first leaves a retried child asking nothing for far
// longer, with no symptom anyone would notice.
func TestNewChildAgent_BothPathsGetTheChildInterval(t *testing.T) {
	env := Env{SystemPrompt: "you are a child"}
	for _, maxRounds := range []int{agent.UnlimitedToolRounds, 25} {
		a := newChildAgent(env, maxRounds)
		if got := a.CheckInInterval(); got != ChildCheckInInterval {
			t.Errorf("maxRounds=%d: interval = %d, want %d", maxRounds, got, ChildCheckInInterval)
		}
	}
}

// The configured wording reaches a child and the configured interval does
// not: a child has none of what makes a session's long interval safe, and
// the two halves of that are one line apart in newChildAgent.
func TestNewChildAgent_TakesTheWordingsAndKeepsItsOwnInterval(t *testing.T) {
	env := Env{
		SystemPrompt: "you are a child",
		Steering: agent.Steering{
			CheckInInterval: 200,
			CheckIn:         "used " + agent.PlaceholderRounds + ". " + agent.PlaceholderFinished,
		},
	}
	a := newChildAgent(env, 25)
	if got := a.CheckInInterval(); got != ChildCheckInInterval {
		t.Errorf("interval = %d, want the child's own %d", got, ChildCheckInInterval)
	}
	want := "used 0. " + agent.FinishedAsSubAgent
	if got := a.CheckInMessage(agent.FinishedAsSubAgent); got != want {
		t.Errorf("check-in = %q, want the configured wording %q", got, want)
	}
}

// A child's edit refused because the file moved reads the way the session's
// does: a row naming the file, with the sentence the model was given folded
// under it. A parent that mirrors the child's transcript then shows one row
// for the two paths rather than two accounts of the same refusal.
func TestStaleChildEditIsItsOwnRow(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "loop.go")
	if err := os.WriteFile(path, []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	read, _ := json.Marshal(map[string]string{"path": path})
	if _, err := tools.Execute(tools.ReadFileName, read); err != nil {
		t.Fatal(err)
	}
	// Somebody else — an editor, a sibling session — gets there first.
	if err := os.WriteFile(path, []byte("alpha\ndelta\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	env := &scriptedEnv{
		steps: []streamStep{
			{calls: []provider.ToolCall{{ID: "e1", Name: tools.EditFileName,
				Arguments: `{"path":"loop.go","old_text":"alpha","new_text":"omega"}`}}},
			{text: "task complete"},
		},
		gated: map[string]bool{tools.EditFileName: true},
	}
	sup := New(context.Background(), Options{Root: root, NewEnv: env.factory()})
	t.Cleanup(sup.Close)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"change the loop"}`)

	if report := execTool(t, sup, ReportToolName, `{"name":"researcher-1"}`); !strings.Contains(report, "task complete") {
		t.Fatalf("child should continue after the refusal: %s", report)
	}
	if !transcriptHas(sup.Transcript("researcher-1"), EntrySystem, "skipped · loop.go changed since it was read") {
		t.Fatalf("no named staleness row in the child transcript: %+v", sup.Transcript("researcher-1"))
	}
	for _, e := range sup.Transcript("researcher-1") {
		if e.Kind == EntrySystem && strings.HasPrefix(e.Text, "skipped · ") {
			if !strings.Contains(e.Result, "read_file it again") {
				t.Errorf("the row should fold the model's own sentence, got %q", e.Result)
			}
		}
	}
	// A call nobody can approve is never put to the parent.
	select {
	case ev := <-sup.Events():
		if ev.Kind == EventAsk {
			t.Fatal("a refused preview must not reach the user as a decision")
		}
	default:
	}
}

// A child waiting out a provider is holding no stream, so cancelling its
// context alone would leave it asleep for the rest of a countdown it has no
// reason to finish — worktree still on disk, lane still on screen. Kill has
// to reach the wait itself.
func TestKillWakesAChildWaitingOutAProvider(t *testing.T) {
	env := &scriptedEnv{steps: []streamStep{
		// Longer than the cap, so the wait is the full minute and nothing but
		// the kill can end it inside this test.
		{fail: &provider.Failure{Class: provider.ClassOverloaded, RetryAfter: 2 * time.Minute}},
	}}
	sup := newTestSupervisor(t, env)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"survey the code"}`)

	deadline := time.Now().Add(5 * time.Second)
	for {
		if st, ok := sup.Get("researcher-1"); ok && strings.Contains(st.Detail, "retry") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the child never reached its retry wait")
		}
		time.Sleep(5 * time.Millisecond)
	}

	if err := sup.Kill("researcher-1"); err != nil {
		t.Fatal(err)
	}
	// waitState gives up well inside the minute the wait would otherwise run.
	waitState(t, sup, "researcher-1", StateFailed)
}
