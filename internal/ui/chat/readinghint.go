package chat

// The hint bar reading mode puts where the input frame was (S-122,
// DESIGN-TUI.md §7a). It replaces the frame rather than sitting under it:
// two bottom elements is how you get a session where nobody can tell which
// one enter belongs to, which is also why the frame goes rather than dims.
//
// It is one line of the mode's own keys with the position on the right, and —
// when the row under the cursor offers keys of its own — a second line
// prefixed by that row's ▎, so a key that acts on one row never reads as a
// key that acts on the session.

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// collapseKey is the explicit half of [enter]'s toggle. It is offered only
// while the row under the cursor is actually open, so every key on the bar is
// one the surface can honour — an offer nothing accepts is worse than no
// offer at all.
const collapseKey = "-"

// hintSeg is one offer on the hint bar: a key, what it does, and — for a key
// that is on screen but cannot act — the reason, which is said in words
// rather than left to the colour (invariant 1).
type hintSeg struct {
	key    string
	label  string
	reason string
	safe   bool
}

// render paints one segment: the key in info as every offered key is (§10a),
// its imperative in dim, and the safe answer in add where there is one
// (invariant 3).
func (s hintSeg) render() string {
	if s.reason != "" {
		return hintDimStyle.Render("[" + s.key + "] " + s.label + " — " + s.reason)
	}
	key := hintKeyStyle
	if s.safe {
		key = hintSafeStyle
	}
	return key.Render("["+s.key+"]") + hintDimStyle.Render(" "+s.label)
}

// joinSegs renders a run of segments with the interpunct the whole product
// separates offers with.
func joinSegs(segs []hintSeg) string {
	parts := make([]string, 0, len(segs))
	for _, s := range segs {
		parts = append(parts, s.render())
	}
	return strings.Join(parts, hintDimStyle.Render(" · "))
}

// readingModeKeys are the mode's own keys in the artboard's order: move,
// expand, collapse once the row under the cursor is open, and the way back to
// the prompt. On a transcript with nothing to select, [enter] stays on the bar
// in grey with its reason beside it rather than disappearing.
func (m Model) readingModeKeys() []hintSeg {
	segs := []hintSeg{{key: "j/k", label: "move"}}
	switch {
	case m.focusIdx < 0:
		segs = append(segs, hintSeg{key: "enter", label: "expand",
			reason: "nothing on this row expands"})
	default:
		segs = append(segs, hintSeg{key: "enter", label: "expand"})
		if m.focusedRowOpen() {
			segs = append(segs, hintSeg{key: collapseKey, label: "collapse"})
		}
	}
	return append(segs, hintSeg{key: "q", label: "back to the prompt"})
}

// shortenBackKey is the first thing the key line gives up as the terminal
// narrows: the words around the key, never the key.
func shortenBackKey(segs []hintSeg) []hintSeg {
	out := append([]hintSeg(nil), segs...)
	for i := range out {
		if out[i].key == "q" {
			out[i].label = "prompt"
		}
	}
	return out
}

// dropExpandKey is the second: [enter] leaves whole rather than clipping.
func dropExpandKey(segs []hintSeg) []hintSeg {
	out := make([]hintSeg, 0, len(segs))
	for _, s := range segs {
		if s.key == "enter" {
			continue
		}
		out = append(out, s)
	}
	return out
}

// readingPositionFields is the right-hand field in its forms, widest first.
// It is the position of the cursor among the rows — or, once rows are open,
// how many are, which is the fact the reader is actually holding then. Prose
// has no addressable rows to count, so it reports nothing (§7a).
func (m Model) readingPositionFields() []string {
	if n := m.expandedRowCount(); n > 0 {
		return []string{plural(n, "row") + " expanded", fmt.Sprintf("%d expanded", n)}
	}
	pos, total := m.readingPosition()
	if pos == 0 || total == 0 {
		return nil
	}
	fields := []string{
		fmt.Sprintf("row %d of %d", pos, total),
		fmt.Sprintf("%d of %d", pos, total),
		fmt.Sprintf("%d/%d", pos, total),
	}
	if ord := m.readingStepOrdinal(); ord > 0 {
		fields = append([]string{fmt.Sprintf("row %d of %d · step %d", pos, total, ord)}, fields...)
	}
	return fields
}

// readingStepOrdinal is the number of the step the cursor is standing in, or
// 0 where it is not in one.
func (m Model) readingStepOrdinal() int {
	if m.focusIdx < 0 {
		return 0
	}
	es := *m.entries()
	for _, blk := range m.blocksOf(es) {
		if blk.step == nil || blk.step.queued() {
			continue
		}
		start, end := blk.members()
		if m.focusIdx == blk.step.titleIdx || (m.focusIdx >= start && m.focusIdx < end) {
			return blk.step.ordinal
		}
	}
	return 0
}

// expandedRowCount is how many rows the reader has opened — the count the
// position field reports once there is one, and the reason [-] is on the bar.
func (m Model) expandedRowCount() int {
	n := 0
	for _, e := range *m.entries() {
		switch {
		case e.diff != nil:
			if e.diff.Mode != components.DiffCollapsed {
				n++
			}
		case e.expanded:
			n++
		}
	}
	return n
}

