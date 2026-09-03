package chat

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/ui/components"
)

func mockStream(msgs []provider.Message, _ string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
	ch := make(chan provider.StreamEvent, 1)
	ch <- provider.StreamEvent{Token: "hello", Done: false}
	close(ch)
	_, cancel := context.WithCancel(context.Background())
	return ch, cancel, nil
}

func multiTokenStream(tokens ...string) StreamFunc {
	return func(msgs []provider.Message, _ string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
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
	// The viewport is the transcript, not the pane: the last column is the
	// scroll gutter's.
	if want := 100 - horizontalPadding*2 - components.ScrollGutterWidth; model.viewport.Width() != want {
		t.Fatalf("viewport width should be %d, got %d", want, model.viewport.Width())
	}
	expectedVPHeight := 40 - inputHeight - (headerHeight + dividerHeight + bottomChromeHeight)
	if model.viewport.Height() != expectedVPHeight {
		t.Fatalf("viewport height should be %d, got %d", expectedVPHeight, model.viewport.Height())
	}
}

func TestWindowResize_Subsequent(t *testing.T) {
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	model := updated.(Model)

	updated2, _ := model.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	model2 := updated2.(Model)

	if want := 60 - horizontalPadding*2 - components.ScrollGutterWidth; model2.viewport.Width() != want {
		t.Fatalf("viewport width should update to %d, got %d", want, model2.viewport.Width())
	}
	expectedH := 20 - inputHeight - (headerHeight + dividerHeight + bottomChromeHeight)
	if model2.viewport.Height() != expectedH {
		t.Fatalf("viewport height should be %d, got %d", expectedH, model2.viewport.Height())
	}
}

func TestViewBeforeReady(t *testing.T) {
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream)

	view := m.View().Content
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
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
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
	events, cancel, _ := stream(m.Messages(), provider.ToolChoiceAuto)
	updated, _ = m.Update(streamStartedMsg{events: events, cancel: cancel})
	m = updated.(Model)

	// Simulate token arrival
	updated, _ = m.Update(tokenMsg{text: "world"})
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
	stream := func(msgs []provider.Message, _ string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
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
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)

	events, cancel, _ := stream(m.Messages(), provider.ToolChoiceAuto)
	updated, _ = m.Update(streamStartedMsg{events: events, cancel: cancel})
	m = updated.(Model)

	updated, _ = m.Update(tokenMsg{text: "first response"})
	m = updated.(Model)
	updated, _ = m.Update(doneMsg{})
	m = updated.(Model)

	// Second turn
	m.input.SetValue("question two")
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)

	if len(m.Messages()) != 4 {
		t.Fatalf("expected 4 messages before second stream, got %d", len(m.Messages()))
	}

	events, cancel, _ = stream(m.Messages(), provider.ToolChoiceAuto)
	updated, _ = m.Update(streamStartedMsg{events: events, cancel: cancel})
	m = updated.(Model)

	updated, _ = m.Update(tokenMsg{text: "second response"})
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
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)

	events, cancel, _ := stream(m.Messages(), provider.ToolChoiceAuto)
	updated, _ = m.Update(streamStartedMsg{events: events, cancel: cancel})
	m = updated.(Model)

	// Token by token
	updated, _ = m.Update(tokenMsg{text: "one"})
	m = updated.(Model)
	if m.streaming != "one" {
		t.Fatalf("after first token, expected 'one', got %q", m.streaming)
	}

	updated, _ = m.Update(tokenMsg{text: " two"})
	m = updated.(Model)
	if m.streaming != "one two" {
		t.Fatalf("after second token, expected 'one two', got %q", m.streaming)
	}

	updated, _ = m.Update(tokenMsg{text: " three"})
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
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)

	events, cancel, _ := stream(m.Messages(), provider.ToolChoiceAuto)
	updated, _ = m.Update(streamStartedMsg{events: events, cancel: cancel})
	m = updated.(Model)

	// Receive one token, then cancel: the first press arms, the second acts.
	updated, _ = m.Update(tokenMsg{text: "partial"})
	m = updated.(Model)

	updated, _ = m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
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
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
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

	// The first press arms the quit window; the second quits.
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	m = updated.(Model)
	if m.quitting {
		t.Fatal("a single Ctrl+D should arm, not quit")
	}
	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	m = updated.(Model)

	if !m.quitting {
		t.Fatal("Ctrl+D twice should set quitting")
	}
	if cmd == nil {
		t.Fatal("Ctrl+D twice should return a quit cmd")
	}
}

func TestExit_SlashQuit(t *testing.T) {
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)

	m.input.SetValue("/quit")
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
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
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
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
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
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
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)

	events, cancel, _ := mockStream(m.Messages(), provider.ToolChoiceAuto)
	updated, _ = m.Update(streamStartedMsg{events: events, cancel: cancel})
	m = updated.(Model)

	updated, _ = m.Update(tokenMsg{text: "partial"})
	m = updated.(Model)

	// Ctrl+C twice during streaming cancels but does not quit
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
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

	// Two presses: the first arms the quit window, the second quits.
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	m = updated.(Model)
	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	m = updated.(Model)

	if !m.quitting {
		t.Fatal("Ctrl+C twice during input should quit")
	}
	if cmd == nil {
		t.Fatal("Ctrl+C twice during input should return quit cmd")
	}
}

