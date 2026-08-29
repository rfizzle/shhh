package chat

// Application-owned transcript selection: a drag inside the
// transcript selects text shhh copies itself, scrolls the pane when it
// reaches an edge, and gives the coordinates up rather than guessing when the
// render underneath them changes shape.

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/clipboard"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// --- gestures -------------------------------------------------------------

func mousePress(x, y int) tea.MouseMsg {
	return tea.MouseClickMsg{Button: tea.MouseLeft, X: x, Y: y}
}

func mouseMotion(x, y int) tea.MouseMsg {
	return tea.MouseMotionMsg{Button: tea.MouseLeft, X: x, Y: y}
}

func mouseRelease(x, y int) tea.MouseMsg {
	// Terminals disagree about whether a release names the button; X10 does
	// not, so the harder case is the one exercised here.
	return tea.MouseReleaseMsg{Button: tea.MouseNone, X: x, Y: y}
}

// withColor makes lipgloss emit escape sequences, which a test binary with no
// terminal behind it otherwise strips — the highlight is an escape sequence
// and there is nothing to assert on without this.
func withColor(t *testing.T) {
	t.Helper()
	was := components.Profile()
	components.SetProfile(colorprofile.ANSI256)
	t.Cleanup(func() { components.SetProfile(was) })
}

// clip records what the session put on the clipboard, and can be told to
// fail.
type clip struct {
	text  string
	calls int
	warn  string
}

func (c *clip) fn() func(string) clipboard.Result {
	return func(s string) clipboard.Result {
		c.text = s
		c.calls++
		if c.warn != "" {
			return clipboard.Result{Warning: c.warn}
		}
		return clipboard.Result{OK: true, Tool: "pbcopy"}
	}
}

// --- fixtures -------------------------------------------------------------

// selectModel is a transcript with mouse reporting on and a recording
// clipboard. The entries are the caller's, so each test owns the exact prose
// its assertions are about.
func selectModel(t *testing.T, c *clip, entries ...entry) Model {
	t.Helper()
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream).WithMouse(true)
	m.copyFn = c.fn()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)
	for _, e := range entries {
		m.appendEntry(e)
	}
	m.viewport.SetLines(m.renderHistoryLines())
	m.viewport.GotoTop()
	m.atBottom = false
	return m
}

// contentLines is the transcript as the viewport holds it, without styling.
func contentLines(m Model) []string {
	raw := m.renderHistoryRaw()
	lines := strings.Split(raw, "\n")
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = ansi.Strip(l)
	}
	return out
}

// lineOf is the index of the first rendered line containing want. Tests name
// the text they mean rather than a number, so a change in the transcript's
// spacing does not turn into a wrong-line failure somewhere else.
func lineOf(t *testing.T, m Model, want string) int {
	t.Helper()
	for i, l := range contentLines(m) {
		if strings.Contains(l, want) {
			return i
		}
	}
	t.Fatalf("no rendered line contains %q; transcript was:\n%s", want, strings.Join(contentLines(m), "\n"))
	return -1
}

// at converts a transcript coordinate back to the screen cell the pointer
// would be over. It is the inverse of transcriptPoint, and it fails loudly
// when the line is not on screen, because a test pointing at a scrolled-off
// row is not testing what it thinks it is.
func at(t *testing.T, m Model, line, col int) (x, y int) {
	t.Helper()
	row := line - m.viewport.YOffset()
	if row < 0 || row >= m.paneRows() {
		t.Fatalf("line %d is not on screen (offset %d, height %d)", line, m.viewport.YOffset(), m.paneRows())
	}
	at := m.transcriptOrigin()
	return col + at.X, row + at.Y
}

// endOf is the last cell of a rendered line holding anything.
func endOf(m Model, line int) int {
	return max(lastContentCol(contentLines(m)[line])-1, 0)
}

// dragLines selects whole rendered lines [from, to] and releases.
func dragLines(t *testing.T, m Model, from, to int) Model {
	t.Helper()
	x0, y0 := at(t, m, from, 0)
	updated, _ := m.Update(mousePress(x0, y0))
	m = updated.(Model)
	x1, y1 := at(t, m, to, endOf(m, to))
	updated, _ = m.Update(mouseMotion(x1, y1))
	m = updated.(Model)
	updated, _ = m.Update(mouseRelease(x1, y1))
	return updated.(Model)
}

const wrappedProse = "The parser walks the token stream once and keeps a stack of open groups, so a nested group is just another frame on that stack rather than a special case in the grammar."

// --- the gesture ----------------------------------------------------------

func TestSelection_IgnoredWhileMouseReportingIsOff(t *testing.T) {
	c := &clip{}
	m := selectModel(t, c, entry{kind: entryUser, text: "a question about the parser"})
	m.mouseOn = false

	for _, msg := range []tea.MouseMsg{mousePress(4, 3), mouseMotion(20, 4), mouseRelease(20, 4)} {
		updated, cmd := m.Update(msg)
		m = updated.(Model)
		if cmd != nil {
			t.Fatalf("reporting is off; %v should produce no command", msg)
		}
	}
	if m.hasSelection() {
		t.Fatal("reporting is off; nothing should be selected")
	}
	if c.calls != 0 {
		t.Fatalf("reporting is off; nothing should be copied, got %d calls", c.calls)
	}
}

