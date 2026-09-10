package subagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/evidence"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/web"
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
	// stop is why the step's reply ended, for a test that needs an ending
	// other than the ordinary one. The zero value is a reply the model
	// finished, which is what every step that says nothing means.
	stop provider.StopReason
}

// scriptedEnv builds an EnvFactory whose children replay steps in order. The
// stream respects the child context, so cancellation behaves like a real
// provider stream.
type scriptedEnv struct {
	mu    sync.Mutex
	steps []streamStep

	// summarizer, when set, is the reader the child takes periodic readings
	// through — off in an ordinary session, so a test that wants a child
	// judged has to say so.
	summarizer *agent.Summarizer
	// delay is how long each scripted round takes to answer. Rounds here are
	// otherwise instant, which no provider is, and a reading that runs beside
	// the loop needs the loop to take some time for the reading to land in.
	delay time.Duration

	gated      map[string]bool
	execOut    string
	execCode   int
	ranCommand atomic.Bool
	// reduce is the reduction pipeline the child's command output goes
	// through, as a session hands one in. Nil is a child whose surface has
	// no evidence store.
	reduce func(tool, result string) string
	// requests is every message list the scripted stream has been handed,
	// so a test can state what the child's next round actually carried —
	// a tool result included, which is the only place a child's own view of
	// a result can be read from.
	requests [][]provider.Message
	// repeats is the circling detector the surface wraps the child's auto-run
	// dispatcher with and then asks for sweeps, as a session wires one. Nil
	// is a surface that wired none, which is the ordinary test child. It is
	// set before the supervisor runs and only read after that, so it is
	// outside the lock the steps are under.
	repeats *agent.RepeatDetector
}

// asked reports whether any request the child has made carried this text —
// what the child was actually told, rather than what a hook was handed.
func (s *scriptedEnv) asked(text string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, msgs := range s.requests {
		for _, m := range msgs {
			if strings.Contains(m.Content, text) {
				return true
			}
		}
	}
	return false
}

// lastToolResult is the tool result the child's most recent round carried —
// what the child itself read of the call it just made.
func (s *scriptedEnv) lastToolResult() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.requests) == 0 {
		return ""
	}
	var result string
	for _, m := range s.requests[len(s.requests)-1] {
		if m.Role == provider.RoleTool {
			result = m.Content
		}
	}
	return result
}

func (s *scriptedEnv) factory() EnvFactory {
	return func(ctx context.Context, spec Spec) (Env, error) {
		stream := func(msgs []provider.Message, _ string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
			if ctx.Err() != nil {
				return nil, nil, ctx.Err()
			}
			s.mu.Lock()
			s.requests = append(s.requests, append([]provider.Message(nil), msgs...))
			if len(s.steps) == 0 {
				s.mu.Unlock()
				return nil, nil, errors.New("scripted stream exhausted")
			}
			step := s.steps[0]
			s.steps = s.steps[1:]
			delay := s.delay
			s.mu.Unlock()
			if delay > 0 {
				time.Sleep(delay)
			}

			if step.fail != nil {
				return nil, nil, step.fail
			}
			ch := make(chan provider.StreamEvent, 3)
			if step.text != "" {
				ch <- provider.StreamEvent{Token: step.text}
			}
			if len(step.calls) > 0 {
				ch <- provider.StreamEvent{ToolCalls: step.calls, Usage: step.usage, Stop: step.stop}
			} else {
				ch <- provider.StreamEvent{Done: true, Usage: step.usage, Stop: step.stop}
			}
			close(ch)
			_, cancel := context.WithCancel(context.Background())
			return ch, cancel, nil
		}
		env := Env{
			SystemPrompt: "test system prompt",
			Stream:       stream,
			Summarizer:   s.summarizer,
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
			Reduce: s.reduce,
			Gated:  s.gated,
		}
		if s.repeats != nil {
			env.WrapAuto = func(_ Seam, next agent.ToolExecutor) agent.ToolExecutor {
				return s.repeats.WrapExecutor(next)
			}
			env.Sweeps = s.repeats.Sweeps
		}
		return env, nil
	}
}

// readingProvider is the reader a child is judged by, answering whatever a
// test last told it to. It is asked from the summarizer's own goroutine while
// the child runs, so the word it is on is taken under a lock.
type readingProvider struct {
	mu    sync.Mutex
	state string
	// digests is every digest the reader was handed, so a test can state
	// what the child was judged against rather than only what came back.
	digests []string
}

func (p *readingProvider) say(state string) {
	p.mu.Lock()
	p.state = state
	p.mu.Unlock()
}

// judgedAgainst is every digest the reader has been handed so far.
func (p *readingProvider) judgedAgainst() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.digests...)
}

func (p *readingProvider) StreamCompletion(_ context.Context, msgs []provider.Message, _ provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
	p.mu.Lock()
	state := p.state
	for _, m := range msgs {
		if m.Role == provider.RoleUser {
			p.digests = append(p.digests, m.Content)
		}
	}
	p.mu.Unlock()
	ch := make(chan provider.StreamEvent, 1)
	ch <- provider.StreamEvent{
		ToolCalls: []provider.ToolCall{{
			ID:   "s1",
			Name: agent.SummaryToolName,
			Arguments: `{"summary":"reading files in the importer","state":"` + state +
				`","reason":"reading the importer, not the exporter"}`,
		}},
		Done: true,
	}
	close(ch)
	return ch, nil
}

func (p *readingProvider) Name() string { return "reading" }

// toolRounds is a script of n rounds that each read a file, for a child that
// has to run long enough to be read, steered or retried while it works. What
// ends the turn is the caller's: a script that runs out mid-turn fails the
// child, which is an ending a test rarely means.
func toolRounds(n int) []streamStep {
	steps := make([]streamStep, 0, n+1)
	for i := range n {
		steps = append(steps, streamStep{calls: []provider.ToolCall{
			{ID: fmt.Sprintf("r%d", i), Name: "read_file", Arguments: `{"path":"importer.go"}`},
		}})
	}
	return steps
}

// readRounds is toolRounds with the answer that ends the turn.
func readRounds(n int) []streamStep {
	return append(toolRounds(n), streamStep{text: "read the importer"})
}