func TestExit_SlashQuit_DuringStreaming_CancelsAndQuits(t *testing.T) {
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	cancelled := false
	stream := func(msgs []provider.Message, _ string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
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
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)

	events, cancel, _ := stream(m.Messages(), provider.ToolChoiceAuto)
	updated, _ = m.Update(streamStartedMsg{events: events, cancel: cancel})
	m = updated.(Model)

	// Now type /quit — this won't work mid-stream since input is disabled,
	// but verify the slash command logic works from input state
	// First cancel streaming (two presses) to get back to input
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	m = updated.(Model)

	// Now /quit from input state
	m.input.SetValue("/quit")
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
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
	stream := func(msgs []provider.Message, _ string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
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
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
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

// execToolLoop feeds a toolCallsMsg through the async tool-execution flow:
// Update returns a cmd that runs the tools, whose toolResultsMsg is fed back.
func execToolLoop(t *testing.T, m Model, msg toolCallsMsg) (Model, tea.Cmd) {
	t.Helper()
	updated, cmd := m.Update(msg)
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("expected tool execution cmd")
	}
	res := cmdMsg(t, cmd)
	if _, ok := res.(toolResultsMsg); !ok {
		t.Fatalf("expected toolResultsMsg, got %T", res)
	}
	updated, cmd = m.Update(res)
	return updated.(Model), cmd
}

func TestToolCallLoop_SingleToolCall(t *testing.T) {
	callCount := 0
	stream := func(msgs []provider.Message, _ string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		callCount++
		ch := make(chan provider.StreamEvent, 2)
		if callCount == 1 {
			ch <- provider.StreamEvent{
				ToolCalls: []provider.ToolCall{
					{ID: "call_1", Name: "read_file", Arguments: `{"path":"test.go"}`},
				},
				Done: true,
			}
		} else {
			ch <- provider.StreamEvent{Token: "file contents are good"}
			ch <- provider.StreamEvent{Done: true}
		}
		close(ch)
		_, cancel := context.WithCancel(context.Background())
		return ch, cancel, nil
	}

	executor := func(name string, args json.RawMessage) (string, error) {
		if name != "read_file" {
			t.Fatalf("unexpected tool name: %s", name)
		}
		return "package main", nil
	}

	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, stream).WithToolExecutor(executor)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)

	m.input.SetValue("read test.go")
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)

	// Stream starts, returns tool call
	events, cancel, _ := stream(m.Messages(), provider.ToolChoiceAuto)
	_ = cancel
	updated, _ = m.Update(streamStartedMsg{events: events, cancel: cancel})
	m = updated.(Model)

	// waitForEvent yields toolCallsMsg
	var cmd tea.Cmd
	m, cmd = execToolLoop(t, m, toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_1", Name: "read_file", Arguments: `{"path":"test.go"}`},
	}})

	// Should have: system, user, assistant(tool calls), tool result
	if len(m.Messages()) != 4 {
		t.Fatalf("expected 4 messages after tool execution, got %d", len(m.Messages()))
	}
	if m.Messages()[2].Role != provider.RoleAssistant {
		t.Fatalf("expected assistant message, got %s", m.Messages()[2].Role)
	}
	if len(m.Messages()[2].ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call on assistant msg, got %d", len(m.Messages()[2].ToolCalls))
	}
	if m.Messages()[3].Role != provider.RoleTool {
		t.Fatalf("expected tool message, got %s", m.Messages()[3].Role)
	}
	if m.Messages()[3].Content != "package main" {
		t.Fatalf("expected tool result 'package main', got %q", m.Messages()[3].Content)
	}
	if m.Messages()[3].ToolCallID != "call_1" {
		t.Fatalf("expected tool call ID 'call_1', got %q", m.Messages()[3].ToolCallID)
	}

	// The cmd should be a requestStream (re-stream)
	if cmd == nil {
		t.Fatal("expected non-nil cmd to re-stream after tool execution")
	}
}

func TestToolCallLoop_MultipleToolCalls(t *testing.T) {
	executor := func(name string, args json.RawMessage) (string, error) {
		switch name {
		case "read_file":
			return "file content", nil
		case "list_directory":
			return "dir: src\nfile: main.go", nil
		default:
			return "", nil
		}
	}

	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "check stuff"},
	}
	m := New(msgs, mockStream).WithToolExecutor(executor)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)
	m.state = stateStreaming

	m, _ = execToolLoop(t, m, toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_1", Name: "read_file", Arguments: `{"path":"a.go"}`},
		{ID: "call_2", Name: "list_directory", Arguments: `{"path":"."}`},
	}})

	// system, user, assistant(2 tool calls), tool_result_1, tool_result_2
	if len(m.Messages()) != 5 {
		t.Fatalf("expected 5 messages, got %d", len(m.Messages()))
	}
	if m.Messages()[3].Content != "file content" {
		t.Errorf("expected first tool result 'file content', got %q", m.Messages()[3].Content)
	}
	if m.Messages()[3].ToolCallID != "call_1" {
		t.Errorf("expected tool call ID 'call_1', got %q", m.Messages()[3].ToolCallID)
	}
	if m.Messages()[4].Content != "dir: src\nfile: main.go" {
		t.Errorf("expected second tool result, got %q", m.Messages()[4].Content)
	}
	if m.Messages()[4].ToolCallID != "call_2" {
		t.Errorf("expected tool call ID 'call_2', got %q", m.Messages()[4].ToolCallID)
	}
}

func TestToolCallLoop_TextBeforeToolCall(t *testing.T) {
	executor := func(name string, args json.RawMessage) (string, error) {
		return "result", nil
	}

	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "hi"},
	}
	m := New(msgs, mockStream).WithToolExecutor(executor)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)
	m.state = stateStreaming
	m.streaming = "Let me check that."

	m, _ = execToolLoop(t, m, toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_1", Name: "read_file", Arguments: `{"path":"x.go"}`},
	}})

	// The assistant message should contain both text and tool calls
	assistant := m.Messages()[2]
	if assistant.Content != "Let me check that." {
		t.Errorf("expected assistant text preserved, got %q", assistant.Content)
	}
	if len(assistant.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(assistant.ToolCalls))
	}
}

func TestToolCallLoop_ExecutorError(t *testing.T) {
	executor := func(name string, args json.RawMessage) (string, error) {
		return "", errors.New("executor failed")
	}

	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "hi"},
	}
	m := New(msgs, mockStream).WithToolExecutor(executor)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)
	m.state = stateStreaming

	m, _ = execToolLoop(t, m, toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_1", Name: "bad_tool", Arguments: `{}`},
	}})

	// Tool result should contain error prefix
	toolResult := m.Messages()[3]
	if !strings.HasPrefix(toolResult.Content, "error:") {
		t.Errorf("expected error in tool result, got %q", toolResult.Content)
	}
}

func TestToolCallLoop_NoExecutor(t *testing.T) {
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "hi"},
	}
	m := New(msgs, mockStream)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)
	m.state = stateStreaming

	m, _ = execToolLoop(t, m, toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_1", Name: "read_file", Arguments: `{"path":"x"}`},
	}})

	toolResult := m.Messages()[3]
	if !strings.Contains(toolResult.Content, "no tool executor") {
		t.Errorf("expected 'no tool executor' error, got %q", toolResult.Content)
	}
}

func TestWaitForEvent_ToolCalls(t *testing.T) {
	ch := make(chan provider.StreamEvent, 1)
	ch <- provider.StreamEvent{
		ToolCalls: []provider.ToolCall{
			{ID: "call_1", Name: "search", Arguments: `{"pattern":"TODO"}`},
		},
		Done: true,
	}
	close(ch)

	cmd := waitForEvent(ch)
	msg := cmd()

	tcMsg, ok := msg.(toolCallsMsg)
	if !ok {
		t.Fatalf("expected toolCallsMsg, got %T", msg)
	}
	if len(tcMsg.calls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(tcMsg.calls))
	}
	if tcMsg.calls[0].Name != "search" {
		t.Errorf("expected name 'search', got %q", tcMsg.calls[0].Name)
	}
}

func TestResize_RewrapsHistory(t *testing.T) {
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	long := strings.Repeat("word ", 30) // ~150 chars, wraps differently at each width
	m.appendEntry(entry{kind: entryUser, text: strings.TrimSpace(long)})

	wide := m.renderHistory()
	updated, _ = m.Update(tea.WindowSizeMsg{Width: 40, Height: 30})
	m = updated.(Model)
	narrow := m.renderHistory()

	if wide == narrow {
		t.Fatal("history should re-wrap after resize")
	}
	for _, line := range strings.Split(narrow, "\n") {
		if lipglossWidth(line) > 40 {
			t.Fatalf("line exceeds narrow width after resize: %q", line)
		}
	}
}

