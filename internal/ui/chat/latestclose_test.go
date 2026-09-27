package chat

// The newest close's offers, drawn and answered with nothing selected
// (docs/interface/surfaces.md#the-turns-close), and the pointer they were
// designed around: nothing here moves it.

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// twoClosesModel is a session whose two turns each wrote a file, so the
// transcript holds two closes offering the same keys.
func twoClosesModel(t *testing.T) Model {
	t.Helper()
	m := turnModel(t)
	for i, name := range []string{"one.go", "two.go"} {
		m = sendText(t, m, fmt.Sprintf("write file %d", i+1))
		m = applyWrite(t, m, filepath.Join(t.TempDir(), name), "package main\n", "y")
		m = finishTurn(t, m)
	}
	return m
}

// closeIndexes is where the transcript's closes stand, oldest first.
func closeIndexes(m Model) []int {
	var at []int
	for i, e := range m.transcript {
		if e.kind == entryTurnClose {
			at = append(at, i)
		}
	}
	return at
}

// With nothing selected the newest close draws its keep and take back as the
// last turn's and the chord acts on it, never on an older one; selecting an
// older close quiets the newest, so one close offers at a time; and the
// pointer does not move for any of it.
func TestRowChords_WithNothingSelectedOnlyTheNewestCloseAnswers(t *testing.T) {
	m := twoClosesModel(t)
	at := closeIndexes(m)
	if len(at) != 2 {
		t.Fatalf("want two closes, have %d", len(at))
	}
	older, newer := m.transcript[at[0]], m.transcript[at[1]]
	if idx, c := m.latestClose(); c != newer.close || idx != at[1] {
		t.Fatalf("the target is %p at %d, want the newest close at %d", c, idx, at[1])
	}
	if drawn := ansi.Strip(m.renderEntry(newer, 110)); !strings.Contains(drawn, latestUndoWords) {
		t.Fatalf("the newest close should offer its undo as the last turn's:\n%s", drawn)
	}
	if drawn := ansi.Strip(m.renderEntry(older, 110)); strings.Contains(drawn, "undo") || strings.Contains(drawn, "review") {
		t.Fatalf("an older close offers nothing until it is selected:\n%s", drawn)
	}

	focus, pointer := m.focusIdx, m.pointer
	next := pressChord(t, m, keys.RowChord.Undo)
	if next.state != stateUndoConfirm || next.undoAsk == nil || next.undoAsk.Prompt != "Undo turn 2?" {
		t.Fatalf("the chord should ask to undo the last turn: state %v, ask %+v", next.state, next.undoAsk)
	}
	if next.focusIdx != focus || next.pointer != pointer {
		t.Fatalf("the chord moved the pointer: focus %d → %d, lit %v → %v", focus, next.focusIdx, pointer, next.pointer)
	}
	if got := next.input.Value(); got != "" {
		t.Fatalf("a chord cannot reach the draft: %q", got)
	}

	// The pointer on the older close: it offers, and the newest goes quiet.
	pointed := m
	pointed.pointer, pointed.focusIdx = true, at[0]
	if _, c := pointed.latestClose(); c != nil {
		t.Fatal("with a row selected the newest close is no longer the chord's target")
	}
	if drawn := ansi.Strip(pointed.renderEntryKeys(newer, 110, rowUnselected)); strings.Contains(drawn, "undo") {
		t.Fatalf("the newest close offers nothing while another row is selected:\n%s", drawn)
	}
	undone := pressChord(t, pointed, keys.RowChord.Undo)
	if undone.undoAsk == nil || undone.undoAsk.Prompt != "Undo turn 1?" {
		t.Fatalf("the chord should act on the selected close: ask %+v", undone.undoAsk)
	}
}

// The tie between the two rows that offer with nothing selected: whichever
// is newer in the transcript draws its offers, and the other is inert.
func TestRowChords_TheNewerOfTheCloseAndTheFailureOffers(t *testing.T) {
	failure := func(turn int64) entry {
		return entry{kind: entryFailure, turn: turn,
			fail: &provider.Failure{Class: provider.ClassUnclassified, Status: 400, Message: "no"}}
	}
	m := twoClosesModel(t)
	m.turnOutcome = components.TurnFailed
	m.appendEntry(failure(m.turnCount))
	m.invalidateRenderCache()
	if _, c := m.latestClose(); c != nil {
		t.Fatal("a newer failure row should quiet the close")
	}
	if _, target := m.latestRecovery(); target == (recoveryTarget{}) {
		t.Fatal("the newer failure should keep its labelled retry")
	}

	m = twoClosesModel(t)
	m.turnOutcome = components.TurnFailed
	closeAt := closeIndexes(m)[1]
	m.transcript = append(m.transcript[:closeAt:closeAt],
		append([]entry{failure(m.turnCount)}, m.transcript[closeAt:]...)...)
	m.invalidateRenderCache()
	if _, target := m.latestRecovery(); target != (recoveryTarget{}) {
		t.Fatal("a newer close should quiet the failure")
	}
	if _, c := m.latestClose(); c == nil {
		t.Fatal("the newer close should keep its offers")
	}
}

// The pointer moves on the reader's key or click and on nothing else, so a
// turn closing leaves it where it was, lit or not.
func TestPointer_ACloseDoesNotMoveIt(t *testing.T) {
	m := turnModel(t)
	m = sendText(t, m, "write file 1")
	m = applyWrite(t, m, filepath.Join(t.TempDir(), "one.go"), "package main\n", "y")
	m = finishTurn(t, m)
	first := closeIndexes(m)[0]

	// Unlit: the close lands and the pointer stays dark where it was.
	unlit := sendText(t, m, "write file 2")
	unlit = applyWrite(t, unlit, filepath.Join(t.TempDir(), "two.go"), "package main\n", "y")
	focus := unlit.focusIdx
	unlit = finishTurn(t, unlit)
	if unlit.pointer || unlit.focusIdx != focus {
		t.Fatalf("a close moved the pointer: lit %v, focus %d → %d", unlit.pointer, focus, unlit.focusIdx)
	}

	// Lit on the older close mid-turn: the new close lands under it and the
	// pointer stays on the row the reader put it on.
	lit := sendText(t, m, "write file 2")
	lit = applyWrite(t, lit, filepath.Join(t.TempDir(), "two.go"), "package main\n", "y")
	lit.pointer, lit.focusIdx = true, first
	lit = finishTurn(t, lit)
	if !lit.pointer || lit.focusIdx != first {
		t.Fatalf("a close moved the lit pointer: lit %v, focus %d, want %d", lit.pointer, lit.focusIdx, first)
	}
}