// judgedChild is a supervisor whose one child is read every few rounds by
// reader. The wall-clock floor is off — this is about the round interval, and
// twenty real seconds is not a thing a test can wait for — and the interval
// is wide enough that a reading is still fresh at the boundary it is
// collected at, since a verdict older than one interval is withheld as
// describing work the run has left behind.
func judgedChild(t *testing.T, reader provider.Provider, rounds int) *Supervisor {
	t.Helper()
	env := &scriptedEnv{steps: readRounds(rounds), delay: 3 * time.Millisecond,
		summarizer: agent.NewSummarizer(reader,
			agent.SummaryConfig{Model: "fast", IntervalRounds: 10, MinGap: -1, InterveneCooldownIntervals: 1})}
	sup := newTestSupervisor(t, env)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"survey the exporter"}`)
	return sup
}

// statusOf is one child's live snapshot by name.
func statusOf(t *testing.T, sup *Supervisor, name string) Status {
	t.Helper()
	for _, st := range sup.Snapshot() {
		if st.Name == name {
			return st
		}
	}
	t.Fatalf("no agent named %s", name)
	return Status{}
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

// testRunOutput is what `go test ./...` looks like when the failure is in
// the middle of it: passing packages either side of a failed one, and the
// verdict on the last line. It is deliberately far past the formatter's
// output cap, which keeps the head and nothing else.
func testRunOutput(pkgs int) string {
	var b strings.Builder
	line := func(i int) {
		fmt.Fprintf(&b, "ok  \tgithub.com/example/project/pkg%03d\t0.0%ds\n", i, i%10)
	}
	for i := range pkgs {
		line(i)
	}
	b.WriteString("--- FAIL: TestImporterRejectsEmptyRows (0.01s)\n")
	b.WriteString("    importer_test.go:42: wanted 3 rows, got 4\n")
	b.WriteString("FAIL\tgithub.com/example/project/importer\t0.11s\n")
	for i := range pkgs {
		line(pkgs + i)
	}
	b.WriteString("FAIL\n")
	return b.String()
}

// evidenceID is the id a reduction notice tells the reader to page with.
var evidenceID = regexp.MustCompile(`evidence (ev-[0-9a-f]+)`)

func TestChildCommandOutputIsReduced(t *testing.T) {
	store, err := evidence.Open(t.TempDir(), evidence.NewSessionID())
	if err != nil {
		t.Fatalf("open evidence store: %v", err)
	}
	red := evidence.NewReducer(store)
	out := testRunOutput(500)
	if len(out) < 20_000 {
		t.Fatalf("fixture is only %d bytes; the point is a run past the cap", len(out))
	}
	env := &scriptedEnv{
		steps:    gatedCommandSteps("go test ./..."),
		gated:    map[string]bool{tools.ExecCommandName: true},
		execOut:  out,
		execCode: 1,
		reduce:   red.Process,
	}
	sup := newTestSupervisor(t, env)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"run the tests"}`)
	nextAsk(t, sup).Respond(true)
	execTool(t, sup, ReportToolName, `{"name":"researcher-1"}`)

	result := env.lastToolResult()
	if result == "" {
		t.Fatal("the child's next round carried no tool result")
	}
	if !strings.Contains(result, "--- FAIL: TestImporterRejectsEmptyRows") {
		t.Fatalf("the failing test is not in what the child read:\n%s", result)
	}
	if !strings.HasSuffix(strings.TrimRight(result, "\n"), "\nFAIL") {
		t.Fatalf("the run's verdict is not in what the child read:\n%s", result)
	}
	if !strings.Contains(result, "exit code: 1") {
		t.Fatalf("result lost the exit code:\n%s", result)
	}

	// The notice names an id, and the id pages the whole output back through
	// the same store the child's evidence tool is dispatched against.
	m := evidenceID.FindStringSubmatch(result)
	if m == nil {
		t.Fatalf("no evidence id in the result:\n%s", result)
	}
	paged, err := store.ExecuteTool(json.RawMessage(
		fmt.Sprintf(`{"action":"read","id":%q,"offset":%d}`, m[1], len(out)-2000)))
	if err != nil {
		t.Fatalf("paging the stored output: %v", err)
	}
	if !strings.Contains(paged, "pkg999") {
		t.Fatalf("the store kept something other than the whole run:\n%s", paged)
	}
}

// A child whose surface has no evidence store still gets a formatted result
// rather than a panic, and the formatter's cap is what bounds it.
func TestChildCommandOutputWithoutReducer(t *testing.T) {
	env := &scriptedEnv{
		steps:    gatedCommandSteps("go test ./..."),
		gated:    map[string]bool{tools.ExecCommandName: true},
		execOut:  testRunOutput(500),
		execCode: 1,
	}
	sup := newTestSupervisor(t, env)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"run the tests"}`)
	nextAsk(t, sup).Respond(true)
	execTool(t, sup, ReportToolName, `{"name":"researcher-1"}`)

	if result := env.lastToolResult(); !strings.Contains(result, "(output truncated)") {
		t.Fatalf("expected the formatter's own cap:\n%s", result)
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
				usage: &provider.Usage{PromptTokens: 300100, CompletionTokens: 100},
			},
		},
	}
	sup := newTestSupervisor(t, env)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"read a lot","max_tokens":300000}`)

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
				usage: &provider.Usage{PromptTokens: 300100, CompletionTokens: 100},
			},
			{text: "got as far as the parser; the lexer is untouched"},
		},
	}
	sup := newTestSupervisor(t, env)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"read a lot","max_tokens":300000}`)

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
				usage: &provider.Usage{PromptTokens: 300100, CompletionTokens: 100},
			},
		},
	}
	sup := newTestSupervisor(t, env)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"find the bottleneck","max_tokens":300000}`)

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

