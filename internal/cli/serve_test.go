package cli

// What a client that is not shhh's own terminal gets when it drives the
// agent, asserted against the built binary for the same reason the unattended
// run's contract is: the protocol is a promise made to another process, and
// one checked a function call away from the command that serves it can be
// broken anywhere along the way with none of these noticing.

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/rpc"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/tools"
)

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}

// A socket that is already there is refused rather than replaced: it is
// either a server that is still running, whose clients would silently stop
// being served, or the remains of one that died, and unlinking on the
// person's behalf is how the first case becomes the second.
func TestServeOnSocket_RefusesAPathThatIsAlreadyThere(t *testing.T) {
	path := filepath.Join(t.TempDir(), "taken")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	err := serveOnSocket(context.Background(), rpc.NewServer(nil), path)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("listening on a path that is taken answered %v", err)
	}
}

// childStep is one scripted reply a spawned child is answered with, in order.
type childStep struct {
	text  string
	calls []provider.ToolCall
}

// scriptedChildren stands in for the provider behind a session's children, so
// a test can drive a served session's fan-out without one. It is the smallest
// environment a child runs against: a stream that replays steps, a dispatcher
// for the calls that run on their own, and one tool named as gated so a
// request is routed up at all.
type scriptedChildren struct {
	mu    sync.Mutex
	steps []childStep
}

func (s *scriptedChildren) factory() subagent.EnvFactory {
	return func(ctx context.Context, _ subagent.Spec) (subagent.Env, error) {
		stream := func(_ []provider.Message, _ string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
			if ctx.Err() != nil {
				return nil, nil, ctx.Err()
			}
			s.mu.Lock()
			if len(s.steps) == 0 {
				s.mu.Unlock()
				return nil, nil, context.Canceled
			}
			step := s.steps[0]
			s.steps = s.steps[1:]
			s.mu.Unlock()

			ch := make(chan provider.StreamEvent, 2)
			if step.text != "" {
				ch <- provider.StreamEvent{Token: step.text}
			}
			if len(step.calls) > 0 {
				ch <- provider.StreamEvent{ToolCalls: step.calls}
			} else {
				ch <- provider.StreamEvent{Done: true}
			}
			close(ch)
			_, cancel := context.WithCancel(context.Background())
			return ch, cancel, nil
		}
		return subagent.Env{
			SystemPrompt: "a scripted child",
			Stream:       stream,
			Executor:     func(name string, _ json.RawMessage) (string, error) { return "auto:" + name, nil },
			ExecuteGated: func(name string, _ json.RawMessage) (string, error) { return "gated:" + name, nil },
			RunCommand:   func(context.Context, string) (string, int) { return "ok", 0 },
			Gated:        map[string]bool{tools.ExecCommandName: true},
		}, nil
	}
}

// spawnScripted builds a supervisor over scripted children and starts one,
// answering with the name the session gave it.
func spawnScripted(t *testing.T, steps ...childStep) (*subagent.Supervisor, string) {
	t.Helper()
	env := &scriptedChildren{steps: steps}
	sup := subagent.New(t.Context(), subagent.Options{Root: t.TempDir(), NewEnv: env.factory()})
	t.Cleanup(sup.Close)
	exec := sup.WrapExecutor("", func(string, json.RawMessage) (string, error) {
		return "", context.Canceled
	})
	if _, err := exec(subagent.SpawnToolName,
		json.RawMessage(`{"role":"researcher","task":"survey the exporter"}`)); err != nil {
		t.Fatalf("spawning a child: %v", err)
	}
	return sup, "researcher-1"
}

// syncLines is where a test reads the event stream from. The stream is written
// from the supervisor's reader goroutine while the test goroutine reads it, so
// the two share a lock rather than a bare buffer.
type syncLines struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *syncLines) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *syncLines) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

// waitFor polls until want is true or the case has waited long enough to say
// it never will. A child runs on goroutines of its own and nothing here is
// answering a request, so there is no other moment to read.
func waitFor(t *testing.T, why string, want func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		if want() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", why)
}

