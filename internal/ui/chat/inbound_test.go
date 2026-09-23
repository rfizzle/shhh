package chat

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/rpc"
)

// idleInbound is an idle session wired to take lines under a policy, with
// its record's signals collected.
func idleInbound(t *testing.T, policy string, signals *[]string) Model {
	t.Helper()
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream).WithInbound(Inbound{Policy: policy})
	m = m.WithObserver(observe.Observer{Signal: func(_ observe.Pos, code, reason string) {
		*signals = append(*signals, code+":"+reason)
	}})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)
	m.lastKeypress = time.Time{}
	return m
}

// sendLine hands the session a line the way its listener does, and returns
// what the sender was told.
func sendLine(t *testing.T, m Model, from, text string) (Model, string) {
	t.Helper()
	taken := make(chan string, 1)
	updated, _ := m.Update(inboundMsg{line: InboundLine{From: from, Text: text, Taken: taken}})
	select {
	case word := <-taken:
		return updated.(Model), word
	default:
		t.Fatal("the sender was never told what became of its line")
	}
	return updated.(Model), ""
}

func lastMessage(m Model) provider.Message {
	msgs := m.agent.RequestMessages()
	return msgs[len(msgs)-1]
}

func TestInbound_AnIdleSessionTakesTheLineAsItsNextInstruction(t *testing.T) {
	var signals []string
	m := idleInbound(t, InboundAccept, &signals)
	m, word := sendLine(t, m, "2026-09-23 10:00:00", "master moved: rebase onto it")

	if word != rpc.TakenDelivered {
		t.Errorf("the sender is told %q, want %q", word, rpc.TakenDelivered)
	}
	if m.state != stateStreaming {
		t.Fatalf("an idle session starts a turn on the line, state %d", m.state)
	}
	last := lastMessage(m)
	if !last.Machine || last.Role != provider.RoleUser {
		t.Errorf("the line joins in the session's own voice, not the reader's: %+v", last)
	}
	if !strings.Contains(last.Content, "session 2026-09-23 10:00:00") ||
		!strings.HasSuffix(last.Content, "master moved: rebase onto it") {
		t.Errorf("the message names the sender and ends on the line, got:\n%s", last.Content)
	}
	row := m.transcript[len(m.transcript)-1]
	if row.notice == nil || row.notice.Verb != "steer" || row.notice.Subject != "from session 2026-09-23 10:00:00" {
		t.Errorf("the row is a steer naming the sending slot, got %+v", row.notice)
	}
	if !strings.Contains(m.summaryTarget, "rebase onto it") {
		t.Errorf("the line is what the turn is judged against, target %q", m.summaryTarget)
	}
	if strings.Join(signals, " ") != observe.SignalSteer+":session" {
		t.Errorf("the record files a steer with its source word, got %v", signals)
	}
}

func TestInbound_ALineWithNoSessionBehindItNamesTheCommandLine(t *testing.T) {
	var signals []string
	m := idleInbound(t, InboundAccept, &signals)
	m, _ = sendLine(t, m, "", "the release is tagged")
	if got := m.transcript[len(m.transcript)-1].notice.Subject; got != "from a command line" {
		t.Errorf("row subject %q", got)
	}
	if !strings.Contains(lastMessage(m).Content, "sent by a command line") {
		t.Errorf("the message names where it came from, got:\n%s", lastMessage(m).Content)
	}
}

func TestInbound_AMidTurnLineWaitsForTheBoundary(t *testing.T) {
	executor := func(string, json.RawMessage) (string, error) { return "result", nil }
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}, {Role: provider.RoleUser, Content: "go"}}
	m := New(msgs, mockStream).WithToolExecutor(executor).WithInbound(Inbound{Policy: InboundAccept})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)
	m.state = stateStreaming

	before := len(m.agent.RequestMessages())
	m, _ = sendLine(t, m, "lane-b", "rebase onto master")
	if len(m.agent.RequestMessages()) != before {
		t.Fatal("a line that arrives mid-turn does not join until the boundary")
	}
	if len(m.steering) != 1 || !m.steering[0].sent {
		t.Fatalf("the line is queued as a steer, got %+v", m.steering)
	}

	m, _ = execToolLoop(t, m, toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_1", Name: "read_file", Arguments: `{"path":"a.go"}`},
	}})
	last := lastMessage(m)
	if !last.Machine || !strings.HasSuffix(last.Content, "rebase onto master") {
		t.Fatalf("the line joins at the boundary in the session's voice, got %+v", last)
	}
	if m.agent.Rounds() != 0 {
		t.Errorf("a steer resets the round counter, got %d", m.agent.Rounds())
	}
}

func TestInbound_ACardWaitingIsUntouched(t *testing.T) {
	m := gatedModel(t, func(string, json.RawMessage) (string, error) { return "ok", nil }, nil)
	m = m.WithInbound(Inbound{Policy: InboundAccept})
	m = m.WithRunner(legacyRunner(func(context.Context, string) (string, int) { return "ran", 0 }))
	m = pendingExec(t, m, "make deploy")
	if m.state != stateConfirmRun {
		t.Fatalf("setup: expected a card, state %d", m.state)
	}
	pending := m.pendingApproval

	// Even a line that reads as an answer answers nothing.
	m, _ = sendLine(t, m, "lane-b", "y")
	if m.state != stateConfirmRun || m.pendingApproval != pending {
		t.Fatalf("the card waiting when the line arrived is still waiting, state %d", m.state)
	}
	if len(m.steering) != 1 || !m.steering[0].sent {
		t.Fatalf("the line waits behind the card as a steer, got %+v", m.steering)
	}
}

