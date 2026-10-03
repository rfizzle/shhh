package components

// The open card (docs/interface/surfaces.md#the-step): a card opened onto
// its calls lists them under the receipt's verbs, one group line per kind,
// with the reads rolled up by directory and every other call a row of its
// own, and ends in the strip — one glyph per call in the order they were
// made, which is the way to one call. The host lays each line on the band
// as a unit of its own, so reading mode's cursor can stand on a group and a
// click can say which line it landed on; this file draws the lines.

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// cardCallRail is the column a call row's rail stands in, one right of the
// group line's ▾, so the rails of an open card's rows run down one column
// under their group and the glyphs after them line up with the group's
// words.
const cardCallRail = CardBodyIndent + 1

// cardDirIndent is where a read directory's row starts: the group's words'
// column, under the verb.
const cardDirIndent = CardBodyIndent + 2

// CardGroupLine is the line heading one group of an open card's calls:
// `▾ read 17 files  41s`. Folded, it is the line alone, with ▸.
type CardGroupLine struct {
	// Label is the group's clause in the receipt's verbs.
	Label string
	// Right is what the group's calls took, or a write's `+N −M`.
	Right string
	// Folded draws the group as its line alone.
	Folded bool
	// Selected puts the reading cursor on the line.
	Selected bool
}

// View draws the line at the pane's width.
func (g CardGroupLine) View(width int) string {
	mark := "▾"
	if g.Folded {
		mark = "▸"
	}
	right := ""
	if g.Right != "" {
		if painted, ok := paintLineCounts(g.Right); ok {
			right = painted
		} else {
			right = sty.dim.Render(g.Right)
		}
	}
	rightW := lipgloss.Width(right)
	room := max(width-cardMargin-CardBodyIndent-2-rightW-1, 1)
	left := strings.Repeat(" ", CardBodyIndent) + sty.dim.Render(mark+" "+Clip(g.Label, room))
	gap := max(width-cardMargin-lipgloss.Width(left)-rightW, 1)
	line := left + strings.Repeat(" ", gap) + right
	if g.Selected {
		return cardLit(line, CardBodyIndent, width)
	}
	return onBand(Clip(line, width), width)
}

// CardDirRow is one directory of an open card's reads: the directory, then
// the files read in it by name, as many as the pane holds and the rest
// counted, `… +7`. A row with no directory and no files is the reads past
// the ceiling on directories, counted the same way.
type CardDirRow struct {
	Dir   string
	Files []string
	// More is how many files the row counts beyond Files.
	More int
}

// View draws the row at the pane's width.
func (d CardDirRow) View(width int) string {
	inner := max(width-cardMargin-cardDirIndent, 1)
	head := ""
	if d.Dir != "" {
		head = sty.dimmer.Render(d.Dir) + " "
	}
	room := inner - lipgloss.Width(head)
	listed := ""
	for n := len(d.Files); n >= 0; n-- {
		parts := append([]string{}, d.Files[:n]...)
		if rest := len(d.Files) - n + d.More; rest > 0 {
			parts = append(parts, "… +"+strconv.Itoa(rest))
		}
		listed = strings.Join(parts, " · ")
		if lipgloss.Width(listed) <= room {
			break
		}
	}
	line := strings.Repeat(" ", cardDirIndent) + head + sty.dim.Render(Clip(listed, max(room, 1)))
	return onBand(Clip(line, width), width)
}

// CardCallRow is one call inside an open card: its rail and glyph, its
// subject, the one line it stands on where it has one — the line a break
// was said with, an edit's first changed line — and its outcome and time on
// the right. The row's body, where it is open, is the host's: it is the
// same body the call's row draws anywhere, laid under this line.
type CardCallRow struct {
	// Row is the call as its row states it: the kind and the state that pick
	// the glyph and the rail, the subject, the outcome and the time.
	Row ActivityRow
	// Line is the call's one line, after the subject.
	Line string
	// Lit is the strip's cursor on this call, or reading mode's on the row:
	// the pointer in its column and the row on the focus ground.
	Lit bool
}

// View draws the row's line at the pane's width.
func (c CardCallRow) View(width int) string {
	r := c.Row
	lead := strings.Repeat(" ", cardCallRail) + r.railCell() + r.glyph()
	rightRun := c.right(r)
	if r.Allowed != "" && width-cardMargin-lipgloss.Width(lead)-lipgloss.Width(rightRun)-1 < minTargetWidth {
		// The account of how the call came to be allowed gives way before
		// the subject is squeezed past reading, as it does on every row.
		bare := r
		bare.Allowed = ""
		rightRun = c.right(bare)
	}
	rightW := lipgloss.Width(rightRun)
	room := max(width-cardMargin-lipgloss.Width(lead)-rightW-1, 1)
	// Target already ends in its scope (ActivityRow.Scope is its tail), so
	// the subject is the target whole.
	subject := r.Target
	text := sty.dimmer.Render(Clip(subject, room))
	if c.Line != "" {
		if left := room - lipgloss.Width(subject) - 3; left > 0 {
			text += sty.dim.Render(" · ") + cardLine(Clip(c.Line, left))
		}
	}
	gap := max(width-cardMargin-lipgloss.Width(lead)-lipgloss.Width(text)-rightW, 1)
	line := lead + text + strings.Repeat(" ", gap) + rightRun
	if c.Lit {
		return cardLit(line, cardCallRail, width)
	}
	return onBand(Clip(line, width), width)
}

