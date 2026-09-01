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
// Two things are targets, and the test they pass is the same one twice: the
// pointer names exactly one of them, and the thing it names already has a
// key.
//
//   - An activity row. Its whole width is one row, and [enter] under reading
//     mode's cursor already opens it.
//   - The approval card's decision run. Each key owns its own cells inside
//     `[y/N/a]`, and the click is delivered as the keystroke.
//
// Everything else on the screen fails that test. Prose under the pointer is a
// selection surface first and has no single act behind it; the scroll gutter
// is a shape rather than a control; a chip's `✕` would be a
// button with no keyboard equal, and a target only the mouse can reach is a
// target half the readers do not have.
//
// Two rules hold the whole file together. **A click never takes the
// keyboard** — reading is not a decision, so a row opened by pointer
// leaves the draft holding every character it had, exactly as the wheel does.
// And **a clicked key is the keystroke**: it goes to the handler the key goes
// to, so there is no second decision path that could answer differently from
// the first.

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

// clickAt resolves a click to the one thing under it. The pane is asked
// first, because that is the half of the screen a coordinate can be checked
// against without rendering anything.
func (m Model) clickAt(x, y int) (tea.Model, tea.Cmd) {
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

// unitAtLine reports which transcript entry a rendered line belongs to.
//
// It walks the same units the render walks, with the same separators counted
// the same way (steps.go, focus.go), because the only honest way to say which
// entry a line came from is to count the lines the way they were emitted. The
// live streaming tail is not a unit and belongs to no entry, which is right:
// there is nothing there to open yet.
func (m Model) unitAtLine(line int) (int, bool) {
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
			return u.idx, true
		}
		at += n
		prev, havePrev = u.sepAfter, true
	}
	return 0, false
}

// clickRow opens the transcript row a rendered line belongs to. It is
// [enter]'s act reached from the other input, so it takes the same branches
// in the same order — a step header folds its group, a folded run gives its
// rows back, a diff cycles its three modes — and a row with nothing to open
// does nothing at all.
//
// The rows a click can open are narrower than the rows reading mode can put
// its cursor on. A turn's close block and a provider failure are selectable
// because they *offer keys*, not because they expand, and a
// pointer has no way to say which of `[v]` and `[u]` it meant. So those keep
// their cursor and lose nothing: the keys are still where they were.
func (m Model) clickRow(line int) (tea.Model, tea.Cmd) {
	idx, ok := m.unitAtLine(line)
	if !ok {
		return m, nil
	}
	es := *m.entries()
	if idx < 0 || idx >= len(es) {
		return m, nil
	}
	claimed, full, output := m.toggleRow(idx)
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
		// An output body cycled past its window wants it the same way.
		return m.openOutputFull(m.rowOutputView(es[idx]), idx, m.state)
	}
	if m.state == stateFocus {
		// Inside reading mode the cursor is the reader's place in the rows, so it
		// goes to the row they pointed at. Outside it there is no cursor to move,
		// and the click does not make one: taking the keyboard to open a row is the
		// handover reading mode refuses to charge for a glance.
		m.focusIdx = idx
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

// toggleRow opens or closes whatever structure the row at idx is — a step
// header's group, a folded run of read-only calls, a think row's three
// depths, a diff's three modes, an output body's — and reports whether it
// was one of those at all. output reports that the row cycled past its
// in-place window and wants the full screen (outputview.go).
//
// The plain case, a row that simply shows its own body, is left to the
// callers because they disagree about which rows have one: reading mode
// toggles the flag on every row it can put its cursor on, and a click only on
// the rows that expand.
func (m *Model) toggleRow(idx int) (claimed bool, full *components.DiffView, output bool) {
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
		// glance wants and the whole block is what a read does.
		m.cycleThink(idx)
		return true, nil, false
	}
	if d := es[idx].diff; d != nil {
		// A diff row cycles collapsed → expanded → full screen (
		// docs/interface/surfaces.md#the-diff-view).
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
		// row's tail makes.
		switch {
		case !es[idx].expanded:
			es[idx].expanded = true
		case len(lines) > maxExpandedResultLines:
			return true, nil, true
		default:
			es[idx].expanded = false
		}
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
