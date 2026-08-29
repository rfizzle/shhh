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
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// keys.Reading.Collapse is the explicit half of [enter]'s toggle. It is
// offered only while the row under the cursor is actually open, so every key
// on the bar is one the surface can honour — an offer nothing accepts is
// worse than no offer at all.

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
		return sty.Hint.Dim.Render("[" + s.key + "] " + s.label + " — " + s.reason)
	}
	key := sty.Hint.Key
	if s.safe {
		key = sty.Hint.Safe
	}
	return key.Render("["+s.key+"]") + sty.Hint.Dim.Render(" "+s.label)
}

// joinSegs renders a run of segments with the interpunct the whole product
// separates offers with.
func joinSegs(segs []hintSeg) string {
	parts := make([]string, 0, len(segs))
	for _, s := range segs {
		parts = append(parts, s.render())
	}
	return strings.Join(parts, sty.Hint.Dim.Render(" · "))
}

// readingModeKeys are the mode's own keys in the artboard's order: move,
// expand, the step's detail, collapse once the row under the cursor is open,
// and the way back to the prompt. On a transcript with nothing to select,
// [enter] stays on the bar in grey with its reason beside it rather than
// disappearing.
//
// [ctrl+o] is the one key on the bar with no mnemonic behind it (§7a), which
// is exactly why it is written here: the bar is where a chord is learned, and
// a chord nobody names is a chord nobody presses.
func (m Model) readingModeKeys() []hintSeg {
	segs := []hintSeg{seg(keys.Reading.Move)}
	switch {
	case m.focusIdx < 0:
		expand := seg(keys.Reading.Expand)
		expand.reason = "nothing on this row expands"
		segs = append(segs, expand)
	default:
		segs = append(segs, seg(keys.Reading.Expand))
		segs = append(segs, m.detailKeySeg())
		if m.focusedRowOpen() {
			segs = append(segs, seg(keys.Reading.Collapse))
		}
	}
	// The register's own key sits between the row's offers and the way out:
	// it is the last thing a reader reaches for and the first the bar sheds
	// (§7d).
	segs = append(segs, seg(keys.Reading.List))
	return append(segs, seg(keys.Reading.Back))
}

// seg is a binding as one segment of the bar: the register's spelling and the
// register's words, so the bar cannot offer a key the dispatch does not
// answer (§7d).
func seg(b keys.Binding) hintSeg {
	return hintSeg{key: keys.Shown(b), label: keys.Words(b)}
}

// detailKeySeg is [ctrl+o] in its three readings: the step under the cursor
// is open and the key closes it, it is closed and the key opens it, or the
// cursor is not in a step at all — which is said in words on the bar rather
// than by the key quietly doing nothing (S-137, §13d).
func (m Model) detailKeySeg() hintSeg {
	es := *m.entries()
	g, ok := m.stepAt(es, m.focusIdx)
	if !ok {
		s := seg(keys.Reading.Detail)
		s.reason = "this row is not in a step"
		return s
	}
	if m.stepDetailOpen(g, es) {
		s := seg(keys.Reading.Detail)
		s.label = "close the detail"
		return s
	}
	return seg(keys.Reading.Detail)
}

// shortenBackKey is the first thing the key line gives up as the terminal
// narrows: the words around the key, never the key.
func shortenBackKey(segs []hintSeg) []hintSeg {
	out := append([]hintSeg(nil), segs...)
	for i := range out {
		if out[i].key == keys.Shown(keys.Reading.Back) {
			out[i].label = "prompt"
		}
	}
	return out
}

// dropKeyListKey is the very first thing the bar gives up (§7d). `[?]` is the
// only offer here that acts on nothing in the transcript at all, and the one
// a reader who loses it can still find — /help names it, and the supporting
// TUIs have taught the same key for four screens. A key that explains the
// keys goes before any key that does something.
func dropKeyListKey(segs []hintSeg) []hintSeg {
	return withoutSeg(segs, keys.Shown(keys.Reading.List))
}

// dropDetailKey is the second thing the key line gives up, before any key
// shortens its words (S-137). It is the only offer on the bar that acts past
// the row under the cursor, and the only one with a home outside this mode —
// the draft answers the same chord, and /help and the start screen both name
// it — so it is the one key here a reader can lose and still find.
func dropDetailKey(segs []hintSeg) []hintSeg {
	return withoutSeg(segs, keys.Shown(keys.Reading.Detail))
}

// withoutSeg is a bar with one key shed. Shedding is whole-segment: nothing
// on a key row is ever truncated (invariant 4).
func withoutSeg(segs []hintSeg, key string) []hintSeg {
	out := make([]hintSeg, 0, len(segs))
	for _, s := range segs {
		if s.key == key {
			continue
		}
		out = append(out, s)
	}
	return out
}

