package components

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/ui/golden"
)

// alertsFixture is four episodes, standing first then superseded, each newest
// first: a suite still failing since turn 3, a formatter that broke once in
// the turn running now, a linter the quality gate answered, and a build a
// clean run of itself answered inside the turn it broke in.
func alertsFixture() []AlertsItem {
	return []AlertsItem{
		{Alert: InspectorAlert{Label: "make test", Note: "exit 2", Runs: 3, Turn: 3, Turns: 2},
			Runs: []AlertsRun{
				{Turn: 3, Line: "make test", Outcome: "exit 2", Duration: "4.2s", Evidence: "ev-0f3a9c1d2e4b5a67"},
				{Turn: 3, Line: "make test", Outcome: "exit 2", Duration: "3.9s"},
				{Turn: 5, Line: "make test", Outcome: "killed · signal 9", Duration: "30s", Evidence: "ev-7b21c0de9f8a3e44"},
			}},
		{Alert: InspectorAlert{Label: "gofmt", Note: "exit 1", Runs: 1, Turn: 5, Turns: 1},
			Runs: []AlertsRun{{Turn: 5, Line: "gofmt -l internal/agent", Outcome: "exit 1"}}},
		{Alert: InspectorAlert{Label: "golangci-lint run", Note: "exit 1", Runs: 2, Turn: 2, Turns: 1, Superseded: true},
			Answer: &AlertsAnswer{What: "quality gate default passed", Turn: 4},
			Runs: []AlertsRun{
				{Turn: 2, Line: "golangci-lint run ./...", Outcome: "exit 1", Duration: "12s"},
				{Turn: 2, Line: "golangci-lint run ./internal/...", Outcome: "exit 1", Duration: "9.1s"},
			}},
		{Alert: InspectorAlert{Label: "go build", Note: "exit 1", Runs: 1, Turn: 1, Turns: 1, Superseded: true},
			Answer: &AlertsAnswer{What: "go build ./... came back clean", Turn: 1},
			Runs:   []AlertsRun{{Turn: 1, Line: "go build ./...", Outcome: "exit 1", Duration: "2.4s"}}},
	}
}

// alertsScreen is the screen over the fixture with the pointer on focus.
func alertsScreen(focus int, open bool) *AlertsScreen {
	return &AlertsScreen{Alerts: alertsFixture(), focus: focus, Open: open, maxLines: 24}
}

// The list is one row per episode in the rail row's own words, standing
// before superseded, and the preview is the episode's account with what
// answered it.
func TestAlertsScreen_ListsEveryEpisodeAndTheOneUnderThePointer(t *testing.T) {
	view := ansi.Strip(alertsScreen(2, false).View(130))
	for _, want := range []string{
		"/alerts", "2 standing", "2 superseded", "[q] back",
		"✗ make test", "exit 2 · 3 runs", "since turn 3", "standing",
		"✗ gofmt", "✓ golangci-lint run", "✓ go build", "superseded",
		"quality gate default passed · turn 4", "[enter] show each run",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the screen is missing %q:\n%s", want, view)
		}
	}
	if strings.Index(view, "✗ gofmt") > strings.Index(view, "✓ golangci-lint run") {
		t.Errorf("a superseded episode was listed above a standing one:\n%s", view)
	}
}

// A standing episode says nothing has answered it yet.
func TestAlertsScreen_AStandingEpisodeIsNotYetAnswered(t *testing.T) {
	if view := ansi.Strip(alertsScreen(0, false).View(130)); !strings.Contains(view, "not yet") {
		t.Errorf("a standing episode claimed an answer:\n%s", view)
	}
}

