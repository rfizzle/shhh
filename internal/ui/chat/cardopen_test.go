package chat

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/diff"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/golden"
)

var (
	stripLeft  = tea.KeyPressMsg{Code: tea.KeyLeft}
	stripRight = tea.KeyPressMsg{Code: tea.KeyRight}
)

// largeStepEntries is the catalogue's large step: twenty-two calls, reads
// in two directories, three commands and two edits, the last command
// failing.
func largeStepEntries() []entry {
	ui := []string{"keys.go", "render.go", "reply.go", "click.go", "fold.go", "scene.go", "scene_test.go",
		"focus.go", "steps.go", "lines.go", "copy.go", "flash.go", "model.go", "view.go"}
	docs := []string{"replies.md", "keys.md", "scenes.md"}
	read := func(p string) entry { return readEntry(p, 2400*time.Millisecond) }
	edit := func(p, old, new string) entry {
		return entry{kind: entryDiff, toolName: "edit_file", duration: 900 * time.Millisecond,
			diff: &components.DiffView{Path: p, Verb: "edit", Mode: components.DiffCollapsed, Hunks: diff.Compute(old, new)}}
	}
	es := []entry{
		{kind: entryUser, text: "add a copy key to code blocks"},
		{kind: entryAssistant, text: "The copy key lives in the reply pane's key map, next to the existing fold key. " +
			"I've added it there and a flash to render.go so the block blinks once on copy. " +
			"Tests pass except one golden I'll refresh next."},
	}
	for _, f := range ui[:10] {
		es = append(es, read("internal/ui/"+f))
	}
	es = append(es, entry{kind: entryCommand, text: "go build ./...", toolResult: "", duration: 4 * time.Second})
	for _, f := range ui[10:] {
		es = append(es, read("internal/ui/"+f))
	}
	for _, f := range docs {
		es = append(es, read("docs/"+f))
	}
	es = append(es,
		edit("internal/ui/keys.go", "switch k {\n}\n", "switch k {\ncase \"c\": m.copyBlock(m.focusedBlock())\n}\n"),
		entry{kind: entryCommand, text: "go test ./internal/ui/ -run Copy", toolResult: "ok", duration: 11 * time.Second},
		edit("internal/ui/render.go", "func flash() {}\n", "func (m *Model) flashBlock(b Block) tea.Cmd {\n\treturn nil\n}\n"),
		entry{kind: entryCommand, text: "go test ./internal/ui/",
			toolResult: "--- FAIL: TestReplyGolden (0.02s)\nFAIL TestReplyGolden: reply_copy.golden differs",
			exitCode:   1, duration: 57 * time.Second},
	)
	return es
}

// largeStepModel is the large step, finished, opened onto its calls.
func largeStepModel(t *testing.T, width int) Model {
	t.Helper()
	m := activityModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 60})
	m = updated.(Model)
	m.transcript = largeStepEntries()
	openCardRows(m.transcript, 1)
	m.invalidateRenderCache()
	m.viewport.SetLines(m.renderHistoryLines())
	return m
}

// largeStepCalls is the entry of each of the large step's calls, in the
// order they were made.
func largeStepCalls(m Model) []int {
	var at []int
	for i, e := range m.transcript {
		if isActivityEntry(e) {
			at = append(at, i)
		}
	}
	return at
}

// lineIndex is the first line holding s, or -1.
func lineIndex(lines []string, s string) int {
	for i, l := range lines {
		if strings.Contains(l, s) {
			return i
		}
	}
	return -1
}