// lipglossWidth is the widest visible line of s.
//
// It counts cells, not runes. Counting runes agreed with the answer only
// while the rows it measured were unstyled: once a row carries escapes —
// which every rendered row does — a thirty-column line of prose measures
// seventy and the check fails on the colour rather than on the width.
func lipglossWidth(s string) int {
	w := 0
	for _, line := range strings.Split(s, "\n") {
		if l := ansi.StringWidth(line); l > w {
			w = l
		}
	}
	return w
}

func TestWaitForEvent_BatchesBufferedTokens(t *testing.T) {
	ch := make(chan provider.StreamEvent, 4)
	ch <- provider.StreamEvent{Token: "a"}
	ch <- provider.StreamEvent{Token: "b"}
	ch <- provider.StreamEvent{Token: "c"}
	ch <- provider.StreamEvent{Done: true}
	close(ch)

	msg := waitForEvent(ch)()
	tm, ok := msg.(tokenMsg)
	if !ok {
		t.Fatalf("expected tokenMsg, got %T", msg)
	}
	if tm.text != "abc" {
		t.Fatalf("expected batched 'abc', got %q", tm.text)
	}
	if _, ok := tm.final.(doneMsg); !ok {
		t.Fatalf("expected final doneMsg carried with batch, got %T", tm.final)
	}
}

func TestWaitForEvent_BatchDeliveredThroughUpdate(t *testing.T) {
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)
	m.state = stateStreaming

	updated, _ = m.Update(tokenMsg{text: "abc", final: doneMsg{}})
	m = updated.(Model)

	if m.state != stateInput {
		t.Fatal("carried doneMsg should finish streaming")
	}
	if got := m.Messages()[len(m.Messages())-1].Content; got != "abc" {
		t.Fatalf("expected assistant content 'abc', got %q", got)
	}
}

func TestSlashCommand_NoDB_StillHandled(t *testing.T) {
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream)

	for _, cmd := range []string{"/save x", "/load x", "/chats"} {
		handled, result := m.handleSlashCommand(cmd)
		if !handled {
			t.Fatalf("%s should be handled even without a DB (not sent to the LLM)", cmd)
		}
		if !strings.Contains(result, "unavailable") {
			t.Fatalf("%s should report persistence unavailable, got %q", cmd, result)
		}
	}
}

func TestCancelDuringToolRun_IgnoresStaleResults(t *testing.T) {
	executor := func(name string, args json.RawMessage) (string, error) {
		return "slow result", nil
	}
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "hi"},
	}
	m := New(msgs, mockStream).WithToolExecutor(executor)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)
	m.state = stateStreaming

	updated, cmd := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_1", Name: "read_file", Arguments: `{"path":"x"}`},
	}})
	m = updated.(Model)

	// User cancels while the tool is still running: arm, then act.
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	m = updated.(Model)

	if m.state != stateInput {
		t.Fatal("cancel during tool run should return to input")
	}
	// Pending call must have a synthetic result so the conversation stays valid.
	last := m.Messages()[len(m.Messages())-1]
	if last.Role != provider.RoleTool || last.ToolCallID != "call_1" {
		t.Fatalf("expected synthetic tool result for pending call, got role=%s id=%s", last.Role, last.ToolCallID)
	}
	before := len(m.Messages())

	// The stale result arrives after cancel and must be ignored.
	updated, restream := m.Update(cmd())
	m = updated.(Model)
	if len(m.Messages()) != before {
		t.Fatal("stale tool results should be ignored after cancel")
	}
	if restream != nil {
		t.Fatal("stale tool results should not trigger a re-stream")
	}
}

func TestSlashHelp_ListsCommands(t *testing.T) {
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream)

	handled, result := m.handleSlashCommand("/help")
	if !handled {
		t.Fatal("/help should be handled")
	}
	for _, want := range []string{"/save", "/load", "/chats", "/clear", "/exit"} {
		if !strings.Contains(result, want) {
			t.Errorf("help should mention %s", want)
		}
	}
}

func TestSlashClear_ResetsConversation(t *testing.T) {
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "hi"},
		{Role: provider.RoleAssistant, Content: "hello"},
	}
	m := New(msgs, mockStream)
	m.appendEntry(entry{kind: entryUser, text: "hi"})
	m.appendEntry(entry{kind: entryAssistant, text: "hello"})
	m.contextTokens = 500

	handled, _ := m.handleSlashCommand("/clear")
	if !handled {
		t.Fatal("/clear should be handled")
	}
	if len(m.Messages()) != 1 || m.Messages()[0].Role != provider.RoleSystem {
		t.Fatalf("expected only system message to survive /clear, got %d messages", len(m.Messages()))
	}
	if len(m.transcript) != 0 {
		t.Fatal("transcript should be empty after /clear")
	}
	if m.contextTokens != 0 {
		t.Fatal("context estimate should reset after /clear")
	}
}

func TestSlashUnknown_Handled(t *testing.T) {
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream)

	handled, result := m.handleSlashCommand("/bogus")
	if !handled {
		t.Fatal("unknown slash command should be intercepted")
	}
	if !strings.Contains(result, "/help") {
		t.Fatalf("unknown-command message should point at /help, got %q", result)
	}

	// A path is not a command.
	if handled, _ := m.handleSlashCommand("/etc/hosts is weird"); handled {
		t.Fatal("a path should fall through to the LLM")
	}
}

func TestStatusBar_ShowsModelAndContext(t *testing.T) {
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream).WithPricing(nil, "gpt-4o")
	m.accumulateUsage(&provider.Usage{PromptTokens: 1200, CompletionTokens: 300})

	bar := m.renderStatusBar(120)
	if !strings.Contains(bar, "gpt-4o") {
		t.Error("status bar should show model name")
	}
	// No pricing table here, so the window comes from the model family
	//: 1500 of gpt-4o's 128k ≈ 1% on the context meter.
	if !strings.Contains(bar, "ctx ") || !strings.Contains(bar, "1%") {
		t.Errorf("status bar should show the context meter, got %q", bar)
	}
	if !strings.Contains(bar, "↑1.2k ↓300") {
		t.Errorf("status bar should show the usage segment, got %q", bar)
	}

	// Model name shows even before any usage arrives.
	empty := New(msgs, mockStream).WithPricing(nil, "gpt-4o")
	if !strings.Contains(empty.renderStatusBar(80), "gpt-4o") {
		t.Error("status bar should show model name before first response")
	}
}

func sendText(t *testing.T, m Model, text string) Model {
	t.Helper()
	m.input.SetValue(text)
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	return updated.(Model)
}

