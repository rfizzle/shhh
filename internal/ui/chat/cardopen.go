package chat

// The open card (docs/interface/surfaces.md#the-step). Opened, a card lists
// its calls under the receipt's verbs — one group per kind, in the order the
// header counts them, each folding on its own — and ends in the strip, one
// glyph per call in the order they were made. The strip is the way to one
// call: the arrows walk a cursor along it, the call under the cursor is lit
// in its group with it, and enter opens that call's own view.
//
// Reading mode's cursor stops on a card's header and on its group lines; a
// call in a group is reached along the strip rather than by walking a feed
// of all of them, which is what the groups are for. A group of one call has
// no line over it — the line would say the call's subject twice — so its
// row stands in the card on its own and is the stop; a card of one call has
// no strip to walk.
//
// The groups and the strip are read once from the step's receipt
// (internal/receipt), in openCard, which is the seam a ladder rung or a
// summary that opens a card reads too.

import (
	"fmt"
	"path"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/receipt"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// stripCursor is where the cursor along an open card's strip stands: the card
// it is on, by the entry the card is kept on, and the call, by its place in
// the strip. It is the session's rather than the card's, because there is
// one cursor and it moves between cards; a card that closes takes it with
// it.
type stripCursor struct {
	anchor, call int
	on           bool
	// child is the transcript the anchor indexes: the session's own, or
	// the attached child's. An index into one names nothing in the other.
	child string
}

// openCard is a card's calls as its open form lists them: the step's
// receipt, whose groups and strip say what goes where, and the entry each
// call is, by its place in the strip.
type openCard struct {
	anchor int
	step   receipt.Step
	acts   []receipt.Act
	at     []int
	// detail is the card's detail open — /step, or the high rung — which
	// draws every call with its body, the reads included.
	detail bool
}

// openCardOf reads the open form of a card.
func (m Model) openCardOf(blk transcriptBlock, es []entry) openCard {
	acts, at, _ := m.cardActs(blk, es)
	return openCard{anchor: cardAnchor(blk), step: receipt.BuildStep(acts), acts: acts, at: at,
		detail: m.cardDetailOpen(blk, es)}
}

// stripped reports whether the card ends in the strip: from two calls, since
// a strip of one glyph is no way to anywhere.
// See docs/interface/departures.md#the-open-cards-strip-starts-at-two-calls-and-a-groups-line-at-two.
func (oc openCard) stripped() bool { return len(oc.at) >= 2 }

// lined reports whether a group stands under a line of its own: from two
// calls. One call is its own row, whose subject the line would only repeat.
func lined(g receipt.Group) bool { return len(g.Calls) >= 2 }

// groupFirst is the entry a group is kept on: its first call.
func (oc openCard) groupFirst(g receipt.Group) int { return oc.at[g.Calls[0]] }

// place is a call's place in the strip, by its entry.
func (oc openCard) place(idx int) (int, bool) {
	for i, at := range oc.at {
		if at == idx {
			return i, true
		}
	}
	return 0, false
}

// rolledUp reports whether a group's reads are drawn as rows by directory
// rather than a row per call: every read named a file, so the directories
// say where the step read in fewer rows than the calls would. A card whose
// detail is open draws each read with its body instead, since the bodies
// are what the reader opened it for.
func (oc openCard) rolledUp(g receipt.Group) bool {
	return lined(g) && !oc.detail && g.Kind == receipt.KindRead && len(oc.step.Reads) > 0
}

// openCardStops is the entries reading mode's cursor stops on inside the
// open card, before its other members: each group's line, or the row of a
// group of one call.
func (m Model) openCardStops(blk transcriptBlock, es []entry) []int {
	oc := m.openCardOf(blk, es)
	idxs := make([]int, 0, len(oc.step.Groups))
	for _, g := range oc.step.Groups {
		if first := oc.groupFirst(g); lined(g) || m.selectableRow(es[first]) {
			idxs = append(idxs, first)
		}
	}
	return idxs
}

// openCardStopFor is the stop inside an open card that stands for the entry
// at idx: the call's own row where it is one, the line of its group
// otherwise, and idx itself for a member that is not a call.
func (m Model) openCardStopFor(blk transcriptBlock, es []entry, idx int) int {
	oc := m.openCardOf(blk, es)
	p, ok := oc.place(idx)
	if !ok {
		return idx
	}
	for _, g := range oc.step.Groups {
		for _, c := range g.Calls {
			if c == p {
				return oc.groupFirst(g)
			}
		}
	}
	return idx
}

// groupAt is the open card whose group line the entry at idx is kept on, and
// that group.
func (m Model) groupAt(es []entry, idx int) (transcriptBlock, receipt.Group, bool) {
	if idx < 0 || idx >= len(es) || !isActivityEntry(es[idx]) {
		return transcriptBlock{}, receipt.Group{}, false
	}
	for _, blk := range m.blocksOf(es) {
		if !blk.holds(idx) || !isCardBlock(blk, es) || !m.cardOpen(blk, es) {
			continue
		}
		oc := m.openCardOf(blk, es)
		for _, g := range oc.step.Groups {
			if lined(g) && oc.groupFirst(g) == idx {
				return blk, g, true
			}
		}
	}
	return transcriptBlock{}, receipt.Group{}, false
}

// toggleGroupFold folds the group whose line the entry at idx is kept on, or
// unfolds it, and reports whether there was one. It is enter and a click on
// the line, and esc on an open one.
func (m *Model) toggleGroupFold(idx int) bool {
	es := *m.entries()
	if _, _, ok := m.groupAt(es, idx); !ok {
		return false
	}
	es[idx].groupFolded = !es[idx].groupFolded
	return true
}

// openCardUnits renders an open card: its head, its groups — each line a
// unit of its own, so the cursor can stand on it and a click can name it —
// the card's other members as they draw anywhere, and the strip.
func (m Model) openCardUnits(blk transcriptBlock, es []entry, width int, focus bool, focusIdx int, card components.StepCard) []unit {
	anchor := cardAnchor(blk)
	block, tight := entry{kind: entryAssistant}, entry{kind: entryTool}
	// Open, a card nothing titled is kept on its first call, whose group's
	// line or row is the stop; its own lines are not.
	units := []unit{{idx: anchor, sepBefore: block, sepAfter: tight, text: card.Head(width) + "\n",
		cardHead: true, shadow: blk.step == nil}}
	add := func(u unit) {
		u.sepBefore, u.sepAfter = tight, tight
		units = append(units, u)
	}
	oc := m.openCardOf(blk, es)
	detail := oc.detail
	lit, onStrip := m.stripCallOn(anchor)
	cursorOn := func(idx int) bool { return focus && focusIdx == idx && !onStrip }

	for _, g := range oc.step.Groups {
		first := oc.groupFirst(g)
		if !lined(g) {
			on := cursorOn(first) || (onStrip && lit == g.Calls[0])
			add(unit{idx: first, text: m.cardCallText(oc, g.Calls[0], width, detail, on), call: true})
			continue
		}
		folded := es[first].groupFolded
		right := turnDuration(g.Duration)
		if g.Added+g.Removed > 0 {
			right = lineChange(g.Added, g.Removed)
		}
		line := components.CardGroupLine{Label: g.Label, Right: right, Folded: folded, Selected: cursorOn(first)}
		text := line.View(width) + "\n"
		if !folded && oc.rolledUp(g) {
			text += cardDirRows(oc.step.Reads, width)
		}
		add(unit{idx: first, text: text, group: true})
		if folded {
			continue
		}
		for _, p := range g.Calls {
			// A read the directories name is drawn there; one that broke or
			// was refused is a row of its own, since a directory row says
			// what was read and not what was not.
			if oc.rolledUp(g) && oc.step.Strip[p].State == receipt.StateDone {
				continue
			}
			add(unit{idx: oc.at[p], text: m.cardCallText(oc, p, width, detail, onStrip && lit == p),
				shadow: oc.at[p] == first, call: true})
		}
	}

	// What else the step holds — the model thinking between its rounds, the
	// notices its calls earned — draws as it does anywhere, under the calls.
	start, end := blk.members()
	for i := start; i < end; i++ {
		if isActivityEntry(es[i]) {
			continue
		}
		text, selectable, grid := m.entryUnitText(i, es, width, focus, focusIdx, detail)
		if text == "" {
			continue
		}
		if focus && selectable {
			text = gutterPrefix(text, i == focusIdx, grid, gutterWidth(width, grid))
		}
		var lines []string
		for _, l := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
			lines = append(lines, components.OnBand(l, width))
		}
		add(unit{idx: i, text: strings.Join(lines, "\n") + "\n"})
	}

	if oc.stripped() {
		cursor := -1
		if onStrip {
			cursor = lit
		}
		add(unit{idx: anchor, strip: true, shadow: true, text: components.CardPad(width) + "\n" +
			m.cardStripFor(oc, cursor).View(width) + "\n"})
	}
	last := &units[len(units)-1]
	last.text += components.CardPad(width) + "\n"
	last.sepAfter = block
	last.closesCard = true
	return units
}

