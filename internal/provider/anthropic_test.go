package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewAnthropic_RequiresKey(t *testing.T) {
	t.Setenv("SHHH_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	if _, err := NewAnthropic(ResolveOpts{}); err == nil {
		t.Fatal("expected error without API key")
	}
}

func TestNewAnthropic_DefaultModel(t *testing.T) {
	p, err := NewAnthropic(ResolveOpts{APIKey: "sk-test"})
	if err != nil {
		t.Fatal(err)
	}
	if p.model != "claude-opus-5" {
		t.Errorf("expected default model 'claude-opus-5', got %q", p.model)
	}
	if p.Name() != "anthropic" {
		t.Errorf("expected name 'anthropic', got %q", p.Name())
	}
}

func TestAnthropic_Registered(t *testing.T) {
	if _, err := Resolve("anthropic", ResolveOpts{APIKey: "sk-test"}); err != nil {
		t.Fatalf("anthropic should be registered: %v", err)
	}
	if d := Defaults("anthropic"); d.Model != "claude-opus-5" {
		t.Errorf("expected default model in registry, got %q", d.Model)
	}
}

func TestToAnthropicMessages_SystemExtracted(t *testing.T) {
	system, msgs := toAnthropicMessages([]Message{
		{Role: RoleSystem, Content: "be helpful"},
		{Role: RoleUser, Content: "hi"},
		{Role: RoleAssistant, Content: "hello"},
	})
	if system != "be helpful" {
		t.Errorf("expected system extracted, got %q", system)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages (system pulled out), got %d", len(msgs))
	}
}

func TestToAnthropicMessages_ToolResultsMergeIntoOneUserTurn(t *testing.T) {
	_, msgs := toAnthropicMessages([]Message{
		{Role: RoleUser, Content: "check both files"},
		{Role: RoleAssistant, ToolCalls: []ToolCall{
			{ID: "t1", Name: "read_file", Arguments: `{"path":"a"}`},
			{ID: "t2", Name: "read_file", Arguments: `{"path":"b"}`},
		}},
		{Role: RoleTool, Content: "content a", ToolCallID: "t1"},
		{Role: RoleTool, Content: "content b", ToolCallID: "t2"},
	})
	// user, assistant(tool_use x2), user(tool_result x2)
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages with merged tool results, got %d", len(msgs))
	}
	last := msgs[2]
	if string(last.Role) != "user" {
		t.Fatalf("tool results should be a user turn, got %s", last.Role)
	}
	if len(last.Content) != 2 {
		t.Fatalf("expected both tool results in one turn, got %d blocks", len(last.Content))
	}
	assistant := msgs[1]
	if len(assistant.Content) != 2 {
		t.Fatalf("expected 2 tool_use blocks on assistant turn, got %d", len(assistant.Content))
	}
	if assistant.Content[0].OfToolUse == nil || assistant.Content[0].OfToolUse.ID != "t1" {
		t.Error("expected tool_use block with ID t1")
	}
}

