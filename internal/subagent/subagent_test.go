package subagent

import (
	"context"
	"encoding/json"
	"errors"
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
	return func(ctx context.Context, role Role, root string) (Env, error) {
		stream := func(msgs []provider.Message) (<-chan provider.StreamEvent, context.CancelFunc, error) {
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
			{text: "should never get here"},
		},
	}
	sup := newTestSupervisor(t, env)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"read a lot","max_tokens":1000}`)

	report := execTool(t, sup, ReportToolName, `{"name":"researcher-1"}`)
	if !strings.Contains(report, "token budget") {
		t.Fatalf("expected a token-budget failure, got: %s", report)
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
	s, err := SpawnSummary(json.RawMessage(`{"role":"writer","task":"refactor the loop"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s, "writer") || !strings.Contains(s, "refactor the loop") {
		t.Fatalf("unexpected summary: %s", s)
	}
	if _, err := SpawnSummary(json.RawMessage(`{"role":"nope","task":"x"}`)); err == nil {
		t.Fatal("expected an error for an invalid role")
	}
}

func TestParseSpawnArgsClampsBudgets(t *testing.T) {
	args, err := parseSpawnArgs(json.RawMessage(`{"role":"researcher","task":"x","max_rounds":999,"max_tokens":99999999}`))
	if err != nil {
		t.Fatal(err)
	}
	if args.maxRounds != MaxRoundsCeiling {
		t.Fatalf("max_rounds not clamped: %d", args.maxRounds)
	}
	if args.maxTokens != MaxTokensCeiling {
		t.Fatalf("max_tokens not clamped: %d", args.maxTokens)
	}
	args, err = parseSpawnArgs(json.RawMessage(`{"role":"researcher","task":"x"}`))
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
	return func(ctx context.Context, role Role, root string) (Env, error) {
		stream := func(msgs []provider.Message) (<-chan provider.StreamEvent, context.CancelFunc, error) {
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
	factory := func(ctx context.Context, role Role, root string) (Env, error) {
		stream := func(msgs []provider.Message) (<-chan provider.StreamEvent, context.CancelFunc, error) {
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
