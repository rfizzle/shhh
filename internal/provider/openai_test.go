package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

func newTestOpenAI(baseURL string, model string) *OpenAI {
	cfg := openai.DefaultConfig("test-key")
	cfg.BaseURL = baseURL
	return NewOpenAIWithConfig(openai.NewClientWithConfig(cfg), model)
}

func TestOpenAI_Name(t *testing.T) {
	p := newTestOpenAI("http://unused", "")
	if p.Name() != "openai" {
		t.Errorf("expected 'openai', got %q", p.Name())
	}
}

func TestOpenAI_DefaultModel(t *testing.T) {
	p := NewOpenAIWithConfig(openai.NewClient("fake"), "")
	if p.model != "gpt-4o" {
		t.Errorf("expected default model 'gpt-4o', got %q", p.model)
	}
}

func TestNewOpenAI_MissingKey(t *testing.T) {
	t.Setenv("SHHH_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	_, err := NewOpenAI(ResolveOpts{})
	if err == nil {
		t.Fatal("expected error for missing API key")
	}
}

func TestOpenAI_StreamCompletion(t *testing.T) {
	tokens := []string{"hello", " world"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		for _, tok := range tokens {
			chunk := openai.ChatCompletionStreamResponse{
				Choices: []openai.ChatCompletionStreamChoice{
					{Delta: openai.ChatCompletionStreamChoiceDelta{Content: tok}},
				},
			}
			data, _ := json.Marshal(chunk)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	p := newTestOpenAI(srv.URL+"/v1", "gpt-4o")
	ch, err := p.StreamCompletion(context.Background(), []Message{
		{Role: RoleUser, Content: "hi"},
	}, CompletionOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var got string
	for ev := range ch {
		if ev.Err != nil {
			t.Fatalf("unexpected stream error: %v", ev.Err)
		}
		got += ev.Token
	}
	if got != "hello world" {
		t.Errorf("expected 'hello world', got %q", got)
	}
}

func TestOpenAI_StreamCompletion_OptsOverrideModel(t *testing.T) {
	var receivedModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req openai.ChatCompletionRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		receivedModel = req.Model
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	p := newTestOpenAI(srv.URL+"/v1", "gpt-4o")
	ch, err := p.StreamCompletion(context.Background(), []Message{
		{Role: RoleUser, Content: "hi"},
	}, CompletionOpts{Model: "o3"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for range ch {
	}
	if receivedModel != "o3" {
		t.Errorf("expected model 'o3', got %q", receivedModel)
	}
}

func TestOpenAI_StreamCompletion_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"message": "Incorrect API key provided",
				"type":    "invalid_request_error",
			},
		})
	}))
	defer srv.Close()

	p := newTestOpenAI(srv.URL+"/v1", "gpt-4o")
	_, err := p.StreamCompletion(context.Background(), []Message{
		{Role: RoleUser, Content: "hi"},
	}, CompletionOpts{})
	if err == nil {
		t.Fatal("expected error for 401")
	}
	if !errors.Is(err, ErrAuth) {
		t.Errorf("expected ErrAuth, got: %v", err)
	}
}

func TestOpenAI_StreamCompletion_RateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"message": "Rate limit reached",
				"type":    "rate_limit_error",
			},
		})
	}))
	defer srv.Close()

	p := newTestOpenAI(srv.URL+"/v1", "gpt-4o")
	_, err := p.StreamCompletion(context.Background(), []Message{
		{Role: RoleUser, Content: "hi"},
	}, CompletionOpts{})
	if err == nil {
		t.Fatal("expected error for 429")
	}
	if !errors.Is(err, ErrRateLimited) {
		t.Errorf("expected ErrRateLimited, got: %v", err)
	}
}

func TestOpenAI_StreamCompletion_ContextCanceled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	p := newTestOpenAI(srv.URL+"/v1", "gpt-4o")
	_, err := p.StreamCompletion(ctx, []Message{
		{Role: RoleUser, Content: "hi"},
	}, CompletionOpts{})
	if err == nil {
		t.Fatal("expected error for canceled context")
	}
}

func TestToOpenAIMessages(t *testing.T) {
	msgs := []Message{
		{Role: RoleSystem, Content: "you are helpful"},
		{Role: RoleUser, Content: "hello"},
		{Role: RoleAssistant, Content: "hi"},
	}
	got := toOpenAIMessages(msgs)
	if len(got) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(got))
	}
	if got[0].Role != "system" || got[0].Content != "you are helpful" {
		t.Errorf("message 0 mismatch: %+v", got[0])
	}
	if got[1].Role != "user" || got[1].Content != "hello" {
		t.Errorf("message 1 mismatch: %+v", got[1])
	}
	if got[2].Role != "assistant" || got[2].Content != "hi" {
		t.Errorf("message 2 mismatch: %+v", got[2])
	}
}

