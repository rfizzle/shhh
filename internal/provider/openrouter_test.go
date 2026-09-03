package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

func newTestOpenRouter(baseURL string, model string) *OpenRouter {
	cfg := openai.DefaultConfig("test-key")
	cfg.BaseURL = baseURL
	cfg.HTTPClient = &http.Client{
		Transport: &openRouterTransport{base: http.DefaultTransport},
	}
	return NewOpenRouterWith(openai.NewClientWithConfig(cfg), model)
}

func TestOpenRouter_Name(t *testing.T) {
	p := newTestOpenRouter("http://unused", "")
	if p.Name() != "openrouter" {
		t.Errorf("expected 'openrouter', got %q", p.Name())
	}
}

func TestOpenRouter_DefaultModel(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "test-key")
	p, err := NewOpenRouter(ResolveOpts{APIKey: "test-key"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.model != "anthropic/claude-sonnet-4-6" {
		t.Errorf("expected default model 'anthropic/claude-sonnet-4-6', got %q", p.model)
	}
}

func TestNewOpenRouter_MissingKey(t *testing.T) {
	t.Setenv("SHHH_API_KEY", "")
	t.Setenv("OPENROUTER_API_KEY", "")
	if err := os.Unsetenv("OPENROUTER_API_KEY"); err != nil {
		t.Fatal(err)
	}
	_, err := NewOpenRouter(ResolveOpts{})
	if err == nil {
		t.Fatal("expected error for missing API key")
	}
}

func TestNewOpenRouter_EnvKey(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "env-key")
	p, err := NewOpenRouter(ResolveOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
}

func TestOpenRouter_CustomModel(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "test-key")
	p, err := NewOpenRouter(ResolveOpts{APIKey: "test-key", Model: "meta-llama/llama-3-70b"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.model != "meta-llama/llama-3-70b" {
		t.Errorf("expected model 'meta-llama/llama-3-70b', got %q", p.model)
	}
}

func TestOpenRouter_SendsRequiredHeaders(t *testing.T) {
	var gotReferer, gotTitle string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotReferer = r.Header.Get("HTTP-Referer")
		gotTitle = r.Header.Get("X-Title")
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	p := newTestOpenRouter(srv.URL+"/v1", "test-model")
	ch, err := p.StreamCompletion(context.Background(), []Message{
		{Role: RoleUser, Content: "hi"},
	}, CompletionOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for range ch {
	}

	if gotReferer != "https://github.com/rfizzle/shhh" {
		t.Errorf("expected HTTP-Referer header, got %q", gotReferer)
	}
	if gotTitle != "shhh" {
		t.Errorf("expected X-Title header, got %q", gotTitle)
	}
}

func TestOpenRouter_StreamCompletion(t *testing.T) {
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

	p := newTestOpenRouter(srv.URL+"/v1", "test-model")
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

func TestOpenRouter_StreamCompletion_OptsOverrideModel(t *testing.T) {
	var receivedModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req openai.ChatCompletionRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		receivedModel = req.Model
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	p := newTestOpenRouter(srv.URL+"/v1", "default-model")
	ch, err := p.StreamCompletion(context.Background(), []Message{
		{Role: RoleUser, Content: "hi"},
	}, CompletionOpts{Model: "override-model"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for range ch {
	}
	if receivedModel != "override-model" {
		t.Errorf("expected model 'override-model', got %q", receivedModel)
	}
}

func TestOpenRouter_StreamCompletion_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"message": "Invalid API key",
				"type":    "invalid_request_error",
			},
		})
	}))
	defer srv.Close()

	p := newTestOpenRouter(srv.URL+"/v1", "test-model")
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

func TestOpenRouter_StreamCompletion_RateLimited(t *testing.T) {
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

	p := newTestOpenRouter(srv.URL+"/v1", "test-model")
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

func TestOpenRouter_Registration(t *testing.T) {
	names := Available()
	found := false
	for _, n := range names {
		if n == "openrouter" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'openrouter' to be registered")
	}
}

func TestOpenRouter_StreamCompletion_CeilingIsCompletionTokens(t *testing.T) {
	// The gateway speaks OpenAI's chat-completions dialect and passes the
	// request through, so the deprecated field is refused here for the same
	// reason it is refused upstream.
	for _, model := range []string{"openai/o3", "openai/gpt-4o"} {
		body := captureChatRequest(t, func(baseURL string) (<-chan StreamEvent, error) {
			return newTestOpenRouter(baseURL, model).StreamCompletion(
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
