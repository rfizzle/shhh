package chat

// Click targets (
// docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
// The two things a click can mean, the gesture it is told apart from, and the
// two properties that keep it from undoing what the selection and the
// mid-sentence rule settled: a drag is never a click, and a click is never a
// handover.

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/diff"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// click is a press and a release in the same cell, delivered the way a
// terminal delivers one. Whatever the release asked for is run, because
// answering a decision is a command rather than a state change (approval.go).
func click(t *testing.T, m Model, x, y int) Model {
	t.Helper()
	next, _ := m.Update(mousePress(x, y))
	rm, ok := next.(Model)
	if !ok {
		t.Fatal("a press should return the chat model")
	}
	next, cmd := rm.Update(mouseRelease(x, y))
	rm, ok = next.(Model)
	if !ok {
		t.Fatal("a release should return the chat model")
	}
	runCmd(cmd)
	return rm
}

// runCmd runs whatever a release asked for and returns the messages it
// produced, flattening a batch. A click that changes nothing returns no
// command at all, which is the common case here.
func runCmd(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			if c == nil {
				continue
			}
			if m := c(); m != nil {
				out = append(out, m)
			}
		}
		return out
	}
	if msg == nil {
		return nil
	}
	return []tea.Msg{msg}
}

// clickAnswer is click for a decision: the command the answer returned is run
// and the message it produced is fed back, which is how the approved tool's
// result reaches the transcript.
func clickAnswer(t *testing.T, m Model, x, y int) Model {
	t.Helper()
	next, _ := m.Update(mousePress(x, y))
	m = next.(Model)
	next, cmd := m.Update(mouseRelease(x, y))
	m = next.(Model)
	for _, msg := range runCmd(cmd) {
		out, _ := m.Update(msg)
		m = out.(Model)
	}
	return m
}

// clickModel is focusModel with mouse reporting on: a search row with twenty
// lines of output behind it, and a command row after it.
func clickModel(t *testing.T) Model {
	t.Helper()
	m := focusModel(t).WithMouse(true)
	m.viewport.SetLines(m.renderHistoryLines())
	m.viewport.GotoTop()
	m.atBottom = false
	return m
}

// rowCell is the screen cell the given rendered line's first column is at.
func rowCell(t *testing.T, m Model, want string) (x, y int) {
	t.Helper()
	return at(t, m, lineOf(t, m, want), 0)
}

func TestClick_OpensTheRowUnderIt(t *testing.T) {
	m := clickModel(t)
	if (*m.entries())[1].expanded {
		t.Fatal("the search row starts collapsed")
	}
	x, y := rowCell(t, m, "search")
	m = click(t, m, x, y)
	if !(*m.entries())[1].expanded {
		t.Fatal("a click on an activity row should open it, the way [enter] does")
	}
	// And close it again: the pointer reaches the same toggle the key does.
	m = click(t, m, x, y)
	if (*m.entries())[1].expanded {
		t.Fatal("a second click should close the row again")
	}
}

// A click reads the transcript, and reading is not a decision: the
// draft keeps every character and the keyboard it had.
func TestClick_NeverTakesTheKeyboard(t *testing.T) {
	m := clickModel(t)
	m.input.SetValue("half a sentence")
	x, y := rowCell(t, m, "search")
	m = click(t, m, x, y)
	if m.state == stateFocus {
		t.Fatal("a click must not open reading mode")
	}
	if got := m.input.Value(); got != "half a sentence" {
		t.Fatalf("the draft should be untouched, got %q", got)
	}
}

// The gesture the targets had to be compatible with: a press that goes
// somewhere before it comes up is a drag, and the drag owns it.
func TestClick_ADragIsNotAClick(t *testing.T) {
	c := &clip{}
	m := clickModel(t)
	m.copyFn = c.fn()
	line := lineOf(t, m, "search")
	x, y := at(t, m, line, 0)
	next, _ := m.Update(mousePress(x, y))
	m = next.(Model)
	ex, ey := at(t, m, line, endOf(m, line))
	next, _ = m.Update(mouseMotion(ex, ey))
	m = next.(Model)
	next, _ = m.Update(mouseRelease(ex, ey))
	m = next.(Model)
	if (*m.entries())[1].expanded {
		t.Fatal("a drag across a row must select it, not open it")
	}
	if c.calls != 1 {
		t.Fatalf("the drag should have copied what it covered, got %d copies", c.calls)
	}
}

// Prose is read rather than navigated, so there is nothing under the
// pointer for a click to mean.
func TestClick_ProseDoesNothing(t *testing.T) {
	m := clickModel(t)
	before := m.renderHistoryRaw()
	x, y := rowCell(t, m, "look around")
	m = click(t, m, x, y)
	if m.renderHistoryRaw() != before {
		t.Fatal("a click on prose should change nothing")
	}
}

