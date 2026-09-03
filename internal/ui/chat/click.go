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
// Four things are targets, and the test they pass is the same one four times:
// the pointer names exactly one of them, and the thing it names already has a
// key.
//
//   - An activity row. Its whole width is one row, and [enter] under reading
//     mode's cursor already opens it. The row line opens and closes it; the
//     body under the row opens that body whole (clickRow).
//   - The approval card's decision run. Each key owns its own cells inside
//     `[y/N/a]`, and the click is delivered as the keystroke.
//   - A file on the rail. It names one path, and `/diff <path>` opens that
//     path's diff by name (railclick.go).
//   - A session on the rail. It names one session, and the chord that walks
//     the map and the manager's [enter] both attach to it.
//
// Everything else on the screen fails that test. Prose under the pointer is a
// selection surface first and has no single act behind it; the scroll gutter
// is a shape rather than a control; the rail's headings and its readings —
// the summary, the plan, the todo list, the tool sources and the two meters —
// name blocks and numbers rather than things to go to; a chip's `✕` would be a
// button with no keyboard equal, and a target only the mouse can reach is a
// target half the readers do not have.
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
// half of the row it landed in instead. A modifier would have been the other
// way to say it, and it is not available here: every terminal worth naming
// keeps shift-click for its own selection, and hands the application
// nothing.

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/ui/components"
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
	m.press = pointerPress{x: x, y: y, live: true}
}

// endClick reports whether a release completes a click, and forgets the press
// either way. A release anywhere but the cell the press landed in is a drag,
// and the drag's own release is what answers it.
func (m *Model) endClick(x, y int) bool {
	p := m.press
	m.press = pointerPress{}
	return p.live && p.x == x && p.y == y
}

// clickAt resolves a click to the one thing under it. The rail is asked
// first and the pane second, because those are the halves of the screen a
// coordinate can be checked against without rendering anything; the card is
// what is left, and it is the one that has to be drawn to be found.
func (m Model) clickAt(x, y int) (tea.Model, tea.Cmd) {
	if next, cmd, ok := m.clickRail(x, y); ok {
		return next, cmd
	}
	if pt, ok := m.transcriptPoint(x, y); ok {
		if !m.clickableTranscript() {
			return m, nil
		}
		return m.clickRow(pt.line)
	}
	return m.clickKey(x, y)
}

// clickableTranscript reports whether the pane under the pointer is a
// transcript whose rows can be opened.
//
// It is selectableSurface's list with one difference, and the difference is
// the point: reading mode is excluded from selection because the transcript
// is drawn there through a cursor gutter nobody wants on their clipboard, and
// a row click copies nothing — the row is the target and the gutter is not
// part of it. So the pointer opens a row from either side of the handover.
func (m Model) clickableTranscript() bool {
	if !m.ready || m.attachedTo != "" {
		return false
	}
	switch m.state {
	case stateDiffFull, stateOutputFull, statePreview, stateReview, stateContext, statePersona:
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
	es := *m.entries()
	focus := m.state == stateFocus
	at := 0
	var prev entry
	havePrev := false
	for _, u := range m.transcriptUnits(es, m.transcriptWidth(), focus, m.focusIdx) {
		if havePrev {
			at += strings.Count(separatorBefore(prev, u.sepBefore), "\n")
		}
		n := strings.Count(u.text, "\n")
		if line >= at && line < at+n {
			return u.idx, line - at, true
		}
		at += n
		prev, havePrev = u.sepAfter, true
	}
	return 0, 0, false
}

// clickRow opens the transcript row a rendered line belongs to. It is
// [enter]'s act reached from the other input, so it takes the same branches
// in the same order — a step header folds its group, a folded run gives its
// rows back, a diff opens — and a row with nothing to open does nothing at
// all.
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
// its cursor on. A turn's close block and a provider failure are selectable
// because they *offer keys*, not because they expand, and a
// pointer has no way to say which of `[v]` and `[u]` it meant. So those keep
// their cursor and lose nothing: the keys are still where they were.
func (m Model) clickRow(line int) (tea.Model, tea.Cmd) {
	idx, offset, ok := m.unitAtLine(line)
	if !ok {
		return m, nil
	}
	es := *m.entries()
	if idx < 0 || idx >= len(es) {
		return m, nil
	}
	g := gestureHeader
	if offset > 0 {
		g = gestureBody
	}
	if m.state == stateFocus {
		// Inside reading mode the cursor is the reader's place in the rows, so
		// it goes to the row they pointed at — before the row is opened rather
		// than after, because a body that takes the screen returns to this
		// cursor when it closes.
		m.focusIdx = idx
	}
	claimed, full, output := m.toggleRow(idx, g)
	if !claimed {
		if !expandable(es[idx]) {
			return m, nil
		}
		es[idx].expanded = !es[idx].expanded
	}
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
	m.invalidateRenderCache()
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

// toggleRow opens or closes whatever structure the row at idx is — a step
// header's group, a folded run of read-only calls, a think row's three
// depths, a diff's three modes, an output body's — and reports whether it
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
	if _, ok := m.stepBlockAt(es, idx); ok {
		// A step header folds or unfolds the whole group in place (
		// step folding).
		m.toggleStepFold(idx)
		return true, nil, false
	}
	if m.groupAnchor(es, idx) {
		// A folded group restores its rows in place, and folds them back
		// again.
		m.toggleGroupFold(idx)
		return true, nil, false
	}
	if es[idx].kind == entryThink {
		// A think row cycles its three depths the way a diff cycles its three
		// modes (think.go), and for the same reason: the middle one is what a
		// glance wants and the whole block is what a read does. All three are
		// in place, so there is no deeper surface for a body click to open —
		// the thought is prose, and prose under the pointer is read rather
		// than pressed. The cycle wraps, so the row line alone still reaches
		// every depth and closes it again.
		if g == gestureBody {
			return true, nil, false
		}
		m.cycleThink(idx)
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
			if len(lines) > maxExpandedResultLines {
				return true, nil, true
			}
		case !es[idx].expanded:
			es[idx].expanded = true
		case g == gestureCycle && len(lines) > maxExpandedResultLines:
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
	key, ok := card.KeyAt(lines[y], x)
	if !ok {
		return m, nil
	}
	if m.decisionUngated() {
		// The card is on screen with its keys drawn not-yet-live and the
		// draft holding the keyboard. A click that answered anyway
		// would be answering keys the screen says nobody can press, so it
		// means what the handover means instead: the card gets the keyboard,
		// the decision stays waiting, and the second click answers it. Nothing
		// about a decision is decided by a gesture the surface has not first
		// said is live.
		return m.gateDecision()
	}
	if m.graceShowing() && m.graceDiscards(key) {
		// The screen says the keys are a moment from live (interrupt.go);
		// a click on the dimmed run means no more than the key it stands
		// for would.
		return m, nil
	}
	return m.routeDecision(clickKeyPress(key))
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
