package chat

// Click targets (
// docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
//
// Until now the mouse could read this surface and nothing else: the wheel
// scrolled, a drag selected, and a press on anything else was deliberately
// inert. The inertness was load-bearing — a press that also expanded a row
// would have made every drag a gamble on where it started — but it was a
// rule about the *press*, and the press is the wrong event to hang it on. A
// click is a press and a release in the same cell. Nothing here fires while
// the button is down, so a drag that starts on a target still selects, and
// the one button carries both gestures without either having to give ground.
//
// Eleven things are targets, and the test they pass is the same one eleven
// times: the pointer names exactly one of them, and the thing it names
// already has a key.
//
//   - A card's header: a step's, a fan-out's, a plan's. It names one card,
//     and [enter] with reading mode's cursor on the card opens it and closes
//     it again, esc closes an open one; the click does the same, opening a
//     closed card and closing an open one (card.go). Each has two depths and
//     no fold: a step's opens onto its calls, a fan-out's onto its
//     children's reports, a plan's onto the steps past its ceiling, and at
//     low each opens from its header alone. A fan-out or a plan with nothing
//     more to draw is no stop for the cursor, and its header no target. The
//     rest of the card — its padding, the sentence that titled the step, the
//     evidence under it, a plan's step rows — is text, a selection surface
//     with no single act behind it, and a click there does nothing.
//   - An activity row. Its whole width is one row, and [enter] under reading
//     mode's cursor already opens it. The row line opens and closes it; the
//     body under the row opens that body whole (clickRow). A call's row
//     inside an open card opens that call's own view, as [enter] on the
//     strip does with the call under its cursor; a child's row on a
//     fan-out's card opens the children's reports as [enter] on the card
//     does. A turn's close
//     is one too: the line stating what the turn changed opens that turn's
//     review, which enter on the selected close opens as well.
//   - The approval card's decision run. Each key owns its own cells inside
//     `[y/N/a]`, and the click is delivered as the keystroke.
//   - A recovery or round-limit row's offers, by the card's rule
//     (clickRowOffer): while the row holds the keyboard each offer it draws
//     is its letter, delivered through the row's own dispatch; while it does
//     not, the one live key it draws is the handover, and a click there
//     hands the row the keyboard as the chord would.
//   - A file on the rail. It names one path, and `/diff <path>` opens that
//     path's diff by name (railclick.go).
//   - A session on the rail. It names one session, and the chord that walks
//     the map and the manager's [enter] both attach to it.
//   - A staged chip. It names one attachment, and [enter] with reading mode's
//     cursor on it opens it (stagedstrip.go). The chip is a door and not a
//     button: nothing is printed on it, because it sits above a live draft
//     where a printed key would be an offer nothing accepts, and the keys
//     that act on it are live only in the mode that says so on its own bar.
//     The count of the chips the strip gave up names none of them and is
//     not a target.
//   - A sent attachment's row in the tray under a message. It names one
//     attachment, and [enter] with reading mode's cursor on the row opens it:
//     a picture onto its card, every click, and a paste in place
//     (attachments.go). A document's row opens onto nothing under either
//     input, so it is not one. A picture a call returned, on its card's
//     footer, is the same door and the same target.
//   - An open card's group line. It names one group of the card's calls,
//     and [enter] with reading mode's cursor on the line folds and unfolds
//     that group (cardopen.go). The directories under a group of reads name
//     files rather than an act, and are not targets.
//   - A glyph on an open card's strip. It names one call, and the arrows
//     walk the strip's cursor to it a call at a time; the click puts the
//     cursor there, and enter opens the call from there, as it does after
//     the arrows. The strip's words and its count name no call.
//   - A code block's heading row in a reply. It names exactly one block, and
//     reading mode's [c] on that reply reaches the same block; the click
//     copies it through the key's own handler (blockhead.go). The rest of
//     the block is code under the pointer, which is a selection surface, and
//     a sent message's headings have no key that copies them.
//
// Everything else on the screen fails that test. Prose under the pointer is a
// selection surface first and has no single act behind it; the scroll gutter
// is a shape rather than a control; the rail's readings — the summary, the
// plan, the todo list, the tool sources and the two meters — name blocks and
// numbers rather than things to go to (its headings are doors of their own,
// railclick.go); a chip's `✕` would be a button with no keyboard equal, and
// a target only the mouse can reach is a target half the readers do not
// have.
//
// Three rules hold the whole file together. **A click never takes the
// keyboard** — reading is not a decision, so a row opened by pointer
// leaves the draft holding every character it had, exactly as the wheel does.
// **A clicked key is the keystroke**: it goes to the handler the key goes
// to, so there is no second decision path that could answer differently from
// the first. And **a click that opened a thing closes it again**: the same
// cell, pressed twice, is where it started. That last one is why the pointer
// does not walk the keyboard's three-depth cycle, where the second press
// would take the whole screen instead of giving the row back; it reads the
// half of the row it landed in instead. A call's row inside an open card is
// the one row that takes the screen on a click: the card's groups already say
// what the call did, so what is left to open is its own view, and esc gives
// the card back as the strip's enter does. A modifier would have been the other
// way to say it, and it is not available here: every terminal worth naming
// keeps shift-click for its own selection, and hands the application
// nothing.

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// pointerPress is where the primary button went down, and whether one is down
// at all. Every press is recorded, not just the ones the transcript would
// anchor a selection in: the card is in the bottom panel, which is not a
// surface a selection can be anchored in at all.
type pointerPress struct {
	x, y int
	live bool
}

