package profile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/provider"
)

func writeProfile(t *testing.T, dir, name, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const gatewayTOML = `
name        = "gateway"
api         = "openai-chat"
base_url    = "https://llm-gateway.internal/v1"
api_key_env = "GATEWAY_API_KEY"
models_path = "/v1/models/simple"

[headers]
X-Title = "shhh"

[[models]]
id             = "gemini-3.1-pro"
context_window = 1048576
max_tokens     = 65536
cost           = { input = 2.0, output = 12.0, cache_read = 0.2 }

[[models]]
id = "kimi-k2.6"

[[rewrite]]
when  = { model = "gemini-*" }
op    = "cut-at"
path  = "messages[].tool_calls[].id"
value = "__thought__"
note  = "LiteLLM fabricates thought signatures Vertex then rejects."

[[rewrite]]
when      = { model = "claude-*" }
direction = "response"
op        = "delete"
path      = "top_p"
`

func TestLoadFile_ReadsAGatewayProfile(t *testing.T) {
	path := writeProfile(t, t.TempDir(), "gateway.toml", gatewayTOML)
	loaded, err := LoadFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("want one profile, got %d", len(loaded))
	}
	p := loaded[0]
	if p.Name != "gateway" || p.API != APIOpenAIChat {
		t.Fatalf("unexpected identity: %+v", p)
	}
	if p.Headers["X-Title"] != "shhh" || p.ModelsPath != "/v1/models/simple" {
		t.Fatalf("unexpected transport config: %+v", p)
	}
	if len(p.Models) != 2 || p.Models[0].ContextWindow != 1048576 || p.Models[0].Cost.Input != 2.0 {
		t.Fatalf("unexpected models: %+v", p.Models)
	}
	if p.Models[0].Cost.CacheRead != 0.2 || p.Models[0].MaxTokens != 65536 {
		t.Fatalf("declared metadata should survive the round trip: %+v", p.Models[0])
	}
	if len(p.Rewrite) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(p.Rewrite))
	}
	if p.Rewrite[0].Direction != DirectionRequest {
		t.Fatalf("an omitted direction should default to request, got %q", p.Rewrite[0].Direction)
	}
	if p.Rewrite[1].Direction != DirectionResponse {
		t.Fatalf("an explicit direction should survive, got %q", p.Rewrite[1].Direction)
	}
	if p.Rewrite[0].Note == "" {
		t.Fatal("the note should survive — it is why the rule exists")
	}
}

func TestLoadFile_NameDefaultsToTheFilename(t *testing.T) {
	path := writeProfile(t, t.TempDir(), "corp-gateway.toml", `base_url = "https://gw.example/v1"`)
	loaded, err := LoadFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("want one profile, got %d", len(loaded))
	}
	p := loaded[0]
	if p.Name != "corp-gateway" {
		t.Fatalf("expected the filename as the name, got %q", p.Name)
	}
	if p.API != APIOpenAIChat {
		t.Fatalf("expected the default dialect, got %q", p.API)
	}
}

func TestLoadFile_Rejects(t *testing.T) {
	tests := map[string]struct{ body, want string }{
		"no base url":     {`name = "x"`, "base_url"},
		"unknown api":     {"name = \"x\"\nbase_url = \"u\"\napi = \"grpc\"", "not supported"},
		"both key forms":  {"name = \"x\"\nbase_url = \"u\"\napi_key = \"k\"\napi_key_env = \"E\"", "not both"},
		"model with none": {"name = \"x\"\nbase_url = \"u\"\n[[models]]\ncontext_window = 1", "id is required"},
		"bad rule":        {"name = \"x\"\nbase_url = \"u\"\n[[rewrite]]\nop = \"explode\"\npath = \"a\"", "rewrite[0]"},
		"bad toml":        {"name = ", "expected"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			path := writeProfile(t, t.TempDir(), "bad.toml", tc.body)
			_, err := LoadFile(path)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error should name the problem (%q), got %v", tc.want, err)
			}
			if !strings.Contains(err.Error(), path) {
				t.Fatalf("error should name the file, got %v", err)
			}
		})
	}
}

func TestLoad_SkipsBadFilesAndKeepsGoing(t *testing.T) {
	dir := t.TempDir()
	writeProfile(t, dir, "gateway.toml", gatewayTOML)
	writeProfile(t, dir, "broken.toml", `name = "broken"`)
	writeProfile(t, dir, "notes.txt", "ignored")

	profiles, errs := Load([]string{dir})
	if len(profiles) != 1 || profiles[0].Name != "gateway" {
		t.Fatalf("the good profile should load, got %+v", profiles)
	}
	if len(errs) != 1 {
		t.Fatalf("the bad profile should be reported, got %v", errs)
	}
}

