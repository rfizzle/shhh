package chat

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// statusModel is a model mid-turn with usage and pricing behind it, so every
// field of the status line has something to state.
func statusModel(t *testing.T) Model {
	t.Helper()
	m := frameModel(t, 130, 40)
	m.turnCount = 1
	m.turnStarted = time.Now()
	m.state = stateStreaming
	return m
}

// settleCounts runs the counters to their targets, so a test can assert the
// figures the session measured rather than whichever frame of the climb it
// happened to stop on.
func settleCounts(m *Model) {
	for range 20 {
		m.easeCounts()
		if !m.countsEasing() {
			return
		}
		m.spinFrame++
	}
}

func TestTurnStatus_PhaseFollowsWhatTheTurnIsDoing(t *testing.T) {
	m := statusModel(t)

	// Nothing has arrived yet: the model is reasoning before it acts.
	if p, ok := m.turnPhase(); !ok || p != components.PhaseThinking {
		t.Fatalf("a silent stream = phase %d ok=%v, want thinking", p, ok)
	}

	m.streaming = "here is what I found"
	if p, ok := m.turnPhase(); !ok || p != components.PhaseStreaming {
		t.Fatalf("prose arriving = phase %d ok=%v, want streaming", p, ok)
	}

	m.state = stateClassifying
	if p, ok := m.turnPhase(); !ok || p != components.PhaseDeciding {
		t.Fatalf("the classifier = phase %d ok=%v, want deciding", p, ok)
	}

	m.state = stateRunningCmd
	m.runningCommand = "go test ./internal/agent/...\nsecond line"
	if p, ok := m.turnPhase(); !ok || p != components.PhaseActing {
		t.Fatalf("a running command = phase %d ok=%v, want acting", p, ok)
	}

	// An idle session is in none of the four.
	m.state = stateInput
	if _, ok := m.turnPhase(); ok {
		t.Fatal("an idle session should report no phase")
	}
}

// The command runs in the feed and nowhere else. The rail states the phase
// the turn is in; the row under the transcript states what is running, whole,
// with the clock that belongs to it — so the long command that used to be cut
// in half on the rail is readable in the one place that has the width for it
// (docs/interface/surfaces.md#the-input-frame).
func TestTurnStatus_TheRailLeavesTheCommandToTheFeed(t *testing.T) {
	const command = "go test ./internal/agent/... ./internal/ui/chat/... -run TestRoundLimit -count=1"
	m := statusModel(t)
	// Past the label's entrance, so the rail's word is the settled one
	// rather than the cells that have arrived of it so far.
	m.turnStarted = time.Now().Add(-2 * time.Minute)
	m.state = stateRunningCmd
	m.runningCommand = command
	m.runStart = time.Now().Add(-42 * time.Second)
	m.runTail = &commandTail{}
	m.runTail.Set("ok  	github.com/rfizzle/shhh/internal/agent	0.412s")

	s, ok := m.turnStatus()
	if !ok {
		t.Fatal("a command in flight should have a live line")
	}
	rail := stripANSI(s.View(120))
	if strings.Contains(rail, "go test") {
		t.Fatalf("the rail repeated the command: %q", rail)
	}
	if !strings.Contains(rail, "acting…") {
		t.Fatalf("the rail should still say what the turn is doing: %q", rail)
	}

	row := stripANSI(m.runningCommandRow(120))
	if !strings.Contains(row, command) {
		t.Fatalf("the feed's row should carry the command whole:\n%s", row)
	}
	if !strings.Contains(row, "0.412s") {
		t.Fatalf("the feed's row should carry the command's live tail:\n%s", row)
	}

	// Two words as well as two clocks: `running…` is the outcome of the one
	// call the row is about, so the rail states the turn's phase in a word of
	// its own rather than standing a few rows from itself with a second
	// subject (docs/interface/surfaces.md#the-input-frame).
	if !strings.Contains(row, components.OutcomeRunning) {
		t.Fatalf("the feed's row should state the call's own outcome:\n%s", row)
	}
	if strings.Contains(rail, "running") {
		t.Fatalf("the rail should not carry the row's outcome word: %q", rail)
	}

	// Two clocks, and only one of them says `turn`: the row's is the
	// command's own, in the duration column every row states its own span
	// in, and the rail's is the whole turn's.
	if !strings.Contains(rail, "turn ") {
		t.Fatalf("the rail's clock should name the span it measures: %q", rail)
	}
	if strings.Contains(row, "turn ") {
		t.Fatalf("the command's row should not state the turn's clock:\n%s", row)
	}
}

