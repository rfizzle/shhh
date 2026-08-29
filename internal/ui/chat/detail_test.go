package chat

// Step detail: ctrl+o opens one step's rows' bodies, from the
// draft and from under reading mode's cursor, and leaves every other step
// where it was.

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func ctrlO() tea.KeyPressMsg { return tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl} }

// detailModel is the verbosity fixture with a second step after it, so every
// test here can check that the chord opened one step and not the transcript.
// Step 1 is the six reads and two searches; step 2 is an edit and a broken
// command.
func detailModel(t *testing.T) Model {
	t.Helper()
	m := foldModel(t)
	m.appendEntry(entry{kind: entryAssistant, text: "Re-run the agent suite"})
	m.appendEntry(entry{kind: entryTool, toolName: "read_file",
		toolArgs: `{"path":"internal/agent/suite.go"}`, toolResult: "suite line one\nsuite line two",
		duration: 300 * time.Millisecond})
	m.invalidateRenderCache()
	m.viewport.SetLines(m.renderHistoryLines())
	m.atBottom = true
	return m
}

// firstStep and lastStep are the fixture's two steps, read the way the chord
// reads them rather than by index arithmetic.
func firstStep(t *testing.T, m Model) *stepGroup {
	t.Helper()
	blocks := m.blocksOf(m.transcript)
	for _, blk := range blocks {
		if blk.step != nil && !blk.step.queued() {
			return blk.step
		}
	}
	t.Fatal("fixture has no step")
	return nil
}

func lastStep(t *testing.T, m Model) *stepGroup {
	t.Helper()
	g, ok := m.draftStep(m.transcript)
	if !ok {
		t.Fatal("fixture has no step with rows")
	}
	return g
}

func pressCtrlO(t *testing.T, m Model) Model {
	t.Helper()
	updated, _ := m.Update(ctrlO())
	next, ok := updated.(Model)
	if !ok {
		t.Fatalf("ctrl+o returned %T, want Model", updated)
	}
	return next
}

func TestStepDetail_DraftOpensTheStepInFlight(t *testing.T) {
	m := detailModel(t)
	if m.stepDetailOpen(lastStep(t, m), m.transcript) {
		t.Fatal("a step starts with its detail closed")
	}

	m = pressCtrlO(t, m)

	if !m.stepDetailOpen(lastStep(t, m), m.transcript) {
		t.Error("ctrl+o from the draft did not open the last step's detail")
	}
	if m.stepDetailOpen(firstStep(t, m), m.transcript) {
		t.Error("ctrl+o opened a step the chord was not pointed at")
	}
	view := stripANSI(m.renderHistory())
	if !strings.Contains(view, "suite line two") {
		t.Errorf("the opened step is not showing its rows' bodies:\n%s", view)
	}

	m = pressCtrlO(t, m)
	if m.stepDetailOpen(lastStep(t, m), m.transcript) {
		t.Error("a second ctrl+o did not close the detail again")
	}
	if strings.Contains(stripANSI(m.renderHistory()), "suite line two") {
		t.Error("the closed step is still showing a body")
	}
}

func TestStepDetail_DraftKeepsTheKeyboard(t *testing.T) {
	m := typeChars(t, detailModel(t), "half a sentence")

	m = pressCtrlO(t, m)

	if m.state == stateFocus {
		t.Error("ctrl+o took the keyboard into reading mode")
	}
	if got := m.input.Value(); got != "half a sentence" {
		t.Errorf("the draft did not survive the chord: %q", got)
	}
	if !m.stepDetailOpen(lastStep(t, m), m.transcript) {
		t.Error("the chord did nothing while a draft was live")
	}
}

func TestStepDetail_OpeningUnfoldsTheStepAndClosingLeavesItOpen(t *testing.T) {
	m := detailModel(t)
	g := firstStep(t, m)
	// A finished step collapses to its header, which is exactly the
	// step a reader reaches for: opening the detail of rows nobody can see
	// would be a chord that reports success and shows nothing.
	m.transcript[g.titleIdx].stepFold = foldClosed

	m.toggleStepDetail(g)

	blk, ok := m.stepBlockAt(m.transcript, g.titleIdx)
	if !ok {
		t.Fatal("step 1 went missing")
	}
	if m.headerFor(blk, m.transcript).Folded {
		t.Error("opening the detail left the step folded")
	}

	m.toggleStepDetail(g)
	if m.headerFor(blk, m.transcript).Folded {
		t.Error("closing the detail folded the step back up; the reader unfolded it")
	}
}

