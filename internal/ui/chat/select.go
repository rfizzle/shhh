package chat

// Transcript text selection (docs/interface/surfaces.md#reading-mode).
// Mouse reporting used to buy the wheel and cost the terminal's own
// click-drag selection, and the note about it told the reader to hold shift.
// That answer works for what is on the screen and for nothing else: a
// terminal's native selection cannot follow a transcript past the edge of the
// window, so copying an answer three viewport-heights long meant scrolling,
// selecting, pasting, and doing it again — four times, without a seam
// anywhere to say where the joins went.
//
// So while reporting is on, the application owns the selection. Press inside
// the transcript to anchor it, drag to extend it — including off the top or
// bottom of the pane, where the transcript scrolls itself and the selection
// keeps going — and release to copy what was covered.
//
// # Coordinates
//
// Everything here works in *rendered transcript space*: the index of a visual
// line in renderHistory()'s output, and a display-cell column inside it. That
// is the one coordinate system in this package that is stable under the two
// things that happen during a selection.
//
//   - Scrolling moves the viewport's YOffset, not the lines. A line index
//     taken before a scroll still names the same text after it, which is what
//     lets a drag that started six screens up survive arriving here.
//   - Streaming appends. renderHistory builds the history as a frozen prefix
//     plus a live tail, so a line already rendered keeps its index
//     while the turn writes more underneath it.
//
// What it is *not* stable under is a change of pane width: every line reflows
// and the coordinates name different text. There is no honest remapping, so a
// width change clears the selection outright (resizeSelection).
//
// Screen cells are used for one thing only — the pointer — and are converted
// on arrival (transcriptPoint). Nothing here holds a screen row across a
// frame, and nothing uses a byte offset into a styled string as a position:
// the render is full of ANSI, so every column is a display cell and every cut
// goes through x/ansi, which counts cells rather than bytes.