// cardDirRows is a group of reads as rows by directory, the directories past
// the ceiling counted on one row of their own.
func cardDirRows(dirs []receipt.Dir, width int) string {
	var b strings.Builder
	more := 0
	for i, d := range dirs {
		if i >= receipt.ReadDirCeiling {
			more += len(d.Files)
			continue
		}
		names := make([]string, 0, len(d.Files))
		for _, f := range d.Files {
			names = append(names, path.Base(f))
		}
		b.WriteString(components.CardDirRow{Dir: d.Name, Files: names}.View(width) + "\n")
	}
	if more > 0 {
		b.WriteString(components.CardDirRow{More: more}.View(width) + "\n")
	}
	return b.String()
}

// cardStripFor is the open card's strip with the cursor on the call at
// cursor, or on none where it is -1.
func (m Model) cardStripFor(oc openCard, cursor int) components.CardStrip {
	strip := components.CardStrip{Cursor: cursor}
	for _, mk := range oc.step.Strip {
		strip.Cells = append(strip.Cells, components.StripCell{Kind: activityKind(mk.Kind), State: markState(mk.State)})
	}
	return strip
}

// cardCallText is one call of an open card: its row on the card's grid, and
// under it whatever of its body is open, the same body its row draws
// anywhere. lit puts a cursor on it.
func (m Model) cardCallText(oc openCard, p, width int, detail, lit bool) string {
	es := *m.entries()
	e := es[oc.at[p]]
	bw := components.CardCallBodyWidth(width)
	var row components.ActivityRow
	var drawn string
	if e.kind == entryDiff && e.diff != nil {
		row = e.diff.Row()
		if e.diff.Mode == components.DiffFull {
			drawn = strings.Join(e.diff.ExpandedLines(bw), "\n")
		} else {
			drawn = e.diff.View(bw)
		}
	} else {
		row = m.activityRowDetail(e, detail, bw)
		drawn = row.View(bw)
	}
	body := strings.Split(drawn, "\n")[1:]
	if !impliedVerb(row.Verb) {
		// A verb the group's line does not say for it — staging and
		// committing, a lookup's hover — stays with its subject.
		row.Target = strings.TrimSpace(row.Verb + " " + row.Target)
	}
	call := components.CardCallRow{Row: row, Lit: lit}
	if len(body) == 0 {
		// The line a call stands on is said beside its subject while its
		// body is shut; open — and a failed call's body is open, as it is
		// on any row — the body says it in place.
		// See docs/interface/departures.md#a-failed-call-in-an-open-card-keeps-its-mark-and-its-body.
		call.Line = evidenceLine(oc.acts[p].Line())
	}
	lines := append([]string{call.View(width)}, components.CardCallBody(body, width)...)
	return strings.Join(lines, "\n") + "\n"
}

