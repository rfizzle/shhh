package chat

// Terminal interactivity and prompt↔transcript focus (S-115, §7a): the wheel
// reaches the transcript, typed letters do not, and there are named ways in
// and out that a draft can never produce by accident.

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/diff"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// wheel builds a wheel notch in dir (-1 up, +1 down).
func wheel(dir int) tea.MouseMsg {
	btn := tea.MouseWheelUp
	if dir > 0 {
		btn = tea.MouseWheelDown
	}
	return tea.MouseWheelMsg{Button: btn}
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
	m.viewport.SetLines(m.renderHistoryLines())
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
	// Reporting is off by default, so the wheel has to be asked for before
	// there is a wheel event to route at all (S-136).
	m := typeChars(t, proseModel(t).WithMouse(true), "half a sentence")
	before := m.viewport.YOffset()

	updated, _ := m.Update(wheel(-1))
	m = updated.(Model)

	if m.viewport.YOffset() >= before {
		t.Fatalf("the wheel should scroll the transcript up, offset %d → %d", before, m.viewport.YOffset())
	}
	if m.input.Value() != "half a sentence" {
		t.Fatalf("the wheel must not touch the draft, got %q", m.input.Value())
	}
	if m.state != stateInput {
		t.Fatalf("the wheel reads; it should not take the keyboard, got state %d", m.state)
	}

	updated, _ = m.Update(wheel(1))
	next := updated.(Model)
	if got := next.viewport.YOffset(); got <= m.viewport.YOffset() {
		t.Fatalf("the wheel should scroll back down, offset %d → %d", m.viewport.YOffset(), got)
	}
}

// The wheel is the one gesture that reaches a full-screen viewer, since the
// transcript behind it is not what the reader is looking at (§3c).
func TestWheel_ReachesTheFullScreenDiff(t *testing.T) {
	m := diffFullModel(t).WithMouse(true)
	before := m.fullDiff.Offset

	updated, _ := m.Update(wheel(1))
	m = updated.(Model)

	if m.fullDiff.Offset <= before {
		t.Fatalf("the wheel should scroll the full-screen diff, offset %d → %d", before, m.fullDiff.Offset)
	}
	if m.viewport.YOffset() != 0 {
		t.Fatalf("the transcript behind the viewer should not have moved, got %d", m.viewport.YOffset())
	}
}

func TestWheel_IgnoredWhileMouseReportingIsOff(t *testing.T) {
	m := proseModel(t)
	m.mouseOn = false
	before := m.viewport.YOffset()

	updated, _ := m.Update(wheel(-1))
	next := updated.(Model)
	if got := next.viewport.YOffset(); got != before {
		t.Fatalf("with reporting off nothing should scroll, offset %d → %d", before, got)
	}
}

// The bug this story is named for: bubbles binds j, k, u, d, f, b and the
// spacebar in its viewport, and the chat model used to hand it every key.
func TestTypedLetters_NeverReachTheTranscript(t *testing.T) {
	m := proseModel(t)
	before := m.viewport.YOffset()

	const draft = "just find b u d f k"
	m = typeChars(t, m, draft)

	if m.viewport.YOffset() != before {
		t.Fatalf("typing must not scroll the transcript, offset %d → %d", before, m.viewport.YOffset())
	}
	if m.input.Value() != draft {
		t.Fatalf("every character should have landed in the draft, got %q", m.input.Value())
	}
}

// Paging reads the transcript and leaves the keyboard in the draft (S-140):
// the reader scrolling back to check a path mid-sentence is not asking to
// stop writing the sentence.
func TestPgUp_ScrollsWithoutTakingTheKeyboard(t *testing.T) {
	m := typeChars(t, proseModel(t), "keep this")
	before := m.viewport.YOffset()

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyPgUp})
	m = updated.(Model)

	if m.state != stateInput {
		t.Fatalf("pgup should leave the keyboard in the draft, got state %d", m.state)
	}
	if m.viewport.YOffset() >= before {
		t.Fatalf("pgup should page up, offset %d → %d", before, m.viewport.YOffset())
	}
	if m.input.Value() != "keep this" {
		t.Fatalf("the draft should be untouched, got %q", m.input.Value())
	}

	// And the sentence carries on from where it was, into the same draft.
	m = typeChars(t, m, " too")
	if m.input.Value() != "keep this too" {
		t.Fatalf("typing should continue the draft after a scroll, got %q", m.input.Value())
	}
	if m.state != stateInput {
		t.Fatalf("typing after a scroll should not have changed surface, got state %d", m.state)
	}
}

