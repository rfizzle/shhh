package agent

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/provider"
)

// recordedRequest is one request a test's stream was asked for: what it
// carried and what it was allowed to do about tools.
type recordedRequest struct {
	msgs   []provider.Message
	choice string
}

// recordingStream is scriptedStream that also keeps every request, so a test
// can assert what a compaction actually sent rather than only what came back.
func recordingStream(t *testing.T, into *[]recordedRequest, rounds ...[]provider.StreamEvent) StreamFunc {
	t.Helper()
	i := 0
	return func(msgs []provider.Message, choice string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		*into = append(*into, recordedRequest{msgs: msgs, choice: choice})
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

// prose is a message of about n bytes, so a fixture can be built to a size
// rather than to a word count.
func prose(role provider.Role, n int) provider.Message {
	return provider.Message{Role: role, Content: strings.Repeat("word ", n/5)}
}

// filledWithProse is a conversation over the trim threshold of a 4000-token
// window with nothing a trim can take: every message is text, and text is
// what a trim always keeps.
func filledWithProse() []provider.Message {
	return []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		prose(provider.RoleUser, 7000),
		prose(provider.RoleAssistant, 7000),
	}
}

const testWindow = 4000

func TestHeadlessCompactsWhereATrimCannotRecover(t *testing.T) {
	var reqs []recordedRequest
	a := New(filledWithProse(), recordingStream(t, &reqs,
		doneRound("the conversation so far, summarised"),
		doneRound("done"),
	))

	var notices []CompactNotice
	h := &Headless{
		Agent:     a,
		Compact:   &Compactor{Model: "test-model", Window: testWindow},
		OnCompact: func(n CompactNotice) { notices = append(notices, n) },
	}

	final, err := h.Run("carry on")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if final != "done" {
		t.Fatalf("final = %q, want %q", final, "done")
	}
	if len(reqs) != 2 {
		t.Fatalf("expected the summary request and the turn's own, got %d", len(reqs))
	}

	// The summary request: the whole conversation, the instruction last, and
	// a choice that forbids the model answering with the tool call the turn
	// was about to make.
	summary := reqs[0]
	if summary.choice != provider.ToolChoiceNone {
		t.Fatalf("summary request choice = %q, want %q", summary.choice, provider.ToolChoiceNone)
	}
	if last := summary.msgs[len(summary.msgs)-1]; last.Content != CompactInstruction {
		t.Fatalf("summary request does not end with the instruction: %q", last.Content)
	}
	if reqs[1].choice != provider.ToolChoiceAuto {
		t.Fatalf("the turn's own request lost its tools: choice = %q", reqs[1].choice)
	}

	// The system prompt, the summary, the turn being answered, and the answer
	// the rebuilt conversation went on to produce.
	msgs := a.Messages()
	if len(msgs) != 4 {
		t.Fatalf("rebuilt conversation has %d messages: %+v", len(msgs), msgs)
	}
	if msgs[0].Role != provider.RoleSystem || msgs[0].Content != "sys" {
		t.Fatalf("system prompt did not survive: %+v", msgs[0])
	}
	if !strings.HasPrefix(msgs[1].Content, CompactSummaryPrefix) {
		t.Fatalf("summary is not the second message: %+v", msgs[1])
	}
	if msgs[2].Role != provider.RoleUser || msgs[2].Content != "carry on" {
		t.Fatalf("the turn being answered was not kept: %+v", msgs[2])
	}

	if len(notices) != 1 || !notices[0].Compacted {
		t.Fatalf("expected one compaction reported, got %+v", notices)
	}
	if notices[0].BeforePct < TrimThresholdPercent || notices[0].AfterPct >= TrimThresholdPercent {
		t.Fatalf("notice does not show the window recovered: %+v", notices[0])
	}
	if notices[0].Notice == "" {
		t.Fatal("a compaction nobody was watching reported no line")
	}
}

// The trim is the cheaper answer and it is the one that runs: a conversation
// whose bulk is old tool output never reaches the summary request.
func TestHeadlessTrimsBeforeItCompacts(t *testing.T) {
	var reqs []recordedRequest
	a := New([]provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "look at this"},
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "c1", Name: "read_file"}}},
		prose(provider.RoleTool, 14000),
	}, recordingStream(t, &reqs, doneRound("done")))
	a.messages[3].ToolCallID = "c1"

	var notices []CompactNotice
	h := &Headless{
		Agent:     a,
		Compact:   &Compactor{Model: "test-model", Window: testWindow},
		OnCompact: func(n CompactNotice) { notices = append(notices, n) },
	}

	if _, err := h.Run("and now this"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(reqs) != 1 {
		t.Fatalf("a trim that cleared the line still asked for a summary: %d requests", len(reqs))
	}
	if len(notices) != 1 || notices[0].Elided != 1 || notices[0].Compacted {
		t.Fatalf("expected one trim and no compaction, got %+v", notices)
	}
	if !strings.HasPrefix(a.Messages()[3].Content, elidedPrefix) {
		t.Fatalf("the old result was not elided: %q", a.Messages()[3].Content)
	}
}