// dropExpandKey is the last: [enter] leaves whole rather than clipping.
func dropExpandKey(segs []hintSeg) []hintSeg {
	return withoutSeg(segs, keys.Shown(keys.Reading.Expand))
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
// 0 where it is not in one. It asks stepAt, which is the same walk the chord
// that opens that step makes (S-137), so the number on the bar and the step
// ctrl+o acts on are always the same step.
func (m Model) readingStepOrdinal() int {
	es := *m.entries()
	if g, ok := m.stepAt(es, m.focusIdx); ok {
		return g.ordinal
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
		h := m.headerFor(blk, es)
		return !h.Folded || h.Detail
	}
	if m.groupAnchor(es, m.focusIdx) {
		return !m.groupFolded(es[m.focusIdx], m.stepDetailAt(es, m.focusIdx))
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
	segs = append(segs, hintSeg{key: keys.Shown(keys.Select.Cancel), label: "nothing", safe: true})

	rail := sty.Hint.MutationRail.Render("▎")
	lead := rail + sty.Hint.Dim.Render("this row · ")
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
	// The order S-122 settled, with the register key ahead of the detail key
	// and the detail key ahead of the rest: [?] goes, then [ctrl+o], then [q]
	// gives up its words, then [enter] goes whole.
	noList := dropKeyListKey(full)
	noDetail := dropDetailKey(noList)
	short := shortenBackKey(noDetail)
	forms := [][]hintSeg{full, noList, noDetail, short, dropExpandKey(short)}
	positions := m.readingPositionFields()
	for _, form := range forms {
		left := joinSegs(form)
		lw := lipgloss.Width(left)
		for _, pos := range positions {
			if gap := width - lw - lipgloss.Width(pos); gap >= 2 {
				return left + strings.Repeat(" ", gap) + sty.Hint.Dim.Render(pos)
			}
		}
	}
	for _, form := range forms {
		if left := joinSegs(form); lipgloss.Width(left) <= width {
			return left
		}
	}
	return clipRow(joinSegs(forms[len(forms)-1]), width)
}

// hintStyles is the reading-mode hint line's own group (§7a), with the
// mutation rail (§14) that shares its file.
type hintStyles struct {
	Key          lipgloss.Style
	Safe         lipgloss.Style
	Dim          lipgloss.Style
	MutationRail lipgloss.Style
}

func newHintStyles(p components.ColorTokens) hintStyles {
	return hintStyles{
		Key:          lipgloss.NewStyle().Foreground(p.Info),
		Safe:         lipgloss.NewStyle().Foreground(p.Add),
		Dim:          lipgloss.NewStyle().Foreground(p.Dim),
		MutationRail: lipgloss.NewStyle().Foreground(p.Accent),
	}
}

// readingKeyListLines is what `[?]` puts where the compact bar was (S-153,
// §7d): the mode's whole register, one key per line, then the offers the row
// under the cursor makes, then the key that puts it away again.
//
// It is the supporting TUIs' answer to the same question (§19), moved onto
// the one chat surface that can hold a bare letter — the compact row swapped
// for the full list, in place, and swapped back by the same key. Nothing here
// is a second vocabulary: the words are the register's, and what the list
// adds over the bar is completeness, not longer prose. The bar sheds keys as
// the terminal narrows and never says which; this is where they went.
//
// The panel is bounded like every other one (§1: 40% of the screen). What
// does not fit is counted on a final row rather than dropped silently
// (invariant 4) — and the count is honest about which end it came from,
// because the keys a reader is most likely to be looking for are the ones
// the bar had already dropped.
func (m Model) readingKeyListLines(width, bound int) []string {
	var segs []hintSeg
	for _, b := range keys.Reading.All() {
		s := seg(b)
		if s.key == keys.Shown(keys.Reading.List) {
			// The same key again, saying what it does now.
			s.label = "hide the keys"
		}
		segs = append(segs, s)
	}
	lines := make([]string, 0, len(segs)+len(m.readingRowOffers())+1)
	for _, s := range segs {
		lines = append(lines, clipRow(s.render(), width))
	}
	// The row's own offers keep the rail that says they are the row's, the
	// way the compact bar's second line does. A row that offers nothing adds
	// no rows: nothing has to say "this row has nothing to offer".
	rail := sty.Hint.MutationRail.Render("▎")
	for _, o := range m.readingRowOffers() {
		s := hintSeg{key: strings.Trim(o.Key, "[]"), label: o.Label}
		lines = append(lines, clipRow(rail+s.render(), width))
	}
	if len(lines) > bound {
		keep := max(bound-1, 1)
		lines = append(lines[:keep:keep],
			clipRow(sty.Hint.Dim.Render(fmt.Sprintf("… %d more keys — the terminal is too short for the list",
				len(lines)-keep)), width))
	}
	return lines
}