// TestTokenBudgetCountsFreshTokensNotCachedOnes: the budget bounds what a
// child has taken in, and a prompt the provider served from its cache is
// neither new to the child nor billed at the input rate. Counting the whole
// prompt charged a child's own standing context to it again on every round,
// which killed children with a large instruction block a handful of requests
// in. The spend the parent is shown is still the billed figure.
func TestTokenBudgetCountsFreshTokensNotCachedOnes(t *testing.T) {
	env := &scriptedEnv{
		steps: []streamStep{
			{
				text:  "the parser is the bottleneck",
				usage: &provider.Usage{PromptTokens: 300100, CachedTokens: 300000, CompletionTokens: 50},
			},
		},
	}
	sup := newTestSupervisor(t, env)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"find the bottleneck","max_tokens":300000}`)

	report := execTool(t, sup, ReportToolName, `{"name":"researcher-1"}`)
	if strings.Contains(report, "token budget") {
		t.Fatalf("150 fresh tokens against a budget of 300000 must not stop a child: %s", report)
	}
	if !strings.Contains(report, "the parser is the bottleneck") {
		t.Fatalf("the finished report must reach the parent: %s", report)
	}
	st, ok := sup.Get("researcher-1")
	if !ok {
		t.Fatal("the child is missing from the roster")
	}
	if st.Spend.In != 300100 || st.Spend.Out != 50 {
		t.Fatalf("the spend must stay the billed figure, got ↑%d ↓%d", st.Spend.In, st.Spend.Out)
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

// A child that is steered and carries on says so where the parent is
// looking. The steer itself lands on the child's own transcript, which is a
// surface nobody has attached to; the count and the reading behind it ride
// the status, which is what the roster prints and what the lane draws.
func TestSteeredChildCountsItsSteersOnTheStatusAndTheRoster(t *testing.T) {
	// Enough rounds for several readings: the first falls due early, the
	// rest on the interval, and a cooldown of one interval sits between two
	// steers. The child answers none of them, which is the case this counts.
	sup := judgedChild(t, &readingProvider{state: "off_target"}, 40)
	execTool(t, sup, ReportToolName, `{"name":"researcher-1"}`)

	st := statusOf(t, sup, "researcher-1")
	if st.Steers < 2 {
		t.Fatalf("a child steered and read again must count more than one steer, got %d", st.Steers)
	}
	if st.Verdict != agent.SummaryOffTarget.String() {
		t.Fatalf("the status must carry the last reading's word, got %q", st.Verdict)
	}

	roster := execTool(t, sup, ReportToolName, `{}`)
	for _, want := range []string{plural(st.Steers, "steer"), agent.SummaryOffTarget.String()} {
		if !strings.Contains(roster, want) {
			t.Fatalf("the roster does not say %q:\n%s", want, roster)
		}
	}
	if strings.Contains(roster, "reading the importer") {
		t.Fatalf("the roster carries the reading's word and never its prose:\n%s", roster)
	}
}

// The count is the turn's. A child handed a new instruction — which is how a
// person answers the very count that reached them — starts it at zero, while
// the reading stands until another one replaces it: the last reading of this
// child's work is still the last reading.
func TestASteerCountBelongsToTheTurnItWasGivenIn(t *testing.T) {
	c := &child{name: "researcher-1", role: RoleResearcher}
	c.steers, c.verdict = 2, agent.SummaryOffTarget.String()
	c.beginTurn()
	st := c.status()
	if st.Steers != 0 {
		t.Fatalf("a new turn starts with no steers, got %d", st.Steers)
	}
	if st.Verdict != agent.SummaryOffTarget.String() {
		t.Fatalf("the last reading stands into the next turn, got %q", st.Verdict)
	}
	if got := steerMark(st); got != " · off target" {
		t.Fatalf("the roster says the reading and no count, got %q", got)
	}
}

// What a child has changed is counted off its own calls. Its edits happen in
// an isolated worktree, so the parent's changeset hears nothing about them
// until the patch lands — which is after the last reading this child will
// ever take, and those readings are the ones that have to tell a child that
// has started acting from one that is still reading.
func TestAChildCountsTheFilesItsOwnCallsWrote(t *testing.T) {
	c := &child{name: "writer-1", role: RoleWriter}
	c.noteWrite(provider.ToolCall{Name: "write_file", Arguments: `{"path":"a.go","content":"x"}`}, "written")
	c.noteWrite(provider.ToolCall{Name: "edit_file", Arguments: `{"path":"a.go"}`}, "edited")
	c.noteWrite(provider.ToolCall{Name: "edit_file", Arguments: `{"path":"b.go"}`}, "error: no such file")
	c.noteWrite(provider.ToolCall{Name: "read_file", Arguments: `{"path":"c.go"}`}, "package main")

	files, added, removed := c.changed()
	if files != 1 || added != 0 || removed != 0 {
		t.Fatalf("changed = %d files +%d −%d, want the one file two calls wrote", files, added, removed)
	}
}

// A person who redirects a child mid-turn has answered the count that
// reached them, and the child stops reporting it — the lane and the roster
// would otherwise go on naming a child as not answering a steer while it
// works on the instruction that answered it. The redirect joins the running
// turn rather than starting a new one, which is what makes this the mid-turn
// reset and not the one every turn boundary does.
func TestAPersonsRedirectClearsTheCountItAnswers(t *testing.T) {
	reader := &readingProvider{state: "off_target"}
	sup := judgedChild(t, reader, 200)

	deadline := time.After(10 * time.Second)
	for statusOf(t, sup, "researcher-1").Steers == 0 {
		select {
		case <-deadline:
			t.Fatal("no steer was delivered to the child")
		default:
		}
		time.Sleep(time.Millisecond)
	}

	// Nothing further is owed a steer, so what the count ends on is what the
	// redirect left it at.
	reader.say("on_target")
	if err := sup.Steer("researcher-1", "read the exporter instead", SteerFromLane); err != nil {
		t.Fatalf("steering the child: %v", err)
	}
	execTool(t, sup, ReportToolName, `{"name":"researcher-1"}`)

	if st := statusOf(t, sup, "researcher-1"); st.Steers != 0 {
		t.Fatalf("a person's redirect clears the count it answers, got %d", st.Steers)
	}
	c := sup.byName["researcher-1"]
	c.mu.Lock()
	turns := c.turns
	c.mu.Unlock()
	if turns != 1 {
		t.Fatalf("the redirect should have joined the running turn, not started one: %d turns", turns)
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

func TestSpawnAdmissionRefusesBeforeCreatingAChild(t *testing.T) {
	opened := 0
	sup := New(t.Context(), Options{
		Root: t.TempDir(),
		NewEnv: func(context.Context, Spec) (Env, error) {
			opened++
			return Env{SystemPrompt: strings.Repeat("p", 8_000)}, nil
		},
		Record: func(Spec, string) Recorder { t.Fatal("a refused spawn opened a record"); return Recorder{} },
	})
	t.Cleanup(sup.Close)
	_, err := sup.Spawn(json.RawMessage(`{"role":"writer","task":"x","max_tokens":200000}`))
	if err == nil || !strings.Contains(err.Error(), "cannot admit") || !strings.Contains(err.Error(), "202000") {
		t.Fatalf("undersized spawn error = %v, want its growing admission floor", err)
	}
	if opened != 1 {
		t.Fatalf("admission should build one preflight environment, got %d", opened)
	}
	if children := sup.Snapshot(); len(children) != 0 {
		t.Fatalf("a refused spawn claimed a child slot: %+v", children)
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
	if _, err := parseSpawnArgs(nil, json.RawMessage(`{"role":"researcher","task":"x","max_tokens":60000}`)); err == nil {
		t.Fatal("a 60000-token request must be refused rather than promoted")
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

	if err := sup.Steer("researcher-1", "continue please", SteerFromLane); err != nil {
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

	if err := sup.Steer("researcher-1", "too late", SteerFromLane); err == nil {
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

	if err := sup.Steer("researcher-1", "queued mid-turn", SteerFromLane); err != nil {
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
	if err := sup.Steer("researcher-1", "one more thing", SteerFromLane); err != nil {
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
	// The account is on the act, and the judge's seconds with it: the
	// classifier is the one rule whose judgement costs anything.
	row := toolRow(t, sup.Transcript("researcher-1"), tools.ExecCommandName)
	if row.AllowedBy != classifierRule {
		t.Fatalf("the command's row should name the classifier, got %q", row.AllowedBy)
	}
	if row.AllowElapsed <= 0 {
		t.Fatal("the classifier's judgement should carry what it took")
	}
}

// TestChildAutoApprovalStatesTheActOnce: a call the child's mode allowed
// takes one row in the child's transcript — the act's, carrying the rule that
// allowed it — rather than a notice naming the same call above it.
func TestChildAutoApprovalStatesTheActOnce(t *testing.T) {
	env := &scriptedEnv{
		steps: []streamStep{
			{calls: []provider.ToolCall{{ID: "e1", Name: tools.EditFileName,
				Arguments: `{"path":"loop.go","old_text":"alpha","new_text":"omega"}`}}},
			{text: "task complete"},
		},
		gated: map[string]bool{tools.EditFileName: true},
	}
	sup := newTestSupervisor(t, env)
	sup.SetParentMode(agent.ModeAuto)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"change the loop"}`)

	if report := execTool(t, sup, ReportToolName, `{"name":"researcher-1"}`); !strings.Contains(report, "task complete") {
		t.Fatalf("unexpected report: %s", report)
	}
	entries := sup.Transcript("researcher-1")
	row := toolRow(t, entries, tools.EditFileName)
	if row.AllowedBy != "auto mode" {
		t.Fatalf("the edit's row should name the mode that allowed it, got %q", row.AllowedBy)
	}
	// The mode decided without paying anybody to think about it, so there is
	// no cost to state beside the rule.
	if row.AllowElapsed != 0 {
		t.Fatalf("a mode's own decision costs nothing, got %v", row.AllowElapsed)
	}
	for _, e := range entries {
		if e.Kind == EntrySystem {
			t.Fatalf("the approval should be the act's own field, not a row: %q", e.Text)
		}
	}
}