func TestLoad_FirstDirectoryWins(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	writeProfile(t, first, "gateway.toml", "name = \"gateway\"\nbase_url = \"https://first.example/v1\"")
	writeProfile(t, second, "gateway.toml", "name = \"gateway\"\nbase_url = \"https://second.example/v1\"")

	profiles, errs := Load([]string{first, second})
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(profiles) != 1 || profiles[0].BaseURL != "https://first.example/v1" {
		t.Fatalf("the higher-priority directory should win, got %+v", profiles)
	}
}

func TestLoad_MissingDirectoryIsNotAnError(t *testing.T) {
	profiles, errs := Load([]string{filepath.Join(t.TempDir(), "nope")})
	if len(profiles) != 0 || len(errs) != 0 {
		t.Fatalf("an absent profile directory is the normal case, got %v / %v", profiles, errs)
	}
}

func TestKey_ReadsTheNamedEnvironmentVariable(t *testing.T) {
	t.Setenv("GATEWAY_API_KEY", "secret")
	p := Profile{APIKeyEnv: "GATEWAY_API_KEY"}
	if p.Key() != "secret" {
		t.Fatalf("expected the env value, got %q", p.Key())
	}
	if (Profile{APIKey: "literal"}).Key() != "literal" {
		t.Fatal("a literal key should be used as-is")
	}
	if (Profile{}).Key() != "" {
		t.Fatal("no key configured means no key")
	}
}

func TestRegister_MakesTheProfileResolvable(t *testing.T) {
	t.Setenv("GATEWAY_API_KEY", "secret")
	path := writeProfile(t, t.TempDir(), "gateway.toml", gatewayTOML)
	loaded, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	p := loaded[0]
	Register([]Profile{p})

	prov, err := provider.Resolve("gateway", provider.ResolveOpts{Model: "kimi-k2.6"})
	if err != nil {
		t.Fatalf("the profile should resolve as a provider: %v", err)
	}
	if prov.Name() != "gateway" {
		t.Fatalf("expected the profile's name, got %q", prov.Name())
	}
	if models := provider.KnownModels("gateway"); len(models) != 2 || models[0] != "gemini-3.1-pro" {
		t.Fatalf("the declared models should seed the picker, got %v", models)
	}
	if d := provider.Defaults("gateway"); d.Model != "gemini-3.1-pro" || d.BaseURL != p.BaseURL {
		t.Fatalf("unexpected defaults: %+v", d)
	}
	if got := Loaded(); len(got) != 1 || got[0].Name != "gateway" {
		t.Fatalf("registered profiles should be readable back, got %+v", got)
	}
}

func TestNew_ReportsAMissingKey(t *testing.T) {
	t.Setenv("GATEWAY_API_KEY", "")
	p := Profile{Name: "gateway", API: APIOpenAIChat, BaseURL: "https://gw.example/v1", APIKeyEnv: "GATEWAY_API_KEY"}
	_, err := New(p, provider.ResolveOpts{})
	if err == nil || !strings.Contains(err.Error(), "GATEWAY_API_KEY") {
		t.Fatalf("the error should name the variable to export, got %v", err)
	}
}

func TestNew_AnthropicDialect(t *testing.T) {
	p := Profile{Name: "gateway-claude", API: APIAnthropicMessage, BaseURL: "https://gw.example/anthropic", APIKey: "k"}
	prov, err := New(p, provider.ResolveOpts{Model: "claude-opus-5"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if prov.Name() != "gateway-claude" {
		t.Fatalf("expected the profile's name, got %q", prov.Name())
	}
}

func TestListModels_UsesTheProfilesCatalogPath(t *testing.T) {
	var path string
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		auth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"gemini-3.1-pro"},{"id":"kimi-k2.6"}]}`))
	}))
	defer srv.Close()

	p := Profile{Name: "gw", API: APIOpenAIChat, BaseURL: srv.URL + "/v1", APIKey: "secret", ModelsPath: "/v1/models/simple"}
	prov, err := New(p, provider.ResolveOpts{Model: "kimi-k2.6"})
	if err != nil {
		t.Fatal(err)
	}
	lister, ok := prov.(provider.ModelLister)
	if !ok {
		t.Fatal("a profile-backed provider should support discovery")
	}
	names, err := lister.ListModels(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(names) != 2 || names[0] != "gemini-3.1-pro" {
		t.Fatalf("unexpected catalog: %v", names)
	}
	if path != "/v1/models/simple" {
		t.Fatalf("the profile's catalog path should be used, got %q", path)
	}
	if auth != "Bearer secret" {
		t.Fatalf("the catalog request should be authenticated, got %q", auth)
	}
}

func TestListModels_FallsBackToTheStandardEndpoint(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"llama3"}]}`))
	}))
	defer srv.Close()

	p := Profile{Name: "gw", API: APIOpenAIChat, BaseURL: srv.URL + "/v1", APIKey: "k"}
	prov, _ := New(p, provider.ResolveOpts{Model: "llama3"})
	names, err := prov.(provider.ModelLister).ListModels(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(names) != 1 || names[0] != "llama3" || path != "/v1/models" {
		t.Fatalf("expected the standard endpoint, got %v from %q", names, path)
	}
}