func TestToAnthropicTools_SchemaMapped(t *testing.T) {
	tools := toAnthropicTools([]Tool{{
		Name:        "read_file",
		Description: "Read a file",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`),
	}})
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
	tp := tools[0].OfTool
	if tp == nil {
		t.Fatal("expected OfTool set")
	}
	if tp.Name != "read_file" {
		t.Errorf("unexpected name %q", tp.Name)
	}
	props, ok := tp.InputSchema.Properties.(map[string]any)
	if !ok {
		t.Fatalf("expected properties map, got %T", tp.InputSchema.Properties)
	}
	if _, ok := props["path"]; !ok {
		t.Error("expected 'path' property in input schema")
	}
	if len(tp.InputSchema.Required) != 1 || tp.InputSchema.Required[0] != "path" {
		t.Errorf("expected required [path], got %v", tp.InputSchema.Required)
	}
}

func sseEvent(w http.ResponseWriter, event, data string) {
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
}

func anthropicSSEServer(t *testing.T, write func(w http.ResponseWriter)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		write(w)
	}))
}

func drainAnthropic(t *testing.T, events <-chan StreamEvent) (text string, toolCalls []ToolCall, usage *Usage, errOut error) {
	t.Helper()
	for ev := range events {
		if ev.Err != nil {
			return text, toolCalls, usage, ev.Err
		}
		text += ev.Token
		if ev.Done {
			return text, ev.ToolCalls, ev.Usage, nil
		}
	}
	return text, toolCalls, usage, nil
}

func TestAnthropic_StreamText(t *testing.T) {
	srv := anthropicSSEServer(t, func(w http.ResponseWriter) {
		sseEvent(w, "message_start", `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-opus-5","stop_reason":null,"usage":{"input_tokens":12,"output_tokens":1}}}`)
		sseEvent(w, "content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
		sseEvent(w, "content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ls "}}`)
		sseEvent(w, "content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"-la"}}`)
		sseEvent(w, "content_block_stop", `{"type":"content_block_stop","index":0}`)
		sseEvent(w, "message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":5}}`)
		sseEvent(w, "message_stop", `{"type":"message_stop"}`)
	})
	defer srv.Close()

	p, err := NewAnthropic(ResolveOpts{APIKey: "sk-test", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	events, err := p.StreamCompletion(context.Background(), []Message{
		{Role: RoleSystem, Content: "sys"},
		{Role: RoleUser, Content: "list files"},
	}, CompletionOpts{})
	if err != nil {
		t.Fatal(err)
	}

	text, toolCalls, usage, streamErr := drainAnthropic(t, events)
	if streamErr != nil {
		t.Fatalf("unexpected stream error: %v", streamErr)
	}
	if text != "ls -la" {
		t.Errorf("expected 'ls -la', got %q", text)
	}
	if len(toolCalls) != 0 {
		t.Errorf("expected no tool calls, got %d", len(toolCalls))
	}
	if usage == nil || usage.PromptTokens != 12 || usage.CompletionTokens != 5 {
		t.Errorf("unexpected usage: %+v", usage)
	}
}

func TestAnthropic_StreamToolUse(t *testing.T) {
	srv := anthropicSSEServer(t, func(w http.ResponseWriter) {
		sseEvent(w, "message_start", `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-opus-5","stop_reason":null,"usage":{"input_tokens":20,"output_tokens":1}}}`)
		sseEvent(w, "content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"read_file","input":{}}}`)
		sseEvent(w, "content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"path\":"}}`)
		sseEvent(w, "content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"\"main.go\"}"}}`)
		sseEvent(w, "content_block_stop", `{"type":"content_block_stop","index":0}`)
		sseEvent(w, "message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":15}}`)
		sseEvent(w, "message_stop", `{"type":"message_stop"}`)
	})
	defer srv.Close()

	p, err := NewAnthropic(ResolveOpts{APIKey: "sk-test", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	events, err := p.StreamCompletion(context.Background(), []Message{
		{Role: RoleUser, Content: "read main.go"},
	}, CompletionOpts{Tools: []Tool{{Name: "read_file", Parameters: json.RawMessage(`{"type":"object","properties":{}}`)}}})
	if err != nil {
		t.Fatal(err)
	}

	_, toolCalls, usage, streamErr := drainAnthropic(t, events)
	if streamErr != nil {
		t.Fatalf("unexpected stream error: %v", streamErr)
	}
	if len(toolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(toolCalls))
	}
	if toolCalls[0].ID != "toolu_1" || toolCalls[0].Name != "read_file" {
		t.Errorf("unexpected tool call: %+v", toolCalls[0])
	}
	var args map[string]string
	if err := json.Unmarshal([]byte(toolCalls[0].Arguments), &args); err != nil || args["path"] != "main.go" {
		t.Errorf("expected accumulated arguments with path=main.go, got %q", toolCalls[0].Arguments)
	}
	if usage == nil || usage.CompletionTokens != 15 {
		t.Errorf("unexpected usage: %+v", usage)
	}
}

func TestAnthropic_RefusalStopReason(t *testing.T) {
	srv := anthropicSSEServer(t, func(w http.ResponseWriter) {
		sseEvent(w, "message_start", `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-opus-5","stop_reason":null,"usage":{"input_tokens":5,"output_tokens":1}}}`)
		sseEvent(w, "message_delta", `{"type":"message_delta","delta":{"stop_reason":"refusal","stop_sequence":null},"usage":{"output_tokens":1}}`)
		sseEvent(w, "message_stop", `{"type":"message_stop"}`)
	})
	defer srv.Close()

	p, err := NewAnthropic(ResolveOpts{APIKey: "sk-test", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	events, err := p.StreamCompletion(context.Background(), []Message{
		{Role: RoleUser, Content: "do a bad thing"},
	}, CompletionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, streamErr := drainAnthropic(t, events)
	if streamErr == nil {
		t.Fatal("expected an error for refusal stop reason")
	}
}