// Inside reading mode the cursor is the reader's place in the rows, so a
// click moves it to the row they pointed at rather than leaving it behind.
func TestClick_ReadingModeMovesTheCursor(t *testing.T) {
	m := clickModel(t)
	updated, _ := m.Update(readingChord())
	m = updated.(Model)
	if m.focusIdx != 2 {
		t.Fatalf("reading mode should open on the last row, got %d", m.focusIdx)
	}
	x, y := rowCell(t, m, "search")
	m = click(t, m, x, y)
	if m.focusIdx != 1 {
		t.Fatalf("a click should put the cursor on the row it opened, got %d", m.focusIdx)
	}
	if !(*m.entries())[1].expanded {
		t.Fatal("the clicked row should have opened")
	}
	if m.state != stateFocus {
		t.Fatal("a click must not close reading mode")
	}
}

// --- the decision run -----------------------------------------------------

// cardKeyCell is the screen cell a decision key is drawn in, found the way a
// click finds it: by asking the card what it drew.
func cardKeyCell(t *testing.T, m Model, key string) (x, y int) {
	t.Helper()
	card := m.decisionCard()
	if card == nil {
		t.Fatal("no decision card is on screen")
	}
	for row, line := range strings.Split(m.screen(), "\n") {
		plain := ansi.Strip(line)
		for col := range ansi.StringWidth(plain) {
			if k, ok := card.KeyAt(line, col); ok && k == key {
				return col, row
			}
		}
	}
	t.Fatalf("the card drew no cell for %q", key)
	return 0, 0
}

// clickCardModel is a gated write_file decision with mouse reporting on. A
// draft in the box is what decides whether it arrives holding the keyboard,
// so the two card tests below differ only in that.
func clickCardModel(t *testing.T, draft string, executor ToolExecutor) Model {
	t.Helper()
	m := gatedModel(t, executor, map[string]GatedPreviewFunc{
		"write_file": writeFilePreview("line one\n"),
	}).WithMouse(true)
	m.width, m.height = 130, 40
	m.syncInputWidth()
	m.input.SetValue(draft)
	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_w", Name: "write_file", Arguments: `{"path":"main.go","content":"line one\nline two\n"}`},
	}})
	m = updated.(Model)
	if m.state != stateConfirmRun {
		t.Fatalf("the gated call should have raised a decision, got %d", m.state)
	}
	return m
}

// A clicked key is the keystroke: it goes to the handler [y] goes to, so
// there is no second decision path that could answer differently.
func TestClick_ApprovalKeyAnswers(t *testing.T) {
	var executed []string
	m := clickCardModel(t, "", func(name string, args json.RawMessage) (string, error) {
		executed = append(executed, name)
		return "wrote 2 lines", nil
	})
	if !m.decisionGated() {
		t.Fatal("a card arriving on an idle draft holds the keyboard")
	}
	x, y := cardKeyCell(t, m, "y")
	m = clickAnswer(t, m, x, y)
	if m.state == stateConfirmRun {
		t.Fatal("clicking [y] should have answered the decision")
	}
	if len(executed) != 1 {
		t.Fatalf("clicking [y] should have run the tool, got %v", executed)
	}
}

func TestClick_ApprovalDenyIsTheCapitalN(t *testing.T) {
	m := clickCardModel(t, "", func(name string, args json.RawMessage) (string, error) {
		t.Fatalf("nothing may run on a denial, but %s did", name)
		return "", nil
	})
	// The card draws the safe answer as `N` — the approval card's default
	// marker, not a shifted key — so the cell has to resolve to the keystroke
	// `n`.
	x, y := cardKeyCell(t, m, "n")
	m = clickAnswer(t, m, x, y)
	if m.state == stateConfirmRun {
		t.Fatal("clicking the safe answer should have denied the decision")
	}
}

// Invariant 5 through the pointer: a card whose keys are drawn not-yet-live
// cannot be answered by a gesture that skips the state saying so. The click
// means what the handover means, and the second one answers.
func TestClick_UngatedCardHandsOverRatherThanAnswering(t *testing.T) {
	var executed []string
	m := clickCardModel(t, "half a sentence", func(name string, args json.RawMessage) (string, error) {
		executed = append(executed, name)
		return "wrote 2 lines", nil
	})
	if !m.decisionUngated() {
		t.Fatal("a card landing on a live draft arrives ungated")
	}
	x, y := cardKeyCell(t, m, "y")
	m = click(t, m, x, y)
	if len(executed) != 0 {
		t.Fatal("a click on a not-yet-live key must not answer the decision")
	}
	if !m.decisionGated() {
		t.Fatal("the click should have handed the keyboard to the card")
	}
	if got := m.input.Value(); got != "half a sentence" {
		t.Fatalf("the handover must keep the draft, got %q", got)
	}
	// Now the keys are live, and the same cell answers.
	x, y = cardKeyCell(t, m, "y")
	m = clickAnswer(t, m, x, y)
	if len(executed) != 1 {
		t.Fatalf("the second click should have answered, got %v", executed)
	}
}

