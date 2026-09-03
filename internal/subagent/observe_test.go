package subagent

import (
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/provider"
)

// recordedEvent is one thing a child reported, flattened so a test can state
// the whole shape it expected on one line.
type recordedEvent struct {
	kind    string
	tool    string
	outcome string
	reason  string
	pos     observe.Pos
	// timed says a duration was reported at all, which is the fact worth
	// asserting: a call's wall time is not reproducible, and its presence is.
	timed bool
}

// testRecorder collects what one child reports, standing in for the CLI's
// store-backed recorder.
type testRecorder struct {
	mu        sync.Mutex
	sysPrompt string
	events    []recordedEvent
	turns     int64
	tokensIn  int64
	tokensOut int64
	priced    bool
	ended     bool
}

func (r *testRecorder) recorder() Recorder {
	return Recorder{
		Observer: observe.Observer{
			Usage: func(turns, in, out int64, _ float64, priced bool) {
				r.mu.Lock()
				defer r.mu.Unlock()
				r.turns, r.tokensIn, r.tokensOut, r.priced = turns, in, out, priced
			},
			ToolCall: func(at observe.Pos, tool string, d time.Duration, outcome, class string) {
				r.add(recordedEvent{kind: "tool", tool: tool, outcome: outcome, reason: class, pos: at, timed: d > 0})
			},
			Turn: func(turn, rounds int64, d time.Duration, outcome string) {
				r.add(recordedEvent{kind: "turn", outcome: outcome, pos: observe.Pos{Turn: turn, Round: rounds}, timed: d > 0})
			},
			Signal: func(at observe.Pos, code, reason string) {
				r.add(recordedEvent{kind: "signal", outcome: code, reason: reason, pos: at})
			},
			Decision: func(at observe.Pos, decision, reason string) {
				r.add(recordedEvent{kind: "decision", outcome: decision, reason: reason, pos: at})
			},
		},
		End: func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.ended = true
		},
	}
}

func (r *testRecorder) add(e recordedEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *testRecorder) all() []recordedEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]recordedEvent(nil), r.events...)
}

func (r *testRecorder) of(kind string) []recordedEvent {
	var out []recordedEvent
	for _, e := range r.all() {
		if e.kind == kind {
			out = append(out, e)
		}
	}
	return out
}

// supervisorRecording is a supervisor whose one child reports to rec.
func supervisorRecording(t *testing.T, env *scriptedEnv, rec *testRecorder) *Supervisor {
	t.Helper()
	sup := New(t.Context(), Options{
		Root:   t.TempDir(),
		NewEnv: env.factory(),
		Record: func(_ Spec, sysPrompt string) Recorder {
			rec.mu.Lock()
			rec.sysPrompt = sysPrompt
			rec.mu.Unlock()
			return rec.recorder()
		},
	})
	t.Cleanup(sup.Close)
	return sup
}

