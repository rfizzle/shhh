package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/provider"
)

// scriptedStream returns a StreamFunc that serves one scripted event slice
// per request, failing the test if more requests arrive than scripted.
func scriptedStream(t *testing.T, rounds ...[]provider.StreamEvent) StreamFunc {
	t.Helper()
	i := 0
	return func([]provider.Message, string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		if i >= len(rounds) {
			t.Fatalf("unexpected stream request #%d", i+1)
		}
		evs := rounds[i]
		i++
		ch := make(chan provider.StreamEvent, len(evs))
		for _, ev := range evs {
			ch <- ev
		}
		close(ch)
		_, cancel := context.WithCancel(context.Background())
		return ch, cancel, nil
	}
}

func toolCallRound(calls ...provider.ToolCall) []provider.StreamEvent {
	return []provider.StreamEvent{{ToolCalls: calls}}
}

func doneRound(text string) []provider.StreamEvent {
	return []provider.StreamEvent{{Token: text}, {Done: true}}
}

func TestHeadlessRun_PlainResponse(t *testing.T) {
	a := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}},
		scriptedStream(t, []provider.StreamEvent{{Token: "hello "}, {Token: "world"}, {Done: true}}))

	var streamed strings.Builder
	h := &Headless{Agent: a, OnText: func(s string) { streamed.WriteString(s) }}

	final, err := h.Run("hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if final != "hello world" {
		t.Fatalf("final = %q, want %q", final, "hello world")
	}
	if streamed.String() != final {
		t.Fatalf("OnText streamed %q, want %q", streamed.String(), final)
	}
	msgs := a.Messages()
	last := msgs[len(msgs)-1]
	if last.Role != provider.RoleAssistant || last.Content != "hello world" {
		t.Fatalf("final assistant message not recorded, got %+v", last)
	}
}

func TestHeadlessRun_AutoToolRound(t *testing.T) {
	a := New(nil, scriptedStream(t,
		toolCallRound(provider.ToolCall{ID: "c1", Name: "read_file", Arguments: `{"path":"x"}`}),
		doneRound("done"),
	))
	a.SetExecutor(func(name string, args json.RawMessage) (string, error) {
		if name != "read_file" {
			t.Fatalf("executor got tool %q", name)
		}
		return "contents", nil
	})

	var calls, results []string
	h := &Headless{
		Agent:        a,
		OnToolCall:   func(tc provider.ToolCall) { calls = append(calls, tc.Name) },
		OnToolResult: func(r ToolResult) { results = append(results, r.Result) },
	}

	final, err := h.Run("go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if final != "done" {
		t.Fatalf("final = %q, want %q", final, "done")
	}
	if len(calls) != 1 || calls[0] != "read_file" {
		t.Fatalf("OnToolCall calls = %v", calls)
	}
	if len(results) != 1 || results[0] != "contents" {
		t.Fatalf("OnToolResult results = %v", results)
	}
	var toolMsg *provider.Message
	for i := range a.Messages() {
		if a.Messages()[i].Role == provider.RoleTool {
			toolMsg = &a.Messages()[i]
		}
	}
	if toolMsg == nil || toolMsg.Content != "contents" || toolMsg.ToolCallID != "c1" {
		t.Fatalf("tool result message not recorded, got %+v", toolMsg)
	}
}

func TestHeadlessRun_GatedCallResolved(t *testing.T) {
	a := New(nil, scriptedStream(t,
		toolCallRound(provider.ToolCall{ID: "c1", Name: "execute_command", Arguments: `{"command":"ls"}`}),
		doneRound("ok"),
	))

	h := &Headless{
		Agent: a,
		Gate:  func(tc provider.ToolCall) bool { return tc.Name == "execute_command" },
		Resolve: func(tc provider.ToolCall) string {
			return "error: the user declined this tool call"
		},
	}

	final, err := h.Run("go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if final != "ok" {
		t.Fatalf("final = %q, want %q", final, "ok")
	}
	var toolMsg provider.Message
	for _, m := range a.Messages() {
		if m.Role == provider.RoleTool {
			toolMsg = m
		}
	}
	if toolMsg.ToolCallID != "c1" || !strings.HasPrefix(toolMsg.Content, "error:") {
		t.Fatalf("gated result not recorded as decline, got %+v", toolMsg)
	}
}

