package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/provider"
)

// scriptedStream returns a StreamFunc that serves one scripted event slice
// per request, failing the test if more requests arrive than scripted.
func scriptedStream(t *testing.T, rounds ...[]provider.StreamEvent) StreamFunc {
	t.Helper()
	i := 0
	return func([]provider.Message) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		if i >= len(rounds) {
			t.Fatalf("unexpected stream request #%d", i+1)
		}
		evs := rounds[i]
		i++
		ch := make(chan provider.StreamEvent, len(evs))
		for _, ev := range evs {
			ch <- ev
		}
		close(ch)
		_, cancel := context.WithCancel(context.Background())
		return ch, cancel, nil
	}
}

func toolCallRound(calls ...provider.ToolCall) []provider.StreamEvent {
	return []provider.StreamEvent{{ToolCalls: calls}}
}

func doneRound(text string) []provider.StreamEvent {
	return []provider.StreamEvent{{Token: text}, {Done: true}}
}

func TestHeadlessRun_PlainResponse(t *testing.T) {
	a := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}},
		scriptedStream(t, []provider.StreamEvent{{Token: "hello "}, {Token: "world"}, {Done: true}}))

	var streamed strings.Builder
	h := &Headless{Agent: a, OnText: func(s string) { streamed.WriteString(s) }}

	final, err := h.Run("hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if final != "hello world" {
		t.Fatalf("final = %q, want %q", final, "hello world")
	}
	if streamed.String() != final {
		t.Fatalf("OnText streamed %q, want %q", streamed.String(), final)
	}
	msgs := a.Messages()
	last := msgs[len(msgs)-1]
	if last.Role != provider.RoleAssistant || last.Content != "hello world" {
		t.Fatalf("final assistant message not recorded, got %+v", last)
	}
}

func TestHeadlessRun_AutoToolRound(t *testing.T) {
	a := New(nil, scriptedStream(t,
		toolCallRound(provider.ToolCall{ID: "c1", Name: "read_file", Arguments: `{"path":"x"}`}),
		doneRound("done"),
	))
	a.SetExecutor(func(name string, args json.RawMessage) (string, error) {
		if name != "read_file" {
			t.Fatalf("executor got tool %q", name)
		}
		return "contents", nil
	})

	var calls, results []string
	h := &Headless{
		Agent:        a,
		OnToolCall:   func(tc provider.ToolCall) { calls = append(calls, tc.Name) },
		OnToolResult: func(tc provider.ToolCall, r string) { results = append(results, r) },
	}

	final, err := h.Run("go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if final != "done" {
		t.Fatalf("final = %q, want %q", final, "done")
	}
	if len(calls) != 1 || calls[0] != "read_file" {
		t.Fatalf("OnToolCall calls = %v", calls)
	}
	if len(results) != 1 || results[0] != "contents" {
		t.Fatalf("OnToolResult results = %v", results)
	}
	var toolMsg *provider.Message
	for i := range a.Messages() {
		if a.Messages()[i].Role == provider.RoleTool {
			toolMsg = &a.Messages()[i]
		}
	}
	if toolMsg == nil || toolMsg.Content != "contents" || toolMsg.ToolCallID != "c1" {
		t.Fatalf("tool result message not recorded, got %+v", toolMsg)
	}
}

func TestHeadlessRun_GatedCallResolved(t *testing.T) {
	a := New(nil, scriptedStream(t,
		toolCallRound(provider.ToolCall{ID: "c1", Name: "execute_command", Arguments: `{"command":"ls"}`}),
		doneRound("ok"),
	))

	h := &Headless{
		Agent: a,
		Gate:  func(name string) bool { return name == "execute_command" },
		Resolve: func(tc provider.ToolCall) string {
			return "error: the user declined this tool call"
		},
	}

	final, err := h.Run("go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if final != "ok" {
		t.Fatalf("final = %q, want %q", final, "ok")
	}
	var toolMsg provider.Message
	for _, m := range a.Messages() {
		if m.Role == provider.RoleTool {
			toolMsg = m
		}
	}
	if toolMsg.ToolCallID != "c1" || !strings.HasPrefix(toolMsg.Content, "error:") {
		t.Fatalf("gated result not recorded as decline, got %+v", toolMsg)
	}
}

func TestHeadlessRun_NilResolveDeclines(t *testing.T) {
	a := New(nil, scriptedStream(t,
		toolCallRound(provider.ToolCall{ID: "c1", Name: "write_file"}),
		doneRound("ok"),
	))
	h := &Headless{Agent: a, Gate: func(string) bool { return true }}

	if _, err := h.Run("go"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, m := range a.Messages() {
		if m.Role == provider.RoleTool && !strings.Contains(m.Content, "cannot be approved") {
			t.Fatalf("nil Resolve should decline, got %q", m.Content)
		}
	}
}

func TestHeadlessRun_RoundCap(t *testing.T) {
	// Every stream requests another tool round; with a cap of 2 the run must
	// stop with ErrRoundCap after the second round's results are recorded.
	rounds := [][]provider.StreamEvent{
		toolCallRound(provider.ToolCall{ID: "c1", Name: "search"}),
		toolCallRound(provider.ToolCall{ID: "c2", Name: "search"}),
	}
	a := New(nil, scriptedStream(t, rounds...))
	a.SetExecutor(func(string, json.RawMessage) (string, error) { return "r", nil })
	a.SetMaxRounds(2)

	h := &Headless{Agent: a}
	_, err := h.Run("go")
	if !errors.Is(err, ErrRoundCap) {
		t.Fatalf("expected ErrRoundCap, got %v", err)
	}
	// Both rounds' calls must still have recorded results so the conversation
	// stays well-formed.
	var toolResults int
	for _, m := range a.Messages() {
		if m.Role == provider.RoleTool {
			toolResults++
		}
	}
	if toolResults != 2 {
		t.Fatalf("expected 2 tool results before capping, got %d", toolResults)
	}
}

func TestHeadlessRun_StreamError(t *testing.T) {
	boom := errors.New("boom")
	a := New(nil, scriptedStream(t, []provider.StreamEvent{{Err: boom}}))
	h := &Headless{Agent: a}
	if _, err := h.Run("go"); !errors.Is(err, boom) {
		t.Fatalf("expected stream error to propagate, got %v", err)
	}
}

func TestHeadlessRun_UsageReported(t *testing.T) {
	a := New(nil, scriptedStream(t, []provider.StreamEvent{
		{Token: "x"},
		{Done: true, Usage: &provider.Usage{PromptTokens: 10, CompletionTokens: 5}},
	}))
	var got provider.Usage
	h := &Headless{Agent: a, OnUsage: func(u *provider.Usage) { got = *u }}
	if _, err := h.Run("go"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.PromptTokens != 10 || got.CompletionTokens != 5 {
		t.Fatalf("usage not surfaced, got %+v", got)
	}
}
