package chat

// Reading mode against the Reading artboard (
// docs/interface/surfaces.md#reading-mode). The behaviour is reading mode's
// and settled; what is checked here is the dressing — the labelled rail, the
// lit row, the hint bar that replaces the frame, and the rule that only one
// pane wears any of it at a time.

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/diff"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// colorProfile forces 256-color rendering: a test binary's stdout is not a
// terminal, so lipgloss would otherwise emit no escapes and every question
// about colour would answer itself.
func colorProfile(t *testing.T) {
	t.Helper()
	was := components.Profile()
	components.SetProfile(colorprofile.ANSI256)
	t.Cleanup(func() { components.SetProfile(was) })
}

// readingModel is the golden transcript with the keyboard already handed to
// it: a two-step turn with an edit, a broken command and a close block.
func readingModel(t *testing.T, width int) Model {
	t.Helper()
	m := goldenModel(t, width)
	next, _ := m.enterFocusMode()
	return next.(Model)
}

// The rail is the same labelled rule DRAFT and DECISION draw: four
// cells in, the label in its own spaces, the rule to the edge. Reading mode
// borrowed Rule's trailing variant when it had no artboard to read, and that
// hung the label off the right end.
func TestReadingRail_IsLabelledFourCellsIn(t *testing.T) {
	m := readingModel(t, 130)
	rail := ansi.Strip(m.readingRail(m.contentWidth()))

	if !strings.HasPrefix(rail, "──── READING ") {
		t.Fatalf("the rail should carry its label four cells in, got %q", rail)
	}
	if lipgloss.Width(rail) != m.contentWidth() {
		t.Fatalf("the rail should run the full width, got %d of %d", lipgloss.Width(rail), m.contentWidth())
	}
	if strings.HasSuffix(rail, "READING 5/5 ─") {
		t.Fatal("the label is no longer hung off the right end")
	}
}

// Below the minimal breakpoint the word goes rather than being cut down
// (guidelines/layout-breakpoints): a clipped label says less than no label.
func TestReadingRail_DropsTheLabelBelowTheMinimalBreakpoint(t *testing.T) {
	wide := ansi.Strip(readingModel(t, 104).readingRail(100))
	if !strings.Contains(wide, "READING") {
		t.Fatalf("at 100 columns the label still fits, got %q", wide)
	}
	narrow := ansi.Strip(readingModel(t, 62).readingRail(58))
	if strings.Contains(narrow, "READING") {
		t.Fatalf("below 70 columns the rail is a bare divider, got %q", narrow)
	}
	if strings.Trim(narrow, "─") != "" || lipgloss.Width(narrow) != 58 {
		t.Fatalf("the fallback is a plain divider of the full width, got %q", narrow)
	}
}

// The row under the cursor is lit: the focus background runs its width, its
// words go bright, and the pointer stays outside the highlight.
func TestReadingRow_IsLitWithThePointerOutsideIt(t *testing.T) {
	colorProfile(t)
	m := readingModel(t, 130)
	line := focusedLine(t, m)

	pointer, rest, ok := strings.Cut(line, " ")
	if !ok || !strings.Contains(pointer, "❯") {
		t.Fatalf("the lit row should be pointed at, got %q", line)
	}
	if strings.Contains(pointer, "48;5;") {
		t.Fatalf("the pointer sits outside the highlight, got %q", pointer)
	}
	if !strings.Contains(rest, "48;5;62") {
		t.Fatalf("the row should carry the focus background, got %q", rest)
	}
	if !strings.Contains(rest, "97;48;5;62") {
		t.Fatalf("the row's words should be bright inside the highlight, got %q", rest)
	}
}

// It still reads over a row that changed the machine: the rail is drawn
// inside the highlight and keeps its accent, and the bright text is what
// changes.
func TestReadingRow_KeepsTheMutationRailInsideTheHighlight(t *testing.T) {
	colorProfile(t)
	m := readingModel(t, 130)
	m.moveFocus(-1)
	m.moveFocus(-1)
	line := focusedLine(t, m)

	if !strings.Contains(ansi.Strip(line), "▎✎ edit") {
		t.Fatalf("expected the edit row under the cursor, got %q", ansi.Strip(line))
	}
	// The rail keeps its own colour rather than being repainted bright with
	// the words, and the background is armed before it: the highlight runs
	// under the rail rather than starting after it.
	bg, accent := strings.Index(line, "48;5;62"), strings.Index(line, "38;5;214m▎")
	if accent < 0 || bg < 0 || bg > accent {
		t.Fatalf("the mutation rail should keep its accent inside the highlight, got %q", line)
	}
	// The glyph beside it keeps its accent too, and the background is put
	// back after the rail's own reset rather than being punched through.
	if !strings.Contains(line, "\x1b[48;5;62m\x1b[38;5;214m✎") {
		t.Fatalf("the kind glyph should stay accented inside the highlight, got %q", line)
	}
}