// A child's tool events are placed and timed the way a session's are. Without
// a position a child's forty searches in one turn read as forty sessions'
// worth of searching, which is the shape the record exists to show.
func TestChildRecordsToolCallsWithPositionAndClass(t *testing.T) {
	env := &scriptedEnv{steps: []streamStep{
		{calls: []provider.ToolCall{{ID: "c1", Name: "read_file", Arguments: `{"path":"x"}`}}},
		{text: "found it", usage: &provider.Usage{PromptTokens: 40, CompletionTokens: 6}},
	}}
	rec := &testRecorder{}
	sup := supervisorRecording(t, env, rec)

	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"survey the code"}`)
	execTool(t, sup, ReportToolName, `{"name":"researcher-1"}`)

	calls := rec.of("tool")
	if len(calls) != 1 {
		t.Fatalf("expected one tool event, got %+v", calls)
	}
	c := calls[0]
	if c.tool != "read_file" || c.outcome != observe.OutcomeOK || c.reason != "" {
		t.Fatalf("unexpected tool event: %+v", c)
	}
	if c.pos.Turn != 1 || c.pos.Round != 1 {
		t.Fatalf("tool event at %+v, want turn 1 round 1", c.pos)
	}
	if !c.timed {
		t.Fatal("tool event carries no duration")
	}
}

// A failed call carries its class, so a child's failures can be told apart
// the way a session's are.
func TestChildRecordsToolErrorClass(t *testing.T) {
	env := &scriptedEnv{
		gated: map[string]bool{"read_file": true},
		steps: []streamStep{
			{calls: []provider.ToolCall{{ID: "c1", Name: "read_file", Arguments: `{"path":"x"}`}}},
			{text: "gave up"},
		},
	}
	rec := &testRecorder{}
	sup := supervisorRecording(t, env, rec)

	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"survey the code"}`)
	nextAsk(t, sup).Respond(false)
	execTool(t, sup, ReportToolName, `{"name":"researcher-1"}`)

	calls := rec.of("tool")
	if len(calls) != 1 {
		t.Fatalf("expected one tool event, got %+v", calls)
	}
	if calls[0].outcome != observe.OutcomeError || calls[0].reason != observe.ClassDeclined {
		t.Fatalf("unexpected tool event: %+v", calls[0])
	}

	// Two events, as a session records: what put the call in front of a
	// person, and what they said.
	decisions := rec.of("decision")
	if len(decisions) != 2 {
		t.Fatalf("expected the ask and the answer, got %+v", decisions)
	}
	if decisions[0].outcome != observe.DecisionAsk || decisions[0].reason != observe.ReasonPolicy {
		t.Fatalf("unexpected ask: %+v", decisions[0])
	}
	if decisions[1].outcome != observe.DecisionDeny || decisions[1].reason != observe.ReasonUser {
		t.Fatalf("unexpected answer: %+v", decisions[1])
	}
	if decisions[0].pos.Turn != 1 || decisions[1].pos.Turn != 1 {
		t.Fatalf("decisions are unplaced: %+v", decisions)
	}
}

// A child closes with a turn event and ends its own row: the whole of what a
// session reports about how a turn went, from a surface nobody watched.
func TestChildClosesWithATurnEvent(t *testing.T) {
	env := &scriptedEnv{steps: []streamStep{
		{calls: []provider.ToolCall{{ID: "c1", Name: "read_file", Arguments: `{"path":"x"}`}}},
		{text: "found it", usage: &provider.Usage{PromptTokens: 40, CompletionTokens: 6}},
	}}
	rec := &testRecorder{}
	sup := supervisorRecording(t, env, rec)

	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"survey the code"}`)
	execTool(t, sup, ReportToolName, `{"name":"researcher-1"}`)

	turns := rec.of("turn")
	if len(turns) != 1 {
		t.Fatalf("expected one turn event, got %+v", turns)
	}
	if turns[0].outcome != observe.TurnDone || turns[0].pos.Turn != 1 || turns[0].pos.Round != 1 {
		t.Fatalf("unexpected turn event: %+v", turns[0])
	}
	if !rec.ended {
		t.Fatal("the child's row was never ended")
	}
	if rec.turns != 1 || rec.tokensIn != 40 || rec.tokensOut != 6 {
		t.Fatalf("unexpected totals: turns=%d in=%d out=%d", rec.turns, rec.tokensIn, rec.tokensOut)
	}
	// A child runs its whole life on one model, so its totals go out
	// unpriced for the recorder to price at that model.
	if rec.priced {
		t.Fatal("a child's totals must arrive unpriced")
	}
}

// A child is stamped with the prompt it actually ran under. Inheriting the
// parent's would put a child that runs a different prompt on the parent's
// side of an edit that never touched it.
func TestChildRecordIsGivenItsOwnSystemPrompt(t *testing.T) {
	env := &scriptedEnv{steps: []streamStep{{text: "done"}}}
	rec := &testRecorder{}
	sup := supervisorRecording(t, env, rec)

	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"survey the code"}`)
	execTool(t, sup, ReportToolName, `{"name":"researcher-1"}`)

	rec.mu.Lock()
	got := rec.sysPrompt
	rec.mu.Unlock()
	if got != "test system prompt" {
		t.Fatalf("child stamped with %q, want its own system prompt", got)
	}
}