// impliedVerb reports whether a row's verb is the one its group's line
// already says — a command run, a file read, an edit made, a search — so the
// row in an open card can name its subject alone.
// See docs/interface/departures.md#a-call-in-an-open-card-names-its-verb-where-its-group-does-not.
func impliedVerb(verb string) bool {
	switch verb {
	case "", "run", "read", "edit", "write", "search":
		return true
	}
	return false
}

// stripCallOn is the call the strip's cursor stands on in the card kept on
// anchor, if it stands on that card at all.
func (m Model) stripCallOn(anchor int) (int, bool) {
	if !m.strip.on || m.strip.anchor != anchor || !m.stripLive() {
		return 0, false
	}
	return m.strip.call, true
}

// stripLive reports whether the strip's cursor is set on a card that is
// still open, in the transcript on screen, and — under reading mode — the
// card the reading cursor is in. A card folded under it by a rung, or a
// cursor moved to another card, takes the strip's cursor with it, whatever
// the record says.
func (m Model) stripLive() bool {
	if !m.strip.on || m.strip.child != m.attachedTo {
		return false
	}
	es := *m.entries()
	blk, ok := m.cardBlockAt(es, m.strip.anchor)
	if !ok || !m.cardOpen(blk, es) {
		return false
	}
	return m.state != stateFocus || blk.holds(m.focusIdx)
}