// A client driving a served session is told about the children its turn
// spawns, and is put the requests they route up under the name of the agent
// that raised them. Both go through the seams the assembly wires: the event
// stream a client reads everything else on, and the approval queue the turn's
// own calls go through.
func TestAServedSessionsClientIsToldAboutItsChildren(t *testing.T) {
	sup, name := spawnScripted(t,
		childStep{calls: []provider.ToolCall{{
			ID: "1", Name: tools.ExecCommandName, Arguments: `{"command":"go test ./..."}`}}},
		childStep{text: "the exporter is fine"})

	lines := &syncLines{}
	events := newJSONLStream(lines)
	routed := make(chan *subagent.Ask, 4)
	answerChildAsks(sup, true, nil,
		func(as *subagent.Ask) bool { routed <- as; return true },
		func(st subagent.Status) {
			parent, _ := sup.Parent(st.Name)
			events.agent(childPos(1), agentLine(st, parent))
		})

	select {
	case as := <-routed:
		if as.Agent != name {
			t.Errorf("a routed request named %q, want %s", as.Agent, name)
		}
		if as.Tool != tools.ExecCommandName || !strings.Contains(as.Arguments, "go test") {
			t.Errorf("a routed request lost the call it is about: tool %q args %q", as.Tool, as.Arguments)
		}
		if as.Title == "" {
			t.Error("a routed request reached the client with nothing to say it is")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the child's request never reached the client")
	}

	waitFor(t, "the child to be reported finished", func() bool {
		return strings.Contains(lines.String(), `"state":"done"`)
	})

	// Every line is one of the run's own events, in the kind the record
	// spells, and says which child it is about.
	var sawAgent int
	for _, line := range strings.Split(strings.TrimSpace(lines.String()), "\n") {
		var ev struct {
			Kind  string `json:"kind"`
			Turn  int64  `json:"turn"`
			Round int64  `json:"round"`
			Agent *struct {
				Name  string `json:"name"`
				Role  string `json:"role"`
				State string `json:"state"`
				Task  string `json:"task"`
			} `json:"agent"`
		}
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("the stream wrote a line that is not an event: %q (%v)", line, err)
		}
		if ev.Kind != observe.EventAgent {
			t.Fatalf("a child's state change went out as %q", ev.Kind)
		}
		if ev.Agent == nil || ev.Agent.Name != name || ev.Agent.Role != string(subagent.RoleResearcher) {
			t.Fatalf("an agent line does not say which child it is about: %q", line)
		}
		if ev.Agent.Task != "survey the exporter" || ev.Turn != 1 {
			t.Fatalf("an agent line lost what the child was asked for, or where: %q", line)
		}
		// And no round: a child does not act in a round of its parent's
		// turn, and the counter it would have read is the parent's own
		// goroutine's.
		if ev.Round != 0 {
			t.Fatalf("an agent line filed the child under a round of the parent's turn: %q", line)
		}
		sawAgent++
	}
	if sawAgent < 2 {
		t.Errorf("a child that was spawned, blocked, ran and finished produced %d lines", sawAgent)
	}
}

// The two calls a client makes about a child reach the two supervisor verbs
// shhh's own screen calls, under the source the person at a lane speaks with.
// A client is the person: there is nobody else on the other end of a socket.
func TestAServedSessionsClientSteersAChildAndEndsOne(t *testing.T) {
	sup, name := spawnScripted(t,
		childStep{calls: []provider.ToolCall{{
			ID: "1", Name: tools.ExecCommandName, Arguments: `{"command":"sleep 1"}`}}},
		childStep{text: "done"})

	// The routed request is what holds the child still: it is blocked on a
	// client that has not answered, which is where a person steers or ends
	// one. Released at the end so the child is not left waiting on a test.
	answered := make(chan struct{})
	t.Cleanup(func() { close(answered) })
	answerChildAsks(sup, true, nil, func(*subagent.Ask) bool {
		<-answered
		return false
	}, nil)
	waitFor(t, "the child to block on its request", func() bool {
		st, ok := sup.Get(name)
		return ok && st.State == subagent.StateBlocked
	})

	l := &serveLoop{agents: sup}
	if err := l.SteerAgent(name, "leave the goldens alone"); err != nil {
		t.Fatalf("steering a blocked child: %v", err)
	}
	if got := sup.QueuedSteering(name); got != 1 {
		t.Errorf("the child has %d messages waiting, want 1", got)
	}
	if st, _ := sup.Get(name); st.SteerFrom != subagent.SteerFromLane {
		t.Errorf("a client's steer was recorded as %q, want %q", st.SteerFrom, subagent.SteerFromLane)
	}

	if err := l.KillAgent(name); err != nil {
		t.Fatalf("ending a child: %v", err)
	}
	waitFor(t, "the child to end", func() bool {
		st, ok := sup.Get(name)
		return ok && (st.State == subagent.StateFailed || st.State == subagent.StateDone)
	})

	// And what a client is told about a name there is nothing left to reach:
	// the supervisor's own sentence, which is what the protocol forwards.
	err := l.SteerAgent(name, "one more thing")
	if err == nil || !strings.Contains(err.Error(), "nothing to steer") {
		t.Errorf("steering a child that has ended answered %v", err)
	}
	if err := l.KillAgent("researcher-9"); err == nil || !strings.Contains(err.Error(), "researcher-9") {
		t.Errorf("ending an agent the session never had answered %v", err)
	}
	if err := (&serveLoop{}).KillAgent(name); err == nil {
		t.Error("a session with no supervisor answered a kill")
	}
}

