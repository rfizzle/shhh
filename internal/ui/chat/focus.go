package chat

// Focus mode (docs/interface/surfaces.md#reading-mode): ctrl+o gives
// the transcript a selection cursor over expandable rows (tool and command
// output). j/k moves between them, enter expands/collapses the selected row
// in place, and esc returns to the input. This is the one mechanism behind
// "[enter] expand" everywhere in the transcript, so the input textarea keeps
// all other keys.

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// expandable reports whether a transcript entry has bounded output that focus
// mode can expand.
//
// A notice counts when it carries a body: the resumed row's line is the
// account of what the conversation was told and its body is what was actually
// said (reopen.go). A notice that is only its own sentence does not, which is
// every other notice — a cursor stopping on a row with nothing under it is a
// press that does nothing.
func expandable(e entry) bool {
	return e.kind == entryTool || e.kind == entryCommand || e.kind == entryDiff ||
		e.kind == entryThink || e.kind == entrySummary || e.kind == entryTodoRun ||
		(e.kind == entrySystem && len(outputLines(e)) > 0)
}

// selectable reports whether focus mode can put its cursor on an entry. It is
// expandable plus the rows that offer keys without expanding: a turn's close
// block is passive, but [v] and [u] are handled on it, and so are
// a provider failure's own keys and a round-limit pause's — and an assistant
// message, which expands nothing but is what [y] copies as markdown source
// (docs/interface/surfaces.md#reading-mode).
func selectable(e entry) bool {
	return expandable(e) || e.kind == entryTurnClose || e.kind == entryFailure ||
		e.kind == entryStreamDrop || e.kind == entryRoundPause ||
		e.kind == entryAssistant
}

// selectableRow is selectable plus the one thing that depends on the session
// rather than on the entry: a think row the verbosity is not drawing is not on
// screen to put a cursor on (think.go). A cursor that could land on a row
// nobody can see is a cursor that vanishes.
func (m Model) selectableRow(e entry) bool {
	if e.kind == entryThink && !m.showThink() {
		return false
	}
	return selectable(e)
}

// expandableIndices lists the transcript indices focus mode can select,
// scoped to whichever agent's transcript the surface renders. Step
// headers are targets too: j/k steps between headers and rows
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
				// expand.
				continue
			}
			idxs = append(idxs, blk.step.titleIdx)
			if m.headerFor(blk, es).Folded {
				continue
			}
			// A folded group offers its group row, not the rows inside it.
			for _, sl := range m.stepSlots(es, blk.step) {
				if m.selectableRow(es[sl.idx]) {
					idxs = append(idxs, sl.idx)
				}
			}
			continue
		}
		start, end := blk.members()
		for i := start; i < end; i++ {
			if m.selectableRow(es[i]) {
				idxs = append(idxs, i)
			}
		}
	}
	return idxs
}