// beginClick records the cell a press landed on.
func (m *Model) beginClick(x, y int) {
	m.pointer.press = pointerPress{x: x, y: y, live: true}
}

// endClick reports whether a release completes a click, and forgets the press
// either way. A release anywhere but the cell the press landed in is a drag,
// and the drag's own release is what answers it.
func (m *Model) endClick(x, y int) bool {
	p := m.pointer.press
	m.pointer.press = pointerPress{}
	return p.live && p.x == x && p.y == y
}

// clickAt resolves a click to the one thing under it. The rail is asked
// first and the pane second, because those are the halves of the screen a
// coordinate can be checked against without rendering anything; the card is
// what is left, and it is the one that has to be drawn to be found.
func (m Model) clickAt(x, y int) (tea.Model, tea.Cmd) {
	if o := m.staged.opened; o.x == x && o.y == y && o.showing(m) {
		// The card or reader this chip's cell opened covers the strip, so the
		// chip is not there to be found — but the cell is, and a click that
		// opened a thing closes it again (stagedstrip.go).
		m.staged.opened = railOpening{}
		return o.door.close(m)
	}
	if next, cmd, ok := m.clickRail(x, y); ok {
		return next, cmd
	}
	// The editor pane's outline, the one row of a pane overlay a click
	// reaches: it moves the pane's cursor and never the keyboard (edit.go).
	if next, cmd, ok := m.clickEditPane(x, y); ok {
		return next, cmd
	}
	if pt, ok := m.transcriptPoint(x, y); ok {
		if !m.clickableTranscript() {
			return m, nil
		}
		// On the start screen the rows are the offers, and an offer names
		// one line of input that enter already runs (start.go).
		if m.startChoosing() {
			return m.clickOffer(pt.line)
		}
		if next, cmd, ok := m.clickRowOffer(pt); ok {
			return next, cmd
		}
		if next, cmd, ok := m.clickBlockHeading(pt.line); ok {
			return next, cmd
		}
		if next, cmd, ok := m.clickCardStrip(pt); ok {
			return next, cmd
		}
		return m.clickRow(pt.line)
	}
	if next, cmd, ok := m.clickChip(x, y); ok {
		return next, cmd
	}
	return m.clickKey(x, y)
}

// clickableTranscript reports whether the pane under the pointer is a
// transcript whose rows can be opened.
//
// It is every mode the register places over the pane (overlay.go) and nothing
// else, which is one difference from selectableSurface, and the difference is
// the point: reading mode is excluded from selection because the transcript
// is drawn there through a cursor gutter nobody wants on their clipboard, and
// a row click copies nothing — the row is the target and the gutter is not
// part of it. So the pointer opens a row from either side of the handover.
func (m Model) clickableTranscript() bool {
	if !m.ready || m.attachedTo != "" {
		return false
	}
	if o := overlayFor(m.state); o != nil && o.place == placePane {
		return false
	}
	return true
}