// focusedLine is the rendered line the cursor is on.
func focusedLine(t *testing.T, m Model) string {
	t.Helper()
	content, start, _ := m.renderFocusHistory()
	lines := strings.Split(content, "\n")
	if start < 0 || start >= len(lines) {
		t.Fatalf("the cursor points at line %d of %d", start, len(lines))
	}
	return lines[start]
}

// The hint bar carries the artboard's keys in the artboard's order, with the
// position on the right.
func TestReadingHint_CarriesTheModeKeysInOrder(t *testing.T) {
	m := readingModel(t, 130)
	// The cursor starts on the close row, which expands nothing; [enter] is
	// offered where the row honours it, so step to one that does.
	m.moveFocus(-1)
	m.moveFocus(-1)
	line := ansi.Strip(m.readingKeyLine(m.contentWidth()))

	for i, want := range []string{"[j/k] move", "[enter] expand", "[q] back to the prompt"} {
		idx := strings.Index(line, want)
		if idx < 0 {
			t.Fatalf("the bar should offer %q, got %q", want, line)
		}
		if i > 0 && idx < strings.Index(line, "[j/k] move") {
			t.Fatalf("the keys are out of order in %q", line)
		}
	}
	if !strings.Contains(line, "row 3 of 5") {
		t.Fatalf("the position is the right-hand field, got %q", line)
	}
}

