package components

// The flat line (docs/interface/surfaces.md#the-leading-columns): what
// happened to the session rather than in it — the tree moved, the window
// was trimmed or compacted, a conversation was reopened — and the round
// writing a call that has not landed yet. Each is one dim line at the glyph
// column a card puts its glyph in, its mark in that slot and its words where
// a card's verb starts, on bare screen: no band, because it is not a step,
// and no key, because the hint bar carries what enter does
// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// NoticeMark is a notice's mark: the session speaking about itself. A dot
// rather than a kind's glyph, because a notice reports no act.
const NoticeMark = "·"

// NoticeLine is one flat line. Mark is the glyph-slot mark, NoticeMark where
// it is left empty; Text is what it says, dim.
type NoticeLine struct {
	Mark string
	Text string
}

// View renders the line. Text too long for the pane wraps under its own
// first word rather than under the mark, so the mark stays the one thing in
// its column.
func (n NoticeLine) View(width int) string {
	mark := n.Mark
	if mark == "" {
		mark = NoticeMark
	}
	lead := closeLead("", sty.Dim.Render(mark))
	under := strings.Repeat(" ", lipgloss.Width(lead))
	inner := max(width-lipgloss.Width(lead), 1)
	text := strings.TrimSpace(n.Text)
	// A separator binds to the word before it, so a wrapped line ends on
	// `·` rather than the next one starting with it — a line that opened
	// with the mark would read as a notice of its own.
	// See docs/interface/departures.md#a-notices-words-where-the-catalogue-drew-none.
	const nbsp = "\u00a0"
	bound := strings.ReplaceAll(text, " · ", nbsp+"· ")
	var lines []string
	for i, l := range strings.Split(lipgloss.Wrap(bound, inner, ""), "\n") {
		head := under
		if i == 0 {
			head = lead
		}
		l = strings.ReplaceAll(strings.TrimRight(l, " "), nbsp, " ")
		lines = append(lines, head+sty.Dim.Render(Clip(l, inner)))
	}
	return strings.Join(lines, "\n")
}