// Open, a card lists its calls under the receipt's verbs in the receipt's
// order: the reads rolled up by directory, each command and each write a
// row of its own with its rail, and the strip under them.
func TestCard_OpenListsCallsUnderTheVerbs(t *testing.T) {
	m := largeStepModel(t, 110)
	lines := strings.Split(stripANSI(m.renderHistory()), "\n")
	order := []string{
		"▾ read 17 files", "internal/ui/ keys.go · render.go", "docs/ replies.md · keys.md · scenes.md",
		"▾ ran 3 commands", "▎$ go build ./...", "▎$ go test ./internal/ui/ -run Copy", "▎✗ go test ./internal/ui/ ",
		"FAIL TestReplyGolden: reply_copy.golden differs",
		"▾ wrote 2 files", "▎✎ internal/ui/keys.go · + case \"c\"", "▎✎ internal/ui/render.go · - func flash() {}",
		"in order ⚙⚙⚙⚙⚙⚙⚙⚙⚙⚙$⚙⚙⚙⚙⚙⚙⚙✎$✎✗",
	}
	at := -1
	for _, want := range order {
		i := lineIndex(lines, want)
		if i < 0 {
			t.Fatalf("the open card has no line holding %q:\n%s", want, strings.Join(lines, "\n"))
		}
		if i <= at {
			t.Fatalf("%q is out of the receipt's order:\n%s", want, strings.Join(lines, "\n"))
		}
		at = i
	}
	if !strings.Contains(lines[lineIndex(lines, "in order")], "22 tools") {
		t.Errorf("the strip counts its calls on the right: %q", lines[lineIndex(lines, "in order")])
	}
	if l := lines[lineIndex(lines, "▾ wrote 2 files")]; !strings.HasSuffix(strings.TrimSpace(l), "+4 −1") {
		t.Errorf("a group of writes states its change, not its time: %q", l)
	}
	for _, l := range lines {
		if strings.Contains(l, "read    ") || strings.Contains(l, "[enter]") || strings.Contains(l, "[←") {
			t.Errorf("an open card drew a row on the old grid or a key: %q", l)
		}
	}
}

// Each group folds on its own, and the fold is kept on the call the group
// starts at: the other groups keep their rows.
func TestCard_AGroupFoldsAlone(t *testing.T) {
	m := largeStepModel(t, 110)
	calls := largeStepCalls(m)
	ran := calls[10] // go build, the first command
	m, _ = pressKey(t, m, readingChord())
	m.focusIdx = ran
	m.refreshFocusView()
	m, _ = pressKey(t, m, enter)

	if !m.transcript[ran].groupFolded {
		t.Fatal("enter on a group's line should fold the group, kept on its first call")
	}
	view := stripANSI(m.renderHistory())
	if !strings.Contains(view, "▸ ran 3 commands") {
		t.Errorf("a folded group draws its line with ▸:\n%s", view)
	}
	for _, gone := range []string{"go build ./...", "-run Copy"} {
		if strings.Contains(view, gone) {
			t.Errorf("a folded group still draws %q", gone)
		}
	}
	for _, kept := range []string{"▾ read 17 files", "docs/ replies.md", "▾ wrote 2 files", "internal/ui/keys.go ·"} {
		if !strings.Contains(view, kept) {
			t.Errorf("folding one group took %q from another", kept)
		}
	}
	m, _ = pressKey(t, m, enter)
	if m.transcript[ran].groupFolded || !strings.Contains(stripANSI(m.renderHistory()), "go build ./...") {
		t.Error("enter again should unfold the group")
	}
}