// The turn's account is not on the status line, but it is what the vitals
// rail's counters are aimed at, and it moves while the prose does.
func TestTurnStatus_TokensMoveWhileTheProseArrives(t *testing.T) {
	m := statusModel(t)
	beforeIn, beforeOut := m.liveTurnTokens()

	m.streaming = strings.Repeat("token ", 400)
	in, out := m.liveTurnTokens()
	if out == beforeOut {
		t.Fatalf("output tokens did not move as prose arrived (%d)", out)
	}
	if in != beforeIn {
		t.Fatalf("input tokens should not move while output arrives (%d -> %d)", beforeIn, in)
	}
}

// The thinking half of the same account: reasoning is billed as output, and
// on a model that reasons before it answers it is most of what the opening of
// a round produces — the seconds the rail used to report as a phase with no
// numbers under it.
func TestTurnStatus_TokensMoveWhileTheReasoningArrives(t *testing.T) {
	m := statusModel(t)
	m.events = make(chan provider.StreamEvent)
	_, before := m.liveTurnTokens()

	m.appendThinking(strings.Repeat("weighing it up ", 200))
	if _, out := m.liveTurnTokens(); out == before {
		t.Fatalf("output tokens did not move as the reasoning arrived (%d)", out)
	}

	// The row stays on screen for the rest of the turn, but the usage event
	// that closed its round has already counted those tokens: the estimate
	// stops with the round rather than being added to what it was billed.
	m.events = nil
	if _, out := m.liveTurnTokens(); out != before {
		t.Fatalf("the estimate should stop when the round does: %d -> %d", before, out)
	}
}

// Until the turn's first request reports, there is no billed prompt to
// count — and zero would be a number the session knows is wrong.
func TestTurnStatus_PromptEstimatedUntilTheFirstUsageLands(t *testing.T) {
	m := statusModel(t)
	m.vitals.startTurn() // a fresh turn: nothing billed yet
	if in, _ := m.liveTurnTokens(); in == 0 {
		t.Fatal("an unbilled prompt should count the context estimate, got 0")
	}

	m.accumulateUsage(&provider.Usage{PromptTokens: 41200, CompletionTokens: 100})
	if in, _ := m.liveTurnTokens(); in != 41200 {
		t.Fatalf("a reported prompt replaces the estimate, got %d", in)
	}
}

// The running line states no account at all. A turn in flight can only be
// priced at the fresh input rate, which charges every cached prompt read as
// if it were new; the figure that produces is the newest one on the frame and
// the only wrong one, beside the billed totals the rails below carry. The
// tokens go with it — the vitals rail already states the pair
// (docs/interface/surfaces.md#the-input-frame).
func TestTurnStatus_TheRunningLineStatesNoAccount(t *testing.T) {
	m := statusModel(t)
	m.TotalTokensIn, m.TotalTokensOut = 5000, 2000
	m.streaming = strings.Repeat("token ", 400)
	settleCounts(&m)

	s, ok := m.turnStatus()
	if !ok || s.Done {
		t.Fatalf("a running turn should have a live line (ok=%v done=%v)", ok, s.Done)
	}
	if line := stripANSI(s.View(160)); strings.ContainsAny(line, "$↑↓") {
		t.Fatalf("the running line stated an account: %q", line)
	}

	// What it leaves out is not missing from the frame: the rail below
	// carries the session's pair, this turn's estimate inside it.
	if bar := stripANSI(m.renderStatusBar(160)); !strings.Contains(bar, "↑") {
		t.Fatalf("the vitals rail should still carry the counts:\n%s", bar)
	}
}

