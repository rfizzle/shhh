package components

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func profileStarts() []string {
	return []string{
		"a test writer who adds table-driven tests for a package and runs them",
		"a reviewer who reads a diff for security problems and reports by severity",
	}
}

func briefScreen() *ProfileScreen {
	p := NewProfileScreen("/agents new")
	p.Subject = "a coding agent · reviewer tester"
	p.AskBrief("What should this agent do?", "or start from one of these", profileStarts())
	return p
}

func typeRunes(p *ProfileScreen, text string) {
	for _, r := range text {
		p.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

// The field has the pointer from the first keystroke, which is what a person
// who already has the sentence needs; the starting points are under it for
// the person who does not.
func TestProfileScreen_BriefTakesTheFieldOrAStartingPoint(t *testing.T) {
	p := briefScreen()
	typeRunes(p, "something for tests")
	done, result := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !done || result.Text != "something for tests" {
		t.Fatalf("enter should take what was typed: %+v", result)
	}

	p = briefScreen()
	typeRunes(p, "ignored")
	p.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	p.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	done, result = p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !done || result.Text != profileStarts()[1] {
		t.Fatalf("enter on a starting point should take it: %+v", result)
	}
	// And up from the top row hands the keyboard back to the field.
	p.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	p.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	p.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	done, result = p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !done || result.Text != "ignored" {
		t.Fatalf("up should return to the field: %+v", result)
	}
}

// An empty brief is the one answer the flow cannot supply for itself, so
// enter over it does nothing rather than drafting from an empty sentence.
func TestProfileScreen_EmptyBriefTakesNothing(t *testing.T) {
	p := briefScreen()
	if done, _ := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); done {
		t.Fatal("enter over an empty brief should do nothing")
	}
}

// An empty answer is an answer: someone with no preference should not be
// held at the question until they invent one.
func TestProfileScreen_EmptyAnswerIsAnAnswer(t *testing.T) {
	p := NewProfileScreen("/agents new")
	p.AskQuestion("Which package?", 1, 2)
	done, result := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !done || result.Action != ProfileTake || result.Text != "" {
		t.Fatalf("enter should take the empty answer: %+v", result)
	}
}

// The rail says where in the flow you are, and the wait keeps saying the step
// it was started from: a drafting turn started from the brief may still come
// back with questions.
func TestProfileScreen_RailAndTheWait(t *testing.T) {
	p := briefScreen()
	if view := p.View(90); !strings.Contains(view, "● brief") || !strings.Contains(view, "· draft") {
		t.Fatalf("the brief step should be marked on the rail:\n%s", view)
	}
	p.Work("drafting")
	view := p.View(90)
	if !strings.Contains(view, "● brief") {
		t.Fatalf("the wait should keep the step it was started from:\n%s", view)
	}
	if !strings.Contains(view, "drafting") || !strings.Contains(view, "stop drafting") {
		t.Fatalf("the wait should say what it waits for and how to stop:\n%s", view)
	}
	if done, result := p.Update(tea.KeyPressMsg{Code: tea.KeyEscape}); !done || result.Action != ProfileAbort {
		t.Fatalf("esc should stop the drafting: %+v", result)
	}
	p.AskQuestion("Which package?", 1, 2)
	p.Work("drafting")
	if view := p.View(90); !strings.Contains(view, "● questions") {
		t.Fatalf("a wait from the questions should say so:\n%s", view)
	}
}

// A brief that was already a specification gets a draft and no questions, and
// the rail says which happened rather than ticking a step nobody was asked.
func TestProfileScreen_TheRailSaysWhenQuestionsWereSkipped(t *testing.T) {
	skipped := draftScreen()
	if view := skipped.View(90); !strings.Contains(view, "⊘ questions") {
		t.Fatalf("a draft with no questions should mark the step skipped:\n%s", view)
	}
	asked := draftScreen()
	asked.Of = 2
	if view := asked.View(90); !strings.Contains(view, "✓ questions") {
		t.Fatalf("a draft that was asked questions should tick the step:\n%s", view)
	}
}

func draftScreen() *ProfileScreen {
	p := NewProfileScreen("/agents new")
	view := sectionedDraft()
	// A long Method, so a short pane has something to fold.
	view.Sections[3].Body = strings.Repeat("A line of the method that is long enough to wrap on a narrow pane.\n", 12)
	p.Show(view, []SelectOption{
		{Label: "Save to this project", Desc: ".shhh/agents"},
		{Label: "Save globally", Desc: "~/.config/shhh/agents"},
	})
	return p
}

// The card's rows map onto the actions without the host having to know where
// the save rows end, once tab has handed it the keyboard.
func TestProfileScreen_DecisionRows(t *testing.T) {
	for _, tc := range []struct {
		downs  int
		action ProfileAction
		index  int
	}{
		{0, ProfileSave, 0},
		{1, ProfileSave, 1},
		{2, ProfileDiscard, 0},
	} {
		p := draftScreen()
		p.Update(key("tab"))
		for range tc.downs {
			p.Update(key("down"))
		}
		done, res := p.Update(key("enter"))
		if !done || res.Action != tc.action || res.Index != tc.index {
			t.Fatalf("%d downs: %+v", tc.downs, res)
		}
	}
	// The card has no Refine row and no note: revision is per section.
	p := draftScreen()
	if view := ansi.Strip(p.View(100)); strings.Contains(view, "Refine") || strings.Contains(view, "note or list") {
		t.Fatalf("the card should be the ways out and nothing else:\n%s", view)
	}
}

// The sections hold the keyboard when the draft arrives, and what enter, e, x
// and esc do is the selected section's.
func TestProfileScreen_TheSelectedSectionIsRevised(t *testing.T) {
	p := draftScreen()
	// Purpose has a revision, so esc takes it back rather than leaving.
	if done, res := p.Update(key("esc")); !done || res.Action != ProfileUndo || res.Index != 0 {
		t.Fatalf("esc on a revised section = %+v", res)
	}
	// Scope has none, so esc is the step's own: the draft is discarded.
	p.Update(key("down"))
	if done, res := p.Update(key("esc")); !done || res.Action != ProfileDiscard {
		t.Fatalf("esc on an unrevised section = %+v", res)
	}
	if done, res := p.Update(key("e")); !done || res.Action != ProfileEdit || res.Index != 1 {
		t.Fatalf("e = %+v", res)
	}
	if done, res := p.Update(key("x")); !done || res.Action != ProfileClear || res.Index != 1 {
		t.Fatalf("x = %+v", res)
	}
	// enter opens a note under the section; an empty one sends nothing, esc
	// closes it, and a note is sent with the section it is about.
	p.Update(key("enter"))
	if view := ansi.Strip(p.View(100)); !strings.Contains(view, "what to change in Scope — the other seven are sent as fixed context") {
		t.Fatalf("the note should open under the section:\n%s", view)
	}
	if done, _ := p.Update(key("enter")); done {
		t.Fatal("an empty note should send nothing")
	}
	p.Update(key("esc"))
	if done, res := p.Update(key("enter")); done {
		t.Fatalf("enter should open the note again, not act: %+v", res)
	}
	typeRunes(p, "name the goldens")
	if done, res := p.Update(key("enter")); !done || res.Action != ProfileRefine || res.Index != 1 || res.Text != "name the goldens" {
		t.Fatalf("refine = %+v", res)
	}
	// The empty Report offers nothing to clear.
	p = draftScreen()
	for range 4 {
		p.Update(key("down"))
	}
	if done, res := p.Update(key("x")); done {
		t.Fatalf("x on an empty section should do nothing: %+v", res)
	}
	// A field block is not prose: enter is handed to the host as a pick, and
	// e and x do nothing.
	p.Update(key("down"))
	if done, res := p.Update(key("e")); done {
		t.Fatalf("e on a field block should do nothing: %+v", res)
	}
	if done, res := p.Update(key("enter")); !done || res.Action != ProfilePick || res.Index != 5 {
		t.Fatalf("enter on the tools block = %+v", res)
	}
	// The selector the host opens holds the keyboard where the card is:
	// space ticks inside it, enter takes it and esc leaves it.
	ms := NewMultiSelect("What may test-writer do?", []SelectOption{{Label: "read"}, {Label: "web"}})
	p.OpenPicker(ms)
	if done, _ := p.Update(key("space")); done || !ms.Checked[0] {
		t.Fatalf("space should tick inside the selector: %v", ms.Checked)
	}
	if !strings.Contains(p.View(130), "What may test-writer do?") {
		t.Fatalf("the selector should be drawn:\n%s", p.View(130))
	}
	if done, res := p.Update(key("enter")); !done || res.Action != ProfilePicked || res.Index != 5 {
		t.Fatalf("enter on the selector = %+v", res)
	}
	if done, res := p.Update(key("esc")); !done || res.Action != ProfileUnpicked {
		t.Fatalf("esc on the selector = %+v", res)
	}
	// A field block with no selector offers no enter.
	p.Picker = nil
	p.Update(key("down"))
	if done, res := p.Update(key("enter")); done {
		t.Fatalf("enter on the Commands block should do nothing: %+v", res)
	}
}

// A redraft's wait keeps the draft on screen with the wait under the section
// it is for, and esc stops it naming that section.
func TestProfileScreen_ARedraftWaitsUnderItsSection(t *testing.T) {
	p := draftScreen()
	p.Update(key("down"))
	p.Work("redrafting Scope")
	view := ansi.Strip(p.View(100))
	for _, want := range []string{"Purpose", "redrafting Scope", "the other sections stand", "the section keeps its last text"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the wait lacks %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Keep test-writer?") {
		t.Fatalf("nothing can be saved during a redraft, so the card is not drawn:\n%s", view)
	}
	if done, res := p.Update(key("esc")); !done || res.Action != ProfileAbort || res.Index != 1 {
		t.Fatalf("esc on the wait = %+v", res)
	}
}

// The sections scroll under the card, and the fold counts the sections it is
// holding back rather than hiding them (invariant 4).
func TestProfileScreen_TheProfileScrolls(t *testing.T) {
	p := draftScreen()
	p.MaxLines = 30
	first := p.View(100)
	if !strings.Contains(ansi.Strip(first), "more section") {
		t.Fatalf("the fold should count what it holds back:\n%s", first)
	}
	for range 3 {
		p.Update(tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModShift})
	}
	if p.View(100) == first {
		t.Fatal("shift+↓ should scroll the profile")
	}
}

