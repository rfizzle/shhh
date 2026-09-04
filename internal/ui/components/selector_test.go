package components

import (
	"strings"
	"testing"
)

func planOptions() []SelectOption {
	return []SelectOption{
		{Label: "Execute plan (accept edits)"},
		{Label: "Execute plan (manual approvals)"},
		{Label: "Keep planning", Desc: "tell me what to change"},
		{Label: "Reject plan"},
	}
}

func TestSelect_MoveAndConfirm(t *testing.T) {
	s := &Select{Title: "Plan ready — how should I proceed?", Options: planOptions()}
	s.Update(key("j"))
	s.Update(key("down"))
	if s.Focus != 2 {
		t.Fatalf("j/down should move focus, got %d", s.Focus)
	}
	s.Update(key("k"))
	if s.Focus != 1 {
		t.Fatalf("k should move focus up, got %d", s.Focus)
	}
	done, result := s.Update(key("enter"))
	if !done || result.Index != 1 {
		t.Fatalf("enter should select the focused option, got %v %v", done, result)
	}
}

func TestSelect_DigitJumpAndCancel(t *testing.T) {
	s := &Select{Options: planOptions()}
	done, result := s.Update(key("3"))
	if !done || result.Index != 2 {
		t.Fatalf("number keys should select immediately, got %v %v", done, result)
	}
	s = &Select{Options: planOptions()}
	if done, _ := s.Update(key("9")); done {
		t.Fatal("out-of-range digits should do nothing")
	}
	done, result = s.Update(key("esc"))
	if !done || !result.Canceled {
		t.Fatalf("esc should cancel, got %v %v", done, result)
	}
}

func TestSelect_ViewShowsPointerAndFocusedDesc(t *testing.T) {
	s := &Select{Title: "Pick", Options: planOptions(), Focus: 2}
	view := s.View(80)
	if !strings.Contains(view, "❯ 3. Keep planning") {
		t.Fatalf("focused row should carry the pointer:\n%s", view)
	}
	if !strings.Contains(view, "tell me what to change") {
		t.Fatalf("focused option's description should show:\n%s", view)
	}
	if !strings.Contains(view, "esc cancel") {
		t.Fatalf("hints should render:\n%s", view)
	}
}

func TestMultiSelect_ToggleAllAndApply(t *testing.T) {
	s := NewMultiSelect("Apply which changes?", planOptions())
	s.Update(key("space"))
	s.Update(key("j"))
	s.Update(key("space"))
	done, result := s.Update(key("enter"))
	if !done {
		t.Fatal("enter with selections should apply")
	}
	got := result.Indices
	if len(got) != 2 || got[0] != 0 || got[1] != 1 {
		t.Fatalf("expected indices [0 1], got %v", got)
	}
}

func TestMultiSelect_AllNoneAndZeroSelection(t *testing.T) {
	s := NewMultiSelect("Apply?", planOptions())
	s.Update(key("a"))
	if s.count() != len(s.Options) {
		t.Fatalf("a should check everything, got %d", s.count())
	}
	s.Update(key("a"))
	if s.count() != 0 {
		t.Fatalf("a again should clear everything, got %d", s.count())
	}
	// Zero selected + enter is a no-op with a notice, not a confirm.
	done, _ := s.Update(key("enter"))
	if done {
		t.Fatal("enter with nothing selected must not confirm")
	}
	if !strings.Contains(s.View(80), "nothing selected") {
		t.Fatal("zero-selection enter should show a notice")
	}
	done, result := s.Update(key("esc"))
	if !done || !result.Canceled {
		t.Fatal("esc should cancel the multi-select")
	}
}

func TestMultiSelect_ViewShowsChecksAndCount(t *testing.T) {
	s := NewMultiSelect("Apply?", planOptions())
	s.Update(key("space"))
	view := s.View(80)
	if !strings.Contains(view, "[x]") || !strings.Contains(view, "[ ]") {
		t.Fatalf("view should render checked and unchecked boxes:\n%s", view)
	}
	if !strings.Contains(view, "enter apply (1)") {
		t.Fatalf("confirm hint should show the live count:\n%s", view)
	}
}

func TestNoteSelect_ConfirmWithNote(t *testing.T) {
	s := NewNoteSelect("Remember this?", []SelectOption{
		{Label: "Save (project)"},
		{Label: "Don't save"},
	})
	s.Update(key("tab"))
	if !s.FocusNote {
		t.Fatal("tab should move focus to the note")
	}
	for _, r := range "only for Go" {
		s.Update(key(string(r)))
	}
	done, result := s.Update(key("enter"))
	res := result
	if !done || res.Index != 0 || res.Note != "only for Go" {
		t.Fatalf("enter should confirm selection and note together, got %+v", res)
	}
}