func TestSelection_DragHighlightsAndCopiesOnRelease(t *testing.T) {
	withColor(t)
	c := &clip{}
	m := selectModel(t, c, entry{kind: entryUser, text: "a question about the parser"})
	line := lineOf(t, m, "a question about the parser")

	x0, y0 := at(t, m, line, 0)
	updated, _ := m.Update(mousePress(x0, y0))
	m = updated.(Model)
	if m.hasSelection() {
		t.Fatal("a press that has not moved is not a selection yet")
	}

	x1, y1 := at(t, m, line, endOf(m, line))
	updated, _ = m.Update(mouseMotion(x1, y1))
	m = updated.(Model)
	if !m.hasSelection() {
		t.Fatal("dragging across the row should select it")
	}
	// The highlight is in the render, and it is an attribute rather than a
	// colour, so it survives mono (invariant 1).
	rendered := strings.Split(m.renderHistory(), "\n")[line]
	if !strings.Contains(rendered, "\x1b[7m") {
		t.Fatalf("the selected row should be drawn in reverse video, got %q", rendered)
	}

	updated, _ = m.Update(mouseRelease(x1, y1))
	m = updated.(Model)
	if c.calls != 1 {
		t.Fatalf("release should copy exactly once, got %d", c.calls)
	}
	if c.text != "a question about the parser" {
		t.Fatalf("copied %q", c.text)
	}
	if !m.hasSelection() {
		t.Fatal("a released selection stays lit until esc or the next press")
	}
	if m.selNotice == "" {
		t.Fatal("a successful copy should say so on the notice rail")
	}
}

// A click is not a selection: it copies nothing, lights nothing, and leaves
// every other click semantic — row expansion, the focus cursor — untouched.
func TestSelection_ClickWithoutMovementDoesNothing(t *testing.T) {
	c := &clip{}
	m := selectModel(t, c,
		entry{kind: entryUser, text: "a question about the parser"},
		entry{kind: entryTool, toolName: "read_file", toolResult: "one\ntwo\nthree"},
	)
	line := lineOf(t, m, "⚙ read")
	focusBefore, stateBefore := m.focusIdx, m.state

	x, y := at(t, m, line, 2)
	updated, _ := m.Update(mousePress(x, y))
	m = updated.(Model)
	updated, _ = m.Update(mouseRelease(x, y))
	m = updated.(Model)

	if c.calls != 0 {
		t.Fatalf("a click should copy nothing, got %d calls", c.calls)
	}
	if m.hasSelection() {
		t.Fatal("a click should leave nothing selected")
	}
	if m.focusIdx != focusBefore || m.state != stateBefore {
		t.Fatalf("a click should not move the focus cursor or the surface: %d/%v → %d/%v",
			focusBefore, stateBefore, m.focusIdx, m.state)
	}
}

// A drag over an expandable row must not also expand it. shhh draws no click
// targets, and this is the test that keeps it that way.
func TestSelection_DragDoesNotExpandARow(t *testing.T) {
	c := &clip{}
	m := selectModel(t, c, entry{kind: entryTool, toolName: "read_file", toolResult: "one\ntwo\nthree"})
	before := ansi.Strip(m.renderHistoryRaw())
	line := lineOf(t, m, "⚙ read")

	m = dragLines(t, m, line, line)

	if got := ansi.Strip(m.renderHistoryRaw()); got != before {
		t.Fatalf("a drag must not expand the row it started on:\n%s\n---\n%s", before, got)
	}
	if m.state != stateInput {
		t.Fatalf("a drag must not take the keyboard, got state %v", m.state)
	}
	if c.calls != 1 || !strings.Contains(c.text, "⚙ read") {
		t.Fatalf("the drag should have copied the row, got %d calls %q", c.calls, c.text)
	}
}

// A press outside the transcript pane — over the prompt, over the chrome —
// starts nothing, so the textarea's own behaviour is untouched.
func TestSelection_PressOutsideTheTranscriptStartsNothing(t *testing.T) {
	c := &clip{}
	m := selectModel(t, c, entry{kind: entryUser, text: "a question about the parser"})
	origin := m.transcriptOrigin()

	cases := []struct {
		name string
		x, y int
	}{
		{"above the pane", 4, origin.Y - 1},
		{"below the pane, over the prompt", 4, origin.Y + m.paneRows() + 1},
		{"left of the pane", 0, origin.Y + 1},
		{"right of the pane", origin.X + m.transcriptWidth(), origin.Y + 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			updated, _ := m.Update(mousePress(tc.x, tc.y))
			pm := updated.(Model)
			if pm.sel.dragging || pm.sel.on {
				t.Fatalf("a press at (%d,%d) should not anchor a selection", tc.x, tc.y)
			}
			updated, _ = pm.Update(mouseMotion(tc.x+20, tc.y))
			if updated.(Model).hasSelection() {
				t.Fatal("and dragging from it should select nothing")
			}
		})
	}
}

func TestSelection_TypingIsUnaffectedByALiveSelection(t *testing.T) {
	c := &clip{}
	m := selectModel(t, c, entry{kind: entryUser, text: "a question about the parser"})
	line := lineOf(t, m, "a question about the parser")
	m = dragLines(t, m, line, line)

	m = typeChars(t, m, "half a sentence")
	if m.input.Value() != "half a sentence" {
		t.Fatalf("a live selection must not swallow the draft, got %q", m.input.Value())
	}
	if !m.hasSelection() {
		t.Fatal("and typing must not cancel it either")
	}
}

