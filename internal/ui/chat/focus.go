package chat

// Focus mode (S-076, DESIGN-TUI.md §7): ctrl+e gives the transcript a
// selection cursor over expandable rows (tool and command output). j/k moves
// between them, enter expands/collapses the selected row in place, and esc
// returns to the input. This is the one mechanism behind "[enter] expand"
// everywhere in the transcript, so the input textarea keeps all other keys.

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// expandable reports whether a transcript entry has bounded output that focus
// mode can expand.
func expandable(e entry) bool {
	return e.kind == entryTool || e.kind == entryCommand || e.kind == entryDiff
}

// selectable reports whether focus mode can put its cursor on an entry. It is
// expandable plus the rows that offer keys without expanding: a turn's close
// block is passive, but [v] and [u] are handled on it (S-098, §16), and so are
// a provider failure's own keys (S-106, §17a) and a round-limit pause's
// (S-109).
func selectable(e entry) bool {
	return expandable(e) || e.kind == entryTurnClose || e.kind == entryFailure ||
		e.kind == entryStreamDrop || e.kind == entryRoundPause
}

// expandableIndices lists the transcript indices focus mode can select,
// scoped to whichever agent's transcript the surface renders (S-077). Step
// headers are targets too (S-090, §7): j/k steps between headers and rows
// alike, and a folded step offers only its header, since its rows are not on
// screen to select.
func (m Model) expandableIndices() []int {
	es := *m.entries()
	var idxs []int
	for _, blk := range m.blocksOf(es) {
		if blk.step != nil {
			if blk.step.queued() {
				// A declared step nobody has started is a header with no rows
				// and no entry behind it: nothing to select, nothing to
				// expand (S-104).
				continue
			}
			idxs = append(idxs, blk.step.titleIdx)
			if m.headerFor(blk, es).Folded {
				continue
			}
			// A folded group offers its group row, not the rows inside it
			// (S-091, §13c).
			for _, sl := range m.stepSlots(es, blk.step) {
				if selectable(es[sl.idx]) {
					idxs = append(idxs, sl.idx)
				}
			}
			continue
		}
		start, end := blk.members()
		for i := start; i < end; i++ {
			if selectable(es[i]) {
				idxs = append(idxs, i)
			}
		}
	}
	return idxs
}

// enterFocusMode starts focus mode on the most recent expandable row — or on
// the failure that ended the turn, when there is one. The close rows that
// follow a broken turn are chrome about it (S-098); the row that broke it is
// the one holding the way out (S-106), so that is where the cursor belongs.
//
// A transcript with rows but nothing expandable in them still opens, without
// a cursor (S-115): what is on screen is prose, and prose is read rather than
// navigated. Refusing there was the old answer and it left the reader in the
// input box with nowhere to go. An empty transcript is the one case with
// nothing to open onto, and it still says so.
func (m Model) enterFocusMode() (tea.Model, tea.Cmd) {
	if len(*m.entries()) == 0 {
		if m.startScreenShowing() {
			// First contact (§17c) is the one screen that is visibly empty,
			// and it is the screen that advertises these keys. A notice here
			// would be noise and would spend the screen to say it (S-115).
			return m, nil
		}
		const notice = "Nothing to focus yet — tool and command rows become expandable."
		if m.attachedTo != "" {
			m.noteChild(m.attachedTo, notice)
		} else {
			m.appendEntry(entry{kind: entrySystem, text: notice})
		}
		m.viewport.SetContent(m.renderHistory())
		m.viewport.GotoBottom()
		return m, nil
	}
	m.enterSurface(stateFocus)
	idxs := m.expandableIndices()
	if len(idxs) == 0 {
		m.focusIdx = -1
		m.invalidateRenderCache()
		m.viewport.SetContent(m.renderHistory())
		return m, nil
	}
	m.focusIdx = idxs[len(idxs)-1]
	es := *m.entries()
	for i := len(idxs) - 1; i >= 0; i-- {
		if es[idxs[i]].kind == entryTurnClose {
			continue
		}
		// A drop row sits under the failure that caused it and holds the
		// better offer of the two, so it is the one the cursor lands on
		// (S-107).
		if k := es[idxs[i]].kind; k == entryFailure || k == entryStreamDrop {
			m.focusIdx = idxs[i]
		}
		break
	}
	m.refreshFocusView()
	return m, nil
}