func TestInputHistory_RecallWithArrows(t *testing.T) {
	stream := multiTokenStream("ok")
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, stream)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)

	m = sendText(t, m, "first question")
	updated, _ = m.Update(doneMsg{})
	m = updated.(Model)
	m = sendText(t, m, "second question")
	updated, _ = m.Update(doneMsg{})
	m = updated.(Model)

	// Up recalls most recent first.
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	m = updated.(Model)
	if m.input.Value() != "second question" {
		t.Fatalf("expected 'second question', got %q", m.input.Value())
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	m = updated.(Model)
	if m.input.Value() != "first question" {
		t.Fatalf("expected 'first question', got %q", m.input.Value())
	}

	// Down walks forward and clears past the newest entry.
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = updated.(Model)
	if m.input.Value() != "second question" {
		t.Fatalf("expected 'second question' going down, got %q", m.input.Value())
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = updated.(Model)
	if m.input.Value() != "" {
		t.Fatalf("expected empty input past newest entry, got %q", m.input.Value())
	}
}

func TestInputHistory_UpIgnoredWhenDraftPresent(t *testing.T) {
	stream := multiTokenStream("ok")
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, stream)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)

	m = sendText(t, m, "earlier input")
	updated, _ = m.Update(doneMsg{})
	m = updated.(Model)

	m.input.SetValue("my draft")
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	m = updated.(Model)
	if m.input.Value() != "my draft" {
		t.Fatalf("up with a non-empty draft should not clobber it, got %q", m.input.Value())
	}
}

func TestEsc_ClearsInput(t *testing.T) {
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)

	m.input.SetValue("half-typed thought")
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)
	if m.input.Value() != "" {
		t.Fatalf("Esc should clear input, got %q", m.input.Value())
	}
}

func TestCtrlC_ClearsNonEmptyInputBeforeQuitting(t *testing.T) {
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)

	m.input.SetValue("oops")
	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	m = updated.(Model)
	if m.quitting {
		t.Fatal("Ctrl+C with a draft should clear it, not quit")
	}
	if m.input.Value() != "" {
		t.Fatalf("Ctrl+C should clear the draft, got %q", m.input.Value())
	}
	_ = cmd

	// The second press arms the quit window; the third quits.
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	m = updated.(Model)
	if m.quitting {
		t.Fatal("Ctrl+C on empty input should arm, not quit")
	}
	updated, cmd = m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	m = updated.(Model)
	if !m.quitting || cmd == nil {
		t.Fatal("Ctrl+C twice on empty input should quit")
	}
}

func runCapableModel(response string) Model {
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "how?"},
		{Role: provider.RoleAssistant, Content: response},
	}
	m := New(msgs, mockStream).WithRunner(func(ctx context.Context, cmd string) (string, int) {
		return "ran: " + cmd, 0
	})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	return updated.(Model)
}

func TestRun_SingleBlock_ConfirmAndExecute(t *testing.T) {
	m := runCapableModel("Do this:\n```bash\necho hi\n```")

	m = sendText(t, m, "/run")
	if m.state != stateConfirmRun {
		t.Fatalf("expected confirm state, got %d", m.state)
	}
	if m.pendingRun != "echo hi" {
		t.Fatalf("expected pending 'echo hi', got %q", m.pendingRun)
	}
	view := m.View().Content
	if !strings.Contains(view, "echo hi") || !strings.Contains(view, "[y/N]") {
		t.Fatal("confirm prompt should show the command and y/N")
	}

	m = handover(t, m)
	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m = updated.(Model)
	if m.state != stateRunningCmd {
		t.Fatalf("expected running state, got %d", m.state)
	}
	if cmd == nil {
		t.Fatal("expected run cmd")
	}

	var done cmdDoneMsg
	// tea.Batch wraps cmds; find the cmdDoneMsg among them.
	found := false
	for _, c := range unwrapBatch(cmd) {
		if msg, ok := c().(cmdDoneMsg); ok {
			done = msg
			found = true
		}
	}
	if !found {
		t.Fatal("expected cmdDoneMsg from run cmd")
	}
	if done.output != "ran: echo hi" || done.exitCode != 0 {
		t.Fatalf("unexpected run result: %+v", done)
	}

	updated, _ = m.Update(done)
	m = updated.(Model)
	if m.state != stateInput {
		t.Fatal("should return to input after command completes")
	}
	last := m.Messages()[len(m.Messages())-1]
	if last.Role != provider.RoleUser || !strings.Contains(last.Content, "ran: echo hi") {
		t.Fatalf("command output should be recorded in conversation, got %+v", last)
	}
	if !strings.Contains(last.Content, "Exit code: 0") {
		t.Fatal("conversation record should include the exit code")
	}
}

// cmdMsg runs cmd and returns the one message the test is about. Update
// batches the spinner's tick alongside whatever a transition into a working
// state produced (spin.go), so a caller that wants the work has to
// look past the tick — which is never what a test is asserting.
func cmdMsg(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	var found []tea.Msg
	var walk func(tea.Cmd)
	walk = func(c tea.Cmd) {
		if c == nil {
			return
		}
		switch msg := c().(type) {
		case tea.BatchMsg:
			for _, sub := range msg {
				walk(sub)
			}
		case spinner.TickMsg:
		case nil:
		default:
			found = append(found, msg)
		}
	}
	walk(cmd)
	if len(found) != 1 {
		t.Fatalf("expected one message from the cmd, got %d", len(found))
	}
	return found[0]
}

func unwrapBatch(cmd tea.Cmd) []tea.Cmd {
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		return batch
	}
	return []tea.Cmd{func() tea.Msg { return msg }}
}

func TestRun_Declined(t *testing.T) {
	m := runCapableModel("```bash\nrm -rf /tmp/x\n```")
	m = sendText(t, m, "/run")
	m = handover(t, m)
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	m = updated.(Model)

	if m.state != stateInput {
		t.Fatal("declining should return to input")
	}
	if m.pendingRun != "" {
		t.Fatal("pending command should be cleared")
	}
	// Nothing was appended to the conversation.
	if len(m.Messages()) != 3 {
		t.Fatalf("declined run should not touch messages, got %d", len(m.Messages()))
	}
}

func TestRun_ConfirmShowsSafetyWarning(t *testing.T) {
	// A flagged command pointed inside the test's own scratch directory. The
	// card measures what a command writes, so a fixture naming the machine's
	// root would walk the real filesystem — twenty thousand entries, and a
	// different set of them every run.
	m := runCapableModel("```bash\nrm -rf ./build\n```").WithWorkspace(t.TempDir())
	m = sendText(t, m, "/run")
	if m.state != stateConfirmRun {
		t.Fatalf("expected confirm state, got %d", m.state)
	}
	if !strings.Contains(m.View().Content, "⚠") {
		t.Fatal("dangerous command should show a safety warning in the confirm prompt")
	}
}

func TestRun_MultipleBlocks_NumberedFormIsDirect(t *testing.T) {
	m := runCapableModel("First:\n```bash\necho one\n```\nSecond:\n```bash\necho two\n```")

	m = sendText(t, m, "/run 2")
	if m.state != stateConfirmRun || m.pendingRun != "echo two" {
		t.Fatalf("expected confirm of 'echo two', got state=%d pending=%q", m.state, m.pendingRun)
	}
}

func TestRun_NoBlocks(t *testing.T) {
	m := runCapableModel("no code here, sorry")
	m = sendText(t, m, "/run")
	if m.state != stateInput {
		t.Fatal("should stay in input state")
	}
	if len(m.transcript) == 0 || !strings.Contains(m.transcript[len(m.transcript)-1].text, "No code blocks") {
		t.Fatal("expected 'No code blocks' message")
	}
}