// --- what lands on the clipboard -----------------------------------------

func TestSelection_WrappedProseCopiesAsOneSentence(t *testing.T) {
	c := &clip{}
	m := selectModel(t, c, entry{kind: entryAssistant, text: wrappedProse})
	first := lineOf(t, m, "The parser walks")
	last := lineOf(t, m, "special case in the grammar")
	if last <= first {
		t.Fatalf("the fixture must wrap over several rows, got %d → %d", first, last)
	}

	// The subject is what reached the clipboard, not the model afterwards.
	dragLines(t, m, first, last)

	if c.text != wrappedProse {
		t.Fatalf("wrapped prose should copy as the sentence it is:\n got %q\nwant %q", c.text, wrappedProse)
	}
	if strings.Contains(c.text, "\n") {
		t.Fatal("no newline should be introduced at a visual wrap")
	}
}

// The join is the geometry's, not the terminal's: the same paragraph copies
// as one sentence at every width, because softWrap measures against the block
// glamour filled rather than against the pane. It used to measure
// against the pane, which happened to be right at 80 columns and wrong at 78.
func TestSelection_WrappedProseCopiesAsOneSentenceAtEveryWidth(t *testing.T) {
	for _, width := range []int{72, 78, 80, 96, 130} {
		c := &clip{}
		msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
		m := New(msgs, mockStream).WithMouse(true)
		m.copyFn = c.fn()
		updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
		m = updated.(Model)
		m.appendEntry(entry{kind: entryAssistant, text: wrappedProse})
		m.viewport.SetLines(m.renderHistoryLines())
		m.viewport.GotoTop()
		m.atBottom = false

		first := lineOf(t, m, "The parser walks")
		last := lineOf(t, m, "special case in the grammar")
		if last <= first {
			t.Fatalf("width %d: the fixture must wrap over several rows", width)
		}
		dragLines(t, m, first, last)
		if c.text != wrappedProse {
			t.Fatalf("width %d: wrapped prose should copy as the sentence it is:\n got %q\nwant %q",
				width, c.text, wrappedProse)
		}
	}
}

func TestSelection_KeepsParagraphsListsAndCodeLines(t *testing.T) {
	c := &clip{}
	md := "First paragraph.\n\nSecond paragraph.\n\n- first bullet\n- second bullet\n\n```go\nfunc main() {\n\tprintln(\"hi\")\n}\n```"
	m := selectModel(t, c, entry{kind: entryAssistant, text: md})
	first := lineOf(t, m, "First paragraph")
	last := lineOf(t, m, "}")

	dragLines(t, m, first, last)

	for _, want := range []string{
		"First paragraph.\n\nSecond paragraph.",
		"• first bullet\n• second bullet",
		"func main() {",
	} {
		if !strings.Contains(c.text, want) {
			t.Fatalf("copied text should keep %q:\n%s", want, c.text)
		}
	}
	// The code block's own indentation survives; glamour's document margin,
	// which is chrome, does not.
	if strings.HasPrefix(c.text, " ") {
		t.Fatalf("the renderer's left margin should not be copied:\n%q", c.text)
	}
}

func TestSelection_AcrossEntriesKeepsReadingOrderAndSeparation(t *testing.T) {
	c := &clip{}
	m := selectModel(t, c,
		entry{kind: entryUser, text: "first question"},
		entry{kind: entryUser, text: "second question"},
	)
	first := lineOf(t, m, "first question")
	last := lineOf(t, m, "second question")

	dragLines(t, m, first, last)

	i := strings.Index(c.text, "first question")
	j := strings.Index(c.text, "second question")
	if i < 0 || j < 0 || i > j {
		t.Fatalf("entries should copy in reading order:\n%q", c.text)
	}
	// The blank row the transcript puts between entries is the separation,
	// and it comes through as the blank line it is drawn as.
	if !strings.Contains(c.text, "first question\n") || !strings.Contains(c.text[i:j], "\n\n") {
		t.Fatalf("entries should stay separated by the blank row between them:\n%q", c.text)
	}
}

func TestSelection_BackwardDragMatchesForward(t *testing.T) {
	c := &clip{}
	base := selectModel(t, c,
		entry{kind: entryUser, text: "first question"},
		entry{kind: entryUser, text: "second question"},
	)
	first := lineOf(t, base, "first question")
	last := lineOf(t, base, "second question")

	dragLines(t, base, first, last)
	want := c.text

	// The same range walked the other way: press at the end, drag to the start.
	m := base
	x0, y0 := at(t, m, last, endOf(m, last))
	updated, _ := m.Update(mousePress(x0, y0))
	m = updated.(Model)
	x1, y1 := at(t, m, first, 0)
	updated, _ = m.Update(mouseMotion(x1, y1))
	m = updated.(Model)
	updated, _ = m.Update(mouseRelease(x1, y1))
	m = updated.(Model)

	if c.text != want {
		t.Fatalf("a backward drag should copy what the forward one did:\n got %q\nwant %q", c.text, want)
	}
	if !m.hasSelection() {
		t.Fatal("a backward drag should leave the range lit too")
	}
}

