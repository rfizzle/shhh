package chat

// The hint bar reading mode puts where the input frame was (
// docs/interface/surfaces.md#reading-mode). It replaces the frame rather than
// sitting under it: two bottom elements is how you get a session where nobody
// can tell which one enter belongs to, which is also why the frame goes
// rather than dims.
//
// It is one line of the mode's own keys with the position on the right, and —
// when the row under the cursor offers keys of its own — a second line
// prefixed by that row's ▎, so a key that acts on one row never reads as a
// key that acts on the session.

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
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

// render paints one segment: the key in info as every offered key is,
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
// expand where the row under the cursor opens (with collapse once it is
// open), copy where the row holds something a clipboard can carry, and the
// way back to the prompt. With no cursor at all — nothing selectable —
// [enter] stays on the bar in grey with its reason rather than disappearing;
// with a cursor on a row that merely does not expand, the row's real offers
// stand where it would have, the way [-] and the row keys come and go.
func (m Model) readingModeKeys() []hintSeg {
	segs := []hintSeg{seg(keys.Reading.Move)}
	switch {
	case m.focusIdx < 0:
		expand := seg(keys.Reading.Expand)
		expand.reason = "nothing on this row expands"
		segs = append(segs, expand)
	case m.focusedExpands():
		segs = append(segs, seg(keys.Reading.Expand))
		if m.focusedRowOpen() {
			segs = append(segs, seg(keys.Reading.Collapse))
		}
	}
	// Like [-], [y] is offered only while the row under the cursor can
	// honour it — an offer nothing accepts is worse than no offer at all.
	if m.focusedCopyable() {
		segs = append(segs, seg(keys.Reading.Copy))
	}
	// [/] stands whether or not a search is open: it opens the query row, or
	// reopens it on the query already in it. The step pair joins it only once
	// there is something to step through, the way [-] joins once a row is
	// open.
	segs = append(segs, seg(keys.Reading.Search))
	if m.viewport.Searching() {
		segs = append(segs, seg(keys.Reading.Match))
	}
	// The register's own key sits between the row's offers and the way out:
	// it is the last thing a reader reaches for and the first the bar sheds.
	segs = append(segs, seg(keys.Reading.List))
	return append(segs, seg(keys.Reading.Back))
}

// focusedExpands reports whether [enter] would open anything on the row
// under the cursor: a step header's fold, a group's, or a row with a body of
// its own. The bar reads it so the key it offers is one the row will honour.
func (m Model) focusedExpands() bool {
	es := *m.entries()
	if m.focusIdx < 0 || m.focusIdx >= len(es) {
		return false
	}
	if _, ok := m.stepBlockAt(es, m.focusIdx); ok {
		return true
	}
	if m.groupAnchor(es, m.focusIdx) {
		return true
	}
	return expandable(es[m.focusIdx])
}

// seg is a binding as one segment of the bar: the register's spelling and the
// register's words, so the bar cannot offer a key the dispatch does not
// answer.
func seg(b keys.Binding) hintSeg {
	return hintSeg{key: keys.Shown(b), label: keys.Words(b)}
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

// dropKeyListKey is the very first thing the bar gives up. `[?]` is the
// only offer here that acts on nothing in the transcript at all, and the one
// a reader who loses it can still find — /help names it, and the supporting
// TUIs have taught the same key for four screens. A key that explains the
// keys goes before any key that does something.
func dropKeyListKey(segs []hintSeg) []hintSeg {
	return withoutSeg(segs, keys.Shown(keys.Reading.List))
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

// dropCopyKey goes after the register's: a row offer the reader can still
// find in `?`, shed before the mode's own movement and exits.
func dropCopyKey(segs []hintSeg) []hintSeg {
	return withoutSeg(segs, keys.Shown(keys.Reading.Copy))
}

// dropSearchKey goes before the copy, because the two are different kinds of
// offer: [y] acts on the row the reader is already standing on, and [/] opens
// a surface they have not asked for yet. What is on screen wins the column.
func dropSearchKey(segs []hintSeg) []hintSeg {
	return withoutSeg(segs, keys.Shown(keys.Reading.Search))
}

// dropMatchKey is the last of the search's, and it outlasts both: the pair is
// only on the bar while a search is live, and the key that walks what the
// reader is already reading is worth more than the key that would start
// another one.
func dropMatchKey(segs []hintSeg) []hintSeg {
	return withoutSeg(segs, keys.Shown(keys.Reading.Match))
}

// dropExpandKey is the last: [enter] leaves whole rather than clipping.
func dropExpandKey(segs []hintSeg) []hintSeg {
	return withoutSeg(segs, keys.Shown(keys.Reading.Expand))
}

// readingPositionFields is the right-hand field in its forms, widest first.
// It is the position of the cursor among the rows — or, once rows are open,
// how many are, and just after a [y], what the copy caught (copyrow.go),
// which is the one moment that fact outranks the position. Prose
// has no addressable rows to count, so it reports nothing.
func (m Model) readingPositionFields() []string {
	if m.readingCopied != "" {
		return []string{m.readingCopied}
	}
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
// 0 where it is not in one. It asks stepAt, which is the same walk /step
// makes, so the number on the bar and the step
// /step acts on are always the same step.
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
		case e.kind == entryThink:
			// The reader's own depth, not the one the verbosity is imposing:
			// this count is what [-] acts on (think.go).
			if e.thinkDepth == thinkTail || e.thinkDepth == thinkFull {
				n++
			}
		case e.expanded:
			n++
		}
	}
	return n
}

