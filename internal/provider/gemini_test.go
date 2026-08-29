package provider

import (
	"encoding/json"
	"os"
	"testing"

	"google.golang.org/genai"
)

func TestGemini_Name(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "test-key")
	p, err := NewGemini(ResolveOpts{APIKey: "test-key"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name() != "gemini" {
		t.Errorf("expected 'gemini', got %q", p.Name())
	}
}

func TestGemini_DefaultModel(t *testing.T) {
	p, err := NewGemini(ResolveOpts{APIKey: "test-key"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.model != "gemini-2.5-flash" {
		t.Errorf("expected default model 'gemini-2.5-flash', got %q", p.model)
	}
}

func TestGemini_CustomModel(t *testing.T) {
	p, err := NewGemini(ResolveOpts{APIKey: "test-key", Model: "gemini-2.5-pro"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.model != "gemini-2.5-pro" {
		t.Errorf("expected model 'gemini-2.5-pro', got %q", p.model)
	}
}

func TestNewGemini_MissingKey(t *testing.T) {
	t.Setenv("SHHH_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")
	os.Unsetenv("GEMINI_API_KEY")
	_, err := NewGemini(ResolveOpts{})
	if err == nil {
		t.Fatal("expected error for missing API key")
	}
}

func TestNewGemini_EnvKey(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "env-key")
	p, err := NewGemini(ResolveOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
}

func TestToGeminiContents(t *testing.T) {
	msgs := []Message{
		{Role: RoleSystem, Content: "you are helpful"},
		{Role: RoleUser, Content: "hello"},
		{Role: RoleAssistant, Content: "hi there"},
		{Role: RoleUser, Content: "what's up"},
	}

	contents, sysInstruction := toGeminiContents(msgs)

	if sysInstruction == nil {
		t.Fatal("expected system instruction")
	}
	if sysInstruction.Parts[0].Text != "you are helpful" {
		t.Errorf("system instruction mismatch: %q", sysInstruction.Parts[0].Text)
	}

	if len(contents) != 3 {
		t.Fatalf("expected 3 content entries, got %d", len(contents))
	}
	if contents[0].Role != "user" || contents[0].Parts[0].Text != "hello" {
		t.Errorf("content 0 mismatch: %+v", contents[0])
	}
	if contents[1].Role != "model" || contents[1].Parts[0].Text != "hi there" {
		t.Errorf("content 1 mismatch: %+v", contents[1])
	}
	if contents[2].Role != "user" || contents[2].Parts[0].Text != "what's up" {
		t.Errorf("content 2 mismatch: %+v", contents[2])
	}
}

func TestToGeminiContents_NoSystem(t *testing.T) {
	msgs := []Message{
		{Role: RoleUser, Content: "hello"},
	}

	contents, sysInstruction := toGeminiContents(msgs)

	if sysInstruction != nil {
		t.Error("expected nil system instruction")
	}
	if len(contents) != 1 {
		t.Fatalf("expected 1 content entry, got %d", len(contents))
	}
}

func TestToGeminiContents_AssistantToolCalls(t *testing.T) {
	msgs := []Message{
		{Role: RoleUser, Content: "read my file"},
		{
			Role: RoleAssistant,
			ToolCalls: []ToolCall{
				{ID: "call_1", Name: "read_file", Arguments: `{"path":"/tmp/test.go"}`},
			},
		},
		{
			Role:       RoleTool,
			Content:    "package main",
			ToolCallID: "call_1",
		},
	}

	contents, _ := toGeminiContents(msgs)

	if len(contents) != 3 {
		t.Fatalf("expected 3 content entries, got %d", len(contents))
	}

	// Assistant with function call
	assistant := contents[1]
	if assistant.Role != "model" {
		t.Errorf("expected role 'model', got %q", assistant.Role)
	}
	if len(assistant.Parts) != 1 {
		t.Fatalf("expected 1 part (function call only, no text), got %d", len(assistant.Parts))
	}
	fc := assistant.Parts[0].FunctionCall
	if fc == nil {
		t.Fatal("expected FunctionCall part")
	}
	if fc.Name != "read_file" {
		t.Errorf("expected function name 'read_file', got %q", fc.Name)
	}
	if fc.Args["path"] != "/tmp/test.go" {
		t.Errorf("expected path arg '/tmp/test.go', got %v", fc.Args["path"])
	}

	// Tool result
	toolResult := contents[2]
	if toolResult.Role != "function" {
		t.Errorf("expected role 'function', got %q", toolResult.Role)
	}
	fr := toolResult.Parts[0].FunctionResponse
	if fr == nil {
		t.Fatal("expected FunctionResponse part")
	}
	if fr.Name != "read_file" {
		t.Errorf("expected function name 'read_file', got %q", fr.Name)
	}
	if fr.Response["result"] != "package main" {
		t.Errorf("expected result 'package main', got %v", fr.Response["result"])
	}
}

func TestToGeminiContents_AssistantTextAndToolCall(t *testing.T) {
	msgs := []Message{
		{
			Role:    RoleAssistant,
			Content: "Let me check that file.",
			ToolCalls: []ToolCall{
				{Name: "read_file", Arguments: `{"path":"main.go"}`},
			},
		},
	}

	contents, _ := toGeminiContents(msgs)

	if len(contents) != 1 {
		t.Fatalf("expected 1 content entry, got %d", len(contents))
	}
	if len(contents[0].Parts) != 2 {
		t.Fatalf("expected 2 parts (text + function call), got %d", len(contents[0].Parts))
	}
	if contents[0].Parts[0].Text != "Let me check that file." {
		t.Errorf("expected text part, got %q", contents[0].Parts[0].Text)
	}
	if contents[0].Parts[1].FunctionCall == nil {
		t.Fatal("expected FunctionCall in second part")
	}
}

func TestToGeminiTools(t *testing.T) {
	tools := []Tool{
		{Name: "read_file", Description: "Read a file", Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)},
		{Name: "list_directory", Description: "List a directory", Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`)},
	}

	result := toGeminiTools(tools)
	if len(result) != 1 {
		t.Fatalf("expected 1 genai.Tool (with multiple declarations), got %d", len(result))
	}
	decls := result[0].FunctionDeclarations
	if len(decls) != 2 {
		t.Fatalf("expected 2 function declarations, got %d", len(decls))
	}
	if decls[0].Name != "read_file" {
		t.Errorf("expected name 'read_file', got %q", decls[0].Name)
	}
	if decls[0].Description != "Read a file" {
		t.Errorf("expected description 'Read a file', got %q", decls[0].Description)
	}
	if decls[0].ParametersJsonSchema == nil {
		t.Error("expected non-nil ParametersJsonSchema")
	}
	if decls[1].Name != "list_directory" {
		t.Errorf("expected name 'list_directory', got %q", decls[1].Name)
	}
}

func TestToGeminiTools_NilParameters(t *testing.T) {
	tools := []Tool{
		{Name: "no_params", Description: "No parameters"},
	}
	result := toGeminiTools(tools)
	decls := result[0].FunctionDeclarations
	if decls[0].ParametersJsonSchema != nil {
		t.Errorf("expected nil ParametersJsonSchema for empty params, got %v", decls[0].ParametersJsonSchema)
	}
}

func TestToGeminiToolConfig(t *testing.T) {
	tests := []struct {
		choice string
		want   string
	}{
		{"auto", string(genai.FunctionCallingConfigModeAuto)},
		{"any", string(genai.FunctionCallingConfigModeAny)},
		{"required", string(genai.FunctionCallingConfigModeAny)},
		{"none", string(genai.FunctionCallingConfigModeNone)},
		{"unknown", string(genai.FunctionCallingConfigModeAuto)},
	}
	for _, tt := range tests {
		t.Run(tt.choice, func(t *testing.T) {
			cfg := toGeminiToolConfig(tt.choice)
			if cfg == nil || cfg.FunctionCallingConfig == nil {
				t.Fatal("expected non-nil ToolConfig with FunctionCallingConfig")
			}
			got := string(cfg.FunctionCallingConfig.Mode)
			if got != tt.want {
				t.Errorf("choice %q: expected mode %q, got %q", tt.choice, tt.want, got)
			}
		})
	}
}

func TestJsonSchemaToAny(t *testing.T) {
	raw := json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`)
	result := jsonSchemaToAny(raw)
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", result)
	}
	if m["type"] != "object" {
		t.Errorf("expected type 'object', got %v", m["type"])
	}
}

func TestJsonSchemaToAny_Empty(t *testing.T) {
	result := jsonSchemaToAny(nil)
	if result != nil {
		t.Errorf("expected nil for empty input, got %v", result)
	}
}

func TestGemini_Registration(t *testing.T) {
	names := Available()
	found := false
	for _, n := range names {
		if n == "gemini" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'gemini' to be registered")
	}
}

// Addressing a tool result to the call it answers.
//
// Gemini pairs a functionResponse to its functionCall by name — the SDK's own
// field doc says so — and the ids the rest of shhh pairs on are ours, not the
// API's, which sends none. Putting the id in the name field addressed every
// result to a function the model had never called, so nothing it searched for
// ever came back and it searched again.

func TestToGeminiContents_ToolResultCarriesTheFunctionName(t *testing.T) {
	msgs := []Message{
		{Role: RoleUser, Content: "find it"},
		{Role: RoleAssistant, ToolCalls: []ToolCall{
			{ID: "gemini_call_7", Name: "search", Arguments: `{"pattern":"foo"}`},
		}},
		{Role: RoleTool, ToolCallID: "gemini_call_7", Content: "a.go:1: foo"},
	}

	contents, _ := toGeminiContents(msgs)

	fr := contents[2].Parts[0].FunctionResponse
	if fr == nil {
		t.Fatal("expected a FunctionResponse part")
	}
	if fr.Name != "search" {
		t.Errorf("FunctionResponse.Name = %q, want the function name %q", fr.Name, "search")
	}
	if fr.Response["result"] != "a.go:1: foo" {
		t.Errorf("unexpected result payload: %v", fr.Response)
	}
}

func TestToGeminiContents_ToolResultNamedEvenWithoutIDs(t *testing.T) {
	// A history with no ids at all — a resumed session, a hand-built list —
	// still pairs, by position.
	msgs := []Message{
		{Role: RoleAssistant, ToolCalls: []ToolCall{{Name: "read_file", Arguments: `{"path":"a.go"}`}}},
		{Role: RoleTool, Content: "package a"},
	}

	contents, _ := toGeminiContents(msgs)

	if got := contents[1].Parts[0].FunctionResponse.Name; got != "read_file" {
		t.Errorf("FunctionResponse.Name = %q, want %q", got, "read_file")
	}
}

func TestToGeminiContents_ParallelResultsMergeInCallOrder(t *testing.T) {
	// Two calls of the same function in one round are told apart by their
	// order inside a single function turn, which is how Gemini reads them.
	msgs := []Message{
		{Role: RoleAssistant, ToolCalls: []ToolCall{
			{ID: "c1", Name: "search", Arguments: `{"pattern":"foo"}`},
			{ID: "c2", Name: "read_file", Arguments: `{"path":"a.go"}`},
		}},
		{Role: RoleTool, ToolCallID: "c1", Content: "foo hit"},
		{Role: RoleTool, ToolCallID: "c2", Content: "package a"},
	}

	contents, _ := toGeminiContents(msgs)

	if len(contents) != 2 {
		t.Fatalf("expected the model turn and one function turn, got %d contents", len(contents))
	}
	fn := contents[1]
	if fn.Role != "function" {
		t.Fatalf("expected role 'function', got %q", fn.Role)
	}
	if len(fn.Parts) != 2 {
		t.Fatalf("expected both results in one turn, got %d parts", len(fn.Parts))
	}
	if fn.Parts[0].FunctionResponse.Name != "search" || fn.Parts[1].FunctionResponse.Name != "read_file" {
		t.Errorf("results out of order: %q then %q",
			fn.Parts[0].FunctionResponse.Name, fn.Parts[1].FunctionResponse.Name)
	}
}

func TestToGeminiContents_ResultsOutOfOrderFollowTheirIDs(t *testing.T) {
	msgs := []Message{
		{Role: RoleAssistant, ToolCalls: []ToolCall{
			{ID: "c1", Name: "search", Arguments: `{}`},
			{ID: "c2", Name: "read_file", Arguments: `{}`},
		}},
		{Role: RoleTool, ToolCallID: "c2", Content: "package a"},
		{Role: RoleTool, ToolCallID: "c1", Content: "foo hit"},
	}

	contents, _ := toGeminiContents(msgs)

	fn := contents[1]
	if fn.Parts[0].FunctionResponse.Name != "read_file" || fn.Parts[1].FunctionResponse.Name != "search" {
		t.Errorf("an id should outrank position: got %q then %q",
			fn.Parts[0].FunctionResponse.Name, fn.Parts[1].FunctionResponse.Name)
	}
}

// Thought signatures: Gemini 3 attaches an opaque signature to
// the parts it produced and expects it back on the same part. Dropped, the
// model cannot recognise the plan in its own history and starts over.

func TestToGeminiContents_ThoughtSignaturesRoundTrip(t *testing.T) {
	sig := encodeSignature([]byte{0x01, 0x02, 0xff})
	msgs := []Message{
		{
			Role:      RoleAssistant,
			Reasoning: []ReasoningBlock{{Text: "I should grep", Signature: sig}},
			ToolCalls: []ToolCall{{ID: "c1", Name: "search", Arguments: `{}`, Signature: sig}},
		},
	}

	contents, _ := toGeminiContents(msgs)

	parts := contents[0].Parts
	if len(parts) != 2 {
		t.Fatalf("expected a thought part and a call part, got %d", len(parts))
	}
	if !parts[0].Thought {
		t.Error("thinking should go back marked as a thought")
	}
	if string(parts[0].ThoughtSignature) != "\x01\x02\xff" {
		t.Errorf("thought signature not restored: %v", parts[0].ThoughtSignature)
	}
	if string(parts[1].ThoughtSignature) != "\x01\x02\xff" {
		t.Errorf("the call's signature has to ride the call part: %v", parts[1].ThoughtSignature)
	}
}

func TestSignatureRoundTrip(t *testing.T) {
	if encodeSignature(nil) != "" {
		t.Error("no signature encodes to nothing")
	}
	if decodeSignature("") != nil {
		t.Error("nothing decodes to no signature")
	}
	if decodeSignature("not base64!!") != nil {
		t.Error("an undecodable signature is no signature, not a panic")
	}
	raw := []byte{0x00, 0x7f, 0x80, 0xff}
	if got := decodeSignature(encodeSignature(raw)); string(got) != string(raw) {
		t.Errorf("round trip changed the bytes: %v", got)
	}
}

func TestAppendThought_JoinsChunksUntilSigned(t *testing.T) {
	// Thinking streams in pieces and the signature closes the piece it
	// belongs to; a block per chunk would be a hundred blocks per turn.
	var blocks []ReasoningBlock
	blocks = appendThought(blocks, "I should ", nil)
	blocks = appendThought(blocks, "grep for it", []byte("sig"))
	blocks = appendThought(blocks, "then read it", []byte("sig2"))

	if len(blocks) != 2 {
		t.Fatalf("expected two thoughts, got %d: %+v", len(blocks), blocks)
	}
	if blocks[0].Text != "I should grep for it" {
		t.Errorf("chunks should have joined, got %q", blocks[0].Text)
	}
	if blocks[0].Signature != encodeSignature([]byte("sig")) {
		t.Errorf("the closing signature belongs to the block it closed, got %q", blocks[0].Signature)
	}
	if blocks[1].Text != "then read it" {
		t.Errorf("a signed block is finished, got %q", blocks[1].Text)
	}
	if appendThought(nil, "", nil) != nil {
		t.Error("an empty part is not a thought")
	}
}

func TestNextGeminiCallID_Unique(t *testing.T) {
	// Ids are ours to invent because the API sends none, and a reused id
	// would let a later result pair with an earlier call.
	a, b := nextGeminiCallID(), nextGeminiCallID()
	if a == b || a == "" {
		t.Errorf("expected distinct non-empty ids, got %q and %q", a, b)
	}
	if len(CompletedToolCalls([]ToolCall{{ID: a, Name: "search", Arguments: `{}`}})) != 1 {
		t.Error("a call with an invented id should survive a dropped stream")
	}
}
