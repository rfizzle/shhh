package chat

// The pointer a close's offers are taken from
// (docs/interface/surfaces.md#the-turns-close): a close landing does not move
// it.

import (
	"path/filepath"
	"testing"
)

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