// What a compaction leaves has to be a conversation every dialect will
// accept, which is a stricter thing than one that reads well: a tail cut
// inside a tool round opens with results for calls the model can no longer
// see it made, and that is a request the provider refuses rather than a
// summary that is merely lossy.
func TestCompactRebuildsAWellFormedConversation(t *testing.T) {
	a := New(nil, nil)
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	// Two turns that fill the window and a small one behind them, which is
	// the shape that has a tail worth keeping: a last turn the size of the
	// others would not fit the bound and there would be nothing to cut.
	for _, size := range []int{8000, 8000, 500} {
		id := "c" + strconv.Itoa(len(msgs))
		msgs = append(msgs,
			prose(provider.RoleUser, size),
			provider.Message{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: id, Name: "search"}}},
			provider.Message{Role: provider.RoleTool, ToolCallID: id, Content: "hits"},
			provider.Message{Role: provider.RoleAssistant, Content: "answered"},
		)
	}
	a.SetMessages(msgs)

	c := &Compactor{Model: "test-model", Window: testWindow}
	n := c.Recover(a, func(msgs []provider.Message, choice string) (string, error) {
		if choice != provider.ToolChoiceNone {
			t.Fatalf("summary asked under choice %q", choice)
		}
		return "what happened", nil
	})
	if !n.Compacted {
		t.Fatalf("expected a compaction, got %+v", n)
	}

	got := a.Messages()
	if got[0].Role != provider.RoleSystem {
		t.Fatalf("rebuilt conversation does not open with the system prompt: %+v", got[0])
	}
	if got[1].Role != provider.RoleUser || !strings.HasPrefix(got[1].Content, CompactSummaryPrefix) {
		t.Fatalf("the summary is not the first thing said: %+v", got[1])
	}
	if n.Kept != 1 {
		t.Fatalf("kept %d turns, want the last one alone: %+v", n.Kept, got)
	}
	if got[2].Role != provider.RoleUser {
		t.Fatalf("the kept tail does not start at a turn: %+v", got[2])
	}
	called := map[string]bool{}
	for _, msg := range got {
		for _, tc := range msg.ToolCalls {
			called[tc.ID] = true
		}
		if msg.Role == provider.RoleTool && !called[msg.ToolCallID] {
			t.Fatalf("a result survived without the call it answers: %+v", msg)
		}
	}
}

// A summary that came back empty is not asked for again until the window has
// gone back under the line and crossed it afresh. The request carries the
// whole conversation, so asking once a round is the most expensive way there
// is to keep failing.
func TestCompactorAsksOncePerCrossing(t *testing.T) {
	a := New(filledWithProse(), nil)
	c := &Compactor{Model: "test-model", Window: testWindow}
	asks := 0
	ask := func([]provider.Message, string) (string, error) {
		asks++
		return "", errors.New("the provider refused")
	}

	first := c.Recover(a, ask)
	if first.Err == nil || first.Compacted {
		t.Fatalf("expected a failed compaction, got %+v", first)
	}
	if second := c.Recover(a, ask); second.Compacted {
		t.Fatalf("a failed compaction was retried into a success: %+v", second)
	}
	if asks != 1 {
		t.Fatalf("asked %d times on one crossing, want 1", asks)
	}

	// Under the line and over it again: a new crossing gets its own attempt.
	a.SetMessages([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}})
	c.Recover(a, ask)
	a.SetMessages(filledWithProse())
	if n := c.Recover(a, ask); n.Err == nil {
		t.Fatalf("a fresh crossing did not ask again: %+v", n)
	}
	if asks != 2 {
		t.Fatalf("asked %d times over two crossings, want 2", asks)
	}
}

