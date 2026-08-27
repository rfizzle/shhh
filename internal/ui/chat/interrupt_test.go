package chat

// When a decision lands mid-sentence (S-117, DESIGN-TUI.md §7b). The hazard
// these tests hold shut is the one the story opened with: a card that arrives
// unbidden used to take the keyboard, so a sentence containing the word "yes"
// could approve a shell command before it had been read.

import (
	"encoding/json"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/subagent"
)

// handover hands the keyboard to the decision on screen — what ctrl+g does
// for a reader (§7b). A card that arrives unbidden holds no keyboard, so a
// test that answers one presses this first, exactly as a user would.
func handover(t *testing.T, m Model) Model {
	t.Helper()
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	rm, ok := next.(Model)
	if !ok {
		t.Fatal("ctrl+g should return the chat model")
	}
	if !rm.decisionGated() {
		t.Fatal("ctrl+g should hand the keyboard to the decision on screen")
	}
	return rm
}

// interruptedModel is a session with a half-typed sentence in the draft and a
// gated write_file call arriving on top of it.
func interruptedModel(t *testing.T, draft string) Model {
	t.Helper()
	executor := func(name string, args json.RawMessage) (string, error) {
		t.Fatalf("no tool may run without an answer, but %s did", name)
		return "", nil
	}
	m := gatedModel(t, executor, map[string]GatedPreviewFunc{
		"write_file": writeFilePreview("line one\n"),
	})
	m.width, m.height = 130, 40
	m.syncInputWidth()
	m.input.SetValue(draft)
	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_w", Name: "write_file", Arguments: `{"path":"main.go","content":"line one\nline two\n"}`},
	}})
	m = updated.(Model)
	if m.state != stateConfirmRun {
		t.Fatalf("the gated call should have raised a decision, got %d", m.state)
	}
	return m
}

func TestInterrupt_ALetterGoesIntoTheSentenceNotIntoTheCard(t *testing.T) {
	const draft = "also add a --max-rounds flag"
	m := interruptedModel(t, draft)

	// The most dangerous keystroke in the product: y, typed mid-sentence,
	// while an edit is waiting.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m = updated.(Model)

	if m.state != stateConfirmRun {
		t.Fatalf("a letter must leave the decision waiting, state is now %d", m.state)
	}
	if got := m.input.Value(); got != draft+"y" {
		t.Fatalf("the letter belongs in the draft, got %q", got)
	}
	// The other three, and enter, which is how a sentence ends.
	for _, k := range []rune{'n', 'a', 'd', 'A'} {
		updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{k}})
		m = updated.(Model)
		if m.state != stateConfirmRun {
			t.Fatalf("%q answered a card that does not hold the keyboard", k)
		}
	}
	if got := m.input.Value(); got != draft+"ynadA" {
		t.Fatalf("every letter belongs in the draft, got %q", got)
	}
}

func TestInterrupt_TheCardSaysItsKeysAreNotLiveAndOffersTheOneThatIs(t *testing.T) {
	m := interruptedModel(t, "also add a --max-rounds flag")

	view := ansi.Strip(m.View())
	for _, want := range []string{"not live yet", "[ctrl+g] answer it", "these letters go into your draft"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the ungated card should say %q:\n%s", want, view)
		}
	}
	// The rail names the surface that has the keyboard, in words — the check
	// invariant 5 asks for is that covering the colours still answers it.
	if !strings.Contains(view, "DRAFT") {
		t.Fatalf("the rail should name the draft as the keyboard's owner:\n%s", view)
	}
	if strings.Contains(view, "DECISION") {
		t.Fatalf("only one surface names itself as the holder:\n%s", view)
	}
	// And the frame counts what is waiting rather than claiming to be idle.
	if !strings.Contains(view, "⏸ 1 waiting") {
		t.Fatalf("the frame's top rail should count the waiting decision:\n%s", view)
	}

	m = handover(t, m)
	view = ansi.Strip(m.View())
	if strings.Contains(view, "not live yet") {
		t.Fatalf("a gated card's keys are ordinary keys:\n%s", view)
	}
	if !strings.Contains(view, "DECISION") || strings.Contains(view, "DRAFT") {
		t.Fatalf("the rail should move to the decision:\n%s", view)
	}
}

func TestInterrupt_TheDraftSurvivesTheWholeRoundTrip(t *testing.T) {
	const draft = "also add a --max-rounds flag while you're in there"
	m := interruptedModel(t, draft)
	// Park the cursor mid-word, where a reader would have left it.
	m.input.SetCursor(24)
	before := m.draftCursor()

	m = handover(t, m)
	if got := m.input.Value(); got != draft {
		t.Fatalf("gating must not touch the draft, got %q", got)
	}
	if got := m.draftCursor(); got != before {
		t.Fatalf("gating must not move the cursor, %d → %d", before, got)
	}
	// The undressed frame states the position it is holding, so the reader
	// can see that nothing moved (§7b).
	view := ansi.Strip(m.View())
	if !strings.Contains(view, m.draftPosition()) {
		t.Fatalf("the held draft should state its own position (%q):\n%s", m.draftPosition(), view)
	}
	if !strings.Contains(view, draft) {
		t.Fatalf("the held draft should still show every character:\n%s", view)
	}

	// Answer it. The keyboard comes back to the draft, at the same character.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m = updated.(Model)
	if m.decisionHeld {
		t.Fatal("answering hands the keyboard back to the draft")
	}
	if got := m.input.Value(); got != draft {
		t.Fatalf("answering must not clear or submit the draft, got %q", got)
	}
	if got := m.draftCursor(); got != before {
		t.Fatalf("answering must not move the cursor to the end, %d → %d", before, got)
	}
}

