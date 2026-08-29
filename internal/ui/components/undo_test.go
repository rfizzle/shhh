package components

// The undo confirm's answers: the default is the safe one, esc
// declines, and force is only offered when there is drift to force through.

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func runes(s string) tea.KeyPressMsg { return tea.KeyPressMsg{Code: []rune(s)[0], Text: s} }

func answer(t *testing.T, c *UndoConfirm, msg tea.KeyPressMsg) (bool, UndoDecision) {
	t.Helper()
	done, result := c.Update(msg)
	d, _ := result.(UndoDecision)
	return done, d
}

func TestUndoConfirm_DefaultIsDecline(t *testing.T) {
	c := &UndoConfirm{Turn: 7, Restores: 2}
	for _, msg := range []tea.KeyPressMsg{
		runes("n"), {Code: tea.KeyEnter}, {Code: tea.KeyEscape},
	} {
		done, d := answer(t, c, msg)
		if !done || d != UndoCancel {
			t.Fatalf("%v should decline, got done=%v %v", msg, done, d)
		}
	}
	if done, d := answer(t, c, runes("y")); !done || d != UndoApply {
		t.Fatalf("y should undo, got done=%v %v", done, d)
	}
}

// Force exists only where there is drift to force through; without any, [f]
// is not an answer and the key is left alone.
func TestUndoConfirm_ForceOnlyWithDrift(t *testing.T) {
	clean := &UndoConfirm{Turn: 7, Restores: 1}
	if done, _ := answer(t, clean, runes("f")); done {
		t.Fatal("f should not resolve a confirm with nothing drifted")
	}
	if view := ansi.Strip(clean.View(72)); strings.Contains(view, "[f]") {
		t.Fatalf("force should not be offered without drift, got %q", view)
	}

	drifted := &UndoConfirm{Turn: 7, Restores: 1, Drifted: []string{"a.go"}}
	if done, d := answer(t, drifted, runes("f")); !done || d != UndoForce {
		t.Fatalf("f should force through drift, got done=%v %v", done, d)
	}
}

// With every file drifted there is nothing for [y] to do, so it is neither
// offered nor bound — the confirm never shows a key that does nothing.
func TestUndoConfirm_YesIsWithheldWhenItWouldDoNothing(t *testing.T) {
	c := &UndoConfirm{Turn: 7, Drifted: []string{"a.go"}}
	if done, _ := answer(t, c, runes("y")); done {
		t.Fatal("y should not resolve a confirm with nothing to restore")
	}
	view := ansi.Strip(c.View(72))
	if strings.Contains(view, "[y/N]") || !strings.Contains(view, "[f/N]") {
		t.Fatalf("the offered keys should match what can be done, got %q", view)
	}
	if !strings.Contains(view, "Nothing is left to restore") {
		t.Fatalf("the confirm should say why, got %q", view)
	}
}

// The drift list is bounded: past a few names the rest are counted, because
// the prompt has to fit in the input area.
func TestUndoConfirm_DriftListIsBounded(t *testing.T) {
	c := UndoConfirm{Turn: 7, Restores: 1,
		Drifted: []string{"a.go", "b.go", "c.go", "d.go", "e.go"}}
	view := ansi.Strip(c.View(80))
	if !strings.Contains(view, "5 files changed since the turn") {
		t.Fatalf("the count should be complete, got %q", view)
	}
	if strings.Contains(view, "e.go") || !strings.Contains(view, "and 2 more") {
		t.Fatalf("the names should be bounded and the rest counted, got %q", view)
	}
}

func TestUndoConfirm_StatesBothKindsOfEffect(t *testing.T) {
	view := ansi.Strip(UndoConfirm{Turn: 3, Restores: 2, Removes: 1}.View(96))
	if !strings.Contains(view, "restores 2 files") || !strings.Contains(view, "deletes 1 file it created") {
		t.Fatalf("the confirm should state what it would do to each kind, got %q", view)
	}
	// A kind with no files in it is left out rather than reported as a zero.
	only := ansi.Strip(UndoConfirm{Turn: 3, Restores: 2}.View(96))
	if strings.Contains(only, "deletes") {
		t.Fatalf("nothing should be said about a kind with no files, got %q", only)
	}
}