func TestToOpenAIMessages_ToolCalls(t *testing.T) {
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
	got := toOpenAIMessages(msgs)

	if len(got) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(got))
	}

	// Assistant message with tool calls
	assistant := got[1]
	if len(assistant.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(assistant.ToolCalls))
	}
	tc := assistant.ToolCalls[0]
	if tc.ID != "call_1" {
		t.Errorf("expected tool call ID 'call_1', got %q", tc.ID)
	}
	if tc.Function.Name != "read_file" {
		t.Errorf("expected function name 'read_file', got %q", tc.Function.Name)
	}
	if tc.Function.Arguments != `{"path":"/tmp/test.go"}` {
		t.Errorf("unexpected arguments: %q", tc.Function.Arguments)
	}

	// Tool result message
	tool := got[2]
	if tool.Role != "tool" {
		t.Errorf("expected role 'tool', got %q", tool.Role)
	}
	if tool.ToolCallID != "call_1" {
		t.Errorf("expected tool call ID 'call_1', got %q", tool.ToolCallID)
	}
	if tool.Content != "package main" {
		t.Errorf("expected content 'package main', got %q", tool.Content)
	}
}

func TestToOpenAIMessages_MultipleToolCalls(t *testing.T) {
	msgs := []Message{
		{
			Role: RoleAssistant,
			ToolCalls: []ToolCall{
				{ID: "call_1", Name: "read_file", Arguments: `{"path":"a.go"}`},
				{ID: "call_2", Name: "list_directory", Arguments: `{"path":"."}`},
			},
		},
	}
	got := toOpenAIMessages(msgs)

	if len(got[0].ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(got[0].ToolCalls))
	}
	if got[0].ToolCalls[0].Function.Name != "read_file" {
		t.Errorf("first tool call name mismatch: %q", got[0].ToolCalls[0].Function.Name)
	}
	if got[0].ToolCalls[1].Function.Name != "list_directory" {
		t.Errorf("second tool call name mismatch: %q", got[0].ToolCalls[1].Function.Name)
	}
}

func TestOpenAI_StreamCompletion_ToolCalls(t *testing.T) {
	idx0 := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req openai.ChatCompletionRequest
		_ = json.NewDecoder(r.Body).Decode(&req)

		if len(req.Tools) != 1 {
			t.Errorf("expected 1 tool in request, got %d", len(req.Tools))
		}

		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)

		// First chunk: tool call ID and name
		chunk1 := openai.ChatCompletionStreamResponse{
			Choices: []openai.ChatCompletionStreamChoice{
				{
					Delta: openai.ChatCompletionStreamChoiceDelta{
						ToolCalls: []openai.ToolCall{
							{Index: &idx0, ID: "call_abc", Function: openai.FunctionCall{Name: "read_file", Arguments: `{"pa`}},
						},
					},
				},
			},
		}
		data, _ := json.Marshal(chunk1)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()

		// Second chunk: rest of arguments
		chunk2 := openai.ChatCompletionStreamResponse{
			Choices: []openai.ChatCompletionStreamChoice{
				{
					Delta: openai.ChatCompletionStreamChoiceDelta{
						ToolCalls: []openai.ToolCall{
							{Index: &idx0, Function: openai.FunctionCall{Arguments: `th":"main.go"}`}},
						},
					},
				},
			},
		}
		data, _ = json.Marshal(chunk2)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()

		// Final chunk: finish_reason = tool_calls
		chunk3 := openai.ChatCompletionStreamResponse{
			Choices: []openai.ChatCompletionStreamChoice{
				{
					Delta:        openai.ChatCompletionStreamChoiceDelta{},
					FinishReason: "tool_calls",
				},
			},
		}
		data, _ = json.Marshal(chunk3)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()

		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	p := newTestOpenAI(srv.URL+"/v1", "gpt-4o")
	ch, err := p.StreamCompletion(context.Background(), []Message{
		{Role: RoleUser, Content: "read main.go"},
	}, CompletionOpts{
		Tools: []Tool{
			{Name: "read_file", Description: "Read a file", Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)},
		},
		ToolChoice: "auto",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var toolCalls []ToolCall
	for ev := range ch {
		if ev.Err != nil {
			t.Fatalf("unexpected stream error: %v", ev.Err)
		}
		if len(ev.ToolCalls) > 0 {
			toolCalls = ev.ToolCalls
		}
	}

	if len(toolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(toolCalls))
	}
	if toolCalls[0].ID != "call_abc" {
		t.Errorf("expected ID 'call_abc', got %q", toolCalls[0].ID)
	}
	if toolCalls[0].Name != "read_file" {
		t.Errorf("expected name 'read_file', got %q", toolCalls[0].Name)
	}
	if toolCalls[0].Arguments != `{"path":"main.go"}` {
		t.Errorf("expected arguments '{\"path\":\"main.go\"}', got %q", toolCalls[0].Arguments)
	}
}