// focusedRowOpen reports whether the row under the cursor is showing more
// than its own line — an expanded body, an unfolded step, or a group whose
// rows are back.
func (m Model) focusedRowOpen() bool {
	es := *m.entries()
	if m.focusIdx < 0 || m.focusIdx >= len(es) {
		return false
	}
	if blk, ok := m.stepBlockAt(es, m.focusIdx); ok {
		return !m.headerFor(blk, es).Folded
	}
	if m.groupAnchor(es, m.focusIdx) {
		return !m.groupFolded(es[m.focusIdx])
	}
	if d := es[m.focusIdx].diff; d != nil {
		return d.Mode != components.DiffCollapsed
	}
	return es[m.focusIdx].expanded
}

// collapseFocused closes whatever the row under the cursor has open, and
// reports whether there was anything to close. Where there is not, [-] is a
// character like any other and belongs in the draft (S-115).
func (m *Model) collapseFocused() bool {
	if !m.focusedRowOpen() {
		return false
	}
	es := *m.entries()
	switch {
	case func() bool { _, ok := m.stepBlockAt(es, m.focusIdx); return ok }():
		m.toggleStepFold(m.focusIdx)
	case m.groupAnchor(es, m.focusIdx):
		m.toggleGroupFold(m.focusIdx)
	case es[m.focusIdx].diff != nil:
		es[m.focusIdx].diff.Mode = components.DiffCollapsed
	default:
		es[m.focusIdx].expanded = false
	}
	return true
}

// readingRowOffers are the keys the row under the cursor offers, read off the
// row itself so the bar and the row cannot drift apart.
func (m Model) readingRowOffers() []components.KeyOffer {
	if e, ok := m.focusedRoundPause(); ok {
		return roundPauseOffers(e.pause)
	}
	if e, ok := m.focusedClose(); ok && e.close.Changes != nil {
		return e.close.Changes.Keys
	}
	if e, ok := m.focusedDrop(); ok {
		return m.dropKeys(e.resume)
	}
	if e, ok := m.focusedFailure(); ok {
		return m.failureKeys(e.fail)
	}
	return nil
}

// readingRowLines are the rest of the bar: the row's own ▎, the offers it
// makes, and what esc does about them. A row that offers nothing renders no
// line at all — nothing has to say "this row has nothing to offer".
//
// Where the offers do not fit on one line they stack rather than clip: an
// offer folded out of sight is an offer nobody can take (invariant 4). The
// words around the rail go first, since the rail is what says "this row".
func (m Model) readingRowLines(width int, budget int) []string {
	offers := m.readingRowOffers()
	if len(offers) == 0 || budget < 1 {
		return nil
	}
	segs := make([]hintSeg, 0, len(offers)+1)
	for _, o := range offers {
		segs = append(segs, hintSeg{key: strings.Trim(o.Key, "[]"), label: o.Label})
	}
	segs = append(segs, hintSeg{key: "esc", label: "nothing", safe: true})

	rail := mutationRailStyle.Render("▎")
	lead := rail + hintDimStyle.Render("this row · ")
	if line := lead + joinSegs(segs); lipgloss.Width(line) <= width {
		return []string{line}
	}
	if line := rail + joinSegs(segs); lipgloss.Width(line) <= width {
		return []string{line}
	}
	return stackSegs(segs, rail, width, budget)
}

// stackSegs packs segments onto as many lines as the budget allows, each
// carrying the row's rail so a continuation still reads as the row's own. A
// terminal too narrow for even that keeps what fits: the row itself still
// carries its offers, and this bar is where they are named a second time.
func stackSegs(segs []hintSeg, rail string, width, budget int) []string {
	var lines []string
	var cur []hintSeg
	flush := func() {
		if len(cur) > 0 {
			lines = append(lines, clipRow(rail+joinSegs(cur), width))
			cur = nil
		}
	}
	for _, s := range segs {
		next := append(append([]hintSeg(nil), cur...), s)
		if len(cur) > 0 && lipgloss.Width(rail+joinSegs(next)) > width {
			flush()
			cur = []hintSeg{s}
			continue
		}
		cur = next
	}
	flush()
	if len(lines) > budget {
		lines = lines[:budget]
	}
	return lines
}

// readingKeyLine is the first line: the mode's keys with the position on the
// right. The position narrows through its forms before the keys give up any
// of their words, the keys shorten before any of them leaves, and the
// position is dropped altogether only when nothing else is left to give —
// the lit row still says which row it is, which is why dropping it costs
// least (§7a).
func (m Model) readingKeyLine(width int) string {
	full := m.readingModeKeys()
	forms := [][]hintSeg{full, shortenBackKey(full), dropExpandKey(shortenBackKey(full))}
	positions := m.readingPositionFields()
	for _, keys := range forms {
		left := joinSegs(keys)
		lw := lipgloss.Width(left)
		for _, pos := range positions {
			if gap := width - lw - lipgloss.Width(pos); gap >= 2 {
				return left + strings.Repeat(" ", gap) + hintDimStyle.Render(pos)
			}
		}
	}
	for _, keys := range forms {
		if left := joinSegs(keys); lipgloss.Width(left) <= width {
			return left
		}
	}
	return clipRow(joinSegs(forms[len(forms)-1]), width)
}

// applyReadingHintStyles rebuilds this file's styles from the palette.
func applyReadingHintStyles(p components.ColorTokens) {
	hintKeyStyle = lipgloss.NewStyle().Foreground(p.Info)
	hintSafeStyle = lipgloss.NewStyle().Foreground(p.Add)
	hintDimStyle = lipgloss.NewStyle().Foreground(p.Dim)
	mutationRailStyle = lipgloss.NewStyle().Foreground(p.Accent)
}

var (
	hintKeyStyle      lipgloss.Style
	hintSafeStyle     lipgloss.Style
	hintDimStyle      lipgloss.Style
	mutationRailStyle lipgloss.Style
)
