package chat

// The staged strip as a door (docs/interface/surfaces.md#a-staged-attachment).
//
// A screenshot pasted halfway through a sentence becomes a chip above the
// draft, and until now the only ways to look at it or take it back were two
// slash commands — which are read only as the first word of the whole draft,
// so they were out of reach at exactly the moment they were wanted. Reading
// mode already keeps the draft and hands the keyboard to the transcript, so
// the strip is the last thing its cursor reaches: `j` off the last row lands
// on it, the arrows pick a chip, enter opens it, `x` drops it, and esc goes
// back to the sentence as it was.
//
// Nothing is printed on a chip to say so. The keys are the mode's, live
// because the mode holds the keyboard, and the mode's own bar is where they
// are offered
// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
// A click on a chip is the pointer twin of enter on it.
//
// The cursor's place is an index into the staging area, which is the one
// list of what is staged; a chip is found by where it stands in that list and
// never by its name, so anything that later points at a chip — a fold in the
// sentence — points at the same index the strip does.

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// stagedDoor is the state the staging area's doors keep between keys.
type stagedDoor struct {
	// onStrip is reading mode's cursor standing on the strip rather than on
	// a transcript row, and chip the chip it stands on — an index into the
	// staging area.
	onStrip bool
	chip    int
	// back is that the card or reader up now was opened from the strip, so
	// its way out is back to the strip rather than to the draft.
	back bool
	// row is that the card up now was opened from a sent message's fold row
	// in reading mode, so its way out is back to that row (foldPicture).
	row bool
	// shows is the staged attachment the card is showing, which is what the
	// card's own drop takes out.
	shows provider.Attachment
	// opened is the strip cell a click opened a chip from, which the same
	// click closes (click.go).
	opened railOpening
}

// stripReachable reports whether there is a strip for the cursor to reach:
// something staged, in the session's own staging area. Attached, the
// keyboard is pointed at a child and the orchestrator's strip is not drawn
// (stagedRail).
func (m Model) stripReachable() bool {
	return m.attachedTo == "" && len(m.attachments) > 0
}

// atStrip reports whether reading mode's cursor is on the strip. A transcript
// row the cursor was put on by anything else — a click, a search walking to
// a match — wins, because a lit row and a lit chip at once is two cursors.
func (m Model) atStrip() bool {
	return m.staged.onStrip && m.focusIdx < 0 && m.stripReachable()
}

// pickedChip is the chip the cursor stands on, or -1 where it is not on the
// strip, clamped against a staging area that has moved under it.
func (m Model) pickedChip() int {
	if !m.atStrip() {
		return -1
	}
	return min(max(m.staged.chip, 0), len(m.attachments)-1)
}

// stripRow is the strip as reading mode draws it — the chip under the cursor
// picked — and the cells each chip was drawn in. It is the same chips the
// frame's staged rail draws, at the same width, so the strip does not change
// shape when the keyboard moves to it.
func (m Model) stripRow() (string, []components.ChipHit) {
	if !m.stripReachable() {
		return "", nil
	}
	return components.AttachmentChipsAt(m.attachmentChips(), m.contentWidth(), m.pickedChip())
}

// lastRow is the transcript row the cursor stands on when it leaves the strip
// upwards: the last one it can stand on, or none.
func (m Model) lastRow() int {
	if idxs := m.expandableIndices(); len(idxs) > 0 {
		return idxs[len(idxs)-1]
	}
	return -1
}

// enterStrip puts the cursor on the strip, on the chip it last stood on. The
// transcript is redrawn with no row lit.
func (m *Model) enterStrip() {
	m.staged.onStrip = true
	m.staged.chip = min(max(m.staged.chip, 0), len(m.attachments)-1)
	m.focusIdx = -1
	m.refreshFocusView()
}

// stripMove is moveFocus's half for the strip: `j` off the last transcript
// row lands on it, `k` leaves it back up, and it reports whether it took the
// key. The strip is the last target, so `j` on it goes nowhere; with no row
// above it to stand on, `k` scrolls the transcript the way it does wherever
// there is nothing to select, and the cursor stays where it is.
func (m *Model) stripMove(dir int) bool {
	if !m.stripReachable() {
		return false
	}
	if m.atStrip() {
		if dir < 0 {
			if row := m.lastRow(); row >= 0 {
				m.staged.onStrip = false
				m.focusIdx = row
				m.refreshFocusView()
			} else {
				m.scrollLines(dir)
			}
		}
		return true
	}
	idxs := m.expandableIndices()
	if dir > 0 && (len(idxs) == 0 || m.focusIdx == idxs[len(idxs)-1]) {
		m.enterStrip()
		return true
	}
	return false
}

