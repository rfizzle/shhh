package components

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// A card below the floor gives up its right column before its words: the
// description is whole, the meta field sits under it, and no row clips.
func TestSelect_StackedRowKeepsItsWordsBelowTheFloor(t *testing.T) {
	opts := []SelectOption{
		{Label: "SQLite", Desc: "in the checkout already", Meta: "3 files", Recommended: true},
		{Label: "Postgres", Desc: "one more service to run", Meta: "9 files"},
	}
	s := &Select{Title: "Question", Options: opts, Stack: true}
	narrow := ansi.Strip(s.View(CardFloor - 6))
	for _, want := range []string{"in the checkout already", "recommended · 3 files", "one more service to run", "9 files"} {
		if !strings.Contains(narrow, want) {
			t.Errorf("below the floor the card lost %q:\n%s", want, narrow)
		}
	}
	if strings.Contains(narrow, "…") {
		t.Errorf("below the floor nothing clips:\n%s", narrow)
	}

	wide := ansi.Strip(s.View(CardFloor + 16))
	for _, l := range strings.Split(wide, "\n") {
		if strings.Contains(l, "SQLite") && !strings.Contains(l, "recommended · 3 files") {
			t.Errorf("above the floor the meta stays on the row:\n%s", wide)
		}
	}

	s.Stack = false
	if plain := ansi.Strip(s.View(CardFloor - 6)); strings.Count(plain, "\n") >= strings.Count(narrow, "\n") {
		t.Errorf("a card that did not ask to stack keeps its one row per option:\n%s", plain)
	}
}

// The close's command never breaks inside itself: the clause moves whole to
// the next line.
func TestCloseRunOn_KeepsTheUndoCommandWhole(t *testing.T) {
	for _, width := range []int{40, 50, 56, 60, 70} {
		ch := &TurnChanges{Files: 1, Added: 12, Removed: 4, Note: "all tracked in git", Back: "/undo 1 takes it back"}
		out := ansi.Strip(TurnClose{Changes: ch}.View(width))
		joined := strings.Join(strings.Fields(out), " ")
		if !strings.Contains(joined, "/undo 1 takes it back") {
			t.Fatalf("width %d lost the command:\n%s", width, out)
		}
		for _, l := range strings.Split(out, "\n") {
			if strings.Contains(l, "/undo") && !strings.Contains(l, "/undo 1 takes it back") {
				t.Errorf("width %d breaks inside the command:\n%s", width, out)
			}
		}
	}
}
