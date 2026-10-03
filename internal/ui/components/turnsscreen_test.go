package components

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/ui/golden"
)

// turnsFixture is six turns, newest first: one still running, one that ran a
// command and wrote nothing, one that committed, one whose check failed, one
// restored from an ended sitting with its files and no figures, and a first
// that was stopped before it wrote anything.
func turnsFixture() []TurnsItem {
	return []TurnsItem{
		{N: 6, Running: &InspectorTurn{Step: 2, Tools: 7, Files: 1, Added: 4, Removed: 1, Running: true},
			Files: []TurnsFile{{Path: "internal/agent/loop.go", Added: 4, Removed: 1}}, Added: 4, Removed: 1, Reviewable: true},
		{N: 5, Close: &TurnClose{State: TurnDone, Steps: 1, Tools: 3, Elapsed: "12s", Spend: "$0.0210", WroteNothing: true}},
		{N: 4, Close: &TurnClose{State: TurnDone, Steps: 2, Tools: 9, Elapsed: "1m 04s", Spend: "$0.0870",
			Changes: &TurnChanges{Files: 2, Added: 18, Removed: 6},
			Commit:  &TurnCommit{Receipt: "committed a1b2c3d on main · raise the round cap"}},
			Files: []TurnsFile{{Path: "internal/agent/loop.go", Added: 12, Removed: 4},
				{Path: "internal/agent/loop_test.go", Added: 6, Removed: 2}},
			Added: 18, Removed: 6, Reviewable: true},
		{N: 3, Close: &TurnClose{State: TurnDone, Steps: 1, Tools: 5, Elapsed: "38s", Spend: "$0.0440",
			Changes: &TurnChanges{Files: 1, Added: 3, Removed: 3, Keys: []TurnKey{{Key: "[ctrl+space/ctrl+y]", Label: "commit"}}},
			Checks: &TurnChecks{Failed: true, Label: "go test ./internal/agent/...", Counts: "1/3 checks · 4.2s",
				Again: "/gate run default"}},
			Files: []TurnsFile{{Path: "internal/agent/policy.go", Added: 3, Removed: 3}},
			Added: 3, Removed: 3, Reviewable: true},
		{N: 2, Files: []TurnsFile{{Path: "README.md", Added: 9, Removed: 0}, {Path: "docs/loop.md", Added: 2, Removed: 1}},
			Added: 11, Removed: 1, Reviewable: true},
		{N: 1, Close: &TurnClose{State: TurnCancelled, Tools: 2, Elapsed: "4.1s", Spend: "$0.0060"}},
	}
}

// turnsScreen is the screen over the fixture with the pointer on one turn.
func turnsScreen(focus int) *TurnsScreen {
	return &TurnsScreen{Turns: turnsFixture(), Focus: focus,
		Subject: "6 turns", Tools: "26 tools", Spend: "$0.1580 spent", maxLines: 20}
}

// The list is one row per turn in the close's own marks and words, and the
// preview is the turn's close drawn whole with the files it changed.
func TestTurnsScreen_ThePreviewIsTheTurnsClose(t *testing.T) {
	view := ansi.Strip(turnsScreen(2).View(130))
	for _, want := range []string{
		"/turns", "6 turns · 26 tools", "$0.1580 spent", "[q] back",
		"▸ turn 6", "running", "✓ turn 5", "changed no files", "✓ turn 4", "· turn 2", "no figures kept",
		"⊘ turn 1", "cancelled",
		"∗ worked 1m 04s · 9 tools · $0.0870", "committed a1b2c3d on main",
		"internal/agent/loop_test.go", "[enter] review turn 4",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the screen is missing %q:\n%s", want, view)
		}
	}
}

