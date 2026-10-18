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
//
// The segment this file builds is also what every other key row the chat
// surface draws for itself is built from — the frame's bottom rail, the one
// line a full-screen viewer leaves where the draft was, the query rows'
// two keys. One composer means one notation and one set of tones: brackets,
// the key in Key, the words in Dim, the safe answer in Add
// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// The two things esc does here, offered by the row under the cursor: it
// collapses an open row, and with nothing open it leaves reading mode.
const (
	collapseWords     = "collapse"
	backToPromptWords = components.EscBack
)

// hintSeg is one offer on the hint bar: the segment every key row in the
// product is built from — a key, what it does, the priority it holds its
// place with, and for a key that is on screen but cannot act, the reason,
// which is said in words rather than left to the colour (invariant 1).
type hintSeg = components.KeyOffer

// hintTones are the chat surface's key rows as the shared renderer paints
// them: the key in Key as every offered key is, its imperative in dim, and
// the safe answer in add (invariant 3). A segment with no key is a fact the
// rail is stating rather than an offer it is making — what is waiting
// elsewhere on the screen — so it is painted the way the frame paints
// anything that is waiting, and the offer that answers it is the segment
// beside it (frame.go).
func hintTones() components.HintTones {
	return components.HintTones{
		Key: sty.Hint.Key, Gloss: sty.Hint.Dim, Sep: sty.Hint.Dim, Safe: sty.Hint.Safe,
		Inert: sty.Hint.Dim, Absent: sty.Hint.Dim, Fact: sty.Frame.WaitingChip,
		Primary: sty.Hint.Key, Danger: sty.Hint.Key, Quiet: sty.Hint.Dim,
	}
}

// paneTones are the same for the one line a full-screen surface leaves in
// the draft's place, where the fact a row states is the name of the screen
// it belongs to and is said in the system's own grey.
func paneTones() components.HintTones {
	t := hintTones()
	t.Fact = sty.SystemMsg
	return t
}

// paneHint is that line's run: the screen's name, then its way out. It is a
// constructor rather than a string so the line is built from segments like
// every other key row and laid out by the one renderer (paint.go).
func paneHint(name string, segs ...hintSeg) []hintSeg {
	return append([]hintSeg{{Label: name}}, segs...)
}

// joinSegs renders a run of segments with the interpunct the whole product
// separates offers with, for a caller that has already decided the run fits.
func joinSegs(segs []hintSeg) string { return hintTones().Join(segs) }

// readingModeKeys are the mode's own keys in the artboard's order: move,
// expand where the row under the cursor opens (with collapse once it is
// open), copy where the row holds something a clipboard can carry, and the
// way back to the prompt.
//
// [enter] is on the bar at every position, and where the row under the
// cursor will not open it is shown absent with the reason beside it —
// `[enter] expand — nothing on this row expands`, the whole segment grey —
// rather than dropped. A key that comes and goes as the cursor walks a pane
// of prose reads as a key that is broken; a key stated absent with its
// reason teaches which rows open and which do not
// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
func (m Model) readingModeKeys() []hintSeg {
	if m.stripLive() {
		// The strip prints no key; while the cursor is on it, the bar is
		// its keys and nothing else (docs/interface/surfaces.md#the-step).
		return m.stripKeys()
	}
	segs := []hintSeg{seg(keys.Reading.Move)}
	if _, _, ok := m.groupAt(*m.entries(), m.focusIdx); ok {
		// A group line folds and unfolds its group.
		words := groupFoldWords
		if (*m.entries())[m.focusIdx].groupFolded {
			words = groupUnfoldWords
		}
		segs = append(segs, segAs(keys.Reading.Expand, words).At(readingGivesExpand))
	} else if _, ok := m.reviewableRow(m.focusIdx); ok {
		// The row states what a turn changed, and enter opens that turn's
		// review rather than a body (openCursorRow).
		segs = append(segs, segAs(keys.Reading.Expand, reviewTurnWords).At(readingGivesExpand))
	} else if m.focusedTrayPicture() {
		// A sent picture's row has no open state: enter opens its card,
		// every time, so the bar says that and never offers to close it
		// (attachments.go).
		segs = append(segs, segAs(keys.Reading.Expand, openTrayWords).At(readingGivesExpand))
	} else if words, ok := m.focusedCardWords(); ok {
		// A card prints no key of its own; the bar says what enter does to
		// the one under the cursor (docs/interface/surfaces.md#the-step).
		segs = append(segs, segAs(keys.Reading.Expand, words).At(readingGivesExpand))
	} else if m.focusedExpands() {
		segs = append(segs, seg(keys.Reading.Expand).At(readingGivesExpand))
	} else {
		expand := seg(keys.Reading.Expand).At(readingGivesAbsent)
		expand.Reason = "nothing on this row expands"
		segs = append(segs, expand)
	}
	// [c] is offered only while the row under the cursor can
	// honour it — an offer nothing accepts is worse than no offer at all —
	// and its words say which copy it is: one fenced block on a reply that
	// holds one, the whole row everywhere else.
	if _, blocks := m.focusedBlocks(); len(blocks) > 0 {
		segs = append(segs, segAs(keys.Reading.Copy, "copy a block").At(readingGivesCopy))
	} else if m.focusedCopyable() {
		segs = append(segs, segAs(keys.Reading.Copy, "copy the row").At(readingGivesCopy))
	}
	// [/] stands whether or not a search is open: it opens the query row, or
	// reopens it on the query already in it. The step pair joins it only once
	// there is something to step through, the way the collapse joins once a row is
	// open.
	segs = append(segs, seg(keys.Reading.Search).At(readingGivesSearch))
	if m.viewport.Searching() {
		segs = append(segs, seg(keys.Reading.Match).At(readingGivesMatch))
	}
	// The register's own key sits between the row's offers and the way out:
	// it is the last thing a reader reaches for and the first the bar sheds.
	segs = append(segs, seg(keys.Reading.List).At(readingGivesList))
	// esc is one level per press (updateFocus), so while the row under the
	// cursor is open the bar says what the next press does to it.
	if m.focusedRowOpen() {
		return append(segs, segAs(keys.Reading.Back, collapseWords))
	}
	return append(segs, segAs(keys.Reading.Back, backToPromptWords))
}

