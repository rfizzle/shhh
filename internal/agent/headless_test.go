package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
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
		OnToolResult: func(tc provider.ToolCall, r string) { results = append(results, r) },
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