// settleStrip puts the cursor back on something that exists after the
// staging area changed under it: the chip that took the dropped one's place,
// the one before it when the last went, or the transcript when nothing is
// left.
func (m *Model) settleStrip() {
	if m.state != stateFocus || !m.staged.onStrip {
		return
	}
	if !m.stripReachable() {
		m.staged.onStrip = false
		m.focusIdx = m.lastRow()
	} else {
		m.staged.chip = min(max(m.staged.chip, 0), len(m.attachments)-1)
	}
	m.syncViewport()
	m.refreshFocusView()
}

// updateStrip answers the strip's own keys while the cursor is on it, and
// reports whether it took one. Every other key is the mode's, answered as it
// always is: `k` leaves, `q` goes back, a letter goes to the draft.
func (m Model) updateStrip(msg tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	if !m.atStrip() {
		return m, nil, false
	}
	switch pressed := msg.String(); {
	case keys.Is(pressed, keys.Staged.Pick):
		m.staged.chip = min(max(m.pickedChip()+keys.Step(pressed, keys.Staged.Pick), 0), len(m.attachments)-1)
		return m, nil, true
	case keys.Is(pressed, keys.Staged.Open):
		next, cmd := m.openStripChip(m.pickedChip())
		return next, cmd, true
	case keys.Is(pressed, keys.Staged.Drop):
		next, cmd := m.dropStripChip()
		return next, cmd, true
	case keys.Is(pressed, keys.Staged.Back):
		next, cmd := m.exitFocusMode()
		return next, cmd, true
	}
	return m, nil, false
}

// openStripChip opens one chip the way `/paste show` opens it — the card for
// a picture or a text file, the reader for a paste, the refusal for what
// neither can show — and marks the surface as opened from the strip, so its
// way out comes back here.
func (m Model) openStripChip(i int) (tea.Model, tea.Cmd) {
	if i < 0 || i >= len(m.attachments) {
		return m, nil
	}
	m.staged.onStrip, m.staged.chip, m.focusIdx = true, i, -1
	next, cmd := m.openPreview(m.attachments[i])
	nm, ok := next.(Model)
	if !ok {
		return next, cmd
	}
	switch nm.state {
	case statePreview, statePasteView:
		nm.staged.back = true
	case stateFocus:
		// Refused: the sentence saying why is a transcript row, and the pane
		// it was drawn on is the plain one rather than the cursor's.
		nm.refreshFocusView()
	}
	return nm, cmd
}

// dropStripChip takes the chip under the cursor out of the staging area and
// its fold out of the draft, says what went, and moves the cursor on.
func (m Model) dropStripChip() (tea.Model, tea.Cmd) {
	i := m.pickedChip()
	if i < 0 {
		return m, nil
	}
	note := m.dropStagedAt(i)
	next, cmd := m.systemNotice(note)
	nm := next.(Model)
	nm.settleStrip()
	return nm, cmd
}

// dropStagedAt takes one staged attachment out, with its fold in the draft,
// and is the sentence saying what went. Every per-chip drop — the verb by
// name, the strip's key, the card's — goes through it, so they say it alike.
func (m *Model) dropStagedAt(i int) string {
	a := m.attachments[i]
	// A full slice expression, because the staged set is handed off whole by
	// takeAttachments and must not be shortened through a shared array.
	m.attachments = append(m.attachments[:i:i], m.attachments[i+1:]...)
	m.dropPasteToken(a)
	m.syncViewport()
	return "dropped " + describeStaged(a)
}

// stagedIndex is where one attachment stands in the staging area now, or -1
// when it has left — matched by handle and name together, since a surface
// can outlive the chip it was opened on.
func (m Model) stagedIndex(a provider.Attachment) int {
	for i, s := range m.attachments {
		if s.Handle == a.Handle && strings.EqualFold(s.Name, a.Name) {
			return i
		}
	}
	return -1
}

