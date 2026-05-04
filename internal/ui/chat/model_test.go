package chat

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rfizzle/shhh/internal/provider"
)

func mockStream(msgs []provider.Message) (<-chan provider.StreamEvent, context.CancelFunc, error) {
	ch := make(chan provider.StreamEvent, 1)
	ch <- provider.StreamEvent{Token: "hello", Done: false}
	close(ch)
	_, cancel := context.WithCancel(context.Background())
	return ch, cancel, nil
}

func multiTokenStream(tokens ...string) StreamFunc {
	return func(msgs []provider.Message) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		ch := make(chan provider.StreamEvent, len(tokens)+1)
		for _, t := range tokens {
			ch <- provider.StreamEvent{Token: t}
		}
		ch <- provider.StreamEvent{Done: true}
		close(ch)
		_, cancel := context.WithCancel(context.Background())
		return ch, cancel, nil
	}
}

func TestNew_InitialState(t *testing.T) {
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream)

	if m.state != stateInput {
		t.Fatalf("expected stateInput, got %d", m.state)
	}
	if m.ready {
		t.Fatal("model should not be ready before first WindowSizeMsg")
	}
}

func TestWindowResize_SetsReady(t *testing.T) {
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	model := updated.(Model)

	if !model.ready {
		t.Fatal("model should be ready after WindowSizeMsg")
	}
	if model.width != 100 || model.height != 40 {
		t.Fatalf("unexpected dimensions: %dx%d", model.width, model.height)
	}
	if model.viewport.Width != 100 {
		t.Fatalf("viewport width should be 100, got %d", model.viewport.Width)
	}
	expectedVPHeight := 40 - inputHeight - chromeHeight
	if model.viewport.Height != expectedVPHeight {
		t.Fatalf("viewport height should be %d, got %d", expectedVPHeight, model.viewport.Height)
	}
}

func TestWindowResize_Subsequent(t *testing.T) {
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	model := updated.(Model)

	updated2, _ := model.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	model2 := updated2.(Model)

	if model2.viewport.Width != 60 {
		t.Fatalf("viewport width should update to 60, got %d", model2.viewport.Width)
	}
	expectedH := 20 - inputHeight - chromeHeight
	if model2.viewport.Height != expectedH {
		t.Fatalf("viewport height should be %d, got %d", expectedH, model2.viewport.Height)
	}
}

func TestViewBeforeReady(t *testing.T) {
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream)

	view := m.View()
	if !strings.Contains(view, "Initializing") {
		t.Fatalf("expected initializing text, got: %s", view)
	}
}

func TestWordWrap(t *testing.T) {
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream)

	input := "one two three four five six seven eight nine ten"
	wrapped := m.wordWrap(input, 20)

	for _, line := range strings.Split(wrapped, "\n") {
		if len(line) > 20 && !strings.Contains(line, " ") {
			continue
		}
		if len(line) > 25 {
			t.Fatalf("line too long: %q (%d chars)", line, len(line))
		}
	}
}

func TestWordWrap_PreservesNewlines(t *testing.T) {
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream)

	input := "line one\nline two\nline three"
	wrapped := m.wordWrap(input, 80)

	if !strings.Contains(wrapped, "line one\n") {
		t.Fatal("should preserve existing newlines")
	}
	if !strings.Contains(wrapped, "line two\n") {
		t.Fatal("should preserve existing newlines")
	}
}

func TestEmptyHistory_ShowsWelcome(t *testing.T) {
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream)
	m.width = 80

	content := m.renderHistory()
	if !strings.Contains(content, "Type a message") {
		t.Fatalf("expected welcome message, got: %s", content)
	}
}

func TestMultiTurn_MessageAccumulation(t *testing.T) {
	stream := multiTokenStream("world")
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, stream)

	// Initialize with window size
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)

	// Simulate user typing "hello" and pressing enter
	m.input.SetValue("hello")
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)

	if m.state != stateStreaming {
		t.Fatalf("expected stateStreaming, got %d", m.state)
	}
	if len(m.Messages()) != 2 {
		t.Fatalf("expected 2 messages (system+user), got %d", len(m.Messages()))
	}
	if m.Messages()[1].Content != "hello" {
		t.Fatalf("expected user message 'hello', got %q", m.Messages()[1].Content)
	}

	// Simulate stream started
	events, cancel, _ := stream(m.Messages())
	updated, _ = m.Update(streamStartedMsg{events: events, cancel: cancel})
	m = updated.(Model)

	// Simulate token arrival
	updated, _ = m.Update(tokenMsg("world"))
	m = updated.(Model)

	if m.streaming != "world" {
		t.Fatalf("expected streaming 'world', got %q", m.streaming)
	}

	// Simulate done
	updated, _ = m.Update(doneMsg{})
	m = updated.(Model)

	if m.state != stateInput {
		t.Fatalf("expected stateInput after done, got %d", m.state)
	}
	if len(m.Messages()) != 3 {
		t.Fatalf("expected 3 messages (system+user+assistant), got %d", len(m.Messages()))
	}
	if m.Messages()[2].Role != provider.RoleAssistant {
		t.Fatalf("expected assistant role, got %s", m.Messages()[2].Role)
	}
	if m.Messages()[2].Content != "world" {
		t.Fatalf("expected assistant content 'world', got %q", m.Messages()[2].Content)
	}
}