func TestRun_NoRunner(t *testing.T) {
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleAssistant, Content: "```bash\nls\n```"},
	}
	m := New(msgs, mockStream)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)

	m = sendText(t, m, "/run")
	if m.state != stateInput {
		t.Fatal("should stay in input state without a runner")
	}
	if len(m.transcript) == 0 || !strings.Contains(m.transcript[len(m.transcript)-1].text, "not available") {
		t.Fatal("expected 'not available' message")
	}
}

func TestExecTool_ApprovalFlow(t *testing.T) {
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "create a file"},
	}
	m := New(msgs, mockStream).WithRunner(func(ctx context.Context, cmd string) (string, int) {
		return "done: " + cmd, 0
	})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)
	m.state = stateStreaming

	updated, _ = m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_1", Name: "execute_command", Arguments: `{"command":"touch /tmp/x"}`},
	}})
	m = updated.(Model)

	if m.state != stateConfirmRun {
		t.Fatalf("exec tool call should enter confirm state, got %d", m.state)
	}
	if !strings.Contains(m.View().Content, "Assistant wants to run") {
		t.Fatal("confirm prompt should say the assistant is asking")
	}

	// Approve.
	m = handover(t, m)
	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m = updated.(Model)
	if m.state != stateRunningCmd {
		t.Fatalf("expected running state, got %d", m.state)
	}
	var done cmdDoneMsg
	for _, c := range unwrapBatch(cmd) {
		if msg, ok := c().(cmdDoneMsg); ok {
			done = msg
		}
	}
	updated, restream := m.Update(done)
	m = updated.(Model)

	// Result recorded as a tool message, then the stream resumes.
	var toolMsg *provider.Message
	for i := range m.Messages() {
		if m.Messages()[i].Role == provider.RoleTool {
			toolMsg = &m.Messages()[i]
		}
	}
	if toolMsg == nil || toolMsg.ToolCallID != "call_1" {
		t.Fatal("expected tool result message for call_1")
	}
	if !strings.Contains(toolMsg.Content, "done: touch /tmp/x") || !strings.Contains(toolMsg.Content, "exit code: 0") {
		t.Fatalf("tool result should carry output and exit code, got %q", toolMsg.Content)
	}
	if m.state != stateStreaming {
		t.Fatalf("expected stream to resume after tool result, got state %d", m.state)
	}
	if restream == nil {
		t.Fatal("expected re-stream cmd after exec tool completes")
	}
}

func TestExecTool_Declined(t *testing.T) {
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "wipe it"},
	}
	m := New(msgs, mockStream).WithWorkspace(t.TempDir()).
		WithRunner(func(ctx context.Context, cmd string) (string, int) {
			t.Fatal("runner must not be called on decline")
			return "", 0
		})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)
	m.state = stateStreaming

	updated, _ = m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_1", Name: "execute_command", Arguments: `{"command":"rm -rf ./build"}`},
	}})
	m = updated.(Model)

	m = handover(t, m)
	updated, restream := m.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	m = updated.(Model)

	last := m.Messages()[len(m.Messages())-1]
	if last.Role != provider.RoleTool || !strings.Contains(last.Content, "declined") {
		t.Fatalf("declined call should produce an error tool result, got %+v", last)
	}
	if m.state != stateStreaming || restream == nil {
		t.Fatal("stream should resume after decline so the model can react")
	}
}

func TestExecTool_MixedWithReadOnly(t *testing.T) {
	executor := func(name string, args json.RawMessage) (string, error) {
		return "read result", nil
	}
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "check then fix"},
	}
	m := New(msgs, mockStream).WithToolExecutor(executor).
		WithRunner(func(ctx context.Context, cmd string) (string, int) { return "ok", 0 })
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)
	m.state = stateStreaming

	updated, cmd := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_r", Name: "read_file", Arguments: `{"path":"a.go"}`},
		{ID: "call_x", Name: "execute_command", Arguments: `{"command":"echo hi"}`},
	}})
	m = updated.(Model)

	// Read-only tools run first, asynchronously.
	if !m.agent.Executing() {
		t.Fatal("read-only calls should run first")
	}
	res := cmdMsg(t, cmd)
	updated, _ = m.Update(res)
	m = updated.(Model)

	// Then the exec call asks for approval.
	if m.state != stateConfirmRun || m.pendingRun != "echo hi" {
		t.Fatalf("expected confirm for exec call after read-only results, got state=%d pending=%q", m.state, m.pendingRun)
	}
	// The read-only result must already be recorded for call_r.
	foundRead := false
	for _, msg := range m.Messages() {
		if msg.Role == provider.RoleTool && msg.ToolCallID == "call_r" && msg.Content == "read result" {
			foundRead = true
		}
	}
	if !foundRead {
		t.Fatal("read-only tool result should be recorded before approval prompt")
	}
}

func TestExecTool_InvalidArgsSkipped(t *testing.T) {
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "go"},
	}
	m := New(msgs, mockStream).WithRunner(func(ctx context.Context, cmd string) (string, int) { return "", 0 })
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)
	m.state = stateStreaming

	updated, restream := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_1", Name: "execute_command", Arguments: `not json`},
	}})
	m = updated.(Model)

	last := m.Messages()[len(m.Messages())-1]
	if last.Role != provider.RoleTool || !strings.Contains(last.Content, "invalid") {
		t.Fatalf("invalid args should produce an error tool result, got %+v", last)
	}
	if m.state != stateStreaming || restream == nil {
		t.Fatal("stream should resume after skipping the invalid call")
	}
}

func TestAutosave_AfterExchange(t *testing.T) {
	db, err := storage.OpenPath(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, multiTokenStream("hi there")).WithDB(db)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)

	m = sendText(t, m, "hello")
	updated, _ = m.Update(tokenMsg{text: "hi there"})
	m = updated.(Model)
	updated, save := m.Update(doneMsg{})
	m = updated.(Model)

	if save == nil {
		t.Fatal("doneMsg should return an autosave cmd")
	}
	save() // run the autosave

	saved, err := db.LoadChat(m.sessionName)
	if err != nil {
		t.Fatalf("expected autosave slot to exist: %v", err)
	}
	if len(saved) != 3 || saved[2].Content != "hi there" {
		t.Fatalf("autosave should contain the full exchange, got %d messages", len(saved))
	}
}

