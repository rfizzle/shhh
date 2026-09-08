package chat

// Focus mode (docs/interface/surfaces.md#reading-mode): ctrl+o gives
// the transcript a selection cursor over expandable rows (tool and command
// output). j/k moves between them, enter expands/collapses the selected row
// in place, and esc returns to the input. This is the one mechanism behind
// "[enter] expand" everywhere in the transcript, so the input textarea keeps
// all other keys.

import (
	"slices"
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
//
// An interruption's notice is the one system row on the list. It is a
// sentence with nothing under it, which is why a notice is otherwise not a
// stop; this one offers [u] (intervene.go), and a cursor that could not
// reach it would be the offer nobody can take. It stays selectable once the
// offer is spent, so the row does not go out from under the cursor standing
// on it.
func selectable(e entry) bool {
	return expandable(e) || e.kind == entryTurnClose || e.kind == entryFailure ||
		e.kind == entryStreamDrop || e.kind == entryRoundPause ||
		e.kind == entryAssistant || e.kind == entryCompactSummary ||
		e.intervened != nil
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
	if m.framed != nil {
		key := runOf(es)
		if idxs, ok := m.framed.expandable[key]; ok {
			return idxs
		}
		idxs := m.scanExpandable(es)
		if m.framed.expandable == nil {
			m.framed.expandable = map[blockRun][]int{}
		}
		m.framed.expandable[key] = idxs
		return idxs
	}
	return m.scanExpandable(es)
}

// scanExpandable is that list, built.
func (m Model) scanExpandable(es []entry) []int {
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

// rowOnScreen is expandableIndices' membership test for one index, which is
// all a lit pointer has to ask: is the row it names still one the cursor can
// stand on, and is the pane still drawing it (pointer.go).
//
// The list answers it too, by scanning the whole tiling and building a
// header for every step in the session. That is a session-long scan spent to
// look at one row, and the pointer asks it on every repaint — which during a
// turn is every frame.
func (m Model) rowOnScreen(idx int) bool {
	es := *m.entries()
	if idx < 0 || idx >= len(es) {
		return false
	}
	for _, blk := range m.blocksOf(es) {
		if !blk.holds(idx) {
			continue
		}
		if blk.step == nil {
			start, end := blk.members()
			return idx >= start && idx < end && m.selectableRow(es[idx])
		}
		if blk.step.queued() {
			// A declared step nobody has started is a header with no rows
			// and no entry behind it.
			return false
		}
		if idx == blk.step.titleIdx {
			return true
		}
		if m.headerFor(blk, es).Folded {
			// A folded group offers its group row, not the rows inside it.
			return false
		}
		for _, sl := range m.stepSlots(es, blk.step) {
			if sl.idx == idx {
				return m.selectableRow(es[idx])
			}
		}
		return false
	}
	return false
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
	if m.pointer && slices.Contains(idxs, m.focusIdx) {
		// A pointer lit from the prompt is where the reader already is, so
		// the mode opens on it; the flag goes, because in here the cursor
		// is the state (pointer.go).
		m.pointer = false
		m.refreshFocusView()
		return m, nil
	}
	m.pointer = false
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

// openCursorRow is [enter]'s act on the row under the cursor, shared with the
// pointer's open (pointer.go) so the two are one handler: the row's
// structure — a step's fold, a group's, a diff's three modes — is
// toggleRow's, the key's own gesture is the cycle, and what is left is the
// plain body flag. ret is the surface a full-screen body comes back to,
// which is the mode here and the prompt from the pointer.
func (m Model) openCursorRow(ret state) (tea.Model, tea.Cmd) {
	claimed, full, output := m.toggleRow(m.focusIdx, gestureCycle)
	if full != nil {
		return m.openDiffFull(full, ret)
	}
	if output {
		es := *m.entries()
		return m.openOutputFull(m.rowOutputView(es[m.focusIdx]), m.focusIdx, ret)
	}
	if !claimed {
		if es := *m.entries(); m.focusIdx >= 0 && m.focusIdx < len(es) {
			es[m.focusIdx].expanded = !es[m.focusIdx].expanded
		}
	}
	// An entry's rendering changed in place, and the row may belong to a
	// block both caches have frozen. Moving the cursor away is what would
	// show it: the frozen lines are from before the press, so the row the
	// reader just opened would close itself behind them.
	m.invalidateRenderCache()
	m.refreshCursorView()
	return m, nil
}

// updateFocus handles keys while focus mode is active. Esc never destroys:
// it only returns to the input, keeping any expansion state.
func (m Model) updateFocus(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// The query row reads every key first, which is what makes typing search
	// rather than move the cursor. It is the same rule the reverse search
	// over the draft follows (historysearch.go), and for the same reason: a
	// row being typed into is a surface of its own while it is up.
	if m.viewport.SearchOpen() {
		return m.updateSearchQuery(msg)
	}
	// The copy caption stands until the next key: whatever the reader does
	// next, they have moved on from the copy it describes.
	if !keys.Match(msg, keys.Reading.Copy) {
		m.readingCopied = ""
	}
	switch pressed := msg.String(); {
	case keys.Is(pressed, keys.Reading.Back):
		return m.exitFocusMode()
	case keys.Is(pressed, keys.Reading.Search):
		return m.openSearchQuery()
	case keys.Is(pressed, keys.Reading.Match):
		// The step pair walks what the query found. With no search live they
		// are letters like any other and belong in the draft — offering them
		// there would be an offer with nothing behind it.
		if !m.viewport.Searching() {
			return m.returnToInput(msg)
		}
		m.searchStep(keys.Step(pressed, keys.Reading.Match))
		return m, nil
	case keys.Is(pressed, keys.Reading.Move):
		// One binding, both directions: the bar offers `j/k` as a pair and
		// the dispatch reads which half was pressed, so the four keystrokes
		// the mode moves on are declared in one place. Two bindings
		// for one offer would put the same keystroke on the surface twice,
		// which is the thing the register refuses.
		m.moveFocus(keys.Step(pressed, keys.Reading.Move))
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
		// And `[u]` on the notice an automatic steer left, which takes the
		// message back out of the conversation (intervene.go).
		if next, cmd, claimed := m.withdrawSteer(pressed); claimed {
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
		// A fold counting a search's matches answers first, and answers with
		// the match rather than with the fold: it opens and puts the cursor
		// on the first row inside that holds one (search.go). Everywhere else
		// this is the ordinary open, so a step with nothing the query wants
		// behind it is opened the way it always was.
		if next, opened := m.openFoldToMatch(); opened {
			return next, nil
		}
		// The row's structure — a step's fold, a group's, a diff's three
		// modes — is toggleRow's (click.go), so the key and the pointer open
		// a row through one act rather than two that agree by inspection.
		// The key's own gesture is the cycle: one row under the cursor, one
		// press to spend, three depths reached by spending it again.
		// What is left is the plain body flag, which this mode sets on every
		// row it can put its cursor on.
		return m.openCursorRow(stateFocus)
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

// updateSearchQuery answers a key while the query row is open. Typing and
// backspace are the query; the two keys that are not letters end the row, one
// keeping what was found and one clearing it.
//
// A key that is neither — an arrow, a chord the mode has no use for — is
// swallowed rather than closing the row: a reader half way through typing a
// path has not asked to leave, and a stray chord that dropped their query
// would cost more than it saved.
func (m Model) updateSearchQuery(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case keys.Match(msg, keys.Find.Clear):
		m.clearSearch()
	case keys.Match(msg, keys.Find.Keep):
		m.closeSearchQuery()
	case msg.Code == tea.KeyBackspace:
		m.backspaceSearch()
	case typedRune(msg):
		m.typeSearch(msg.Text)
	}
	return m, nil
}

// openSearchQuery puts the query row where the key bar was. The panel grows
// by a row, so the transcript pays for it and gives it back the way it does
// for the key register — and the occurrence the reader was on is brought back
// into the shorter pane rather than left behind it.
func (m Model) openSearchQuery() (tea.Model, tea.Cmd) {
	m.viewport.OpenSearch()
	m.resizeAroundSearchRow()
	return m, nil
}

// closeSearchQuery is [enter]: the row goes and the search stands, which is
// what puts the mode's own letters back in the reader's hands.
func (m *Model) closeSearchQuery() {
	m.viewport.KeepSearch()
	m.resizeAroundSearchRow()
}

// clearSearch is the safe answer on the row: the query, the marks and the
// count on the rail go together, and the mode the reader was in is still
// there.
//
// So do the folds the search opened. A fold it opened was opened to answer
// the query, and the query is over; a fold the reader opened themselves is
// theirs and stays open, which is the whole reason the search writes its own
// override rather than the reader's (steps.go).
func (m *Model) clearSearch() {
	m.viewport.ClearSearch()
	m.clearSearchFolds()
	m.resizeAroundSearchRow()
}

// resizeAroundSearchRow re-lays the surface after the query row appeared or
// went. The transcript is redrawn without being pulled back to the row
// cursor: the reader is standing on an occurrence the search took them to,
// and scrolling to the cursor would take it away from them.
//
// The order is load-bearing and the reveal has to be last. A pane whose
// height changed follows its content to the end (syncViewport), so opening
// the row over a search would drop the reader at the live tail with the
// occurrence they were reading somewhere above it.
func (m *Model) resizeAroundSearchRow() {
	m.syncViewport()
	m.redrawFocusContent()
	m.viewport.RevealMatch()
	m.atBottom = m.viewport.AtBottom()
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
	// So does the search. Its marks are painted on the pane the feed uses
	// too, and the keys that walk them are this mode's: a query left standing
	// would mark lines in a transcript with nothing on screen offering to
	// clear them. The folds it opened to reach a match go back with it.
	m.viewport.ClearSearch()
	m.clearSearchFolds()
	m.leaveSurface()
	// The pointer is not left lit behind the mode: esc from here returns to
	// the prompt, and the prompt the reader left had no gutter on it.
	m.pointer = false
	// The feed's cache is the render without the gutter, and the reader is
	// back on it; the gutter's own units are unchanged and stay (render.go).
	m.cached.reset()
	m.syncViewport()
	// The way out is where a repaint the gutter held is paid for: rows that
	// landed while the reader was scrolled up owe one.
	m.flushStream()
	return m, nil
}

// halfPageFocus scrolls half the viewport — [u] up, [d] down — and brings
// the cursor along: a cursor left behind a half-page jump would be a lit row
// nobody can see, which is the thing selectableRow exists to prevent.
func (m *Model) halfPageFocus(pressed string) {
	dir := keys.Step(pressed, keys.Reading.Half)
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
	m.redrawFocusContent()
}

// redrawFocusContent re-renders the transcript with the selection gutter and
// leaves the pane where it is. It is what a change that is not the cursor's
// needs — a half-page jump, a query row opening — because pulling the pane
// back to the cursor would undo the movement the reader asked for.
func (m *Model) redrawFocusContent() {
	lines, _, _ := m.renderFocusLines()
	m.viewport.SetLines(lines)
}

// unitLineStarts is each transcript entry's first rendered line, counted by
// the render itself (gutterRender), so the cursor and the pane cannot
// disagree about what is in view.
func (m *Model) unitLineStarts() map[int]int {
	starts := map[int]int{}
	m.gutterRender(starts)
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
	lines, start, count := m.renderFocusLines()
	m.viewport.SetLines(lines)
	if m.focusIdx >= 0 {
		switch {
		case start < m.viewport.YOffset():
			m.viewport.SetYOffset(start)
		case start+count > m.viewport.YOffset()+m.viewport.Height():
			m.viewport.SetYOffset(start + count - m.viewport.Height())
		}
	}
	// With no cursor to keep on screen, where the reader scrolled to is
	// where they meant to be — but either way the pane has moved, and what
	// the model believes about the live end has to follow it: a stream holds
	// its repaints on that belief (render.go).
	m.atBottom = m.viewport.AtBottom()
}

// renderFocusLines renders every entry with a two-column gutter on
// expandable rows (❯ on the selected one) and reports the selected block's
// first line and line count for scrolling.
func (m *Model) renderFocusLines() (lines []string, selStart, selCount int) {
	return m.gutterRender(nil)
}

// renderFocusHistory is the same render as one string. Nothing on the
// drawing path uses it: it is what the goldens capture and what the tests
// read, joined back up from the lines above.
func (m *Model) renderFocusHistory() (content string, selStart, selCount int) {
	lines, start, count := m.renderFocusLines()
	return strings.Join(lines, "\n"), start, count
}

// gutterCache is the reading gutter's own render cache.
//
// The gutter is not a render of its own: it is two columns in front of one
// that has already been made. So what is kept here is every block that can
// no longer change, drawn once with the gutter on and no row selected, and
// each frame puts the pointer on the one unit the cursor stands in and joins
// the rest onto the lines they left. A tick that lands a row costs the last
// block rather than the session, which is what the line cache buys the plain
// feed (lines.go) and what reading mode used to pay in full on every frame —
// thirty-two milliseconds and four megabytes of it at six hundred rows,
// spent while the reader was standing still.
//
// The live Model owns it, the way it owns the feed's lines (lines.go): a
// Model copy that has been superseded holds blocks it may no longer render
// from. What is held is safe to share regardless — a frozen block is a pure
// function of entries that can no longer change, so two copies appending the
// same block append the same bytes.
type gutterCache struct {
	// blocks holds one element per frozen transcript block, in block order.
	blocks []gutterBlock
	// count is how many transcript entries those blocks cover, always a
	// whole number of blocks.
	count int
	// width is the pane width they were drawn at. A different width re-wraps
	// every one of them, so it drops the cache.
	width int
	// hint is the last render's line count, which is what the next one is
	// sized from: the pane is handed the whole transcript, and growing a
	// slice of several thousand lines from nothing is the one allocation
	// this cache would otherwise still pay every frame.
	hint int
}

func (c *gutterCache) reset() {
	c.blocks, c.count = nil, 0
}

// gutterBlock is one frozen block as the gutter draws it.
type gutterBlock struct {
	// lines is the block's render. The first element continues the line the
	// block before it left open, exactly as a unit's text does.
	lines []string
	// first and last are the entries the spacing on either side of the block
	// is decided by (separatorBefore) — the first unit's and the last one's.
	first, last entry
	// starts is where each unit begins, relative to the block's first line.
	// It is what the reading cursor's place on screen is read off, and it is
	// kept rather than recomputed because the lines it indexes are.
	starts []unitStart
}

// unitStart is one unit's first line inside the block that holds it.
type unitStart struct {
	idx  int
	line int
}

// noFocusRow is the cursor index that selects nothing, which is what the
// cached blocks are drawn under: a unit renders the same whether the cursor
// is elsewhere or nowhere, and only the unit under it differs.
const noFocusRow = -1

// gutterRender is the transcript with the reading gutter over it, as the
// lines the pane takes. starts, when given, is filled with each unit's first
// line — the same walk, so the cursor's place and the render cannot drift.
//
// Lines rather than one string, for the reason the feed hands the pane lines
// (lines.go): a session joined into one string and split again is three
// passes over the whole transcript to redraw the last forty rows of it.
func (m *Model) gutterRender(starts map[int]int) (lines []string, selStart, selCount int) {
	// Reading mode is a way of reading the transcript, not a takeover
	// surface, so it wraps to the transcript pane like the ordinary feed.
	w := m.transcriptWidth()
	es := *m.entries()
	blocks := m.blocksOf(es)
	// The first element is the open line every unit's text continues, so the
	// result is what strings.Split of the joined units would have been.
	lines = make([]string, 1, m.gutter.hint+1)
	lines[0] = ""
	var prev entry
	havePrev := false

	// emit is one unit onto the end of the lines so far. The separator
	// counts toward the line total before the unit starts, so the gutter
	// pointer and scrolling stay aligned.
	emit := func(u *unit) {
		if havePrev {
			lines = appendRendered(lines, separatorBefore(prev, u.sepBefore))
		}
		at := len(lines) - 1
		if starts != nil {
			starts[u.idx] = at
		}
		if u.idx == m.focusIdx {
			selStart, selCount = at, strings.Count(u.text, "\n")
		}
		lines = appendRendered(lines, u.text)
		prev, havePrev = u.sepAfter, true
	}
	// splice is a whole block, taken from the cache rather than walked.
	splice := func(gb gutterBlock) {
		if len(gb.lines) == 0 {
			return
		}
		if havePrev {
			lines = appendRendered(lines, separatorBefore(prev, gb.first))
		}
		base := len(lines) - 1
		if starts != nil {
			for _, st := range gb.starts {
				starts[st.idx] = base + st.line
			}
		}
		lines[base] += gb.lines[0]
		lines = append(lines, gb.lines[1:]...)
		prev, havePrev = gb.last, true
	}
	fresh := func(blk transcriptBlock) {
		units := m.blockUnits(blk, es, w, true, m.focusIdx)
		for i := range units {
			emit(&units[i])
		}
	}

	if m.attachedTo != "" {
		// A child's mirrored transcript is rewritten in place as its calls
		// settle (attach.go), so no block of it is frozen and there is
		// nothing here to keep. It is drawn whole, as it always was.
		for _, blk := range blocks {
			fresh(blk)
		}
		return lines, selStart, selCount
	}
	frozen := m.frozenGutterBlocks(es, w, blocks)
	for bi, blk := range blocks {
		if bi < len(frozen) && !blk.holds(m.focusIdx) {
			splice(frozen[bi])
			continue
		}
		fresh(blk)
	}
	m.gutter.hint = len(lines)
	return lines, selStart, selCount
}

// frozenGutterBlocks fills the cache up to the last block a row can still
// land in, and answers with what it holds.
//
// The freeze is the feed's (render.go): everything before the last block
// rows can land in, a live fan-out, or a run's own row. What is frozen
// cannot change, so the cache is a prefix of the tiling — and where it is
// not, because something rebuilt the transcript without saying so, the seam
// it was built against no longer matches and the whole of it goes.
func (m *Model) frozenGutterBlocks(es []entry, w int, blocks []transcriptBlock) []gutterBlock {
	if w != m.gutter.width {
		m.gutter.reset()
		m.gutter.width = w
	}
	if n := len(m.gutter.blocks); n > 0 && (n > len(blocks) || blocks[n-1].end != m.gutter.count) {
		m.gutter.reset()
	}
	freeze := min(lastLiveBlock(blocks), m.liveFanoutBlock(blocks), m.liveTodoRunBlock(blocks))
	for bi := len(m.gutter.blocks); bi < freeze; bi++ {
		m.gutter.blocks = append(m.gutter.blocks, newGutterBlock(m.blockUnits(blocks[bi], es, w, true, noFocusRow)))
		m.gutter.count = blocks[bi].end
	}
	return m.gutter.blocks
}

// newGutterBlock draws one block's units into the lines the cache keeps.
func newGutterBlock(units []unit) gutterBlock {
	if len(units) == 0 {
		return gutterBlock{}
	}
	gb := gutterBlock{lines: []string{""}, first: units[0].sepBefore}
	var prev entry
	for i := range units {
		u := &units[i]
		if i > 0 {
			gb.lines = appendRendered(gb.lines, separatorBefore(prev, u.sepBefore))
		}
		gb.starts = append(gb.starts, unitStart{idx: u.idx, line: len(gb.lines) - 1})
		gb.lines = appendRendered(gb.lines, u.text)
		prev = u.sepAfter
	}
	gb.last = prev
	return gb
}

// appendRendered appends rendered text to lines, continuing the line the
// last write left open. It is lineCache.write without the cache: the same
// arithmetic, so the two renders split into lines identically.
func appendRendered(lines []string, s string) []string {
	switch s {
	case "":
		return lines
	case "\n":
		// The blank line between two blocks, which is most of what this is
		// called with: splitting it would allocate a slice per block seam,
		// which over a session is the whole of what a frame allocates.
		return append(lines, "")
	}
	parts := strings.Split(s, "\n")
	lines[len(lines)-1] += parts[0]
	return append(lines, parts[1:]...)
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

// focusHintLines is the bar's content, which is also what the bottom panel is
// sized from (approval.go) — one list, so the panel a reader gets is the
// panel the layout paid for.
func (m Model) focusHintLines() []string {
	width := m.contentWidth()
	// The query row takes the bar's place while it is open, and takes the
	// row's own offers with it: every letter is going into the query, so
	// nothing else on the surface can be honoured while it is up.
	if m.viewport.SearchOpen() {
		return m.transcriptSearchLines(width)
	}
	if m.readingKeyList {
		return m.readingKeyListLines(width, m.maxConfirmPanelHeight())
	}
	lines := []string{m.readingKeyLine(width)}
	return append(lines, m.readingRowLines(width, inputHeight-1)...)
}