// leaveStagedSurface hands the pane back from the card or the reader: to the
// strip, when that is where the reader opened it from, and to the turn
// otherwise.
func (m *Model) leaveStagedSurface() {
	if !m.staged.back {
		m.leaveSurface()
		m.syncViewport()
		return
	}
	m.staged.back = false
	m.state = stateFocus
	m.staged.onStrip, m.focusIdx = true, -1
	m.settleStrip()
}

// stripKeyLine is reading mode's bar while the cursor is on the strip: the
// strip's own keys, and which chip of how many on the right.
//
// A narrow bar gives up the position first and then the words of the way
// back, the order reading mode's own bar sheds in (readingKeyLine), and never
// cuts a key in half: an offer folded out of sight is one nobody can take.
func (m Model) stripKeyLine(width int) string {
	segs := make([]hintSeg, 0, 4)
	for _, b := range keys.Staged.All() {
		segs = append(segs, seg(b))
	}
	full := joinSegs(segs)
	pos := fmt.Sprintf("chip %d of %d", m.pickedChip()+1, len(m.attachments))
	if gap := width - lipgloss.Width(full) - lipgloss.Width(pos); gap >= 2 {
		return full + strings.Repeat(" ", gap) + sty.Hint.Dim.Render(pos)
	}
	if lipgloss.Width(full) <= width {
		return full
	}
	segs[len(segs)-1] = segAs(keys.Staged.Back, "back")
	return clipRow(joinSegs(segs), width)
}

// chipShowing is the surface a chip opened, while it is the one up.
func chipShowing(m Model) any {
	switch {
	case m.state == statePreview && m.preview != nil:
		return m.preview
	case m.state == statePasteView && m.pasteRead != nil:
		return m.pasteRead
	}
	return nil
}

// closeChipSurface is the remembered cell's close: the surface's own way out,
// whichever of the two a chip opened.
func (m Model) closeChipSurface() (tea.Model, tea.Cmd) {
	if m.state == statePasteView {
		m.closePasteReader()
		return m, nil
	}
	act := m.closePreview()
	m.leaveStagedSurface()
	return m, act.run
}

// clickChip opens the chip under a click, and reports whether the click was
// on one. It is enter on that chip reached from the other input: the card or
// the reader opens, and the cell is remembered so the same click closes it
// again (click.go). The count of chips the row gave up is not a target.
//
// The chip is found on the row the screen drew rather than by working out
// where the strip stands: it is a pre-rail above the frame at the draft and
// the panel's first row in reading mode, and what was drawn is the only
// thing a click can honestly be resolved against.
func (m Model) clickChip(x, y int) (tea.Model, tea.Cmd, bool) {
	if !m.stripReachable() || m.activeChildAsk() != nil {
		return m, nil, false
	}
	switch m.state {
	case stateFocus:
	case stateInput, stateStreaming, stateRunningCmd, stateClassifying:
		if !m.frameShowing() || m.decisionRides() {
			return m, nil, false
		}
	default:
		return m, nil, false
	}
	row, hits := m.stripRow()
	want := strings.TrimSpace(ansi.Strip(row))
	lines := strings.Split(m.screen(), "\n")
	if want == "" || y < 0 || y >= len(lines) {
		return m, nil, false
	}
	plain := ansi.Strip(lines[y])
	at := strings.Index(plain, want)
	if at < 0 || strings.TrimSpace(plain[:at]) != "" || strings.TrimSpace(plain[at+len(want):]) != "" {
		return m, nil, false
	}
	col := x - lipgloss.Width(plain[:at])
	for _, h := range hits {
		if col < h.From || col >= h.To {
			continue
		}
		back := m.state == stateFocus
		next, cmd := m.openStripChip(h.Index)
		nm, ok := next.(Model)
		if !ok {
			return next, cmd, true
		}
		if !back {
			// From the draft the click takes nothing: the surface goes back
			// to the sentence, and no cursor is left on the strip.
			nm.staged.back = false
			nm.staged.onStrip = false
			nm.focusIdx = m.focusIdx
		}
		nm.staged.opened = railOpening{}
		if s := chipShowing(nm); s != nil {
			nm.staged.opened = railOpening{
				pointerPress: pointerPress{x: x, y: y, live: true},
				door:         railDoor{surface: chipShowing, close: Model.closeChipSurface},
				surface:      s,
			}
		}
		return nm, cmd, true
	}
	return m, nil, true
}