// unitAtLine reports which transcript entry a rendered line belongs to, and
// how far into that entry the line is — 0 for the row itself, more for the
// body under it.
//
// It walks the same units the render walks, with the same separators counted
// the same way (steps.go, focus.go), because the only honest way to say which
// entry a line came from is to count the lines the way they were emitted. The
// live streaming tail is not a unit and belongs to no entry, which is right:
// there is nothing there to open yet.
func (m Model) unitAtLine(line int) (idx, offset int, ok bool) {
	u, offset, ok := m.unitAt(line)
	return u.idx, offset, ok
}

// unitAt is unitAtLine with the unit itself, for a caller that needs to know
// whether the line is a card's own rather than one of its rows.
func (m Model) unitAt(line int) (u unit, offset int, ok bool) {
	es := *m.entries()
	focus := m.gutterShowing()
	at := 0
	var prev entry
	havePrev := false
	for _, u := range m.transcriptUnits(es, m.transcriptWidth(), focus, m.focusIdx) {
		if havePrev {
			at += strings.Count(separatorBefore(prev, u.sepBefore), "\n")
		}
		n := strings.Count(u.text, "\n")
		if line >= at && line < at+n {
			return u, line - at, true
		}
		at += n
		prev, havePrev = u.sepAfter, true
	}
	return unit{}, 0, false
}