// Paging down with nothing below has nowhere to go, and no mode to leave.
func TestPgDown_AtTheBottomDoesNothing(t *testing.T) {
	m := proseModel(t)
	before := m.viewport.YOffset()

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
	m = updated.(Model)

	if m.state != stateInput {
		t.Fatalf("pgdn at the bottom should do nothing, got state %d", m.state)
	}
	if m.viewport.YOffset() != before {
		t.Fatalf("pgdn at the bottom should not move, offset %d → %d", before, m.viewport.YOffset())
	}
}

// Shift+arrows are the fine adjustment beside pgup/pgdn, and they are held to
// the same rule: a line of scroll, and the draft keeps the keyboard.
func TestShiftArrows_ScrollALineWithoutTakingTheKeyboard(t *testing.T) {
	for _, k := range []tea.KeyPressMsg{
		{Code: tea.KeyUp, Mod: tea.ModShift},
		{Code: tea.KeyUp, Mod: tea.ModCtrl},
	} {
		m := typeChars(t, proseModel(t), "mid sentence")
		before := m.viewport.YOffset()

		updated, _ := m.Update(k)
		m = updated.(Model)

		if m.state != stateInput {
			t.Fatalf("%v should leave the keyboard in the draft, got state %d", k, m.state)
		}
		if m.viewport.YOffset() != before-keyScrollLines {
			t.Fatalf("%v should scroll up one line, offset %d → %d", k, before, m.viewport.YOffset())
		}
		if m.input.Value() != "mid sentence" {
			t.Fatalf("%v must not touch the draft, got %q", k, m.input.Value())
		}
	}

	// And back down again.
	m := proseModel(t)
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModShift})
	m = updated.(Model)
	up := m.viewport.YOffset()
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModShift})
	next := updated.(Model)
	if got := next.viewport.YOffset(); got != up+keyScrollLines {
		t.Fatalf("shift+↓ should scroll back down, offset %d → %d", up, got)
	}
}

// ↑ used to hand the keyboard over on an empty draft with no history to
// recall. A key that changes surface depending on how much history a session
// happens to have is one nobody can learn — and on a terminal that
// synthesises arrows for the wheel, it was a flick of the wheel that opened
// reading mode (S-140, altscroll.go).
func TestUpFromAnEmptyPrompt_NeverTakesTheKeyboard(t *testing.T) {
	m := proseModel(t)
	before := m.viewport.YOffset()

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	m = updated.(Model)

	if m.state != stateInput {
		t.Fatalf("↑ should stay the input's key, got state %d", m.state)
	}
	if m.viewport.YOffset() != before {
		t.Fatalf("↑ should not scroll the transcript either, offset %d → %d", before, m.viewport.YOffset())
	}
}

// Scrolling away pauses the follow, and the notice rail is the only thing on
// screen that can say so now the draft keeps the keyboard (S-140).
func TestFollowNotice_CountsWhatIsBelowAndClearsAtTheEnd(t *testing.T) {
	m := proseModel(t)
	if note := m.followNotice(); note != "" {
		t.Fatalf("at the live end there is nothing to say, got %q", note)
	}

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyPgUp})
	m = updated.(Model)

	note := m.followNotice()
	if note == "" {
		t.Fatal("scrolled off the end, the rail should say so")
	}
	if !strings.Contains(note, "below") || !strings.Contains(note, "pgdn") {
		t.Fatalf("the notice should count what is below and name the way back, got %q", note)
	}
	if !strings.Contains(ansi.Strip(m.renderPromptFrame()), "below") {
		t.Fatal("the notice should reach the frame's notice rail")
	}

	// Walking back to the end retires it.
	for i := 0; i < 10 && !m.viewport.AtBottom(); i++ {
		updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
		m = updated.(Model)
	}
	if note := m.followNotice(); note != "" {
		t.Fatalf("back at the live end the notice should be gone, got %q", note)
	}
}