// Two sessions must not share a slot: the autosave used to write every
// session to one reserved name, so starting a second session erased the
// first and --resume only ever listed one.
func TestAutosave_EachSessionKeepsItsOwnSlot(t *testing.T) {
	db, err := storage.OpenPath(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	sys := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	run := func(m Model, user, reply string) Model {
		m = sendText(t, m, user)
		updated, _ := m.Update(tokenMsg{text: reply})
		updated, save := updated.(Model).Update(doneMsg{})
		if save == nil {
			t.Fatal("doneMsg should return an autosave cmd")
		}
		save()
		return updated.(Model)
	}
	first := run(New(sys, mockStream).WithDB(db), "first session", "one")
	second := run(New(sys, mockStream).WithDB(db), "second session", "two")

	if first.sessionName == second.sessionName {
		t.Fatalf("two sessions were given the same slot %q", first.sessionName)
	}
	entries, err := db.ListChats()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("both sessions should be saved, got %d", len(entries))
	}
	recent, ok, err := db.MostRecentChat()
	if err != nil || !ok || recent.Name != second.sessionName {
		t.Fatalf("--continue should find the newest session %q, got %q (ok=%v, err=%v)",
			second.sessionName, recent.Name, ok, err)
	}
	if kept, err := db.LoadChat(first.sessionName); err != nil || len(kept) != 3 {
		t.Fatalf("the first session should still be whole, got %d messages, err=%v", len(kept), err)
	}

	// A resumed session keeps growing in the slot it came from, not a copy.
	resumed := New(sys, mockStream).WithDB(db).WithResumedMessages(first.sessionName, first.Messages())
	if resumed.sessionName != first.sessionName {
		t.Fatalf("resume should keep the slot %q, got %q", first.sessionName, resumed.sessionName)
	}

	// /clear moves to a fresh slot rather than overwriting the one just left.
	cleared := second
	cleared.handleSlashCommand("/clear")
	if cleared.sessionName == second.sessionName {
		t.Fatal("/clear should start a new slot")
	}
}

// A slot the session did not write into is not one it may empty: the
// conversation in there belongs to whichever session got the name first, so
// this one takes a slot of its own and says so rather than going unsaved.
func TestAutosave_RefusedSlotMovesTheSessionRatherThanClobbering(t *testing.T) {
	path := t.TempDir() + "/test.db"
	db, err := storage.OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	other, err := storage.OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()

	sys := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := sendText(t, New(sys, mockStream).WithDB(db), "first question")
	updated, _ := m.Update(tokenMsg{text: "an answer"})
	updated, save := updated.(Model).Update(doneMsg{})
	if save == nil {
		t.Fatal("doneMsg should return an autosave cmd")
	}
	save()
	m = updated.(Model)
	taken := m.sessionName

	// Another session takes the slot over and grows it past where this one
	// left off.
	if _, err := other.LoadChat(taken); err != nil {
		t.Fatalf("load: %v", err)
	}
	theirs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "theirs"},
		{Role: provider.RoleAssistant, Content: "answered"},
		{Role: provider.RoleUser, Content: "and more"},
	}
	if err := other.SaveChat(taken, theirs); err != nil {
		t.Fatalf("save: %v", err)
	}

	refusal := m.autosaveCmd()()
	move, ok := refusal.(autosaveMovedMsg)
	if !ok {
		t.Fatalf("the autosave should have moved the conversation, got %T", refusal)
	}
	if move.from != taken || move.to == taken {
		t.Fatalf("the move should leave %q for a slot of its own, got %+v", taken, move)
	}
	moved, _ := m.Update(refusal)
	m = moved.(Model)
	if m.sessionName != move.to {
		t.Fatalf("the session should have followed the conversation to %q, got %q", move.to, m.sessionName)
	}

	// The session is in its own slot now, so the saves that follow are
	// ordinary ones.
	if second, isMove := m.autosaveCmd()().(autosaveMovedMsg); isMove {
		t.Fatalf("a save from the new slot should not move again, got %+v", second)
	}

	var notice string
	for _, e := range m.transcript {
		if e.kind == entrySystem && strings.Contains(e.text, taken) {
			notice = e.text
		}
	}
	if notice == "" {
		t.Fatal("the move should leave one row naming the slot it left")
	}
	if !strings.Contains(notice, move.to) {
		t.Fatalf("the row should name the slot the conversation moved to: %q", notice)
	}

	kept, err := other.LoadChat(taken)
	if err != nil || len(kept) != len(theirs) {
		t.Fatalf("the other session's conversation should be intact, got %d messages (err=%v)", len(kept), err)
	}
	mine, err := db.LoadChat(m.sessionName)
	if err != nil || len(mine) != 3 {
		t.Fatalf("this conversation should be in its new slot, got %d messages (err=%v)", len(mine), err)
	}
}

func TestAutosave_NilWithoutDBOrContent(t *testing.T) {
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream)
	if m.autosaveCmd() != nil {
		t.Fatal("no DB → no autosave cmd")
	}

	db, err := storage.OpenPath(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	m = m.WithDB(db)
	if m.autosaveCmd() != nil {
		t.Fatal("system-prompt-only conversation should not autosave")
	}
}

func TestWithResumedMessages_RebuildsTranscript(t *testing.T) {
	saved := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "old question"},
		{Role: provider.RoleAssistant, Content: "old answer"},
	}
	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream).
		WithResumedMessages("", saved)

	if len(m.Messages()) != 3 {
		t.Fatalf("expected 3 resumed messages, got %d", len(m.Messages()))
	}
	if len(m.transcript) != 2 {
		t.Fatalf("expected 2 transcript entries (user+assistant), got %d", len(m.transcript))
	}
	m.width = 100
	history := stripANSI(m.renderHistory())
	if !strings.Contains(history, "old question") || !strings.Contains(history, "old answer") {
		t.Fatal("resumed conversation should render in history")
	}
}

func TestSlashLoad_NoArg_ListsChats(t *testing.T) {
	db, err := storage.OpenPath(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.SaveChat("my-session", []provider.Message{{Role: provider.RoleUser, Content: "x"}}); err != nil {
		t.Fatal(err)
	}

	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream).WithDB(db)

	handled, result := m.handleSlashCommand("/load")
	if !handled {
		t.Fatal("/load should be handled")
	}
	if !strings.Contains(result, "my-session") || !strings.Contains(result, "Usage: /load <name>") {
		t.Fatalf("bare /load should list chats with usage hint, got %q", result)
	}
}

func TestSlashModel_ShowsCurrentModel(t *testing.T) {
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream).WithPricing(nil, "gpt-4o")

	handled, result := m.handleSlashCommand("/model")
	if !handled {
		t.Fatal("/model should be handled")
	}
	if !strings.Contains(result, "gpt-4o") || !strings.Contains(result, "Usage: /model <name>") {
		t.Fatalf("bare /model should show current model and usage, got %q", result)
	}
}

func TestSlashModel_Switches(t *testing.T) {
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	var switched string
	m := New(msgs, mockStream).
		WithPricing(nil, "gpt-4o").
		WithModelSwitcher(func(name string) { switched = name })

	handled, result := m.handleSlashCommand("/model claude-opus-5")
	if !handled || !strings.Contains(result, "Switched model to claude-opus-5") {
		t.Fatalf("expected switch confirmation, got handled=%v result=%q", handled, result)
	}
	if switched != "claude-opus-5" {
		t.Fatalf("switch callback should receive the new model, got %q", switched)
	}
	if m.modelName != "claude-opus-5" {
		t.Fatalf("status-bar model name should update, got %q", m.modelName)
	}
	if !strings.Contains(m.renderStatusBar(80), "claude-opus-5") {
		t.Fatal("status bar should show the new model")
	}
}