// clickRow opens the transcript row a rendered line belongs to. It is
// [enter]'s act reached from the other input, so it takes the same branches
// in the same order — a card opens or closes, a diff opens — and a row with nothing
// to open does nothing at all.
//
// What it does not share with [enter] is the *cycle*. The key has one row
// under its cursor and one press to spend, so its three depths are three
// presses of the same key; the pointer names a cell, and a cell says which
// half of the row it landed in. So the pointer spends that instead: the row
// line is the control and toggles, the body under it is the content and
// opens whole (gestureHeader, gestureBody). The reason is the one thing a
// cycle cannot give — a click that opened a row has to be undoable by the
// identical click, and a third press that takes the whole screen is not an
// undo. Nothing becomes pointer-only by it: every depth is still where the
// keyboard left it.
//
// The rows a click can open are narrower than the rows reading mode can put
// its cursor on. A provider failure is selectable because it *offers keys*,
// not because it expands, so a click on it is answered by the offer it
// landed on (clickRowOffer) and a click anywhere else leaves it where it is. A turn's close is the exception:
// its changed-files line names one turn, and the turn's review is what
// enter on the block opens too (openCursorRow) — its other offers stay keys.
func (m Model) clickRow(line int) (tea.Model, tea.Cmd) {
	u, offset, ok := m.unitAt(line)
	if !ok {
		return m, nil
	}
	idx := u.idx
	es := *m.entries()
	if idx < 0 || idx >= len(es) {
		return m, nil
	}
	// A click on anything but a strip glyph takes the strip's cursor off
	// it: the click names the line it landed on, and enter after it acts
	// there (cardopen.go).
	m.strip = stripCursor{}
	if u.closesCard && offset == strings.Count(u.text, "\n")-1 {
		// An open card's closing padding row rides its last unit, but it
		// is the card's line and not the call's: like the rest of the
		// card around its header, a click there does nothing.
		return m, nil
	}
	if u.cardHead {
		// A click on a card's header opens a closed card and closes an
		// open one, as enter does: a run nothing titled is kept on its
		// first call, so the card and that call's row share an index, and
		// the line says which one was meant. The card's other lines — its
		// padding, the sentence, the evidence — are text to read and
		// select, and a click there does nothing
		// (docs/interface/surfaces.md#the-step).
		// See docs/interface/departures.md#only-a-cards-header-answers-a-click.
		blk, ok := m.cardBlockAt(es, idx)
		if !ok || offset != m.cardHeaderOffset(idx) {
			return m, nil
		}
		m.toggleCard(blk, idx)
		m.invalidateRenderCache()
		if m.state == stateFocus {
			m.focusIdx = idx
			m.refreshFocusView()
			return m, nil
		}
		m.viewport.SetLines(m.renderHistoryLines())
		if m.atBottom {
			m.viewport.GotoBottom()
		}
		return m, nil
	}
	if u.group {
		// An open card's group line folds its group and unfolds it; the
		// directories under a group of reads name files rather than an act,
		// and a click there does nothing (cardopen.go).
		if offset > 0 || !m.toggleGroupFold(idx) {
			return m, nil
		}
		m.invalidateRenderCache()
		if m.state == stateFocus {
			m.focusIdx = idx
			m.refreshFocusView()
			return m, nil
		}
		m.viewport.SetLines(m.renderHistoryLines())
		if m.atBottom {
			m.viewport.GotoBottom()
		}
		return m, nil
	}
	if u.call {
		// A call inside an open card is reached for its own view: the
		// click opens it, as enter on the strip opens the call under the
		// strip's cursor, and esc there comes back to the open card. The
		// card's groups already say what each call did, so a row opening
		// in place under its group would be a third depth of the same
		// card (docs/interface/surfaces.md#the-step).
		if m.state == stateFocus {
			m.focusIdx = m.cursorStopFor(es, idx)
		}
		return m.openCallView(idx, m.state)
	}
	g := gestureHeader
	if offset > 0 {
		g = gestureBody
	}
	if u.lines != nil {
		// A card whose footer is a list — the fan-out's, the plan's — has
		// the step card's two depths, and its header opens it and closes it
		// as enter does, where it has more to draw: the fan-out's reports,
		// the plan's steps past its ceiling, and at low everything under the
		// header. Nothing folds it to its header alone. A child's row is the
		// lane, and does what the lane's click did: it opens the reports
		// and closes them again; a line of a report is the report, and opens
		// it whole. A plan's step row is text, as enter has no act on it:
		// the step's own card further down is where its work is. The rest
		// of the card is text, and a click there does nothing.
		// See docs/interface/departures.md#a-fan-out-childs-row-answers-the-click-its-lane-did.
		role := components.CardLineText
		if offset < len(u.lines) {
			role = u.lines[offset]
		}
		switch {
		case role == components.CardLineHeader:
			if !m.rowExpands(es[idx]) {
				return m, nil
			}
			es[idx].expanded = !es[idx].expanded
			m.invalidateRenderCache()
			if m.state == stateFocus {
				if m.selectableRow(es[idx]) {
					m.focusIdx = idx
				}
				m.refreshFocusView()
				return m, nil
			}
			m.viewport.SetLines(m.renderHistoryLines())
			if m.atBottom {
				m.viewport.GotoBottom()
			}
			return m, nil
		case role == components.CardLineChild && es[idx].kind == entryFanout:
			g = gestureHeader
		case role == components.CardLineReport:
			g = gestureBody
		default:
			return m, nil
		}
	}
	if m.state == stateFocus {
		// Inside reading mode the cursor is the reader's place in the rows, so
		// it goes to the row they pointed at — before the row is opened rather
		// than after, because a body that takes the screen returns to this
		// cursor when it closes. A call inside an open card is not a stop of
		// its own, so the cursor goes to the line of its group.
		m.focusIdx = m.cursorStopFor(es, idx)
	}
	// The turn clicked, not the newest one: the changed-files line is its own
	// target, and a click reaches it without taking the keyboard from the
	// draft. It is the line under the close's first, so it is the block's
	// body, and a body opens whole — here, as the turn's review.
	if turn, ok := m.reviewableRow(idx); ok && es[idx].kind == entryTurnClose && offset == 1 {
		return m.openReview(turn)
	}
	// A sent picture's row opens its card, every click — the same press
	// enter makes (openCursorRow). The row has no open state, so a second
	// click on it opens the card again rather than closing something.
	if _, ok := trayPicture(es[idx]); ok {
		return m.openTrayPicture(idx)
	}
	claimed, full, output := m.toggleRow(idx, g)
	if !claimed {
		if !m.rowExpands(es[idx]) {
			return m, nil
		}
		es[idx].expanded = !es[idx].expanded
	}
	// The row renders differently now, and it may belong to a block the
	// caches have frozen (render.go, focus.go). Both go, before anything
	// draws from them again.
	m.invalidateRenderCache()
	if full != nil {
		// A diff cycled past its expanded mode wants the screen. It is
		// opened from wherever the click came from, so esc comes back there.
		return m.openDiffFull(full, m.state)
	}
	if output {
		// An output body asked for the whole screen the same way.
		return m.openOutputFull(m.rowOutputView(es[idx]), idx, m.state)
	}
	if m.state == stateFocus {
		// Outside reading mode there is no cursor to move, and the click does
		// not make one: taking the keyboard to open a row is the handover
		// reading mode refuses to charge for a glance.
		m.refreshFocusView()
		return m, nil
	}
	m.viewport.SetLines(m.renderHistoryLines())
	if m.atBottom {
		// The row grew underneath itself. A reader pinned to the live end
		// stays pinned to it, which is where the rows it just opened are.
		m.viewport.GotoBottom()
	}
	return m, nil
}