import (
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// transcriptOrigin is where the transcript pane's first cell lands on the
// screen. It is read off the layout (layout.go) rather than added up
// from the chrome again: the pane's origin is a rectangle's corner, and a
// second description of it here is exactly how a pointer comes to name a
// different row than the one under it.

// selectionScrollInterval is the cadence of the edge auto-scroll. A drag held
// at the edge of the pane is a stationary pointer: the terminal reports no
// motion at all, so the scroll cannot be driven by the events and is driven
// by a timer instead. One line per tick at this rate reads as a steady crawl
// rather than a jump, and is slow enough that a reader can stop on the line
// they wanted.
const selectionScrollInterval = 60 * time.Millisecond

// selectionScrollMsg advances an edge auto-scroll. seq fences it: every way a
// selection can end bumps Model.selScrollSeq, so a tick that outlived its
// drag arrives with a stale number and is dropped rather than scrolling a
// transcript nobody is dragging over (retry waits do the same).
type selectionScrollMsg struct{ seq int }

// selPoint is a coordinate in rendered transcript space: line is an index
// into renderHistory()'s output split on newlines, col a display cell within
// that line.
type selPoint struct {
	line int
	col  int
}

// before reports reading order — up the pane first, then left to right.
func (p selPoint) before(q selPoint) bool {
	if p.line != q.line {
		return p.line < q.line
	}
	return p.col < q.col
}

// selection is the whole selection state: where it was anchored, where it
// reaches now, and whether the button is still down.
//
// anchor and end are both cells the reader pointed at, so the range they
// describe is inclusive at both ends — dragging from the h of "hello" to the
// o copies "hello", not "hell". span() is the one place that turns the pair
// into the half-open range the extraction and the highlight both want.
type selection struct {
	// on is whether there is a selection at all. It outlives the drag: a
	// released selection stays lit until esc, a new press, or anything that
	// invalidates its coordinates.
	on bool
	// dragging is whether the primary button is still down.
	dragging bool
	anchor   selPoint
	end      selPoint
	// width is the pane width the coordinates were taken at. A render at any
	// other width describes different text, so this is what resizeSelection
	// compares against rather than trusting the numbers to travel.
	width int
	// px, py is the last pointer position in screen cells. The edge scroll
	// re-reads it every tick, because a stationary pointer at the bottom row
	// names a further line each time the transcript moves under it.
	px, py int
}

// empty reports whether the selection covers nothing — no selection at all,
// or a press that never moved. A click is not a selection: it must not copy,
// must not light anything up, and must leave the click's own semantics alone.
func (s selection) empty() bool {
	return !s.on || s.anchor == s.end
}

// span normalizes the selection into a half-open range in reading order, so
// backward drags and forward drags reach the extraction as the same thing.
// The end column is one past the last selected cell.
func (s selection) span() (start, end selPoint) {
	start, end = s.anchor, s.end
	if end.before(start) {
		start, end = end, start
	}
	end.col++
	return start, end
}

// hasSelection reports whether something is selected and lit.
func (m Model) hasSelection() bool {
	return !m.sel.empty()
}

// selectableSurface reports whether the normal chat transcript is what the
// pointer is over. Selection is confined to it on purpose:
//
//   - the full-screen diff and review surfaces replace the body with their
//     own scrolling viewers, and their wheel behaviour is theirs;
//   - focus mode renders the transcript through a selection gutter, and a
//     cursor column is chrome nobody wants on their clipboard;
//   - an attached child's session is a different transcript in the same
//     viewport, so a selection anchored in one would name lines in
//     the other the moment the reader detached.
func (m Model) selectableSurface() bool {
	if !m.mouseOn || !m.ready {
		return false
	}
	if m.attachedTo != "" {
		return false
	}
	switch m.state {
	case stateDiffFull, stateReview, stateContext, stateFocus:
		return false
	}
	return true
}

// paneRows and paneCols are the transcript pane's size *as it was last
// drawn*. They read the viewport's own dimensions rather than recomputing
// them from the chrome, because the pointer is over the last frame and the
// two can differ for the moment between a bottom panel growing and
// syncViewport catching up. Recomputing there would put the anchor a row off
// the cell under the cursor — the class of bug this whole file is careful
// about, stated once here so it is not restated at four call sites.
func (m Model) paneRows() int {
	if m.viewport.Height() > 0 {
		return m.viewport.Height()
	}
	return m.viewportHeight()
}

func (m Model) paneCols() int {
	if m.viewport.Width() > 0 {
		return m.viewport.Width()
	}
	return m.transcriptWidth()
}

// transcriptPoint converts a screen cell to a transcript coordinate, and says
// whether the cell is inside the transcript pane at all. Presses use the
// answer: a press outside the pane — on the input, on the inspector rail, on
// the chrome — starts nothing.
func (m Model) transcriptPoint(x, y int) (selPoint, bool) {
	at := m.transcriptOrigin()
	row, col := y-at.Y, x-at.X
	if row < 0 || row >= m.paneRows() {
		return selPoint{}, false
	}
	if col < 0 || col >= m.paneCols() {
		return selPoint{}, false
	}
	return selPoint{line: m.viewport.YOffset() + row, col: col}, true
}

// clampedPoint is transcriptPoint for a drag, where leaving the pane is a
// gesture rather than a mistake: the pointer is pinned to the nearest cell
// inside it, so dragging off the right edge selects to the end of the line
// and dragging off the bottom selects to the last visible row (which the edge
// scroll then keeps feeding).
func (m Model) clampedPoint(x, y int) selPoint {
	at := m.transcriptOrigin()
	row := min(max(y-at.Y, 0), max(m.paneRows()-1, 0))
	col := min(max(x-at.X, 0), max(m.paneCols()-1, 0))
	line := m.viewport.YOffset() + row
	if last := m.viewport.TotalLineCount() - 1; last >= 0 && line > last {
		line = last
	}
	return selPoint{line: line, col: col}
}

// edgeDir is which way a pointer at (x, y) asks the transcript to scroll: -1
// at or above the first row, +1 at or below the last, 0 anywhere between. The
// row is deliberately unclamped, so a pointer dragged clean off the top of
// the terminal keeps scrolling up rather than stopping at the header.
func (m Model) edgeDir(y int) int {
	row := y - m.transcriptOrigin().Y
	if row <= 0 {
		return -1
	}
	if row >= m.paneRows()-1 {
		return 1
	}
	return 0
}

// beginSelection anchors a selection under the pointer. It answers only a
// press inside the transcript pane, and it deliberately does nothing else:
// a press that also expanded a row or answered a decision would make every
// selection a gamble on holding still. The click targets are answered
// on the release instead, in the cell the press landed in, which is the one
// event a drag cannot produce (click.go).
func (m Model) beginSelection(x, y int) (tea.Model, tea.Cmd) {
	pt, ok := m.transcriptPoint(x, y)
	if !ok {
		return m, nil
	}
	m.clearSelection()
	m.sel = selection{
		on:       true,
		dragging: true,
		anchor:   pt,
		end:      pt,
		width:    m.transcriptWidth(),
		px:       x,
		py:       y,
	}
	return m, nil
}

// dragSelection extends the selection to the pointer and starts, stops or
// leaves running the edge auto-scroll.
//
// The scroll chain is keyed on direction rather than restarted per event:
// motion arrives in bursts a terminal chooses the size of, and a chain
// restarted on each one would scroll at the rate of the reader's hand instead
// of its own. A direction that has not changed is therefore the no-op — the
// running chain already knows it — and only a change bumps the fence.
func (m Model) dragSelection(x, y int) (tea.Model, tea.Cmd) {
	if !m.sel.dragging {
		return m, nil
	}
	m.extendSelection(x, y)
	dir := m.edgeDir(y)
	if dir == m.selScrollDir {
		return m, nil
	}
	m.selScrollDir = dir
	m.selScrollSeq++
	if dir == 0 {
		return m, nil
	}
	return m, selectionScrollCmd(m.selScrollSeq)
}

// extendSelection moves the endpoint and re-renders if it actually moved.
// Motion events arrive per cell crossed, and most of them land on the cell
// the selection already ends at; dropping those is the throttle, and it is
// exact rather than a timer, because a position that has not changed cannot
// have changed the highlight.
func (m *Model) extendSelection(x, y int) {
	// The pointer is stored on every event even when the endpoint did not
	// move, because the edge scroll reads it back each tick.
	m.sel.px, m.sel.py = x, y
	pt := m.clampedPoint(x, y)
	if pt == m.sel.end {
		return
	}
	m.sel.end = pt
	// Selecting is reading, and reading pauses the follow the same way
	// scrolling away does: a transcript that jumped to its live end
	// mid-drag would tear the selection off the text it was covering.
	if !m.sel.empty() {
		m.atBottom = false
	}
	m.refreshTranscript()
}

// releaseSelection ends the drag and copies what was covered.
//
// A press that never moved is not a selection and is dropped here rather than
// copied as one cell. It does not reach this function at all any more —
// updateMouse routes it to the click targets — but the guard stays,
// because "one cell is not a selection" is this file's rule and not a
// consequence of who happens to call it.
func (m Model) releaseSelection(x, y int) (tea.Model, tea.Cmd) {
	if !m.sel.dragging {
		return m, nil
	}
	m.extendSelection(x, y)
	m.sel.dragging = false
	m.stopEdgeScroll()
	if m.sel.empty() {
		m.clearSelection()
		m.refreshTranscript()
		return m, nil
	}
	return m.copySelection()
}

// copySelection puts the selected text on the clipboard and says what
// happened.
//
// The two outcomes report in different places on purpose. A success is worth
// one transient line and nothing more, so it goes on the notice rail,
// where it costs no transcript row and does not move the pane the reader is
// still looking at their selection in. It stands exactly as long as the
// selection it describes and goes with it, so it can never outlive the thing
// it is a caption for. A failure is a fact about the machine
// — no clipboard tool, a tool that failed — that the reader has to act on, so
// it goes into the transcript, which is where /copy's failures already go and
// where a narrow layout without a notice rail can still show it.
//
// Either way the selection stays lit. A failed copy that also cleared the
// selection would make the retry — after installing wl-copy, say — a fresh
// drag over the same six screens.
func (m Model) copySelection() (tea.Model, tea.Cmd) {
	text := m.selectedText()
	if text == "" {
		m.clearSelection()
		m.refreshTranscript()
		return m, nil
	}
	if m.copyFn == nil {
		m.selNotice = ""
		return m.systemNotice("Copying is not available in this session.")
	}
	res := m.copyFn(text)
	if res.Warning != "" {
		m.selNotice = ""
		return m.systemNotice(res.Warning)
	}
	m.selNotice = copiedNotice(text)
	return m, nil
}

// copiedNotice sizes the copy in the units the reader was working in — rows
// on the screen — rather than in bytes, which says nothing about whether the
// right thing was caught.
func copiedNotice(text string) string {
	n := strings.Count(text, "\n") + 1
	if n == 1 {
		return "✂ copied 1 line"
	}
	return fmt.Sprintf("✂ copied %d lines", n)
}

// cancelSelection drops any selection and stops any edge scroll, and reports
// whether there was anything to drop. Esc reads the answer: with a selection
// showing esc cancels it and nothing else, and with none it goes on to mean
// what it always meant.
func (m *Model) cancelSelection() bool {
	had := m.sel.on || m.selScrollDir != 0
	m.clearSelection()
	return had
}

// clearSelection forgets the selection and fences off any tick still in
// flight for it.
func (m *Model) clearSelection() {
	m.sel = selection{}
	m.selNotice = ""
	m.stopEdgeScroll()
}

// stopEdgeScroll ends the auto-scroll chain. Bumping the sequence is what
// stops it: the pending tick still arrives, sees a number that is no longer
// current, and does nothing.
func (m *Model) stopEdgeScroll() {
	if m.selScrollDir != 0 {
		m.selScrollDir = 0
		m.selScrollSeq++
	}
}

// resizeSelection answers a change of pane width or height.
//
// Width is the destructive one: every rendered line reflows, so a coordinate
// taken at the old width names different text at the new one. There is no
// remapping that is not a guess, so the selection goes.
//
// Height leaves the lines alone — the same text is at the same indices, just
// less of it on screen — so the range survives. The drag does not: the
// pointer's relation to the pane moved under it, and continuing would extend
// the selection to somewhere the reader never pointed.
func (m *Model) resizeSelection(width int) {
	if m.sel.on && m.sel.width != width {
		m.cancelSelection()
		return
	}
	if m.sel.dragging {
		m.sel.dragging = false
		m.stopEdgeScroll()
	}
}

// selectionScrollCmd schedules the next edge-scroll tick.
func selectionScrollCmd(seq int) tea.Cmd {
	return tea.Tick(selectionScrollInterval, func(time.Time) tea.Msg {
		return selectionScrollMsg{seq: seq}
	})
}

// updateSelectionScroll advances one line and extends the selection over
// what that revealed.
//
// The chain ends of its own accord at the end of the transcript: a scroll
// that moved nothing has nothing left to reveal, and continuing would burn a
// timer to keep re-selecting the same last line.
func (m Model) updateSelectionScroll(msg selectionScrollMsg) (tea.Model, tea.Cmd) {
	if msg.seq != m.selScrollSeq || m.selScrollDir == 0 || !m.sel.dragging {
		return m, nil
	}
	// The transcript stopped being what is on screen between the tick being
	// scheduled and arriving — a takeover surface opened, reporting went off,
	// the session attached to a child. Scrolling something else on behalf of
	// a drag nobody can see is the one thing this must not do.
	if !m.selectableSurface() {
		m.cancelSelection()
		return m, nil
	}
	before := m.viewport.YOffset()
	m.scrollLines(m.selScrollDir)
	if m.viewport.YOffset() == before {
		m.stopEdgeScroll()
		return m, nil
	}
	// The pointer has not moved; the text under it has. Re-reading the stored
	// position against the new offset is what makes a held pointer keep
	// extending instead of pinning the selection to the row it started on.
	m.extendSelection(m.sel.px, m.sel.py)
	return m, selectionScrollCmd(msg.seq)
}

// refreshTranscript re-renders the history into the viewport. The highlight
// is applied over the finished render (applySelectionHighlight), so a drag
// costs a restyle of the selected rows and never a re-render of the history
// behind them — the incremental cache is not touched by any of this.
func (m *Model) refreshTranscript() {
	if !m.ready {
		return
	}
	m.viewport.SetLines(m.renderHistoryLines())
}

// selectedText is what the selection would put on the clipboard.
//
// It reads the *current* render rather than anything captured when the drag
// started, which is the whole streaming policy in one line: the selection is
// a pair of coordinates, so what it copies is whatever those coordinates name
// at the moment of release. A turn that wrote more underneath the selection
// does not change it; a turn that rewrote the lines inside it copies the
// rewrite, and a transcript that shrank copies as much as is left. Nothing
// here can name a line that no longer exists.
func (m *Model) selectedText() string {
	if m.sel.empty() {
		return ""
	}
	start, end := m.sel.span()
	return selectedTextFrom(m.renderHistoryRawLines(), start, end, m.transcriptWidth())
}

// selectedTextFrom extracts the selected rectangle from rendered lines and
// stitches it back into text.
//
// Every row is stripped of ANSI first, so no escape code, no selection
// styling and no colour reaches the clipboard — what is copied is the
// characters that were on the screen inside the range, and nothing about how
// they were drawn.
func selectedTextFrom(lines []string, start, end selPoint, width int) string {
	if start.line < 0 || start.line >= len(lines) {
		return ""
	}
	last := min(end.line, len(lines)-1)
	rows := make([]string, 0, last-start.line+1)
	for y := start.line; y <= last; y++ {
		plain := ansi.Strip(lines[y])
		lo, hi := 0, ansi.StringWidth(plain)
		if y == start.line {
			lo = start.col
		}
		if y == end.line && end.col < hi {
			hi = end.col
		}
		if lo >= hi {
			rows = append(rows, "")
			continue
		}
		rows = append(rows, ansi.Cut(plain, lo, hi))
	}
	return joinSelectedRows(rows, width, start.col > 0)
}

// joinSelectedRows turns screen rows back into text.
//
// This is the one genuinely lossy step, and it is lossy because the render is
// the only record there is: glamour and this package's own wordWrap both emit
// finished rows, and neither leaves a mark saying which row boundaries it
// invented. So the boundaries are read back out of the geometry (softWrap),
// and the result is prose that joins at its wraps and keeps every newline the
// content actually had.
func joinSelectedRows(rows []string, width int, cutFirst bool) string {
	if len(rows) == 0 {
		return ""
	}
	trimmed := make([]string, len(rows))
	for i, row := range rows {
		trimmed[i] = strings.TrimRight(row, " \t")
	}
	// Classify boundaries before dedenting: softWrap reads row widths against
	// the width the wrapper was filling, and a dedented row is narrower than
	// the one it actually emitted.
	joins := make([]bool, max(len(trimmed)-1, 0))
	for i := range joins {
		fill := width
		// A first row the selection cut mid-line is shorter than the block it
		// came out of, so its padding says nothing about that block; every
		// other row's does.
		if i > 0 || !cutFirst {
			fill = fillWidth(rows[i], trimmed[i], width)
		}
		joins[i] = softWrap(trimmed[i], trimmed[i+1], fill)
	}
	body := dedent(trimmed, cutFirst)
	var b strings.Builder
	for i, row := range body {
		b.WriteString(row)
		if i == len(body)-1 {
			break
		}
		if joins[i] {
			b.WriteByte(' ')
		} else {
			b.WriteByte('\n')
		}
	}
	// Leading and trailing blank rows are the selection overshooting into the
	// gaps between entries, not content; the gaps *inside* it are.
	return strings.Trim(b.String(), "\n")
}

// fillWidth is the width the wrapper that emitted a row was filling, which is
// not always the pane's.
//
// glamour lays prose out inside a two-column document margin and pads every
// row it emits out to the block it filled, so trailing padding is the record
// of how far that wrapper was allowed to go — four columns short of the pane,
// two of which are already inside the row. Measuring against the pane instead
// leaves a two-column window in which a wrap reads as a newline, and which
// pane widths fall inside that window is arithmetic nobody chose: the fixture
// in select_test.go joins at 76 columns and does not at 74, and no rule says
// so.
//
// A row carrying no padding was not laid out inside a block, and gets the
// pane: this package's own wordWrap emits none, and neither does a row the
// selection cut short of its right edge.
func fillWidth(row, trimmed string, width int) int {
	w := ansi.StringWidth(row)
	if w > ansi.StringWidth(trimmed) && w < width {
		return w
	}
	return width
}

// softWrap reports whether the boundary between two rows is a renderer's word
// wrap rather than a newline the content has.
//
// The test is the definition of a greedy word wrapper run backwards: a row is
// a wrap when the next row's first word could not have fitted on the end of
// it. That is stricter than measuring how full the row looks, and it is what
// separates a paragraph continuing from a short line that simply ended —
// a code line, a list item, the last line of a paragraph.
//
// Three things end a row outright, whatever the arithmetic says: a blank row
// on either side (a paragraph break), a change of indent (a different block —
// a fenced code line under prose, a nested item), and a next row that opens a
// markdown block of its own, since a list item may wrap right up to the width
// and still be followed by the next bullet.
func softWrap(row, next string, width int) bool {
	if strings.TrimSpace(row) == "" || strings.TrimSpace(next) == "" {
		return false
	}
	if leadingSpaces(row) != leadingSpaces(next) {
		return false
	}
	if startsBlock(next) {
		return false
	}
	word := strings.TrimLeft(next, " ")
	if i := strings.IndexAny(word, " \t"); i > 0 {
		word = word[:i]
	}
	return ansi.StringWidth(row)+1+ansi.StringWidth(word) > width
}

// startsBlock reports whether a row opens a markdown block — a bullet, a
// heading, a numbered item — rather than continuing the row above it.
func startsBlock(row string) bool {
	row = strings.TrimLeft(row, " ")
	switch {
	case strings.HasPrefix(row, "- "), strings.HasPrefix(row, "* "),
		strings.HasPrefix(row, "+ "), strings.HasPrefix(row, "• "),
		strings.HasPrefix(row, "#"), strings.HasPrefix(row, "> "):
		return true
	}
	// "1." / "12)" and nothing longer: a paragraph that happens to contain a
	// full stop early is not a list.
	if i := strings.IndexAny(row, ".)"); i > 0 && i < 3 && i+1 < len(row) && row[i+1] == ' ' {
		for j := range i {
			if row[j] < '0' || row[j] > '9' {
				return false
			}
		}
		return true
	}
	return false
}

// dedent removes the indent every selected row shares. glamour renders the
// transcript's prose inside a two-column document margin, and that margin is
// chrome: nobody pastes a paragraph in order to paste the gutter it was drawn
// in. Only the *common* indent goes, so a code block's own indentation and a
// nested list's shape survive.
//
// skipFirst excludes a first row the selection cut mid-line: its leading
// spaces are ones the reader dragged across, not a margin, and they say
// nothing about the block's indent.
func dedent(rows []string, skipFirst bool) []string {
	pad := -1
	for i, row := range rows {
		if strings.TrimSpace(row) == "" {
			continue
		}
		if i == 0 && skipFirst {
			continue
		}
		if n := leadingSpaces(row); pad < 0 || n < pad {
			pad = n
		}
	}
	if pad <= 0 {
		return rows
	}
	out := make([]string, len(rows))
	for i, row := range rows {
		if i == 0 && skipFirst {
			out[i] = row
			continue
		}
		if len(row) >= pad {
			out[i] = row[pad:]
		} else {
			out[i] = strings.TrimLeft(row, " ")
		}
	}
	return out
}

// leadingSpaces counts the spaces a row opens with.
func leadingSpaces(row string) int {
	return len(row) - len(strings.TrimLeft(row, " "))
}

// applySelectionHighlight lights the selected range in a finished render.
//
// It runs over the rendered string rather than inside the renderers, which is
// what keeps a drag cheap: the history's incremental cache is never
// invalidated by a selection, so moving the pointer restyles the rows the
// selection covers and re-renders nothing at all.
func (m Model) applySelectionHighlight(content []string) []string {
	if m.sel.empty() || m.sel.width != m.transcriptWidth() {
		return content
	}
	start, end := m.sel.span()
	if start.line >= len(content) {
		return content
	}
	// The lines belong to the block cache, so the restyle works
	// on a copy of the slice: the frozen prefix is rendered once and must
	// still be what a later frame, or the clipboard, reads.
	lines := slices.Clone(content)
	last := min(end.line, len(lines)-1)
	for y := start.line; y <= last; y++ {
		lo := 0
		if y == start.line {
			lo = start.col
		}
		// Rendered rows are padded out to the pane with real spaces, so the
		// highlight stops at the last cell holding something. A block of
		// inverse running past the end of a sentence would say the trailing
		// blanks were selected, and they are not what gets copied.
		hi := lastContentCol(lines[y])
		if y == end.line && end.col < hi {
			hi = end.col
		}
		if lo >= hi {
			continue
		}
		lines[y] = highlightSpan(lines[y], lo, hi)
	}
	return lines
}

// lastContentCol is one past the last non-blank display cell of a row.
func lastContentCol(line string) int {
	return ansi.StringWidth(strings.TrimRight(ansi.Strip(line), " \t"))
}

// highlightSpan restyles cells [lo, hi) of a rendered row. The span is cut by
// display cell through x/ansi, never by byte offset: the row is full of
// escape sequences, and a byte index into one is not a column.
//
// The selected cells lose whatever styling they had. That is deliberate —
// a selection has to read as one continuous block, and a range that kept its
// syntax colours underneath would be a different shade every few characters.
func highlightSpan(line string, lo, hi int) string {
	w := ansi.StringWidth(line)
	pre := ansi.Cut(line, 0, lo)
	mid := ansi.Strip(ansi.Cut(line, lo, hi))
	post := ""
	if hi < w {
		post = ansi.Cut(line, hi, w)
	}
	return pre + selectionStyle.Render(mid) + post
}

// selectionStyle is how a selected cell is drawn. It is reverse video rather
// than a background colour, which is the one styling that survives every
// palette this product has: it says "selected" in mono exactly as
// loudly as in colour, so the invariant that nothing is carried by colour
// alone holds without a second signal being invented for it. It is also what
// a terminal's own selection looks like, so the gesture and its feedback
// match what the reader already expects of a drag.
var selectionStyle = lipgloss.NewStyle().Reverse(true)