func TestSlashModel_EdgeCases(t *testing.T) {
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}

	// No switcher configured.
	m := New(msgs, mockStream).WithPricing(nil, "gpt-4o")
	if handled, result := m.handleSlashCommand("/model gpt-5"); !handled || !strings.Contains(result, "not available") {
		t.Fatalf("expected 'not available' without a switcher, got %q", result)
	}
	if m.modelName != "gpt-4o" {
		t.Fatal("model name must not change without a switcher")
	}

	calls := 0
	m = m.WithModelSwitcher(func(string) { calls++ })

	// Same model is a no-op.
	if _, result := m.handleSlashCommand("/model gpt-4o"); !strings.Contains(result, "Already using") {
		t.Fatalf("expected 'Already using', got %q", result)
	}
	if calls != 0 {
		t.Fatal("switching to the current model should not invoke the callback")
	}

	// Spaces are rejected.
	if _, result := m.handleSlashCommand("/model two words"); !strings.Contains(result, "cannot contain spaces") {
		t.Fatalf("expected spaces rejection, got %q", result)
	}
	if calls != 0 {
		t.Fatal("invalid input should not invoke the callback")
	}
}

func TestMaxToolRounds_Default(t *testing.T) {
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream)
	if got := m.effectiveMaxToolRounds(); got != DefaultMaxToolRounds {
		t.Fatalf("default cap should be %d, got %d", DefaultMaxToolRounds, got)
	}
	if got := m.WithMaxToolRounds(0).effectiveMaxToolRounds(); got != DefaultMaxToolRounds {
		t.Fatalf("zero should keep the default cap, got %d", got)
	}
	if got := m.WithMaxToolRounds(5).effectiveMaxToolRounds(); got != 5 {
		t.Fatalf("configured cap should win, got %d", got)
	}
}

func TestToolLoop_StopsAtRoundCap(t *testing.T) {
	executor := func(name string, args json.RawMessage) (string, error) {
		return "result", nil
	}
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "go"},
	}
	m := New(msgs, mockStream).WithToolExecutor(executor).WithMaxToolRounds(2)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)
	m.state = stateStreaming

	// Round 1: under the cap, the loop re-streams.
	var cmd tea.Cmd
	m, cmd = execToolLoop(t, m, toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_1", Name: "read_file", Arguments: `{"path":"a.go"}`},
	}})
	if m.state != stateStreaming {
		t.Fatalf("round 1 should stay streaming, got state %d", m.state)
	}
	if cmd == nil {
		t.Fatal("round 1 should return a re-stream cmd")
	}

	// Round 2 hits the cap: the loop pauses and control returns to the user.
	m, cmd = execToolLoop(t, m, toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_2", Name: "read_file", Arguments: `{"path":"b.go"}`},
	}})
	if m.state != stateInput {
		t.Fatalf("cap should return control to the user, got state %d", m.state)
	}
	if cmd != nil {
		t.Fatal("no re-stream cmd at the cap (autosave is nil without a DB)")
	}
	last := m.Messages()[len(m.Messages())-1]
	if last.Role != provider.RoleTool || last.ToolCallID != "call_2" {
		t.Fatalf("conversation must stay well-formed at the cap, last message: %+v", last)
	}
	// The cap is a checkpoint, not a notice: the turn closes on the `rounds`
	// row that says what it managed and offers the ways on.
	pause := m.transcript[len(m.transcript)-1]
	if pause.kind != entryRoundPause || pause.pause == nil {
		t.Fatalf("expected a round-limit pause row, got %+v", pause)
	}
	if pause.pause.used != 2 || pause.pause.limit != 2 {
		t.Fatalf("pause should report 2 of 2 used, got %d of %d", pause.pause.used, pause.pause.limit)
	}

	// A fresh user message resets the counter and streams again.
	m = sendText(t, m, "continue")
	if m.agent.Rounds() != 0 {
		t.Fatalf("fresh input should reset the round counter, got %d", m.agent.Rounds())
	}
	if m.state != stateStreaming {
		t.Fatalf("fresh input should resume streaming, got state %d", m.state)
	}
}

func TestToolLoop_RoundCapAfterApprovedCommand(t *testing.T) {
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "list files"},
	}
	m := New(msgs, mockStream).
		WithRunner(func(ctx context.Context, cmd string) (string, int) { return "ok", 0 }).
		WithMaxToolRounds(1)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)
	m.state = stateStreaming

	updated, _ = m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_1", Name: "execute_command", Arguments: `{"command":"go build ./..."}`},
	}})
	m = updated.(Model)
	if m.state != stateConfirmRun {
		t.Fatalf("expected confirm state, got %d", m.state)
	}

	m = handover(t, m)
	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m = updated.(Model)
	var done cmdDoneMsg
	for _, c := range unwrapBatch(cmd) {
		if msg, ok := c().(cmdDoneMsg); ok {
			done = msg
		}
	}
	updated, restream := m.Update(done)
	m = updated.(Model)

	if m.state != stateInput {
		t.Fatalf("cap should pause the loop after the approved command, got state %d", m.state)
	}
	if restream != nil {
		t.Fatal("no re-stream cmd at the cap (autosave is nil without a DB)")
	}
	last := m.Messages()[len(m.Messages())-1]
	if last.Role != provider.RoleTool || last.ToolCallID != "call_1" {
		t.Fatalf("expected the command's tool result last, got %+v", last)
	}
}

func TestStatusBar_ShowsRoundCounter(t *testing.T) {
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream)
	for i := 0; i < 7; i++ {
		m.agent.BeginToolRound("", nil, nil)
	}
	m.state = stateStreaming
	if !strings.Contains(m.renderStatusBar(80), "round 7") {
		t.Fatal("status bar should show the round counter mid-turn")
	}
	m.state = stateInput
	if strings.Contains(m.renderStatusBar(80), "round") {
		t.Fatal("status bar should hide the round counter between turns")
	}
}

// steeringModel is a streaming-state model ready to receive steering input.
func steeringModel(t *testing.T, stream StreamFunc) Model {
	t.Helper()
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, stream)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)
	return sendText(t, m, "do the task")
}

func TestSteering_EnterQueuesWhileStreaming(t *testing.T) {
	m := steeringModel(t, mockStream)
	if m.state != stateStreaming {
		t.Fatalf("expected stateStreaming, got %d", m.state)
	}

	before := len(m.Messages())
	m = sendText(t, m, "also update the docs")

	if len(m.steering) != 1 || m.steering[0] != "also update the docs" {
		t.Fatalf("expected one queued steering message, got %v", m.steering)
	}
	if m.input.Value() != "" {
		t.Fatal("queueing should clear the input")
	}
	if len(m.Messages()) != before {
		t.Fatal("queued steering must not join the conversation until the round completes")
	}
	if !strings.Contains(m.renderStatusBar(80), "queued 1") {
		t.Fatal("status bar should show the queued steering count")
	}
}

func TestSteering_InputStaysLiveWhileStreaming(t *testing.T) {
	m := steeringModel(t, mockStream)

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'h', Text: "hi"})
	m = updated.(Model)

	if m.input.Value() != "hi" {
		t.Fatalf("input should accept typing while streaming, got %q", m.input.Value())
	}
}