// toolRow is the one transcript row a call left behind. It fails the test
// when a call has none or more than one, which is the two-row shape these
// tests are about.
func toolRow(t *testing.T, entries []TranscriptEntry, tool string) TranscriptEntry {
	t.Helper()
	var found []TranscriptEntry
	for _, e := range entries {
		if e.Kind == EntryTool && e.Tool == tool {
			found = append(found, e)
		}
	}
	if len(found) != 1 {
		t.Fatalf("%s has %d rows, want exactly one: %+v", tool, len(found), entries)
	}
	return found[0]
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
	if len(seen) != 4 || seen[0] != "role-default" || seen[1] != "role-default" || seen[2] != "tiny-model" || seen[3] != "tiny-model" {
		t.Fatalf("models handed to the env factory = %v, want [role-default role-default tiny-model tiny-model]", seen)
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
// the two halves of that are one line apart in newChildAgent. The child's own
// exit is the third: every check-in it is asked closes on its final report,
// including the configured wording's own {{finished}}, because a child told
// to say so says so into a transcript nobody reads.
func TestNewChildAgent_TakesTheWordingsAndKeepsItsOwnIntervalAndExit(t *testing.T) {
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
	if got := a.CheckInMessage(); got != want {
		t.Errorf("check-in = %q, want the configured wording %q", got, want)
	}
	// One field, so every route the agent has to a check-in — its clock, a
	// reading that says it has enough, its round cap — asks with the report.
	if got := a.Steering().Finished; got != agent.FinishedAsSubAgent {
		t.Errorf("finish = %q, want the child's own %q", got, agent.FinishedAsSubAgent)
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

// A lane can only say what its child started from if the count travels with
// the status. A reader has no copy of the repository and starts from nothing,
// so it is never asked what the parent has not committed.
func TestWriterStatusCountsWhatItStartedFrom(t *testing.T) {
	repo := initTestRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("package main\n\nvar edited = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "notes.md"), []byte("the session wrote this\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	asked := 0
	env := &scriptedEnv{steps: []streamStep{{text: "done"}, {text: "done"}}}
	sup := New(context.Background(), Options{
		Root:   repo,
		NewEnv: env.factory(),
		Untracked: func() []string {
			// Under a lock: the tree is read on the child's own goroutine
			// now, because the copy is taken when the writer starts.
			mu.Lock()
			asked++
			mu.Unlock()
			return []string{"notes.md"}
		},
	})
	t.Cleanup(sup.Close)

	if _, err := spawnRaw(sup, `{"role":"writer","task":"carry on"}`); err != nil {
		t.Fatalf("the writer should spawn: %v", err)
	}
	if _, err := spawnRaw(sup, `{"role":"researcher","task":"look around"}`); err != nil {
		t.Fatalf("the researcher should spawn: %v", err)
	}
	// The count arrives with the run rather than with the spawn: a queued
	// writer has no copy of the repository to have been seeded from.
	waitState(t, sup, "writer-1", StateDone)
	waitState(t, sup, "researcher-1", StateDone)
	if st, ok := sup.Get("writer-1"); !ok || st.Seeded != 2 {
		t.Fatalf("the writer's status should say it started from 2 parent paths, got %+v", st)
	}
	if st, ok := sup.Get("researcher-1"); !ok || st.Seeded != 0 {
		t.Fatalf("a reader starts from nothing, got %+v", st)
	}
	mu.Lock()
	defer mu.Unlock()
	if asked != 1 {
		t.Fatalf("the parent's untracked files were asked for %d times, want once — only the writer has a worktree", asked)
	}
}

// A retry is a second attempt against the tree as it is now, not as it was
// when the first attempt started: minutes have passed, and the session has
// usually gone on working in them. A child handed the old tree would write
// its patch against text the session has already moved past — the very thing
// starting from the parent's tree exists to avoid.
func TestRetryStartsFromTheTreeAsItIsNow(t *testing.T) {
	repo := initTestRepo(t)
	untracked := []string{}
	env := &scriptedEnv{}
	sup := New(context.Background(), Options{
		Root:      repo,
		NewEnv:    env.factory(),
		Untracked: func() []string { return untracked },
	})
	t.Cleanup(sup.Close)

	// No scripted steps: the first stream fails, which fails the child.
	if _, err := spawnRaw(sup, `{"role":"writer","task":"carry on"}`); err != nil {
		t.Fatalf("the writer should spawn: %v", err)
	}
	waitState(t, sup, "writer-1", StateFailed)
	if st, _ := sup.Get("writer-1"); st.Seeded != 0 {
		t.Fatalf("a clean parent seeded %d paths, want none", st.Seeded)
	}

	// The session keeps working while the child is down.
	if err := os.WriteFile(filepath.Join(repo, "notes.md"), []byte("written since\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	untracked = []string{"notes.md"}

	env.mu.Lock()
	env.steps = []streamStep{{text: "done"}}
	env.mu.Unlock()
	if err := sup.Retry("writer-1"); err != nil {
		t.Fatalf("retry: %v", err)
	}
	waitState(t, sup, "writer-1", StateDone)
	if st, _ := sup.Get("writer-1"); st.Seeded != 1 {
		t.Fatalf("the retry started from %d parent paths, want the one written since", st.Seeded)
	}
}

// waitHeld polls until the named child has parked at its own round boundary.
func waitHeld(t *testing.T, sup *Supervisor, name string, want bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if st, ok := sup.Get(name); ok && st.Held == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	st, _ := sup.Get(name)
	t.Fatalf("agent %s held = %v, want %v (state %s)", name, st.Held, want, st.State)
}

// A hold reaches the whole fan-out and one release lets it all go. Nothing
// stops where it stands: each child finishes the round it is in and waits at
// its own boundary, which is the only place a turn can be stopped without
// abandoning a request the provider is still answering.
func TestHoldParksEveryChildAndOneReleaseLetsThemGo(t *testing.T) {
	// Both children take a tool round first, whichever order they get to the
	// scripted stream in, so each one has a boundary of its own to park at.
	env := &scriptedEnv{steps: []streamStep{
		{calls: []provider.ToolCall{{ID: "c1", Name: "read_file", Arguments: `{"path":"x"}`}}},
		{calls: []provider.ToolCall{{ID: "c2", Name: "read_file", Arguments: `{"path":"y"}`}}},
		{text: "first done"},
		{text: "second done"},
	}}
	sup := newTestSupervisor(t, env)
	sup.Hold()
	if !sup.Holding() {
		t.Fatal("the hold did not stand")
	}

	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"survey one"}`)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"survey two"}`)
	waitHeld(t, sup, "researcher-1", true)
	waitHeld(t, sup, "researcher-2", true)

	for _, name := range []string{"researcher-1", "researcher-2"} {
		st, _ := sup.Get(name)
		if st.State != StateRunning {
			t.Errorf("%s should still be running while held, got %s", name, st.State)
		}
		if !strings.Contains(st.Detail, "held") {
			t.Errorf("%s should say it is held, got %q", name, st.Detail)
		}
	}

	sup.Release()
	if sup.Holding() {
		t.Fatal("the release did not clear the hold")
	}
	waitState(t, sup, "researcher-1", StateDone)
	waitState(t, sup, "researcher-2", StateDone)
	waitHeld(t, sup, "researcher-1", false)
}

// A hold has to be able to come back, which is why the channel is replaced
// rather than reopened: a closed one would let every later fan-out straight
// through.
func TestHoldCanBeTakenAgainAfterARelease(t *testing.T) {
	sup := newTestSupervisor(t, &scriptedEnv{})
	sup.Hold()
	sup.Release()
	sup.Hold()
	if !sup.Holding() {
		t.Fatal("a hold taken after a release did not stand")
	}
	sup.Release()
	// And releasing what nothing holds is not an error, so a session that
	// lets go twice does not have to remember which time was the real one.
	sup.Release()
}

// A held child is still killable: it must not sit in its worktree waiting for
// a release nobody is going to send.
func TestHoldDoesNotOutlastAKill(t *testing.T) {
	env := &scriptedEnv{steps: []streamStep{
		{calls: []provider.ToolCall{{ID: "c1", Name: "read_file", Arguments: `{"path":"x"}`}}},
		{text: "done"},
	}}
	sup := newTestSupervisor(t, env)
	sup.Hold()
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"survey"}`)
	waitHeld(t, sup, "researcher-1", true)

	if err := sup.Kill("researcher-1"); err != nil {
		t.Fatal(err)
	}
	waitState(t, sup, "researcher-1", StateFailed)
	// And it stops reading as held: a finished lane offering a release that
	// can no longer do anything is a lie about what is left to do.
	waitHeld(t, sup, "researcher-1", false)
}

// A release un-marks only the hold it is releasing. A hold taken again while
// a release is still walking the children would otherwise have its freshly
// parked child un-marked by the release before it, and the rail would report
// a child as running that is going nowhere.
func TestReleaseDoesNotUnparkAChildHeldByALaterHold(t *testing.T) {
	env := &scriptedEnv{steps: []streamStep{
		{calls: []provider.ToolCall{{ID: "c1", Name: "read_file", Arguments: `{"path":"x"}`}}},
		{text: "done"},
	}}
	sup := newTestSupervisor(t, env)
	sup.Hold()
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"survey"}`)
	waitHeld(t, sup, "researcher-1", true)

	// The first hold's channel, and a second hold taken over the top of it —
	// the order a release racing a child's own arrival would produce.
	first := currentHold(sup)
	sup.Release()
	sup.Hold()
	c, err := sup.lookup("researcher-1")
	if err != nil {
		t.Fatal(err)
	}
	c.park(currentHold(sup))

	// The stale release finds a child parked on a hold it does not own.
	if c.unpark(first) {
		t.Fatal("a release un-parked a child held by a later hold")
	}
	if st, _ := sup.Get("researcher-1"); !st.Held {
		t.Fatal("the child should still read as held")
	}
	sup.Release()
	waitState(t, sup, "researcher-1", StateDone)
}

// currentHold is the hold as it stands, read the way everything else in the
// supervisor reads it.
func currentHold(sup *Supervisor) chan struct{} {
	sup.mu.Lock()
	defer sup.mu.Unlock()
	return sup.held
}

// A child whose reply was cut at the model's output ceiling asks for the rest
// by itself, and says on its lane that it did: an answer assembled out of two
// halves is a different reading from one that arrived whole, and the lane is
// the only place anyone is looking.
func TestChildFinishesAReplyCutAtTheCeiling(t *testing.T) {
	env := &scriptedEnv{steps: []streamStep{
		{text: "the first half of the answer", stop: provider.StopLength},
		{text: " and the second."},
	}}
	sup := newTestSupervisor(t, env)

	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"explain it"}`)
	report := execTool(t, sup, ReportToolName, `{"name":"researcher-1"}`)
	if !strings.Contains(report, "and the second") {
		t.Fatalf("the child should have finished its answer: %s", report)
	}

	var said bool
	for _, e := range sup.Transcript("researcher-1") {
		if e.Kind == EntrySystem && strings.Contains(e.Text, "output ceiling") {
			said = true
		}
	}
	if !said {
		t.Fatalf("a child that answered in two halves said nothing on its lane: %+v",
			sup.Transcript("researcher-1"))
	}
}

// What a child is told about the workspace turns on where it is standing. A
// writer's git answers about a seed commit and a clean tree, so a writer
// handed the parent's branch and dirty count without being told it is in a
// copy reads its own `git status` as a contradiction; a reader is in the
// parent's own directory and has nothing to reconcile.
func TestSpecSaysWhetherTheChildStandsInACopy(t *testing.T) {
	repo := initTestRepo(t)
	var specs []Spec
	var mu sync.Mutex
	base := (&scriptedEnv{steps: []streamStep{{text: "done"}, {text: "done"}}}).factory()
	sup := New(context.Background(), Options{
		Root: repo,
		NewEnv: func(ctx context.Context, spec Spec) (Env, error) {
			mu.Lock()
			specs = append(specs, spec)
			mu.Unlock()
			return base(ctx, spec)
		},
	})
	t.Cleanup(sup.Close)

	if _, err := spawnRaw(sup, `{"role":"writer","task":"carry on"}`); err != nil {
		t.Fatalf("the writer should spawn: %v", err)
	}
	if _, err := spawnRaw(sup, `{"role":"researcher","task":"look around"}`); err != nil {
		t.Fatalf("the researcher should spawn: %v", err)
	}

	// A writer's environment is built when its slot comes free rather than
	// when it is spawned, so the two arrive in no fixed order and each spec
	// is found by the child it belongs to.
	waitState(t, sup, "writer-1", StateDone)
	waitState(t, sup, "researcher-1", StateDone)
	mu.Lock()
	defer mu.Unlock()
	byName := map[string]Spec{}
	for _, sp := range specs {
		byName[sp.Name] = sp
	}
	if len(byName) != 2 {
		t.Fatalf("both children build an environment, got %+v", specs)
	}
	if !byName["writer-1"].Worktree {
		t.Fatalf("a writer works in an isolated copy: %+v", byName["writer-1"])
	}
	if byName["researcher-1"].Worktree {
		t.Fatalf("a reader stands in the parent's own directory: %+v", byName["researcher-1"])
	}
}

// A child fetches on the hosts its parent answered for and on no others: the
// grants arrive from the parent at every decision, so revoking one there
// takes it from the child too, and a child has no way to add one.
func TestChildHostGrantsComeFromTheParentAndOnlyFromIt(t *testing.T) {
	sup := New(context.Background(), Options{
		Root:       t.TempDir(),
		AllowHosts: []string{"crates.io"},
		DenyHosts:  []string{"paste.example.test"},
	})
	t.Cleanup(sup.Close)
	sup.SetParentMode(agent.ModeManual)
	sup.SetParentGrants(agent.Grants{Hosts: []string{"docs.python.org"}})

	c := &child{mode: agent.ModeManual}
	decide := func(rawURL string) agent.Decision {
		t.Helper()
		action, err := actionFor(web.FetchToolName, json.RawMessage(fmt.Sprintf(`{"url":%q}`, rawURL)))
		if err != nil {
			t.Fatalf("actionFor: %v", err)
		}
		if action.Kind != agent.ActionFetch {
			t.Fatalf("a child's fetch is classified as %v", action.Kind)
		}
		decision, _ := sup.childPolicy(c).Decide(action)
		return decision
	}

	if got := decide("https://docs.python.org/3/library/json.html"); got != agent.Allow {
		t.Errorf("the parent's grant did not reach the child: %v", got)
	}
	if got := decide("https://crates.io/crates/serde"); got != agent.Allow {
		t.Errorf("the config's standing grant did not reach the child: %v", got)
	}
	if got := decide("https://pkg.go.dev/context"); got != agent.Ask {
		t.Errorf("an ungranted host = %v; want the child to ask its parent", got)
	}
	if got := decide("https://paste.example.test/x"); got != agent.Deny {
		t.Errorf("a denied host = %v; want Deny in a child too", got)
	}

	// The set is read from the parent at every decision, so taking a grant
	// back there takes it back here. There is no path the other way: a child
	// hands nothing to SetParentGrants.
	sup.SetParentGrants(agent.Grants{})
	if got := decide("https://docs.python.org/3/library/json.html"); got != agent.Ask {
		t.Errorf("a revoked grant still ran in a child: %v", got)
	}
}

// The orchestrator's own redirect goes down the path the lane's does, and the
// child is then judged against the task plus what it was told: without that
// the next reading calls the correction a departure and steers the child back
// onto the instruction the orchestrator has just moved it off.
func TestSteerToolRedirectsAChildAndItsNextReadingIsJudgedAgainstIt(t *testing.T) {
	reader := &readingProvider{state: "on_target"}
	sup := judgedChild(t, reader, 200)

	// The child has to be running before a steer can join its turn.
	waitFor(t, func() bool { return statusOf(t, sup, "researcher-1").State == StateRunning })

	out := execTool(t, sup, SteerToolName, `{"name":"researcher-1","message":"read the exporter instead"}`)
	if !strings.Contains(out, "researcher-1") {
		t.Fatalf("the tool result names the agent it steered, got %q", out)
	}

	waitFor(t, func() bool {
		for _, d := range reader.judgedAgainst() {
			if strings.Contains(d, "read the exporter instead") {
				return true
			}
		}
		return false
	})
	// The task it was spawned with is still half of what it is judged
	// against: a steer is added to the instruction, never a replacement for
	// it.
	var last string
	for _, d := range reader.judgedAgainst() {
		if strings.Contains(d, "read the exporter instead") {
			last = d
		}
	}
	if !strings.Contains(last, "survey the exporter") {
		t.Fatalf("the digest dropped the task the child was spawned with:\n%s", last)
	}
}

// A steer is a message for a running agent. One for an agent that has already
// answered has nowhere to go, and the refusal names it rather than failing
// quietly — the orchestrator is choosing between agents by name.
func TestSteerToolRefusesAFinishedChildByName(t *testing.T) {
	env := &scriptedEnv{steps: []streamStep{{text: "done"}}}
	sup := newTestSupervisor(t, env)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"survey the exporter"}`)
	waitFor(t, func() bool { return statusOf(t, sup, "researcher-1").State == StateDone })

	exec := sup.WrapExecutor(func(string, json.RawMessage) (string, error) {
		return "", errors.New("unexpected passthrough")
	})
	_, err := exec(SteerToolName, json.RawMessage(`{"name":"researcher-1","message":"read the exporter"}`))
	if err == nil {
		t.Fatal("a finished agent cannot be steered")
	}
	if !strings.Contains(err.Error(), "researcher-1") || !strings.Contains(err.Error(), "finished") {
		t.Fatalf("the refusal names the agent and why, got %q", err)
	}

	if _, err := exec(SteerToolName, json.RawMessage(`{"name":"nobody","message":"x"}`)); err == nil ||
		!strings.Contains(err.Error(), "nobody") {
		t.Fatalf("an agent that was never spawned is refused by name, got %v", err)
	}
}

// Both fields are required, and each refusal says what to do about it: a
// steer with no name has nowhere to go, and one with no message spends a
// round saying nothing.
func TestSteerToolRefusesACallWithNothingToDeliver(t *testing.T) {
	for _, tc := range []struct{ args, want string }{
		{`{"message":"read the exporter"}`, "name is required"},
		{`{"name":"researcher-1","message":"  "}`, "message is required"},
		{`{`, "invalid arguments"},
	} {
		if _, err := parseSteerArgs(json.RawMessage(tc.args)); err == nil ||
			!strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s should be refused with %q, got %v", tc.args, tc.want, err)
		}
	}
}