// openTrayWords is what enter does on a sent picture's row.
const openTrayWords = "open it"

// The words for what enter does to a card, one per depth: a card is closed
// or open, and enter takes it to the other.
const (
	cardOpenWords  = "open it"
	cardCloseWords = "close it"
)

// The words for what enter does to an open card's group line, which folds
// its group and unfolds it.
const (
	groupFoldWords   = "fold it"
	groupUnfoldWords = "unfold it"
)

// focusedCardWords is what enter does to the card under the cursor, if the
// cursor is on one: open a closed one onto its calls, close an open one
// (toggleCard).
func (m Model) focusedCardWords() (string, bool) {
	es := *m.entries()
	blk, ok := m.cardTakesKey(es, m.focusIdx)
	if !ok {
		return "", false
	}
	if m.cardOpen(blk, es) {
		return cardCloseWords, true
	}
	return cardOpenWords, true
}

// focusedTrayPicture reports whether the cursor stands on a sent picture's
// row, which enter opens onto its card.
func (m Model) focusedTrayPicture() bool {
	es := *m.entries()
	if m.focusIdx < 0 || m.focusIdx >= len(es) {
		return false
	}
	_, ok := trayPicture(es[m.focusIdx])
	return ok
}

// focusedExpands reports whether [enter] would open anything on the row
// under the cursor: a card's fold, or a row with a body of its own. The bar
// reads it so the key it offers is one the row will honour.
func (m Model) focusedExpands() bool {
	es := *m.entries()
	if m.focusIdx < 0 || m.focusIdx >= len(es) {
		return false
	}
	if _, ok := m.cardTakesKey(es, m.focusIdx); ok {
		return true
	}
	return m.rowExpands(es[m.focusIdx])
}

// seg is a binding as one segment of the bar: the register's spelling and the
// register's words, so the bar cannot offer a key the dispatch does not
// answer.
func seg(b keys.Binding) hintSeg { return segAs(b, keys.Words(b)) }

// segAs is the same segment with the surface's own words, which is what a
// rail reaches for wherever it means something more specific than the
// register does. The key stays the register's either way, and so does
// whether it is the safe answer: that is decided once, off the spelling,
// where every row's offers are made (components.OfferAs).
func segAs(b keys.Binding, label string) hintSeg { return components.OfferAs(b, label) }

