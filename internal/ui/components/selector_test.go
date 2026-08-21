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
	if !done || result.(SelectResult).Index != 1 {
		t.Fatalf("enter should select the focused option, got %v %v", done, result)
	}
}

func TestSelect_DigitJumpAndCancel(t *testing.T) {
	s := &Select{Options: planOptions()}
	done, result := s.Update(key("3"))
	if !done || result.(SelectResult).Index != 2 {
		t.Fatalf("number keys should select immediately, got %v %v", done, result)
	}
	s = &Select{Options: planOptions()}
	if done, _ := s.Update(key("9")); done {
		t.Fatal("out-of-range digits should do nothing")
	}
	done, result = s.Update(key("esc"))
	if !done || !result.(SelectResult).Canceled {
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
	got := result.(MultiSelectResult).Indices
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
	if !done || !result.(MultiSelectResult).Canceled {
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
	res := result.(NoteSelectResult)
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
	if !done || !result.(NoteSelectResult).Canceled {
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