func TestSteering_InjectedBeforeNextStreamRequest(t *testing.T) {
	executor := func(name string, args json.RawMessage) (string, error) {
		return "result", nil
	}
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "go"},
	}
	m := New(msgs, mockStream).WithToolExecutor(executor)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)
	m.state = stateStreaming
	m.steering = []string{"focus on the tests"}

	var cmd tea.Cmd
	m, cmd = execToolLoop(t, m, toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_1", Name: "read_file", Arguments: `{"path":"a.go"}`},
	}})

	// system, user, assistant(tool call), tool result, steering user message
	last := m.Messages()[len(m.Messages())-1]
	if last.Role != provider.RoleUser || last.Content != "focus on the tests" {
		t.Fatalf("steering should be injected as a user message after the tool round, got %+v", last)
	}
	if m.Messages()[len(m.Messages())-2].Role != provider.RoleTool {
		t.Fatal("steering must come after the round's tool results")
	}
	if len(m.steering) != 0 {
		t.Fatal("queue should be empty after injection")
	}
	if m.agent.Rounds() != 0 {
		t.Fatalf("steering is fresh user input and should reset the round counter, got %d", m.agent.Rounds())
	}
	if m.state != stateStreaming || cmd == nil {
		t.Fatal("the loop should continue streaming with the steering message in context")
	}
	if m.transcript[len(m.transcript)-1].kind != entryUser {
		t.Fatal("the injected steering message should appear in the transcript")
	}
}

func TestSteering_LiftsRoundCap(t *testing.T) {
	executor := func(name string, args json.RawMessage) (string, error) {
		return "result", nil
	}
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "go"},
	}
	m := New(msgs, mockStream).WithToolExecutor(executor).WithMaxToolRounds(1)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)
	m.state = stateStreaming
	m.steering = []string{"keep going"}

	var cmd tea.Cmd
	m, cmd = execToolLoop(t, m, toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_1", Name: "read_file", Arguments: `{"path":"a.go"}`},
	}})

	if m.state != stateStreaming || cmd == nil {
		t.Fatalf("steering should lift the round cap and keep the loop going, got state %d", m.state)
	}
}

func TestSteering_SentAfterDone(t *testing.T) {
	m := steeringModel(t, multiTokenStream("ok"))
	m = sendText(t, m, "one more thing")

	updated, cmd := m.Update(doneMsg{})
	m = updated.(Model)

	if m.state != stateStreaming {
		t.Fatalf("queued steering should start the next turn after done, got state %d", m.state)
	}
	if cmd == nil {
		t.Fatal("expected a stream request cmd for the steering turn")
	}
	last := m.Messages()[len(m.Messages())-1]
	if last.Role != provider.RoleUser || last.Content != "one more thing" {
		t.Fatalf("expected the steering message as the new user turn, got %+v", last)
	}
}

func TestSteering_CtrlCRestoresQueueToInput(t *testing.T) {
	m := steeringModel(t, mockStream)
	m = sendText(t, m, "queued while working")

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	m = updated.(Model)

	if m.state != stateInput {
		t.Fatal("Ctrl+C twice must keep its hard-cancel semantics")
	}
	if len(m.steering) != 0 {
		t.Fatal("cancel should drain the steering queue")
	}
	if m.input.Value() != "queued while working" {
		t.Fatalf("cancel should return the queued text to the input, got %q", m.input.Value())
	}
}

func TestSteering_StreamErrorRestoresQueueToInput(t *testing.T) {
	m := steeringModel(t, mockStream)
	m = sendText(t, m, "queued while working")

	updated, _ := m.Update(streamErrMsg{err: errors.New("boom")})
	m = updated.(Model)

	if m.state != stateInput {
		t.Fatal("stream error should return to input")
	}
	if m.input.Value() != "queued while working" {
		t.Fatalf("stream error should return the queued text to the input, got %q", m.input.Value())
	}
}

func TestSteering_SlashCommandNotQueued(t *testing.T) {
	m := steeringModel(t, mockStream)
	m = sendText(t, m, "/compact")

	if len(m.steering) != 0 {
		t.Fatalf("slash commands must not queue as steering, got %v", m.steering)
	}
	notice := m.transcript[len(m.transcript)-1]
	if notice.kind != entrySystem || !strings.Contains(notice.text, "/compact") {
		t.Fatalf("expected a system notice about the rejected command, got %+v", notice)
	}
}

func TestSteering_QuitCommandStillQuits(t *testing.T) {
	m := steeringModel(t, mockStream)
	m = sendText(t, m, "/exit")

	// The agent is working, so /exit asks before throwing the turn away.
	if m.quitting {
		t.Fatal("/exit over a live turn should ask, not quit")
	}
	if m.state != stateQuitConfirm {
		t.Fatalf("/exit over a live turn should open the quit confirm, got state %d", m.state)
	}
	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m = updated.(Model)
	if !m.quitting || cmd == nil {
		t.Fatal("[y] on the quit confirm should quit")
	}
}

// The exit banner names the slot the autosave actually wrote, counts
// the whole conversation, and offers the front-end's own resume command.
func TestExitBanner_NamesTheSlotTheAutosaveWrote(t *testing.T) {
	db, err := storage.OpenPath(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "one"},
		{Role: provider.RoleAssistant, Content: "ok"},
		{Role: provider.RoleUser, Content: "two"},
		{Role: provider.RoleAssistant, Content: "ok"},
	}
	m := New(msgs, mockStream).WithDB(db)

	b := m.ExitBanner("shhh code --continue")
	if b.Session != m.sessionName {
		t.Fatalf("banner should name the session's own slot, got %q", b.Session)
	}
	if b.Turns != 2 {
		t.Fatalf("banner should count both user turns, got %d", b.Turns)
	}
	if b.Resume != "shhh code --continue" {
		t.Fatalf("banner should carry the front-end's resume command, got %q", b.Resume)
	}
	if b.Unsaved {
		t.Fatal("a session with a store behind it is not unsaved")
	}
}

// Without a store there is nothing to reopen, and the banner says so rather
// than pointing at a slot holding somebody else's conversation. The condition
// is autosaveCmd's, so the two can never disagree.
func TestExitBanner_NoStoreIsUnsaved(t *testing.T) {
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "one"},
	}
	m := New(msgs, mockStream)

	b := m.ExitBanner("shhh chat --continue")
	if !b.Unsaved {
		t.Fatal("no store → nothing was saved")
	}
	if b.Session != "" || b.Resume != "" {
		t.Fatalf("an unsaved session names no slot and no command, got %q / %q", b.Session, b.Resume)
	}
	if m.autosaveCmd() != nil {
		t.Fatal("the banner and the autosave must agree about there being nothing to write")
	}
}

// A session that never said anything renders nothing at all.
func TestExitBanner_NothingSaidHasNoTurns(t *testing.T) {
	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream)
	if b := m.ExitBanner("shhh chat --continue"); b.Turns != 0 || b.View(80) != "" {
		t.Fatalf("an empty session should render no banner, got %q", b.View(80))
	}
}