// The reading bar's order of giving things up, written as the priority each
// segment is built with (components.Shed): `[?]` goes first, then a key
// stated absent with its reason, then [/], then [c], then [n/N], then a live
// [enter] goes whole, and the way back is the last segment standing.
//
// `[?]` is the only offer here that acts on nothing in the transcript at
// all, and the one a reader who loses it can still find — /help names it,
// and the supporting TUIs have taught the same key for four screens. A key
// stated absent with its reason is a teaching and not an offer, and the
// widest thing on the bar, so it stands wherever the terminal can carry it
// whole and leaves before any key the row can honour
// (docs/interface/principles.md#fold-never-hide). [/] goes before [c]
// because [c] acts on the row the reader is already standing on and [/]
// opens a surface they have not asked for yet; [n/N] outlasts both, since
// the key that walks what the reader is already reading is worth more than
// the key that would start another search.
const (
	readingGivesExpand = iota + 1
	readingGivesMatch
	readingGivesCopy
	readingGivesSearch
	readingGivesAbsent
	readingGivesList
)

// readingPositionFields is the right-hand field in its forms, widest first.
// It is the position of the cursor among the rows — or, once rows are open,
// how many are, and just after a [c], what the copy caught (copyrow.go),
// which is the one moment that fact outranks the position. Prose
// has no addressable rows to count, so it reports nothing.
func (m Model) readingPositionFields() []string {
	if m.stripLive() {
		return m.stripPosition()
	}
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
// position field reports once there is one, and the reason esc is on the bar.
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

// openKind is one way a transcript row can be showing more than its own line.
// It is a closed vocabulary rather than a condition spelled out at each call
// site (docs/interface/principles.md#closed-vocabularies), because two
// gestures act on it and they must not learn different lists: reading mode's
// esc closes whichever one the cursor is standing on, and esc on an empty
// draft puts every one the reader opened back at once.
//
// The two gestures ask different questions of the same list. Reading mode's asks what is
// open under the cursor, whatever opened it, because the reader is pointing
// at that row and asking for it closed; the draft's asks what the *reader* opened, so
// a body the verbosity is holding open is not its to fold
// (docs/interface/surfaces.md#the-input-frame).
type openKind int

const (
	// openNone is a row showing nothing but itself.
	openNone openKind = iota
	// openStep is a card open onto its calls.
	openStep
	// openDetail is those rows showing their detail bodies — what /step
	// opens. It is a kind of its own rather than a degree of openStep because
	// the two are answered separately: closing a step's header leaves its
	// detail answer standing, and closing the detail leaves the step open
	// (steps.go, detail.go).
	openDetail
	// openGroup is an open card's group showing its calls (cardopen.go).
	openGroup
	// openDiff is a diff showing its hunks inside the transcript.
	openDiff
	// openBody is any other row showing what its call returned.
	openBody
)

// focusedOpenKind is what the row under the cursor has open, or openNone.
// Anything showing counts, the verbosity's doing included: esc is the reader
// naming one row.
func (m Model) focusedOpenKind() openKind {
	es := *m.entries()
	if m.focusIdx < 0 || m.focusIdx >= len(es) {
		return openNone
	}
	if _, _, ok := m.groupAt(es, m.focusIdx); ok {
		// A group line has its group open or folded; folded, what esc has
		// left to close on a run nothing titled is the card it starts.
		switch _, isCard := m.cardBlockAt(es, m.focusIdx); {
		case !es[m.focusIdx].groupFolded:
			return openGroup
		case isCard:
			return openStep
		}
		return openNone
	}
	if blk, ok := m.cardTakesKey(es, m.focusIdx); ok {
		switch {
		case m.cardOpen(blk, es):
			return openStep
		case es[m.focusIdx].detailFold == foldOpen:
			// A card closed over an open detail: the rows are not on
			// screen, but the answer that would show their bodies is still
			// on record, and esc takes it back.
			return openDetail
		}
		return openNone
	}
	if d := es[m.focusIdx].diff; d != nil {
		if d.Mode == components.DiffCollapsed {
			return openNone
		}
		return openDiff
	}
	if es[m.focusIdx].expanded {
		return openBody
	}
	// The first call of an open run nothing titled is where its card is
	// kept: with nothing of its own open, what esc has to close is the card.
	if _, ok := m.cardBlockAt(es, m.focusIdx); ok {
		return openStep
	}
	return openNone
}

// focusedRowOpen reports whether the row under the cursor is showing more
// than its own line — an expanded body, or a card drawing more than its
// header.
func (m Model) focusedRowOpen() bool { return m.focusedOpenKind() != openNone }

// collapseFocused closes whatever the row under the cursor has open, and
// reports whether there was anything to close. Where there is not, esc
// leaves reading mode.
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
	case openGroup:
		es[m.focusIdx].groupFolded = true
	case openStep:
		if blk, ok := m.cardBlockAt(es, m.focusIdx); ok {
			m.closeCard(blk, m.focusIdx)
		}
	case openDetail:
		es[m.focusIdx].detailFold = foldClosed
	case openDiff:
		es[m.focusIdx].diff.Mode = components.DiffCollapsed
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
// overrides each have a value meaning "no answer of mine", and no setting
// can write one (steps.go, detail.go).
func readerOpened(e entry) []openKind {
	var kinds []openKind
	// The two step overrides are read and put back one at a time, even
	// though /step writes both at once: a reader who then closes the card
	// has said no on one of them and nothing on the other, and a fold that
	// treated them as one answer would overwrite that no. Which also means
	// a detail answer standing behind a closed card is still put back —
	// nothing on screen reports it, and an answer nothing reports is one
	// that springs the bodies open the next time the card opens. A card the
	// reader closed at high is a no, not an open, and is left alone.
	if e.stepFold == foldOpen {
		kinds = append(kinds, openStep)
	}
	if e.detailFold == foldOpen {
		kinds = append(kinds, openDetail)
	}
	// DiffExpanded, and not "anything but collapsed": the full-screen view is
	// a surface of its own, and esc there steps back to the expanded form
	// long before it can reach the draft (components/diff.go).
	if e.diff != nil && e.diff.Mode == components.DiffExpanded {
		kinds = append(kinds, openDiff)
	}
	if e.expanded {
		kinds = append(kinds, openBody)
	}
	return kinds
}

// restOpen puts one of those answers back to the value that means "no answer
// of mine on record" — foldAuto for the three overrides — so a row is folded to what the setting says and never past it.
// Writing foldClosed instead would make `/ui verbosity high` quietly stop
// meaning what it says, and a reader who ran it afterwards and saw nothing
// open would have been lied to.
func restOpen(e *entry, k openKind) {
	switch k {
	case openStep:
		e.stepFold = foldAuto
	case openDetail:
		e.detailFold = foldAuto
	case openDiff:
		e.diff.Mode = components.DiffCollapsed
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
	if !m.density(verbosityHigh) {
		return false
	}
	es := *m.entries()
	for _, blk := range m.blocksOf(es) {
		if blk.step != nil && blk.step.queued() {
			continue
		}
		start, end := blk.start, blk.end
		if isCardBlock(blk, es) {
			// A card drawing anything less than its calls shows no bodies.
			if !m.cardOpen(blk, es) {
				continue
			}
			start, end = blk.members()
		}
		for i := start; i < end; i++ {
			if m.settingShowsBody(es[i]) {
				return true
			}
		}
	}
	return false
}

// settingShowsBody reports whether this row is showing a body only because
// the verbosity says so — the caller has already established that it does.
func (m Model) settingShowsBody(e entry) bool {
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
		// and the changeset's is the one that offers anything.
		if e.close.Changes != nil {
			return e.close.Changes.Keys
		}
		return nil
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
	if e, ok := m.focusedRewound(); ok {
		return m.rewoundOffers(e)
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
		segs = append(segs, hintSeg{Key: o.Key, Label: o.Label, Safe: o.Safe})
	}
	segs = append(segs, segAs(keys.Select.Cancel, "nothing"))

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
	room := width - lipgloss.Width(rail)
	var lines []string
	for _, row := range hintTones().Pack(segs, room) {
		lines = append(lines, clipRow(rail+row, width))
	}
	if len(lines) > budget {
		// The run ends on esc, the safe answer, and a budget that cannot hold
		// every row gives up the rows before it, never that one.
		lines = append(lines[:budget-1], lines[len(lines)-1])
	}
	return lines
}

// readingKeyLine is the first line: the mode's keys with the position on the
// right. The position narrows through its forms before any key leaves, the
// keys leave whole in the order their priorities give with the way back the
// last of them, and the position is dropped once only the floor is left —
// the lit row still says which row it is, which is why dropping it costs
// least.
func (m Model) readingKeyLine(width int) string {
	full := m.readingModeKeys()
	forms := components.Shed(full)
	if m.stripLive() {
		// The strip's three keys are its whole bar, so their words give
		// up first, to the verb; only a terminal too narrow for the short
		// form loses a key, whole and from the right.
		forms = [][]hintSeg{full, stripKeysShort(full)}
	}
	positions := m.readingPositionFields()
	for _, form := range forms {
		// The position goes before the floor does: a form that has given up
		// a key the bar always carries is one the lit row says more than.
		if components.Floor(form) < components.Floor(full) {
			break
		}
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
	return hintTones().Fit(forms[len(forms)-1], width)
}
