package chat

import (
	"fmt"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/storage"
)

// Every hook on the contract still has a forwarder in this package that
// reaches it. The codes and their meanings are internal/observe's to test,
// and whether each hook site still fires is the four tests below; what this
// holds is the seam between them — a forwarder that stopped calling its
// hook, or a hook the adapter never grew one for. Decision and Session have
// no test below, so for those two this is the only cover.
func TestObserver_ModelReachesEveryHook(t *testing.T) {
	db, err := storage.OpenPath(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	var reached []string
	m := New([]provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "hello"},
	}, mockStream).
		WithDB(db).
		WithObserver(observe.Observer{
			Usage:    func(int64, int64, int64, float64, bool) { reached = append(reached, "usage") },
			ToolCall: func(observe.Pos, string, time.Duration, string, string) { reached = append(reached, "tool") },
			Decision: func(observe.Pos, string, string) { reached = append(reached, "decision") },
			Turn:     func(int64, int64, time.Duration, string) { reached = append(reached, "turn") },
			Signal:   func(observe.Pos, string, string) { reached = append(reached, "signal") },
			Session:  func(string) { reached = append(reached, "session") },
		})
	m.notifyUsage()
	m.recordToolResult("read_file", time.Millisecond, "data")
	m.recordDecision(observe.DecisionAllow, "user")
	m.recordTurn(observe.TurnDone)
	m.signal(observe.SignalMode, "auto")
	// Session has no recorder of its own — the slot's name is reported from
	// the autosave that decides it, which needs a store and something past
	// the system prompt to save.
	m.autosaveCmd()

	want := []string{"usage", "tool", "decision", "turn", "signal", "session"}
	if len(reached) != len(want) {
		t.Fatalf("expected %v, got %v", want, reached)
	}
	for i := range want {
		if reached[i] != want[i] {
			t.Fatalf("hook %d: expected %q, got %q", i, want[i], reached[i])
		}
	}
}

func TestObserver_UsageReportsTurnsAndTotals(t *testing.T) {
	var gotTurns, gotIn, gotOut int64
	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream).
		WithObserver(observe.Observer{Usage: func(turns, tokensIn, tokensOut int64, _ float64, _ bool) {
			gotTurns, gotIn, gotOut = turns, tokensIn, tokensOut
		}})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	model := updated.(Model)

	updated, _ = model.sendUserMessage("hello")
	model = updated.(Model)
	model.accumulateUsage(&provider.Usage{PromptTokens: 10, CompletionTokens: 5})

	if gotTurns != 1 || gotIn != 10 || gotOut != 5 {
		t.Fatalf("expected (1, 10, 5), got (%d, %d, %d)", gotTurns, gotIn, gotOut)
	}
}

func TestObserver_ToolEventsRecorded(t *testing.T) {
	var events []string
	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream).
		WithObserver(observe.Observer{ToolCall: func(_ observe.Pos, tool string, duration time.Duration, outcome, class string) {
			events = append(events, tool+":"+outcome)
		}})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	model := updated.(Model)
	model.state = stateStreaming

	updated, _ = model.Update(toolResultsMsg{runID: 0, results: []agent.ToolResult{
		{Call: provider.ToolCall{ID: "1", Name: "read_file"}, Result: "data", Duration: time.Millisecond},
		{Call: provider.ToolCall{ID: "2", Name: "search"}, Result: "error: bad pattern", Duration: time.Millisecond},
	}})
	_ = updated

	want := []string{"read_file:ok", "search:error"}
	if len(events) != len(want) {
		t.Fatalf("expected %d events, got %v", len(want), events)
	}
	for i, w := range want {
		if events[i] != w {
			t.Fatalf("event %d: expected %q, got %q", i, w, events[i])
		}
	}
}

func TestObserver_TurnRecordedOnClose(t *testing.T) {
	var turns []string
	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream).
		WithObserver(observe.Observer{Turn: func(turn, rounds int64, _ time.Duration, outcome string) {
			turns = append(turns, fmt.Sprintf("%d:%d:%s", turn, rounds, outcome))
		}})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	model := updated.(Model)
	updated, _ = model.sendUserMessage("hello")
	model = updated.(Model)
	updated, _ = model.Update(doneMsg{})
	model = updated.(Model)

	if len(turns) != 1 || turns[0] != "1:0:done" {
		t.Fatalf("expected one done turn, got %v", turns)
	}
	// Cancelling the next turn reports it as cancelled.
	updated, _ = model.sendUserMessage("again")
	model = updated.(Model)
	model.cancelStreaming()
	if len(turns) != 2 || turns[1] != "2:0:cancelled" {
		t.Fatalf("expected a cancelled turn, got %v", turns)
	}
}

func TestObserver_SignalsFromResultsAndSummary(t *testing.T) {
	var signals []string
	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream).
		WithObserver(observe.Observer{Signal: func(at observe.Pos, code, reason string) {
			signals = append(signals, fmt.Sprintf("%d/%s:%s", at.Turn, code, reason))
		}})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	model := updated.(Model)
	updated, _ = model.sendUserMessage("hello")
	model = updated.(Model)
	model.state = stateStreaming

	updated, _ = model.Update(toolResultsMsg{runID: model.agent.RunID(), results: []agent.ToolResult{
		{Call: provider.ToolCall{ID: "1", Name: "search"}, Result: "[repeat: this exact search call has now run 2 times]\nx"},
	}})
	model = updated.(Model)
	model.applyMode(agent.ModeAuto)
	model.applyMode(agent.ModeAuto)

	want := []string{"1/repeat-notice:search", "1/mode:auto"}
	if len(signals) != len(want) {
		t.Fatalf("expected %v, got %v", want, signals)
	}
	for i := range want {
		if signals[i] != want[i] {
			t.Fatalf("signal %d: expected %q, got %q", i, want[i], signals[i])
		}
	}
}

func TestSummaryStateCode(t *testing.T) {
	if summaryStateCode(agent.SummaryOffTarget) != "off-target" || summaryStateCode(agent.SummaryOnTarget) != "on-target" || summaryStateCode(agent.SummaryUncertain) != "unclear" {
		t.Fatal("summary state codes drifted")
	}
}
