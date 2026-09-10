package subagent

import (
	"context"
	"math"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/tools"
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
	cost      float64
	priced    bool
	ended     bool
	end       observe.ChildEnd
}

func (r *testRecorder) recorder() Recorder {
	return Recorder{
		Observer: observe.Observer{
			Usage: func(turns, in, out int64, cost float64, priced bool) {
				r.mu.Lock()
				defer r.mu.Unlock()
				r.turns, r.tokensIn, r.tokensOut, r.cost, r.priced = turns, in, out, cost, priced
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
		End: func(e observe.ChildEnd) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.ended, r.end = true, e
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
	// This supervisor was given no pricing table, so nothing could be
	// priced and the recorder's own fallback is what the row is left to.
	if rec.priced {
		t.Fatal("a child with no prices to bill against must report its totals unpriced")
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

	var sigs []recordedEvent
	for _, e := range rec.of("signal") {
		if e.outcome == observe.SignalRetry {
			sigs = append(sigs, e)
		}
	}
	if len(sigs) != 1 || sigs[0].reason != "rate-limit" {
		t.Fatalf("expected one retry signal naming its class, got %+v", rec.of("signal"))
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

// rowRecorder is the store as a child's record reaches it: one row per
// recorder opened, each holding the totals its last usage report set, because
// a session-row update writes absolute totals rather than adding to them. A
// retried child has one row per attempt, which is why the rows are kept
// apart instead of collapsed the way testRecorder collapses them.
type rowRecorder struct {
	mu   sync.Mutex
	rows []*attemptRow
}

type attemptRow struct{ in, out int64 }

func (r *rowRecorder) open() Recorder {
	row := &attemptRow{}
	r.mu.Lock()
	r.rows = append(r.rows, row)
	r.mu.Unlock()
	return Recorder{Observer: observe.Observer{
		Usage: func(_, in, out int64, _ float64, _ bool) {
			r.mu.Lock()
			defer r.mu.Unlock()
			row.in, row.out = in, out
		},
	}, End: func(observe.ChildEnd) {}}
}

func (r *rowRecorder) snapshot() []attemptRow {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]attemptRow, len(r.rows))
	for i, row := range r.rows {
		out[i] = *row
	}
	return out
}

func (r *rowRecorder) sum() (in, out int64) {
	for _, row := range r.snapshot() {
		in, out = in+row.in, out+row.out
	}
	return in, out
}

// supervisorRecordingRows is a supervisor that opens a fresh row for every
// attempt, with the classifier wired when one is given so both roads a
// child's spend travels can be driven.
func supervisorRecordingRows(t *testing.T, env *scriptedEnv, rows *rowRecorder, judge provider.Provider) *Supervisor {
	t.Helper()
	opts := Options{
		Root:   t.TempDir(),
		NewEnv: env.factory(),
		Record: func(Spec, string) Recorder { return rows.open() },
	}
	if judge != nil {
		opts.Classifier = agent.NewClassifier(judge, agent.ClassifierConfig{Model: "judge"})
	}
	sup := New(t.Context(), opts)
	t.Cleanup(sup.Close)
	return sup
}

// retryWhenReady retries a failed child once its previous run has finished
// letting go. Retry refuses a child whose goroutine still owns the worktree
// and the done channel — "try again in a moment" is the contract, and the
// manager's key press is a person who does — so a test that reads the failed
// state and retries in the same breath is racing the shutdown rather than
// testing anything.
func retryWhenReady(t *testing.T, sup *Supervisor, name string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := sup.Retry(name)
		if err == nil {
			return
		}
		if !strings.Contains(err.Error(), "shutting down") || time.Now().After(deadline) {
			t.Fatalf("retry %s: %v", name, err)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A retry opens a second row for the same child, and the first row still
// holds what the failed attempt spent. Reporting the child's carried totals
// to the new row would bill the first attempt on both, so a child that read
// 4000 tokens, failed and read 100 more would be recorded as having read
// 8100 — spend that never happened, on a row a retry is the only way to
// reach.
func TestARetriedChildsRowsSumToWhatItSpent(t *testing.T) {
	env := &scriptedEnv{steps: []streamStep{
		{text: "over budget", usage: &provider.Usage{PromptTokens: 300100, CompletionTokens: 200}},
	}}
	rows := &rowRecorder{}
	sup := supervisorRecordingRows(t, env, rows, nil)

	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"survey","max_tokens":300000}`)
	waitState(t, sup, "researcher-1", StateFailed)

	env.mu.Lock()
	env.steps = []streamStep{{text: "done", usage: &provider.Usage{PromptTokens: 100, CompletionTokens: 20}}}
	env.mu.Unlock()
	retryWhenReady(t, sup, "researcher-1")
	waitState(t, sup, "researcher-1", StateDone)

	got := rows.snapshot()
	if len(got) != 2 {
		t.Fatalf("expected a row per attempt, got %+v", got)
	}
	if got[0].in != 300100 || got[0].out != 200 {
		t.Errorf("the first attempt's row = %+v, want the 300100/200 it spent", got[0])
	}
	if got[1].in != 100 || got[1].out != 20 {
		t.Errorf("the retry's row = %+v, want the 100/20 that attempt spent", got[1])
	}
	in, out := rows.sum()
	st, _ := sup.Get("researcher-1")
	if in != st.Spend.In || out != st.Spend.Out {
		t.Errorf("the rows sum to %d/%d, but the child cost %d/%d", in, out, st.Spend.In, st.Spend.Out)
	}
}

// The classifier's spend is the child's spend and travels the same road, so
// it is double-counted by the same defect and has to be fixed by the same
// accessor.
func TestARetriedChildsClassifierSpendIsCountedOnce(t *testing.T) {
	call := []streamStep{{calls: []provider.ToolCall{{ID: "c1", Name: tools.ExecCommandName, Arguments: `{"command":"go test ./..."}`}}}}
	env := &scriptedEnv{
		// One scripted step and no answer after the tool result: the stream
		// runs out, which fails the child with the classifier's spend on its
		// row.
		steps:   call,
		gated:   map[string]bool{tools.ExecCommandName: true},
		execOut: "PASS",
	}
	rows := &rowRecorder{}
	sup := supervisorRecordingRows(t, env, rows, &judgeSpending{prompt: 600, completion: 40})
	sup.SetParentMode(agent.ModeAuto)

	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"run the tests"}`)
	waitState(t, sup, "researcher-1", StateFailed)

	env.mu.Lock()
	env.steps = append(append([]streamStep(nil), call...), streamStep{text: "done"})
	env.mu.Unlock()
	retryWhenReady(t, sup, "researcher-1")
	waitState(t, sup, "researcher-1", StateDone)

	got := rows.snapshot()
	if len(got) != 2 {
		t.Fatalf("expected a row per attempt, got %+v", got)
	}
	for i, row := range got {
		if row.in != 600 || row.out != 40 {
			t.Errorf("attempt %d's row = %+v, want the one judgement it paid for", i+1, row)
		}
	}
	in, out := rows.sum()
	st, _ := sup.Get("researcher-1")
	if in != st.Spend.In || out != st.Spend.Out {
		t.Errorf("the rows sum to %d/%d, but the child cost %d/%d", in, out, st.Spend.In, st.Spend.Out)
	}
}

// judgeSpending allows every call and bills a fixed amount for it, so the
// classifier's contribution to a child's spend is a number a test can add up.
type judgeSpending struct{ prompt, completion int }

func (p *judgeSpending) StreamCompletion(context.Context, []provider.Message, provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
	ch := make(chan provider.StreamEvent, 1)
	ch <- provider.StreamEvent{
		ToolCalls: []provider.ToolCall{{ID: "d1", Name: agent.DecisionToolName, Arguments: `{"decision":"allow","reason":"runs the requested tests"}`}},
		Usage:     &provider.Usage{PromptTokens: p.prompt, CompletionTokens: p.completion},
		Done:      true,
	}
	close(ch)
	return ch, nil
}

func (p *judgeSpending) Name() string { return "judge" }

// A steer is recorded as a steer with its source and nothing else. The source
// is the point: a fan-out's drift rate cannot say whether the orchestrator is
// answering what it sees unless the record separates the redirects it sent
// from the ones the person sent from a lane. The message never goes in — it
// is the parent's or the person's own words, and the record holds no content.
func TestChildRecordsASteersSourceAndNeverItsWords(t *testing.T) {
	env := &scriptedEnv{steps: []streamStep{
		{calls: []provider.ToolCall{{ID: "c1", Name: "read_file", Arguments: `{"path":"importer.go"}`}}},
		{text: "read the importer"},
	}, delay: 5 * time.Millisecond}
	rec := &testRecorder{}
	sup := supervisorRecording(t, env, rec)

	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"survey the exporter"}`)
	if err := sup.Steer("researcher-1", "read the exporter instead", SteerFromParent); err != nil {
		t.Fatalf("steering the child: %v", err)
	}
	execTool(t, sup, ReportToolName, `{"name":"researcher-1"}`)

	var steers []recordedEvent
	for _, e := range rec.of("signal") {
		if e.outcome == observe.SignalSteer {
			steers = append(steers, e)
		}
	}
	if len(steers) != 1 {
		t.Fatalf("expected one steer signal, got %+v", steers)
	}
	if steers[0].reason != string(SteerFromParent) {
		t.Fatalf("the record carries the source, got %q", steers[0].reason)
	}
	for _, e := range rec.all() {
		if strings.Contains(e.reason, "exporter") {
			t.Fatalf("the steer's words reached the record: %+v", e)
		}
		for _, s := range []string{e.kind, e.tool, e.outcome, e.reason} {
			if s != "" && !storedWord.MatchString(s) {
				t.Fatalf("a stored string is not a code: %q in %+v", s, e)
			}
		}
	}
}

// roundWait is how long a scripted round takes in the tests below: long
// enough that a verdict released a quarter of the way in lands while the
// child is still inside that round, and short enough to run.
const roundWait = 100 * time.Millisecond

// heldReader is a reader whose answer is held until the test lets it go, so a
// reading is still out at a moment the test chooses. A reader that answers
// straight away lands its verdict on the round boundary that asked for it,
// which is the one case that cannot go wrong.
type heldReader struct {
	state   string
	started chan struct{}
	release chan struct{}
}

func newHeldReader(state string) *heldReader {
	return &heldReader{state: state, started: make(chan struct{}, 1), release: make(chan struct{})}
}

func (p *heldReader) StreamCompletion(ctx context.Context, _ []provider.Message, _ provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
	select {
	case p.started <- struct{}{}:
	default:
	}
	select {
	case <-p.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	ch := make(chan provider.StreamEvent, 1)
	ch <- provider.StreamEvent{
		ToolCalls: []provider.ToolCall{{
			ID:        "s1",
			Name:      agent.SummaryToolName,
			Arguments: `{"summary":"reading the importer","state":"` + p.state + `","reason":"reading the importer, not the exporter"}`,
		}},
		Done: true,
	}
	close(ch)
	return ch, nil
}

func (p *heldReader) Name() string { return "held" }

// waitAsked blocks until the reader has been asked for a reading.
func (p *heldReader) waitAsked(t *testing.T) {
	t.Helper()
	select {
	case <-p.started:
	case <-time.After(5 * time.Second):
		t.Fatal("no reading was ever taken of the child")
	}
}

// waitToolCalls blocks until a child has run n tool calls, which is how a test
// says "the run is under way" without knowing how fast the machine is.
func waitToolCalls(t *testing.T, sup *Supervisor, name string, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if st, ok := sup.Get(name); ok && st.ToolCalls >= n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	st, _ := sup.Get(name)
	t.Fatalf("agent %s ran %d tool calls, want %d", name, st.ToolCalls, n)
}

// waitSignal blocks until a signal with this code has been recorded, and
// returns the first one.
func waitSignal(t *testing.T, rec *testRecorder, code string) recordedEvent {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, e := range rec.of("signal") {
			if e.outcome == code {
				return e
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("no %s signal was recorded, got %+v", code, rec.of("signal"))
	return recordedEvent{}
}

// A reading that comes back after the run it read has ended is filed at the
// round it read. The child here is retried while its last reading is still
// out, so the verdict arrives on the reader's own goroutine with a second
// attempt's goroutine advancing the round counter: reading that counter from
// here is a race the detector reports, and the answer it gives is about a
// round the reading never saw.
func TestChildFilesALateReadingAtTheRoundItRead(t *testing.T) {
	reader := newHeldReader("on_target")
	env := &scriptedEnv{
		steps: append(toolRounds(3), streamStep{text: "spent it", usage: &provider.Usage{PromptTokens: 300100}}),
		delay: 2 * time.Millisecond,
		summarizer: agent.NewSummarizer(reader,
			agent.SummaryConfig{Model: "fast", IntervalRounds: 10, MinGap: -1, InterveneCooldownIntervals: 1}),
	}
	rec := &testRecorder{}
	sup := supervisorRecording(t, env, rec)

	// The budget is what ends the first attempt: its turn finishes normally,
	// which is what takes the closing reading, and the overrun is only
	// visible once the answer is in hand.
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"survey the exporter","max_tokens":300000}`)
	reader.waitAsked(t)
	waitState(t, sup, "researcher-1", StateFailed)

	// The second attempt is not read itself, so the only verdict in play is
	// the one still out from the attempt before it. Its rounds are slow
	// because the verdict has to land inside one of them.
	env.mu.Lock()
	env.summarizer, env.steps, env.delay = nil, toolRounds(200), roundWait
	env.mu.Unlock()
	if err := sup.Retry("researcher-1"); err != nil {
		t.Fatalf("retrying the child: %v", err)
	}

	// A moment in, the second attempt has started its turn — which is where
	// it sets the counter — and is waiting on its provider, so the verdict is
	// let go to land there. Waiting for the child to say so instead would
	// order the two goroutines the detector is being asked about.
	time.Sleep(roundWait / 4)
	close(reader.release)

	sig := waitSignal(t, rec, observe.SignalSummary)
	if sig.pos.Round != agent.FirstSummaryRound {
		t.Fatalf("the reading is filed at round %d, want the round %d it read",
			sig.pos.Round, agent.FirstSummaryRound)
	}
	if sig.pos.Turn != 1 {
		t.Fatalf("the reading is filed at turn %d, want the child's own turn 1", sig.pos.Turn)
	}
}

// The signals raised at a round boundary keep the child's live position: a
// verdict too old to act on is withheld now, whatever round the reading it
// came from was taken at, and a record that filed the two events together
// could not tell how long the run had gone on unjudged.
func TestChildFilesAWithheldVerdictAtItsLivePosition(t *testing.T) {
	reader := newHeldReader("off_target")
	env := &scriptedEnv{
		steps: toolRounds(200),
		delay: time.Millisecond,
		summarizer: agent.NewSummarizer(reader,
			agent.SummaryConfig{Model: "fast", IntervalRounds: 10, MinGap: -1, InterveneCooldownIntervals: 1}),
	}
	rec := &testRecorder{}
	sup := supervisorRecording(t, env, rec)

	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"survey the exporter"}`)
	reader.waitAsked(t)
	// A whole interval past the round it was taken at, the verdict describes
	// work the child has left behind and is withheld rather than delivered.
	waitToolCalls(t, sup, "researcher-1", agent.FirstSummaryRound+11)
	close(reader.release)

	sig := waitSignal(t, rec, observe.SignalSummary)
	if sig.pos.Round != agent.FirstSummaryRound {
		t.Fatalf("the reading is filed at round %d, want the round %d it read",
			sig.pos.Round, agent.FirstSummaryRound)
	}
	withheld := waitSignal(t, rec, observe.SignalIntervene)
	if withheld.reason != agent.InterveneStale {
		t.Fatalf("expected the stale verdict to be withheld, got %+v", withheld)
	}
	if withheld.pos.Round <= sig.pos.Round {
		t.Fatalf("the withheld verdict is filed at round %d, want the round the child had reached (past %d)",
			withheld.pos.Round, sig.pos.Round)
	}
}

// endRecorder collects what each attempt's row was closed with, in the order
// the attempts ran, so a retry's row and the one it replaces can be read
// side by side.
type endRecorder struct {
	mu    sync.Mutex
	specs []Spec
	ends  []observe.ChildEnd
}

func (r *endRecorder) open(spec Spec) Recorder {
	r.mu.Lock()
	r.specs = append(r.specs, spec)
	r.mu.Unlock()
	return Recorder{End: func(e observe.ChildEnd) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.ends = append(r.ends, e)
	}}
}

func (r *endRecorder) snapshot() ([]Spec, []observe.ChildEnd) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Spec(nil), r.specs...), append([]observe.ChildEnd(nil), r.ends...)
}

func supervisorEnding(t *testing.T, env *scriptedEnv, rec *endRecorder) *Supervisor {
	t.Helper()
	sup := New(t.Context(), Options{
		Root:   t.TempDir(),
		NewEnv: env.factory(),
		Record: func(spec Spec, _ string) Recorder { return rec.open(spec) },
	})
	t.Cleanup(sup.Close)
	return sup
}

// A child that spent its budget says so, and the retry that follows carries
// the number that joins its row to the one it replaces. "failed" is what the
// lane says and it answers nothing: a budget spent, a kill and a provider
// that stopped answering are the same word there and three different answers
// to whether 200k is the right number.
func TestChildEndsWithABudgetReasonAndItsRetryIsTheSecondAttempt(t *testing.T) {
	env := &scriptedEnv{steps: []streamStep{
		{text: "over budget", usage: &provider.Usage{PromptTokens: 300100, CompletionTokens: 200}},
	}}
	rec := &endRecorder{}
	sup := supervisorEnding(t, env, rec)

	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"survey","max_tokens":300000}`)
	waitState(t, sup, "researcher-1", StateFailed)

	if st, _ := sup.Get("researcher-1"); st.End != observe.ChildBudget {
		t.Fatalf("the child ended as %q, want %q", st.End, observe.ChildBudget)
	}

	env.mu.Lock()
	env.steps = []streamStep{{text: "done", usage: &provider.Usage{PromptTokens: 100, CompletionTokens: 20}}}
	env.mu.Unlock()
	retryWhenReady(t, sup, "researcher-1")
	waitState(t, sup, "researcher-1", StateDone)
	// The second attempt's row is closed on its own goroutine, after the
	// state it reports; the specs are opened before the run either way.
	specs, _ := rec.snapshot()
	for len(specs) < 2 {
		time.Sleep(time.Millisecond)
		specs, _ = rec.snapshot()
	}

	if len(specs) != 2 || specs[0].Attempt != 1 || specs[1].Attempt != 2 {
		t.Fatalf("attempts stamped %+v, want a row per attempt numbered 1 then 2", specs)
	}
	deadline := time.Now().Add(5 * time.Second)
	var ends []observe.ChildEnd
	for time.Now().Before(deadline) {
		if _, ends = rec.snapshot(); len(ends) == 2 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if len(ends) != 2 {
		t.Fatalf("expected an end per attempt, got %+v", ends)
	}
	if ends[0].Reason != observe.ChildBudget || ends[0].Attempt != 1 {
		t.Errorf("the first attempt's row = %+v, want the budget it spent on attempt 1", ends[0])
	}
	if ends[1].Reason != observe.ChildDone || ends[1].Attempt != 2 {
		t.Errorf("the retry's row = %+v, want done on attempt 2", ends[1])
	}
}

// A kill and a session shutting down both reach a child as a cancelled
// context, and the record must not report them as the same thing: one is a
// person deciding the child was not worth finishing.
func TestAKilledChildEndsAsKilledAndNotAsCancelled(t *testing.T) {
	env := &scriptedEnv{steps: toolRounds(200), delay: 5 * time.Millisecond}
	rec := &endRecorder{}
	sup := supervisorEnding(t, env, rec)

	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"survey the code"}`)
	waitToolCalls(t, sup, "researcher-1", 1)
	if err := sup.Kill("researcher-1"); err != nil {
		t.Fatalf("killing the child: %v", err)
	}
	waitState(t, sup, "researcher-1", StateFailed)

	if st, _ := sup.Get("researcher-1"); st.End != observe.ChildKilled {
		t.Fatalf("a killed child ended as %q, want %q", st.End, observe.ChildKilled)
	}
}

// The reading that rides a child's end is the record's own word for it and
// not the wording its lane shows: two spellings of one summariser state is
// two columns nothing can add up, and a column filled from a roster line
// changes meaning the day somebody rewords the roster.
func TestAChildsEndCarriesTheReadingInTheRecordsOwnWord(t *testing.T) {
	reader := newHeldReader("off_target")
	close(reader.release)
	env := &scriptedEnv{
		steps: append(toolRounds(agent.FirstSummaryRound+1), streamStep{text: "done"}),
		delay: time.Millisecond,
		summarizer: agent.NewSummarizer(reader,
			agent.SummaryConfig{Model: "fast", IntervalRounds: 10, MinGap: -1, InterveneCooldownIntervals: 1}),
	}
	rec := &endRecorder{}
	sup := supervisorEnding(t, env, rec)

	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"survey the exporter"}`)
	execTool(t, sup, ReportToolName, `{"name":"researcher-1"}`)

	deadline := time.Now().Add(5 * time.Second)
	var ends []observe.ChildEnd
	for time.Now().Before(deadline) {
		if _, ends = rec.snapshot(); len(ends) == 1 && ends[0].Verdict != "" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if len(ends) != 1 {
		t.Fatalf("expected one closed row, got %+v", ends)
	}
	want := observe.SummaryCode(agent.SummaryOffTarget)
	if ends[0].Verdict != want {
		t.Fatalf("the end carries verdict %q, want the record's own %q", ends[0].Verdict, want)
	}
	if !storedWord.MatchString(ends[0].Verdict) {
		t.Fatalf("the stored verdict is not a code: %q", ends[0].Verdict)
	}
}