// rowGesture is how a row was reached, because the three ways do not all
// have the same amount to say. The keyboard has one row and one key, so it
// spends presses: gestureCycle is [enter]'s three depths in a row. The
// pointer has a cell, so it spends position instead — gestureHeader is the
// row line, which opens and closes in place and never takes the screen, and
// gestureBody is a line of the body under it, which is content rather than a
// control and so opens that content whole or does nothing.
type rowGesture int

const (
	gestureCycle rowGesture = iota
	gestureHeader
	gestureBody
)

// toggleRow opens or closes whatever structure the row at idx is — a card's
// fold, a diff's three modes, an output body's
// — and reports whether it
// was one of those at all. output reports that the row wants the full screen
// (outputview.go), and full says the same for a diff.
//
// The plain case, a row that simply shows its own body, is left to the
// callers because they disagree about which rows have one: reading mode
// toggles the flag on every row it can put its cursor on, and a click only on
// the rows that expand.
func (m *Model) toggleRow(idx int, g rowGesture) (claimed bool, full *components.DiffView, output bool) {
	es := *m.entries()
	if idx < 0 || idx >= len(es) {
		return false, nil, false
	}
	if g == gestureCycle {
		if blk, ok := m.cardTakesKey(es, idx); ok {
			// Enter opens a closed card onto its calls and closes an open
			// one (docs/interface/surfaces.md#the-step).
			m.toggleCard(blk, idx)
			return true, nil, false
		}
	} else if _, ok := m.cardTakesKey(*m.entries(), idx); ok {
		// The pointer's control on a card is its header, answered before
		// this (clickRow); a line of the card that reaches here is text,
		// and the click does nothing rather than opening something the
		// card shares its index with.
		return true, nil, false
	}
	if d := es[idx].diff; d != nil {
		// A diff row cycles collapsed → expanded → full screen (
		// docs/interface/surfaces.md#the-diff-view).
		switch g {
		case gestureBody:
			// The change itself was clicked, and the change is what the full
			// screen is for.
			d.Mode = components.DiffFull
			d.Offset = 0
			return true, d, false
		case gestureHeader:
			if d.Mode == components.DiffCollapsed {
				d.Mode = components.DiffExpanded
			} else {
				d.Mode = components.DiffCollapsed
			}
			return true, nil, false
		}
		d.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		if d.Mode == components.DiffFull {
			return true, d, false
		}
		return true, nil, false
	}
	if f := es[idx].fold; f != nil && len(f.body) > 0 {
		// A sent paste's row cycles the three depths a tool row does:
		// closed, the bounded window in place, the whole paste full screen
		// (attachments.go). The bytes are the row's own rather than the
		// evidence store's, so the third depth is offered whenever the
		// window did not hold all of them.
		switch {
		case g == gestureBody:
			if es[idx].expanded && trayOverflows(es[idx]) {
				return true, nil, true
			}
		case !es[idx].expanded:
			es[idx].expanded = true
		case g == gestureCycle && trayOverflows(es[idx]):
			return true, nil, true
		default:
			es[idx].expanded = false
		}
		return true, nil, false
	}
	if es[idx].kind == entryFanout && m.fanoutOpens(es[idx]) {
		// A settled fan-out block folds its children's reports, bounded when
		// open, and cycles the three depths a paste does: the whole report
		// full screen is offered only where the bound held some of it back
		// (fanout.go).
		switch {
		case g == gestureBody:
			if es[idx].expanded && m.fanoutOverflows(es[idx]) {
				return true, nil, true
			}
		case !es[idx].expanded:
			es[idx].expanded = true
		case g == gestureCycle && m.fanoutOverflows(es[idx]):
			return true, nil, true
		default:
			es[idx].expanded = false
		}
		return true, nil, false
	}
	if lines := outputLines(es[idx]); len(lines) > 0 {
		// A tool or command row with a body cycles the diff's three depths
		// too (docs/interface/surfaces.md#the-activity-row): closed, the
		// in-place window, the whole output full screen. The full step is
		// skipped when the window already shows everything — a press that
		// changes nothing is a press wasted, the same judgement the think
		// row's tail makes, and the same one that leaves a body click on a
		// body already showing whole with nothing to do.
		switch {
		case g == gestureBody:
			if es[idx].opensFullOutput(lines) {
				return true, nil, true
			}
		case !es[idx].expanded:
			es[idx].expanded = true
		case g == gestureCycle && es[idx].opensFullOutput(lines):
			return true, nil, true
		default:
			es[idx].expanded = false
		}
		return true, nil, false
	}
	if g == gestureBody {
		// Whatever the body under this row is — a running command's live
		// tail, a result that has not arrived — it is not a control, and a
		// click on it must not fall through to the plain toggle and close the
		// row the reader was reading.
		return true, nil, false
	}
	return false, nil, false
}