// A key clipped away by a narrow terminal is not clickable, because the
// target is read out of the render rather than laid out beside it.
func TestClick_OffTheRunAnswersNothing(t *testing.T) {
	m := clickCardModel(t, "", func(name string, args json.RawMessage) (string, error) {
		t.Fatalf("nothing may run without an answer, but %s did", name)
		return "", nil
	})
	x, y := cardKeyCell(t, m, "y")
	// Two cells left of the run's opening bracket is the question, not a key.
	m = click(t, m, x-3, y)
	if m.state != stateConfirmRun {
		t.Fatal("a click beside the run should leave the decision waiting")
	}
}

// A routed child approval is the same card component, so the
// pointer reaches it through the same door and lands in the same handler.
func TestClick_RoutedChildApproval(t *testing.T) {
	m := frameModel(t, 130, 40).WithMouse(true)
	ask := subagent.NewAsk("researcher-1", subagent.AskCommand, "run make")
	m.childAsks = []*subagent.Ask{ask}
	// Nothing in the draft, so the card holds the keyboard on arrival.
	m.armArrival()
	if !m.decisionGated() {
		t.Fatal("a card arriving on an idle draft holds the keyboard")
	}
	x, y := cardKeyCell(t, m, "y")
	m = clickAnswer(t, m, x, y)
	if len(m.childAsks) != 0 {
		t.Fatal("clicking [y] should have answered the routed decision")
	}
}

// A pointer obeys the grace window the way the keys do: the run is drawn
// dimmed, and a click on it answers nothing until the quiet arrives.
func TestClick_TheGraceWindowHoldsTheRun(t *testing.T) {
	var executed []string
	m := clickCardModel(t, "", func(name string, args json.RawMessage) (string, error) {
		executed = append(executed, name)
		return "wrote 2 lines", nil
	})
	// Re-arm the arrival on a keyboard still warm, so the window is open.
	m.releaseDecision()
	m.lastDecisionLeft = time.Time{}
	m.lastKeypress = time.Now()
	m.armDecision(stateConfirmRun)
	if !m.graceShowing() {
		t.Fatal("fixture: the grace window should be open")
	}

	x, y := cardKeyCell(t, m, "y")
	m = click(t, m, x, y)
	if len(executed) != 0 || m.state != stateConfirmRun {
		t.Fatal("a click inside the grace window must not answer the decision")
	}

	// Quiet arrived: the same cell answers.
	m.lastKeypress = time.Now().Add(-2 * graceQuiet)
	x, y = cardKeyCell(t, m, "y")
	m = clickAnswer(t, m, x, y)
	if len(executed) != 1 {
		t.Fatalf("after the quiet the click should have answered, got %v", executed)
	}
}

// --- the row line and the body under it -----------------------------------

// deepClickModel is clickModel with one more row behind it: a read whose
// output is longer than the in-place window holds, which is the only kind of
// row with a depth past that window to reach at all.
func deepClickModel(t *testing.T) Model {
	t.Helper()
	m := focusModel(t)
	var long strings.Builder
	for i := range maxExpandedResultLines * 2 {
		fmt.Fprintf(&long, "deep line %d\n", i)
	}
	m.appendEntry(entry{kind: entryTool, toolName: "read_file", toolArgs: `{"path":"main.go"}`,
		toolResult: strings.TrimRight(long.String(), "\n")})
	m = m.WithMouse(true)
	m.viewport.SetLines(m.renderHistoryLines())
	m.viewport.GotoTop()
	m.atBottom = false
	return m
}

// The complaint the split answers: a click that opened a row has to be
// undoable by the identical click. The keyboard's third depth takes the whole
// screen, and a pointer that walked the same cycle would have taken it here
// instead of giving the row back.
func TestClick_TheRowLineToggles(t *testing.T) {
	m := deepClickModel(t)
	x, y := rowCell(t, m, "main.go")
	m = click(t, m, x, y)
	if !(*m.entries())[3].expanded {
		t.Fatal("a click on the row line should open it")
	}
	x, y = rowCell(t, m, "main.go")
	m = click(t, m, x, y)
	if (*m.entries())[3].expanded {
		t.Fatal("the same cell pressed twice should give the row back")
	}
	if m.state == stateOutputFull {
		t.Fatal("the row line never takes the screen, however much output is behind it")
	}
}