// The arrows walk a cursor along the strip: the first press puts it on the
// last call, each press after moves it one call, and the call under it is
// lit in its group with the pointer. The bar names the strip's keys and
// which call the cursor is on.
func TestCard_TheStripWalksTheCalls(t *testing.T) {
	m := largeStepModel(t, 110)
	calls := largeStepCalls(m)
	m, _ = pressKey(t, m, readingChord())
	m.focusIdx = 1
	m.refreshFocusView()

	m, _ = pressKey(t, m, stripLeft)
	if !m.strip.on || m.strip.call != len(calls)-1 {
		t.Fatalf("the first arrow should put the cursor on the last call, got %+v", m.strip)
	}
	lit := func() string {
		for _, l := range strings.Split(stripANSI(m.renderHistory()), "\n") {
			if strings.HasPrefix(l, "❯") && strings.Contains(l, "▎") {
				return l
			}
		}
		return ""
	}
	if l := lit(); !strings.Contains(l, "✗ go test ./internal/ui/ ") {
		t.Errorf("the failed command's row should be lit with its glyph, got %q", l)
	}
	if bar := stripANSI(m.readingKeyLine(110)); !strings.Contains(bar, "[←→] along the strip") ||
		!strings.Contains(bar, "[enter] open that tool") || !strings.Contains(bar, "close the card") ||
		!strings.Contains(bar, "tool 22 of 22 · exit 1") {
		t.Errorf("the bar should name the strip's keys and the call: %q", bar)
	}
	m, _ = pressKey(t, m, stripLeft)
	m, _ = pressKey(t, m, stripLeft)
	if m.strip.call != len(calls)-3 {
		t.Fatalf("two more presses should walk back two calls, got %d", m.strip.call)
	}
	if l := lit(); !strings.Contains(l, "-run Copy") {
		t.Errorf("the lit row should follow the cursor, got %q", l)
	}
	m, _ = pressKey(t, m, stripRight)
	if m.strip.call != len(calls)-2 {
		t.Fatalf("→ should walk on one call, got %d", m.strip.call)
	}
	// A move of the reading cursor takes it off the strip.
	m, _ = pressKey(t, m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	if m.strip.on {
		t.Error("j should take the cursor off the strip")
	}
}

// Enter on the strip opens the call under its cursor in the view its own
// row opens: the same title and the same lines.
func TestCard_EnterOnTheStripOpensThatCall(t *testing.T) {
	m := largeStepModel(t, 110)
	calls := largeStepCalls(m)
	m, _ = pressKey(t, m, readingChord())
	m.focusIdx = 1
	m.refreshFocusView()
	m, _ = pressKey(t, m, stripLeft)
	m, _ = pressKey(t, m, enter)

	if m.state != stateOutputFull || m.fullOutput == nil {
		t.Fatalf("enter on the strip should open the call's own view, got state %d", m.state)
	}
	want := m.rowOutputView(m.transcript[calls[len(calls)-1]])
	if m.fullOutput.Title != want.Title || fmt.Sprint(m.fullOutput.Lines) != fmt.Sprint(want.Lines) {
		t.Errorf("the view is %q %q, want the row's %q %q", m.fullOutput.Title, m.fullOutput.Lines, want.Title, want.Lines)
	}

	// An edit opens the diff's own full screen.
	m2 := largeStepModel(t, 110)
	m2, _ = pressKey(t, m2, readingChord())
	m2.focusIdx = 1
	m2.refreshFocusView()
	m2 = pressKeys(t, m2, stripLeft, stripLeft)
	m2, _ = pressKey(t, m2, enter)
	if m2.state != stateDiffFull || m2.fullDiff == nil || m2.fullDiff.Path != "internal/ui/render.go" {
		t.Fatalf("enter on an edit's glyph should open its diff, got state %d", m2.state)
	}
}

// Esc from a call's view comes back to the card with the strip's cursor
// where it was; esc on the strip closes the card.
func TestCard_EscFromACallReturnsToTheStrip(t *testing.T) {
	m := largeStepModel(t, 110)
	calls := largeStepCalls(m)
	m, _ = pressKey(t, m, readingChord())
	m.focusIdx = 1
	m.refreshFocusView()
	m = pressKeys(t, m, stripLeft, stripLeft, stripLeft)
	at := m.strip.call
	m, _ = pressKey(t, m, enter)
	m, _ = pressKey(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})

	if m.state != stateFocus {
		t.Fatalf("esc should come back to reading mode, got state %d", m.state)
	}
	if !m.strip.on || m.strip.call != at {
		t.Fatalf("the strip's cursor should be where it was (%d), got %+v", at, m.strip)
	}
	if !strings.Contains(stripANSI(m.readingKeyLine(110)), fmt.Sprintf("tool %d of %d", at+1, len(calls))) {
		t.Errorf("the bar should still name the call: %q", stripANSI(m.readingKeyLine(110)))
	}

	m, _ = pressKey(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	blk, _ := m.cardBlockAt(m.transcript, 1)
	if m.strip.on || m.cardOpen(blk, m.transcript) || m.focusIdx != 1 {
		t.Errorf("esc on the strip should close the card with the cursor on it, strip %+v open %v at %d",
			m.strip, m.cardOpen(blk, m.transcript), m.focusIdx)
	}
}

// A click on a strip glyph moves the strip's cursor to that call, and a
// click on a group's line folds the group, without taking the keyboard.
func TestCard_AClickOnTheStripAndAGroupLine(t *testing.T) {
	m := largeStepModel(t, 110)
	m.pointer.mouseOn = true
	m.viewport.GotoTop()
	m.atBottom = false
	line := lineOf(t, m, "in order ")
	x, y := at(t, m, line, components.CardBodyIndent+len("in order ")+10)
	m = click(t, m, x, y)
	if !m.strip.on || m.strip.call != 10 {
		t.Fatalf("a click on the eleventh glyph should put the cursor on that call, got %+v", m.strip)
	}
	if m.state == stateFocus {
		t.Error("a click must not take the keyboard")
	}

	gx, gy := rowCell(t, m, "▾ ran 3 commands")
	m = click(t, m, gx, gy)
	if !m.transcript[largeStepCalls(m)[10]].groupFolded {
		t.Error("a click on a group's line should fold the group")
	}
}

// The strip's cursor goes when anything else takes the reader's place: a
// click on another line, which enter then acts on, and a card closed and
// opened again, which comes back with no cursor on its strip.
func TestCard_TheStripLetsGoOfAClickElsewhere(t *testing.T) {
	m := largeStepModel(t, 110)
	m.pointer.mouseOn = true
	m.viewport.GotoTop()
	m.atBottom = false
	m, _ = pressKey(t, m, readingChord())
	m.focusIdx = 1
	m.refreshFocusView()
	m, _ = pressKey(t, m, stripLeft)
	gx, gy := rowCell(t, m, "▾ ran 3 commands")
	m = click(t, m, gx, gy)
	if m.stripLive() {
		t.Fatal("a click on a group's line should take the cursor off the strip")
	}
	ran := largeStepCalls(m)[10]
	m, _ = pressKey(t, m, enter)
	if m.transcript[ran].groupFolded {
		t.Error("enter after the click should act on the group the click folded, unfolding it")
	}

	// Outside reading mode: a glyph clicked, the card folded and opened by
	// its header, and the strip comes back bare.
	m2 := largeStepModel(t, 110)
	m2.pointer.mouseOn = true
	m2.viewport.GotoTop()
	m2.atBottom = false
	x, y := at(t, m2, lineOf(t, m2, "in order "), components.CardBodyIndent+len("in order ")+3)
	m2 = click(t, m2, x, y)
	hx, hy := rowCell(t, m2, "read 14 files")
	m2 = click(t, m2, hx, hy)
	m2 = click(t, m2, hx, hy)
	if m2.stripLive() {
		t.Error("a card folded and opened again should not light its strip's old cursor")
	}
}

// The open card as the catalogue draws it: the large step open on its
// groups, one group folded, and the strip's cursor on the failed call with
// the bar naming the strip's keys.
func TestGolden_LargeStepOpen(t *testing.T) {
	captureGolden(t, "large-step-open", "a large step opened onto its calls", goldenWidths, func(width int) []golden.Panel {
		open := largeStepModel(t, width)

		folded := largeStepModel(t, width)
		folded.transcript[largeStepCalls(folded)[10]].groupFolded = true
		folded.invalidateRenderCache()

		strip := largeStepModel(t, width)
		strip, _ = pressKey(t, strip, readingChord())
		strip.focusIdx = 1
		strip.refreshFocusView()
		strip, _ = pressKey(t, strip, stripLeft)
		strip.viewport.GotoTop()

		return []golden.Panel{
			{Label: "open · the calls under the receipt's verbs, the strip under them", View: open.renderHistory()},
			{Label: "the commands' group folded · the other groups keep their rows", View: folded.renderHistory()},
			{Label: "the strip's cursor on the failed call · its row lit, the bar naming the strip's keys",
				View: readingSurface(strip)},
		}
	})
}
