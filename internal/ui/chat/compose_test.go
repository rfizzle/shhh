package chat

import (
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/provider"
)

// fragment is one argument fragment off the stream, the way a round writing a
// call delivers it.
func fragment(id string, n int) toolDeltaMsg {
	return toolDeltaMsg{delta: provider.ToolCallDelta{ID: id, Arguments: strings.Repeat("x", n)}}
}

// composingModel is a model with a round's stream open, which is what the
// compose row is a reading of.
func composingModel(t *testing.T) Model {
	t.Helper()
	m := streamingModel(t)
	events := make(chan provider.StreamEvent)
	updated, _ := m.Update(streamStartedMsg{events: events})
	return updated.(Model)
}

// TestComposeRow_CountsWhatTheRoundHasWritten is the row's whole job: a call
// long enough to wait for says how much of it has arrived, and keeps saying
// so as more does.
func TestComposeRow_CountsWhatTheRoundHasWritten(t *testing.T) {
	m := composingModel(t)

	updated, _ := m.Update(fragment("call_1", 2500))
	m = updated.(Model)
	view := stripANSI(m.renderHistory())
	if !strings.Contains(view, "compose") || !strings.Contains(view, "2 KB") {
		t.Fatalf("the row should count the bytes written:\n%s", view)
	}

	updated, _ = m.Update(fragment("call_1", 2500))
	m = updated.(Model)
	if view := stripANSI(m.renderHistory()); !strings.Contains(view, "5 KB") {
		t.Fatalf("the count should have grown with the call:\n%s", view)
	}
	if got := len(m.transcript); got != 0 {
		t.Fatalf("the reading is the round in flight, not history, got %d entries", got)
	}
}

// TestComposeRow_UnderTheAnswerSoFar: the reader is at the bottom of the
// transcript, and the row reports what is happening now — so it is drawn
// below what the round has already said, not above it.
func TestComposeRow_UnderTheAnswerSoFar(t *testing.T) {
	m := composingModel(t)
	updated, _ := m.Update(tokenMsg{text: "Rewriting the loop now."})
	m = updated.(Model)
	updated, _ = m.Update(fragment("call_1", 2500))
	m = updated.(Model)

	view := stripANSI(m.renderHistory())
	answer, row := strings.Index(view, "Rewriting the loop now."), strings.Index(view, "compose")
	if answer < 0 || row < 0 {
		t.Fatalf("expected the answer and the row:\n%s", view)
	}
	if row < answer {
		t.Fatalf("the row belongs under the answer it followed:\n%s", view)
	}
}

// TestComposeRow_KeepsTheTranscriptsSpacing: the row is joined to whatever is
// above it the way the transcript joins anything else — a blank line under a
// block, nothing between two rows — including on the round that says nothing
// at all before it calls a tool.
func TestComposeRow_KeepsTheTranscriptsSpacing(t *testing.T) {
	blank := func(view string) bool {
		lines := strings.Split(strings.TrimRight(view, "\n"), "\n")
		for i, line := range lines {
			if strings.Contains(line, "compose") {
				return i > 0 && strings.TrimSpace(lines[i-1]) == ""
			}
		}
		t.Fatalf("expected a compose row:\n%s", view)
		return false
	}

	// Under a block: the user's own message, with the model going straight to
	// a call.
	m := composingModel(t)
	m.appendEntry(entry{kind: entryUser, text: "rewrite the loop"})
	updated, _ := m.Update(fragment("call_1", 2000))
	m = updated.(Model)
	if !blank(stripANSI(m.renderHistory())) {
		t.Errorf("a block above the row is followed by a blank line:\n%s", stripANSI(m.renderHistory()))
	}

	// Under the answer as it arrives, which is a block whose last line the
	// transcript has not closed yet.
	updated, _ = m.Update(tokenMsg{text: "Rewriting it now."})
	m = updated.(Model)
	if !blank(stripANSI(m.renderHistory())) {
		t.Errorf("the arriving answer is a block too:\n%s", stripANSI(m.renderHistory()))
	}

	// Under a row: the round's own thinking, which is not a block.
	m = composingModel(t)
	m.appendEntry(entry{kind: entryThink, text: "weighing it"})
	updated, _ = m.Update(fragment("call_1", 2000))
	m = updated.(Model)
	if blank(stripANSI(m.renderHistory())) {
		t.Errorf("two rows sit against each other:\n%s", stripANSI(m.renderHistory()))
	}
}