func TestStepDetail_OpenedStepGivesItsGroupRowBack(t *testing.T) {
	m := detailModel(t)
	g := firstStep(t, m)
	if slots := m.stepSlots(m.transcript, g); !slots[0].group {
		t.Fatal("the fixture's read-only run is not folded to begin with")
	}

	m.toggleStepDetail(g)
	m.invalidateRenderCache()

	for _, sl := range m.stepSlots(m.transcript, g) {
		if sl.group {
			t.Error("an opened step is still swallowing rows into a counted group row")
		}
	}
	view := stripANSI(m.renderHistory())
	if strings.Contains(view, "6 reads · 2 searches") {
		t.Errorf("the group row survived the chord that asked what the step did:\n%s", view)
	}
	if !strings.Contains(view, "internal/agent/session.go") {
		t.Errorf("the rows the group had swallowed did not come back:\n%s", view)
	}
}

func TestStepDetail_BodiesAreBoundedAndAnOpenedRowIsNot(t *testing.T) {
	m := detailModel(t)
	long := make([]string, 0, maxToolResultLines*3)
	for i := 0; i < maxToolResultLines*3; i++ {
		long = append(long, "output line "+string(rune('a'+i)))
	}
	m.transcript = append(m.transcript,
		entry{kind: entryAssistant, text: "Read the long one"},
		entry{kind: entryTool, toolName: "read_file", toolArgs: `{"path":"long.go"}`,
			toolResult: strings.Join(long, "\n"), duration: time.Second})
	m.invalidateRenderCache()
	g := lastStep(t, m)
	row := g.end - 1

	m.toggleStepDetail(g)
	m.invalidateRenderCache()
	bounded := stripANSI(m.renderHistory())
	if strings.Contains(bounded, long[len(long)-1]) {
		t.Error("a step's detail is unbounded; a nine-call step would push its own header off the screen")
	}
	if !strings.Contains(bounded, long[0]) {
		t.Error("a step's detail showed no body at all")
	}

	// The step's answer is the default for its rows, never a ceiling on the
	// one row a reader asked about by name.
	m.transcript[row].expanded = true
	m.invalidateRenderCache()
	if !strings.Contains(stripANSI(m.renderHistory()), long[len(long)-1]) {
		t.Error("a row opened by hand lost its unbounded body inside an opened step")
	}
}

func TestStepDetail_ReadingModeOpensTheStepTheCursorIsIn(t *testing.T) {
	cases := []struct {
		name string
		on   func(m Model, g *stepGroup) int
	}{
		{"header", func(m Model, g *stepGroup) int { return g.titleIdx }},
		{"a row inside it", func(m Model, g *stepGroup) int { return g.end - 1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := detailModel(t)
			updated, _ := m.Update(ctrlE())
			m = updated.(Model)
			g := firstStep(t, m)
			m.focusIdx = tc.on(m, g)

			m = pressCtrlO(t, m)

			if m.state != stateFocus {
				t.Error("the chord dropped reading mode")
			}
			if !m.stepDetailOpen(firstStep(t, m), m.transcript) {
				t.Error("ctrl+o did not open the step the cursor was standing in")
			}
			if m.stepDetailOpen(lastStep(t, m), m.transcript) {
				t.Error("ctrl+o opened a step the cursor was nowhere near")
			}
		})
	}
}

func TestStepDetail_CursorOutsideAStepSaysSoRatherThanFailingSilently(t *testing.T) {
	m := detailModel(t)
	updated, _ := m.Update(ctrlE())
	m = updated.(Model)
	// The user row at the top of the fixture belongs to no step.
	m.focusIdx = 0

	seg := m.detailKeySeg()
	if seg.reason == "" {
		t.Error("the bar offered [ctrl+o] with nothing to say about why it cannot act")
	}
	line := stripANSI(m.readingKeyLine(130))
	if !strings.Contains(line, "not in a step") {
		t.Errorf("the hint bar did not name the reason:\n%s", line)
	}

	before := stripANSI(m.renderHistory())
	m = pressCtrlO(t, m)
	if stripANSI(m.renderHistory()) != before {
		t.Error("ctrl+o outside a step changed the transcript")
	}
	if m.state != stateFocus {
		t.Error("ctrl+o outside a step left reading mode")
	}
}

