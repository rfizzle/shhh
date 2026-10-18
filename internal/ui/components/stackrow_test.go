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

// A detail below the floor hangs under its label whole, with the meta field
// closing it, instead of clipping a word beside the label.
func TestSelect_StackedDetailKeepsItsWordsBelowTheFloor(t *testing.T) {
	opts := []SelectOption{{
		Label:  "cap rounds instead of erroring",
		Detail: []DetailSpan{{Text: "▎2 files +18 −12", Tone: ToneQuiet}, {Text: " and the tests", Tone: ToneQuiet}},
		Meta:   "3m",
	}}
	s := &Select{Title: "Rewind", Options: opts, Stack: true, Unnumbered: true}
	narrow := ansi.Strip(s.View(CardFloor - 6))
	if !strings.Contains(narrow, "▎2 files +18 −12 and the tests · 3m") || strings.Contains(narrow, "…") {
		t.Errorf("below the floor the detail and the age stay whole:\n%s", narrow)
	}
}

// A checkbox row whose short field would clip its label moves the field under
// it below the floor.
func TestMultiSelect_StackedFieldKeepsItsWordsBelowTheFloor(t *testing.T) {
	s := NewMultiSelect("Question", []SelectOption{
		{Label: "internal/ui/components/selector", Meta: "touched by the last three rounds"},
		{Label: "internal/cli"},
	})
	s.Stack = true
	narrow := ansi.Strip(s.View(CardFloor - 12))
	if !strings.Contains(narrow, "internal/ui/components/selector") || !strings.Contains(narrow, "touched by the last") || strings.Contains(narrow, "…") {
		t.Errorf("below the floor the label and its field stay whole:\n%s", narrow)
	}
	s.Stack = false
	if plain := ansi.Strip(s.View(CardFloor - 12)); strings.Count(plain, "\n") >= strings.Count(narrow, "\n") {
		t.Errorf("a card that did not ask to stack keeps one row per option:\n%s", plain)
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

// A stacked checkbox row counts as the lines it draws, so a long list below
// the floor stays inside its card's budget and the marker still says how many
// options are under the window.
func TestMultiSelect_StackedRowsCountTheirDrawnLines(t *testing.T) {
	var opts []SelectOption
	for range 12 {
		opts = append(opts, SelectOption{Label: "internal/ui/components/selector", Meta: "touched by the last three rounds"})
	}
	s := NewMultiSelect("Question", opts)
	s.Stack, s.MaxLines = true, 14
	view := ansi.Strip(s.View(CardFloor - 12))
	if got := strings.Count(view, "\n") + 1; got > s.MaxLines {
		t.Errorf("the card drew %d lines against a budget of %d:\n%s", got, s.MaxLines, view)
	}
	if !strings.Contains(view, "below") && !strings.Contains(view, "↓") {
		t.Errorf("the card does not say how many are below:\n%s", view)
	}
}

// A stacked proposal row counts as the lines it draws, so a long plan at the
// narrow width keeps its body inside the budget and says how many are below.
func TestSprintPlan_StackedRowsCountTheirDrawnLines(t *testing.T) {
	p := &SprintPlan{}
	for range 12 {
		p.Rows = append(p.Rows, SprintPlanRow{Slug: "a-slug", Title: "A title", Note: "a reason long enough that it wraps under the title at sixty columns wide"})
	}
	const width, budget = 60, 16
	rows := p.view(width, budget)
	if len(rows) > budget {
		t.Errorf("the plan drew %d lines against a budget of %d:\n%s", len(rows), budget, ansi.Strip(strings.Join(rows, "\n")))
	}
	if out := ansi.Strip(strings.Join(rows, "\n")); !strings.Contains(out, " below") {
		t.Errorf("the plan does not say how many are below:\n%s", out)
	}
}