// enterFocusMode starts focus mode on the most recent expandable row — or on
// the failure that ended the turn, when there is one. The close rows that
// follow a broken turn are chrome about it; the row that broke it is
// the one holding the way out, so that is where the cursor belongs.
//
// A transcript with rows but nothing expandable in them still opens, without
// a cursor: what is on screen is prose, and prose is read rather than
// navigated. Refusing there was the old answer and it left the reader in the
// input box with nowhere to go. An empty transcript is the one case with
// nothing to open onto, and it still says so.
func (m Model) enterFocusMode() (tea.Model, tea.Cmd) {
	if len(*m.entries()) == 0 {
		if m.startScreenShowing() {
			// First contact is the one screen that is visibly empty,
			// and it is the screen that advertises these keys. A notice here
			// would be noise and would spend the screen to say it.
			return m, nil
		}
		const notice = "Nothing to focus yet — tool and command rows become expandable."
		if m.attachedTo != "" {
			m.noteChild(m.attachedTo, notice)
		} else {
			m.appendEntry(entry{kind: entrySystem, text: notice})
		}
		m.viewport.SetLines(m.renderHistoryLines())
		m.viewport.GotoBottom()
		return m, nil
	}
	m.enterSurface(stateFocus)
	idxs := m.expandableIndices()
	if len(idxs) == 0 {
		m.focusIdx = -1
		m.invalidateRenderCache()
		m.viewport.SetLines(m.renderHistoryLines())
		return m, nil
	}
	m.focusIdx = idxs[len(idxs)-1]
	es := *m.entries()
	for i := len(idxs) - 1; i >= 0; i-- {
		if es[idxs[i]].kind == entryTurnClose {
			continue
		}
		// A drop row sits under the failure that caused it and holds the
		// better offer of the two, so it is the one the cursor lands on.
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
	// The copy caption stands until the next key: whatever the reader does
	// next, they have moved on from the copy it describes.
	if !keys.Match(msg, keys.Reading.Copy) {
		m.readingCopied = ""
	}
	switch pressed := msg.String(); {
	case keys.Is(pressed, keys.Draft.Quit):
		m.quitting = true
		return m, m.quitCmd()
	case keys.Is(pressed, keys.Reading.Back):
		return m.exitFocusMode()
	case keys.Is(pressed, keys.Reading.Move):
		// One binding, both directions: the bar offers `j/k` as a pair and
		// the dispatch reads which half was pressed, so the four keystrokes
		// the mode moves on are declared in one place. Two bindings
		// for one offer would put the same keystroke on the surface twice,
		// which is the thing the register refuses.
		if pressed == "j" || pressed == "down" {
			m.moveFocus(1)
		} else {
			m.moveFocus(-1)
		}
		return m, nil
	case keys.Is(pressed, keys.Reading.List):
		// The register on the page. It is the same key the supporting
		// TUIs have long offered, answering the same question about
		// the surface that holds the keyboard — and it is live here for the
		// reason every bare letter on this bar is: nothing else is listening.
		m.readingKeyList = !m.readingKeyList
		// The list is taller than the bar it replaced, so the panel takes
		// rows from the transcript and gives them back — the same accounting
		// every other bottom panel does.
		m.syncViewport()
		m.refreshFocusView()
		return m, nil
	case keys.Is(pressed, keys.Row.Review, keys.Row.Undo, keys.Row.Rounds, keys.Row.Uncap):
		// A round-limit pause offers all four on its own row; it is
		// asked first because it stands where the close block would be.
		if next, cmd, claimed := m.roundPauseKey(pressed); claimed {
			return next, cmd
		}
		// The offers on a turn's changeset row, which are [v]
		// and [u] and no others. They are handled here rather than globally,
		// so the input keeps both keys. The switch names them both rather
		// than treating "not [v]" as [u]: the pause's other two keys reach
		// this line whenever the cursor is on a close row, and a key a row
		// does not offer has to fall through to the draft, not land on
		// whichever offer happened to be last.
		if e, ok := m.focusedClose(); ok && e.close.Changes != nil {
			switch {
			case keys.Is(pressed, keys.Row.Review):
				// Review mode is a takeover opened from the row;
				// esc comes back here, to the row that offered it.
				return m.openReview(e.turn)
			case keys.Is(pressed, keys.Row.Undo):
				// Undo asks before it writes. The confirm borrows the
				// bottom panel and focus mode keeps the screen, so the cursor
				// stays on the row that offered it and esc comes back here.
				return m.undoTurn(e.turn, nil)
			}
		}
		// [u] off a close row is the pager's half page, not an offer nothing
		// made; the rest go back to the draft as the characters they are.
		if keys.Is(pressed, keys.Reading.Half) {
			m.halfPageFocus(pressed)
			return m, nil
		}
		return m.returnToInput(msg)
	case keys.Is(pressed, keys.Reading.Copy):
		// [y] copies the row under the cursor, type-aware (copyrow.go). A
		// row with nothing to copy hands the letter back to the draft, the
		// way [-] does with nothing open.
		return m.copyFocusedRow(msg)
	case keys.Is(pressed, keys.Reading.Half):
		// Half the viewport at a time, so the reader keeps context while
		// moving quickly; the cursor follows the pane rather than pinning
		// the scroll to wherever it was standing.
		m.halfPageFocus(pressed)
		return m, nil
	case keys.Is(pressed, keys.Row.Reopen):
		// A blocked run's own offer: the item goes back to open from the row
		// that says why it stopped. Handled here rather than globally, so
		// the input keeps the letter for typing.
		if next, cmd, claimed := m.todoRunReopen(m.focusIdx); claimed {
			return next, cmd
		}
		return m.returnToInput(msg)
	case keys.Is(pressed, keys.Row.Retry, keys.Row.Continue, keys.Row.Key, keys.Row.Provider):
		// A provider failure's own offers, and a dropped
		// stream's. Like the changeset row's, they are handled here
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
	case keys.Is(pressed, keys.Reading.Collapse):
		// The explicit half of [enter]'s toggle. Where the row under
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
		// The row's structure — a step's fold, a group's, a diff's three
		// modes — is toggleRow's (click.go), so the key and the pointer open
		// a row through one act rather than two that agree by inspection.
		// The key's own gesture is the cycle: one row under the cursor, one
		// press to spend, three depths reached by spending it again.
		// What is left is the plain body flag, which this mode sets on every
		// row it can put its cursor on.
		claimed, full, output := m.toggleRow(m.focusIdx, gestureCycle)
		if full != nil {
			return m.openDiffFull(full, stateFocus)
		}
		if output {
			es := *m.entries()
			return m.openOutputFull(m.rowOutputView(es[m.focusIdx]), m.focusIdx, stateFocus)
		}
		if !claimed {
			if es := *m.entries(); m.focusIdx >= 0 && m.focusIdx < len(es) {
				es[m.focusIdx].expanded = !es[m.focusIdx].expanded
			}
		}
		m.refreshFocusView()
		return m, nil
	}
	// Typing is the other way out. The letters above are focus
	// mode's own and stay its own; every other printable character hands the
	// keyboard back and lands in the draft, so a reader who forgot which
	// pane they were in loses a mode rather than a sentence.
	if typedRune(msg) {
		return m.returnToInput(msg)
	}
	// Anything left is chrome the transcript has no answer for. It used to be
	// handed to the bubbles viewport, whose own keymap bound the arrows and
	// the pager letters; shhh's pane reads no keys (viewport.go), and
	// every key this mode scrolls on is named in the switch above.
	return m, nil
}

// focusedClose returns the turn-close entry the cursor is on, if it is on
// one. The close rows live in the session's own transcript, so an attached
// child's feed never offers them.
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
	// The copy caption goes with it — it captions a mode that is ending.
	m.readingKeyList = false
	m.readingCopied = ""
	m.leaveSurface()
	m.invalidateRenderCache()
	m.syncViewport()
	m.viewport.SetLines(m.renderHistoryLines())
	return m, nil
}

