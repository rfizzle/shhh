package chat

// A code block's heading row (docs/interface/surfaces.md#the-activity-row):
// a click on a reply's heading copies that block through the block copy's
// own handler, and a drag across a heading leaves it out. The clipboard is
// the session's copyFn, held by every test here.

import (
	"slices"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/clipboard"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/golden"
)

// headingLine is the transcript line of the heading word that first follows
// a line containing after, in a model's current render.
func headingLine(t *testing.T, lines []string, after, word string) int {
	t.Helper()
	seen := false
	for i, l := range lines {
		if strings.Contains(l, after) {
			seen = true
			continue
		}
		if seen && strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), "❯")) == word {
			return i
		}
	}
	t.Fatalf("no %q heading after %q in:\n%s", word, after, strings.Join(lines, "\n"))
	return -1
}

// clickLine presses and releases on the heading word of a transcript line,
// scrolling it on screen first.
func clickLine(t *testing.T, m Model, line int, word string) Model {
	t.Helper()
	if line < m.viewport.YOffset() || line >= m.viewport.YOffset()+m.paneRows() {
		m.viewport.SetYOffset(line)
	}
	col := strings.Index(contentLines(m)[line], word)
	x, y := at(t, m, line, len([]rune(contentLines(m)[line][:col])))
	updated, _ := m.Update(mousePress(x, y))
	updated, _ = updated.(Model).Update(mouseRelease(x, y))
	return updated.(Model)
}

// The click from the draft: the second heading of the reply copies the
// second block, says so the way /copy code 2 does, and a click on a row of
// the code under it is no target at all.
func TestClick_TheHeadingCopiesItsBlock(t *testing.T) {
	c := &clip{}
	m := selectModel(t, c, entry{kind: entryUser, text: "show me the handler"}, entry{kind: entryAssistant, text: threeBlocks})
	head := headingLine(t, contentLines(m), "The scene builds once", "sh")

	body := clickLine(t, m, head+1, "make")
	if c.calls != 0 {
		t.Fatalf("a click on the code copied %q", c.text)
	}

	m = clickLine(t, body, head, "sh")
	if c.text != shBody {
		t.Fatalf("copied %q, want the second block %q", c.text, shBody)
	}
	if got := lastNotice(t, m); got != "copied block 2 · sh · 3 lines" {
		t.Errorf("the copy said %q", got)
	}
}

// The heading of a block the reader sent is drawn the same, and is not a
// target: no key copies a sent message's block.
func TestClick_ASentMessagesHeadingIsNotATarget(t *testing.T) {
	c := &clip{}
	m := selectModel(t, c, entry{kind: entryUser, text: "what does this do?\n```sh\nmake build\n```"}, entry{kind: entryAssistant, text: "It builds."})
	head := headingLine(t, contentLines(m), "what does this do?", "sh")
	clickLine(t, m, head, "sh")
	if c.calls != 0 {
		t.Fatalf("a sent message's heading copied %q", c.text)
	}
}

// In reading mode the click moves the cursor to the reply it landed in
// before copying, as a click on a row does, so the caption and the bar are
// about the row the reader pointed at.
func TestClick_InReadingModeTheCursorMovesFirst(t *testing.T) {
	var caught []string
	m := readingOn(t, oneBlock, threeBlocks, 3, &caught)
	m.pointer.mouseOn = true
	head := headingLine(t, contentLines(m), "Build it first:", "sh")
	m = clickLine(t, m, head, "sh")
	if m.state != stateFocus || m.focusIdx != 1 {
		t.Fatalf("state %d, cursor on %d: want reading mode on the earlier reply", m.state, m.focusIdx)
	}
	if !slices.Equal(caught, []string{"make build"}) {
		t.Fatalf("copied %q", caught)
	}
	if m.readingCopied != "✓ copied block 1 · sh · 1 line" {
		t.Errorf("caption %q", m.readingCopied)
	}
}

