package components

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/ui/golden"
)

// flakesScreen is the screen over three checks, the latest flake first: a
// test that has flaked nine times, a lint run that flaked once, and a vet
// check in another suite with no session on its row.
func flakesScreen(focus int) *FlakesScreen {
	s := &FlakesScreen{Rows: []FlakesRow{
		{Check: "test", Suite: "default", Command: "make test", Seen: 9, LastSeen: "2h ago", FirstSeen: "6d ago",
			FirstExit: 2, Session: "418"},
		{Check: "lint", Suite: "default", Command: "golangci-lint run --timeout 5m ./...", Seen: 1,
			LastSeen: "1d ago", FirstSeen: "1d ago", FirstExit: 1, Session: "402"},
		{Check: "vet", Suite: "fast", Command: "go vet ./...", Seen: 3, LastSeen: "4d ago", FirstSeen: "5d ago", FirstExit: 1},
	}, maxLines: 16}
	s.Focus = focus
	return s
}

// The list is a row per check with its suite, count and when it last
// flaked; the preview is the check's count, its dates, the exit code the
// failing run gave and the command it runs.
func TestFlakesScreen_ListsEveryCheckWithItsCount(t *testing.T) {
	tests := []struct {
		name  string
		focus int
		want  []string
		not   []string
	}{
		{"a check that keeps flaking", 0,
			[]string{"/gate flakes", "3 checks · 13 flakes", "[esc] back", "test", "default · 9 times", "2h ago",
				"lint", "default · 1 time", "vet", "fast · 3 times", "flaked 9 times in this checkout",
				"last 2h ago · first 6d ago", "the failing run exited 2", "last in session 418", "make test"}, nil},
		{"a first flake has no first date beside its last", 1,
			[]string{"flaked 1 time in this checkout", "last 1d ago", "golangci-lint run"}, []string{"first 1d ago"}},
		{"a row with no session names none", 2,
			[]string{"flaked 3 times in this checkout", "go vet ./..."}, []string{"last in session"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			view := ansi.Strip(flakesScreen(tc.focus).View(130))
			for _, want := range tc.want {
				if !strings.Contains(view, want) {
					t.Errorf("the screen is missing %q:\n%s", want, view)
				}
			}
			for _, not := range tc.not {
				if strings.Contains(view, not) {
					t.Errorf("the screen says %q:\n%s", not, view)
				}
			}
		})
	}
}

// An empty ledger says so in the list's place.
func TestFlakesScreen_AnEmptyLedgerSaysSo(t *testing.T) {
	view := ansi.Strip((&FlakesScreen{maxLines: 10}).View(110))
	if !strings.Contains(view, "no check has flaked in this checkout") {
		t.Errorf("an empty ledger did not say so:\n%s", view)
	}
}

// The pointer moves on the family's keys, and the way out closes the screen.
func TestFlakesScreen_MovesAndLeaves(t *testing.T) {
	s := flakesScreen(0)
	s.View(110)
	if done := s.Update(tea.KeyPressMsg{Code: tea.KeyDown}); done || s.Focus != 1 {
		t.Fatalf("down: done %v, focus %d", done, s.Focus)
	}
	if done := s.Update(tea.KeyPressMsg{Code: 'q', Text: "q"}); done {
		t.Fatal("q closed the screen; esc is the one way out")
	}
	if done := s.Update(tea.KeyPressMsg{Code: tea.KeyEscape}); !done {
		t.Fatal("esc did not close the screen")
	}
}

// Stacked, both panes stay and nothing runs past the terminal.
func TestFlakesScreen_NarrowStacksThePanes(t *testing.T) {
	s := flakesScreen(0)
	s.maxLines = 26
	view := s.View(60)
	plain := ansi.Strip(view)
	if !strings.Contains(plain, "default · 9 times") || !strings.Contains(plain, "flaked 9 times in this checkout") {
		t.Errorf("the stacked screen dropped a pane:\n%s", plain)
	}
	for _, line := range strings.Split(view, "\n") {
		if lipgloss.Width(line) > 60 {
			t.Errorf("a row ran past the terminal: %q", ansi.Strip(line))
		}
	}
}

// TestGolden_FlakesScreen captures `/gate flakes` over three checks: one
// that keeps flaking, one first flake, and one with no session on its row.
func TestGolden_FlakesScreen(t *testing.T) {
	captureGolden(t, "flakes-screen", "the flakes screen", goldenWidths, func(width int) []golden.Panel {
		return []golden.Panel{
			{Label: "a check that keeps flaking · its count, its dates and the code it failed with", View: flakesScreen(0).View(width)},
			{Label: "a first flake · one date, the command wrapped dim", View: flakesScreen(1).View(width)},
			{Label: "nothing has flaked · the list says so", View: (&FlakesScreen{maxLines: 8}).View(width)},
		}
	})
}