// clickKey answers a click on a decision key.
//
// The card is found by rendering the screen and reading the row the pointer
// is on, rather than by working out where the panel starts: the card rides
// above a live frame in one state and fills the panel in another, and
// the arithmetic that decides which is exactly what P3-7 replaces. What was
// drawn is the only thing a click can honestly be resolved against.
func (m Model) clickKey(x, y int) (tea.Model, tea.Cmd) {
	card := m.decisionCard()
	if card == nil {
		return m, nil
	}
	lines := strings.Split(m.screen(), "\n")
	if y < 0 || y >= len(lines) {
		return m, nil
	}
	if m.decisionUngated() {
		// The card is on screen with the draft holding the keyboard, so the
		// one key it draws is the handover and that is the only thing on it
		// a click can mean. It means what the chord means: the card gets the
		// keyboard, the decision stays waiting, and the next click answers
		// it. Nothing about a decision is decided by a gesture the surface
		// has not first said is live.
		if card.HandoverAt(lines[y], x) {
			return m.gateDecision()
		}
		return m, nil
	}
	key, ok := card.KeyAt(lines[y], x)
	if !ok {
		return m, nil
	}
	if m.graceShowing() && m.graceDiscards(key) {
		// The screen says the keys are a moment from live (interrupt.go);
		// a click on the dimmed run means no more than the key it stands
		// for would.
		return m, nil
	}
	return m.routeDecision(clickKeyPress(key))
}