func TestInterrupt_EscLeavesTheDecisionWaitingRatherThanDenyingIt(t *testing.T) {
	m := interruptedModel(t, "half a sentence")
	m = handover(t, m)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	m = updated.(Model)

	if m.state != stateConfirmRun {
		t.Fatalf("esc leaves the decision unanswered, state is now %d", m.state)
	}
	if m.decisionHeld {
		t.Fatal("esc hands the keyboard back to the draft")
	}
	if got := m.input.Value(); got != "half a sentence" {
		t.Fatalf("esc on the card is not esc on the draft, got %q", got)
	}
	// And the card says so while it holds the keyboard, because the safe
	// answer here is not obvious (invariant 3).
	gatedView := ansi.Strip(handover(t, m).View())
	if !strings.Contains(gatedView, "back to your draft") {
		t.Fatalf("the gated card should state what esc does:\n%s", gatedView)
	}
	// Saying no is still [n].
	updated, _ = handover(t, m).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if next := updated.(Model); next.state == stateConfirmRun {
		t.Fatal("[n] is how a decision is denied")
	}
}

func TestInterrupt_TheDraftKeepsItsOwnKeysWhileTheCardWaits(t *testing.T) {
	m := interruptedModel(t, "queue this")

	// Enter queues the sentence for the next round rather than starting a
	// turn the waiting decision would interrupt (§7b).
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.state != stateConfirmRun {
		t.Fatalf("enter is the draft's, not the card's, state is now %d", m.state)
	}
	if len(m.steering) != 1 || m.steering[0] != "queue this" {
		t.Fatalf("enter should queue the sentence, got %v", m.steering)
	}
	if got := m.input.Value(); got != "" {
		t.Fatalf("a queued sentence leaves the draft, got %q", got)
	}
	// Esc on the draft is still the draft's esc: it clears what was typed and
	// leaves the decision alone.
	m.input.SetValue("scratch that")
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	m = updated.(Model)
	if m.state != stateConfirmRun {
		t.Fatalf("esc on the draft must not reach the card, state is now %d", m.state)
	}
	if got := m.input.Value(); got != "" {
		t.Fatalf("esc clears the draft it belongs to, got %q", got)
	}
}

func TestInterrupt_ASecondDecisionArrivesWithoutTheKeyboard(t *testing.T) {
	m := interruptedModel(t, "")
	m = handover(t, m)

	// Answer the first; the next one arms behind it.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m = updated.(Model)
	if m.decisionHeld {
		t.Fatal("the keyboard goes back to the draft between decisions")
	}

	updated, _ = m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_x", Name: "write_file", Arguments: `{"path":"other.go","content":"x\n"}`},
	}})
	m = updated.(Model)
	if m.state != stateConfirmRun {
		t.Fatalf("the second call should raise its own decision, got %d", m.state)
	}
	if !m.decisionUngated() {
		t.Fatal("a decision may never inherit the keyboard the last one was given")
	}
}

func TestInterrupt_ARoutedChildApprovalIsInertUntilItHoldsTheKeyboard(t *testing.T) {
	m := frameModel(t, 130, 40)
	m.childAsks = []*subagent.Ask{subagent.NewAsk("researcher-1", subagent.AskCommand, "run make")}
	m.input.SetValue("keep going")

	if !m.decisionUngated() {
		t.Fatal("a routed approval arrives the way every other decision does")
	}
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "not live yet") || !strings.Contains(view, "DRAFT") {
		t.Fatalf("the routed card should render as not-yet-live:\n%s", view)
	}
	// [g] jumps to the agent — but only once the card has the keyboard.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	m = updated.(Model)
	if m.attachedTo != "" {
		t.Fatal("a bare letter must not reach a card that does not hold the keyboard")
	}
	if got := m.input.Value(); got != "keep goingg" {
		t.Fatalf("the letter belongs in the draft, got %q", got)
	}
}

// The rail is the check invariant 5 states: cover the colours, and the screen
// still names the surface holding the keyboard.
func TestInterrupt_TheRailFallsBackRatherThanClippingTheWord(t *testing.T) {
	if got := ansi.Strip(keyboardRail("DECISION 1/2", 200)); !strings.Contains(got, "DECISION 1/2") {
		t.Fatalf("a wide rail carries its label, got %q", got)
	}
	narrow := ansi.Strip(keyboardRail("DECISION 1/2", 10))
	if strings.Contains(narrow, "DECIS") {
		t.Fatalf("too narrow for the word, the rail is a plain divider, got %q", narrow)
	}
	if len([]rune(narrow)) != 10 {
		t.Fatalf("the fallback still fills its width, got %q", narrow)
	}
}