func TestTurnStatus_ResolvesFromTheTurnsOwnCloseBlock(t *testing.T) {
	m := statusModel(t)
	m.state = stateInput
	if _, ok := m.turnStatus(); ok {
		t.Fatal("a turn that closed without a summary should resolve into nothing")
	}

	close := &components.TurnClose{State: components.TurnDone, Tools: 18, Elapsed: "1m 04s", Spend: "$0.14"}
	m.transcript = append(m.transcript, entry{kind: entryTurnClose, turn: 1, close: close})
	s, ok := m.turnStatus()
	if !ok || !s.Done {
		t.Fatalf("a closed turn should resolve into its summary (ok=%v done=%v)", ok, s.Done)
	}
	// The outcome agrees because it is the same outcome.
	if s.Outcome != close.State {
		t.Fatalf("the resolved line disagrees with the close row: %+v", s)
	}
	// And the account stays on the close row: the span, the tools and the
	// bill, which the row is still carrying when the turn has scrolled away
	// from a line that reports only the last one
	// (docs/interface/surfaces.md#the-input-frame).
	if line := stripANSI(s.View(200)); line != "✓ done" {
		t.Fatalf("the resolved line restated the close row's account: %q", line)
	}

	// A newer turn with no close of its own does not inherit the old one.
	m.turnCount = 2
	if _, ok := m.turnStatus(); ok {
		t.Fatal("a new turn should not resolve into the previous turn's summary")
	}
}

func TestTurnStatus_FrameRailShowsTheTurnAndThenItsSummary(t *testing.T) {
	m := statusModel(t)
	m.runTail = nil
	// Past the label's entrance: what the rail says while a turn runs
	// is the settled word, and how it gets there is the test below.
	m.turnStarted = time.Now().Add(-2 * time.Second)
	view := stripANSI(m.View().Content)
	if !strings.Contains(view, "thinking…") {
		t.Fatalf("the top rail should carry the live status:\n%s", view)
	}

	m.state = stateInput
	m.transcript = append(m.transcript, entry{kind: entryTurnClose, turn: 1,
		close: &components.TurnClose{State: components.TurnDone, Tools: 18, Elapsed: "1m 04s", Spend: "$0.14"}})
	view = stripANSI(m.View().Content)
	if !strings.Contains(view, "╭─ ✓ done ─") {
		t.Fatalf("the top rail should resolve into the turn's outcome and nothing after it:\n%s", view)
	}
	if strings.Contains(view, "thinking…") {
		t.Fatalf("the live line should be finished, not still running:\n%s", view)
	}
}

// The label materialises over the turn's first second rather than appearing
// . The entrance is measured off the turn's own age — the number
// the line already prints beside the word — so a turn that has just started
// is mid-arrival and one a second old is not, without a second clock.
func TestTurnStatus_TheLabelArrivesWithTheTurn(t *testing.T) {
	m := statusModel(t)
	m.runTail = nil
	if view := stripANSI(m.View().Content); !strings.Contains(view, "·") || strings.Contains(view, "thinking…") {
		t.Fatalf("a turn that just started should still be spelling its label out:\n%s", view)
	}
	m.turnStarted = time.Now().Add(-2 * time.Second)
	if view := stripANSI(m.View().Content); !strings.Contains(view, "thinking…") {
		t.Fatalf("a second in, the label should have arrived:\n%s", view)
	}
	// The width the slot needs never changes while the word fills in: a cell
	// that has not arrived is a mark of the same width, so nothing on the top
	// rail reflows during the entrance.
	settled, _ := m.turnStatus()
	arriving := settled
	arriving.Arriving = 7
	if a, b := lipgloss.Width(arriving.View(60)), lipgloss.Width(settled.View(60)); a != b {
		t.Fatalf("the arriving label is %d columns and the settled one %d", a, b)
	}
}

// A session that has not run a turn says `idle` — the summary is a fact about
// a turn, and there is not one yet.
func TestTurnStatus_FreshSessionIsIdle(t *testing.T) {
	view := stripANSI(frameModel(t, 130, 40).View().Content)
	if !strings.Contains(view, "idle") {
		t.Fatalf("a fresh session's rail should read idle:\n%s", view)
	}
}

// A decision waiting on the reader outranks the turn's own status: what the
// rail should say is how many answers it wants.
func TestTurnStatus_WaitingDecisionOutranksTheStatus(t *testing.T) {
	m := statusModel(t)
	m.pendingRun = "echo hi"
	m.state = stateConfirmRun
	m.syncViewport()
	view := stripANSI(m.View().Content)
	if !strings.Contains(view, "waiting") {
		t.Fatalf("an ungated decision should claim the activity slot:\n%s", view)
	}
	if strings.Contains(view, "thinking…") {
		t.Fatalf("the status line should not share the slot with the waiting chip:\n%s", view)
	}
}