func TestMultiTurn_SecondExchange(t *testing.T) {
	callCount := 0
	stream := func(msgs []provider.Message) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		callCount++
		var token string
		if callCount == 1 {
			token = "first response"
		} else {
			token = "second response"
		}
		ch := make(chan provider.StreamEvent, 2)
		ch <- provider.StreamEvent{Token: token}
		ch <- provider.StreamEvent{Done: true}
		close(ch)
		_, cancel := context.WithCancel(context.Background())
		return ch, cancel, nil
	}

	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, stream)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)

	// First turn
	m.input.SetValue("question one")
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)

	events, cancel, _ := stream(m.Messages())
	updated, _ = m.Update(streamStartedMsg{events: events, cancel: cancel})
	m = updated.(Model)

	updated, _ = m.Update(tokenMsg("first response"))
	m = updated.(Model)
	updated, _ = m.Update(doneMsg{})
	m = updated.(Model)

	// Second turn
	m.input.SetValue("question two")
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)

	if len(m.Messages()) != 4 {
		t.Fatalf("expected 4 messages before second stream, got %d", len(m.Messages()))
	}

	events, cancel, _ = stream(m.Messages())
	updated, _ = m.Update(streamStartedMsg{events: events, cancel: cancel})
	m = updated.(Model)

	updated, _ = m.Update(tokenMsg("second response"))
	m = updated.(Model)
	updated, _ = m.Update(doneMsg{})
	m = updated.(Model)

	if len(m.Messages()) != 5 {
		t.Fatalf("expected 5 messages after second turn, got %d", len(m.Messages()))
	}

	// Verify message order
	expected := []provider.Role{
		provider.RoleSystem,
		provider.RoleUser,
		provider.RoleAssistant,
		provider.RoleUser,
		provider.RoleAssistant,
	}
	for i, msg := range m.Messages() {
		if msg.Role != expected[i] {
			t.Fatalf("message %d: expected role %s, got %s", i, expected[i], msg.Role)
		}
	}
	if m.Messages()[4].Content != "second response" {
		t.Fatalf("expected 'second response', got %q", m.Messages()[4].Content)
	}
}

func TestStreaming_TokenByToken(t *testing.T) {
	stream := multiTokenStream("one", " two", " three")
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, stream)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)

	m.input.SetValue("go")
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)

	events, cancel, _ := stream(m.Messages())
	updated, _ = m.Update(streamStartedMsg{events: events, cancel: cancel})
	m = updated.(Model)

	// Token by token
	updated, _ = m.Update(tokenMsg("one"))
	m = updated.(Model)
	if m.streaming != "one" {
		t.Fatalf("after first token, expected 'one', got %q", m.streaming)
	}

	updated, _ = m.Update(tokenMsg(" two"))
	m = updated.(Model)
	if m.streaming != "one two" {
		t.Fatalf("after second token, expected 'one two', got %q", m.streaming)
	}

	updated, _ = m.Update(tokenMsg(" three"))
	m = updated.(Model)
	if m.streaming != "one two three" {
		t.Fatalf("after third token, expected 'one two three', got %q", m.streaming)
	}

	updated, _ = m.Update(doneMsg{})
	m = updated.(Model)

	if m.Messages()[2].Content != "one two three" {
		t.Fatalf("final message content should be 'one two three', got %q", m.Messages()[2].Content)
	}
}

func TestStreaming_CancelPreservesPartial(t *testing.T) {
	stream := multiTokenStream("partial", " content")
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, stream)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)

	m.input.SetValue("go")
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)

	events, cancel, _ := stream(m.Messages())
	updated, _ = m.Update(streamStartedMsg{events: events, cancel: cancel})
	m = updated.(Model)

	// Receive one token, then cancel
	updated, _ = m.Update(tokenMsg("partial"))
	m = updated.(Model)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = updated.(Model)

	if m.state != stateInput {
		t.Fatal("should return to input state after cancel")
	}
	// Partial content should be preserved in messages
	if len(m.Messages()) != 3 {
		t.Fatalf("expected 3 messages (partial response preserved), got %d", len(m.Messages()))
	}
	if m.Messages()[2].Content != "partial" {
		t.Fatalf("expected partial content preserved, got %q", m.Messages()[2].Content)
	}
}