// The offset is held inside the sections: pressing past the last row settles
// on it rather than reading into nothing, and one press back up is enough to
// undo the overshoot.
func TestProfileScreen_TheProfilePaneHoldsItsEnds(t *testing.T) {
	p := draftScreen()
	p.MaxLines = 30
	top := p.View(100)
	for range 80 {
		p.Update(tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModShift})
	}
	if want := p.pane.Total - p.pane.Height; p.pane.Offset != want {
		t.Fatalf("offset after the overshoot = %d, want %d", p.pane.Offset, want)
	}
	end := p.View(100)
	if end == top {
		t.Fatalf("shift+↓ should scroll the profile:\n%s", end)
	}
	p.Update(tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModShift})
	if p.View(100) == end {
		t.Fatalf("shift+↑ after an overshoot should scroll back up:\n%s", end)
	}
}

// The card is the thing the surface is for, so it is the one thing that never
// gives ground: a decision whose keys were cut off by the height is not one.
func TestProfileScreen_TheCardSurvivesAShortSurface(t *testing.T) {
	for _, height := range []int{15, 18, 22, 40} {
		p := draftScreen()
		p.MaxLines = height
		p.Update(key("tab"))
		view := p.View(80)
		if lines := strings.Count(view, "\n") + 1; lines > height {
			t.Fatalf("h=%d: rendered %d lines", height, lines)
		}
		for _, want := range []string{"Keep test-writer?", "Save to this project", "[enter] confirm", "[tab] the sections", "[esc] take none"} {
			if !strings.Contains(ansi.Strip(view), want) {
				t.Fatalf("h=%d: the card lost %q:\n%s", height, want, view)
			}
		}
	}
}

// The selected section stays in the pane as the pointer moves down a folded
// draft.
func TestProfileScreen_TheSelectedSectionIsKeptInView(t *testing.T) {
	p := draftScreen()
	p.MaxLines = 24
	for range 7 {
		p.Update(key("down"))
	}
	if view := ansi.Strip(p.View(80)); !strings.Contains(view, "❯ Model") {
		t.Fatalf("the last section should be in view once selected:\n%s", view)
	}
}