// The agent line is the supervisor's record copied field for field: what a
// client draws from it is what shhh's own lane and map draw, and a line that
// summarised would be deciding which surface a client may build.
func TestAgentLineCarriesTheRecordALaneIsDrawnFrom(t *testing.T) {
	st := subagent.Status{
		Name: "writer-2", Role: subagent.RoleWriter, Task: "port the exporter",
		Model: "some-model", Paths: []string{"internal/observe"},
		State: subagent.StateRunning, Detail: "editing otel.go", ToolCalls: 7,
		Budget: 300_000, Batch: 3, Step: 2, Steps: 5,
		Elapsed: 90 * time.Second, Summary: "ported", Verdict: "on-target",
		End: observe.ChildKilled, Steers: 1, SteerFrom: subagent.SteerFromLane, Held: true,
	}
	st.Tokens.Fresh = 42_000

	line := agentLine(st, "writer-1")
	want := jsonAgent{
		Name: "writer-2", Parent: "writer-1", Role: "writer", State: "running",
		Task: "port the exporter", Model: "some-model", Detail: "editing otel.go",
		Paths: []string{"internal/observe"}, Batch: 3, Step: 2, Steps: 5, ToolCalls: 7,
		ElapsedMS: 90_000, Budget: 300_000, Tokens: 42_000,
		Summary: "ported", Verdict: "on-target", End: observe.ChildKilled,
		Steers: 1, SteerFrom: "lane", Held: true,
	}
	got, _ := json.Marshal(line)
	wantJSON, _ := json.Marshal(want)
	if string(got) != string(wantJSON) {
		t.Errorf("the agent line is\n%s\nwant\n%s", got, wantJSON)
	}
}

// heldSummaries answers a reading only once the test lets it, so a closing
// reading can be kept in flight across the start of the next turn.
type heldSummaries struct {
	asked   chan struct{}
	release chan struct{}
}

func (p *heldSummaries) StreamCompletion(context.Context, []provider.Message, provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
	p.asked <- struct{}{}
	<-p.release
	ch := make(chan provider.StreamEvent, 1)
	ch <- provider.StreamEvent{
		ToolCalls: []provider.ToolCall{{ID: "s1", Name: agent.SummaryToolName,
			Arguments: `{"summary":"reading","state":"on_target","reason":"a reason"}`}},
		Done: true,
	}
	close(ch)
	return ch, nil
}

func (p *heldSummaries) Name() string { return "held" }

// A served turn's closing reading lands after Run has returned, and a client
// may have started the next turn by then. The reading is about the turn it
// read, so its row is filed there and not beside the next turn's events.
func TestServedClosingReadingIsFiledUnderItsOwnTurn(t *testing.T) {
	rounds := [][]provider.StreamEvent{
		{{ToolCalls: []provider.ToolCall{{ID: "c1", Name: "read_file", Arguments: `{"path":"x"}`}}}},
		{{ToolCalls: []provider.ToolCall{{ID: "c2", Name: "read_file", Arguments: `{"path":"y"}`}}}},
		{{Token: "first"}, {Done: true}},
		{{Token: "second"}, {Done: true}},
	}
	var next int
	a := agent.New(nil, func([]provider.Message, string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		if next >= len(rounds) {
			t.Fatalf("unexpected stream request #%d", next+1)
		}
		ch := make(chan provider.StreamEvent, len(rounds[next]))
		for _, ev := range rounds[next] {
			ch <- ev
		}
		close(ch)
		next++
		return ch, func() {}, nil
	})
	a.SetExecutor(func(string, json.RawMessage) (string, error) { return "contents", nil })

	held := &heldSummaries{asked: make(chan struct{}, 1), release: make(chan struct{})}
	lines := &syncLines{}
	l := &serveLoop{
		agent:  a,
		events: newJSONLStream(lines),
		own:    &writtenByCalls{},
		saved:  &headlessChat{},
		// A negative floor removes the wall clock from the schedule, which a
		// test does not wait out.
		summarizer: agent.NewSummarizer(held, agent.SummaryConfig{Model: "fast", MinGap: -1}),
	}
	l.obs = headlessObserver{rounds: a.Rounds, turn: l.turnNow, stream: l.events}
	l.headless = &agent.Headless{Agent: a, OnSummary: l.obs.summary}

	if _, err := l.Run(1, "read two files"); err != nil {
		t.Fatalf("the first turn: %v", err)
	}
	select {
	case <-held.asked:
	case <-time.After(10 * time.Second):
		t.Fatal("the first turn closed without asking for a reading")
	}
	if _, err := l.Run(2, "and answer"); err != nil {
		t.Fatalf("the second turn: %v", err)
	}
	close(held.release)

	var turn, round int64
	waitFor(t, "the closing reading to be filed", func() bool {
		for _, line := range strings.Split(strings.TrimSpace(lines.String()), "\n") {
			var ev struct {
				Kind  string `json:"kind"`
				Code  string `json:"code"`
				Turn  int64  `json:"turn"`
				Round int64  `json:"round"`
			}
			if json.Unmarshal([]byte(line), &ev) == nil && ev.Kind == observe.EventSignal && ev.Code == observe.SignalSummary {
				turn, round = ev.Turn, ev.Round
				return true
			}
		}
		return false
	})
	if turn != 1 || round != 2 {
		t.Fatalf("the closing reading was filed at turn %d round %d, want turn 1 round 2", turn, round)
	}
}