// [-] is offered only while the row under the cursor is open, and it closes
// it. Where nothing is open it is a character like any other.
func TestReadingHint_CollapseIsOfferedOnlyWhenSomethingIsOpen(t *testing.T) {
	m := readingModel(t, 130)
	m.moveFocus(-1)
	m.moveFocus(-1)
	if strings.Contains(ansi.Strip(m.readingKeyLine(m.contentWidth())), "[-] collapse") {
		t.Fatal("nothing is open yet, so nothing should offer to close it")
	}

	// [-] with nothing open is a character, and it lands in the draft.
	typed, _ := m.updateFocus(tea.KeyPressMsg{Code: []rune(keys.Shown(keys.Reading.Collapse))[0], Text: keys.Shown(keys.Reading.Collapse)})
	if got := typed.(Model); got.state != stateFocus && got.input.Value() != keys.Shown(keys.Reading.Collapse) {
		t.Fatalf("an unclaimed [-] should return to the draft carrying itself, got %q", got.input.Value())
	}

	opened, _ := m.updateFocus(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = opened.(Model)
	if !strings.Contains(ansi.Strip(m.readingKeyLine(m.contentWidth())), "[-] collapse") {
		t.Fatalf("an open row should offer to close it, got %q", ansi.Strip(m.readingKeyLine(m.contentWidth())))
	}
	if !strings.Contains(ansi.Strip(m.readingKeyLine(m.contentWidth())), "1 row expanded") {
		t.Fatal("the position field reports what is open once something is")
	}

	closed, _ := m.updateFocus(tea.KeyPressMsg{Code: []rune(keys.Shown(keys.Reading.Collapse))[0], Text: keys.Shown(keys.Reading.Collapse)})
	m = closed.(Model)
	if m.state != stateFocus {
		t.Fatalf("[-] on an open row is the mode's own key, got state %d", m.state)
	}
	if m.expandedRowCount() != 0 {
		t.Fatal("[-] should have closed the row under the cursor")
	}
}

// A row's own keys are a second line prefixed by that row's ▎, so a key that
// acts on one row never reads as a key that acts on the session. A row that
// offers none renders no second line at all.
func TestReadingHint_RowKeysAreASecondLineUnderTheRowsOwnRail(t *testing.T) {
	m := readingModel(t, 130)
	rows := m.readingRowLines(m.contentWidth(), inputHeight-1)
	if len(rows) != 1 {
		t.Fatalf("the close row offers keys, so it should carry one line, got %d", len(rows))
	}
	line := ansi.Strip(rows[0])
	if !strings.HasPrefix(line, "▎this row · ") {
		t.Fatalf("the row's keys carry the row's own rail, got %q", line)
	}
	for _, want := range []string{"[v] review", "[u] undo turn", "[esc] nothing"} {
		if !strings.Contains(line, want) {
			t.Fatalf("the row should offer %q, got %q", want, line)
		}
	}

	m.moveFocus(-1)
	m.moveFocus(-1)
	if rows := m.readingRowLines(m.contentWidth(), inputHeight-1); len(rows) != 0 {
		t.Fatalf("an edit row offers no keys, so it should say nothing, got %q", rows)
	}
}

// The pair, rendered rather than asserted: whichever pane holds the
// keyboard wears the labelled rail and the lit row, and the other wears the
// frame's accent. Never both, never neither.
func TestReadingMode_OnlyOnePaneIsDressedAtATime(t *testing.T) {
	colorProfile(t)
	const width = 140
	idle := goldenModel(t, width)
	next, _ := idle.enterFocusMode()
	reading := next.(Model)

	idleView, readingView := idle.View().Content, reading.View().Content

	if strings.Contains(ansi.Strip(idleView), "READING") {
		t.Fatal("the input has the keyboard, so the rail says nothing")
	}
	if !strings.Contains(ansi.Strip(readingView), "READING") {
		t.Fatal("the transcript has the keyboard, so the rail names it")
	}
	if strings.Contains(idleView, "48;5;62") {
		t.Fatal("no row is lit while the input has the keyboard")
	}
	if !strings.Contains(readingView, "48;5;62") {
		t.Fatal("the row under the cursor is lit while the transcript has the keyboard")
	}
	// The frame is the input's own dressing, and reading mode replaces it
	// with the hint bar rather than dimming it: two bottom elements is how
	// you get a session where nobody can tell which one enter belongs to.
	if !idle.frameShowing() {
		t.Fatal("the input should be wearing its frame")
	}
	if reading.frameShowing() {
		t.Fatal("the frame goes when the transcript takes the keyboard")
	}
	if !strings.Contains(ansi.Strip(readingView), "[q] back to the prompt") {
		t.Fatal("the hint bar stands where the frame was")
	}
}

// The key line shortens rather than clipping, and the position narrows before
// the keys give up any of their words.
func TestReadingHint_ShortensRatherThanClipping(t *testing.T) {
	for _, width := range []int{120, 80, 60, 46, 30} {
		m := readingModel(t, 130)
		line := ansi.Strip(m.readingKeyLine(width))
		if lipgloss.Width(line) > width {
			t.Fatalf("at %d columns the bar overflowed: %q", width, line)
		}
		if strings.Contains(line, "…") {
			t.Fatalf("at %d columns a key was clipped: %q", width, line)
		}
		if !strings.Contains(line, "[j/k] move") || !strings.Contains(line, "[q]") {
			t.Fatalf("at %d columns the bar lost a key it cannot lose: %q", width, line)
		}
	}
}

// Mono has to keep the distinction the colours carry: the label, the keys and
// the lit row all survive as words and greys.
func TestReadingMode_SurvivesMono(t *testing.T) {
	colorProfile(t)
	monoRestore(t)
	components.SetMono(true)

	m := readingModel(t, 130)
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "READING") {
		t.Fatal("the rail's word is what carries it, so mono keeps it")
	}
	if !strings.Contains(view, "[q] back to the prompt") {
		t.Fatal("the hint bar is words before it is colours")
	}
	litBg := ansi.NewStyle().BackgroundColor(components.MonoBg.Color()).String()
	if !strings.Contains(m.View().Content, litBg) {
		t.Fatal("the lit row keeps a background in mono; the two greys are what it has")
	}
}

// The key register on the page (
// docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).

// TestReadingKeyListNamesEveryModeKey is what `[?]` is for: the bar sheds
// keys as the terminal narrows and never says which, and this is where they
// went. A key in reading mode's register that the list does not print is a
// key a reader has no way left to find.
func TestReadingKeyListNamesEveryModeKey(t *testing.T) {
	m := readingModel(t, 100)
	lines := strings.Join(m.readingKeyListLines(100, 40), "\n")
	for _, b := range keys.Reading.All() {
		if !strings.Contains(ansi.Strip(lines), "["+keys.Shown(b)+"]") {
			t.Errorf("the key list never names %q (%s)", keys.Shown(b), keys.Words(b))
		}
	}
}

