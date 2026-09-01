package chat

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/tools"
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

func TestTurnStatus_PhaseFollowsWhatTheTurnIsDoing(t *testing.T) {
	m := statusModel(t)

	// Nothing has arrived yet: the model is reasoning before it acts.
	if p, _, ok := m.turnPhase(); !ok || p != components.PhaseThinking {
		t.Fatalf("a silent stream = phase %d ok=%v, want thinking", p, ok)
	}

	m.streaming = "here is what I found"
	if p, _, ok := m.turnPhase(); !ok || p != components.PhaseStreaming {
		t.Fatalf("prose arriving = phase %d ok=%v, want streaming", p, ok)
	}

	m.state = stateClassifying
	if p, _, ok := m.turnPhase(); !ok || p != components.PhaseDeciding {
		t.Fatalf("the classifier = phase %d ok=%v, want deciding", p, ok)
	}

	m.state = stateRunningCmd
	m.runningCommand = "go test ./internal/agent/...\nsecond line"
	p, tool, ok := m.turnPhase()
	if !ok || p != components.PhaseRunning || tool != "go test ./internal/agent/... …" {
		t.Fatalf("a running command = phase %d tool %q ok=%v", p, tool, ok)
	}

	// An idle session is in none of the four.
	m.state = stateInput
	if _, _, ok := m.turnPhase(); ok {
		t.Fatal("an idle session should report no phase")
	}
}

func TestTurnStatus_NamesTheCallItIsRunning(t *testing.T) {
	m := statusModel(t)
	cases := []struct {
		name string
		call provider.ToolCall
		want string
	}{
		// A command is named by the command: `running run go test` says it
		// twice.
		{"command", provider.ToolCall{Name: tools.ExecCommandName, Arguments: `{"command":"go test ./..."}`}, "go test ./..."},
		// Everything else keeps the grid's verb, so the argument is not
		// mistaken for something being executed.
		{"read", provider.ToolCall{Name: "read_file", Arguments: `{"path":"internal/agent/loop.go"}`}, "read internal/agent/loop.go"},
		{"edit", provider.ToolCall{Name: "edit_file", Arguments: `{"path":"internal/ui/chat/frame.go"}`}, "edit internal/ui/chat/frame.go"},
		{"no argument", provider.ToolCall{Name: "read_file"}, "read"},
	}
	for _, c := range cases {
		m.runningTools = []provider.ToolCall{c.call}
		if got := m.runningToolLabel(); got != c.want {
			t.Fatalf("%s = %q, want %q", c.name, got, c.want)
		}
	}

	// A round running three calls at once is named by none of them.
	m.runningTools = []provider.ToolCall{
		{Name: "read_file", Arguments: `{"path":"a.go"}`},
		{Name: "read_file", Arguments: `{"path":"b.go"}`},
		{Name: "search", Arguments: `{"pattern":"x"}`},
	}
	if got := m.runningToolLabel(); got != "" {
		t.Fatalf("a batch of three named %q; it should name none of them", got)
	}
}

func TestTurnStatus_TokensMoveWhileTheProseArrives(t *testing.T) {
	m := statusModel(t)
	before, _ := m.turnStatus()

	m.streaming = strings.Repeat("token ", 400)
	after, _ := m.turnStatus()
	if after.Down == before.Down {
		t.Fatalf("output tokens did not move as prose arrived (%q)", after.Down)
	}
	if after.Cost == before.Cost {
		t.Fatalf("cost is derived from the live counts, so it should have moved too (%q)", after.Cost)
	}
	if after.Up != before.Up {
		t.Fatalf("input tokens should not move while output arrives (%q -> %q)", before.Up, after.Up)
	}
}

// The thinking half of the same account: reasoning is billed as output, and
// on a model that reasons before it answers it is most of what the opening of
// a round produces — the seconds the rail used to report as a phase with no
// numbers under it.
func TestTurnStatus_TokensMoveWhileTheReasoningArrives(t *testing.T) {
	m := statusModel(t)
	m.events = make(chan provider.StreamEvent)
	before, _ := m.turnStatus()

	m.appendThinking(strings.Repeat("weighing it up ", 200))
	after, _ := m.turnStatus()
	if after.Down == before.Down {
		t.Fatalf("output tokens did not move as the reasoning arrived (%q)", after.Down)
	}

	// The row stays on screen for the rest of the turn, but the usage event
	// that closed its round has already counted those tokens: the estimate
	// stops with the round rather than being added to what it was billed.
	m.events = nil
	closed, _ := m.turnStatus()
	if closed.Down != before.Down {
		t.Fatalf("the estimate should stop when the round does: %q -> %q", before.Down, closed.Down)
	}
}

// Until the turn's first request reports, there is no billed prompt to
// state — and `↑0` would be stating a number the session knows is wrong.
func TestTurnStatus_PromptEstimatedUntilTheFirstUsageLands(t *testing.T) {
	m := statusModel(t)
	m.vitals.startTurn() // a fresh turn: nothing billed yet
	if got, _ := m.turnStatus(); got.Up == "" || got.Up == "0" {
		t.Fatalf("an unbilled prompt should state the context estimate, got %q", got.Up)
	}

	m.accumulateUsage(&provider.Usage{PromptTokens: 41200, CompletionTokens: 100})
	if got, _ := m.turnStatus(); got.Up != formatTokenCount(41200) {
		t.Fatalf("a reported prompt replaces the estimate, got %q", got.Up)
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
	// The numbers agree because they are the same numbers.
	if s.Tools != close.Tools || s.Duration != close.Elapsed || s.Cost != close.Spend {
		t.Fatalf("the resolved line disagrees with the close row: %+v", s)
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
	if !strings.Contains(view, "thinking…") || !strings.Contains(view, "$") {
		t.Fatalf("the top rail should carry the live status:\n%s", view)
	}

	m.state = stateInput
	m.transcript = append(m.transcript, entry{kind: entryTurnClose, turn: 1,
		close: &components.TurnClose{State: components.TurnDone, Tools: 18, Elapsed: "1m 04s", Spend: "$0.14"}})
	view = stripANSI(m.View().Content)
	if !strings.Contains(view, "✓ done · 1m 04s · 18 tools · $0.14") {
		t.Fatalf("the top rail should resolve into the turn summary:\n%s", view)
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
	if s.Duration != c.Elapsed || s.Tools != c.Tools || s.Cost != c.Spend {
		t.Fatalf("the rail and the close row disagree: %+v vs %+v", s, c)
	}
	if view := stripANSI(m.View().Content); !strings.Contains(view, "✓ done") {
		t.Fatalf("the top rail should carry the resolved summary:\n%s", view)
	}
}