// Enter shows each run — its turn, its ending, its time and the evidence id
// where the output was kept — with the pointer on the first, and moving past
// the last one leaves the episode and puts them away again.
func TestAlertsScreen_EnterShowsEachRun(t *testing.T) {
	s := alertsScreen(0, false)
	s.View(130)
	if done, _ := s.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); done || !s.Open || s.runAt != 0 {
		t.Fatalf("enter: done %v, open %v, run %d", done, s.Open, s.runAt)
	}
	view := ansi.Strip(s.View(130))
	for _, want := range []string{"each run", "❯ ✗ turn 3 · exit 2 · 4.2s", "ev-0f3a9c1d2e4b5a67",
		"turn 5 · killed · signal 9 · 30s", "ev-7b21c0de9f8a3e44", "[enter] open its output"} {
		if !strings.Contains(view, want) {
			t.Errorf("the open episode is missing %q:\n%s", want, view)
		}
	}
	for range 2 {
		s.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	if !s.Open || s.focus != 0 || s.runAt != 2 {
		t.Fatalf("down should walk the runs: open %v, focus %d, run %d", s.Open, s.focus, s.runAt)
	}
	s.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if s.Open || s.focus != 1 {
		t.Fatalf("moving past the last run should leave the episode: open %v, focus %d", s.Open, s.focus)
	}
	if done, _ := s.Update(tea.KeyPressMsg{Code: tea.KeyEscape}); !done {
		t.Fatal("esc did not close the screen")
	}
}

// Enter on a run whose output was kept hands its id back for the host to
// open; on one whose output was never cut it opens nothing, says so, and
// does not offer the key.
func TestAlertsScreen_EnterOnARunOpensItsKeptOutput(t *testing.T) {
	s := alertsScreen(0, true)
	done, result := s.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !done || !result.Open || result.Evidence != "ev-0f3a9c1d2e4b5a67" || result.Line != "make test" {
		t.Fatalf("enter on a kept run: done %v, %+v", done, result)
	}
	s.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	done, result = s.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if done || result.Open {
		t.Fatalf("a run nothing was kept of opened something: done %v, %+v", done, result)
	}
	view := ansi.Strip(s.View(130))
	if !strings.Contains(view, "never cut, so nothing was kept") {
		t.Errorf("a run nothing was kept of did not say so:\n%s", view)
	}
	if strings.Contains(view, "open its output") {
		t.Errorf("a run nothing was kept of offered to open it:\n%s", view)
	}
}

// Where the evidence id will not stand at the end of a run's row it takes a
// row of its own rather than going, since it is how the output is reached.
func TestAlertsScreen_TheEvidenceIdIsNeverDropped(t *testing.T) {
	s := alertsScreen(0, true)
	s.maxLines = 40
	view := s.View(60)
	plain := ansi.Strip(view)
	if !strings.Contains(plain, "ev-0f3a9c1d2e4b5a67") {
		t.Errorf("a narrow screen dropped the evidence id:\n%s", plain)
	}
	for _, line := range strings.Split(view, "\n") {
		if lipgloss.Width(line) > 60 {
			t.Errorf("a row ran past the terminal: %q", ansi.Strip(line))
		}
	}
}

// A session that has broken nothing opens on a sentence saying so.
func TestAlertsScreen_EmptySaysNothingBroke(t *testing.T) {
	s := &AlertsScreen{maxLines: 12}
	view := ansi.Strip(s.View(130))
	if !strings.Contains(view, "nothing this session ran has come back broken") || strings.Contains(view, "standing") {
		t.Errorf("the empty screen:\n%s", view)
	}
	if done, _ := s.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); done || s.Open {
		t.Fatal("enter on an empty screen should do nothing")
	}
}

// TestGolden_AlertsScreen captures `/alerts` over two standing episodes and
// two superseded ones: the pointer on a standing one, on a superseded one the
// suite answered, one episode opened to its runs, and a session that has
// broken nothing.
func TestGolden_AlertsScreen(t *testing.T) {
	captureGolden(t, "alerts-screen", "the alerts screen", goldenWidths, func(width int) []golden.Panel {
		return []golden.Panel{
			{Label: "standing · the suite still failing, nothing has answered it", View: alertsScreen(0, false).View(width)},
			{Label: "superseded · the linter the quality gate answered, and the turn it did", View: alertsScreen(2, false).View(width)},
			{Label: "one episode opened · each run, its ending, its time and the evidence id its output was kept under", View: func() string { s := alertsScreen(0, true); s.maxLines = 34; return s.View(width) }()},
			{Label: "empty · a session that has broken nothing", View: (&AlertsScreen{maxLines: 12}).View(width)},
		}
	})
}
