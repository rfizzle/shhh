package components

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/ui/golden"
)

// stepsItems is a seven-step list: the first marked finished, the second the
// one the agent is on, the fourth a step the model ran ahead to, and the rest
// with nothing in the transcript titled for them.
func stepsItems() []StepsItem {
	return []StepsItem{
		{Number: 1, Title: "Read the loop", Done: true, Started: true, Count: "2 tools", Duration: "1.2s",
			Rows: []ActivityRow{
				{Kind: ActivityTool, Verb: "read", Target: "internal/agent/loop.go", Counts: "212 lines", Duration: "0.4s"},
				{Kind: ActivityTool, Verb: "search", Target: "maxRounds", Counts: "3 matches", Duration: "0.8s"},
			}},
		{Number: 2, Title: "Patch the round limit", Current: true, Started: true, Count: "2 tools", Duration: "4.1s",
			Paths: []string{"internal/agent/round.go", "internal/agent/round_test.go"},
			Rows: []ActivityRow{
				{Kind: ActivityEdit, Verb: "edit", Target: "internal/agent/round.go", Counts: "+12 −4 · 2 hunks", Duration: "1.1s"},
				{Kind: ActivityCommand, Verb: "run", Target: "go test ./internal/agent/...", Outcome: OutcomeOK,
					Counts: "1 line", Duration: "3.0s"},
			}},
		{Number: 3, Title: "Name the ceiling in the error", Paths: []string{"internal/agent/errors.go"}},
		{Number: 4, Title: "Read the headless driver", Started: true, Count: "1 tool",
			Rows: []ActivityRow{
				{Kind: ActivityTool, Verb: "read", Target: "internal/agent/headless.go", Counts: "480 lines"},
			}},
		{Number: 5, Title: "Carry the cap into the child"},
		{Number: 6, Title: "Run the whole suite"},
		{Number: 7, Title: "Write the report"},
	}
}

func stepsScreen(focus int) *StepsScreen {
	return &StepsScreen{Steps: stepsItems(), Focus: focus, Subject: "1 of 7", MaxLines: 16}
}

// The list is the checklist and the preview is what the transcript recorded
// for the step under the pointer, in the activity row's own words.
func TestStepsScreen_ThePreviewIsTheStepsRun(t *testing.T) {
	view := ansi.Strip(stepsScreen(1).View(130))
	for _, want := range []string{
		"/steps", "1 of 7", "[?] keys", "[q] back",
		"▸ Patch the round limit", "current", "✓ Read the loop", "done",
		"touches internal/agent/round.go", "internal/agent/round_test.go",
		"in the transcript · 2 tools · 4.1s", "+12 −4 · 2 hunks", "go test ./internal/agent/...",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the screen is missing %q:\n%s", want, view)
		}
	}
}

// A step the transcript has no run titled for says so, on its row and in the
// preview, rather than drawing an empty pane.
func TestStepsScreen_AStepWithNoRunSaysNotStarted(t *testing.T) {
	view := ansi.Strip(stepsScreen(2).View(130))
	if !strings.Contains(view, "not started · no step in the transcript is titled for it") {
		t.Errorf("the preview did not say the step has no run:\n%s", view)
	}
	if strings.Contains(view, "in the transcript ·") {
		t.Errorf("a step with no run drew a run's heading:\n%s", view)
	}
}

// The pointer moves on the family's keys, and the way out closes the screen.
func TestStepsScreen_MovesAndLeaves(t *testing.T) {
	s := stepsScreen(0)
	s.View(110)
	if done := s.Update(tea.KeyPressMsg{Code: tea.KeyDown}); done || s.Focus != 1 {
		t.Fatalf("down: done %v, focus %d", done, s.Focus)
	}
	if done := s.Update(tea.KeyPressMsg{Code: 'q', Text: "q"}); !done {
		t.Fatal("q did not close the screen")
	}
}

// Stacked, both panes stay and nothing runs past the terminal.
func TestStepsScreen_NarrowStacksThePanes(t *testing.T) {
	s := stepsScreen(1)
	s.MaxLines = 24
	view := s.View(60)
	plain := ansi.Strip(view)
	if !strings.Contains(plain, "Patch the round limit") || !strings.Contains(plain, "in the transcript") {
		t.Errorf("the stacked screen dropped a pane:\n%s", plain)
	}
	for _, line := range strings.Split(view, "\n") {
		if lipgloss.Width(line) > 60 {
			t.Errorf("a row ran past the terminal: %q", ansi.Strip(line))
		}
	}
}

// TestGolden_StepsScreen captures `/steps` over a seven-step list: the current
// step's run under it, the finished one, and a step nothing in the transcript
// is titled for.
func TestGolden_StepsScreen(t *testing.T) {
	captureGolden(t, "steps-screen", "the steps screen", goldenWidths, func(width int) []golden.Panel {
		return []golden.Panel{
			{Label: "the current step · its paths and the run the transcript titled for it", View: stepsScreen(1).View(width)},
			{Label: "a finished step · marked done by the agent's own progress line", View: stepsScreen(0).View(width)},
			{Label: "a step with no run · not started, and the paths it said it would touch", View: stepsScreen(2).View(width)},
		}
	})
}
