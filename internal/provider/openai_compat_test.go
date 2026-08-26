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

func newTestCompat(baseURL, model string) *OpenAICompat {
	cfg := openai.DefaultConfig("test-key")
	cfg.BaseURL = baseURL
	return NewOpenAICompatWith(openai.NewClientWithConfig(cfg), model, baseURL)
}

func TestOpenAICompat_Name(t *testing.T) {
	p := newTestCompat("http://unused", "llama3")
	if p.Name() != "openai-compatible" {
		t.Errorf("expected 'openai-compatible', got %q", p.Name())
	}
}

func TestOpenAICompat_DefaultModel(t *testing.T) {
	p := NewOpenAICompatWith(openai.NewClient("fake"), "", "http://localhost:11434/v1")
	if p.model != "llama3" {
		t.Errorf("expected default model 'llama3', got %q", p.model)
	}
}

func TestNewOpenAICompat_Defaults(t *testing.T) {
	t.Setenv("SHHH_API_KEY", "")
	t.Setenv("SHHH_BASE_URL", "")

	p, err := NewOpenAICompat(ResolveOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.baseURL != "http://localhost:11434/v1" {
		t.Errorf("expected default base URL, got %q", p.baseURL)
	}
	if p.model != "llama3" {
		t.Errorf("expected default model 'llama3', got %q", p.model)
	}
}

func TestNewOpenAICompat_EnvOverrides(t *testing.T) {
	t.Setenv("SHHH_BASE_URL", "http://myhost:8080/v1")
	t.Setenv("SHHH_API_KEY", "my-key")

	p, err := NewOpenAICompat(ResolveOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.baseURL != "http://myhost:8080/v1" {
		t.Errorf("expected custom base URL, got %q", p.baseURL)
	}
}

func TestNewOpenAICompat_OptsOverrideEnvAndDefaults(t *testing.T) {
	t.Setenv("SHHH_BASE_URL", "http://env-host:9999/v1")

	p, err := NewOpenAICompat(ResolveOpts{
		BaseURL: "http://flag-host:1234/v1",
		Model:   "flag-model",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.baseURL != "http://flag-host:1234/v1" {
		t.Errorf("expected flag base URL, got %q", p.baseURL)
	}
	if p.model != "flag-model" {
		t.Errorf("expected flag model, got %q", p.model)
	}
}

func TestNewOpenAICompat_NoKeyRequired(t *testing.T) {
	t.Setenv("SHHH_API_KEY", "")
	_, err := NewOpenAICompat(ResolveOpts{})
	if err != nil {
		t.Fatalf("compat provider should not require an API key, got: %v", err)
	}
}

func TestOpenAICompat_StreamCompletion(t *testing.T) {
	tokens := []string{"foo", " bar"}
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

	p := newTestCompat(srv.URL+"/v1", "llama3")
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
	if got != "foo bar" {
		t.Errorf("expected 'foo bar', got %q", got)
	}
}

func TestOpenAICompat_StreamCompletion_ModelOverride(t *testing.T) {
	var receivedModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req openai.ChatCompletionRequest
		json.NewDecoder(r.Body).Decode(&req)
		receivedModel = req.Model
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	p := newTestCompat(srv.URL+"/v1", "llama3")
	ch, err := p.StreamCompletion(context.Background(), []Message{
		{Role: RoleUser, Content: "hi"},
	}, CompletionOpts{Model: "codellama"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for range ch {
	}
	if receivedModel != "codellama" {
		t.Errorf("expected model 'codellama', got %q", receivedModel)
	}
}

func TestOpenAICompat_StreamCompletion_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"message": "Invalid key",
				"type":    "invalid_request_error",
			},
		})
	}))
	defer srv.Close()

	p := newTestCompat(srv.URL+"/v1", "llama3")
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

func TestOpenAICompat_BaseURLPassedToServer(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	p := newTestCompat(srv.URL+"/custom/v1", "llama3")
	ch, err := p.StreamCompletion(context.Background(), []Message{
		{Role: RoleUser, Content: "hi"},
	}, CompletionOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for range ch {
	}
	if gotPath != "/custom/v1/chat/completions" {
		t.Errorf("expected path '/custom/v1/chat/completions', got %q", gotPath)
	}
}

func TestOpenAICompat_Registration(t *testing.T) {
	names := Available()
	found := false
	for _, n := range names {
		if n == "openai-compatible" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'openai-compatible' to be registered")
	}
}