// cursorStopFor is the stop reading mode's cursor takes for the entry at
// idx: inside an open card whose calls are grouped, a call's group line;
// anywhere else the entry itself.
func (m Model) cursorStopFor(es []entry, idx int) int {
	for _, blk := range m.blocksOf(es) {
		if blk.holds(idx) && isCardBlock(blk, es) && m.cardOpen(blk, es) {
			return m.openCardStopFor(blk, es, idx)
		}
	}
	return idx
}

// clickCardStrip answers a click on an open card's strip: a glyph puts the
// strip's cursor on its call, as the arrows would walk it there. Anywhere
// else on the strip's row names no call and does nothing. Under reading
// mode the cursor is then live, and the reading cursor is in the card;
// outside it the click marks the call and takes no keyboard.
func (m Model) clickCardStrip(pt selPoint) (tea.Model, tea.Cmd, bool) {
	u, offset, ok := m.unitAt(pt.line)
	if !ok || !u.strip {
		return m, nil, false
	}
	es := *m.entries()
	blk, ok := m.cardBlockAt(es, u.idx)
	if !ok || offset != 1 {
		return m, nil, true
	}
	oc := m.openCardOf(blk, es)
	call, ok := m.cardStripFor(oc, -1).CellAt(m.transcriptWidth(), pt.col)
	if !ok {
		return m, nil, true
	}
	m.strip = stripCursor{anchor: u.idx, call: call, on: true, child: m.attachedTo}
	m.invalidateRenderCache()
	if m.state == stateFocus {
		if _, _, in := m.openCardAt(es, m.focusIdx); !in || !blk.holds(m.focusIdx) {
			m.focusIdx = m.cursorStopFor(es, oc.at[call])
		}
		m.refreshFocusView()
		return m, nil, true
	}
	m.viewport.SetLines(m.renderHistoryLines())
	return m, nil, true
}

// openCardAt is the open card whose stops include the entry at idx — its
// header, a group line, its one call's row — where that card has a strip to
// walk.
func (m Model) openCardAt(es []entry, idx int) (transcriptBlock, openCard, bool) {
	if idx < 0 || idx >= len(es) {
		return transcriptBlock{}, openCard{}, false
	}
	for _, blk := range m.blocksOf(es) {
		if !blk.holds(idx) || !isCardBlock(blk, es) || !m.cardOpen(blk, es) {
			continue
		}
		oc := m.openCardOf(blk, es)
		if !oc.stripped() {
			return transcriptBlock{}, openCard{}, false
		}
		return blk, oc, true
	}
	return transcriptBlock{}, openCard{}, false
}

