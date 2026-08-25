package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func modelsServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/models") {
			t.Errorf("expected the models endpoint, got %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestOpenAICompat_ListModels(t *testing.T) {
	srv := modelsServer(t, `{"object":"list","data":[
		{"id":"qwen3:8b"},{"id":"llama3"},{"id":"nomic-embed-text"}]}`)

	p := newTestCompat(srv.URL+"/v1", "llama3")
	names, err := p.ListModels(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(names) != 2 || names[0] != "llama3" || names[1] != "qwen3:8b" {
		t.Fatalf("expected the chat models sorted, got %v", names)
	}
}

func TestOpenAICompat_ListModelsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"bad key"}}`))
	}))
	defer srv.Close()

	p := newTestCompat(srv.URL+"/v1", "llama3")
	if _, err := p.ListModels(context.Background()); err == nil {
		t.Fatal("expected an error from a 401")
	}
}

func TestOpenAI_ListModelsSatisfiesModelLister(t *testing.T) {
	srv := modelsServer(t, `{"object":"list","data":[{"id":"gpt-4o"},{"id":"tts-1"}]}`)

	for name, p := range map[string]Provider{
		"openai":     newTestOpenAI(srv.URL+"/v1", "gpt-4o"),
		"openrouter": newTestOpenRouter(srv.URL+"/v1", "openai/gpt-4o"),
		"compat":     newTestCompat(srv.URL+"/v1", "llama3"),
	} {
		lister, ok := p.(ModelLister)
		if !ok {
			t.Fatalf("%s should implement ModelLister", name)
		}
		names, err := lister.ListModels(context.Background())
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", name, err)
		}
		if len(names) != 1 || names[0] != "gpt-4o" {
			t.Fatalf("%s: expected the speech model filtered out, got %v", name, names)
		}
	}
}

func TestAnthropicIsNotAModelLister(t *testing.T) {
	// The Messages API has no models endpoint; the curated catalog covers it.
	var p Provider = &Anthropic{}
	if _, ok := p.(ModelLister); ok {
		t.Fatal("anthropic should not claim live model discovery")
	}
}

func TestChatModels_KeepsEverythingWhenAllFiltered(t *testing.T) {
	names := []string{"my-embed-model", "text-embedding-3-small"}
	if got := chatModels(names); len(got) != 2 {
		t.Fatalf("an all-filtered list should be kept whole, got %v", got)
	}
}

func TestChatModels_Filters(t *testing.T) {
	names := []string{"gpt-4o", "whisper-1", "dall-e-3", "o3", "text-embedding-3-large", "tts-1-hd"}
	got := chatModels(names)
	if len(got) != 2 || got[0] != "gpt-4o" || got[1] != "o3" {
		t.Fatalf("expected only the chat models, got %v", got)
	}
}