// right is a call row's right-hand run: what came of the call, then its
// time.
func (c CardCallRow) right(r ActivityRow) string {
	var parts []string
	if out := r.outcomeField(); out != "" {
		parts = append(parts, out)
	}
	if r.Duration != "" && r.Duration != NoDuration {
		parts = append(parts, sty.dim.Render(r.Duration))
	}
	return strings.Join(parts, sty.dim.Render(" · "))
}

// cardLine paints a call's one line: an edit's marker in the diff's token
// and the code after it dimmer, as the footer draws a hunk head; anything
// else dimmer.
func cardLine(line string) string {
	if rest, ok := strings.CutPrefix(line, "+ "); ok {
		return sty.add.Render("+ ") + sty.dimmer.Render(rest)
	}
	if rest, ok := strings.CutPrefix(line, "- "); ok {
		return sty.del.Render("- ") + sty.dimmer.Render(rest)
	}
	return sty.dimmer.Render(line)
}

// CardCallBody lays the lines of a call's open body on the band, held in
// from the body column so they hang under the call's subject.
func CardCallBody(lines []string, width int) []string {
	const hang = cardCallRail + 3 - detailIndent
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, onBand(Clip(strings.Repeat(" ", hang)+l, width), width))
	}
	return out
}

// CardCallBodyWidth is the width a call's body is drawn at so that, held in
// by CardCallBody, it ends where the card's rows do.
func CardCallBodyWidth(width int) int {
	return max(width-(cardCallRail+3-detailIndent)-cardMargin, 1)
}

// CardStrip is the strip row under an open card's groups: `in order`, one
// glyph per call in the order the calls were made, and how many there were.
// It prints no key: the hint bar names the keys that walk it while the
// cursor is on it.
type CardStrip struct {
	Cells []StripCell
	// Cursor is the call the strip's cursor is on, lit on the focus ground,
	// or -1 where the cursor is not on the strip.
	Cursor int
}

// stripWords leads the strip where the pane has room for them.
const stripWords = "in order "

// stripLayout is where the strip's cells fall on the row: whether the words
// lead them, which cell is the first shown, and whether a … stands for the
// calls before it. Where the pane cannot hold every call the strip keeps the
// latest, as the footer's does, and moves its window back to keep a cursor
// that walked off its left end in view.
func (s CardStrip) stripLayout(width int) (words bool, first int, more bool) {
	room := width - cardMargin - CardBodyIndent - 1 - lipgloss.Width(s.count())
	n := len(s.Cells)
	switch {
	case n+lipgloss.Width(stripWords) <= room:
		return true, 0, false
	case n <= room:
		return false, 0, false
	}
	shown := max(room-1, 1)
	first = n - shown
	if s.Cursor >= 0 && s.Cursor < first {
		first = s.Cursor
	}
	return false, first, true
}

// count is the strip's right-hand fact: how many calls it holds.
func (s CardStrip) count() string {
	if len(s.Cells) == 1 {
		return "1 tool"
	}
	return strconv.Itoa(len(s.Cells)) + " tools"
}

// View draws the strip at the pane's width.
func (s CardStrip) View(width int) string {
	words, first, more := s.stripLayout(width)
	var b strings.Builder
	b.WriteString(strings.Repeat(" ", CardBodyIndent))
	if words {
		b.WriteString(sty.dim.Render(stripWords))
	}
	if more {
		b.WriteString(sty.dim.Render("…"))
	}
	room := width - cardMargin - CardBodyIndent - 1 - lipgloss.Width(s.count())
	if more {
		room--
	}
	for i := first; i < len(s.Cells) && i-first < room; i++ {
		cell := stripCell(s.Cells[i])
		if i == s.Cursor {
			if bg := backgroundSeq(Palette.FocusBg); bg != "" {
				cell = rearm(cell, bg) + ansiReset
			}
		}
		b.WriteString(cell)
	}
	line := b.String()
	gap := max(width-cardMargin-lipgloss.Width(line)-lipgloss.Width(s.count()), 1)
	return onBand(Clip(line+strings.Repeat(" ", gap)+sty.dim.Render(s.count()), width), width)
}

// CellAt is the call whose glyph stands at column x of the strip's row, so
// a click on a glyph names that call.
func (s CardStrip) CellAt(width, x int) (int, bool) {
	words, first, more := s.stripLayout(width)
	at := CardBodyIndent
	if words {
		at += lipgloss.Width(stripWords)
	}
	if more {
		at++
	}
	i := first + x - at
	if x < at || i >= len(s.Cells) {
		return 0, false
	}
	return i, true
}

// cardLit is a line inside an open card under a cursor: the ❯ in the
// transcript's one pointer column, the band up to where the line's own marks
// begin at from, and the line from there on the focus ground with its marks
// keeping their colours, as a lit row anywhere does. The pointer stands
// where it stands on every other row, rather than beside the call, so the
// eye finds the cursor in one column whatever it is on.
// See docs/interface/departures.md#an-open-cards-cursor-keeps-the-pointer-column.
func cardLit(line string, from, width int) string {
	head := sty.focusPointer.Render("❯") + strings.Repeat(" ", max(from-1, 0))
	if bg := backgroundSeq(cardBand()); bg != "" {
		head = rearm(head, bg) + ansiReset
	}
	rest := ansi.TruncateLeft(Clip(line, width), from, "")
	return head + litRowKeeping(rest, 0, -1, max(width-from, 0))
}