// Reading mode has its own labelled rail and position, so the follow notice
// stays out of its way (§7a).
func TestFollowNotice_SilentInReadingMode(t *testing.T) {
	m := proseModel(t)
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyPgUp})
	m = updated.(Model)
	if m.followNotice() == "" {
		t.Fatal("precondition: the notice should be showing")
	}

	updated, _ = m.Update(ctrlE())
	m = updated.(Model)
	if m.state != stateFocus {
		t.Fatalf("precondition: ctrl+e should open reading mode, got state %d", m.state)
	}
	if note := m.followNotice(); note != "" {
		t.Fatalf("reading mode names the keyboard itself, got %q", note)
	}
}

// Where there is history, ↑ stays the history's: that convention is older
// than this surface, and pgup is the transfer.
func TestUpFromAnEmptyPrompt_KeepsTheHistoryWhereThereIsOne(t *testing.T) {
	m := proseModel(t)
	m.recordInput("the previous prompt")

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
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
	before := m.viewport.YOffset()
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	next := updated.(Model)
	if got := next.viewport.YOffset(); got >= before {
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
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyPgUp})
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

	updated, _ = m.Update(tea.KeyPressMsg{Code: 'w', Text: "w"})
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

	updated, _ = m.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
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

	updated, _ = m.Update(tea.KeyPressMsg{Code: 'v', Text: "v"})
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
	if strings.Contains(m.View().Content, "READING") {
		t.Fatal("the rail should be a plain divider while the input has the keyboard")
	}

	updated, _ := m.Update(ctrlE())
	m = updated.(Model)
	view := m.View().Content
	if !strings.Contains(view, "READING") {
		t.Fatal("the rail should name the transcript once it has the keyboard")
	}
	if !strings.Contains(view, "READING 2/2") {
		t.Fatalf("the rail should report the cursor's place among the rows, got:\n%s", view)
	}
}

func TestUICommand_MouseTogglesReporting(t *testing.T) {
	m := readyModel(t)

	// Off is the default now, so the states below are the other way round.
	if _, result := m.handleSlashCommand("/ui"); !strings.Contains(result, "Mouse reporting: off") {
		t.Fatalf("bare /ui should report the mouse state, got %q", result)
	}
	if _, result := m.handleSlashCommand("/ui mouse on"); !strings.Contains(result, "wheel scrolls") {
		t.Fatalf("the reply should say what turning it on buys, got %q", result)
	}
	if !m.mouseOn {
		t.Fatal("/ui mouse on should turn reporting on")
	}
	if _, result := m.handleSlashCommand("/ui mouse"); !strings.Contains(result, "Mouse reporting: on") {
		t.Fatalf("bare /ui mouse should report the state, got %q", result)
	}
	if _, result := m.handleSlashCommand("/ui mouse off"); !strings.Contains(result, "click-drag selection") {
		t.Fatalf("turning it back off should say so, got %q", result)
	}
	if m.mouseOn {
		t.Fatal("/ui mouse off should turn reporting back off")
	}
	if _, result := m.handleSlashCommand("/ui mouse sometimes"); !strings.Contains(result, "unknown mouse setting") {
		t.Fatalf("an unknown setting should be an error, got %q", result)
	}
}

// The setting has to reach the terminal, not just the model: turning
// reporting off has to un-tell the terminal, or the click-drag selection it
// was traded for never comes back. Since S-155 the frame says so — the mouse
// mode is a field on the View — so the check is that the next frame asks for
// what the model believes.
func TestUICommand_MouseSendsTheTerminalACommand(t *testing.T) {
	m := readyModel(t)
	next, _ := m.runCommand("/ui mouse on", "/ui")
	on := next.(Model)
	if !on.mouseOn {
		t.Fatal("running /ui mouse on should turn reporting on")
	}
	if on.View().MouseMode != tea.MouseModeCellMotion {
		t.Fatal("flipping reporting should ask the terminal for it in the frame")
	}
	off, _ := on.runCommand("/ui mouse off", "/ui")
	if off.(Model).View().MouseMode != tea.MouseModeNone {
		t.Fatal("turning it off should stop asking, or the terminal keeps tracking")
	}
}