// TestComposeRow_GoesWhenTheCallsLand: what replaces the reading is the rows
// of the calls it was counting, and one act does not get two rows.
func TestComposeRow_GoesWhenTheCallsLand(t *testing.T) {
	m := composingModel(t)
	updated, _ := m.Update(fragment("call_1", 2500))
	m = updated.(Model)
	if view := stripANSI(m.renderHistory()); !strings.Contains(view, "compose") {
		t.Fatalf("expected the row while the round is open:\n%s", view)
	}

	updated, _ = m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_1", Name: "write_file", Arguments: `{"path":"main.go","content":"x"}`},
	}})
	m = updated.(Model)
	if view := stripANSI(m.renderHistory()); strings.Contains(view, "compose") {
		t.Fatalf("the reading goes when the round's calls land:\n%s", view)
	}
}

// TestComposeRow_NewRoundNewCount: the count belongs to the round that wrote
// it, so the next request does not inherit it.
func TestComposeRow_NewRoundNewCount(t *testing.T) {
	m := composingModel(t)
	updated, _ := m.Update(fragment("call_1", 2500))
	m = updated.(Model)

	events := make(chan provider.StreamEvent)
	updated, _ = m.Update(streamStartedMsg{events: events})
	m = updated.(Model)
	if m.composed != 0 {
		t.Fatalf("a new round starts at nothing, got %d", m.composed)
	}
	if view := stripANSI(m.renderHistory()); strings.Contains(view, "compose") {
		t.Fatalf("the last round's count does not carry over:\n%s", view)
	}
}

// TestComposeRow_SilentUntilTheCallIsWorthWatching: a read or a search is a
// path and a pattern, and a row counting them would be a row on every round.
func TestComposeRow_SilentUntilTheCallIsWorthWatching(t *testing.T) {
	m := composingModel(t)

	updated, _ := m.Update(fragment("call_1", 120))
	m = updated.(Model)
	if view := stripANSI(m.renderHistory()); strings.Contains(view, "compose") {
		t.Fatalf("a short call draws no row:\n%s", view)
	}

	// The bytes were still counted, so the row states the whole call when it
	// crosses rather than only the part that crossed.
	updated, _ = m.Update(fragment("call_1", composeFloor))
	m = updated.(Model)
	if view := stripANSI(m.renderHistory()); !strings.Contains(view, "1 KB") {
		t.Fatalf("the row appears counting everything the round wrote:\n%s", view)
	}
}

// TestComposeRow_NamesNoTarget: a fragment carries the call's id and its
// bytes, and the tool's name arrives with the finished call — so the row
// states a size and never guesses at what is being written.
func TestComposeRow_NamesNoTarget(t *testing.T) {
	m := composingModel(t)
	updated, _ := m.Update(toolDeltaMsg{delta: provider.ToolCallDelta{
		ID:        "call_1",
		Arguments: `{"path":"internal/agent/loop.go","content":"` + strings.Repeat("x", 2000),
	}})
	m = updated.(Model)

	view := stripANSI(m.renderHistory())
	if strings.Contains(view, "internal/agent/loop.go") {
		t.Fatalf("the row renders no part of the half-written arguments:\n%s", view)
	}
	if strings.Contains(view, "running…") {
		t.Fatalf("what moves on the row is the count, not an outcome:\n%s", view)
	}
}

