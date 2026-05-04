package provider

import (
	"encoding/json"
	"errors"
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
			ToolCallID: "read_file",
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

func TestClassifyGeminiError_Unauthorized(t *testing.T) {
	err := classifyGeminiError(errors.New("googleapi: Error 401: invalid key"))
	if !errors.Is(err, ErrGeminiUnauthorized) {
		t.Errorf("expected ErrGeminiUnauthorized, got: %v", err)
	}
}

func TestClassifyGeminiError_Forbidden(t *testing.T) {
	err := classifyGeminiError(errors.New("googleapi: Error 403: forbidden"))
	if !errors.Is(err, ErrGeminiUnauthorized) {
		t.Errorf("expected ErrGeminiUnauthorized, got: %v", err)
	}
}

func TestClassifyGeminiError_RateLimited(t *testing.T) {
	err := classifyGeminiError(errors.New("googleapi: Error 429: rate limit"))
	if !errors.Is(err, ErrGeminiRateLimited) {
		t.Errorf("expected ErrGeminiRateLimited, got: %v", err)
	}
}

func TestClassifyGeminiError_Generic(t *testing.T) {
	original := errors.New("some network error")
	got := classifyGeminiError(original)
	if got != original {
		t.Errorf("expected original error returned, got %v", got)
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