// openKind is one way a transcript row can be showing more than its own line.
// It is a closed vocabulary rather than a condition spelled out at each call
// site (docs/interface/principles.md#closed-vocabularies), because two
// gestures act on it and they must not learn different lists: reading mode's
// [-] closes whichever one the cursor is standing on, and esc on an empty
// draft puts every one the reader opened back at once.
//
// The two gestures ask different questions of the same list. [-] asks what is
// open under the cursor, whatever opened it, because the reader is pointing
// at that row and asking for it closed; esc asks what the *reader* opened, so
// a body the verbosity is holding open is not its to fold
// (docs/interface/surfaces.md#the-input-frame).
type openKind int

const (
	// openNone is a row showing nothing but itself.
	openNone openKind = iota
	// openStep is a step showing its rows rather than its header alone.
	openStep
	// openDetail is those rows showing their detail bodies — what /step
	// opens. It is a kind of its own rather than a degree of openStep because
	// the two are answered separately: closing a step's header leaves its
	// detail answer standing, and closing the detail leaves the step open
	// (steps.go, detail.go).
	openDetail
	// openGroup is a folded run of read-only calls given back row by row.
	openGroup
	// openDiff is a diff showing its hunks inside the transcript.
	openDiff
	// openThink is a reasoning row showing part or all of its block.
	openThink
	// openBody is any other row showing what its call returned.
	openBody
)

// focusedOpenKind is what the row under the cursor has open, or openNone.
// Anything showing counts, the verbosity's doing included: [-] is the reader
// naming one row.
func (m Model) focusedOpenKind() openKind {
	es := *m.entries()
	if m.focusIdx < 0 || m.focusIdx >= len(es) {
		return openNone
	}
	if blk, ok := m.stepBlockAt(es, m.focusIdx); ok {
		h := m.headerFor(blk, es)
		switch {
		case !h.Folded:
			return openStep
		case h.Detail:
			// A header folded over an open detail: the rows are not on
			// screen, but the answer that would show their bodies is still
			// on record, and [-] answers both with the one toggle below.
			return openDetail
		}
		return openNone
	}
	if m.groupAnchor(es, m.focusIdx) {
		if m.groupFolded(es[m.focusIdx], m.stepDetailAt(es, m.focusIdx)) {
			return openNone
		}
		return openGroup
	}
	if d := es[m.focusIdx].diff; d != nil {
		if d.Mode == components.DiffCollapsed {
			return openNone
		}
		return openDiff
	}
	if es[m.focusIdx].kind == entryThink {
		// The reader's own depth, like every other row here: a row the
		// verbosity opened is not a row [-] has anything to close.
		if d := es[m.focusIdx].thinkDepth; d == thinkTail || d == thinkFull {
			return openThink
		}
		return openNone
	}
	if es[m.focusIdx].expanded {
		return openBody
	}
	return openNone
}

// focusedRowOpen reports whether the row under the cursor is showing more
// than its own line — an expanded body, an unfolded step, or a group whose
// rows are back.
func (m Model) focusedRowOpen() bool { return m.focusedOpenKind() != openNone }

