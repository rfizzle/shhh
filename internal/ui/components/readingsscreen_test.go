package components

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/ui/golden"
)

// readingsFixture is five readings across two turns, newest first: turn 2's
// last one steered and was taken back, the one before it steered and stood,
// the first had what it needed; turn 1's two were an unclear one and a quiet
// on-target one no transcript row was written for.
func readingsFixture() ([]ReadingsItem, [][]string) {
	items := []ReadingsItem{
		{Round: 11, Turn: 2, Tone: SummaryOffTarget, Steered: true, Withdrawn: true},
		{Round: 6, Turn: 2, Tone: SummaryOffTarget, Steered: true},
		{Round: 3, Turn: 2, Tone: SummarySufficient},
		{Round: 9, Turn: 1, Tone: SummaryUnclear},
		{Round: 3, Turn: 1, Tone: SummaryOnTarget},
	}
	bodies := [][]string{
		{"Rewriting the README's install section after the exporter fix.",
			"  the instruction was the exporter; docs were not asked for",
			"read against: fix the CSV exporter's quoting"},
		{"Editing the importer's tests, which the exporter does not share.",
			"  the importer is not the exporter",
			"read against: fix the CSV exporter's quoting"},
		{"The quoting fix is in and its test passes; still reading callers.",
			"read against: fix the CSV exporter's quoting"},
		{"Reading the CI config; unclear how it bears on the loop.",
			"read against: find why the loop never stops"},
		{"Reading the loop and its round counter.",
			"read against: find why the loop never stops"},
	}
	return items, bodies
}

// readingsScreen is the screen over the fixture, its preview built the way
// the host builds it: an opened summary row per reading.
func readingsScreen(focus int) *ReadingsScreen {
	items, bodies := readingsFixture()
	s := &ReadingsScreen{
		Readings: items,
		Row: func(i, _ int) ActivityRow {
			r := items[i]
			detail := append([]string{bodies[i][0], SummaryGlyph(r.Tone) + " " + SummaryWord(r.Tone)}, bodies[i][1:]...)
			return ActivityRow{
				Kind: ActivitySummary, Verb: "summary", Target: fmt.Sprintf("round %d", r.Round),
				Outcome:  SummaryGlyph(r.Tone) + " " + SummaryWord(r.Tone),
				Counts:   "1 line",
				Expanded: true, Detail: detail,
			}
		},
		Subject: "5 readings", Cost: "$0.0142 spent", maxLines: 18,
	}
	s.Focus = focus
	return s
}

// The list is one row per reading in the rail's own marks, and the preview is
// the reading whole with what became of its steer.
func TestReadingsScreen_ThePreviewIsTheReadingWhole(t *testing.T) {
	view := ansi.Strip(readingsScreen(0).View(130))
	for _, want := range []string{
		"/readings", "5 readings", "$0.0142 spent", "[q] back",
		"⚠ r 11 · off target", "withdrawn", "⚠ r 6 · off target", "steered",
		"▸ r 3 · on target", "turn 1",
		"docs were not asked for", "read against: fix the CSV exporter's quoting",
		"it steered the turn, and the steer was taken back",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the screen is missing %q:\n%s", want, view)
		}
	}
}

// A reading that interrupted nobody says nothing about a steer.
func TestReadingsScreen_AQuietReadingClaimsNoSteer(t *testing.T) {
	view := ansi.Strip(readingsScreen(4).View(130))
	if strings.Contains(view, "it steered the turn") {
		t.Errorf("a reading that steered nothing claimed a steer:\n%s", view)
	}
}

// Once the oldest readings have been let go, the screen says so under the
// header rather than beginning part-way through without a word.
func TestReadingsScreen_SaysWhenTheOldestWereDropped(t *testing.T) {
	s := readingsScreen(0)
	s.Dropped, s.Kept = 3, 200
	if view := ansi.Strip(s.View(130)); !strings.Contains(view, "3 oldest readings let go · the screen keeps the last 200") {
		t.Errorf("the dropped readings went unsaid:\n%s", view)
	}
	if view := ansi.Strip(readingsScreen(0).View(130)); strings.Contains(view, "let go") {
		t.Errorf("a history with nothing dropped said something was:\n%s", view)
	}
}

// The pointer moves on the family's keys, and the way out closes the screen.
func TestReadingsScreen_MovesAndLeaves(t *testing.T) {
	s := readingsScreen(0)
	s.View(110)
	if done := s.Update(tea.KeyPressMsg{Code: tea.KeyDown}); done || s.Focus != 1 {
		t.Fatalf("down: done %v, focus %d", done, s.Focus)
	}
	if done := s.Update(tea.KeyPressMsg{Code: 'q', Text: "q"}); !done {
		t.Fatal("q did not close the screen")
	}
}

// Stacked, both panes stay and nothing runs past the terminal.
func TestReadingsScreen_NarrowStacksThePanes(t *testing.T) {
	s := readingsScreen(1)
	s.maxLines = 26
	view := s.View(60)
	plain := ansi.Strip(view)
	if !strings.Contains(plain, "r 6 · off target") || !strings.Contains(plain, "the importer is not the exporter") {
		t.Errorf("the stacked screen dropped a pane:\n%s", plain)
	}
	for _, line := range strings.Split(view, "\n") {
		if lipgloss.Width(line) > 60 {
			t.Errorf("a row ran past the terminal: %q", ansi.Strip(line))
		}
	}
}

// TestGolden_ReadingsScreen captures `/readings` over five readings across two
// turns: a steer taken back, a steer that stood, and a quiet reading no
// transcript row was written for.
func TestGolden_ReadingsScreen(t *testing.T) {
	captureGolden(t, "readings-screen", "the readings screen", goldenWidths, func(width int) []golden.Panel {
		dropped := readingsScreen(1)
		dropped.Dropped, dropped.Kept = 3, 200
		return []golden.Panel{
			{Label: "a steer taken back · the reading whole, and what became of its steer", View: readingsScreen(0).View(width)},
			{Label: "a steer that stood · the oldest let go, and the screen saying so", View: dropped.View(width)},
			{Label: "a quiet reading · kept here though the transcript drew no row for it", View: readingsScreen(4).View(width)},
		}
	})
}
