package chat

// The pointer (pointer.go): reading mode's cursor moved from the prompt.
// What these hold is the one property that makes it different from the mode
// — the keyboard never moves — and the one that makes it the same: what a
// chord does to a row is what the mode's key does to it.

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/provider"
)

func shiftKey(code rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: code, Mod: tea.ModShift} }

// pointerRow is the pane row carrying the gutter marker, as drawn, or "".
func pointerRow(m Model) string {
	for _, line := range strings.Split(ansi.Strip(m.viewport.View()), "\n") {
		if strings.HasPrefix(strings.TrimLeft(line, " "), "❯") {
			return line
		}
	}
	return ""
}

func TestPointer_ShiftArrowsLightAndMoveItWithoutTakingTheKeyboard(t *testing.T) {
	m := typeChars(t, focusModel(t), "half a sen")

	// The first press lights the pointer where reading mode would open:
	// the most recent row, which is the command.
	updated, _ := m.Update(shiftKey(tea.KeyUp))
	m = updated.(Model)
	if m.state != stateInput {
		t.Fatalf("the chord must leave the keyboard in the draft, got state %d", m.state)
	}
	if !m.pointerLit() || m.focusIdx != 2 {
		t.Fatalf("the first press should light the pointer on the last row, lit=%v idx=%d", m.pointerLit(), m.focusIdx)
	}
	if row := pointerRow(m); !strings.Contains(row, "go test") {
		t.Fatalf("the marker should be drawn on the command row, got %q", row)
	}
	// The second steps.
	updated, _ = m.Update(shiftKey(tea.KeyUp))
	m = updated.(Model)
	if m.focusIdx != 1 {
		t.Fatalf("up should step to the search row, got %d", m.focusIdx)
	}
	if row := pointerRow(m); !strings.Contains(row, "search") {
		t.Fatalf("the marker should follow to the search row, got %q", row)
	}
	updated, _ = m.Update(shiftKey(tea.KeyDown))
	m = updated.(Model)
	if m.focusIdx != 2 {
		t.Fatalf("down should step back, got %d", m.focusIdx)
	}
	if m.input.Value() != "half a sen" {
		t.Fatalf("the draft must be untouched, got %q", m.input.Value())
	}
}

func TestPointer_OpenAndCloseAreReadingModesEnterAndCollapse(t *testing.T) {
	m := typeChars(t, focusModel(t), "half a sen")
	for range 2 {
		updated, _ := m.Update(shiftKey(tea.KeyUp))
		m = updated.(Model)
	}
	if m.focusIdx != 1 || (*m.entries())[1].expanded {
		t.Fatal("the pointer should be on the collapsed search row")
	}
	updated, _ := m.Update(shiftKey(tea.KeyRight))
	m = updated.(Model)
	if !(*m.entries())[1].expanded {
		t.Fatal("shift+→ should open the pointed row, the way enter does under the cursor")
	}
	if m.state != stateInput || m.input.Value() != "half a sen" {
		t.Fatalf("opening a row must not move the keyboard or the draft: state %d, draft %q", m.state, m.input.Value())
	}
	updated, _ = m.Update(shiftKey(tea.KeyLeft))
	m = updated.(Model)
	if (*m.entries())[1].expanded {
		t.Fatal("shift+← should close it again")
	}
}

func TestPointer_EnterOnAnEmptyDraftOpensAndWithADraftSends(t *testing.T) {
	m := focusModel(t)
	for range 2 {
		updated, _ := m.Update(shiftKey(tea.KeyUp))
		m = updated.(Model)
	}
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if !(*m.entries())[1].expanded {
		t.Fatal("enter on an empty draft with the pointer lit should open the pointed row")
	}
	if len(m.transcript) != 3 {
		t.Fatalf("nothing should have been sent, transcript has %d entries", len(m.transcript))
	}

	m = typeChars(t, m, "a question")
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if len(m.transcript) < 4 || m.transcript[3].kind != entryUser || m.transcript[3].text != "a question" {
		t.Fatalf("enter with a draft should send the draft, pointer or not: %+v", m.transcript[len(m.transcript)-1])
	}
}

func TestPointer_EscDropsItAndLeavesTheDraft(t *testing.T) {
	m := typeChars(t, focusModel(t), "half a sen")
	updated, _ := m.Update(shiftKey(tea.KeyUp))
	m = updated.(Model)
	if !m.pointerLit() {
		t.Fatal("the pointer should be lit")
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)
	if m.pointerLit() {
		t.Fatal("esc should drop the pointer first")
	}
	if row := pointerRow(m); row != "" {
		t.Fatalf("the gutter should be gone from the pane, still see %q", row)
	}
	if m.input.Value() != "half a sen" {
		t.Fatalf("dropping the pointer must not cost the draft, got %q", m.input.Value())
	}
	// Typing never drops it.
	updated, _ = m.Update(shiftKey(tea.KeyUp))
	m = typeChars(t, updated.(Model), "tence")
	if !m.pointerLit() {
		t.Fatal("typing should leave the pointer where it is")
	}
}

func TestPointer_ReadingModeOpensOnThePointedRowAndLeavingDropsIt(t *testing.T) {
	m := focusModel(t)
	for range 2 {
		updated, _ := m.Update(shiftKey(tea.KeyUp))
		m = updated.(Model)
	}
	updated, _ := m.Update(readingChord())
	m = updated.(Model)
	if m.state != stateFocus || m.focusIdx != 1 {
		t.Fatalf("ctrl+o should open reading mode on the pointed row, got state %d idx %d", m.state, m.focusIdx)
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)
	if m.state != stateInput || m.pointerLit() {
		t.Fatalf("leaving reading mode returns to a prompt with no gutter, got state %d lit %v", m.state, m.pointerLit())
	}
}