func TestHeadlessRun_NilResolveDeclines(t *testing.T) {
	a := New(nil, scriptedStream(t,
		toolCallRound(provider.ToolCall{ID: "c1", Name: "write_file"}),
		doneRound("ok"),
	))
	h := &Headless{Agent: a, Gate: func(provider.ToolCall) bool { return true }}

	if _, err := h.Run("go"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, m := range a.Messages() {
		if m.Role == provider.RoleTool && !strings.Contains(m.Content, "cannot be approved") {
			t.Fatalf("nil Resolve should decline, got %q", m.Content)
		}
	}
}

func TestHeadlessRun_RoundCap(t *testing.T) {
	// Every stream requests another tool round; with a cap of 2 the run must
	// stop with ErrRoundCap after the second round's results are recorded.
	rounds := [][]provider.StreamEvent{
		toolCallRound(provider.ToolCall{ID: "c1", Name: "search"}),
		toolCallRound(provider.ToolCall{ID: "c2", Name: "search"}),
	}
	a := New(nil, scriptedStream(t, rounds...))
	a.SetExecutor(func(string, json.RawMessage) (string, error) { return "r", nil })
	a.SetMaxRounds(2)

	h := &Headless{Agent: a}
	_, err := h.Run("go")
	if !errors.Is(err, ErrRoundCap) {
		t.Fatalf("expected ErrRoundCap, got %v", err)
	}
	// Both rounds' calls must still have recorded results so the conversation
	// stays well-formed.
	var toolResults int
	for _, m := range a.Messages() {
		if m.Role == provider.RoleTool {
			toolResults++
		}
	}
	if toolResults != 2 {
		t.Fatalf("expected 2 tool results before capping, got %d", toolResults)
	}
}

func TestHeadlessRun_StreamError(t *testing.T) {
	boom := errors.New("boom")
	a := New(nil, scriptedStream(t, []provider.StreamEvent{{Err: boom}}))
	h := &Headless{Agent: a}
	if _, err := h.Run("go"); !errors.Is(err, boom) {
		t.Fatalf("expected stream error to propagate, got %v", err)
	}
}

func TestHeadlessRun_UsageReported(t *testing.T) {
	a := New(nil, scriptedStream(t, []provider.StreamEvent{
		{Token: "x"},
		{Done: true, Usage: &provider.Usage{PromptTokens: 10, CompletionTokens: 5}},
	}))
	var got provider.Usage
	h := &Headless{Agent: a, OnUsage: func(u *provider.Usage) { got = *u }}
	if _, err := h.Run("go"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.PromptTokens != 10 || got.CompletionTokens != 5 {
		t.Fatalf("usage not surfaced, got %+v", got)
	}
}

func TestHeadlessRun_SteerInjectedBetweenRounds(t *testing.T) {
	a := New(nil, scriptedStream(t,
		toolCallRound(provider.ToolCall{ID: "c1", Name: "search"}),
		doneRound("done"),
	))
	a.SetExecutor(func(string, json.RawMessage) (string, error) { return "r", nil })

	queued := []string{"focus on x"}
	h := &Headless{Agent: a, Steer: func() []string {
		out := queued
		queued = nil
		return out
	}}
	final, err := h.Run("go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if final != "done" {
		t.Fatalf("final = %q, want %q", final, "done")
	}
	// The steering message joins the conversation after the round's results
	// and before the final response, and it resets the round counter.
	steerIdx, toolIdx := -1, -1
	for i, m := range a.Messages() {
		if m.Role == provider.RoleUser && m.Content == "focus on x" {
			steerIdx = i
		}
		if m.Role == provider.RoleTool {
			toolIdx = i
		}
	}
	if steerIdx == -1 {
		t.Fatal("steering message not injected")
	}
	if steerIdx < toolIdx {
		t.Fatalf("steering injected before the round's results (steer=%d tool=%d)", steerIdx, toolIdx)
	}
	if a.Rounds() != 0 {
		t.Fatalf("steering must reset the round counter, got %d", a.Rounds())
	}
}

func TestHeadlessRun_InterruptCancelsTurn(t *testing.T) {
	// The stream blocks until its cancel func fires, like a real provider.
	stream := func([]provider.Message, string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		ch := make(chan provider.StreamEvent)
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			<-ctx.Done()
			close(ch)
		}()
		return ch, cancel, nil
	}
	a := New(nil, stream)
	h := &Headless{Agent: a}

	done := make(chan struct{})
	var runErr error
	go func() {
		_, runErr = h.Run("go")
		close(done)
	}()
	time.Sleep(20 * time.Millisecond)
	h.Interrupt()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after Interrupt")
	}
	if !errors.Is(runErr, ErrInterrupted) {
		t.Fatalf("expected ErrInterrupted, got %v", runErr)
	}
	// The conversation stays well-formed: no assistant message owes results.
	for _, m := range a.Messages() {
		if m.Role == provider.RoleAssistant && len(m.ToolCalls) > 0 {
			t.Fatal("interrupted run must not leave dangling tool calls")
		}
	}
	// A fresh Run works after an interrupt.
	a2 := New(nil, scriptedStream(t, doneRound("ok")))
	h.Agent = a2
	if final, err := h.Run("again"); err != nil || final != "ok" {
		t.Fatalf("Run after Interrupt = %q, %v", final, err)
	}
}