// The orchestration tools are registered together, which is what lets each
// one's description point at the others by name: a session that can collect a
// roster can always act on what the roster says — steer an agent that is not
// answering, run a failed one again. A tool wired into the dispatch and left
// out of the definitions is a tool no model ever calls.
func TestTheOrchestrationToolsAreRegisteredTogether(t *testing.T) {
	have := map[string]bool{}
	for _, d := range Definitions(nil) {
		have[d.Name] = true
	}
	for _, want := range []string{SpawnToolName, ReportToolName, SteerToolName, RetryToolName} {
		if !have[want] {
			t.Fatalf("%s is not registered with the others: %v", want, have)
		}
	}
}

// The roster's steer field is empty for two opposite reasons — a child on
// task, and a child nothing is reading — and only one of them is good news.
// A session that turned readings off gets the difference said once, at the
// top, together with what is left to judge a child by.
func TestRosterSaysWhenNothingIsReadingTheChildren(t *testing.T) {
	sup := newTestSupervisor(t, &scriptedEnv{steps: []streamStep{{text: "done"}}})
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"survey the exporter"}`)
	execTool(t, sup, ReportToolName, `{"name":"researcher-1"}`)

	roster := execTool(t, sup, ReportToolName, `{}`)
	if !strings.Contains(roster, "Readings are off") {
		t.Fatalf("an unread fan-out must say so on the roster:\n%s", roster)
	}
	// The trigger agent_steer names when it cannot name a steer count.
	if !strings.Contains(roster, "has not moved") {
		t.Fatalf("the header must leave the parent something to act on:\n%s", roster)
	}
}

// And a session on the defaults is told nothing about readings: the trigger
// agent_steer names first — a child listed as steered — is reachable, so a
// header saying the mechanism is on would be a line the parent learns to skip.
func TestRosterSaysNothingAboutReadingsWhenAChildIsRead(t *testing.T) {
	sup := judgedChild(t, &readingProvider{state: "off_target"}, 40)
	execTool(t, sup, ReportToolName, `{"name":"researcher-1"}`)

	roster := execTool(t, sup, ReportToolName, `{}`)
	if strings.Contains(roster, "Readings are off") {
		t.Fatalf("a read child must not be reported as unread:\n%s", roster)
	}
	if !strings.Contains(roster, plural(statusOf(t, sup, "researcher-1").Steers, "steer")) {
		t.Fatalf("the steer count is the trigger the roster leaves standing:\n%s", roster)
	}
}

// Where a steer came from is the question a count alone cannot answer once
// more than one party can steer. The word is the same on the status, the
// roster and the lane, and it is the last steer's own — a redirect the child
// has already taken up left the count it answered at zero.
func TestAStatusSaysWhereTheLastSteerCameFrom(t *testing.T) {
	c := &child{name: "researcher-1", role: RoleResearcher}
	if got := steerMark(c.status()); got != "" {
		t.Fatalf("a child nobody has steered says nothing, got %q", got)
	}

	c.steers, c.verdict, c.steerFrom = 2, agent.SummaryOffTarget.String(), SteerFromReading
	st := c.status()
	if st.SteerFrom != SteerFromReading {
		t.Fatalf("the status carries the source, got %q", st.SteerFrom)
	}
	if got := steerMark(st); !strings.Contains(got, "2 steers") || !strings.Contains(got, "from reading") {
		t.Fatalf("the roster says the count and its source, got %q", got)
	}

	// The orchestrator's redirect resets the count it answered, and the
	// source is what is left saying anyone spoke to the child at all.
	c.steers, c.steerFrom = 0, SteerFromParent
	if got := steerMark(c.status()); !strings.Contains(got, "steered from parent") {
		t.Fatalf("the roster says who redirected the child, got %q", got)
	}
}

// waitFor blocks until cond holds, so a test can state the state it is
// waiting for rather than the sleep it guessed at.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for !cond() {
		select {
		case <-deadline:
			t.Fatal("the condition never held")
		default:
		}
		time.Sleep(time.Millisecond)
	}
}

// A child whose turn was cancelled is idle, and a message to it is the next
// turn rather than an addition to one. The tool says which of the two it did,
// because "queued for its next round" would tell the orchestrator to wait for
// a boundary that is not coming.
func TestSteerToolStartsTheNextTurnOfAnIdleChild(t *testing.T) {
	sup := New(t.Context(), Options{Root: t.TempDir(), NewEnv: resumableEnv("resumed and finished")})
	t.Cleanup(sup.Close)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"long survey"}`)

	waitState(t, sup, "researcher-1", StateRunning)
	if err := sup.CancelTurn("researcher-1"); err != nil {
		t.Fatal(err)
	}
	waitState(t, sup, "researcher-1", StateIdle)

	out := execTool(t, sup, SteerToolName, `{"name":"researcher-1","message":"continue please"}`)
	if !strings.Contains(out, "cancelled") || !strings.Contains(out, "next one") {
		t.Fatalf("the result should say the message starts the next turn, got %q", out)
	}
	report := execTool(t, sup, ReportToolName, `{"name":"researcher-1"}`)
	if !strings.Contains(report, "resumed and finished") {
		t.Fatalf("the orchestrator's steer should resume the child, got: %s", report)
	}
	if st := statusOf(t, sup, "researcher-1"); st.SteerFrom != SteerFromParent {
		t.Fatalf("the status should say the parent spoke last, got %q", st.SteerFrom)
	}
}