// halfPageFocus scrolls half the viewport — [u] up, [d] down — and brings
// the cursor along: a cursor left behind a half-page jump would be a lit row
// nobody can see, which is the thing selectableRow exists to prevent.
func (m *Model) halfPageFocus(pressed string) {
	dir := 1
	if pressed == "u" {
		dir = -1
	}
	m.scrollLines(dir * max(m.viewport.Height()/2, 1))
	m.snapFocusIntoView(dir)
}

// snapFocusIntoView moves the cursor to the nearest selectable row the pane
// now shows, when the scroll left it outside. The offset is kept: the reader
// asked for half a page, and pulling the pane back to the cursor would give
// half of it back.
func (m *Model) snapFocusIntoView(dir int) {
	idxs := m.expandableIndices()
	if len(idxs) == 0 || m.focusIdx < 0 {
		return
	}
	starts := m.unitLineStarts()
	top := m.viewport.YOffset()
	bottom := top + max(m.viewport.Height()-1, 0)
	inView := func(idx int) bool {
		s, ok := starts[idx]
		return ok && s >= top && s <= bottom
	}
	if !inView(m.focusIdx) {
		if dir > 0 {
			for _, idx := range idxs {
				if inView(idx) {
					m.focusIdx = idx
					break
				}
			}
		} else {
			for i := len(idxs) - 1; i >= 0; i-- {
				if inView(idxs[i]) {
					m.focusIdx = idxs[i]
					break
				}
			}
		}
	}
	// The cursor moved (or the rows under the highlight did), so the gutter
	// is re-rendered — without refreshFocusView's scroll-to-cursor, which
	// would undo the jump for a block taller than what is left of the pane.
	content, _, _ := m.renderFocusHistory()
	m.viewport.SetContent(content)
}

// unitLineStarts is each transcript entry's first rendered line, counted the
// way the render counts them (renderFocusHistory), so the cursor and the
// pane cannot disagree about what is in view.
func (m Model) unitLineStarts() map[int]int {
	es := *m.entries()
	starts := map[int]int{}
	line := 0
	var prev entry
	havePrev := false
	for _, u := range m.transcriptUnits(es, m.transcriptWidth(), true, m.focusIdx) {
		if havePrev {
			line += strings.Count(separatorBefore(prev, u.sepBefore), "\n")
		}
		starts[u.idx] = line
		line += strings.Count(u.text, "\n")
		prev, havePrev = u.sepAfter, true
	}
	return starts
}

// moveFocus selects the next (+1) or previous (-1) expandable row. With
// nothing to select the transcript is being read rather than navigated, so
// the key is a line of scroll instead.
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
		// they meant to be.
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
	// so it wraps to the transcript pane like the ordinary feed.
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
// The two things reading mode dresses are the rail and this. The row
// under the cursor takes the focus background across its full width with its
// words in bright; the rail and the glyph keep their colours inside the
// highlight, so a row that changed the machine still says so while it is lit
// . The pointer sits outside the highlight, in its own column, because
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
// keyboard. It is the other half of the reading rail: the
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