// updateFocus handles keys while focus mode is active. Esc never destroys:
// it only returns to the input, keeping any expansion state.
func (m Model) updateFocus(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch pressed := msg.String(); {
	case keys.Is(pressed, keys.Draft.Quit):
		m.quitting = true
		return m, m.quitCmd()
	case keys.Is(pressed, keys.Reading.Back):
		return m.exitFocusMode()
	case keys.Is(pressed, keys.Reading.Move):
		// One binding, both directions: the bar offers `j/k` as a pair and
		// the dispatch reads which half was pressed, so the four keystrokes
		// the mode moves on are declared in one place (§7d). Two bindings
		// for one offer would put the same keystroke on the surface twice,
		// which is the thing the register refuses.
		if pressed == "j" || pressed == "down" {
			m.moveFocus(1)
		} else {
			m.moveFocus(-1)
		}
		return m, nil
	case keys.Is(pressed, keys.Reading.List):
		// The register on the page (§7d). It is the same key the supporting
		// TUIs have offered since S-127, answering the same question about
		// the surface that holds the keyboard — and it is live here for the
		// reason every bare letter on this bar is: nothing else is listening.
		m.readingKeyList = !m.readingKeyList
		// The list is taller than the bar it replaced, so the panel takes
		// rows from the transcript and gives them back — the same accounting
		// every other bottom panel does (§12e).
		m.syncViewport()
		m.refreshFocusView()
		return m, nil
	case keys.Is(pressed, keys.Row.Review, keys.Row.Undo, keys.Row.Rounds, keys.Row.Uncap):
		// A round-limit pause offers all four on its own row (S-109); it is
		// asked first because it stands where the close block would be.
		if next, cmd, claimed := m.roundPauseKey(pressed); claimed {
			return next, cmd
		}
		// The offers on a turn's changeset row (S-098, §16), which are [v]
		// and [u] and no others. They are handled here rather than globally,
		// so the input keeps both keys. The switch names them both rather
		// than treating "not [v]" as [u]: the pause's other two keys reach
		// this line whenever the cursor is on a close row, and a key a row
		// does not offer has to fall through to the draft, not land on
		// whichever offer happened to be last.
		if e, ok := m.focusedClose(); ok && e.close.Changes != nil {
			switch {
			case keys.Is(pressed, keys.Row.Review):
				// Review mode is a takeover opened from the row (S-099);
				// esc comes back here, to the row that offered it.
				return m.openReview(e.turn)
			case keys.Is(pressed, keys.Row.Undo):
				// Undo asks before it writes (S-100). The confirm borrows the
				// bottom panel and focus mode keeps the screen, so the cursor
				// stays on the row that offered it and esc comes back here.
				return m.undoTurn(e.turn, nil)
			}
		}
		// The row under the cursor does not offer this key, so it is a
		// character like any other and goes back to the draft (S-115).
		return m.returnToInput(msg)
	case keys.Is(pressed, keys.Row.Retry, keys.Row.Continue, keys.Row.Key, keys.Row.Provider):
		// A provider failure's own offers (S-106, §17a), and a dropped
		// stream's (S-107). Like the changeset row's, they are handled here
		// rather than globally, so the input keeps every one of these letters
		// for typing — which is also why continuing from a partial is [c]
		// rather than the artboard's [enter].
		if next, cmd, claimed := m.dropKey(pressed); claimed {
			return next, cmd
		}
		if next, cmd, claimed := m.failureKey(pressed); claimed {
			return next, cmd
		}
		return m.returnToInput(msg)
	case keys.Is(pressed, keys.Reading.Detail):
		// The step around the cursor opens its rows' detail (S-137, §13d) —
		// the header the cursor is on, or the step the row under it belongs
		// to. A cursor outside every step has nothing to open, and the hint
		// bar has already said so with its reason beside it rather than
		// leaving the chord to fail without a word.
		return m.detailFromReading()
	case keys.Is(pressed, keys.Reading.Collapse):
		// The explicit half of [enter]'s toggle (§7a). Where the row under
		// the cursor has nothing open, [-] is a character like any other and
		// goes back to the draft.
		if m.collapseFocused() {
			m.refreshFocusView()
			return m, nil
		}
		return m.returnToInput(msg)
	case keys.Is(pressed, keys.Reading.PageUp):
		m.scrollPage(-1)
		return m, nil
	case keys.Is(pressed, keys.Reading.PageDown):
		m.scrollPage(1)
		return m, nil
	case keys.Is(pressed, keys.Reading.Expand):
		es := *m.entries()
		if m.focusIdx >= 0 && m.focusIdx < len(es) {
			if _, ok := m.stepBlockAt(es, m.focusIdx); ok {
				// Enter on a step header folds or unfolds the whole group in
				// place (S-090, §13b).
				m.toggleStepFold(m.focusIdx)
				m.refreshFocusView()
				return m, nil
			}
			if m.groupAnchor(es, m.focusIdx) {
				// Enter on a folded group restores its rows in place, and
				// folds them back again (S-091, §13c).
				m.toggleGroupFold(m.focusIdx)
				m.refreshFocusView()
				return m, nil
			}
			if d := es[m.focusIdx].diff; d != nil {
				// A diff row cycles collapsed → expanded → full screen
				// (S-074, DESIGN-TUI.md §3b).
				d.Update(msg)
				if d.Mode == components.DiffFull {
					return m.openDiffFull(d, stateFocus)
				}
			} else {
				es[m.focusIdx].expanded = !es[m.focusIdx].expanded
			}
		}
		m.refreshFocusView()
		return m, nil
	}
	// Typing is the other way out (S-115, §7a). The letters above are focus
	// mode's own and stay its own; every other printable character hands the
	// keyboard back and lands in the draft, so a reader who forgot which
	// pane they were in loses a mode rather than a sentence.
	if typedRune(msg) {
		return m.returnToInput(msg)
	}
	// Anything left is chrome — the viewport keeps it.
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

// focusedClose returns the turn-close entry the cursor is on, if it is on
// one. The close rows live in the session's own transcript, so an attached
// child's feed never offers them (S-077).
func (m Model) focusedClose() (entry, bool) {
	if m.attachedTo != "" || m.focusIdx < 0 || m.focusIdx >= len(m.transcript) {
		return entry{}, false
	}
	e := m.transcript[m.focusIdx]
	if e.kind != entryTurnClose || e.close == nil {
		return entry{}, false
	}
	return e, true
}

// exitFocusMode returns to the input, keeping expansion state; the render
// cache is rebuilt without the selection gutter.
func (m Model) exitFocusMode() (tea.Model, tea.Cmd) {
	// The register closes with the mode: it is a reading of this surface, and
	// the next time reading mode opens the question has not been asked yet.
	m.readingKeyList = false
	m.leaveSurface()
	m.invalidateRenderCache()
	m.syncViewport()
	m.viewport.SetContent(m.renderHistory())
	return m, nil
}

// moveFocus selects the next (+1) or previous (-1) expandable row. With
// nothing to select the transcript is being read rather than navigated, so
// the key is a line of scroll instead (S-115).
func (m *Model) moveFocus(dir int) {
	idxs := m.expandableIndices()
	if len(idxs) == 0 {
		m.scrollLines(dir)
		return
	}
	for pos, idx := range idxs {
		if idx == m.focusIdx {
			if next := pos + dir; next >= 0 && next < len(idxs) {
				m.focusIdx = idxs[next]
			}
			break
		}
	}
	m.refreshFocusView()
}

// refreshFocusView re-renders the transcript with the selection gutter and
// scrolls the selected row into view.
func (m *Model) refreshFocusView() {
	content, start, count := m.renderFocusHistory()
	m.viewport.SetContent(content)
	if m.focusIdx < 0 {
		// No cursor to keep on screen: where the reader scrolled to is where
		// they meant to be (S-115).
		return
	}
	switch {
	case start < m.viewport.YOffset():
		m.viewport.SetYOffset(start)
	case start+count > m.viewport.YOffset()+m.viewport.Height():
		m.viewport.SetYOffset(start + count - m.viewport.Height())
	}
}

// renderFocusHistory renders every entry with a two-column gutter on
// expandable rows (❯ on the selected one) and reports the selected block's
// first line and line count for scrolling. It bypasses the incremental cache.
func (m *Model) renderFocusHistory() (content string, selStart, selCount int) {
	// Focus mode is a way of reading the transcript, not a takeover surface,
	// so it wraps to the transcript pane like the ordinary feed (S-092).
	w := m.transcriptWidth()
	es := *m.entries()
	var b strings.Builder
	line := 0
	var prev entry
	havePrev := false
	for _, u := range m.transcriptUnits(es, w, true, m.focusIdx) {
		// The separator counts toward the line total before the unit starts,
		// so the gutter pointer and scrolling stay aligned.
		if havePrev {
			sep := separatorBefore(prev, u.sepBefore)
			b.WriteString(sep)
			line += strings.Count(sep, "\n")
		}
		n := strings.Count(u.text, "\n")
		if u.idx == m.focusIdx {
			selStart, selCount = line, n
		}
		b.WriteString(u.text)
		line += n
		prev, havePrev = u.sepAfter, true
	}
	return b.String(), selStart, selCount
}

// gutterPrefix indents a rendered block by two columns, placing the focus
// pointer on the first line of the selected block and lighting that line.
//
// The two things reading mode dresses are the rail and this (§7a). The row
// under the cursor takes the focus background across its full width with its
// words in bright; the rail and the glyph keep their colours inside the
// highlight, so a row that changed the machine still says so while it is lit
// (§14). The pointer sits outside the highlight, in its own column, because
// it points at the row rather than belonging to it.
//
// width is what the block was rendered at — the pointer column is not part
// of it, which is what makes the highlight end at the pane's edge.
func gutterPrefix(block string, selected bool, width int) string {
	lines := strings.Split(block, "\n")
	for i, l := range lines {
		if l == "" {
			continue
		}
		if i == 0 && selected {
			lines[i] = sty.FocusMarker.Render("❯") + " " + components.LitRow(l, 0, width)
		} else {
			lines[i] = "  " + l
		}
	}
	return strings.Join(lines, "\n")
}

// renderFocusHint replaces the input frame while reading mode holds the
// keyboard (S-115, S-122, §7a). It is the other half of the reading rail: the
// rail says which pane has the keyboard, this says what the keyboard does
// there. Its two lines are assembled in readinghint.go.
func (m Model) renderFocusHint() string {
	lines := m.focusHintLines()
	for len(lines) < inputHeight {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

// focusHintLines is the bar's content, which is also what the bottom panel is
// sized from (approval.go) — one list, so the panel a reader gets is the
// panel the layout paid for.
func (m Model) focusHintLines() []string {
	width := m.contentWidth()
	if m.readingKeyList {
		return m.readingKeyListLines(width, m.maxConfirmPanelHeight())
	}
	lines := []string{m.readingKeyLine(width)}
	return append(lines, m.readingRowLines(width, inputHeight-1)...)
}