func TestParseCatalog_AcceptsTheShapesGatewaysReturn(t *testing.T) {
	tests := map[string]string{
		"wrapped objects": `{"data":[{"id":"a"},{"id":"b"}]}`,
		"bare objects":    `[{"id":"a"},{"id":"b"}]`,
		"bare strings":    `["a","b"]`,
		"named only":      `{"data":[{"name":"a"},{"name":"b"}]}`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := parseCatalog([]byte(body))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != 2 || got[0] != "a" || got[1] != "b" {
				t.Fatalf("unexpected ids: %v", got)
			}
		})
	}
	if _, err := parseCatalog([]byte(`{"models":{"a":1}}`)); err == nil {
		t.Fatal("an unrecognized shape should be an error, not a silent empty list")
	}
}

func TestDiscoveryURL(t *testing.T) {
	tests := []struct{ base, path, want string }{
		{"https://gw.example/v1", "/v1/models/simple", "https://gw.example/v1/models/simple"},
		{"https://gw.example/v1", "models", "https://gw.example/v1/models"},
		{"https://gw.example/api/v2", "/models", "https://gw.example/models"},
	}
	for _, tc := range tests {
		got, err := discoveryURL(Endpoint{BaseURL: tc.base, ModelsPath: tc.path})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != tc.want {
			t.Fatalf("base %q + path %q: got %q, want %q", tc.base, tc.path, got, tc.want)
		}
	}
}

func TestPricing_ConvertsToPerTokenCosts(t *testing.T) {
	profiles := []Profile{{
		Name: "gw",
		Models: []Model{
			{ID: "gemini-3.1-pro", ContextWindow: 1048576, Cost: Cost{Input: 2.0, Output: 12.0}},
			{ID: "window-only", ContextWindow: 65536},
			{ID: "bare-id"},
		},
	}}
	got := Pricing(profiles)
	if len(got) != 2 {
		t.Fatalf("a model with no metadata should be left to the public table, got %v", got)
	}
	p := got["gemini-3.1-pro"]
	if p.InputCostPerToken != 2.0/1_000_000 || p.OutputCostPerToken != 12.0/1_000_000 {
		t.Fatalf("per-million prices should become per-token: %+v", p)
	}
	if p.MaxInputTokens != 1048576 {
		t.Fatalf("unexpected context window: %+v", p)
	}
}

func TestPricing_OverlayKeepsWhatAProfileOmits(t *testing.T) {
	table := pricing.NewTable(map[string]pricing.ModelPricing{
		"gpt-4o": {InputCostPerToken: 0.0000025, OutputCostPerToken: 0.00001, MaxInputTokens: 128000},
	})
	table.Overlay(Pricing([]Profile{{Models: []Model{{ID: "gpt-4o", ContextWindow: 200000}}}}))

	in, out, ok := table.Cost("gpt-4o", 1_000_000, 1_000_000)
	if !ok || in != 2.5 || out != 10 {
		t.Fatalf("the public prices should survive a metadata-only override: %v %v %v", in, out, ok)
	}
	window, _ := table.ContextWindow("gpt-4o")
	if window != 200000 {
		t.Fatalf("the profile's context window should win, got %d", window)
	}
}

