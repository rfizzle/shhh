package components

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
)

// One pointer, drawn one way: the mark is in its own column and the row is
// lit beside it, out to the width the list was given. A pointer inside the
// highlight is part of the row rather than pointing at it, and a highlight
// that stopped at the last word would end in a different column on every row.
func TestLitOption_PointerOutsideTheHighlight(t *testing.T) {
	withColorProfile(t, colorprofile.ANSI256)

	const width = 40
	row := LitOption("gemini-3-flash  1M ctx", width)
	if got := lipgloss.Width(ansi.Strip(row)); got != width {
		t.Fatalf("the lit row is %d columns, want the list's %d", got, width)
	}
	if plain := ansi.Strip(ansi.Truncate(row, GridPointerWidth, "")); plain != "❯ " {
		t.Fatalf("the pointer column reads %q, want the pointer", plain)
	}
	// `48;5;` is how a 256-colour terminal is told to set a background, which
	// is the whole of what "lit" is.
	const background = "48;5;"
	before, after, found := strings.Cut(row, "❯")
	if !found {
		t.Fatalf("the row carries no pointer: %q", row)
	}
	if strings.Contains(before, background) {
		t.Fatalf("the pointer is inside the highlight: %q", row)
	}
	if !strings.Contains(after, background) {
		t.Fatalf("the row is not lit: %q", row)
	}
}

// The lit row names its own foreground. Left to the terminal's default it is
// a colour the palette never issued, and on half the terminals in use it
// reads brighter than the bright token beside it.
func TestFocusRow_NamesItsForeground(t *testing.T) {
	if sty.FocusRow.GetForeground() == nil {
		t.Fatal("the lit row inherits the terminal's own foreground")
	}
}

// A terminal with no colour has no highlight to give, so the pointer is the
// whole of the cursor there: the row comes back with its cells and the mark
// in front of them (invariant 1).
func TestLitOption_SurvivesATerminalWithNoColour(t *testing.T) {
	withColorProfile(t, colorprofile.NoTTY)

	row := LitOption("gemini-3-flash", 30)
	if got := ansi.Strip(row); !strings.HasPrefix(got, "❯ gemini-3-flash") {
		t.Fatalf("the pointer is the cursor with no colour, got %q", got)
	}
}