func TestPointer_NothingToPointAtIsNothing(t *testing.T) {
	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	m = updated.(Model)
	m.appendEntry(entry{kind: entryUser, text: "a question with no row under it"})
	m.viewport.SetLines(m.renderHistoryLines())
	m.input.SetValue("line one\nline two")
	updated, _ = m.Update(shiftKey(tea.KeyUp))
	m = updated.(Model)
	if m.pointerLit() || m.state != stateInput || m.input.Value() != "line one\nline two" {
		t.Fatal("a pane with nothing to point at gives the chord nothing to do")
	}
	if row := pointerRow(m); row != "" {
		t.Fatalf("no gutter should be drawn, got %q", row)
	}
	// And the chord was still the input's: the textarea binds shift+↑ to
	// selecting a line, which would have moved the cursor up one.
	if m.input.Line() != 1 {
		t.Fatalf("the chord fell through to the textarea, cursor on line %d", m.input.Line())
	}
	for _, k := range []tea.KeyPressMsg{shiftKey(tea.KeyRight), shiftKey(tea.KeyLeft)} {
		updated, _ = m.Update(k)
		m = updated.(Model)
	}
	if m.input.Value() != "line one\nline two" || m.input.Line() != 1 {
		t.Fatal("open and close with nothing pointed at must not reach the textarea either")
	}
}

// The gutter is drawn by the feed's own render, so a flush of the stream
// keeps it; and a row that is gone takes the pointer with it.
func TestPointer_SurvivesAFlushAndGoesWithItsRow(t *testing.T) {
	m := focusModel(t)
	updated, _ := m.Update(shiftKey(tea.KeyUp))
	m = updated.(Model)
	m.streamDirty = true
	m.flushStream()
	if row := pointerRow(m); !strings.Contains(row, "go test") {
		t.Fatalf("a flush should redraw the gutter where it was, got %q", row)
	}
	m.transcript = m.transcript[:2]
	m.viewport.SetLines(m.renderHistoryLines())
	if m.pointerLit() {
		t.Fatal("the pointed row is gone, so there is no pointer")
	}
	if row := pointerRow(m); row != "" {
		t.Fatalf("no gutter should be drawn for a row that is gone, got %q", row)
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)
	if m.input.Value() != "" {
		t.Fatal("esc had nothing to drop and should have gone on to its other meanings")
	}
}

// A full-screen body opened over the pointer comes back to the pointed row,
// not to the live end.
func TestPointer_AFullScreenBodyClosesBackOntoThePointer(t *testing.T) {
	m := proseModel(t)
	m.appendEntry(entry{kind: entryCommand, text: "go test ./...", toolResult: strings.Repeat("line\n", 40), exitCode: 0})
	m.viewport.SetLines(m.renderHistoryLines())
	// The command row is the last row; the pointer lands on it, then walks
	// up into the prose so the pane is off its end.
	for range 4 {
		updated, _ := m.Update(shiftKey(tea.KeyUp))
		m = updated.(Model)
	}
	// Back down to the command and open it three times: collapsed →
	// expanded → the whole output full screen.
	for range 3 {
		updated, _ := m.Update(shiftKey(tea.KeyDown))
		m = updated.(Model)
	}
	idx := m.focusIdx
	for range 3 {
		updated, _ := m.Update(shiftKey(tea.KeyRight))
		m = updated.(Model)
		if m.state == stateOutputFull {
			break
		}
	}
	if m.state != stateOutputFull {
		t.Fatalf("three opens should have taken the output full screen, got state %d", m.state)
	}
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)
	if m.state != stateInput || !m.pointerLit() || m.focusIdx != idx {
		t.Fatalf("esc should return to the prompt with the pointer where it was: state %d lit %v idx %d", m.state, m.pointerLit(), m.focusIdx)
	}
	if row := pointerRow(m); !strings.Contains(row, "go test") {
		t.Fatalf("the pointed row should be back in view, got %q", row)
	}
}

// The pointer moves the pane to keep its row in view, and a reader moved off
// the live end by it stays there when the next flush lands.
func TestPointer_WalkingOffTheLiveEndIsAScroll(t *testing.T) {
	m := proseModel(t)
	for range 6 {
		updated, _ := m.Update(shiftKey(tea.KeyUp))
		m = updated.(Model)
	}
	if !m.pointerLit() || m.viewport.AtBottom() {
		t.Fatalf("six presses up should have walked the pane off its end: lit %v, at bottom %v", m.pointerLit(), m.viewport.AtBottom())
	}
	if m.atBottom {
		t.Fatal("a pane the pointer scrolled must not be pinned to the live end")
	}
}

func TestPointer_WithACardWaitingTheChordIsTheDraftsUntilTheHandover(t *testing.T) {
	m := interruptedModel(t, "also add a --max-rounds flag")
	updated, _ := m.Update(shiftKey(tea.KeyUp))
	m = updated.(Model)
	if m.state != stateConfirmRun {
		t.Fatalf("the card should still be waiting, got state %d", m.state)
	}
	if m.cardScroll != 0 {
		t.Fatal("with the draft holding the keyboard the chord is not the card's scroll")
	}
	m = handover(t, m)
	updated, _ = m.Update(shiftKey(tea.KeyDown))
	m = updated.(Model)
	if m.pointer {
		t.Fatal("with the card holding the keyboard the chord is the card's, not the pointer's")
	}
}