func TestStepDetail_HintBarNamesTheChordAndDropsItFirst(t *testing.T) {
	m := detailModel(t)
	updated, _ := m.Update(ctrlE())
	m = updated.(Model)
	m.focusIdx = firstStep(t, m).titleIdx

	wide := stripANSI(m.readingKeyLine(130))
	if !strings.Contains(wide, "[ctrl+o]") {
		t.Errorf("a chord with no mnemonic is not written down anywhere:\n%s", wide)
	}
	if !strings.Contains(wide, "back to the prompt") {
		t.Errorf("the wide bar gave up words it had room for:\n%s", wide)
	}

	m = pressCtrlO(t, m)
	open := stripANSI(m.readingKeyLine(130))
	if !strings.Contains(open, "close the detail") {
		t.Errorf("an open step does not offer the key that closes it:\n%s", open)
	}

	// [ctrl+o] is the first offer to go, and it goes before any key gives up
	// its words: it is the only key on the bar with a home outside this mode.
	// Stated over every width rather than at one, so the order holds wherever
	// the line actually breaks.
	dropped := false
	for w := 130; w >= 40; w-- {
		line := stripANSI(m.readingKeyLine(w))
		has := strings.Contains(line, "[ctrl+o]")
		if !has {
			dropped = true
		}
		if has && !strings.Contains(line, "back to the prompt") {
			t.Errorf("at %d columns a key gave up its words while [ctrl+o] stayed:\n%s", w, line)
		}
		if has && !strings.Contains(line, "[enter]") {
			t.Errorf("at %d columns [enter] went before [ctrl+o]:\n%s", w, line)
		}
	}
	if !dropped {
		t.Error("[ctrl+o] survived every width; it is meant to be the first offer to go")
	}
}

func TestStepDetail_HeaderMarksYourAnswerAndNotTheSetting(t *testing.T) {
	m := detailModel(t)
	g := firstStep(t, m)
	blk, ok := m.stepBlockAt(m.transcript, g.titleIdx)
	if !ok {
		t.Fatal("step 1 went missing")
	}

	if m.headerFor(blk, m.transcript).Detail {
		t.Error("a step nobody opened is marked as opened")
	}
	m.toggleStepDetail(g)
	h := m.headerFor(blk, m.transcript)
	if !h.Detail {
		t.Error("the step you opened is not marked")
	}
	if !strings.Contains(h.countLabel(), "detail") {
		t.Errorf("the header does not say in words what is open: %q", h.countLabel())
	}

	// High verbosity opens every step, and a word repeated on every header
	// says nothing about any of them. It is asked of a step nobody has
	// answered for: an explicit answer outranks the setting, as stepFold's
	// and groupFold's do.
	m.verbosity = verbosityHigh
	untouched := lastStep(t, m)
	ublk, ok := m.stepBlockAt(m.transcript, untouched.titleIdx)
	if !ok {
		t.Fatal("the last step went missing")
	}
	if !m.stepDetailOpen(untouched, m.transcript) {
		t.Error("high verbosity is not opening a step's detail")
	}
	if m.headerFor(ublk, m.transcript).Detail {
		t.Error("the setting's own answer is being reported as yours on every header")
	}
}

func TestStepDetail_ARowThatLandsAfterTheChordArrivesOpen(t *testing.T) {
	m := detailModel(t)
	m = pressCtrlO(t, m)

	// A step in flight is a step still growing: the answer is resolved at
	// render time from the entry that titles the step, never stamped onto
	// the rows that happened to exist when the chord was pressed.
	m.appendEntry(entry{kind: entryTool, toolName: "read_file",
		toolArgs: `{"path":"internal/agent/late.go"}`, toolResult: "landed after the chord",
		duration: 200 * time.Millisecond})
	m.invalidateRenderCache()

	if !strings.Contains(stripANSI(m.renderHistory()), "landed after the chord") {
		t.Error("a call that landed after ctrl+o arrived collapsed")
	}
}

func TestStepDetail_NoStepSaysSoOnceAndThenStaysQuiet(t *testing.T) {
	m := proseModel(t)
	if _, ok := m.draftStep(m.transcript); ok {
		t.Fatal("the prose fixture grew a step")
	}

	m = pressCtrlO(t, m)
	if !strings.Contains(stripANSI(m.renderHistory()), noStepDetailNotice) {
		t.Error("the chord refused without saying why")
	}
	n := len(m.transcript)

	m = pressCtrlO(t, m)
	if len(m.transcript) != n {
		t.Error("the refusal repeated; a notice on every keypress teaches a reader to stop reading them")
	}
}