// A child's recorded cost is what it was billed, not the whole of its input
// charged at the fresh rate. A coding child re-sends its prompt every round
// and the provider serves nearly all of it from cache, so the two answers are
// not close: the same million tokens is $0.29 billed and $1.51 at the fresh
// rate, and the record is the figure `shhh observe`, the fan-out rows and the
// agent map all read.
func TestAChildsRecordedCostBillsCacheReadsAtTheCacheRate(t *testing.T) {
	env := &scriptedEnv{steps: []streamStep{{
		text:  "done",
		usage: &provider.Usage{PromptTokens: 1_000_000, CachedTokens: 900_000, CompletionTokens: 1_000},
	}}}
	prices := pricing.NewTable(map[string]pricing.ModelPricing{
		"cached-1": {
			InputCostPerToken:     1.5 / 1e6,
			OutputCostPerToken:    9.0 / 1e6,
			CacheReadCostPerToken: 0.15 / 1e6,
		},
	})
	rec := &testRecorder{}
	sup := New(t.Context(), Options{
		Root:   t.TempDir(),
		NewEnv: env.factory(),
		Prices: prices,
		Record: func(Spec, string) Recorder { return rec.recorder() },
	})
	t.Cleanup(sup.Close)

	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"survey","model":"cached-1"}`)
	waitState(t, sup, "researcher-1", StateDone)

	// 100k read fresh at $1.50/M, 900k served from cache at $0.15/M, 1k out
	// at $9/M.
	const billed = 0.15 + 0.135 + 0.009
	// What the same pair comes to with the split thrown away: every input
	// token at the fresh rate.
	const fresh = 1.5 + 0.009

	rec.mu.Lock()
	gotCost, gotPriced, gotIn := rec.cost, rec.priced, rec.tokensIn
	rec.mu.Unlock()
	if !gotPriced {
		t.Fatal("the child reported its spend unpriced, so the record prices the sum at the fresh rate")
	}
	if gotIn != 1_000_000 {
		t.Fatalf("the recorded input = %d, want the million the child took in", gotIn)
	}
	if math.Abs(gotCost-billed) > 1e-9 {
		t.Fatalf("the recorded cost = %.6f, want the billed %.6f (the fresh rate would be %.6f)", gotCost, billed, fresh)
	}

	st, ok := sup.Get("researcher-1")
	if !ok {
		t.Fatal("the child is missing from the roster")
	}
	if !st.Spend.Priced || math.Abs(st.Spend.Cost-billed) > 1e-9 {
		t.Fatalf("the child's row = %.6f (priced %v), want the billed %.6f", st.Spend.Cost, st.Spend.Priced, billed)
	}
	if st.Spend.Cached != 900_000 {
		t.Fatalf("the row carries %d cache reads, want the 900k the provider served", st.Spend.Cached)
	}
}

// A retry's row is the attempt's own bill, and the roster's is both attempts'.
// The two are different questions and a cost that answered the wrong one is
// counted twice: a row update writes absolute totals, so handing the second
// attempt's row the carried figure bills the first attempt on both rows.
func TestARetrysRecordedCostIsItsOwnAttempts(t *testing.T) {
	env := &scriptedEnv{steps: []streamStep{
		{text: "over budget", usage: &provider.Usage{PromptTokens: 600_001, CachedTokens: 300_000}},
	}}
	prices := pricing.NewTable(map[string]pricing.ModelPricing{
		"cached-1": {
			InputCostPerToken:     1.5 / 1e6,
			OutputCostPerToken:    9.0 / 1e6,
			CacheReadCostPerToken: 0.15 / 1e6,
		},
	})
	rec := &testRecorder{}
	sup := New(t.Context(), Options{
		Root:   t.TempDir(),
		NewEnv: env.factory(),
		Prices: prices,
		Record: func(Spec, string) Recorder { return rec.recorder() },
	})
	t.Cleanup(sup.Close)

	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"survey","model":"cached-1","max_tokens":300000}`)
	waitState(t, sup, "researcher-1", StateFailed)
	const first = 0.4500015 + 0.045 // 300001 fresh, 300k cached

	env.mu.Lock()
	env.steps = []streamStep{{text: "done", usage: &provider.Usage{PromptTokens: 100_000, CachedTokens: 99_500}}}
	env.mu.Unlock()
	if err := sup.Retry("researcher-1"); err != nil {
		t.Fatalf("retry: %v", err)
	}
	waitState(t, sup, "researcher-1", StateDone)
	const second = 0.00075 + 0.014925 // 500 fresh, 99.5k cached

	rec.mu.Lock()
	gotCost := rec.cost
	rec.mu.Unlock()
	if math.Abs(gotCost-second) > 1e-9 {
		t.Fatalf("the retry's row = %.6f, want the %.6f that attempt spent", gotCost, second)
	}
	st, _ := sup.Get("researcher-1")
	if math.Abs(st.Spend.Cost-(first+second)) > 1e-9 {
		t.Fatalf("the roster = %.6f, want both attempts' %.6f", st.Spend.Cost, first+second)
	}
}