// The list carries the row's own offers too, under the row's rail — the
// answer to "what can this keyboard do from here" is the mode's keys plus
// the row's, and a list that stopped at the mode's would be answering a
// different question.
func TestReadingKeyListCarriesTheRowsOffers(t *testing.T) {
	m := readingModel(t, 100)
	offers := m.readingRowOffers()
	if len(offers) == 0 {
		t.Fatal("the fixture's cursor is on a row with no offers; the test needs one that has them")
	}
	lines := ansi.Strip(strings.Join(m.readingKeyListLines(100, 40), "\n"))
	for _, o := range offers {
		if !strings.Contains(lines, o.Key) {
			t.Errorf("the key list drops the row's own %s", o.Key)
		}
	}
}

// The panel is bounded like every other one. What does not fit is
// counted rather than dropped in silence (invariant 4).
func TestReadingKeyListCountsWhatDoesNotFit(t *testing.T) {
	m := readingModel(t, 100)
	lines := m.readingKeyListLines(100, 3)
	if len(lines) != 3 {
		t.Fatalf("the list ignored its bound: %d lines", len(lines))
	}
	if last := ansi.Strip(lines[2]); !strings.Contains(last, "more keys") {
		t.Errorf("the last row does not say what it swallowed: %q", last)
	}
}

// `?` is live in reading mode and, from the input, only on an empty draft:
// invariant 5 read literally — a bare letter is a letter while a sentence is
// being typed, and reading mode is a takeover where nothing else is
// listening.
func TestKeyListIsNotAKeyFromTheDraft(t *testing.T) {
	m := typeChars(t, frameModel(t, 100, 40), "how do I")
	next, _ := m.Update(tea.KeyPressMsg{Code: '?', Text: "?"})
	after := next.(Model)
	if after.readingKeyList {
		t.Error("? opened the key register from a live draft")
	}
	if !strings.Contains(after.input.Value(), "?") {
		t.Errorf("? did not land in the draft: %q", after.input.Value())
	}
}

// The bar offers `j/k` as one segment and the dispatch reads the direction
// off the keystroke, so this is what holds the four keys together: every
// keystroke the register puts on the movement binding actually moves, and
// nothing else does.
func TestReadingMoveAnswersEveryDeclaredKey(t *testing.T) {
	for _, k := range keys.Reading.Move.Keys() {
		m := readingModel(t, 100)
		// Away from both ends, so a key that moves has somewhere to go.
		m.moveFocus(-1)
		before := m.focusIdx
		next, _ := m.updateFocus(tea.KeyPressMsg{Code: []rune(k)[0], Text: k})
		if len(k) > 1 {
			// The arrow keys arrive as their own code, carrying no text.
			code := map[string]rune{"up": tea.KeyUp, "down": tea.KeyDown}[k]
			next, _ = m.updateFocus(tea.KeyPressMsg{Code: code})
		}
		if after := next.(Model); after.focusIdx == before {
			t.Errorf("%q is on keys.Reading.Move but moves nothing", k)
		}
	}
}

// The whole-pane fold: esc on an empty draft puts back every row the reader
// opened (readinghint.go, keyroute.go). The one-row collapse above and this
// read the same closed list of open kinds, so the cases here are about which
// answers are the reader's and what the press costs the rest of the chain.

// escFoldModel is a pane with one of every way a row can be open, each on a
// row of its own: a step unfolded, a read-only run given back, a body
// expanded, a second step's detail opened by /step, a diff expanded in place,
// and a reasoning row opened whole.
func escFoldModel(t *testing.T) Model {
	t.Helper()
	// A pane shorter than what the open rows render to, so folding them
	// really does move the transcript under the reader.
	updated, _ := activityModel(t).Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	m := updated.(Model)
	m.transcript = []entry{
		{kind: entryUser, text: "fix the round limit"},
		{kind: entryAssistant, text: "Locate the round accounting"},
		readEntry("internal/agent/loop.go", 400*time.Millisecond),
		readEntry("internal/agent/round.go", 200*time.Millisecond),
		readEntry("internal/agent/tool.go", 300*time.Millisecond),
		searchEntry("ErrRoundLimit", 800*time.Millisecond),
		{kind: entryAssistant, text: "Thread the sentinel through the loop"},
		{kind: entryTool, toolName: "edit_file", toolArgs: `{"path":"internal/agent/loop.go"}`,
			toolResult: "edited", duration: 1100 * time.Millisecond},
		{kind: entryCommand, text: "go test ./internal/agent/...",
			toolResult: "--- FAIL: TestRoundLimit", exitCode: 1, duration: 21400 * time.Millisecond},
		{kind: entryDiff, diff: &components.DiffView{Path: "internal/agent/loop.go", Verb: "edit",
			Hunks: diff.Compute("old line\n", "new line\n"), Mode: components.DiffExpanded}},
		{kind: entryThink, text: "the cap is a checkpoint, not a wall", thinkDepth: thinkFull},
	}
	m.transcript[1].stepFold = foldOpen
	m.transcript[2].groupFold = foldOpen
	m.transcript[3].expanded = true
	m.transcript[6].detailFold = foldOpen
	// A tail of plain rows, so the folded pane is still taller than the
	// window and the anchor is a real scroll position rather than the end.
	for i := 0; i < 14; i++ {
		m.transcript = append(m.transcript, entry{kind: entryCommand,
			text: fmt.Sprintf("go test ./internal/agent/round%d", i), toolResult: "ok"})
	}
	m.invalidateRenderCache()
	m.refreshTranscript()
	return m
}