// Nothing about how the transcript is drawn reaches the clipboard.
func TestSelection_CopiesNoStyling(t *testing.T) {
	c := &clip{}
	m := selectModel(t, c, entry{kind: entryAssistant, text: "**bold** and `code` in a line"})
	line := lineOf(t, m, "bold")
	dragLines(t, m, line, line)

	if strings.Contains(c.text, "\x1b") {
		t.Fatalf("no escape sequence should reach the clipboard: %q", c.text)
	}
	if c.text != ansi.Strip(c.text) {
		t.Fatalf("copied text should already be plain: %q", c.text)
	}
}

// --- edge auto-scroll -----------------------------------------------------

// tallModel is a transcript several viewport-heights long.
func tallModel(t *testing.T, c *clip) Model {
	t.Helper()
	entries := make([]entry, 0, 60)
	for i := 0; i < 60; i++ {
		entries = append(entries, entry{kind: entryUser, text: "row " + string(rune('a'+i%26)) + strings.Repeat("x", i%5)})
	}
	m := selectModel(t, c, entries...)
	if m.viewport.TotalLineCount() <= m.paneRows()*2 {
		t.Fatalf("the fixture must be taller than two screens, got %d lines in %d rows",
			m.viewport.TotalLineCount(), m.paneRows())
	}
	return m
}

// tick dispatches the edge-scroll tick the model is currently waiting on.
func scrollTick(t *testing.T, m Model) (Model, bool) {
	t.Helper()
	seq := m.selScrollSeq
	updated, cmd := m.Update(selectionScrollMsg{seq: seq})
	return updated.(Model), cmd != nil
}

func TestSelection_BottomEdgeAutoScrollsAndExtends(t *testing.T) {
	c := &clip{}
	m := tallModel(t, c)
	bottomRow := m.transcriptOrigin().Y + m.paneRows() - 1

	x0, y0 := at(t, m, 0, 0)
	updated, _ := m.Update(mousePress(x0, y0))
	m = updated.(Model)

	updated, cmd := m.Update(mouseMotion(10, bottomRow))
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("a drag at the bottom edge should start the auto-scroll")
	}
	if m.selScrollDir != 1 {
		t.Fatalf("the edge should ask to scroll down, got %d", m.selScrollDir)
	}

	offsetBefore, endBefore := m.viewport.YOffset(), m.sel.end.line
	for i := 0; i < 8; i++ {
		var more bool
		m, more = scrollTick(t, m)
		if !more {
			t.Fatalf("the chain stopped after %d ticks with content left", i)
		}
	}
	if m.viewport.YOffset() <= offsetBefore {
		t.Fatalf("the transcript should have scrolled, offset %d → %d", offsetBefore, m.viewport.YOffset())
	}
	if m.sel.end.line <= endBefore {
		t.Fatalf("the selection should have extended over what scrolled into view, %d → %d", endBefore, m.sel.end.line)
	}
	if m.sel.end.line < offsetBefore+m.paneRows() {
		t.Fatal("the selection should now cover content that started below the viewport")
	}

	// And the follow is paused while this is going on, exactly as scrolling
	// away pauses it.
	if m.atBottom {
		t.Fatal("a selection drag should pause the follow of the live end")
	}
}

func TestSelection_TopEdgeAutoScrollsUpward(t *testing.T) {
	c := &clip{}
	m := tallModel(t, c)
	m.viewport.GotoBottom()
	m.viewport.SetLines(m.renderHistoryLines())
	m.viewport.GotoBottom()

	bottomRow := m.transcriptOrigin().Y + m.paneRows() - 1
	updated, _ := m.Update(mousePress(10, bottomRow))
	m = updated.(Model)

	updated, cmd := m.Update(mouseMotion(10, m.transcriptOrigin().Y))
	m = updated.(Model)
	if cmd == nil || m.selScrollDir != -1 {
		t.Fatalf("a drag at the top edge should scroll up, dir %d cmd %v", m.selScrollDir, cmd != nil)
	}

	offsetBefore, endBefore := m.viewport.YOffset(), m.sel.end.line
	for i := 0; i < 8; i++ {
		var more bool
		m, more = scrollTick(t, m)
		if !more {
			t.Fatalf("the chain stopped after %d ticks with content left", i)
		}
	}
	if m.viewport.YOffset() >= offsetBefore {
		t.Fatalf("the transcript should have scrolled up, offset %d → %d", offsetBefore, m.viewport.YOffset())
	}
	if m.sel.end.line >= endBefore {
		t.Fatalf("the selection should have extended upward, %d → %d", endBefore, m.sel.end.line)
	}
}

// The pointer never moves again after reaching the edge: the terminal reports
// nothing at all, and the scroll has to come from the timer.
func TestSelection_StationaryPointerKeepsScrolling(t *testing.T) {
	c := &clip{}
	m := tallModel(t, c)
	bottomRow := m.transcriptOrigin().Y + m.paneRows() - 1

	updated, _ := m.Update(mousePress(at(t, m, 0, 0)))
	m = updated.(Model)
	updated, _ = m.Update(mouseMotion(10, bottomRow))
	m = updated.(Model)

	last := m.viewport.YOffset()
	for i := 0; i < 5; i++ {
		var more bool
		m, more = scrollTick(t, m)
		if !more {
			t.Fatalf("tick %d ended the chain early", i)
		}
		if m.viewport.YOffset() <= last {
			t.Fatalf("tick %d did not scroll with a stationary pointer (%d → %d)", i, last, m.viewport.YOffset())
		}
		last = m.viewport.YOffset()
	}

	// And it stops itself at the end rather than burning a timer forever.
	for i := 0; i < 500 && m.selScrollDir != 0; i++ {
		m, _ = scrollTick(t, m)
	}
	if m.selScrollDir != 0 {
		t.Fatal("the chain should end when the transcript runs out")
	}
}