// The roster's own facts reach the report too, because the parent reads the
// two minutes apart and acts on the second: a child that was steered twice
// and comes back calling its work done is a report to check rather than to
// take, and nothing else in the report says so.
func TestAReportCarriesTheReadingAndTheSteerCount(t *testing.T) {
	sup := judgedChild(t, &readingProvider{state: "off_target"}, 40)
	report := execTool(t, sup, ReportToolName, `{"name":"researcher-1"}`)

	st := statusOf(t, sup, "researcher-1")
	for _, want := range []string{agent.SummaryOffTarget.String(), plural(st.Steers, "steer")} {
		if !strings.Contains(report, want) {
			t.Fatalf("the report does not say %q:\n%s", want, report)
		}
	}
	if strings.Contains(report, "reading the importer") {
		t.Fatalf("the report carries the reading's word and never its prose:\n%s", report)
	}
}

// A task that outgrew the interval its spawn chose covered more ground than
// the spawn asked for, and the count is the only thing that says so — the
// check-ins themselves are on the child's own transcript, which the parent
// never sees.
func TestAReportCountsTheCheckInsAChildTookStockAt(t *testing.T) {
	env := &scriptedEnv{steps: []streamStep{
		{calls: []provider.ToolCall{{ID: "c1", Name: "read_file", Arguments: `{"path":"x"}`}}},
		{text: "surveyed the loop"},
	}}
	sup := newTestSupervisor(t, env)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"survey the loop","max_rounds":1}`)

	report := execTool(t, sup, ReportToolName, `{"name":"researcher-1"}`)
	if !strings.Contains(report, "1 check-in") {
		t.Fatalf("the report must count the check-in the child took stock at:\n%s", report)
	}
}

// The ceiling is otherwise something the parent finds out by having a spawn
// refused — a round spent, on a plan for a fan-out that was never going to
// fit — so the roster states it before the refusal does.
func TestRosterSaysHowManyAgentSlotsAreUsed(t *testing.T) {
	env := &scriptedEnv{steps: []streamStep{{text: "surveyed"}}}
	sup := newTestSupervisor(t, env)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"survey the loop"}`)
	execTool(t, sup, ReportToolName, `{"name":"researcher-1"}`)

	roster := execTool(t, sup, ReportToolName, `{}`)
	if !strings.Contains(roster, fmt.Sprintf("1 of %d agent slots used", MaxChildren)) {
		t.Fatalf("the roster must say what is left of the session's spawns:\n%s", roster)
	}
	// A finished agent keeps its slot, which is the half a reader assumes
	// the other way round.
	if !strings.Contains(roster, "keeps its slot") {
		t.Fatalf("the roster must say a finished agent keeps its slot:\n%s", roster)
	}
	if got := slotsLine(MaxChildren); !strings.Contains(got, "can spawn no more") {
		t.Fatalf("a session at the ceiling must be told so, got %q", got)
	}
}