// collapseFocused closes whatever the row under the cursor has open, and
// reports whether there was anything to close. Where there is not, [-] is a
// character like any other and belongs in the draft.
//
// It writes the closed answer where the whole-pane fold writes the resting
// one, and that is the difference between the two gestures: the reader
// pointed at this row and said no, and a no a setting can talk out of is not
// an answer (steps.go, detail.go).
func (m *Model) collapseFocused() bool {
	kind := m.focusedOpenKind()
	if kind == openNone {
		return false
	}
	es := *m.entries()
	switch kind {
	case openStep, openDetail:
		m.toggleStepFold(m.focusIdx)
	case openGroup:
		m.toggleGroupFold(m.focusIdx)
	case openDiff:
		es[m.focusIdx].diff.Mode = components.DiffCollapsed
	case openThink:
		es[m.focusIdx].thinkDepth = thinkClosed
	default:
		es[m.focusIdx].expanded = false
	}
	// The row renders differently now, and it may be in a block both caches
	// have already frozen (render.go, focus.go). A cursor that then moves
	// off it would be handed the lines from before the collapse, and the row
	// would spring open again behind the reader.
	m.invalidateRenderCache()
	return true
}

// readerOpened is what the reader's own gestures have on record for one row:
// the openKinds it is showing because somebody asked for them, never the ones
// a setting is holding open. It reads the entry's own overrides and nothing
// else, which is what makes that distinction possible — the three fold
// overrides and the think depth each have a value meaning "no answer of
// mine", and no setting can write one (steps.go, detail.go, think.go).
func readerOpened(e entry) []openKind {
	var kinds []openKind
	// The two step overrides are read and put back one at a time, even
	// though /step writes both at once: a reader who then folds the header
	// has said foldClosed on one of them and nothing on the other, and a
	// fold that treated them as one answer would overwrite that no.
	// Which also means a detail answer standing behind a folded header is
	// still put back — nothing on screen reports it, and an answer nothing
	// reports is one that springs the bodies open the next time the header
	// opens.
	if e.stepFold == foldOpen {
		kinds = append(kinds, openStep)
	}
	if e.detailFold == foldOpen {
		kinds = append(kinds, openDetail)
	}
	if e.groupFold == foldOpen {
		kinds = append(kinds, openGroup)
	}
	// DiffExpanded, and not "anything but collapsed": the full-screen view is
	// a surface of its own, and esc there steps back to the expanded form
	// long before it can reach the draft (components/diff.go).
	if e.diff != nil && e.diff.Mode == components.DiffExpanded {
		kinds = append(kinds, openDiff)
	}
	if e.thinkDepth == thinkTail || e.thinkDepth == thinkFull {
		kinds = append(kinds, openThink)
	}
	if e.expanded {
		kinds = append(kinds, openBody)
	}
	return kinds
}

// restOpen puts one of those answers back to the value that means "no answer
// of mine on record" — foldAuto for the three overrides, thinkAuto for the
// think row — so a row is folded to what the setting says and never past it.
// Writing foldClosed instead would make `/ui verbosity high` quietly stop
// meaning what it says, and a reader who ran it afterwards and saw nothing
// open would have been lied to.
func restOpen(e *entry, k openKind) {
	switch k {
	case openStep:
		e.stepFold = foldAuto
	case openDetail:
		e.detailFold = foldAuto
	case openGroup:
		e.groupFold = foldAuto
	case openDiff:
		e.diff.Mode = components.DiffCollapsed
	case openThink:
		e.thinkDepth = thinkAuto
	case openBody:
		e.expanded = false
	}
}

// readerOpenedARow reports whether there is anything for the fold to do. It
// is asked before the pane is measured, because measuring is a walk over the
// whole transcript (render.go) and esc on an empty draft is the most reflexive
// key on this surface: the press that folds nothing must cost nothing.
func (m Model) readerOpenedARow() bool {
	for _, e := range *m.entries() {
		if len(readerOpened(e)) > 0 {
			return true
		}
	}
	return false
}

// foldOpenedRows puts every row the reader opened back to its resting state
// and reports how many rows it folded. Rows rather than answers: /step leaves
// two overrides on one entry, and the reader opened one thing.
func (m *Model) foldOpenedRows() int {
	es := *m.entries()
	n := 0
	for i := range es {
		kinds := readerOpened(es[i])
		if len(kinds) == 0 {
			continue
		}
		for _, k := range kinds {
			restOpen(&es[i], k)
		}
		n++
	}
	if n > 0 {
		// The same reason one collapse has above: rows inside a frozen block
		// render differently now, and a cache nobody dropped gives them back.
		m.invalidateRenderCache()
	}
	return n
}