func TestSelection_ReleaseAndEscStopPendingTicks(t *testing.T) {
	bottomRow := func(m Model) int { return m.transcriptOrigin().Y + m.paneRows() - 1 }

	cases := []struct {
		name string
		stop func(t *testing.T, m Model) Model
	}{
		{"release", func(t *testing.T, m Model) Model {
			updated, _ := m.Update(mouseRelease(10, bottomRow(m)))
			return updated.(Model)
		}},
		{"esc", func(t *testing.T, m Model) Model {
			updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
			return updated.(Model)
		}},
		{"ctrl+x", func(t *testing.T, m Model) Model {
			updated, _ := m.Update(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})
			return updated.(Model)
		}},
		{"/ui mouse off", func(t *testing.T, m Model) Model {
			m.uiCommand([]string{"/ui", "mouse", "off"})
			return m
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &clip{}
			m := tallModel(t, c)
			updated, _ := m.Update(mousePress(at(t, m, 0, 0)))
			m = updated.(Model)
			updated, _ = m.Update(mouseMotion(10, bottomRow(m)))
			m = updated.(Model)
			stale := m.selScrollSeq
			if m.selScrollDir == 0 {
				t.Fatal("the fixture should be auto-scrolling")
			}

			m = tc.stop(t, m)
			if m.selScrollDir != 0 {
				t.Fatalf("%s should stop the auto-scroll", tc.name)
			}

			offset := m.viewport.YOffset()
			updated, cmd := m.Update(selectionScrollMsg{seq: stale})
			m = updated.(Model)
			if cmd != nil {
				t.Fatalf("a tick that outlived its drag should schedule nothing")
			}
			if m.viewport.YOffset() != offset {
				t.Fatalf("a stale tick should not scroll, %d → %d", offset, m.viewport.YOffset())
			}
		})
	}
}

// --- cancelling and clearing ---------------------------------------------

func TestSelection_EscCancelsAndLeavesTheDraftAlone(t *testing.T) {
	c := &clip{}
	m := selectModel(t, c, entry{kind: entryUser, text: "a question about the parser"})
	m = typeChars(t, m, "half a sentence")
	line := lineOf(t, m, "a question about the parser")
	m = dragLines(t, m, line, line)

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)

	if m.hasSelection() {
		t.Fatal("esc should cancel a visible selection")
	}
	if m.input.Value() != "half a sentence" {
		t.Fatalf("the esc that cancelled the selection must not clear the draft, got %q", m.input.Value())
	}
	if m.selNotice != "" {
		t.Fatalf("the copy notice should go with it, got %q", m.selNotice)
	}
	// A second esc means what esc always meant.
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if got := updated.(Model).input.Value(); got != "" {
		t.Fatalf("esc with nothing selected should clear the draft, got %q", got)
	}
}

func TestSelection_MouseOffClearsIt(t *testing.T) {
	for _, tc := range []struct {
		name string
		off  func(m Model) Model
	}{
		{"ctrl+x", func(m Model) Model {
			updated, _ := m.Update(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})
			return updated.(Model)
		}},
		{"/ui mouse off", func(m Model) Model {
			m.uiCommand([]string{"/ui", "mouse", "off"})
			return m
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withColor(t)
			c := &clip{}
			m := selectModel(t, c, entry{kind: entryUser, text: "a question about the parser"})
			line := lineOf(t, m, "a question about the parser")
			m = dragLines(t, m, line, line)
			if !m.hasSelection() {
				t.Fatal("the fixture should have a selection")
			}

			m = tc.off(m)

			if m.mouseOn {
				t.Fatal("reporting should be off")
			}
			if m.hasSelection() || m.sel.on {
				t.Fatal("turning reporting off should give the selection back to the terminal")
			}
			if strings.Contains(m.renderHistory(), "\x1b[7m") {
				t.Fatal("and the highlight should be gone from the render")
			}
		})
	}
}

// --- streaming, resize, and the surfaces that are not the transcript ------

// A turn writing into the transcript appends; a selection above the live end
// keeps naming the same text, and the follow stays paused so the pane does
// not jump out from under it.
func TestSelection_SurvivesStreaming(t *testing.T) {
	c := &clip{}
	m := selectModel(t, c, entry{kind: entryUser, text: "a question about the parser"})
	line := lineOf(t, m, "a question about the parser")

	x0, y0 := at(t, m, line, 0)
	updated, _ := m.Update(mousePress(x0, y0))
	m = updated.(Model)
	x1, y1 := at(t, m, line, endOf(m, line))
	updated, _ = m.Update(mouseMotion(x1, y1))
	m = updated.(Model)
	span := m.sel

	m.setTurnState(stateStreaming)
	for i := 0; i < 5; i++ {
		updated, _ = m.Update(tokenMsg{text: "more streamed prose. "})
		m = updated.(Model)
	}

	if m.sel != span {
		t.Fatalf("streaming should not disturb the selection: %+v → %+v", span, m.sel)
	}
	if m.atBottom {
		t.Fatal("the follow should stay paused while a drag is live")
	}
	updated, _ = m.Update(mouseRelease(x1, y1))
	m = updated.(Model)
	if c.text != "a question about the parser" {
		t.Fatalf("the selection should still copy what it covered, got %q", c.text)
	}
}

