package chat

// Reading mode against the Reading artboard (S-122, DESIGN-TUI.md §7a). The
// behaviour is S-115's and settled; what is checked here is the dressing —
// the labelled rail, the lit row, the hint bar that replaces the frame, and
// the rule that only one pane wears any of it at a time.

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
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

// The rail is the same labelled rule DRAFT and DECISION draw (§7b): four
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
// changes (§14).
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
	if !strings.HasSuffix(strings.TrimRight(line, " "), "row 5 of 5") {
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

// The pair, rendered rather than asserted (§7a): whichever pane holds the
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
// the keys give up any of their words (§7a).
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
// the lit row all survive as words and greys (S-095, §10f).
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

// The key register on the page (S-153, DESIGN-TUI.md §7d).

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
// different question (§7a).
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

// The panel is bounded like every other one (§1). What does not fit is
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

// `?` is live in reading mode and nowhere near the draft, which is invariant
// 5 read literally: a bare letter is a letter while a sentence is being
// typed, and reading mode is a takeover where nothing else is listening.
func TestKeyListIsNotAKeyFromTheDraft(t *testing.T) {
	m := frameModel(t, 100, 40)
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