// writingChild is a child whose first round writes files inside its own
// workspace and whose second answers. It is scripted where a real writer's
// model is, and real everywhere else: the files are on disk, the patch is the
// one git computes out of them, and the note under test is written about that.
type writingChild struct {
	paths []string

	mu    sync.Mutex
	round int
}

func (w *writingChild) factory() EnvFactory {
	return func(ctx context.Context, spec Spec) (Env, error) {
		stream := func([]provider.Message, string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
			w.mu.Lock()
			w.round++
			round := w.round
			w.mu.Unlock()
			ch := make(chan provider.StreamEvent, 2)
			if round == 1 {
				ch <- provider.StreamEvent{ToolCalls: []provider.ToolCall{
					{ID: "w1", Name: "write_file", Arguments: `{"path":"exporter.go"}`},
				}}
			} else {
				ch <- provider.StreamEvent{Token: "wrote the exporter"}
				ch <- provider.StreamEvent{Done: true}
			}
			close(ch)
			return ch, func() {}, nil
		}
		return Env{
			SystemPrompt: "sys",
			Stream:       stream,
			Executor: func(string, json.RawMessage) (string, error) {
				for _, p := range w.paths {
					full := filepath.Join(spec.Root, p)
					if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
						return "", err
					}
					if err := os.WriteFile(full, []byte("package x\n"), 0o644); err != nil {
						return "", err
					}
				}
				return "written", nil
			},
		}, nil
	}
}