// openRowCount is how many rows carry an answer of the reader's, counted the
// way the fold counts them.
func openRowCount(m Model) int {
	n := 0
	for _, e := range *m.entries() {
		if len(readerOpened(e)) > 0 {
			n++
		}
	}
	return n
}

func TestEscFold_FoldsEveryRowTheReaderOpened(t *testing.T) {
	m := escFoldModel(t)
	if got := openRowCount(m); got != 6 {
		t.Fatalf("the fixture has %d open rows, wanted one of each of the six", got)
	}

	m, _ = pressKey(t, m, escK)

	if got := openRowCount(m); got != 0 {
		t.Errorf("one esc left %d rows open", got)
	}
	if m.foldNotice != "folded 6 rows" {
		t.Errorf("the rail says %q, wanted the fold counted", m.foldNotice)
	}
	// Resting and not closed: a fold that wrote foldClosed would outrank the
	// setting instead of deferring to it.
	es := *m.entries()
	if es[1].stepFold != foldAuto || es[6].detailFold != foldAuto || es[6].stepFold != foldAuto {
		t.Error("a step was folded past its resting state")
	}
	if es[2].groupFold != foldAuto {
		t.Error("a group was folded past its resting state")
	}
	if es[10].thinkDepth != thinkAuto {
		t.Error("a think row was folded past its resting state")
	}
	if es[9].diff.Mode != components.DiffCollapsed || es[3].expanded {
		t.Error("a diff or a body is still open")
	}
}

func TestEscFold_SaysNothingWithThePaneAlreadyAtRest(t *testing.T) {
	m := activityModel(t)
	m.transcript = goldenTranscript()
	m.invalidateRenderCache()

	m, _ = pressKey(t, m, escK)

	if m.foldNotice != "" {
		t.Errorf("a press that folded nothing said %q", m.foldNotice)
	}
}

func TestEscFold_LeavesWhatTheVerbosityOpenedAndNamesIt(t *testing.T) {
	m := activityModel(t)
	m.transcript = goldenTranscript()
	m.verbosity = verbosityHigh
	m.checkpoints = []checkpoint{{index: 1, preview: "make it fast"}}
	m.invalidateRenderCache()
	if !m.settingHoldsRowsOpen() {
		t.Fatal("the fixture has no rows the verbosity is holding open")
	}

	m, _ = pressKey(t, m, escK)

	if got := openRowCount(m); got != 0 {
		t.Errorf("the verbosity's rows landed on the transcript as answers of the reader's: %d", got)
	}
	if !m.settingHoldsRowsOpen() {
		t.Error("esc folded rows the verbosity had opened")
	}
	if m.foldNotice != verbosityHoldsNotice {
		t.Errorf("the rail says %q, wanted it to name the setting", m.foldNotice)
	}
	// Not claimed: the chain went on and armed the rewind gesture, which is
	// what this press has always meant on an empty idle draft.
	if !m.armed.open(armRewind) {
		t.Error("a press that folded nothing claimed the press")
	}
}

func TestEscFold_ADraftWithTextIsClearedAndNoRowMoves(t *testing.T) {
	m := typeChars(t, escFoldModel(t), "half a thought")

	m, _ = pressKey(t, m, escK)

	if m.input.Value() != "" {
		t.Fatal("esc with a draft must clear it")
	}
	if got := openRowCount(m); got != 6 {
		t.Errorf("clearing the draft folded rows: %d of 6 left open", got)
	}
	if m.foldNotice != "" {
		t.Errorf("clearing the draft said %q about folding", m.foldNotice)
	}
}

