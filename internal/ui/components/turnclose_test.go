package components

// The turn close: what the rows state, and what they drop first.

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func closeFixture() TurnClose {
	return TurnClose{
		Steps: 4, Tools: 18, Elapsed: "1m 04s", Spend: "$0.14", Note: "round 7/25",
		Changes: &TurnChanges{
			Files: 3, Added: 30, Removed: 4,
			Keys: []TurnKey{{Key: "[v]", Label: "review"}, {Key: "[u]", Label: "undo turn"}},
			Note: "all tracked in git",
		},
		Checks: &TurnChecks{Label: "go test ./internal/agent/...", Counts: "41 packages · 12.8s"},
	}
}

func TestTurnClose_ThreeRowsAnswerThreeQuestions(t *testing.T) {
	lines := strings.Split(ansi.Strip(closeFixture().View(130)), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected three rows, got %d:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	for i, want := range []string{
		"✓ Done · 4 steps · 18 tools · 1m 04s · $0.14",
		"▎✎ 3 files changed +30 −4 · [v] review · [u] undo turn",
		"✓ go test ./internal/agent/... passing · 41 packages · 12.8s",
	} {
		if !strings.Contains(lines[i], want) {
			t.Errorf("row %d should state %q, got %q", i+1, want, lines[i])
		}
	}
	if !strings.HasSuffix(lines[0], "round 7/25") || !strings.HasSuffix(lines[1], "all tracked in git") {
		t.Errorf("the notes are right-aligned:\n%s", strings.Join(lines, "\n"))
	}
}

func TestTurnClose_OnlyTheChangesRowCarriesTheMutationRail(t *testing.T) {
	lines := strings.Split(ansi.Strip(closeFixture().View(130)), "\n")
	if strings.HasPrefix(lines[0], "▎") || strings.HasPrefix(lines[2], "▎") {
		t.Errorf("a row that wrote nothing has no rail:\n%s", strings.Join(lines, "\n"))
	}
	if !strings.HasPrefix(lines[1], "▎") {
		t.Errorf("the changed-files row carries the mutation rail, got %q", lines[1])
	}
	// The rows line up: whatever the rail column holds, the glyph follows it.
	for i, l := range lines {
		if got := []rune(l)[1]; got != '✓' && got != '✎' && got != '✗' && got != '⊘' {
			t.Errorf("row %d should carry its glyph in the second column, got %q in %q", i+1, got, l)
		}
	}
}

func TestTurnClose_ATurnThatChangedNothingIsOneRow(t *testing.T) {
	c := TurnClose{Elapsed: "0.4s"}
	view := ansi.Strip(c.View(80))
	if strings.Contains(view, "\n") {
		t.Fatalf("with no changes and no checks the block is one row, got:\n%s", view)
	}
	if strings.Contains(view, "steps") || strings.Contains(view, "tools") {
		t.Fatalf("a stat the turn cannot report is left out, not reported as zero: %q", view)
	}
}

func TestTurnClose_TheNoteDropsBeforeTheStatement(t *testing.T) {
	c := closeFixture()
	for _, width := range []int{60, 40, 24, 12} {
		for _, line := range strings.Split(ansi.Strip(c.View(width)), "\n") {
			if got := len([]rune(line)); got > width {
				t.Errorf("at width %d a row ran to %d columns: %q", width, got, line)
			}
		}
	}
	if strings.Contains(ansi.Strip(c.View(40)), "round 7/25") {
		t.Error("a note that does not fit is dropped, never wrapped")
	}
}

func TestTurnClose_StateAndVerdictAreWordsAsWellAsGlyphs(t *testing.T) {
	for state, word := range map[TurnState]string{
		TurnDone: "Done", TurnCancelled: "Cancelled", TurnFailed: "Failed",
	} {
		c := closeFixture()
		c.State = state
		if !strings.Contains(ansi.Strip(c.View(130)), word) {
			t.Errorf("state %v should read %q", state, word)
		}
	}
	c := closeFixture()
	c.Checks.Failed = true
	if !strings.Contains(ansi.Strip(c.View(130)), "failing") {
		t.Error("a failing verdict says so in words")
	}
}