// updateCardStrip answers the strip's keys under reading mode's cursor, and
// reports whether it took the key. The arrows walk the strip of the open card
// the cursor is in, the first press putting the cursor on the strip at its
// last call — where the step ended, which is what it stands on. While the
// cursor is on the strip, enter opens the call under it and the way back
// closes the card; a key that moves the reading cursor takes it off the
// strip first.
// See docs/interface/departures.md#the-strips-cursor-starts-on-its-last-call-and-lets-go-on-a-move.
func (m Model) updateCardStrip(msg tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	pressed := msg.String()
	es := *m.entries()
	if keys.Is(pressed, keys.Reading.StripLeft, keys.Reading.StripRight) {
		blk, oc, ok := m.openCardAt(es, m.focusIdx)
		if !ok {
			return m, nil, false
		}
		anchor := cardAnchor(blk)
		if call, on := m.stripCallOn(anchor); on {
			step := 1
			if keys.Is(pressed, keys.Reading.StripLeft) {
				step = -1
			}
			m.strip.call = min(max(call+step, 0), len(oc.at)-1)
		} else {
			m.strip = stripCursor{anchor: anchor, call: len(oc.at) - 1, on: true, child: m.attachedTo}
		}
		m.invalidateRenderCache()
		m.refreshFocusView()
		return m, nil, true
	}
	if !m.stripLive() {
		m.strip = stripCursor{}
		return m, nil, false
	}
	switch {
	case keys.Is(pressed, keys.Reading.Expand):
		next, cmd := m.openStripCall()
		return next, cmd, true
	case keys.Is(pressed, keys.Reading.Back):
		m.closeStripCard()
		m.refreshFocusView()
		return m, nil, true
	case keys.Is(pressed, keys.Reading.List):
		// The key list is read over the strip, names the strip's keys
		// while the cursor is on it, and gives the strip back as it was.
		return m, nil, false
	}
	// Anything else is the reading cursor's: it leaves the strip where it
	// was, and the strip lets go of it.
	m.strip = stripCursor{}
	m.invalidateRenderCache()
	return m, nil, false
}

// openStripCall opens the call under the strip's cursor in its own view —
// the full screen its row opens, the diff's for an edit — which comes back
// to reading mode, and so to the strip with its cursor where it was.
func (m Model) openStripCall() (tea.Model, tea.Cmd) {
	es := *m.entries()
	blk, oc, ok := m.openCardAt(es, m.focusIdx)
	if !ok || m.strip.call < 0 || m.strip.call >= len(oc.at) || cardAnchor(blk) != m.strip.anchor {
		return m, nil
	}
	return m.openCallView(oc.at[m.strip.call], stateFocus)
}

// openCallView opens one call of an open card in its own view: the diff's
// full screen for an edit, the output's for anything else. esc there comes
// back to ret, with the card still open behind it.
func (m Model) openCallView(idx int, ret state) (tea.Model, tea.Cmd) {
	es := *m.entries()
	if d := es[idx].diff; d != nil {
		d.Mode = components.DiffFull
		d.Offset = 0
		return m.openDiffFull(d, ret)
	}
	return m.openOutputFull(m.rowOutputView(es[idx]), idx, ret)
}

// closeStripCard is the way back from the strip: the card closes to the card
// the rung draws, and the reading cursor is on it.
func (m *Model) closeStripCard() {
	es := *m.entries()
	anchor := m.strip.anchor
	m.strip = stripCursor{}
	if blk, ok := m.cardBlockAt(es, anchor); ok {
		m.closeCard(blk, anchor)
		m.focusIdx = anchor
	}
	m.invalidateRenderCache()
}

// stripKeys is the hint bar while the cursor is on a strip: the arrows along
// it, enter on the call under it, and the way back, which closes the card.
func (m Model) stripKeys() []hintSeg {
	pair := strings.Trim(keys.BracketPair(keys.Reading.StripLeft, keys.Reading.StripRight), "[]")
	return []hintSeg{
		{key: pair, label: "along the strip"},
		segAs(keys.Reading.Expand, "open that tool"),
		segAs(keys.Reading.Back, "close the card"),
	}
}

// stripKeysShort is the strip's bar with its words cut to the verb, for a
// pane too narrow for the whole of it.
func stripKeysShort(segs []hintSeg) []hintSeg {
	out := append([]hintSeg(nil), segs...)
	for i, word := range []string{"along", "open", "close"} {
		if i < len(out) {
			out[i].label = word
		}
	}
	return out
}

// stripPosition is the bar's right-hand fact while the cursor is on a strip:
// which call it is on, of how many, and how that call came out.
func (m Model) stripPosition() []string {
	es := *m.entries()
	_, oc, ok := m.openCardAt(es, m.focusIdx)
	if !ok || m.strip.call < 0 || m.strip.call >= len(oc.at) {
		return nil
	}
	n, of := m.strip.call+1, len(oc.at)
	pos, short := fmt.Sprintf("tool %d of %d", n, of), fmt.Sprintf("%d/%d", n, of)
	row := m.activityRowFor(es[oc.at[m.strip.call]])
	if row.Outcome != "" {
		return []string{pos + " · " + row.Outcome, pos, short}
	}
	return []string{pos, short}
}
