package chat

// Step detail: /step opens one step's rows' bodies from the draft, and
// leaves every other step where it was.

import (
	"strings"
	"testing"
	"time"
)

// detailModel is the verbosity fixture with a second step after it, so every
// test here can check that the command opened one step and not the transcript.
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

// firstStep and lastStep are the fixture's two steps, read the way the command
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

func runStep(t *testing.T, m Model) Model {
	t.Helper()
	updated, _ := m.runCommand("/step", "/step")
	next, ok := updated.(Model)
	if !ok {
		t.Fatalf("/step returned %T, want Model", updated)
	}
	return next
}

func TestStepDetail_DraftOpensTheStepInFlight(t *testing.T) {
	m := detailModel(t)
	if m.stepDetailOpen(lastStep(t, m), m.transcript) {
		t.Fatal("a step starts with its detail closed")
	}

	m = runStep(t, m)

	if !m.stepDetailOpen(lastStep(t, m), m.transcript) {
		t.Error("/step from the draft did not open the last step's detail")
	}
	if m.stepDetailOpen(firstStep(t, m), m.transcript) {
		t.Error("/step opened a step the command was not pointed at")
	}
	view := stripANSI(m.renderHistory())
	if !strings.Contains(view, "suite line two") {
		t.Errorf("the opened step is not showing its rows' bodies:\n%s", view)
	}

	m = runStep(t, m)
	if m.stepDetailOpen(lastStep(t, m), m.transcript) {
		t.Error("a second /step did not close the detail again")
	}
	if strings.Contains(stripANSI(m.renderHistory()), "suite line two") {
		t.Error("the closed step is still showing a body")
	}
}

func TestStepDetail_CommandDoesNotTakeTheKeyboard(t *testing.T) {
	m := runStep(t, detailModel(t))

	if m.state == stateFocus {
		t.Error("/step took the keyboard into reading mode")
	}
	if !m.stepDetailOpen(lastStep(t, m), m.transcript) {
		t.Error("/step did nothing")
	}
}

func TestStepDetail_OpeningUnfoldsTheStepAndClosingLeavesItOpen(t *testing.T) {
	m := detailModel(t)
	g := firstStep(t, m)
	// A finished step collapses to its header, which is exactly the
	// step a reader reaches for: opening the detail of rows nobody can see
	// would be an answer that reports success and shows nothing.
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
		t.Errorf("the group row survived the command that asked what the step did:\n%s", view)
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

func TestStepDetail_ARowThatLandsAfterTheCommandArrivesOpen(t *testing.T) {
	m := detailModel(t)
	m = runStep(t, m)

	// A step in flight is a step still growing: the answer is resolved at
	// render time from the entry that titles the step, never stamped onto
	// the rows that happened to exist when the command was run.
	m.appendEntry(entry{kind: entryTool, toolName: "read_file",
		toolArgs: `{"path":"internal/agent/late.go"}`, toolResult: "landed after the chord",
		duration: 200 * time.Millisecond})
	m.invalidateRenderCache()

	if !strings.Contains(stripANSI(m.renderHistory()), "landed after the chord") {
		t.Error("a call that landed after /step arrived collapsed")
	}
}

func TestStepDetail_NoStepSaysSoOnceAndThenStaysQuiet(t *testing.T) {
	m := proseModel(t)
	if _, ok := m.draftStep(m.transcript); ok {
		t.Fatal("the prose fixture grew a step")
	}

	m = runStep(t, m)
	if !strings.Contains(stripANSI(m.renderHistory()), noStepDetailNotice) {
		t.Error("the command refused without saying why")
	}
	n := len(m.transcript)

	m = runStep(t, m)
	if len(m.transcript) != n {
		t.Error("the refusal repeated; a notice on every keypress teaches a reader to stop reading them")
	}
}