// The start screen is where the two panes are introduced, and its navigation
// line outlives the typing that dismisses the suggestion list.
func TestStartScreen_NavLineSurvivesTyping(t *testing.T) {
	m := startModel(t, startFixture())
	if !strings.Contains(m.renderStartScreen(100), "scroll") {
		t.Fatal("the start screen should name the scrolling keys")
	}
	m = typeChars(t, m, "x")
	view := m.renderStartScreen(100)
	if strings.Contains(view, "[↑↓] choose") {
		t.Fatal("typing should still dismiss the suggestion list")
	}
	if !strings.Contains(view, "scroll") {
		t.Fatal("the navigation keys still work with a draft in the box, so they should still be offered")
	}
	// Scrolling and the handover are two things now, and the line says so
	// rather than describing them as one (S-140).
	if !strings.Contains(view, "[ctrl+e] select rows") {
		t.Fatal("the line should name ctrl+e as the handover, apart from the scroll keys")
	}
}

// Reporting is off out of the box, because the thing it costs — the
// terminal's own click-drag selection — has no substitute here, while the
// wheel does (S-136, §7a).
func TestMouse_OffByDefaultAndAskedForByChord(t *testing.T) {
	var wrote [][2]string
	m := readyModel(t).WithConfigWriter(func(k, v string) error {
		wrote = append(wrote, [2]string{k, v})
		return nil
	})
	if m.mouseOn {
		t.Fatal("a session starts with reporting off")
	}

	if m.View().MouseMode != tea.MouseModeNone {
		t.Fatal("a session that never asked for reporting must not ask for it")
	}

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})
	on := updated.(Model)
	if !on.mouseOn {
		t.Fatal("ctrl+x should turn reporting on")
	}
	if on.View().MouseMode != tea.MouseModeCellMotion {
		t.Fatal("the terminal has to be told, and the frame is what tells it")
	}
	// The answer outlives the process that gave it, which is the whole
	// difference between this and the old session-only /ui mouse.
	if len(wrote) != 1 || wrote[0] != [2]string{"appearance.mouse", "true"} {
		t.Fatalf("persisted %v, want appearance.mouse=true", wrote)
	}

	updated, _ = on.Update(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})
	if updated.(Model).mouseOn {
		t.Fatal("ctrl+x again should turn it back off")
	}
	if len(wrote) != 2 || wrote[1] != [2]string{"appearance.mouse", "false"} {
		t.Fatalf("persisted %v, want appearance.mouse=false second", wrote)
	}
}

// The chord is answered above the surfaces, not inside one: wanting to copy
// something arrives just as often while reading the transcript as while
// typing, and a key that only worked in the draft would miss the moment.
func TestMouse_ChordWorksFromEverySurface(t *testing.T) {
	for _, tc := range []struct {
		name string
		open func(*testing.T) Model
	}{
		{"reading the transcript", func(t *testing.T) Model { return focusModel(t) }},
		{"the full-screen diff", func(t *testing.T) Model { return diffFullModel(t) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.open(t)
			state := m.state
			updated, _ := m.Update(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})
			next := updated.(Model)
			if !next.mouseOn || next.View().MouseMode != tea.MouseModeCellMotion {
				t.Fatalf("ctrl+x should flip reporting here too, on=%v", next.mouseOn)
			}
			if next.state != state {
				t.Errorf("the chord is a setting, not a way out: state %v → %v", state, next.state)
			}
		})
	}
}

// A session with nowhere to write still flips — the setting is real either
// way — and says only the part it could not do.
func TestMouse_WithoutAWriterSaysSo(t *testing.T) {
	m := readyModel(t)
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})
	next := updated.(Model)
	if !next.mouseOn {
		t.Fatal("the flip is not conditional on being able to save it")
	}
	last := next.transcript[len(next.transcript)-1]
	if !strings.Contains(last.text, "this session only") {
		t.Fatalf("an unsaved flip should say so, got %q", last.text)
	}
}
