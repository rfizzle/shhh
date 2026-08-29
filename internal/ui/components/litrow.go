package components

// The lit row (docs/interface/surfaces.md#reading-mode, S-122). Reading mode
// dresses exactly two things, and this is the second of them: the row the
// cursor is on takes the focus background with its words in bright, while the
// rail and the glyph keep the colours that say what the row did. The pointer
// stays outside the highlight, in the pointer column (§6a), so the cursor
// points at the row rather than being part of it.

import (
	"strings"
	"unicode"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// ansiReset is the sequence lipgloss ends every styled run with. A background
// armed before such a run is cleared by it, so painting a background across a
// line that already carries colours means re-arming after each one.
//
// It is read from the renderer's own vocabulary rather than written out here,
// because the two have to agree exactly and v2 shortened it — \x1b[m rather
// than \x1b[0m, which mean the same thing to a terminal and different things
// to strings.ReplaceAll (S-155).
const ansiReset = ansi.ResetStyle

// LitRow paints one already-rendered line as the row the reading cursor sits
// on: the focus background runs to the row's full width, the words go bright,
// and the glyphs before the first word keep their own colours inside the
// highlight — which is what lets a mutation rail stay a mutation rail while
// the row is lit (§14).
//
// skip leaves that many leading cells outside the highlight; that is where
// the pointer goes. width is the whole line's width, skip included.
//
// A terminal with no colour profile has no highlight to give. The row comes
// back untouched there rather than padded with spaces that mean nothing —
// the pointer is the whole of the cursor on such a terminal, which is why the
// cursor is a glyph and not a colour (invariant 1).
func LitRow(line string, skip, width int) string {
	bg := backgroundSeq(Palette.FocusBg)
	if bg == "" {
		return line
	}
	head := ansi.Truncate(line, skip, "")
	rest := ansi.TruncateLeft(line, skip, "")
	// The glyph run before the first word keeps its paint; from the first
	// word on, the row is bright, and that change is what the highlight is
	// made of.
	keep := glyphRunWidth(ansi.Strip(rest))
	glyphs := ansi.Truncate(rest, keep, "")
	words := ansi.Strip(ansi.TruncateLeft(rest, keep, ""))
	pad := max(width-skip-keep-lipgloss.Width(words), 0)
	return head + rearm(glyphs, bg) +
		sty.LitText.Render(words+strings.Repeat(" ", pad)) + ansiReset
}

// glyphRunWidth is how many cells of a plain row come before its first word.
// Rails, kind glyphs, state glyphs and the spaces between them are all in it;
// a verb, a path, a count or a heading is not.
func glyphRunWidth(s string) int {
	w := 0
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return w
		}
		w += lipgloss.Width(string(r))
	}
	return w
}

// rearm arms a background and puts it back after every reset the run carries,
// so colours already in the line survive inside the highlight instead of
// punching holes in it.
func rearm(s, bg string) string {
	return bg + strings.ReplaceAll(s, ansiReset, ansiReset+bg)
}

// backgroundSeq is the escape that turns one palette token on as a
// background, or "" where the terminal has no colour to turn on. It is the
// one place that needs the escape rather than a lipgloss.Style, because the
// row is lit by re-arming a background after every reset already in the line
// (rearm, above) rather than by rendering it.
func backgroundSeq(t Token) string {
	col := t.Color()
	if col == nil || col == (lipgloss.NoColor{}) {
		return ""
	}
	return ansi.NewStyle().BackgroundColor(col).String()
}