// clickRowOffer answers a click on the offers a recovery or round-limit row
// draws, by clickKey's rule extended to the row. Where the row's letters are
// live — reading mode's cursor on it — a click on an offer is that offer's
// letter, handed to the row's own dispatch (rowKey), so the key and the
// pointer are one handler. Where they are not, the offers are grey and the
// only live key on the row is the handover beside them, so that is the only
// thing on it a click can mean: it hands the row the keyboard, as the chord
// does, and the next click answers it. A grey offer is not an offer yet, and
// a click on one is nothing — as a key pressed at the prompt would be a
// letter.
//
// The offer is found by reading the line that was drawn, as the card's keys
// are, so a key on the screen is clickable by construction rather than by
// upkeep. ok is false wherever the click did not land on one of those keys,
// which leaves the row to clickRow.
func (m Model) clickRowOffer(pt selPoint) (tea.Model, tea.Cmd, bool) {
	idx, _, found := m.unitAtLine(pt.line)
	if !found || m.attachedTo != "" || idx < 0 || idx >= len(m.transcript) ||
		pt.line < 0 || pt.line >= len(m.viewport.lines) {
		return m, nil, false
	}
	e := m.transcript[idx]
	if !m.offersRowKeys(e) {
		return m, nil, false
	}
	plain := ansi.Strip(m.viewport.lines[pt.line])
	if m.state == stateFocus && m.focusIdx == idx {
		for _, o := range m.entryOffers(e) {
			letter, ok := offerLetter(o)
			if ok && markAt(plain, o.Key, pt.col) {
				return m.rowLetter(letter)
			}
		}
		return m, nil, false
	}
	sel := rowUnselected
	if m.pointerLit() && m.focusIdx == idx {
		sel = rowPointed
	}
	if h := m.rowHandover(e, sel); h != "" && markAt(plain, "["+h+"]", pt.col) {
		next, cmd := m.giveRowKeyboard(idx)
		return next, cmd, true
	}
	return m, nil, false
}

// entryOffers is every offer a row the handover reaches makes, as its own
// builder states it.
func (m Model) entryOffers(e entry) []components.KeyOffer {
	switch e.kind {
	case entryRoundPause:
		if e.pause != nil {
			return e.pause.keys()
		}
	case entryRewound:
		return m.rewoundOffers(e)
	}
	return m.rowOffers(e)
}

// offerLetter is the keystroke an offer a row draws stands for. The pause
// draws its grant as the block it grants (`[+50]`), not as the `+` that
// takes it, so that one is read by its opening rather than its whole.
func offerLetter(o components.KeyOffer) (string, bool) {
	for _, b := range []keys.Binding{keys.Row.Retry, keys.Row.Continue, keys.Row.Key,
		keys.Row.Provider, keys.Row.Rounds, keys.Row.Uncap} {
		if o.Key == keys.Bracket(b) {
			return keys.Shown(b), true
		}
	}
	if strings.HasPrefix(o.Key, "[+") {
		return keys.Shown(keys.Row.Rounds), true
	}
	return "", false
}

// markAt reports whether display column col of a plain line is inside the
// bracketed mark wherever the line draws it.
func markAt(plain, mark string, col int) bool {
	i := strings.Index(plain, mark)
	if i < 0 {
		return false
	}
	lo := ansi.StringWidth(plain[:i])
	return col >= lo && col < lo+ansi.StringWidth(mark)
}

// decisionCard is the approval card on screen, if the surface showing one is
// that card at all. The plan card and the memory proposal both take
// typed input rather than a letter, so neither draws a run of keys for a
// pointer to land in; they keep their keyboard and are unchanged.
func (m Model) decisionCard() *components.ApprovalCard {
	if m.state == stateConfirmRun {
		if m.memoryAsk != nil {
			return nil
		}
		return m.approvalCard()
	}
	// The scaffold card draws the same run of keys, and a card that answers
	// a keystroke has to answer the pointer on the key that stands for it
	// (scaffold.go).
	if m.state == stateScaffold {
		return m.scaffoldCard()
	}
	if m.state == stateSetup {
		return m.setupCard()
	}
	if m.state == stateToolchainDraft {
		return m.toolchainDraftCard()
	}
	if m.state == stateHandoff {
		return m.handoffCard()
	}
	if m.state == stateProposal && m.patterns.card != nil && m.patterns.card.ask == nil {
		return m.proposalApprovalCard()
	}
	if ask := m.activeChildAsk(); ask != nil {
		return m.childAskCard(ask)
	}
	return nil
}

// clickKeyPress is the clicked key as the keystroke it stands for. Every key
// on the run is one printable character, and a key message carrying its own
// text is what the register matches against.
func clickKeyPress(key string) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: []rune(key)[0], Text: key}
}
