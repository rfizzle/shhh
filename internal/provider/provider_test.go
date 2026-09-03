package provider

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestToolJSON(t *testing.T) {
	tool := Tool{
		Name:        "read_file",
		Description: "Read file contents",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "file path"}
			},
			"required": ["path"]
		}`),
	}

	data, err := json.Marshal(tool)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var got Tool
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if got.Name != "read_file" {
		t.Errorf("expected name 'read_file', got %q", got.Name)
	}
}

func TestMessageRoles(t *testing.T) {
	if RoleSystem != "system" {
		t.Errorf("RoleSystem = %q", RoleSystem)
	}
	if RoleUser != "user" {
		t.Errorf("RoleUser = %q", RoleUser)
	}
	if RoleAssistant != "assistant" {
		t.Errorf("RoleAssistant = %q", RoleAssistant)
	}
	if RoleTool != "tool" {
		t.Errorf("RoleTool = %q", RoleTool)
	}
}

func TestMessage_ToolCallFields(t *testing.T) {
	msg := Message{
		Role: RoleAssistant,
		ToolCalls: []ToolCall{
			{ID: "call_1", Name: "read_file", Arguments: `{"path":"test.go"}`},
			{ID: "call_2", Name: "search", Arguments: `{"pattern":"TODO"}`},
		},
	}

	if len(msg.ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(msg.ToolCalls))
	}
	if msg.ToolCalls[0].Name != "read_file" {
		t.Errorf("expected 'read_file', got %q", msg.ToolCalls[0].Name)
	}
	if msg.ToolCalls[1].Name != "search" {
		t.Errorf("expected 'search', got %q", msg.ToolCalls[1].Name)
	}
}

func TestMessage_ToolResult(t *testing.T) {
	msg := Message{
		Role:       RoleTool,
		Content:    "file contents here",
		ToolCallID: "call_1",
	}

	if msg.Role != RoleTool {
		t.Errorf("expected RoleTool, got %q", msg.Role)
	}
	if msg.ToolCallID != "call_1" {
		t.Errorf("expected tool call ID 'call_1', got %q", msg.ToolCallID)
	}
}

func TestStreamEvent_ToolCalls(t *testing.T) {
	ev := StreamEvent{
		ToolCalls: []ToolCall{
			{ID: "call_1", Name: "read_file", Arguments: `{"path":"test.go"}`},
		},
		Done: true,
	}

	if len(ev.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(ev.ToolCalls))
	}
	if ev.ToolCalls[0].Name != "read_file" {
		t.Errorf("expected 'read_file', got %q", ev.ToolCalls[0].Name)
	}
}

func TestCompletionOpts_Tools(t *testing.T) {
	opts := CompletionOpts{
		Model: "gpt-4o",
		Tools: []Tool{
			{Name: "read_file", Description: "Read a file"},
			{Name: "search", Description: "Search code"},
		},
		ToolChoice: "auto",
	}

	if len(opts.Tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(opts.Tools))
	}
	if opts.ToolChoice != "auto" {
		t.Errorf("expected tool choice 'auto', got %q", opts.ToolChoice)
	}
}

// A tool whose arguments are an array of objects has to arrive at the model
// as that array on every dialect. A converter that rebuilds a schema from the
// fields its own SDK struct happens to have describes the array as a
// free-form value, and the model sends whatever it guessed — which the tool
// then refuses, one round later, for the whole call.
func TestToolSchema_NestedArraySurvivesEveryDialect(t *testing.T) {
	raw := `{"type":"object","properties":{` +
		`"path":{"type":"string"},` +
		`"edits":{"type":"array","items":{"type":"object","properties":{` +
		`"old_text":{"type":"string"},"new_text":{"type":"string"},` +
		`"replace_all":{"type":"boolean"}},"required":["old_text","new_text"]}}},` +
		`"required":["path"]}`
	var want map[string]any
	if err := json.Unmarshal([]byte(raw), &want); err != nil {
		t.Fatal(err)
	}
	tools := []Tool{{Name: "edit_file", Parameters: json.RawMessage(raw)}}

	// One entry per dialect the registry can resolve to. OpenRouter speaks
	// the chat-completions dialect, so it is the same converter.
	for _, tc := range []struct {
		dialect string
		schema  any
	}{
		{"anthropic", toAnthropicTools(tools)[0].OfTool.InputSchema},
		{"openai", toOpenAITools(tools)[0].Function.Parameters},
		{"openai responses", toResponsesTools(tools)[0].Parameters},
		{"gemini", toGeminiTools(tools)[0].FunctionDeclarations[0].ParametersJsonSchema},
	} {
		raw, err := json.Marshal(tc.schema)
		if err != nil {
			t.Fatalf("%s: marshal: %v", tc.dialect, err)
		}
		var got map[string]any
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("%s: unmarshal: %v", tc.dialect, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s must send the schema as written:\n got %v\nwant %v", tc.dialect, got, want)
		}
	}
}