// The other half of the cell: the body is content, and a click on content
// asks for that content whole.
func TestClick_TheBodyOpensItWhole(t *testing.T) {
	m := deepClickModel(t)
	x, y := rowCell(t, m, "main.go")
	m = click(t, m, x, y)
	bx, by := rowCell(t, m, "deep line 2")
	m = click(t, m, bx, by)
	if m.state != stateOutputFull || m.fullOutput == nil {
		t.Fatalf("a click in the body should open the full screen, got state %d", m.state)
	}
	if len(m.fullOutput.Lines) != maxExpandedResultLines*2 {
		t.Fatalf("the full screen holds the whole output, got %d lines", len(m.fullOutput.Lines))
	}
	if !(*m.entries())[3].expanded {
		t.Fatal("the row it came from stays open behind it: esc never destroys")
	}
}

// A window already showing everything has no screen past it — the same
// judgement the key makes when it skips that depth. What matters is the other
// half: the click must not fall through and close the body being read.
func TestClick_ABodyWithNothingDeeperStaysPut(t *testing.T) {
	m := clickModel(t)
	x, y := rowCell(t, m, "search")
	m = click(t, m, x, y)
	before := m.renderHistoryRaw()
	bx, by := rowCell(t, m, "result line 3")
	m = click(t, m, bx, by)
	if m.state == stateOutputFull {
		t.Fatal("a body that fits its window has nothing deeper to open")
	}
	if !(*m.entries())[1].expanded {
		t.Fatal("a click on the body must not close the row under the pointer")
	}
	if m.renderHistoryRaw() != before {
		t.Fatal("a click on a body with nothing behind it should change nothing")
	}
}

// diffClickModel is a session whose last row is an applied edit.
func diffClickModel(t *testing.T) Model {
	t.Helper()
	m := focusModel(t)
	m.appendEntry(entry{kind: entryDiff, diff: &components.DiffView{
		Path:  "internal/agent/loop.go",
		Verb:  "edit",
		Hunks: diff.Compute("old line\n", "new line\n"),
	}})
	m = m.WithMouse(true)
	m.viewport.SetLines(m.renderHistoryLines())
	m.viewport.GotoTop()
	m.atBottom = false
	return m
}

// A diff's three modes split the same way: the row line is the toggle, and
// the change itself is what the full screen is for.
func TestClick_TheDiffRowLineToggles(t *testing.T) {
	m := diffClickModel(t)
	x, y := rowCell(t, m, "loop.go")
	m = click(t, m, x, y)
	d := (*m.entries())[3].diff
	if d.Mode != components.DiffExpanded {
		t.Fatalf("a click on the row line should expand the diff, got mode %d", d.Mode)
	}
	x, y = rowCell(t, m, "loop.go")
	m = click(t, m, x, y)
	if d.Mode != components.DiffCollapsed {
		t.Fatalf("the same cell should collapse it again, got mode %d", d.Mode)
	}
	if m.state == stateDiffFull {
		t.Fatal("the row line never takes the screen")
	}
}

func TestClick_TheDiffBodyOpensFullScreen(t *testing.T) {
	m := diffClickModel(t)
	x, y := rowCell(t, m, "loop.go")
	m = click(t, m, x, y)
	bx, by := rowCell(t, m, "new line")
	m = click(t, m, bx, by)
	if m.state != stateDiffFull {
		t.Fatalf("a click on the change should open the full screen, got state %d", m.state)
	}
	if d := (*m.entries())[3].diff; d.Mode != components.DiffFull {
		t.Fatalf("the view should be in its full mode, got %d", d.Mode)
	}
}

// The think row's three depths are all in place, so there is no deeper
// surface a body click could open — and the cycle wraps, so the row line
// alone still reaches every depth and closes it again.
func TestClick_TheThinkRowCyclesFromItsRowLine(t *testing.T) {
	m := focusModel(t)
	m.appendEntry(entry{kind: entryThink, text: "the cap is a checkpoint, not a wall"})
	m = m.WithMouse(true)
	m.viewport.SetLines(m.renderHistoryLines())
	m.viewport.GotoTop()
	m.atBottom = false

	line := lineOf(t, m, "✻ think")
	x, y := at(t, m, line, 0)
	depths := map[thinkDepth]bool{}
	for range 3 {
		m = click(t, m, x, y)
		depths[m.thinkDepthOf((*m.entries())[3])] = true
		line = lineOf(t, m, "✻ think")
		x, y = at(t, m, line, 0)
	}
	if !depths[thinkClosed] {
		t.Fatalf("the cycle should come back round to closed, saw %v", depths)
	}
}