// A crossing a trim settles is still a crossing that ended, so the attempt
// comes back. Without that a child — which keeps one step across every turn
// of its life — spends its one attempt on a summary that failed once and
// never asks for another.
func TestCompactorRearmsWhenATrimEndsTheCrossing(t *testing.T) {
	// Over the line, with one old result big enough that eliding it settles
	// the crossing on its own.
	filled := func() []provider.Message {
		return []provider.Message{
			{Role: provider.RoleSystem, Content: "sys"},
			{Role: provider.RoleUser, Content: "look"},
			{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "c1", Name: "search"}}},
			{Role: provider.RoleTool, ToolCallID: "c1", Content: strings.Repeat("word ", 3000)},
			prose(provider.RoleUser, 200),
		}
	}
	a := New(filledWithProse(), nil)
	c := &Compactor{Model: "test-model", Window: testWindow}
	asks := 0
	ask := func([]provider.Message, string) (string, error) {
		asks++
		return "", errors.New("the provider refused")
	}

	// A crossing that spends the attempt.
	if n := c.Recover(a, ask); n.Err == nil {
		t.Fatalf("expected a failed compaction, got %+v", n)
	}
	// A second crossing a trim settles on its own.
	a.SetMessages(filled())
	if n := c.Recover(a, ask); n.Elided != 1 || n.Compacted {
		t.Fatalf("expected the trim to settle this crossing, got %+v", n)
	}
	// A third: the attempt is back, because the second crossing ended.
	a.SetMessages(filledWithProse())
	if n := c.Recover(a, ask); n.Err == nil {
		t.Fatalf("a crossing after one a trim settled did not ask: %+v", n)
	}
	if asks != 2 {
		t.Fatalf("asked %d times over three crossings, want 2", asks)
	}
}

// An interrupted request was never answered, so it was not the crossing's
// attempt and it is not a failure to report: the run is ending either way.
func TestCompactorInterruptIsNotAnAttempt(t *testing.T) {
	a := New(filledWithProse(), nil)
	c := &Compactor{Model: "test-model", Window: testWindow}
	if n := c.Recover(a, func([]provider.Message, string) (string, error) {
		return "", ErrInterrupted
	}); n.Err != nil || n.Notice != "" {
		t.Fatalf("an interrupt was reported as a compaction failure: %+v", n)
	}
	// Still on the same crossing, and the attempt is still there to spend.
	asked := false
	n := c.Recover(a, func([]provider.Message, string) (string, error) {
		asked = true
		return "", errors.New("the provider refused")
	})
	if !asked || n.Err == nil {
		t.Fatalf("the attempt was spent on a request nobody waited for: %+v", n)
	}
}

// A window nobody could name is not a line anything can cross: the step is
// off, and the run behaves exactly as it did before the step existed.
func TestCompactorWithoutAWindowDoesNothing(t *testing.T) {
	a := New(filledWithProse(), nil)
	before := len(a.Messages())
	c := &Compactor{Model: "unknown-model"}
	n := c.Recover(a, func([]provider.Message, string) (string, error) {
		t.Fatal("asked for a summary against a window nothing could name")
		return "", nil
	})
	if n.Notice != "" || len(a.Messages()) != before {
		t.Fatalf("a step ran without a window: %+v", n)
	}
}

// The request forbids a tool call, so one coming back is a provider that did
// not honour that. The conversation is left exactly as it was rather than
// restarted from arguments the model was told not to write.
func TestHeadlessRefusesASummaryThatCalledATool(t *testing.T) {
	a := New(filledWithProse(), scriptedStream(t,
		toolCallRound(provider.ToolCall{ID: "c1", Name: "read_file"}),
		doneRound("done"),
	))
	before := len(a.Messages())

	var notices []CompactNotice
	h := &Headless{
		Agent:     a,
		Compact:   &Compactor{Model: "test-model", Window: testWindow},
		OnCompact: func(n CompactNotice) { notices = append(notices, n) },
	}

	final, err := h.Run("carry on")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if final != "done" {
		t.Fatalf("the run did not go on after a refused compaction: %q", final)
	}
	// The turn's own user message is the only thing that joined it.
	if len(a.Messages()) != before+2 {
		t.Fatalf("the conversation was rebuilt from a tool call: %+v", a.Messages())
	}
	if len(notices) != 1 || !errors.Is(notices[0].Err, errCompactToolCall) {
		t.Fatalf("expected the refusal reported, got %+v", notices)
	}
}

// The estimate the crossing is measured by counts what the request carries
// and not only what the conversation holds, and it is corrected by what the
// provider said the last one came to.
func TestCompactorEstimateCountsToolsAndTheCorrection(t *testing.T) {
	msgs := []provider.Message{prose(provider.RoleUser, 4000)}
	c := &Compactor{Model: "test-model", Window: testWindow, ToolTokens: 250}
	raw := EstimateMessageTokens(msgs) + 250
	if got := c.Estimate(msgs); got != raw {
		t.Fatalf("estimate = %d before any report, want %d", got, raw)
	}
	// A provider that charged half again what was estimated for the same
	// messages moves the estimate towards what it charged.
	c.Observe(raw*3/2, msgs)
	if got := c.Estimate(msgs); got <= raw {
		t.Fatalf("estimate = %d after a larger report, want more than %d", got, raw)
	}
}
