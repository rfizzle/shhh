package components

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
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
	p.Show(ProfileDraftView{
		Name:        "test-writer",
		Description: "adds table-driven tests for a package and runs them",
		Facts: []ProfileFact{
			{Label: "permissions", Value: "read + write + execute", Tone: ToneRisk, Detail: "it can change things"},
			{Label: "model", Value: "inherited from this session"},
		},
		Why:    "a writer that could not run the tests would be proposing them",
		Prompt: strings.Repeat("A line of the profile that is long enough to wrap on a narrow pane.\n", 12),
	}, []SelectOption{
		{Label: "Save to this project", Desc: ".shhh/agents"},
		{Label: "Save globally", Desc: "~/.config/shhh/agents"},
	})
	return p
}

// The card's rows map onto the actions without the host having to know where
// the save rows end.
func TestProfileScreen_DecisionRows(t *testing.T) {
	for _, tc := range []struct {
		downs  int
		action ProfileAction
		index  int
	}{
		{0, ProfileSave, 0},
		{1, ProfileSave, 1},
		{3, ProfileDiscard, 0},
	} {
		p := draftScreen()
		for range tc.downs {
			p.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		}
		done, result := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		res := result
		if !done || res.Action != tc.action || res.Index != tc.index {
			t.Fatalf("%d downs: %+v", tc.downs, res)
		}
	}
	// Refine refuses to confirm without a note, the way every note-required
	// option does, and carries the note when it has one.
	p := draftScreen()
	for range 2 {
		p.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	if done, _ := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); done {
		t.Fatal("Refine should refuse an empty note")
	}
	p.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	typeRunes(p, "table driven")
	done, result := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	res := result
	if !done || res.Action != ProfileRefine || res.Text != "table driven" {
		t.Fatalf("refine = %+v", res)
	}
}

// The profile scrolls under the decision, and the fold counts what it is
// holding back rather than hiding it (invariant 4).
func TestProfileScreen_TheProfileScrolls(t *testing.T) {
	p := draftScreen()
	p.MaxLines = 30
	first := p.View(100)
	if !strings.Contains(first, "more lines") {
		t.Fatalf("the fold should count what it holds back:\n%s", first)
	}
	for range 3 {
		p.Update(tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModShift})
	}
	if p.View(100) == first {
		t.Fatal("shift+↓ should scroll the profile")
	}
}

// The card is the thing the surface is for, so it is the one thing that never
// gives ground: a decision whose keys were cut off by the height is not one.
func TestProfileScreen_TheCardSurvivesAShortSurface(t *testing.T) {
	for _, height := range []int{15, 18, 22, 40} {
		p := draftScreen()
		p.MaxLines = height
		view := p.View(80)
		if lines := strings.Count(view, "\n") + 1; lines > height {
			t.Fatalf("h=%d: rendered %d lines", height, lines)
		}
		for _, want := range []string{"Keep test-writer?", "Save to this project", "enter confirm", "esc cancel"} {
			if !strings.Contains(view, want) {
				t.Fatalf("h=%d: the card lost %q:\n%s", height, want, view)
			}
		}
	}
}