// A width change reflows every line, so the coordinates stop meaning
// anything. The documented policy is to drop them rather than remap by guess.
func TestSelection_ResizePolicy(t *testing.T) {
	t.Run("width change drops it", func(t *testing.T) {
		withColor(t)
		c := &clip{}
		m := selectModel(t, c, entry{kind: entryUser, text: "a question about the parser"})
		line := lineOf(t, m, "a question about the parser")
		m = dragLines(t, m, line, line)

		updated, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
		m = updated.(Model)

		if m.hasSelection() || m.sel.on {
			t.Fatal("a width change should drop the selection")
		}
		if strings.Contains(m.renderHistory(), "\x1b[7m") {
			t.Fatal("and nothing should still be drawn as selected")
		}
	})

	t.Run("height change keeps the range but ends the drag", func(t *testing.T) {
		c := &clip{}
		m := selectModel(t, c, entry{kind: entryUser, text: "a question about the parser"})
		line := lineOf(t, m, "a question about the parser")
		x0, y0 := at(t, m, line, 0)
		updated, _ := m.Update(mousePress(x0, y0))
		m = updated.(Model)
		x1, y1 := at(t, m, line, endOf(m, line))
		updated, _ = m.Update(mouseMotion(x1, y1))
		m = updated.(Model)

		updated, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
		m = updated.(Model)

		if !m.hasSelection() {
			t.Fatal("the same lines are at the same indices; the range should survive")
		}
		if m.sel.dragging {
			t.Fatal("but the drag should end rather than track a pane that moved")
		}
	})
}

// A selection can never name a line that is not there: the extraction reads
// the render it is copying from and stops where it ends.
func TestSelection_ClampsToWhatIsStillRendered(t *testing.T) {
	c := &clip{}
	m := selectModel(t, c,
		entry{kind: entryUser, text: "first question"},
		entry{kind: entryUser, text: "second question"},
	)
	first := lineOf(t, m, "first question")
	last := lineOf(t, m, "second question")
	x0, y0 := at(t, m, first, 0)
	updated, _ := m.Update(mousePress(x0, y0))
	m = updated.(Model)
	x1, y1 := at(t, m, last, endOf(m, last))
	updated, _ = m.Update(mouseMotion(x1, y1))
	m = updated.(Model)

	// The transcript shrinks under the live range.
	m.transcript = m.transcript[:1]
	m.invalidateRenderCache()

	text := m.selectedText()
	if strings.Contains(text, "second question") {
		t.Fatalf("a selection cannot copy content that is gone: %q", text)
	}
	if !strings.Contains(text, "first question") {
		t.Fatalf("what is left should still copy: %q", text)
	}
}

func TestSelection_ConfinedToTheNormalTranscript(t *testing.T) {
	cases := []struct {
		name  string
		build func(t *testing.T) Model
	}{
		{"full-screen diff", func(t *testing.T) Model { return diffFullModel(t).WithMouse(true) }},
		{"focus mode", func(t *testing.T) Model {
			c := &clip{}
			entries := make([]entry, 0, 40)
			for i := 0; i < 40; i++ {
				entries = append(entries, entry{kind: entryTool, toolName: "read_file", toolResult: "one\ntwo"})
			}
			m := selectModel(t, c, entries...)
			next, _ := m.enterFocusMode()
			m = next.(Model)
			m.viewport.SetLines(m.renderHistoryLines())
			m.viewport.GotoTop()
			return m
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.build(t)
			if m.selectableSurface() {
				t.Fatalf("%s is not the transcript; selection should not reach it", tc.name)
			}
			updated, _ := m.Update(mousePress(6, m.transcriptOrigin().Y+1))
			m = updated.(Model)
			updated, _ = m.Update(mouseMotion(20, m.transcriptOrigin().Y+3))
			m = updated.(Model)
			if m.hasSelection() {
				t.Fatalf("%s should select nothing", tc.name)
			}
			// The wheel still reaches it, which is what it always did.
			before := m.viewport.YOffset()
			offset := before
			if m.fullDiff != nil {
				offset = m.fullDiff.Offset
			}
			updated, _ = m.Update(wheel(1))
			m = updated.(Model)
			moved := m.viewport.YOffset() != before
			if m.fullDiff != nil {
				moved = m.fullDiff.Offset != offset
			}
			if !moved {
				t.Fatalf("%s should still scroll on the wheel", tc.name)
			}
		})
	}
}

// --- failures -------------------------------------------------------------

func TestSelection_CopyFailureIsSaidOutLoudAndKeepsTheSelection(t *testing.T) {
	c := &clip{warn: "no clipboard tool found — install xclip, xsel, or wl-copy"}
	m := selectModel(t, c, entry{kind: entryUser, text: "a question about the parser"})
	line := lineOf(t, m, "a question about the parser")

	m = dragLines(t, m, line, line)

	if c.calls != 1 {
		t.Fatalf("the copy should have been attempted once, got %d", c.calls)
	}
	if m.selNotice != "" {
		t.Fatalf("a failed copy must not claim success, got notice %q", m.selNotice)
	}
	view := ansi.Strip(m.renderHistoryRaw())
	if !strings.Contains(view, "no clipboard tool found") {
		t.Fatalf("the failure should be visible in the transcript:\n%s", view)
	}
	if !m.hasSelection() {
		t.Fatal("a failed copy keeps the selection, so the retry is not another six screens of dragging")
	}
}