// The status line takes the room the identity leaves and sheds fields to fit
// it; it never pushes the rail past the terminal.
func TestTurnStatus_RailNeverOverflows(t *testing.T) {
	for _, width := range []int{60, 80, 110, 130} {
		m := statusModel(t)
		m.width = width
		m.syncViewport()
		for _, line := range strings.Split(stripANSI(m.View().Content), "\n") {
			if got := len([]rune(line)); got > width {
				t.Fatalf("width %d produced a %d-column line: %q", width, got, line)
			}
		}
	}
}

// The end-to-end version of the same rule: a turn actually run through the
// model leaves the rail stating the close it appended, with the numbers the
// close row states.
func TestTurnStatus_ARealTurnResolvesOnTheRail(t *testing.T) {
	m := finishTurn(t, sendText(t, readyModel(t), "explain the loop"))

	s, ok := m.turnStatus()
	if !ok || !s.Done || s.Outcome != components.TurnDone {
		t.Fatalf("a finished turn should resolve into ✓ done (ok=%v %+v)", ok, s)
	}
	c := lastClose(t, m)
	if s.Outcome != c.State {
		t.Fatalf("the rail and the close row disagree: %+v vs %+v", s, c)
	}
	view := stripANSI(m.View().Content)
	if !strings.Contains(view, "✓ done") {
		t.Fatalf("the top rail should carry the resolved summary:\n%s", view)
	}
	// And the span it took is on the screen once, on the row the turn left in
	// the transcript rather than on the rail below it
	// (docs/interface/surfaces.md#the-input-frame).
	if c.Elapsed == "" {
		t.Fatal("a finished turn's close row should state its span")
	}
	if n := strings.Count(view, c.Elapsed); n != 1 {
		t.Fatalf("the finished turn's span %q is on screen %d times:\n%s", c.Elapsed, n, view)
	}
}

// The rule the counters are held to while a round works: nothing on screen
// moves that the session has not measured. A tool round bills nothing and
// streams nothing, so the account it is spending stands still for it.
func TestTurnStatus_CountsHoldThroughAToolRound(t *testing.T) {
	m := statusModel(t)
	m.vitals.startTurn()
	m.accumulateUsage(&provider.Usage{PromptTokens: 2000, CompletionTokens: 700})
	settleCounts(&m)
	beforeIn, beforeOut := m.liveSessionTokens()

	for range 10 {
		m.spinFrame++
		m.easeCounts()
	}
	if in, out := m.liveSessionTokens(); in != beforeIn || out != beforeOut {
		t.Fatalf("a round that billed nothing moved the counts: ↑%d ↓%d -> ↑%d ↓%d",
			beforeIn, beforeOut, in, out)
	}
}

// The wiring rather than the arithmetic: the update's tail is what aims the
// counters, so a count climbs without any of the handlers that move it having
// to remember to.
func TestTurnStatus_TheUpdateTailAimsTheCounters(t *testing.T) {
	m := spinModel(t)
	m.input.SetValue("do the thing")
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	if m.turnState() != stateStreaming {
		t.Fatalf("expected a turn in flight, got state %d", m.turnState())
	}

	// Prose arriving is the output growing, and the account has to follow it
	// without any of the stream's own handlers saying so.
	m.streaming = strings.Repeat("token ", 400)
	_, want := m.sessionTokensFrom(m.liveTurnTokens())
	if want == 0 {
		t.Fatal("prose arriving should give the output something to climb to")
	}
	m, _ = tick(t, m)
	if !m.countsEasing() {
		t.Fatal("the tail should have set the output counter climbing")
	}
	if _, got := m.liveSessionTokens(); got >= want {
		t.Fatalf("the first frame should be short of %d, got %d", want, got)
	}
	for range 20 {
		if !m.countsEasing() {
			break
		}
		m, _ = tick(t, m)
	}
	if _, got := m.liveSessionTokens(); got != want {
		t.Fatalf("the climb should land on the measured figure %d, got %d", want, got)
	}
}