func TestOpenAI_StreamCompletion_MultipleToolCalls(t *testing.T) {
	idx0 := 0
	idx1 := 1
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)

		chunk1 := openai.ChatCompletionStreamResponse{
			Choices: []openai.ChatCompletionStreamChoice{
				{
					Delta: openai.ChatCompletionStreamChoiceDelta{
						ToolCalls: []openai.ToolCall{
							{Index: &idx0, ID: "call_1", Function: openai.FunctionCall{Name: "read_file", Arguments: `{"path":"a.go"}`}},
							{Index: &idx1, ID: "call_2", Function: openai.FunctionCall{Name: "search", Arguments: `{"pattern":"TODO"}`}},
						},
					},
				},
			},
		}
		data, _ := json.Marshal(chunk1)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()

		chunk2 := openai.ChatCompletionStreamResponse{
			Choices: []openai.ChatCompletionStreamChoice{
				{
					Delta:        openai.ChatCompletionStreamChoiceDelta{},
					FinishReason: "tool_calls",
				},
			},
		}
		data, _ = json.Marshal(chunk2)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()

		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	p := newTestOpenAI(srv.URL+"/v1", "gpt-4o")
	ch, err := p.StreamCompletion(context.Background(), []Message{
		{Role: RoleUser, Content: "check stuff"},
	}, CompletionOpts{
		Tools: []Tool{
			{Name: "read_file", Description: "Read a file"},
			{Name: "search", Description: "Search code"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var toolCalls []ToolCall
	for ev := range ch {
		if ev.Err != nil {
			t.Fatalf("unexpected stream error: %v", ev.Err)
		}
		if len(ev.ToolCalls) > 0 {
			toolCalls = ev.ToolCalls
		}
	}

	if len(toolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(toolCalls))
	}
	if toolCalls[0].Name != "read_file" || toolCalls[0].ID != "call_1" {
		t.Errorf("tool call 0 mismatch: %+v", toolCalls[0])
	}
	if toolCalls[1].Name != "search" || toolCalls[1].ID != "call_2" {
		t.Errorf("tool call 1 mismatch: %+v", toolCalls[1])
	}
}

func TestOpenAI_StreamCompletion_ToolsPassedInRequest(t *testing.T) {
	var receivedTools int
	var receivedToolChoice any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if tools, ok := req["tools"].([]any); ok {
			receivedTools = len(tools)
		}
		receivedToolChoice = req["tool_choice"]
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	p := newTestOpenAI(srv.URL+"/v1", "gpt-4o")
	ch, err := p.StreamCompletion(context.Background(), []Message{
		{Role: RoleUser, Content: "hi"},
	}, CompletionOpts{
		Tools: []Tool{
			{Name: "read_file", Description: "Read a file", Parameters: json.RawMessage(`{"type":"object"}`)},
			{Name: "search", Description: "Search", Parameters: json.RawMessage(`{"type":"object"}`)},
		},
		ToolChoice: "auto",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for range ch {
	}

	if receivedTools != 2 {
		t.Errorf("expected 2 tools in request, got %d", receivedTools)
	}
	if receivedToolChoice != "auto" {
		t.Errorf("expected tool_choice 'auto', got %v", receivedToolChoice)
	}
}

func TestOpenAI_Registration(t *testing.T) {
	names := Available()
	found := false
	for _, n := range names {
		if n == "openai" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'openai' to be registered")
	}
}

// captureChatRequest runs one completion against a server that decodes the
// request body and answers with an empty stream, and hands back what went
// out on the wire. Decoding into a map rather than the SDK's request type is
// the point: a field the struct would fill in with its zero value has to be
// distinguishable from one that was never sent.
func captureChatRequest(t *testing.T, run func(baseURL string) (<-chan StreamEvent, error)) map[string]any {
	t.Helper()
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	ch, err := run(srv.URL + "/v1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for range ch {
	}
	return body
}

func TestOpenAI_StreamCompletion_CeilingIsCompletionTokens(t *testing.T) {
	// Both models, because the field is OpenAI's own current spelling and
	// not a reasoning-only accommodation: a reasoning model refuses the old
	// one outright, and the rest have deprecated it.
	for _, model := range []string{"o3", "gpt-4o"} {
		body := captureChatRequest(t, func(baseURL string) (<-chan StreamEvent, error) {
			return newTestOpenAI(baseURL, model).StreamCompletion(
				context.Background(),
				[]Message{{Role: RoleUser, Content: "hi"}},
				CompletionOpts{MaxTokens: 8192},
			)
		})
		if got := body["max_completion_tokens"]; got != float64(8192) {
			t.Errorf("%s: max_completion_tokens = %v, want 8192", model, got)
		}
		if got, ok := body["max_tokens"]; ok {
			t.Errorf("%s: request carries the deprecated max_tokens = %v", model, got)
		}
	}
}

func TestToOpenAIMessages_ToolResultStaysAString(t *testing.T) {
	msgs := toOpenAIMessages([]Message{
		{Role: RoleTool, Content: "logo.png is an image", ToolCallID: "t1", Attachments: []Attachment{
			{Kind: AttachmentImage, Name: "logo.png", MediaType: "image/png", Data: []byte{1, 2, 3}},
		}},
	})
	if len(msgs[0].MultiContent) != 0 {
		t.Fatalf("a tool result must not become a parts array here, got %d parts", len(msgs[0].MultiContent))
	}
	if msgs[0].Content != "logo.png is an image" {
		t.Errorf("expected the notice as the content, got %q", msgs[0].Content)
	}
}