func TestDirs_SitBesideEachConfigFile(t *testing.T) {
	got := Dirs([]string{"/a/shhh/config.toml", "/b/shhh/config.toml", "/a/shhh/config.toml"})
	want := []string{filepath.Join("/a/shhh", "providers"), filepath.Join("/b/shhh", "providers")}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// --- the Responses dialect
// --------------------------------------------------

func TestNew_ResponsesDialect(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Errorf("expected the responses endpoint, got %q", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n")
		fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
	}))
	defer srv.Close()

	p := Profile{
		Name:    "gateway",
		API:     APIOpenAIResponses,
		BaseURL: srv.URL + "/v1",
		APIKey:  "k",
		Headers: map[string]string{"X-Title": "shhh"},
		Rewrite: []Rule{
			// A reasoning model needs an effort setting the vanilla request
			// has no place for, and rejects the temperature it never asked
			// for. Both are profile edits, not provider code.
			{When: Match{Model: "gpt-5*"}, Direction: DirectionRequest, Op: OpSet, Path: "reasoning.effort", Value: "high"},
			{When: Match{Model: "gpt-5*"}, Direction: DirectionRequest, Op: OpDelete, Path: "temperature"},
		},
	}
	prov, err := New(p, provider.ResolveOpts{Model: "gpt-5.6-terra"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if prov.Name() != "gateway" {
		t.Fatalf("expected the profile's name, got %q", prov.Name())
	}

	temp := 0.5
	ch, err := prov.StreamCompletion(context.Background(), []provider.Message{
		{Role: provider.RoleUser, Content: "hi"},
	}, provider.CompletionOpts{Temperature: &temp})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var text string
	for ev := range ch {
		if ev.Err != nil {
			t.Fatalf("unexpected stream error: %v", ev.Err)
		}
		text += ev.Token
	}
	if text != "ok" {
		t.Fatalf("expected the streamed token, got %q", text)
	}
	if _, present := got["temperature"]; present {
		t.Fatal("the rule should have removed temperature before it reached the gateway")
	}
	reasoning, ok := got["reasoning"].(map[string]any)
	if !ok || reasoning["effort"] != "high" {
		t.Fatalf("the rule should have added the reasoning block, got %v", got["reasoning"])
	}
}

func TestValidate_AcceptsTheResponsesDialect(t *testing.T) {
	path := writeProfile(t, t.TempDir(), "gw.toml", "base_url = \"https://gw.example/v1\"\napi = \"openai-responses\"")
	loaded, err := LoadFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("want one profile, got %d", len(loaded))
	}
	p := loaded[0]
	if p.API != APIOpenAIResponses {
		t.Fatalf("unexpected dialect: %q", p.API)
	}
}

// A gateway is where an unclassified failure would hurt most: the endpoint is
// someone else's, its error prose is its own, and the session has no other
// way to find out what went wrong. So each dialect a profile can speak is
// pointed at a server that fails, and the failure has to arrive named — and
// named as the profile rather than as the built-in dialect underneath it.
func TestNew_ClassifiesGatewayFailures(t *testing.T) {
	cases := []struct {
		name    string
		api     string
		path    string
		status  int
		body    string
		want    provider.Class
		wantErr error
	}{
		{
			name: "openai chat", api: APIOpenAIChat, path: "/v1/chat/completions",
			status: http.StatusTooManyRequests,
			body:   `{"error":{"message":"Rate limit reached for this gateway","type":"rate_limit_error"}}`,
			want:   provider.ClassRateLimit, wantErr: provider.ErrRateLimited,
		},
		{
			name: "openai responses", api: APIOpenAIResponses, path: "/v1/responses",
			status: http.StatusUnauthorized,
			body:   `{"error":{"message":"Incorrect API key provided","type":"invalid_request_error"}}`,
			want:   provider.ClassAuth, wantErr: provider.ErrAuth,
		},
		{
			name: "anthropic messages", api: APIAnthropicMessage, path: "/v1/messages",
			status: 529,
			body:   `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`,
			want:   provider.ClassOverloaded, wantErr: provider.ErrOverloaded,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			prov, err := New(Profile{
				Name:    "gateway",
				API:     tc.api,
				BaseURL: srv.URL + "/v1",
				APIKey:  "sk-gateway-4f9c",
			}, provider.ResolveOpts{Model: "some-model"})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// The chat dialects report the failure when the request is made;
			// the Messages API reports it on the stream. Either way it has to
			// arrive classified.
			ch, err := prov.StreamCompletion(context.Background(), []provider.Message{
				{Role: provider.RoleUser, Content: "hi"},
			}, provider.CompletionOpts{})
			if err == nil {
				if ch == nil {
					t.Fatal("expected either an error or a stream")
				}
				for ev := range ch {
					if ev.Err != nil {
						err = ev.Err
						break
					}
				}
			}
			if err == nil {
				t.Fatal("expected the gateway's failure to reach the caller")
			}

			f, ok := provider.AsFailure(err)
			if !ok {
				t.Fatalf("a gateway failure reached the caller unclassified: %v", err)
			}
			if f.Class != tc.want {
				t.Errorf("class = %q, want %q (message %q)", f.Class, tc.want, f.Message)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("errors.Is did not match the class sentinel: %v", err)
			}
			if f.Provider != "gateway" {
				t.Errorf("provider = %q, want the profile's own name", f.Provider)
			}
		})
	}
}