// TestComposeRow_WaitsForTheTick: a stream that sends its arguments in small
// chunks would otherwise re-render the transcript once per chunk.
func TestComposeRow_WaitsForTheTick(t *testing.T) {
	m := composingModel(t)
	m.spinning = true

	updated, _ := m.Update(tokenMsg{text: "Writing it now.", final: fragment("call_1", 2000)})
	m = updated.(Model)
	if !m.streamDirty {
		t.Fatal("a fragment owes a repaint, it does not take one")
	}
	if m.composed != 2000 {
		t.Fatalf("the fragment should still have been counted, got %d", m.composed)
	}

	// A terminal message is a different matter: a round that has ended has to
	// draw what it ended with.
	m.streamDirty = false
	updated, _ = m.Update(tokenMsg{text: "done", final: doneMsg{}})
	m = updated.(Model)
	if m.streamDirty {
		t.Fatal("a batch that ended the round repaints rather than owing one")
	}
}

// TestComposeRow_RepaintsOnABareFragment: the fragments of one call arrive
// with no text between them, so most of them are a message of their own and
// never reach the token batch's repaint rule. A count that moved only when
// text happened to arrive beside it would stop moving exactly when the model
// stopped talking, which is the moment the row exists for.
func TestComposeRow_RepaintsOnABareFragment(t *testing.T) {
	m := composingModel(t)
	m.spinning = true

	updated, _ := m.Update(fragment("call_1", 2000))
	m = updated.(Model)
	if !m.streamDirty {
		t.Fatal("a bare fragment owes a repaint")
	}

	// With no tick running there is nothing to owe it to, so the fragment
	// takes the repaint itself.
	m.spinning, m.streamDirty = false, false
	renders := 0
	testHookRenderHistory = func() { renders++ }
	t.Cleanup(func() { testHookRenderHistory = nil })

	updated, _ = m.Update(fragment("call_1", 2000))
	m = updated.(Model)
	if renders == 0 {
		t.Fatal("a fragment with nothing ticking repaints itself")
	}
	if m.streamDirty {
		t.Fatal("a repaint taken is not a repaint owed")
	}
}

// TestComposeRow_NotDuringCompaction: a compaction calls no tools, and a
// reading of one would describe something that never happened.
func TestComposeRow_NotDuringCompaction(t *testing.T) {
	m := composingModel(t)
	m.compacting = true

	updated, _ := m.Update(fragment("call_1", 2000))
	m = updated.(Model)
	if m.composed != 0 {
		t.Fatalf("a compaction counts nothing, got %d", m.composed)
	}
}

// TestComposeRow_Verbosity: low verbosity is step headers only, and this row
// reports no act of its own.
func TestComposeRow_Verbosity(t *testing.T) {
	m := composingModel(t)
	updated, _ := m.Update(fragment("call_1", 2000))
	m = updated.(Model)

	for _, tc := range []struct {
		level verbosity
		drawn bool
	}{{verbosityLow, false}, {verbosityNormal, true}, {verbosityHigh, true}} {
		m.verbosity = tc.level
		m.invalidateRenderCache()
		view := stripANSI(m.renderHistory())
		if got := strings.Contains(view, "compose"); got != tc.drawn {
			t.Errorf("%s verbosity: row drawn = %v, want %v:\n%s", tc.level, got, tc.drawn, view)
		}
	}
}

// TestComposeRow_FragmentsRouteOffTheStream: an event carrying a fragment
// becomes the message the reading is fed by, and carries nothing else.
func TestComposeRow_FragmentsRouteOffTheStream(t *testing.T) {
	msg := terminalMsg(provider.StreamEvent{ToolCallDelta: &provider.ToolCallDelta{ID: "call_1", Arguments: "{"}})
	delta, ok := msg.(toolDeltaMsg)
	if !ok {
		t.Fatalf("expected a fragment message, got %T", msg)
	}
	if delta.delta.ID != "call_1" || delta.delta.Arguments != "{" {
		t.Errorf("unexpected fragment: %+v", delta.delta)
	}
	if got := terminalMsg(provider.StreamEvent{Token: "hello"}); got != nil {
		t.Errorf("a plain token is not a fragment: %T", got)
	}
}
