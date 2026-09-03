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

// The family floor for a model the fetched table has no key for. Wrong-low is
// the failure that matters here: it trims a conversation the model had room
// to keep.
func TestContextWindowFor(t *testing.T) {
	for _, tc := range []struct {
		model string
		want  int64
		known bool
	}{
		{"claude-opus-5", 1_000_000, true},
		{"claude-sonnet-5-20260101", 1_000_000, true},
		{"claude-3-5-sonnet", 200_000, true},
		{"claude-opus-4-5", 200_000, true},
		{"claude-opus-4-7", 1_000_000, true},
		{"claude-haiku-4-5", 200_000, true},
		{"anthropic/claude-opus-5-thinking", 1_000_000, true},
		{"llama3.1:70b", 128_000, true},
		{"llama3", 8_192, true},
		{"llama2:13b", 4_096, true},
		{"llama4:scout", 128_000, true},
		{"meta-llama/Meta-Llama-3.3-70B-Instruct", 128_000, true},
		{"qwen2.5-coder:7b", 32_768, true},
		{"deepseek-chat", 65_536, true},
		{"mistral-nemo", 32_768, true},
		{"mixtral:8x7b", 32_768, true},
		{"gemma3:27b", 128_000, true},
		{"gemma2:9b", 8_192, true},
		{"phi4", 16_384, true},
		{"phi3:medium", 4_096, true},
		// The spellings that must not move: a digit that is already
		// separated, and one that never was.
		{"gpt-4o-mini", 128_000, true},
		{"o3-mini", 200_000, true},
		{"my-own-finetune", 0, false},
		{"", 0, false},
	} {
		got, ok := ContextWindowFor(tc.model)
		if ok != tc.known || got != tc.want {
			t.Errorf("ContextWindowFor(%q) = %d, %v; want %d, %v", tc.model, got, ok, tc.want, tc.known)
		}
	}
}

func TestOpenAICompat_ModelWindows(t *testing.T) {
	srv := modelsServer(t, `{"object":"list","data":[
		{"id":"Qwen/Qwen3-8B","max_model_len":262144},
		{"id":"local.gguf","meta":{"n_ctx":8192,"n_ctx_train":131072}},
		{"id":"lmstudio-model","max_context_length":32768},
		{"id":"llama3"}]}`)

	p, err := NewOpenAICompat(ResolveOpts{BaseURL: srv.URL + "/v1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	windows, err := p.ModelWindows(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for id, want := range map[string]int64{
		"qwen/qwen3-8b":  262_144,
		"local.gguf":     8_192,
		"lmstudio-model": 32_768,
	} {
		if got := windows[id]; got != want {
			t.Errorf("window for %q = %d, want %d", id, got, want)
		}
	}
	// A runtime that reports no length is not an answer, and the table and
	// the family floor take it from there.
	if _, ok := windows["llama3"]; ok {
		t.Errorf("a model with no reported length should be absent, got %v", windows)
	}
}

// A provider built over somebody else's client has no transport of its own to
// ask with, and says so rather than guessing.
func TestOpenAICompat_ModelWindowsWithoutATransport(t *testing.T) {
	windows, err := newTestCompat("http://unused", "llama3").ModelWindows(context.Background())
	if err != nil || windows != nil {
		t.Fatalf("expected no answer and no error, got %v, %v", windows, err)
	}
}

func TestOpenAICompat_ModelWindowsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	p, err := NewOpenAICompat(ResolveOpts{BaseURL: srv.URL + "/v1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := p.ModelWindows(context.Background()); err == nil {
		t.Fatal("expected an error from an endpoint with no catalog")
	}
}

func TestOpenAICompatIsAModelWindower(t *testing.T) {
	var p Provider = &OpenAICompat{}
	if _, ok := p.(ModelWindower); !ok {
		t.Fatal("the compat provider should answer for its endpoint's windows")
	}
}