func TestNoteSelect_RequireNoteBlocksEmpty(t *testing.T) {
	s := NewNoteSelect("Feedback", []SelectOption{
		{Label: "Keep planning", RequireNote: true},
	})
	done, _ := s.Update(key("enter"))
	if done {
		t.Fatal("a note-required option must not confirm with an empty note")
	}
	if !strings.Contains(s.View(80), "note required") {
		t.Fatal("the note hint should turn into a note-required warning")
	}
	done, result := s.Update(key("esc"))
	if !done || !result.Canceled {
		t.Fatal("esc should still cancel")
	}
}

func TestNoteSelect_ListNavigationWhileNoteBlurred(t *testing.T) {
	s := NewNoteSelect("Pick", planOptions())
	s.Update(key("j"))
	if s.Select.Focus != 1 {
		t.Fatalf("j should move the option focus, got %d", s.Select.Focus)
	}
	s.Update(key("3"))
	if s.Select.Focus != 2 {
		t.Fatalf("digits should jump focus without confirming, got %d", s.Select.Focus)
	}
}

func TestConfirm_Keys(t *testing.T) {
	c := &Confirm{Prompt: "Discard 14 unsaved turns?"}
	if done, result := c.Update(key("y")); !done || result != true {
		t.Fatal("y should confirm")
	}
	for _, k := range []string{"n", "enter", "esc"} {
		if done, result := c.Update(key(k)); !done || result != false {
			t.Fatalf("%s should decline (default No)", k)
		}
	}
	if done, _ := c.Update(key("z")); done {
		t.Fatal("other keys should wait")
	}
	if view := c.View(80); !strings.Contains(view, "Discard 14 unsaved turns?") || !strings.Contains(view, "[y/N]") {
		t.Fatalf("confirm should render prompt and [y/N]: %q", view)
	}
}

// grouped is a filtered list of the shape the palette builds: rails
// that label the runs beneath them, and one option that cannot be acted on.
func grouped() []SelectOption {
	return []SelectOption{
		{Label: "COMMANDS", Header: true},
		{Label: "/model"},
		{Label: "/clear", Desc: "needs the turn to be finished", Dim: true},
		{Label: "FILES", Header: true},
		{Label: "internal/agent/loop.go"},
	}
}

func TestSelect_HeadersAreSteppedOver(t *testing.T) {
	s := &Select{Options: grouped(), Focus: 0, Unnumbered: true}
	s.Update(key("down"))
	if s.Focus != 2 {
		t.Fatalf("focus should open on the first option and move to the next, got %d", s.Focus)
	}
	s.Update(key("down"))
	if s.Focus != 4 {
		t.Fatalf("moving down should step over the rail, got %d", s.Focus)
	}
	s.Update(key("down"))
	if s.Focus != 4 {
		t.Fatalf("the last option should hold the focus, got %d", s.Focus)
	}
	s.Update(key("up"))
	s.Update(key("up"))
	if s.Focus != 1 {
		t.Fatalf("moving up should step over the rail too, got %d", s.Focus)
	}
	s.Update(key("up"))
	if s.Focus != 1 {
		t.Fatal("the pointer must never land on a rail")
	}
}

func TestSelect_UnnumberedIgnoresDigits(t *testing.T) {
	s := &Select{Options: grouped(), Unnumbered: true}
	done, _ := s.Update(key("2"))
	if done {
		t.Fatal("a digit is text on an unnumbered list, not a jump")
	}
	view := s.View(70)
	if strings.Contains(view, "1. ") {
		t.Fatalf("an unnumbered list should not number its rows:\n%s", view)
	}
	if strings.Contains(view, "1–") {
		t.Fatalf("nor offer the jump keys:\n%s", view)
	}
}

func TestSelect_DigitJumpCountsOnlySelectableRows(t *testing.T) {
	s := &Select{Options: grouped()}
	done, result := s.Update(key("3"))
	if !done {
		t.Fatal("a numbered list should still jump")
	}
	if got := result.Index; got != 4 {
		t.Fatalf("the third selectable row is index 4, got %d", got)
	}
}

func TestSelect_QueryChipsAndHint(t *testing.T) {
	s := &Select{
		Title: "Palette", Options: grouped(), Unnumbered: true,
		Filtering: true, Query: "mod", Chips: []string{"12 results"},
		Hint: "enter run · tab complete · ↑↓ move · esc dismiss",
	}
	view := s.View(70)
	for _, want := range []string{"Palette", "12 results", "▸ mod█", "COMMANDS", "tab complete"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected %q in the card:\n%s", want, view)
		}
	}
}