// --- the extraction, on its own ------------------------------------------

func TestJoinSelectedRows(t *testing.T) {
	const width = 40
	cases := []struct {
		name string
		rows []string
		want string
	}{
		{
			name: "a wrapped sentence joins at the wraps",
			rows: []string{
				"the parser walks the token stream once",
				"and keeps a stack of open groups",
			},
			want: "the parser walks the token stream once and keeps a stack of open groups",
		},
		{
			name: "a short row ends its line",
			rows: []string{"a short line", "another short line"},
			want: "a short line\nanother short line",
		},
		{
			name: "a blank row is a paragraph break",
			rows: []string{"first paragraph.", "", "second paragraph."},
			want: "first paragraph.\n\nsecond paragraph.",
		},
		{
			name: "list items keep their own lines even when full width",
			rows: []string{
				"• a bullet long enough to fill the row up",
				"• the next bullet",
			},
			want: "• a bullet long enough to fill the row up\n• the next bullet",
		},
		{
			name: "a change of indent ends the line",
			rows: []string{
				"prose that runs right up to the wrap col",
				"    indented code that follows it",
			},
			want: "prose that runs right up to the wrap col\n    indented code that follows it",
		},
		{
			name: "the shared margin goes, the relative indent stays",
			rows: []string{"  func main() {", "      println(\"hi\")", "  }"},
			want: "func main() {\n    println(\"hi\")\n}",
		},
		{
			name: "trailing padding is not content",
			rows: []string{"a short line" + strings.Repeat(" ", 20)},
			want: "a short line",
		},
		{
			name: "leading and trailing blank rows are overshoot",
			rows: []string{"", "the line", ""},
			want: "the line",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := joinSelectedRows(tc.rows, width, false); got != tc.want {
				t.Fatalf("got %q\nwant %q", got, tc.want)
			}
		})
	}
}