func TestEscFold_RunsUnderAStreamingTurnAndSpendsNoArmedWindow(t *testing.T) {
	m := escFoldModel(t)
	m.state = stateStreaming
	m.armPress(armCancel, "ctrl+c")
	was := m.armed

	m, _ = pressKey(t, m, escK)

	if got := openRowCount(m); got != 0 {
		t.Errorf("the fold did not run under a streaming turn: %d rows left open", got)
	}
	if m.armed != was {
		t.Error("the fold spent the two-press window; it abandons nothing and answers nothing")
	}
}

func TestEscFold_KeepsTheRowUnderTheTopOfThePane(t *testing.T) {
	m := escFoldModel(t)
	// The first of the plain rows after the opened ones, and the only row on
	// the pane that says round0.
	const anchor, anchorText = 11, "round0"
	at, ok := m.entryLineStarts()[anchor]
	if !ok {
		t.Fatal("the anchor row is not on the rendered pane")
	}
	m.viewport.SetYOffset(at)
	m.atBottom = m.viewport.AtBottom()
	if m.atBottom {
		t.Fatal("the fixture is not scrolled up, so the anchor would be kept trivially")
	}

	m, _ = pressKey(t, m, escK)

	// Read against the lines the pane is actually showing rather than against
	// the walk the anchor was taken with: the two agreeing with each other
	// would say nothing about where the row went.
	lines := m.renderHistoryLines()
	top := m.viewport.YOffset()
	if top >= len(lines) {
		t.Fatalf("the pane is at line %d of %d", top, len(lines))
	}
	if got := ansi.Strip(lines[top]); !strings.Contains(got, anchorText) {
		t.Errorf("the top of the pane is %q, wanted the row that was under it", got)
	}
}

func TestEscFold_AReaderFollowingTheStreamStaysAtTheEnd(t *testing.T) {
	m := escFoldModel(t)
	m.viewport.GotoBottom()
	m.atBottom = true

	m, _ = pressKey(t, m, escK)

	if !m.viewport.AtBottom() {
		t.Error("the fold left a reader who was following the stream off the live end")
	}
}

func TestEscFold_TheNoticeLastsOnePress(t *testing.T) {
	m := escFoldModel(t)

	m, _ = pressKey(t, m, escK)
	if m.foldNotice == "" {
		t.Fatal("the fold said nothing")
	}
	m = typeChars(t, m, "a")
	if m.foldNotice != "" {
		t.Errorf("the account of the last press outlived it: %q", m.foldNotice)
	}
}

// The two step overrides diverge as soon as the reader answers them
// separately, and the fold has to put each back on its own: /step opens both,
// and [-] on the header then closes one of them and leaves the other standing
// (steps.go, detail.go).
func TestEscFold_PutsTheTwoStepAnswersBackOneAtATime(t *testing.T) {
	m := escFoldModel(t)
	es := *m.entries()
	// /step on the second step, then the header folded by hand: an explicit
	// no on the fold, an open answer on the detail.
	es[6].stepFold, es[6].detailFold = foldClosed, foldOpen

	m, _ = pressKey(t, m, escK)

	after := *m.entries()
	if after[6].stepFold != foldClosed {
		t.Error("the fold overwrote a step the reader had closed; esc puts back what was opened")
	}
	if after[6].detailFold != foldAuto {
		t.Error("the detail answer was left on record with nothing on screen reporting it")
	}
}

// A step the reader folded shut shows none of its rows, so the verbosity is
// holding nothing open inside it and the rail must not say it is.
func TestEscFold_AClosedStepHidesTheSettingsRowsToo(t *testing.T) {
	m := activityModel(t)
	m.transcript = goldenTranscript()
	m.verbosity = verbosityHigh
	m.invalidateRenderCache()
	if !m.settingHoldsRowsOpen() {
		t.Fatal("the fixture starts with rows the verbosity is holding open")
	}

	// Both steps folded by hand: the pane is headers and nothing else.
	for i := range m.transcript {
		if m.transcript[i].kind == entryAssistant {
			m.transcript[i].stepFold = foldClosed
		}
	}
	m.invalidateRenderCache()

	m, _ = pressKey(t, m, escK)

	if m.foldNotice != "" {
		t.Errorf("a pane whose rows are all folded shut said %q", m.foldNotice)
	}
}