// A reading that lands reaches the front-end whatever it says, not only when
// it goes on to interrupt the turn: the record's drift rate is a fraction,
// and the readings that changed nothing are its denominator. (This one is
// on-target, which queues no intervention.)
func TestHeadlessRun_OnSummaryGetsALandedReading(t *testing.T) {
	a := New(nil, scriptedStream(t,
		toolCallRound(provider.ToolCall{ID: "c1", Name: "read_file", Arguments: `{"path":"x"}`}),
		doneRound("done")))
	a.SetExecutor(func(string, json.RawMessage) (string, error) { return "contents", nil })

	run, _ := testSummaryRun(t, &slowProvider{}, "ship the parser")
	// A reading is parked rather than taken: how one is scheduled is
	// summaryrun's business, and this is about what happens to it once it
	// lands. lastRound holds the interval closed so none goes out.
	parked := SummaryVerdict{State: SummaryOnTarget, Text: "on it", Round: 1}
	run.mu.Lock()
	run.verdict, run.lastRound, run.lastAt = &parked, 1, time.Now()
	run.mu.Unlock()

	var seen []SummaryState
	h := &Headless{
		Agent:     a,
		Summary:   run,
		OnSummary: func(v SummaryVerdict) { seen = append(seen, v.State) },
	}
	if _, err := h.Run("go"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(seen) != 1 || seen[0] != SummaryOnTarget {
		t.Fatalf("OnSummary saw %v, want one on-target reading", seen)
	}
}

// A run that takes no readings calls nothing, so a surface can wire the hook
// unconditionally.
func TestHeadlessRun_OnSummarySilentWithoutAReading(t *testing.T) {
	a := New(nil, scriptedStream(t,
		toolCallRound(provider.ToolCall{ID: "c1", Name: "read_file", Arguments: `{"path":"x"}`}),
		doneRound("done")))
	a.SetExecutor(func(string, json.RawMessage) (string, error) { return "contents", nil })

	called := 0
	h := &Headless{Agent: a, OnSummary: func(SummaryVerdict) { called++ }}
	if _, err := h.Run("go"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if called != 0 {
		t.Fatalf("OnSummary called %d times without a summarizer", called)
	}
}

// A headless run is told the tree moved the same way a session is: at the
// round boundary, as a user message, before the next request.
func TestHeadlessRun_TellsTheTurnTheTreeMoved(t *testing.T) {
	ws, _ := treeFixture(t)
	a := New(nil, scriptedStream(t,
		toolCallRound(provider.ToolCall{ID: "c1", Name: "read_file", Arguments: `{"path":"a.txt"}`}),
		doneRound("done"),
	))
	a.SetTreeCheck(TreeCheck{Dir: ws})
	a.SetExecutor(func(name string, args json.RawMessage) (string, error) {
		// Somebody else writes while the round runs.
		write(t, ws, "theirs.txt", "x\n")
		return "hello", nil
	})
	var told []TreeNotice
	h := &Headless{Agent: a, OnTree: func(n TreeNotice) { told = append(told, n) }}
	if _, err := h.Run("go"); err != nil {
		t.Fatal(err)
	}
	if len(told) != 1 || told[0].Paths != 1 {
		t.Fatalf("OnTree should see one notice naming one path, got %+v", told)
	}
	msgs := a.Messages()
	// system-less: user, assistant(tool call), tool result, tree notice, assistant.
	notice := msgs[len(msgs)-2]
	if notice.Role != provider.RoleUser || !strings.Contains(notice.Content, "[tree: 1 path changed outside this session: theirs.txt]") {
		t.Errorf("the notice should sit between the results and the final answer, got %+v", notice)
	}
}

// A round's read-only calls go out together. The prompt tells the model its
// independent reads can be asked for in one round, and a runner that then ran
// them one at a time made that advice false on every surface but the TUI.
func TestHeadlessRun_ARoundsReadsOverlap(t *testing.T) {
	a := New(nil, scriptedStream(t,
		toolCallRound(
			provider.ToolCall{ID: "slow", Name: "search", Arguments: `{"q":"slow"}`},
			provider.ToolCall{ID: "fast", Name: "search", Arguments: `{"q":"fast"}`},
		),
		doneRound("done"),
	))
	// Neither call can finish until both have started, so a dispatcher that
	// runs them in sequence leaves the first one waiting out the deadline —
	// which is the failure, rather than a hang.
	var inFlight sync.WaitGroup
	inFlight.Add(2)
	overlapped := make(chan struct{})
	go func() { inFlight.Wait(); close(overlapped) }()
	a.SetExecutor(func(name string, args json.RawMessage) (string, error) {
		inFlight.Done()
		select {
		case <-overlapped:
		case <-time.After(5 * time.Second):
			return "", errors.New("the round's other call never started")
		}
		var q struct{ Q string }
		if err := json.Unmarshal(args, &q); err != nil {
			return "", err
		}
		if q.Q == "slow" {
			// Come back last, so what is recorded below is the call order and
			// not the order the results happened to arrive in.
			time.Sleep(20 * time.Millisecond)
		}
		return q.Q, nil
	})

	var results []ToolResult
	h := &Headless{Agent: a, OnToolResult: func(r ToolResult) { results = append(results, r) }}
	if _, err := h.Run("go"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 || results[0].Call.ID != "slow" || results[1].Call.ID != "fast" {
		t.Fatalf("results should arrive in call order, got %+v", results)
	}
	// The conversation has to record them in call order too: a tool result
	// addressed out of order is a result addressed to the wrong call.
	var recorded []string
	for _, msg := range a.Messages() {
		if msg.Role == provider.RoleTool {
			recorded = append(recorded, msg.ToolCallID+"="+msg.Content)
		}
	}
	if len(recorded) != 2 || recorded[0] != "slow=slow" || recorded[1] != "fast=fast" {
		t.Fatalf("tool results recorded as %v, want the call order", recorded)
	}
}

// A gated call is still resolved one at a time: each is a decision, and in an
// unattended run the decision is policy's, which may depend on the calls
// before it.
func TestHeadlessRun_GatedCallsStaySequential(t *testing.T) {
	a := New(nil, scriptedStream(t,
		toolCallRound(
			provider.ToolCall{ID: "w1", Name: "write_file", Arguments: `{"path":"a"}`},
			provider.ToolCall{ID: "w2", Name: "write_file", Arguments: `{"path":"b"}`},
		),
		doneRound("done"),
	))
	var asked []string
	h := &Headless{
		Agent: a,
		Gate:  func(provider.ToolCall) bool { return true },
		Resolve: func(tc provider.ToolCall) string {
			// The queue is what makes this sequential: a second call is only
			// offered once this one has been resolved.
			if a.QueuedApprovals() != 2-len(asked) {
				t.Errorf("%s was resolved with %d still queued", tc.ID, a.QueuedApprovals())
			}
			asked = append(asked, tc.ID)
			return "written " + tc.ID
		},
	}
	if _, err := h.Run("go"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(asked) != 2 || asked[0] != "w1" || asked[1] != "w2" {
		t.Fatalf("gated calls resolved as %v, want the call order", asked)
	}
	var recorded []string
	for _, msg := range a.Messages() {
		if msg.Role == provider.RoleTool {
			recorded = append(recorded, msg.Content)
		}
	}
	if len(recorded) != 2 || recorded[0] != "written w1" || recorded[1] != "written w2" {
		t.Fatalf("gated results recorded as %v, want the call order", recorded)
	}
}

// A request the provider never answered is waited out and asked again, the
// same bound the session waits on. The conversation is where the failed
// request left it, so the retry is the same question and not a second one.
func TestHeadlessRun_WaitsOutAnUnansweredRequest(t *testing.T) {
	t.Parallel()
	requests := 0
	stream := func([]provider.Message, string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		requests++
		if requests <= 2 {
			// The floor is a second, so the two waits are the whole of this
			// test's wall clock.
			return nil, nil, &provider.Failure{
				Class: provider.ClassOverloaded, Status: 529, RetryAfter: time.Millisecond,
			}
		}
		ch := make(chan provider.StreamEvent, 2)
		ch <- provider.StreamEvent{Token: "done"}
		ch <- provider.StreamEvent{Done: true}
		close(ch)
		_, cancel := context.WithCancel(context.Background())
		return ch, cancel, nil
	}
	a := New(nil, stream)

	var waits []RetryNotice
	h := &Headless{Agent: a, OnRetry: func(n RetryNotice) { waits = append(waits, n) }}
	final, err := h.Run("go")
	if err != nil {
		t.Fatalf("two 529s then an answer should finish the turn, got %v", err)
	}
	if final != "done" {
		t.Fatalf("final = %q, want %q", final, "done")
	}
	if len(waits) != 2 || waits[0].Attempt != 1 || waits[1].Attempt != 2 {
		t.Fatalf("every wait is reported, got %+v", waits)
	}
	if got := waits[0].Signal(); got != "overloaded" {
		t.Errorf("signal = %q, want overloaded", got)
	}
	// The turn is one user message and one answer: nothing the failures did
	// reached the conversation.
	msgs := a.Messages()
	if len(msgs) != 2 || msgs[0].Role != provider.RoleUser || msgs[1].Content != "done" {
		t.Fatalf("a retried request leaves the conversation alone, got %+v", msgs)
	}
}

// A failure waiting cannot fix is returned rather than slept on, so a run
// with a rejected key fails at once instead of a minute later.
func TestHeadlessRun_DoesNotWaitOutAFailureItCannotFix(t *testing.T) {
	fail := &provider.Failure{Class: provider.ClassAuth, Status: 401}
	requests := 0
	a := New(nil, func([]provider.Message, string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		requests++
		return nil, nil, fail
	})
	h := &Headless{Agent: a, OnRetry: func(RetryNotice) { t.Error("a rejected key is not waited out") }}
	if _, err := h.Run("go"); !errors.Is(err, provider.ErrAuth) {
		t.Fatalf("err = %v, want the auth failure", err)
	}
	if requests != 1 {
		t.Errorf("requests = %d, want the one that failed", requests)
	}
}

// A bound of none is the setting's way of asking for the failure instead of
// the wait, and it has to reach the driver: a run configured that way that
// still slept would be the whole point of the setting missed.
func TestHeadlessRun_ABoundOfNoneWaitsOutNothing(t *testing.T) {
	requests := 0
	a := New(nil, func([]provider.Message, string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		requests++
		return nil, nil, &provider.Failure{Class: provider.ClassOverloaded, Status: 529}
	})
	h := &Headless{Agent: a, OnRetry: func(RetryNotice) { t.Error("a bound of none makes no attempt") }}
	none := 0
	h.SetRetryLimit(&none)
	if _, err := h.Run("go"); err == nil {
		t.Fatal("the failure should stand")
	}
	if requests != 1 {
		t.Errorf("requests = %d, want the one that failed", requests)
	}
}

// Interrupt is honoured during a wait: it holds no stream and owes no
// results, so the turn ends there rather than after the countdown.
func TestHeadlessRun_InterruptEndsAWait(t *testing.T) {
	t.Parallel()
	a := New(nil, func([]provider.Message, string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		return nil, nil, &provider.Failure{Class: provider.ClassOverloaded, RetryAfter: maxRetryWait}
	})
	h := &Headless{Agent: a}
	h.OnRetry = func(RetryNotice) { go h.Interrupt() }
	start := time.Now()
	if _, err := h.Run("go"); !errors.Is(err, ErrInterrupted) {
		t.Fatalf("err = %v, want ErrInterrupted", err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("the wait ran for %v; esc should end it where it stands", elapsed)
	}
}

// A reply that stopped halfway is asked again from the top, and what the
// broken stream had already written comes back with the wait: a surface that
// showed those words is the only thing that can take them back.
func TestHeadlessRun_RetryHandsBackWhatItThrewAway(t *testing.T) {
	t.Parallel()
	requests := 0
	a := New(nil, func([]provider.Message, string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		requests++
		ch := make(chan provider.StreamEvent, 2)
		if requests == 1 {
			ch <- provider.StreamEvent{Token: "half a sen"}
			ch <- provider.StreamEvent{Err: &provider.Failure{
				Class: provider.ClassNetwork, RetryAfter: time.Millisecond,
			}}
		} else {
			ch <- provider.StreamEvent{Token: "the whole answer"}
			ch <- provider.StreamEvent{Done: true}
		}
		close(ch)
		_, cancel := context.WithCancel(context.Background())
		return ch, cancel, nil
	})

	var waits []RetryNotice
	h := &Headless{Agent: a, OnRetry: func(n RetryNotice) { waits = append(waits, n) }}
	final, err := h.Run("go")
	if err != nil {
		t.Fatalf("a dropped reply should be asked again, got %v", err)
	}
	if final != "the whole answer" {
		t.Fatalf("final = %q, want the reply that finished", final)
	}
	if len(waits) != 1 || waits[0].Partial != "half a sen" {
		t.Fatalf("the wait should carry the discarded partial, got %+v", waits)
	}
	// The conversation holds one answer and not one and a half: nothing the
	// broken stream wrote was ever appended.
	msgs := a.Messages()
	if len(msgs) != 2 || msgs[1].Content != "the whole answer" {
		t.Fatalf("the partial reached the conversation, got %+v", msgs)
	}
}

// The bound belongs to one stall, not to the runner's whole life. A child
// reuses its Headless across turns, so a turn that ended mid-backoff must not
// hand the next one whatever was left of the three attempts.
func TestHeadlessRun_EachTurnGetsItsOwnBound(t *testing.T) {
	t.Parallel()
	fail := &provider.Failure{Class: provider.ClassOverloaded, RetryAfter: maxRetryWait}
	requests := 0
	a := New(nil, func([]provider.Message, string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		requests++
		if requests == 1 {
			return nil, nil, fail
		}
		ch := make(chan provider.StreamEvent, 2)
		ch <- provider.StreamEvent{Token: "done"}
		ch <- provider.StreamEvent{Done: true}
		close(ch)
		_, cancel := context.WithCancel(context.Background())
		return ch, cancel, nil
	})

	h := &Headless{Agent: a}
	// The first turn is interrupted while it waits, spending an attempt it
	// never got to use.
	h.OnRetry = func(RetryNotice) { go h.Interrupt() }
	if _, err := h.Run("go"); !errors.Is(err, ErrInterrupted) {
		t.Fatalf("err = %v, want ErrInterrupted", err)
	}
	if h.retry.Attempt() == 0 {
		t.Fatal("the interrupted turn should have spent an attempt")
	}
	h.OnRetry = nil
	if _, err := h.Run("carry on"); err != nil {
		t.Fatalf("the next turn should start clean, got %v", err)
	}
	if h.retry.Attempt() != 0 {
		t.Errorf("attempt = %d, want a fresh bound for the new turn", h.retry.Attempt())
	}
}

// A hold parks the run at the round tail: the round's results are already in
// the conversation, nothing has been asked of the model yet, and the next
// request goes out only once the hold is released.
func TestHeadlessRun_HoldsAtTheRoundTail(t *testing.T) {
	t.Parallel()
	a := New(nil, scriptedStream(t,
		toolCallRound(provider.ToolCall{ID: "c1", Name: "read_file", Arguments: `{"path":"x"}`}),
		doneRound("carried on"),
	))
	a.SetExecutor(func(string, json.RawMessage) (string, error) { return "contents", nil })

	release := make(chan struct{})
	asked := make(chan int, 4)
	h := &Headless{Agent: a}
	h.Hold = func() <-chan struct{} {
		asked <- len(a.Messages())
		return release
	}

	done := make(chan string, 1)
	go func() {
		final, err := h.Run("go")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		done <- final
	}()

	select {
	case n := <-asked:
		// The tool result is in the conversation, so nothing is re-asked
		// when the hold lets go: the request the run was about to make is
		// the request it makes.
		if n < 3 {
			t.Errorf("the round's results should be recorded before the park, %d messages", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the run never reached its round tail")
	}
	select {
	case final := <-done:
		t.Fatalf("the run went on through a standing hold, ending %q", final)
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	select {
	case final := <-done:
		if final != "carried on" {
			t.Errorf("final = %q, want %q", final, "carried on")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the release never let the run go")
	}
}

// And a held run is still killable: the wait registers its cancel where the
// stream's goes, so a run nobody is coming back to does not sit on a release
// that is never sent.
func TestHeadlessRun_InterruptEndsAHold(t *testing.T) {
	t.Parallel()
	a := New(nil, scriptedStream(t,
		toolCallRound(provider.ToolCall{ID: "c1", Name: "read_file", Arguments: `{"path":"x"}`}),
	))
	a.SetExecutor(func(string, json.RawMessage) (string, error) { return "contents", nil })

	h := &Headless{Agent: a}
	h.Hold = func() <-chan struct{} {
		go h.Interrupt()
		return make(chan struct{})
	}
	if _, err := h.Run("go"); !errors.Is(err, ErrInterrupted) {
		t.Fatalf("err = %v, want ErrInterrupted", err)
	}
}

// A run nothing holds never waits: the hook is the child loop's, and a
// scripted run has no keyboard to ask for one.
func TestHeadlessRun_NoHoldNoWait(t *testing.T) {
	t.Parallel()
	a := New(nil, scriptedStream(t,
		toolCallRound(provider.ToolCall{ID: "c1", Name: "read_file", Arguments: `{"path":"x"}`}),
		doneRound("done"),
	))
	a.SetExecutor(func(string, json.RawMessage) (string, error) { return "contents", nil })

	h := &Headless{Agent: a, Hold: func() <-chan struct{} { return nil }}
	if final, err := h.Run("go"); err != nil || final != "done" {
		t.Fatalf("run = %q, %v", final, err)
	}
}