// A child that reaches its round cap reports the turn that reached it as
// paused and carries on, which is what a session's cap does.
func TestChildRecordsTheRoundCapAsAPausedTurn(t *testing.T) {
	env := &scriptedEnv{steps: []streamStep{
		{calls: []provider.ToolCall{{ID: "c1", Name: "read_file", Arguments: `{"path":"x"}`}}},
		{text: "taking stock"},
	}}
	rec := &testRecorder{}
	sup := supervisorRecording(t, env, rec)

	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"survey the code","max_rounds":1}`)
	execTool(t, sup, ReportToolName, `{"name":"researcher-1"}`)

	turns := rec.of("turn")
	if len(turns) != 2 {
		t.Fatalf("expected the capped turn and the check-in, got %+v", turns)
	}
	if turns[0].outcome != observe.TurnCapPaused || turns[0].pos.Turn != 1 {
		t.Fatalf("unexpected first turn event: %+v", turns[0])
	}
	if turns[1].outcome != observe.TurnDone || turns[1].pos.Turn != 2 {
		t.Fatalf("unexpected second turn event: %+v", turns[1])
	}
}

// Every string a child stores is a fixed identifier or a code from a closed
// set — the guarantee the whole record rests on, now that a child writes to
// the same table a session does.
func TestChildRecordsNothingOutsideTheClosedSets(t *testing.T) {
	env := &scriptedEnv{steps: []streamStep{
		{calls: []provider.ToolCall{{ID: "c1", Name: "read_file", Arguments: `{"path":"/home/someone/secrets"}`}}},
		{text: "found it"},
	}}
	rec := &testRecorder{}
	sup := supervisorRecording(t, env, rec)

	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"read /home/someone/secrets"}`)
	execTool(t, sup, ReportToolName, `{"name":"researcher-1"}`)

	for _, e := range rec.all() {
		for _, s := range []string{e.kind, e.tool, e.outcome, e.reason} {
			if s == "" {
				continue
			}
			if !storedWord.MatchString(s) {
				t.Fatalf("a stored string is not a code: %q in %+v", s, e)
			}
		}
	}
}

// storedWord is what a stored string is allowed to look like: a fixed
// identifier or a code from a closed set. A path, a command or a prompt
// fragment fails it, which is the whole guarantee the record rests on.
var storedWord = regexp.MustCompile(`^[a-z0-9]+(?:[-_][a-z0-9]+)*$`)

// A child waiting out a provider says so twice: on its lane, because a writer
// waiting for a rate limit and one that has hung look identical on a progress
// row otherwise, and in the record, because that is where a fan-out's wall
// clock is accounted for afterwards.
func TestChildRecordsARetryAndSaysSoOnItsLane(t *testing.T) {
	env := &scriptedEnv{steps: []streamStep{
		{fail: &provider.Failure{Class: provider.ClassRateLimit, Status: 429, RetryAfter: time.Millisecond}},
		{text: "done"},
	}}
	rec := &testRecorder{}
	sup := supervisorRecording(t, env, rec)

	// The lane's detail is a live snapshot that the next tool call overwrites,
	// so it is collected as it is emitted rather than read at the end.
	ctx := t.Context()
	var mu sync.Mutex
	var details []string
	go func() {
		for {
			select {
			case ev := <-sup.Events():
				if ev.Kind == EventUpdate {
					mu.Lock()
					details = append(details, ev.Status.Detail)
					mu.Unlock()
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"survey the code"}`)
	execTool(t, sup, ReportToolName, `{"name":"researcher-1"}`)

	sigs := rec.of("signal")
	if len(sigs) != 1 || sigs[0].outcome != observe.SignalRetry || sigs[0].reason != "rate-limit" {
		t.Fatalf("expected one retry signal naming its class, got %+v", sigs)
	}
	if sigs[0].pos.Turn != 1 {
		t.Errorf("the retry is unplaced: %+v", sigs[0].pos)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, d := range details {
		if strings.Contains(d, "retry 1 of 3") {
			return
		}
	}
	t.Errorf("no lane update said the child was waiting, got %v", details)
}