// The close drawn here offers none of the row's keys: this screen answers
// none of them, and a commit offer beside a failing check would be a key
// that does nothing. Nor does it suggest running the checks again from a
// screen that is reading an old turn.
func TestTurnsScreen_ThePreviewOffersNoneOfTheRowsKeys(t *testing.T) {
	view := ansi.Strip(turnsScreen(3).View(130))
	if !strings.Contains(view, "go test ./internal/agent/... failing") {
		t.Fatalf("the failing check went unsaid:\n%s", view)
	}
	for _, key := range []string{"[ctrl+space/ctrl+y] commit", "/gate run default"} {
		if strings.Contains(view, key) {
			t.Errorf("the preview offered the row's %q:\n%s", key, view)
		}
	}
}

// A turn the session holds no close for says its figures were not kept, and
// reports no zero it never measured.
func TestTurnsScreen_ARestoredTurnReportsNoFabricatedFigures(t *testing.T) {
	view := ansi.Strip(turnsScreen(4).View(130))
	if !strings.Contains(view, "the session kept its files and not its close") || !strings.Contains(view, "README.md") {
		t.Fatalf("the restored turn did not say what it has and has not got:\n%s", view)
	}
	// The list names every turn in lower case; a capital Done is a close
	// block's, and this turn has none to draw.
	for _, zero := range []string{" 0 steps", " 0 tools", "$0.0000", "Done"} {
		if strings.Contains(view, zero) {
			t.Errorf("the restored turn reported %q:\n%s", zero, view)
		}
	}
}

// The review is an offer only where the turn changed something: grey on a
// turn that wrote nothing, and enter there does nothing.
func TestTurnsScreen_EnterReviewsOnlyATurnWithChanges(t *testing.T) {
	s := turnsScreen(1)
	if view := s.View(130); !strings.Contains(view, sty.dimmer.Render("[enter]")) {
		t.Errorf("the review on a turn that wrote nothing was not grey:\n%s", ansi.Strip(view))
	}
	if done, res := s.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); done || res.Review != 0 {
		t.Fatalf("enter on a turn with nothing to review: done %v, review %d", done, res.Review)
	}
	s.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if done, res := s.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); !done || res.Review != 4 {
		t.Fatalf("enter on turn 4: done %v, review %d", done, res.Review)
	}
	if done, res := turnsScreen(0).Update(tea.KeyPressMsg{Code: 'q', Text: "q"}); !done || res.Review != 0 {
		t.Fatalf("q: done %v, review %d", done, res.Review)
	}
}

// Stacked, both panes stay and nothing runs past the terminal.
func TestTurnsScreen_NarrowStacksThePanes(t *testing.T) {
	s := turnsScreen(2)
	s.maxLines = 30
	view := s.View(60)
	plain := ansi.Strip(view)
	if !strings.Contains(plain, "turn 4") || !strings.Contains(plain, "committed a1b2c3d") {
		t.Errorf("the stacked screen dropped a pane:\n%s", plain)
	}
	for _, line := range strings.Split(view, "\n") {
		if lipgloss.Width(line) > 60 {
			t.Errorf("a row ran past the terminal: %q", ansi.Strip(line))
		}
	}
}

// TestGolden_TurnsScreen captures `/turns` over six turns: the one running,
// one that wrote nothing, one that committed, one whose check failed, one
// restored with no figures kept, and one that was stopped.
func TestGolden_TurnsScreen(t *testing.T) {
	captureGolden(t, "turns-screen", "the turns screen", goldenWidths, func(width int) []golden.Panel {
		return []golden.Panel{
			{Label: "the turn in flight · the rail's reading of it, on the close's grid", View: turnsScreen(0).View(width)},
			{Label: "a turn that wrote nothing · the review offered grey", View: turnsScreen(1).View(width)},
			{Label: "a turn that committed · its close whole, and its files", View: turnsScreen(2).View(width)},
			{Label: "a failing check · the close's verdict, none of the row's keys", View: turnsScreen(3).View(width)},
			{Label: "a restored turn · its files, and no figures kept", View: turnsScreen(4).View(width)},
		}
	})
}