// A drag that starts on the heading catches the code alone, lights the code
// alone, and the dedent reads the code's own indent: what lands is the
// block as the message wrote it.
func TestSelect_TheHeadingIsNotCopiedWithTheBlock(t *testing.T) {
	cases := []struct {
		name     string
		from, to func(head int) int
		fromCol  int
		want     string
	}{
		{"from the heading's first column", func(h int) int { return h }, func(h int) int { return h + 3 }, 0, shBody},
		{"from inside the heading word", func(h int) int { return h }, func(h int) int { return h + 3 }, 5, shBody},
		// Across prose the shared indent is the prose's, as it always was,
		// and the next block's heading drops out of the middle.
		{"from the code across the next heading", func(h int) int { return h + 1 }, func(h int) int { return h + 8 }, 0,
			"  make build\n  make tui-shot SCENE=copy-block COLS=110 ROWS=40\n  make tui-shot SCENE=copy-block COLS=60 ROWS=40\n\nIts steps open the list and take the second row:\n\n  keys \"/copy code\" Enter"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withColor(t)
			c := &clip{}
			m := selectModel(t, c, entry{kind: entryAssistant, text: threeBlocks})
			head := headingLine(t, contentLines(m), "The scene builds once", "sh")
			m.viewport.SetYOffset(head)
			from, to := tc.from(head), tc.to(head)
			x0, y0 := at(t, m, from, tc.fromCol)
			updated, _ := m.Update(mousePress(x0, y0))
			x1, y1 := at(t, m, to, endOf(m, to))
			updated, _ = updated.(Model).Update(mouseMotion(x1, y1))
			dragged := updated.(Model)
			lit, raw := dragged.renderHistoryLines(), dragged.renderHistoryRawLines()
			if _, ok := dragged.pointer.sel.heads.lines[head]; !ok {
				t.Fatalf("the drag did not find the heading at line %d", head)
			}
			if from == head && lit[head] != raw[head] {
				t.Errorf("the heading the drag began on was lit: %q", lit[head])
			}
			dragged.Update(mouseRelease(x1, y1))
			if c.text != tc.want {
				t.Fatalf("copied %q, want %q", c.text, tc.want)
			}
		})
	}
}

// TestGolden_BlockHeading captures a reply's code blocks with their heading
// rows: a tagged block whose comment spans lines, a bare one, one that folds
// at the narrow widths, one inside a list item and one inside a quote.
func TestGolden_BlockHeading(t *testing.T) {
	replies := []struct{ label, text string }{
		{"a tagged block · a comment spanning three lines lexed as one", "The limit is checked once per round:\n\n```go\n/* The cap is 120 rounds,\n   raised from 60 in 2 steps. */\nconst limit = 120\n```"},
		{"a bare block · called code", "Then run:\n\n```\nmake tui-shot SCENE=copy-block\n```"},
		{"a block that folds · the heading stays one word", "The scene runs at both widths:\n\n```sh\nmake tui-shot SCENE=copy-block COLS=110 ROWS=40 && make tui-shot SCENE=copy-block COLS=60 ROWS=40\n```"},
		{"inside a list item · the heading in the item's hang", "Two checks:\n\n1. Capture the scene:\n\n   ```sh\n   make tui-shot SCENE=copy-block\n   ```\n2. Read every golden."},
		{"inside a quote · the heading behind the rail", "> The test:\n>\n> ```go\n> func TestCopy(t *testing.T) {}\n> ```"},
	}
	captureBoundedGolden(t, "block-heading", "a code block headed by its language", goldenWidths, func(width int) []golden.Panel {
		var panels []golden.Panel
		for _, r := range replies {
			m := frameModel(t, width, 40)
			m.transcript = []entry{{kind: entryAssistant, text: r.text}}
			m.invalidateRenderCache()
			panels = append(panels, golden.Panel{Label: r.label, View: m.renderHistory()})
		}
		return panels
	})
}

// headingCell is the screen cell of the heading word that first follows a
// line of the frame containing after.
func headingCell(t *testing.T, frame, after, word string) (x, y int) {
	t.Helper()
	lines := strings.Split(frame, "\n")
	y = headingLine(t, lines, after, word)
	return len([]rune(lines[y][:strings.Index(lines[y], word)])), y
}