// settingHoldsRowsOpen reports whether the verbosity is what is putting
// bodies on the screen. It is asked only where the fold found nothing of the
// reader's, because that is the one moment the answer is worth a rail: the
// pane is full of open rows, esc folded none of them, and the reader is owed
// the reason rather than a key that looks broken (invariant 4).
//
// It walks the blocks rather than the entries, the way the render does
// (steps.go, blockUnits): a row inside a step the reader folded shut is not
// on the screen at all, and crediting the setting for it would put the notice
// on a pane where nothing is open.
func (m Model) settingHoldsRowsOpen() bool {
	if m.verbosity != verbosityHigh {
		return false
	}
	es := *m.entries()
	for _, blk := range m.blocksOf(es) {
		if blk.step == nil {
			if m.settingShowsBody(es[blk.start]) {
				return true
			}
			continue
		}
		if h := m.headerFor(blk, es); h.Folded || blk.step.queued() {
			continue
		}
		for _, sl := range m.stepSlots(es, blk.step) {
			// A folded run is one counted row and no bodies.
			if sl.group {
				continue
			}
			if m.settingShowsBody(es[sl.idx]) {
				return true
			}
		}
	}
	return false
}

// settingShowsBody reports whether this row is showing a body only because
// the verbosity says so — the caller has already established that it does.
func (m Model) settingShowsBody(e entry) bool {
	if e.kind == entryThink {
		return e.thinkDepth == thinkAuto && strings.TrimSpace(e.text) != ""
	}
	return !e.expanded && strings.TrimSpace(e.toolResult) != ""
}

// foldedNotice is what the fold says it did, counted the way the rest of the
// pane's folds count. A fold that silently rearranged the screen would be a
// jump the reader has to explain to themselves
// (docs/interface/principles.md#fold-never-hide).
func foldedNotice(n int) string { return "folded " + plural(n, "row") }

// verbosityHoldsNotice is what the rail says when esc found nothing of the
// reader's to fold and every open row is the setting's. It names the door
// rather than reporting the refusal (invariant 4).
const verbosityHoldsNotice = "/ui verbosity opened these rows"

// readingRowOffers are the keys the row under the cursor offers, read off the
// row itself so the bar and the row cannot drift apart.
func (m Model) readingRowOffers() []components.KeyOffer {
	if e, ok := m.focusedRoundPause(); ok {
		return roundPauseOffers(e.pause)
	}
	if e, ok := m.focusedClose(); ok {
		// A close is one row to the cursor and several rows on the screen,
		// so the bar carries what all of them offer: the changeset's keys
		// and the checks row's, in the order they are drawn.
		var offers []components.KeyOffer
		if e.close.Changes != nil {
			offers = append(offers, e.close.Changes.Keys...)
		}
		if e.close.Checks != nil {
			offers = append(offers, e.close.Checks.Keys...)
		}
		return offers
	}
	if e, ok := m.focusedDrop(); ok {
		return m.dropKeys(e.resume)
	}
	if e, ok := m.focusedFailure(); ok {
		return m.failureKeys(e.fail)
	}
	if e, ok := m.focusedSteerNotice(); ok {
		return m.steerOffers(e)
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
// least.
func (m Model) readingKeyLine(width int) string {
	full := m.readingModeKeys()
	// The settled order: [?] goes first, then [q] gives up its words, then
	// [/], then [y], then [n/N], then [enter] goes whole.
	noList := dropKeyListKey(full)
	short := shortenBackKey(noList)
	noSearch := dropSearchKey(short)
	noCopy := dropCopyKey(noSearch)
	noMatch := dropMatchKey(noCopy)
	forms := [][]hintSeg{full, noList, short, noSearch, noCopy, noMatch,
		dropExpandKey(noMatch)}
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

// readingKeyListLines is what `[?]` puts where the compact bar was (
// the key register): the mode's whole register, one key per line, then the
// offers the row under the cursor makes, then the key that puts it away
// again.
//
// It is the supporting TUIs' answer to the same question, moved onto
// the one chat surface that can hold a bare letter — the compact row swapped
// for the full list, in place, and swapped back by the same key. Nothing here
// is a second vocabulary: the words are the register's, and what the list
// adds over the bar is completeness, not longer prose. The bar sheds keys as
// the terminal narrows and never says which; this is where they went.
//
// The panel is bounded like every other one (40% of the screen). What
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
