package chat

// The tick loop (docs/interface/README.md). What these assert is the
// rule rather than the animation: a chain is in flight exactly while
// something is moving, there is never more than one of them, and the frame
// every surface draws comes from that one chain.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// tick feeds one spinner tick and returns the model and whether the chain
// answered with the command that continues it.
func tick(t *testing.T, m Model) (Model, bool) {
	t.Helper()
	next, cmd := m.Update(spinner.TickMsg{})
	rm, ok := next.(Model)
	if !ok {
		t.Fatal("a tick should return the chat model")
	}
	return rm, cmd != nil
}

// carriesTick reports whether running cmd produces a spinner tick. It is the
// assertion the old code fails: the three transitions that start a turn
// returned their work with no tick beside it.
func carriesTick(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	switch msg := cmd().(type) {
	case spinner.TickMsg:
		return true
	case tea.BatchMsg:
		for _, sub := range msg {
			if carriesTick(sub) {
				return true
			}
		}
	}
	return false
}

// spinModel is a session wired to a stream that never finishes, so the turn
// it starts stays in flight for the length of a test.
func spinModel(t *testing.T) Model {
	t.Helper()
	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, blockingStream(t))
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 130, Height: 40})
	return updated.(Model)
}

func blockingStream(t *testing.T) StreamFunc {
	t.Helper()
	return func(msgs []provider.Message) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		ch := make(chan provider.StreamEvent)
		return ch, func() { close(ch) }, nil
	}
}

// The regression itself: the user's own prompt is one of the three
// transitions that never carried a tick, so the first turn of every session
// started with a frozen spinner.
func TestSpin_StartsWhenTheUserStartsATurn(t *testing.T) {
	m := spinModel(t)
	if m.spinning {
		t.Fatal("an idle session animates nothing, so no chain should be running")
	}
	m.input.SetValue("do the thing")
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	if m.turnState() != stateStreaming {
		t.Fatalf("expected a turn in flight, got state %d", m.turnState())
	}
	if !m.spinning {
		t.Fatal("starting a turn must start the tick loop")
	}
	if !carriesTick(cmd) {
		t.Fatal("the prompt that starts a turn must carry the tick that animates it")
	}
	after, alive := tick(t, m)
	if !alive {
		t.Fatal("a tick during a running turn must answer with the next one")
	}
	if after.spinFrame != m.spinFrame+1 {
		t.Fatalf("the tick should advance the one frame counter, got %d after %d", after.spinFrame, m.spinFrame)
	}
}

// The chain has to survive the handoffs between the stages of a turn, which
// is where the old guard dropped it: a tick arriving just after the state
// moved was discarded, and the chain went with it.
func TestSpin_SurvivesEveryHandoff(t *testing.T) {
	executor := func(name string, args json.RawMessage) (string, error) { return "result", nil }
	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, blockingStream(t)).
		WithToolExecutor(executor)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 130, Height: 40})
	m = updated.(Model)

	// The three handoffs of a tool round, in the order a turn meets them.
	// None of the three carried a tick of its own, so each is a place the
	// animation used to be able to stop for good.
	check := func(stage string, next tea.Model) Model {
		t.Helper()
		rm := next.(Model)
		if !rm.spinning {
			t.Fatalf("%s dropped the tick loop", stage)
		}
		rm, alive := tick(t, rm)
		if !alive {
			t.Fatalf("the chain stopped answering after %s", stage)
		}
		return rm
	}

	m = check("the user's prompt", sendTextModel(t, m, "run the tools"))

	next, cmd := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_1", Name: "read_file", Arguments: `{"path":"a.go"}`},
	}})
	m = check("tool dispatch", next)

	results := cmdMsg(t, cmd)
	if _, ok := results.(toolResultsMsg); !ok {
		t.Fatalf("expected toolResultsMsg, got %T", results)
	}
	next, _ = m.Update(results)
	m = check("the request after the round", next)

	if m.turnState() != stateStreaming {
		t.Fatalf("expected the turn to be streaming again, got %d", m.turnState())
	}
}

// sendTextModel is sendText with the tea.Model still wrapped, for callers
// that want to assert on the transition itself.
func sendTextModel(t *testing.T, m Model, text string) tea.Model {
	t.Helper()
	m.input.SetValue(text)
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	return next
}