// blockSession is a running session that answers once with threeBlocks,
// mouse on, its clipboard recorded.
func blockSession(t *testing.T) (*program, func() []string) {
	t.Helper()
	var (
		mu     sync.Mutex
		caught []string
	)
	p := &programProvider{turns: []programTurn{{text: threeBlocks}}}
	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, streamOf(p), Wiring{})
	m.copyFn = func(text string) clipboard.Result {
		mu.Lock()
		defer mu.Unlock()
		caught = append(caught, text)
		return clipboard.Result{OK: true}
	}
	tm := runProgram(t, m)
	tm.Type("show me the handler")
	tm.Send(programEnter)
	waitForText(t, tm, "Its steps open the list")
	return tm, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(caught)
	}
}

// The route: a reply arrives, and a press and a release on its second
// heading copy the second block and say so.
func TestProgram_AClickOnTheHeadingCopiesThatBlock(t *testing.T) {
	tm, caught := blockSession(t)
	frame := waitForFrame(t, tm, "the reply settled", func(f string) bool { return strings.Contains(f, "Its steps open the list") })
	x, y := headingCell(t, frame, "The scene builds once", "sh")
	tm.Send(tea.MouseClickMsg{Button: tea.MouseLeft, X: x, Y: y})
	tm.Send(tea.MouseReleaseMsg{Button: tea.MouseNone, X: x, Y: y})
	waitForText(t, tm, "copied block 2 · sh · 3 lines")
	finalFrame(t, tm)
	if got := caught(); !slices.Equal(got, []string{shBody}) {
		t.Fatalf("the clipboard took %q", got)
	}
}

// A click never takes the keyboard: the half-typed sentence is still in the
// draft, every character, after the copy.
func TestProgram_ACopyClickLeavesTheDraftAlone(t *testing.T) {
	tm, caught := blockSession(t)
	tm.Send(tea.PasteMsg{Content: draftSentence})
	frame := waitForFrame(t, tm, "the draft and the reply", func(f string) bool {
		return strings.Contains(f, draftSentence) && strings.Contains(f, "Its steps open the list")
	})
	x, y := headingCell(t, frame, "Here is the handler:", "go")
	tm.Send(tea.MouseClickMsg{Button: tea.MouseLeft, X: x, Y: y})
	tm.Send(tea.MouseReleaseMsg{Button: tea.MouseNone, X: x, Y: y})
	waitForText(t, tm, "copied block 1 · go · 5 lines")
	frameHas(t, finalFrame(t, tm), draftSentence)
	if got := caught(); !slices.Equal(got, []string{goBody}) {
		t.Fatalf("the clipboard took %q", got)
	}
}

// A press on the heading that moves before it is released is a drag, not a
// click: it selects, and what it copies is the code under the heading.
func TestProgram_ADragFromTheHeadingStillSelects(t *testing.T) {
	tm, caught := blockSession(t)
	frame := waitForFrame(t, tm, "the reply settled", func(f string) bool { return strings.Contains(f, "Its steps open the list") })
	x, y := headingCell(t, frame, "The scene builds once", "sh")
	end := strings.Split(frame, "\n")[y+3]
	tm.Send(tea.MouseClickMsg{Button: tea.MouseLeft, X: x, Y: y})
	tm.Send(tea.MouseMotionMsg{Button: tea.MouseLeft, X: len([]rune(strings.TrimRight(end, " "))) - 1, Y: y + 3})
	tm.Send(tea.MouseReleaseMsg{Button: tea.MouseNone, X: len([]rune(strings.TrimRight(end, " "))) - 1, Y: y + 3})
	waitForText(t, tm, "copied 3 lines")
	finalFrame(t, tm)
	if got := caught(); !slices.Equal(got, []string{shBody}) {
		t.Fatalf("the drag copied %q, want the code alone", got)
	}
}