func TestSelectedTextFrom_SlicesByCell(t *testing.T) {
	lines := []string{
		"\x1b[1mAssistant\x1b[0m",
		"  hello there, reader",
		"  and a second line",
	}
	cases := []struct {
		name       string
		start, end selPoint
		want       string
	}{
		{"one row, mid-line", selPoint{1, 2}, selPoint{1, 7}, "hello"},
		// The label sits at column 0, so there is no margin the rows all share
		// and the indented row keeps its own.
		{"across rows", selPoint{0, 0}, selPoint{1, 22}, "Assistant\n  hello there, reader"},
		{"past the end of a row clamps", selPoint{1, 2}, selPoint{1, 500}, "hello there, reader"},
		{"past the end of the content clamps", selPoint{1, 2}, selPoint{99, 5}, "hello there, reader\nand a second line"},
		{"a start past the content is empty", selPoint{99, 0}, selPoint{99, 5}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := selectedTextFrom(lines, tc.start, tc.end, 40); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestSelectionSpan_NormalizesBothDirections(t *testing.T) {
	a, b := selPoint{line: 3, col: 4}, selPoint{line: 7, col: 2}
	forward := selection{on: true, anchor: a, end: b}
	backward := selection{on: true, anchor: b, end: a}

	fs, fe := forward.span()
	bs, be := backward.span()
	if fs != bs || fe != be {
		t.Fatalf("a range should normalize the same both ways: %v-%v vs %v-%v", fs, fe, bs, be)
	}
	if fe.col != b.col+1 {
		t.Fatalf("the cell under the pointer is selected, so the end is exclusive at col+1, got %d", fe.col)
	}
	if (selection{on: true, anchor: a, end: a}).empty() == false {
		t.Fatal("a press that never moved covers nothing")
	}
}

// The highlight is drawn with an attribute, not a colour, so it says the same
// thing in mono as it does in colour (invariant 1).
func TestSelectionHighlight_SurvivesMono(t *testing.T) {
	withColor(t)
	was := components.Mono()
	components.SetMono(true)
	t.Cleanup(func() { components.SetMono(was) })

	c := &clip{}
	m := selectModel(t, c, entry{kind: entryUser, text: "a question about the parser"})
	line := lineOf(t, m, "a question about the parser")
	m = dragLines(t, m, line, line)

	if !strings.Contains(m.renderHistory(), "\x1b[7m") {
		t.Fatal("the selection must still be visible with the colours gone")
	}
}

// A surface that borrows the screen takes the selection with it, and any
// auto-scroll running under the drag stops there rather than scrolling a
// transcript nobody can see.
func TestSelection_TakeoverSurfaceEndsTheDrag(t *testing.T) {
	c := &clip{}
	m := tallModel(t, c)
	updated, _ := m.Update(mousePress(at(t, m, 0, 0)))
	m = updated.(Model)
	updated, _ = m.Update(mouseMotion(10, m.transcriptOrigin().Y+m.paneRows()-1))
	m = updated.(Model)
	stale := m.selScrollSeq
	if m.selScrollDir == 0 || !m.sel.dragging {
		t.Fatal("the fixture should be mid-drag with the auto-scroll running")
	}

	m.enterSurface(stateDiffFull)

	if m.sel.on || m.selScrollDir != 0 {
		t.Fatal("a takeover surface should end the selection and the scroll")
	}
	offset := m.viewport.YOffset()
	updated, cmd := m.Update(selectionScrollMsg{seq: stale})
	m = updated.(Model)
	if cmd != nil || m.viewport.YOffset() != offset {
		t.Fatalf("a tick that outlived the surface should do nothing (%d → %d)", offset, m.viewport.YOffset())
	}
}

// Both layouts use the same transcript viewport, so both select — the
// inspector rail just takes columns off the pane, and a press on the rail is
// a press outside it.
func TestSelection_WorksInBothLayouts(t *testing.T) {
	cases := []struct {
		name    string
		width   int
		twoPane bool
	}{
		{"single pane", 80, false},
		{"inspector rail", components.InspectorMinContentWidth + horizontalPadding*2, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &clip{}
			m := selectModel(t, c, entry{kind: entryUser, text: "a question about the parser"})
			updated, _ := m.Update(tea.WindowSizeMsg{Width: tc.width, Height: 24})
			m = updated.(Model)
			m.viewport.GotoTop()
			if m.twoPane() != tc.twoPane {
				t.Fatalf("expected twoPane=%v at width %d", tc.twoPane, tc.width)
			}

			line := lineOf(t, m, "a question about the parser")
			m = dragLines(t, m, line, line)
			if c.text != "a question about the parser" {
				t.Fatalf("copied %q", c.text)
			}

			// The rail beyond the pane is not the transcript.
			if tc.twoPane {
				before := m.sel
				updated, _ = m.Update(mousePress(m.transcriptOrigin().X+m.transcriptWidth()+2, m.transcriptOrigin().Y+1))
				if pm := updated.(Model); pm.sel != before {
					t.Fatalf("a press on the inspector rail should change nothing: %+v → %+v", before, pm.sel)
				}
			}
		})
	}
}

// Review mode owns the screen the same way the full-screen diff does, and
// keeps its own mouse behaviour.
func TestSelection_ReviewModeKeepsItsOwnMouse(t *testing.T) {
	m, _ := reviewModel(t)
	m = sendText(t, m.WithMouse(true), "/review")
	if m.state != stateReview || m.review == nil {
		t.Fatalf("the fixture should be in review mode, got state %v", m.state)
	}
	if m.selectableSurface() {
		t.Fatal("review mode is not the transcript")
	}
	before := m.review.Offset

	updated, _ := m.Update(mousePress(6, m.transcriptOrigin().Y+1))
	m = updated.(Model)
	updated, _ = m.Update(mouseMotion(20, m.transcriptOrigin().Y+4))
	m = updated.(Model)
	if m.hasSelection() {
		t.Fatal("review mode should select nothing")
	}
	updated, _ = m.Update(wheel(1))
	if got := updated.(Model).review.Offset; got == before {
		t.Fatalf("the wheel should still scroll review mode, offset stayed %d", got)
	}
}

// The history's incremental cache is what makes a long transcript
// cheap to redraw, and a drag must not spend it: the highlight is applied
// over the finished render, so moving the pointer restyles rows and
// re-renders nothing.
func TestSelection_DragDoesNotInvalidateTheRenderCache(t *testing.T) {
	c := &clip{}
	m := tallModel(t, c)
	m.viewport.SetLines(m.renderHistoryLines())
	cachedBefore := strings.Join(m.cached.lines[:m.cached.frozen], "\n")
	countBefore := m.cached.count
	if countBefore == 0 {
		t.Fatal("the fixture should have a warm render cache")
	}

	updated, _ := m.Update(mousePress(at(t, m, 0, 0)))
	m = updated.(Model)
	origin := m.transcriptOrigin()
	for row := 1; row < m.paneRows(); row++ {
		for col := 0; col < 8; col++ {
			updated, _ = m.Update(mouseMotion(origin.X+col, origin.Y+row))
			m = updated.(Model)
		}
	}

	if m.cached.count != countBefore || strings.Join(m.cached.lines[:m.cached.frozen], "\n") != cachedBefore {
		t.Fatalf("a drag re-rendered the history: %d entries cached → %d", countBefore, m.cached.count)
	}
	if !m.hasSelection() {
		t.Fatal("and it should still have selected something")
	}
}

// The endpoint only moves when the pointer changes cell, which is the
// throttle: motion arrives per cell crossed and most events repeat one.
func TestSelection_RepeatedMotionOnTheSameCellIsANoOp(t *testing.T) {
	c := &clip{}
	m := selectModel(t, c, entry{kind: entryUser, text: "a question about the parser"})
	line := lineOf(t, m, "a question about the parser")
	x0, y0 := at(t, m, line, 0)
	updated, _ := m.Update(mousePress(x0, y0))
	m = updated.(Model)
	x1, y1 := at(t, m, line, 6)
	updated, _ = m.Update(mouseMotion(x1, y1))
	m = updated.(Model)
	before := m.sel

	for i := 0; i < 5; i++ {
		updated, cmd := m.Update(mouseMotion(x1, y1))
		m = updated.(Model)
		if cmd != nil {
			t.Fatal("a motion that changed nothing should schedule nothing")
		}
	}
	if m.sel != before {
		t.Fatalf("a repeated cell should leave the selection alone: %+v → %+v", before, m.sel)
	}
}