func TestInbound_ASlashCommandInTheLineIsText(t *testing.T) {
	var signals []string
	m := idleInbound(t, InboundAccept, &signals)
	m, _ = sendLine(t, m, "lane-b", "/clear")
	if !strings.HasSuffix(lastMessage(m).Content, "/clear") {
		t.Fatalf("the line reaches the model as text, got:\n%s", lastMessage(m).Content)
	}
	if m.state != stateStreaming {
		t.Errorf("the line started a turn rather than running a command, state %d", m.state)
	}
}

func TestInbound_AHeldLineWaitsOnItsCard(t *testing.T) {
	var signals []string
	m := idleInbound(t, InboundHold, &signals)
	before := len(m.agent.RequestMessages())
	m, word := sendLine(t, m, "lane-b", "rebase onto master")
	if word != rpc.TakenHeld {
		t.Errorf("the sender is told %q, want %q", word, rpc.TakenHeld)
	}
	if m.state != stateInboundHold {
		t.Fatalf("a held line opens its card, state %d", m.state)
	}
	if len(m.agent.RequestMessages()) != before {
		t.Fatal("a held line has not joined the conversation")
	}
	card := strings.Join(m.heldLineLines(), "\n")
	for _, want := range []string{"a line from session lane-b", "rebase onto master", "[y]", "[n]"} {
		if !strings.Contains(card, want) {
			t.Errorf("the card should carry %q:\n%s", want, card)
		}
	}

	// Only y and n answer it: there is no default to fall back on.
	for _, k := range []tea.KeyPressMsg{{Code: tea.KeyEnter}, {Code: tea.KeyEscape}, {Code: 'q', Text: "q"}} {
		updated, _ := m.Update(k)
		if updated.(Model).state != stateInboundHold {
			t.Fatalf("%q answered the card", k.String())
		}
	}

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m = updated.(Model)
	if m.state != stateStreaming {
		t.Fatalf("[y] passes the line to the turn, state %d", m.state)
	}
	if !strings.HasSuffix(lastMessage(m).Content, "rebase onto master") {
		t.Errorf("the passed line joined the conversation, got %q", lastMessage(m).Content)
	}
	if strings.Join(signals, " ") != observe.SignalSteer+":session" {
		t.Errorf("a passed line is filed as a steer from a session, got %v", signals)
	}
}

func TestInbound_ADroppedLineNeverReachesTheModel(t *testing.T) {
	var signals []string
	m := idleInbound(t, InboundHold, &signals)
	before := len(m.agent.RequestMessages())
	m, _ = sendLine(t, m, "lane-b", "rebase onto master")
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	m = updated.(Model)
	if m.state != stateInput || len(m.agent.RequestMessages()) != before {
		t.Fatalf("[n] drops the line, state %d", m.state)
	}
	row := m.transcript[len(m.transcript)-1]
	if row.notice == nil || row.notice.Outcome != "dropped" {
		t.Errorf("the drop is said on the transcript, got %+v", row.notice)
	}
	if len(signals) != 0 {
		t.Errorf("a dropped line is no steer, got %v", signals)
	}
}

func TestInbound_TheHeldCardWaitsForAnEmptyDraft(t *testing.T) {
	var signals []string
	m := idleInbound(t, InboundHold, &signals)
	m.input.SetValue("half a sentence")
	m, _ = sendLine(t, m, "lane-b", "rebase onto master")
	if m.state == stateInboundHold {
		t.Fatal("the card must not open over a sentence being typed: its next letter would answer it")
	}
	m.input.SetValue("")
	updated, _ := m.Update(inboundOpenMsg{})
	if updated.(Model).state != stateInboundHold {
		t.Fatal("the card opens once the draft is empty")
	}
}

func TestInbound_RefuseTakesNothing(t *testing.T) {
	var signals []string
	m := idleInbound(t, InboundRefuse, &signals)
	before := len(m.agent.RequestMessages())
	m, word := sendLine(t, m, "lane-b", "rebase onto master")
	if word != "" || m.state != stateInput || len(m.agent.RequestMessages()) != before || len(m.steering) != 0 {
		t.Fatalf("a refused line changes nothing, told %q, state %d", word, m.state)
	}
}

func TestInbound_TheDefaultHoldsUnderAutoAndAcceptsOtherwise(t *testing.T) {
	for _, c := range []struct {
		mode agent.Mode
		want string
	}{
		{agent.ModeAuto, InboundHold},
		{agent.ModeManual, InboundAccept},
		{agent.ModeAcceptEdits, InboundAccept},
		{agent.ModePlan, InboundAccept},
	} {
		m := New(nil, mockStream).WithApprovalMode(c.mode, nil)
		if got := m.inboundPolicy(); got != c.want {
			t.Errorf("%s: default %q, want %q", c.mode, got, c.want)
		}
	}
	m := New(nil, mockStream).WithApprovalMode(agent.ModeAuto, nil).WithInbound(Inbound{Policy: "accept"})
	if got := m.inboundPolicy(); got != InboundAccept {
		t.Errorf("a stated policy wins over the mode, got %q", got)
	}
}

func TestInbound_ACancelledTurnPutsTheLineBackOnItsCard(t *testing.T) {
	m := steeringModel(t, mockStream)
	m = m.WithInbound(Inbound{Policy: InboundAccept})
	m, _ = sendLine(t, m, "lane-b", "rebase onto master")
	m.cancelStreaming()
	if strings.Contains(m.input.Value(), "rebase") {
		t.Error("another session's words are never put in the reader's draft")
	}
	if len(m.inbound.held) != 1 || m.inbound.held[0].Text != "rebase onto master" {
		t.Errorf("the line goes back to its card, held %+v", m.inbound.held)
	}
}