func TestSelect_DimOptionCarriesItsGlyph(t *testing.T) {
	s := &Select{Options: grouped(), Unnumbered: true, Focus: 1}
	if view := s.View(70); !strings.Contains(view, "⊘ /clear") {
		t.Fatalf("an unavailable option says so in a glyph, not only in a colour:\n%s", view)
	}
}

// A row that carries its own answers is a field: the key that toggles a
// checkbox elsewhere steps it, wrapping, and the card is never left.
func TestSelect_FieldRowsCycleInPlace(t *testing.T) {
	s := &Select{Unnumbered: true, Options: []SelectOption{
		{Label: "kind", Value: "story", Values: []string{"story", "bug", "chore"}},
		{Label: "size", Value: "M", Values: []string{"S", "M", "L"}},
		{Label: "waits on", Value: "nothing"},
	}}
	if done, _ := s.Update(key(" ")); done {
		t.Fatal("changing a field should not finish the card")
	}
	if s.Options[0].Value != "bug" {
		t.Fatalf("kind = %q", s.Options[0].Value)
	}
	s.Update(key(" "))
	s.Update(key(" "))
	if s.Options[0].Value != "story" {
		t.Fatalf("the scale should wrap, got %q", s.Options[0].Value)
	}
	// A row with no answers of its own is not one this key lands on.
	s.Focus = 2
	s.Update(key(" "))
	if s.Options[2].Value != "nothing" {
		t.Fatalf("a plain row changed: %q", s.Options[2].Value)
	}
	// And the key is offered, so it is discoverable.
	if view := s.View(60); !strings.Contains(view, "change it") {
		t.Fatalf("the key row should offer the field key:\n%s", view)
	}
}

// A value the card was handed that is not on the row's scale — a header field
// off its scale, as a file may well carry — steps back onto it.
func TestSelect_AnOffScaleValueCyclesOntoTheScale(t *testing.T) {
	s := &Select{Options: []SelectOption{{Label: "priority", Value: "urgent",
		Values: []string{"high", "medium", "low"}}}}
	s.Update(key(" "))
	if s.Options[0].Value != "high" {
		t.Fatalf("value = %q", s.Options[0].Value)
	}
}

// The reading under the options folds before the options do, and says how
// much it folded.
func TestSelect_ProseFoldsUnderTheOptions(t *testing.T) {
	s := &Select{Options: []SelectOption{{Label: "kind", Value: "story", Values: []string{"story"}}},
		Body: []string{"one", "two", "three", "four", "five", "six"}}
	whole := s.View(60)
	if !strings.Contains(whole, "six") {
		t.Fatalf("an unbounded card should draw the whole reading:\n%s", whole)
	}
	s.MaxLines = 7
	folded := s.View(60)
	switch {
	case !strings.Contains(folded, "kind"):
		t.Fatalf("the options are what a key lands on and must stay:\n%s", folded)
	case !strings.Contains(folded, "one"):
		t.Fatalf("the reading should not fold to nothing:\n%s", folded)
	case strings.Contains(folded, "six"):
		t.Fatalf("the reading should have folded:\n%s", folded)
	case !strings.Contains(folded, "↓"):
		t.Fatalf("a fold should say how much it hid:\n%s", folded)
	}
}

// The warning is pinned above the key row, so it cannot scroll away.
func TestSelect_WarningIsPinned(t *testing.T) {
	s := &Select{MaxLines: 8, Options: planOptions(),
		Warning: "nothing in the backlog is named cache-ttl"}
	view := s.View(60)
	if !strings.Contains(view, "named cache-ttl") {
		t.Fatalf("the warning should be on the card:\n%s", view)
	}
}

// Checking nothing is an answer on a card that is setting a list, and the
// slip it always was on one that is choosing what to do with one.
func TestMultiSelect_AllowNoneTakesAnEmptyAnswer(t *testing.T) {
	s := NewMultiSelect("dependencies", planOptions())
	done, res := s.Update(key("enter"))
	if done || res.Indices != nil {
		t.Fatal("a card choosing what to do with a list refuses an empty answer")
	}
	s.AllowNone = true
	done, res = s.Update(key("enter"))
	if !done || len(res.Indices) != 0 || res.Canceled {
		t.Fatalf("an empty answer should be taken: %v %+v", done, res)
	}
}

// The host's own key on the focused row is offered on the key row, worded by
// the host, and never dropped.
func TestMultiSelect_HostActionsAreOffered(t *testing.T) {
	s := NewMultiSelect("proposals", planOptions())
	s.Actions = []string{"e its header"}
	view := s.View(70)
	if !strings.Contains(view, "e its header") {
		t.Fatalf("the action should be on the key row:\n%s", view)
	}
	if !strings.Contains(view, "apply (0)") {
		t.Fatalf("the count the card always carried should still be there:\n%s", view)
	}
}