// What a patch touched is the whole of what the parent needs to integrate it,
// and the supervisor has it in hand: a note that gave only a count sent the
// parent to `git status` for the names, one round and one approval after the
// files had already landed.
func TestAPatchNoteNamesTheFilesItTouched(t *testing.T) {
	repo := initTestRepo(t)
	ctx, cancel := context.WithCancel(context.Background())
	w := &writingChild{paths: []string{"exporter.go", "internal/csv/writer.go"}}
	sup := New(ctx, Options{Root: repo, NewEnv: w.factory()})
	// Cancelled first and closed second: the goroutine below reads the
	// supervisor's events, and one closed while it still had a reader would
	// leave it spinning on a shut channel.
	t.Cleanup(sup.Close)
	t.Cleanup(cancel)
	go func() {
		for {
			select {
			case ev := <-sup.Events():
				if ev.Kind == EventAsk {
					ev.Ask.Respond(true)
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	execTool(t, sup, SpawnToolName, `{"role":"writer","task":"add the CSV exporter"}`)
	report := execTool(t, sup, ReportToolName, `{"name":"writer-1"}`)
	for _, want := range []string{"patch applied to the workspace", "exporter.go", "internal/csv/writer.go"} {
		if !strings.Contains(report, want) {
			t.Fatalf("the patch note does not say %q:\n%s", want, report)
		}
	}
}

// A patch of two hundred files is a page of the parent's context spent on a
// list it would then have to summarise; the count beside the names says the
// size either way.
func TestAPatchNoteBoundsAVeryLongFileList(t *testing.T) {
	files := make([]string, maxNotedPatchPaths+5)
	for i := range files {
		files[i] = fmt.Sprintf("pkg/file%d.go", i)
	}
	got := patchPaths(files)
	if !strings.Contains(got, "and 5 more") {
		t.Fatalf("a long list must say how many it left out, got %q", got)
	}
	if strings.Contains(got, files[maxNotedPatchPaths]) {
		t.Fatalf("the list is not bounded: %q", got)
	}
}

// spendingRounds is a script of n rounds that each read a file and take in
// tokens doing it, for a child whose budget is what ends it rather than its
// rounds.
func spendingRounds(n int, perRound int) []streamStep {
	steps := toolRounds(n)
	for i := range steps {
		steps[i].usage = &provider.Usage{PromptTokens: perRound}
	}
	return steps
}

// A child dies of tokens, not of rounds, and the round interval cannot see
// that coming: this one spends its whole budget inside four rounds, twenty-one
// short of the interval it would have been asked at.
func TestChildIsAskedOnItsBudgetLongBeforeItsRounds(t *testing.T) {
	env := &scriptedEnv{steps: spendingRounds(12, 30_000)}
	sup := newTestSupervisor(t, env)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"survey the exporter","max_tokens":300000}`)
	waitState(t, sup, "researcher-1", StateFailed)

	if !env.asked("routine check-in") {
		t.Error("the child spent its whole budget without being asked anything")
	}
	if st := statusOf(t, sup, "researcher-1"); st.CheckIns != 0 {
		t.Errorf("check-ins counted %d — the budget clock is the interval's, not the round cap's", st.CheckIns)
	}
}

// The question the budget puts names what the child has to show for the
// spending, because spending is not progress and nothing else it is ever
// asked can tell the two apart.
func TestABudgetCheckInAsksAReadingChildWhatItHasWritten(t *testing.T) {
	env := &scriptedEnv{steps: spendingRounds(12, 30_000)}
	sup := newTestSupervisor(t, env)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"survey the exporter","max_tokens":300000}`)
	waitState(t, sup, "researcher-1", StateFailed)

	if !env.asked("not written to any file") {
		t.Error("a child that spent a quarter of its budget reading was not asked about that")
	}
	if !env.asked("final report") {
		t.Error("a budget check-in must still offer a child its own exit")
	}
}

// A child that ran out of budget having written nothing spent a whole budget
// on reading, and one that ran out mid-edit did not. The parent reads one
// line about a failed child, and retrying is a different decision for each.
func TestABudgetFailureSaysWhatTheChildHadWritten(t *testing.T) {
	env := &scriptedEnv{steps: spendingRounds(12, 30_000)}
	sup := newTestSupervisor(t, env)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"survey the exporter","max_tokens":300000}`)
	waitState(t, sup, "researcher-1", StateFailed)

	st := statusOf(t, sup, "researcher-1")
	if !strings.Contains(st.Detail, "token budget") || !strings.Contains(st.Detail, "nothing written") {
		t.Errorf("a budget failure should say what it had to show for the budget, got %q", st.Detail)
	}
}

// searchRounds is a script of n searches over one directory, each asking a
// different pattern: the sweep, which is not an exact repeat of anything and
// which every row of the digest reads as its own new question.
func searchRounds(n int) []streamStep {
	steps := make([]streamStep, 0, n)
	for i := range n {
		steps = append(steps, streamStep{calls: []provider.ToolCall{{
			ID:        fmt.Sprintf("q%d", i),
			Name:      "search",
			Arguments: fmt.Sprintf(`{"pattern":"handler%d","path":"internal/exporter"}`, i),
		}}})
	}
	return steps
}

// The reading is the only thing watching a child, and the child it read as on
// target twenty times was one that had searched one directory for twenty
// rounds and written nothing. The evidence that tells that child from one
// doing the work has to reach the reading, which is where the judgement is
// made.
func TestAReadingOfAChildThatOnlyReadsIsGivenTheSweep(t *testing.T) {
	reader := &readingProvider{state: "on_target"}
	env := &scriptedEnv{
		steps:   append(searchRounds(20), streamStep{text: "surveyed the exporter"}),
		delay:   3 * time.Millisecond,
		repeats: agent.NewRepeatDetector(),
		summarizer: agent.NewSummarizer(reader,
			agent.SummaryConfig{Model: "fast", IntervalRounds: 10, MinGap: -1, InterveneCooldownIntervals: 1}),
	}
	sup := newTestSupervisor(t, env)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"survey the exporter"}`)
	waitState(t, sup, "researcher-1", StateDone)

	var swept string
	for _, d := range reader.judgedAgainst() {
		if strings.Contains(d, "sweeps") {
			swept = d
		}
	}
	if swept == "" {
		t.Fatalf("no reading of a twenty-round sweep was given one, digests: %v", reader.judgedAgainst())
	}
	for _, want := range []string{"./internal/exporter", "nothing written"} {
		if !strings.Contains(swept, want) {
			t.Errorf("the sweep the reading was given does not say %q:\n%s", want, swept)
		}
	}
}