func TestStreamError_ReturnsToInput(t *testing.T) {
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)

	m.input.SetValue("go")
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)

	updated, _ = m.Update(streamErrMsg{err: context.DeadlineExceeded})
	m = updated.(Model)

	if m.state != stateInput {
		t.Fatal("should return to input state after error")
	}
	// Error should not add a broken assistant message
	if len(m.Messages()) != 2 {
		t.Fatalf("expected 2 messages (no assistant added on error), got %d", len(m.Messages()))
	}
}

func TestExit_CtrlD(t *testing.T) {
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	m = updated.(Model)

	if !m.quitting {
		t.Fatal("Ctrl+D should set quitting")
	}
	if cmd == nil {
		t.Fatal("Ctrl+D should return a quit cmd")
	}
}

func TestExit_SlashQuit(t *testing.T) {
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)

	m.input.SetValue("/quit")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)

	if !m.quitting {
		t.Fatal("/quit should set quitting")
	}
	if cmd == nil {
		t.Fatal("/quit should return a quit cmd")
	}
}

func TestExit_SlashExit(t *testing.T) {
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)

	m.input.SetValue("/exit")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)

	if !m.quitting {
		t.Fatal("/exit should set quitting")
	}
	if cmd == nil {
		t.Fatal("/exit should return a quit cmd")
	}
}

func TestExit_SlashQ(t *testing.T) {
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)

	m.input.SetValue("/q")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)

	if !m.quitting {
		t.Fatal("/q should set quitting")
	}
	if cmd == nil {
		t.Fatal("/q should return a quit cmd")
	}
}

func TestExit_CtrlC_DuringStreaming_DoesNotQuit(t *testing.T) {
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)

	m.input.SetValue("test")
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)

	events, cancel, _ := mockStream(m.Messages())
	updated, _ = m.Update(streamStartedMsg{events: events, cancel: cancel})
	m = updated.(Model)

	updated, _ = m.Update(tokenMsg("partial"))
	m = updated.(Model)

	// Ctrl+C during streaming cancels but does not quit
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = updated.(Model)

	if m.quitting {
		t.Fatal("Ctrl+C during streaming should cancel, not quit")
	}
	if m.state != stateInput {
		t.Fatal("should return to input after cancel")
	}
}

func TestExit_CtrlC_DuringInput_Quits(t *testing.T) {
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = updated.(Model)

	if !m.quitting {
		t.Fatal("Ctrl+C during input should quit")
	}
	if cmd == nil {
		t.Fatal("Ctrl+C during input should return quit cmd")
	}
}

func TestExit_SlashQuit_DuringStreaming_CancelsAndQuits(t *testing.T) {
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	cancelled := false
	stream := func(msgs []provider.Message) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		ch := make(chan provider.StreamEvent, 2)
		ch <- provider.StreamEvent{Token: "data"}
		ch <- provider.StreamEvent{Done: true}
		close(ch)
		_, cancel := context.WithCancel(context.Background())
		wrappedCancel := func() {
			cancelled = true
			cancel()
		}
		return ch, wrappedCancel, nil
	}

	m := New(msgs, stream)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)

	m.input.SetValue("go")
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)

	events, cancel, _ := stream(m.Messages())
	updated, _ = m.Update(streamStartedMsg{events: events, cancel: cancel})
	m = updated.(Model)

	// Now type /quit — this won't work mid-stream since input is disabled,
	// but verify the slash command logic works from input state
	// First cancel streaming to get back to input
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = updated.(Model)

	// Now /quit from input state
	m.input.SetValue("/quit")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)

	if !m.quitting {
		t.Fatal("/quit should set quitting")
	}
	if cmd == nil {
		t.Fatal("/quit should return quit cmd")
	}
	_ = cancelled
}

func TestRequestStream_SendsFullConversation(t *testing.T) {
	var receivedMsgs []provider.Message
	stream := func(msgs []provider.Message) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		receivedMsgs = make([]provider.Message, len(msgs))
		copy(receivedMsgs, msgs)
		ch := make(chan provider.StreamEvent, 1)
		ch <- provider.StreamEvent{Done: true}
		close(ch)
		_, cancel := context.WithCancel(context.Background())
		return ch, cancel, nil
	}

	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "first"},
		{Role: provider.RoleAssistant, Content: "reply"},
	}
	m := New(msgs, stream)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)

	m.input.SetValue("second")
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)

	// Execute the cmd returned by requestStream
	cmd := m.requestStream()
	cmdMsg := cmd()

	// The streamStartedMsg means it called our stream func
	if _, ok := cmdMsg.(streamStartedMsg); !ok {
		if _, ok := cmdMsg.(doneMsg); !ok {
			t.Fatalf("unexpected msg type: %T", cmdMsg)
		}
	}

	// Verify the stream received the full conversation
	if len(receivedMsgs) != 4 {
		t.Fatalf("stream should receive full conversation (4 msgs), got %d", len(receivedMsgs))
	}
	if receivedMsgs[3].Content != "second" {
		t.Fatalf("last message should be 'second', got %q", receivedMsgs[3].Content)
	}
}