// Entering a working state resumes the loop whatever route it took there —
// including from an idle session whose chain has already ended.
func TestSpin_ResumesAfterTheLoopHasStopped(t *testing.T) {
	m := spinModel(t)
	m = sendText(t, m, "first")
	if !m.spinning {
		t.Fatal("expected a running chain")
	}

	// The turn ends: the next tick is the chain's last, and it says so.
	m.setTurnState(stateInput)
	stopped, alive := tick(t, m)
	if alive {
		t.Fatal("an idle session should let the chain end rather than tick forever")
	}
	if stopped.spinning {
		t.Fatal("the model must record that the chain has ended")
	}

	// And a second turn starts a fresh one rather than waiting on the dead
	// chain — which is the freeze the story reports.
	stopped.input.SetValue("second")
	again, cmd := stopped.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	rm := again.(Model)
	if !rm.spinning || cmd == nil {
		t.Fatal("a working state entered from idle must restart the loop")
	}
}

// One tick source, never three. Every further message while the loop
// runs has to leave it alone rather than batching a second chain.
func TestSpin_OneChainAtATime(t *testing.T) {
	m := spinModel(t)
	m = sendText(t, m, "work")
	if !m.spinning {
		t.Fatal("expected a running chain")
	}
	if cmd := m.spinCmd(); cmd != nil {
		t.Fatal("a second chain must never be started while one is running")
	}
	// A tick from a chain this spinner has superseded advances nothing: the
	// passive surfaces' frame counter would otherwise drift a frame away from
	// m.spinner's for the rest of the session.
	before := m.spinFrame
	stale, _ := m.Update(spinner.TickMsg{ID: m.spinner.ID() + 1})
	if got := stale.(Model).spinFrame; got != before {
		t.Fatalf("a rejected tick must not advance the frame, got %d after %d", got, before)
	}
}

// The three places the one-tick rule names show the same frame, because they
// read the same counter rather than each keeping their own.
func TestSpin_OneFrameAcrossTheThreeSurfaces(t *testing.T) {
	m := spinModel(t)
	m = sendText(t, m, "work")
	for i := 0; i < 3; i++ {
		m, _ = tick(t, m)
	}
	want := components.Spinner{Frame: m.spinFrame}.Glyph()

	// The frame header draws through bubbles' own model, which is why the
	// counter may only advance with it: a frame counted while bubbles
	// rejected the tick would drift the two apart for the rest of the
	// session, and the attached rail would then disagree with
	// everything else on screen.
	if got := stripANSI(m.spinner.View()); got != want {
		t.Fatalf("the frame header should be on frame %q, got %q", want, got)
	}
	if got := stripANSI(m.frameActivity(60)); !strings.HasPrefix(got, want) {
		t.Fatalf("the frame's activity slot should be on frame %q, got %q", want, got)
	}

	status, ok := m.turnStatus()
	if !ok {
		t.Fatal("a running turn should have a status line")
	}
	if got := stripANSI(status.View(80)); !strings.HasPrefix(got, want) {
		t.Fatalf("the status line should be on frame %q, got %q", want, got)
	}

	row := m.activityRowFor(entry{kind: entryTool, toolName: "read_file", toolArgs: `{"path":"a.go"}`, toolResult: pendingToolResult})
	if !row.Spin || row.Frame != m.spinFrame {
		t.Fatalf("a running row should animate from the one frame, got spin=%v frame=%d", row.Spin, row.Frame)
	}
	if got := stripANSI(row.View(80)); !strings.Contains(got, want) {
		t.Fatalf("the running row should be on frame %q, got %q", want, got)
	}
}

// A row nothing is ticking keeps the still `▸` rather than standing on one
// braille frame, which would read as a hang.
func TestSpin_StillRowKeepsTheRunningGlyph(t *testing.T) {
	row := components.ActivityRow{State: components.ActivityRunning, Verb: "run", Target: "go test"}
	if got := stripANSI(row.View(80)); !strings.Contains(got, "▸") {
		t.Fatalf("a still running row should read ▸, got %q", got)
	}
}
