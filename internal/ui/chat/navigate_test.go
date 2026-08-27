package chat

// Terminal interactivity and prompt↔transcript focus (S-115, §7a): the wheel
// reaches the transcript, typed letters do not, and there are named ways in
// and out that a draft can never produce by accident.

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/diff"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// wheel builds a wheel notch in dir (-1 up, +1 down).
func wheel(dir int) tea.MouseMsg {
	btn := tea.MouseButtonWheelUp
	if dir > 0 {
		btn = tea.MouseButtonWheelDown
	}
	return tea.MouseMsg{Action: tea.MouseActionPress, Button: btn}
}

// proseModel is a transcript with plenty to scroll and nothing to expand: an
// exchange of messages, which is what most of a chat session actually is.
func proseModel(t *testing.T) Model {
	t.Helper()
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	m = updated.(Model)
	for i := 0; i < 12; i++ {
		m.appendEntry(entry{kind: entryUser, text: "a question about the parser"})
		m.appendEntry(entry{kind: entryAssistant, text: "an answer with several\nlines of prose in it"})
	}
	m.viewport.SetContent(m.renderHistory())
	m.viewport.GotoBottom()
	m.atBottom = true
	return m
}

// diffFullModel is a full-screen viewer over a patch long enough to scroll.
func diffFullModel(t *testing.T) Model {
	t.Helper()
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	m = updated.(Model)

	lines := make([]diff.Line, 0, 60)
	for i := 1; i <= 60; i++ {
		lines = append(lines, diff.Line{Kind: diff.Add, Text: "added line", NewNo: i})
	}
	m.fullDiff = &components.DiffView{
		Path: "internal/agent/loop.go", Verb: "edit", Mode: components.DiffFull,
		Hunks:  []diff.Hunk{{OldStart: 1, NewStart: 1, NewCount: len(lines), Lines: lines}},
		Height: m.viewportHeight(),
	}
	m.enterSurface(stateDiffFull)
	return m
}

func TestWheel_ScrollsTheTranscriptAndLeavesTheDraftAlone(t *testing.T) {
	m := typeChars(t, proseModel(t), "half a sentence")
	before := m.viewport.YOffset

	updated, _ := m.Update(wheel(-1))
	m = updated.(Model)

	if m.viewport.YOffset >= before {
		t.Fatalf("the wheel should scroll the transcript up, offset %d → %d", before, m.viewport.YOffset)
	}
	if m.input.Value() != "half a sentence" {
		t.Fatalf("the wheel must not touch the draft, got %q", m.input.Value())
	}
	if m.state != stateInput {
		t.Fatalf("the wheel reads; it should not take the keyboard, got state %d", m.state)
	}

	updated, _ = m.Update(wheel(1))
	if got := updated.(Model).viewport.YOffset; got <= m.viewport.YOffset {
		t.Fatalf("the wheel should scroll back down, offset %d → %d", m.viewport.YOffset, got)
	}
}

// The wheel is the one gesture that reaches a full-screen viewer, since the
// transcript behind it is not what the reader is looking at (§3c).
func TestWheel_ReachesTheFullScreenDiff(t *testing.T) {
	m := diffFullModel(t)
	before := m.fullDiff.Offset

	updated, _ := m.Update(wheel(1))
	m = updated.(Model)

	if m.fullDiff.Offset <= before {
		t.Fatalf("the wheel should scroll the full-screen diff, offset %d → %d", before, m.fullDiff.Offset)
	}
	if m.viewport.YOffset != 0 {
		t.Fatalf("the transcript behind the viewer should not have moved, got %d", m.viewport.YOffset)
	}
}

func TestWheel_IgnoredWhileMouseReportingIsOff(t *testing.T) {
	m := proseModel(t)
	m.mouseOff = true
	before := m.viewport.YOffset

	updated, _ := m.Update(wheel(-1))
	if got := updated.(Model).viewport.YOffset; got != before {
		t.Fatalf("with reporting off nothing should scroll, offset %d → %d", before, got)
	}
}

// The bug this story is named for: bubbles binds j, k, u, d, f, b and the
// spacebar in its viewport, and the chat model used to hand it every key.
func TestTypedLetters_NeverReachTheTranscript(t *testing.T) {
	m := proseModel(t)
	before := m.viewport.YOffset

	const draft = "just find b u d f k"
	m = typeChars(t, m, draft)

	if m.viewport.YOffset != before {
		t.Fatalf("typing must not scroll the transcript, offset %d → %d", before, m.viewport.YOffset)
	}
	if m.input.Value() != draft {
		t.Fatalf("every character should have landed in the draft, got %q", m.input.Value())
	}
}

