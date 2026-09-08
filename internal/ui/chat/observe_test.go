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

// The position an event is filed at and the position the conversation writes
// on its messages are the same position, which is the whole of what joins the
// record to the words — a session that recorded round 8 and stamped its
// messages round 7 would be two tables about different sessions
// (docs/capabilities/sessions-and-memory.md#a-round-can-be-read-back).
func TestObserver_TheRecordedRoundIsTheRoundTheConversationCarries(t *testing.T) {
	var at observe.Pos
	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream).
		WithObserver(observe.Observer{ToolCall: func(p observe.Pos, _ string, _ time.Duration, _, _ string) {
			at = p
		}})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	model := updated.(Model)
	updated, _ = model.sendUserMessage("where is the steering tuned?")
	model = updated.(Model)
	model.state = stateStreaming

	call := provider.ToolCall{ID: "1", Name: "search", Arguments: `{"pattern":"steeringItem"}`}
	model.agent.BeginToolRound("looking", []provider.ToolCall{call}, nil)
	updated, _ = model.Update(toolResultsMsg{runID: model.agent.RunID(), results: []agent.ToolResult{
		{Call: call, Result: "one hit", Duration: time.Millisecond},
	}})
	model = updated.(Model)

	if at.Turn != 1 || at.Round != 1 {
		t.Fatalf("the event was filed at turn %d round %d, want turn 1 round 1", at.Turn, at.Round)
	}
	var found bool
	for _, msg := range model.agent.Messages() {
		if len(msg.ToolCalls) == 0 {
			continue
		}
		found = true
		if msg.Turn != at.Turn || msg.Round != at.Round {
			t.Fatalf("the call was written at turn %d round %d, but recorded at turn %d round %d",
				msg.Turn, msg.Round, at.Turn, at.Round)
		}
	}
	if !found {
		t.Fatal("the round's calls never reached the conversation")
	}
}

// And a steer moves the turn the same way a typed message does, so the words
// it puts in the conversation and the events filed after it agree about
// which turn they are. A steer is the case most likely to be diagnosed later
// — it is what somebody does when a run has gone wrong — so a join that
// broke on one would be missing exactly where it is wanted.
func TestObserver_ASteerMovesTheTurnOnBothSidesOfTheJoin(t *testing.T) {
	var at observe.Pos
	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream).
		WithObserver(observe.Observer{Signal: func(p observe.Pos, code, _ string) {
			if code == observe.SignalSteer {
				at = p
			}
		}})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	model := updated.(Model)
	updated, _ = model.sendUserMessage("do the task")
	model = updated.(Model)

	model.steering = []steeringItem{{text: "actually do this instead"}}
	if !model.injectSteering() {
		t.Fatal("the steering should inject")
	}

	if at.Turn != model.turnCount {
		t.Fatalf("the steer was recorded at turn %d while the session is on turn %d", at.Turn, model.turnCount)
	}
	last := model.agent.Messages()[len(model.agent.Messages())-1]
	if last.Content != "actually do this instead" {
		t.Fatalf("the steering never reached the conversation, last message is %+v", last)
	}
	if last.Turn != at.Turn {
		t.Fatalf("the steer was written at turn %d and recorded at turn %d", last.Turn, at.Turn)
	}

	// And what the model does after it is written under the same turn, which
	// is what the rest of the timeline is joined by.
	model.agent.BeginToolRound("looking", []provider.ToolCall{{ID: "1", Name: "search"}}, nil)
	call := model.agent.Messages()[len(model.agent.Messages())-1]
	if call.Turn != model.turnCount {
		t.Fatalf("the round after the steer was written at turn %d, want %d", call.Turn, model.turnCount)
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