func TestPgUp_HandsTheKeyboardToTheTranscript(t *testing.T) {
	m := typeChars(t, proseModel(t), "keep this")
	before := m.viewport.YOffset

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m = updated.(Model)

	if m.state != stateFocus {
		t.Fatalf("pgup should hand the keyboard to the transcript, got state %d", m.state)
	}
	if m.viewport.YOffset >= before {
		t.Fatalf("pgup should page up, offset %d → %d", before, m.viewport.YOffset)
	}
	if m.input.Value() != "keep this" {
		t.Fatalf("the draft should survive the transfer, got %q", m.input.Value())
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.state != stateInput {
		t.Fatalf("esc should hand the keyboard back, got state %d", m.state)
	}
	if m.input.Value() != "keep this" {
		t.Fatalf("the draft should survive the return, got %q", m.input.Value())
	}
}

// Paging down with nothing below is not a transfer: the bottom of the
// transcript is where the input already stands.
func TestPgDown_AtTheBottomIsNotATransfer(t *testing.T) {
	m := proseModel(t)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if got := updated.(Model).state; got != stateInput {
		t.Fatalf("pgdn at the bottom should do nothing, got state %d", got)
	}
}

func TestUpFromAnEmptyPrompt_ReadsWhenThereIsNoHistoryToRecall(t *testing.T) {
	m := proseModel(t)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if got := updated.(Model).state; got != stateFocus {
		t.Fatalf("↑ with nothing to recall should reach the transcript, got state %d", got)
	}
}

// Where there is history, ↑ stays the history's: that convention is older
// than this surface, and pgup is the transfer.
func TestUpFromAnEmptyPrompt_KeepsTheHistoryWhereThereIsOne(t *testing.T) {
	m := proseModel(t)
	m.recordInput("the previous prompt")

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = updated.(Model)
	if m.state != stateInput {
		t.Fatalf("↑ should still recall history, got state %d", m.state)
	}
	if m.input.Value() != "the previous prompt" {
		t.Fatalf("↑ should have recalled the last prompt, got %q", m.input.Value())
	}
}

// A transcript of prose has nothing to select, and used to be refused. It is
// still worth reading.
func TestReadingMode_OpensOnATranscriptWithNothingExpandable(t *testing.T) {
	m := proseModel(t)
	updated, _ := m.Update(ctrlE())
	m = updated.(Model)

	if m.state != stateFocus {
		t.Fatalf("ctrl+e should open on prose, got state %d", m.state)
	}
	if m.focusIdx != -1 {
		t.Fatalf("there is nothing to select, so there should be no cursor, got %d", m.focusIdx)
	}
	if strings.Contains(m.renderHistory(), "❯") {
		t.Fatal("a reading surface with no cursor should draw no pointer")
	}
	hint := ansi.Strip(m.renderFocusHint())
	if !strings.Contains(hint, "nothing on this row expands") {
		t.Fatalf("[enter] should stay on the bar with its reason rather than vanishing, got %q", hint)
	}
	if strings.Contains(hint, "this row ·") {
		t.Fatalf("there is no row under the cursor, so nothing should offer row keys, got %q", hint)
	}

	// j/k are a line of scroll where they cannot be a selection.
	before := m.viewport.YOffset
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	if got := updated.(Model).viewport.YOffset; got >= before {
		t.Fatalf("k should scroll a line up, offset %d → %d", before, got)
	}
}

// An empty transcript is the one case with nothing to open onto, and it still
// says so rather than opening an empty pager.
func TestReadingMode_StillRefusesAnEmptyTranscript(t *testing.T) {
	m := readyModel(t)
	updated, _ := m.Update(ctrlE())
	m = updated.(Model)
	if m.state != stateInput {
		t.Fatalf("an empty transcript has nothing to read, got state %d", m.state)
	}
}

// The start screen advertises these keys, so pressing one on it must not
// replace the screen with a notice about how empty it is.
func TestReadingMode_LeavesTheStartScreenAlone(t *testing.T) {
	m := startModel(t, startFixture())
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m = updated.(Model)

	if m.state != stateInput {
		t.Fatalf("there is nothing to read on first contact, got state %d", m.state)
	}
	if len(m.transcript) != 0 {
		t.Fatalf("nothing should have been written to the transcript, got %d entries", len(m.transcript))
	}
	if !strings.Contains(startText(m), "Some things worth doing first") {
		t.Fatal("the start screen should have survived")
	}
}

func TestReadingMode_TypingReturnsToThePromptCarryingTheKey(t *testing.T) {
	m := focusModel(t)
	updated, _ := m.Update(ctrlE())
	m = updated.(Model)
	if m.state != stateFocus {
		t.Fatalf("expected focus mode, got state %d", m.state)
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	m = updated.(Model)
	if m.state != stateInput {
		t.Fatalf("typing should hand the keyboard back, got state %d", m.state)
	}
	if m.input.Value() != "w" {
		t.Fatalf("the keystroke should have landed in the draft, got %q", m.input.Value())
	}
}

// Focus mode's own letters stay its own — that is what the surface is for.
func TestReadingMode_KeepsItsOwnLetters(t *testing.T) {
	m := focusModel(t)
	updated, _ := m.Update(ctrlE())
	m = updated.(Model)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	m = updated.(Model)
	if m.state != stateFocus {
		t.Fatalf("k selects a row; it should not leave the surface, got state %d", m.state)
	}
	if m.input.Value() != "" {
		t.Fatalf("k should not have reached the draft, got %q", m.input.Value())
	}
}

// A row's offer keys (§16, §17a) are focus mode's only while the row under
// the cursor actually offers them. Where it does not, the letter is a letter
// again and goes back to the draft rather than being swallowed.
func TestReadingMode_AnUnclaimedOfferKeyReturnsToThePrompt(t *testing.T) {
	m := focusModel(t)
	updated, _ := m.Update(ctrlE())
	m = updated.(Model)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	m = updated.(Model)
	if m.state != stateInput {
		t.Fatalf("a tool row offers no [v], so the key should return to the prompt, got state %d", m.state)
	}
	if m.input.Value() != "v" {
		t.Fatalf("the keystroke should have landed in the draft, got %q", m.input.Value())
	}
}

// The rail under the header is the visual half of the answer to "which pane
// has the keyboard" (§7a). The word carries it, so it survives mono.
func TestReadingRail_NamesThePaneWithTheKeyboard(t *testing.T) {
	m := focusModel(t)
	if strings.Contains(m.View(), "READING") {
		t.Fatal("the rail should be a plain divider while the input has the keyboard")
	}

	updated, _ := m.Update(ctrlE())
	m = updated.(Model)
	view := m.View()
	if !strings.Contains(view, "READING") {
		t.Fatal("the rail should name the transcript once it has the keyboard")
	}
	if !strings.Contains(view, "READING 2/2") {
		t.Fatalf("the rail should report the cursor's place among the rows, got:\n%s", view)
	}
}

func TestUICommand_MouseTogglesReporting(t *testing.T) {
	m := readyModel(t)

	if _, result := m.handleSlashCommand("/ui"); !strings.Contains(result, "Mouse reporting: on") {
		t.Fatalf("bare /ui should report the mouse state, got %q", result)
	}
	if _, result := m.handleSlashCommand("/ui mouse off"); !strings.Contains(result, "click-drag selection") {
		t.Fatalf("the reply should say what turning it off buys, got %q", result)
	}
	if !m.mouseOff {
		t.Fatal("/ui mouse off should turn reporting off")
	}
	if _, result := m.handleSlashCommand("/ui mouse"); !strings.Contains(result, "Mouse reporting: off") {
		t.Fatalf("bare /ui mouse should report the state, got %q", result)
	}
	if _, result := m.handleSlashCommand("/ui mouse on"); !strings.Contains(result, "wheel scrolls") {
		t.Fatalf("turning it back on should say so, got %q", result)
	}
	if m.mouseOff {
		t.Fatal("/ui mouse on should turn reporting back on")
	}
	if _, result := m.handleSlashCommand("/ui mouse sometimes"); !strings.Contains(result, "unknown mouse setting") {
		t.Fatalf("an unknown setting should be an error, got %q", result)
	}
}

// The command has to reach the program, not just the model: turning reporting
// off has to un-tell the terminal, or the click-drag selection it was traded
// for never comes back.
func TestUICommand_MouseSendsTheTerminalACommand(t *testing.T) {
	m := readyModel(t)
	next, cmd := m.runCommand("/ui mouse off", "/ui")
	if !next.(Model).mouseOff {
		t.Fatal("running /ui mouse off should turn reporting off")
	}
	if cmd == nil {
		t.Fatal("flipping reporting should send the terminal a command")
	}
}

// The start screen is where the two panes are introduced, and its navigation
// line outlives the typing that dismisses the suggestion list.
func TestStartScreen_NavLineSurvivesTyping(t *testing.T) {
	m := startModel(t, startFixture())
	if !strings.Contains(m.renderStartScreen(100), "read the transcript") {
		t.Fatal("the start screen should name the reading keys")
	}
	m = typeChars(t, m, "x")
	view := m.renderStartScreen(100)
	if strings.Contains(view, "[↑↓] choose") {
		t.Fatal("typing should still dismiss the suggestion list")
	}
	if !strings.Contains(view, "read the transcript") {
		t.Fatal("the navigation keys still work with a draft in the box, so they should still be offered")
	}
}
