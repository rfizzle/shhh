package profile

// Endpoints inside one provider (S-142): which address a model routes to,
// what an endpoint inherits, and that the request actually lands there.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/provider"
)

const routedTOML = `
name        = "gateway"
base_url    = "https://gw.example/v1"
api_key_env = "GATEWAY_API_KEY"
models_path = "/v1/models/simple"

[headers]
X-Title = "shhh"

[[models]]
id = "gpt-5.2"

[[rewrite]]
op    = "delete"
path  = "top_p"
note  = "the gateway rejects top_p on everything"

[[endpoint]]
match    = ["claude-*"]
api      = "anthropic-messages"
base_url = "https://gw.example/anthropic"

  [endpoint.headers]
  anthropic-beta = "context-1m-2025-08-07"

  [[endpoint.models]]
  id             = "claude-opus-5"
  context_window = 1000000
  cost           = { input = 5.0, output = 25.0 }

  [[endpoint.rewrite]]
  op   = "delete"
  path = "frequency_penalty"

[[endpoint]]
api      = "openai-responses"
base_url = "https://gw.example/responses"

  [[endpoint.models]]
  id = "o5-pro"
`

func routedProfile(t *testing.T) Profile {
	t.Helper()
	path := writeProfile(t, t.TempDir(), "gateway.toml", routedTOML)
	loaded, err := LoadFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return loaded[0]
}

func TestRoute_SendsEachModelToItsEndpoint(t *testing.T) {
	p := routedProfile(t)
	tests := []struct{ model, wantURL, wantAPI string }{
		// A declared id is the strongest claim.
		{"claude-opus-5", "https://gw.example/anthropic", APIAnthropicMessage},
		{"o5-pro", "https://gw.example/responses", APIOpenAIResponses},
		// A glob catches the ones nobody enumerated.
		{"claude-haiku-4-5", "https://gw.example/anthropic", APIAnthropicMessage},
		// Everything else — declared at the top level or not declared at
		// all — goes to the default.
		{"gpt-5.2", "https://gw.example/v1", APIOpenAIChat},
		{"something-nobody-listed", "https://gw.example/v1", APIOpenAIChat},
		{"", "https://gw.example/v1", APIOpenAIChat},
	}
	for _, tc := range tests {
		got := p.Route(tc.model)
		if got.BaseURL != tc.wantURL || got.API != tc.wantAPI {
			t.Fatalf("%q routed to %s (%s), want %s (%s)", tc.model, got.BaseURL, got.API, tc.wantURL, tc.wantAPI)
		}
	}
}

func TestRoute_EndpointInheritsWhatItDoesNotSay(t *testing.T) {
	t.Setenv("GATEWAY_API_KEY", "secret")
	e := routedProfile(t).Route("claude-opus-5")

	if e.Key() != "secret" {
		t.Fatalf("the endpoint should inherit the profile's key, got %q", e.Key())
	}
	if e.ModelsPath != "/v1/models/simple" {
		t.Fatalf("the endpoint should inherit models_path, got %q", e.ModelsPath)
	}
	if e.Headers["X-Title"] != "shhh" || e.Headers["anthropic-beta"] == "" {
		t.Fatalf("headers should merge, got %v", e.Headers)
	}
	// The profile's rule runs first, then the endpoint's own.
	if len(e.Rewrite) != 2 || e.Rewrite[0].Path != "top_p" || e.Rewrite[1].Path != "frequency_penalty" {
		t.Fatalf("unexpected rule order: %+v", e.Rewrite)
	}
	if e.Label != "https://gw.example/anthropic" {
		t.Fatalf("an unlabelled endpoint should show its address, got %q", e.Label)
	}
}

func TestRoute_EndpointOverridesWhatItDoesSay(t *testing.T) {
	p := Profile{
		Name: "gw", BaseURL: "https://gw.example/v1", APIKeyEnv: "SHARED_KEY",
		Headers: map[string]string{"X-Title": "shhh", "X-Tier": "default"},
		Endpoints: []Endpoint{{
			Match: []string{"private-*"}, BaseURL: "https://private.example/v1",
			APIKeyEnv: "PRIVATE_KEY", Headers: map[string]string{"X-Tier": "private"},
			ModelsPath: "/catalog",
		}},
	}
	t.Setenv("SHARED_KEY", "shared")
	t.Setenv("PRIVATE_KEY", "private")

	e := p.Route("private-7")
	if e.Key() != "private" {
		t.Fatalf("the endpoint's own key should win, got %q", e.Key())
	}
	if e.Headers["X-Tier"] != "private" || e.Headers["X-Title"] != "shhh" {
		t.Fatalf("the endpoint's header should win the collision and keep the rest, got %v", e.Headers)
	}
	if e.ModelsPath != "/catalog" {
		t.Fatalf("the endpoint's catalog path should win, got %q", e.ModelsPath)
	}
	if def := p.Route("anything"); def.Key() != "shared" || def.Headers["X-Tier"] != "default" {
		t.Fatalf("the default route should be untouched, got %+v", def)
	}
}

func TestModelIDs_SpanEveryEndpoint(t *testing.T) {
	got := routedProfile(t).ModelIDs()
	want := []string{"gpt-5.2", "claude-opus-5", "o5-pro"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestNew_RoutesTheRequestToTheRightEndpoint(t *testing.T) {
	// Two addresses of one gateway, each recording what reached it. Both
	// speak the chat dialect so the assertion is about routing alone.
	var hitDefault, hitRouted map[string]any
	var routedTier string
	def := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&hitDefault)
		writeChatStream(w)
	}))
	defer def.Close()
	routed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routedTier = r.Header.Get("X-Tier")
		_ = json.NewDecoder(r.Body).Decode(&hitRouted)
		writeChatStream(w)
	}))
	defer routed.Close()

	p := Profile{
		Name: "gateway", API: APIOpenAIChat, BaseURL: def.URL, APIKey: "k",
		Rewrite: []Rule{{Op: OpDelete, Path: "top_p", Direction: DirectionRequest}},
		Endpoints: []Endpoint{{
			Match: []string{"claude-*"}, BaseURL: routed.URL,
			Headers: map[string]string{"X-Tier": "private"},
			Rewrite: []Rule{{Op: OpSet, Path: "max_tokens", Value: int64(64), Direction: DirectionRequest}},
		}},
	}
	prov, err := New(p, provider.ResolveOpts{Model: "claude-opus-5"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if prov.Name() != "gateway" {
		t.Fatalf("a routed profile is still one provider, got %q", prov.Name())
	}

	drain(t, prov, "claude-opus-5")
	if hitRouted == nil {
		t.Fatal("the claude model should have reached the routed endpoint")
	}
	if hitDefault != nil {
		t.Fatal("the claude model should not have reached the default endpoint")
	}
	if routedTier != "private" {
		t.Fatalf("the endpoint's own header should have travelled, got %q", routedTier)
	}
	if hitRouted["max_tokens"] != float64(64) {
		t.Fatalf("the endpoint's own rule should have run, got %v", hitRouted["max_tokens"])
	}

	// The same session, a different model: the default address answers, and
	// it is a client of its own with the endpoint's rule nowhere in sight.
	drain(t, prov, "gpt-5.2")
	if hitDefault == nil {
		t.Fatal("the unrouted model should have reached the default endpoint")
	}
	if _, present := hitDefault["max_tokens"]; present {
		t.Fatal("the endpoint's rule should not apply to the default endpoint")
	}
}

func TestNew_RoutedEndpointBuildsItsOwnDialect(t *testing.T) {
	p := Profile{
		Name: "gateway", BaseURL: "https://gw.example/v1", APIKey: "k",
		Endpoints: []Endpoint{{
			Match: []string{"claude-*"}, API: APIAnthropicMessage,
			BaseURL: "https://gw.example/anthropic",
		}},
	}
	prov, err := New(p, provider.ResolveOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	r, ok := prov.(*router)
	if !ok {
		t.Fatalf("a profile with endpoints should route, got %T", prov)
	}
	claude, err := r.providerFor("claude-opus-5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := claude.(*anthropicProfile); !ok {
		t.Fatalf("the claude route should speak the Messages dialect, got %T", claude)
	}
	other, err := r.providerFor("gpt-5.2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := other.(*openAIProfile); !ok {
		t.Fatalf("the default route should speak the chat dialect, got %T", other)
	}
	if same, _ := r.providerFor("claude-sonnet-5"); same != claude {
		t.Fatal("a second model on the same route should reuse the built client")
	}
}

func TestNew_ExplicitBaseURLCollapsesRouting(t *testing.T) {
	// --base-url names one address for everything, which has already
	// answered the question routing exists to answer.
	p := Profile{
		Name: "gateway", BaseURL: "https://gw.example/v1", APIKey: "k",
		Endpoints: []Endpoint{{Match: []string{"*"}, API: APIAnthropicMessage, BaseURL: "https://gw.example/anthropic"}},
	}
	prov, err := New(p, provider.ResolveOpts{BaseURL: "https://override.example/v1", Model: "claude-opus-5"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := prov.(*router); ok {
		t.Fatal("an explicit base URL should pin the profile to one endpoint")
	}
	if _, ok := prov.(*openAIProfile); !ok {
		t.Fatalf("the pinned endpoint should be the default one, got %T", prov)
	}
}

func TestNew_RoutedProfileDoesNotBuildEndpointsItNeverUses(t *testing.T) {
	// An endpoint whose key is unset must not fail a session that was never
	// going to send it anything.
	p := Profile{
		Name: "gateway", BaseURL: "https://gw.example/v1", APIKey: "k",
		Endpoints: []Endpoint{{Match: []string{"claude-*"}, APIKeyEnv: "NEVER_SET_KEY"}},
	}
	prov, err := New(p, provider.ResolveOpts{})
	if err != nil {
		t.Fatalf("building the profile should not touch the unused endpoint: %v", err)
	}
	r := prov.(*router)
	if _, err := r.providerFor("gpt-5.2"); err != nil {
		t.Fatalf("the default route should work: %v", err)
	}
	if _, err := r.providerFor("claude-opus-5"); err == nil {
		t.Fatal("the endpoint with no key should fail when it is actually needed")
	}
}

func TestRouterListModels_UnionsDeclaredAndDiscovered(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-5.2"},{"id":"discovered-only"}]}`))
	}))
	defer srv.Close()

	p := Profile{
		Name: "gateway", BaseURL: srv.URL, APIKey: "k",
		Models: []Model{{ID: "gpt-5.2"}},
		Endpoints: []Endpoint{{
			API: APIAnthropicMessage, BaseURL: "https://gw.example/anthropic",
			Models: []Model{{ID: "claude-opus-5"}},
		}},
	}
	prov, err := New(p, provider.ResolveOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	names, err := prov.(*router).ListModels(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "gpt-5.2,claude-opus-5,discovered-only"
	if strings.Join(names, ",") != want {
		t.Fatalf("got %v, want %s — declared first, then discovered, no repeats", names, want)
	}
}

func TestValidate_RejectsAmbiguousAndUnreachableEndpoints(t *testing.T) {
	tests := []struct{ name, body, want string }{
		{
			"a model claimed twice",
			`base_url = "https://gw.example/v1"
[[models]]
id = "claude-opus-5"
[[endpoint]]
[[endpoint.models]]
id = "claude-opus-5"`,
			"declared by both",
		},
		{
			"an endpoint nothing reaches",
			`base_url = "https://gw.example/v1"
[[endpoint]]
base_url = "https://gw.example/anthropic"`,
			"never used",
		},
		{
			"an unknown dialect on an endpoint",
			`base_url = "https://gw.example/v1"
[[endpoint]]
match = ["claude-*"]
api = "messages"`,
			"is not supported",
		},
		{
			"a malformed match glob",
			`base_url = "https://gw.example/v1"
[[endpoint]]
match = ["claude-[", "x"]`,
			"not a valid pattern",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := writeProfile(t, t.TempDir(), "gw.toml", tc.body)
			_, err := LoadFile(path)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want an error mentioning %q, got %v", tc.want, err)
			}
		})
	}
}

// writeChatStream answers with the shortest well-formed chat-completions
// stream: one token and the terminator.
func writeChatStream(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")
	fmt.Fprint(w, "data: [DONE]\n\n")
	w.(http.Flusher).Flush()
}

// drain runs one completion for a model and reads it to the end.
func drain(t *testing.T, prov provider.Provider, model string) {
	t.Helper()
	ch, err := prov.StreamCompletion(context.Background(),
		[]provider.Message{{Role: provider.RoleUser, Content: "hi"}},
		provider.CompletionOpts{Model: model})
	if err != nil {
		t.Fatalf("%s: %v", model, err)
	}
	for ev := range ch {
		if ev.Err != nil {
			t.Fatalf("%s: %v", model, ev.Err)
		}
	}
}

// Turning off the catalog query (S-143): the declared models become the whole
// list, and nothing is asked of the gateway.

func TestDiscoveryDisabled_HidesTheCatalogQuery(t *testing.T) {
	asked := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"something-the-gateway-lists"}]}`))
	}))
	defer srv.Close()

	off := true
	p := Profile{
		Name: "gateway", BaseURL: srv.URL, APIKey: "k",
		DiscoveryDisabled: &off,
		Models:            []Model{{ID: "gpt-5.2"}},
	}
	prov, err := New(p, provider.ResolveOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The picker reads the capability, not the answer: a provider that is
	// not a ModelLister sends it straight to the declared catalog.
	if _, ok := prov.(provider.ModelLister); ok {
		t.Fatal("a provider with discovery off should not offer to enumerate")
	}
	if asked {
		t.Fatal("nothing should have been asked of the gateway")
	}
	// And the declared models still seed the picker.
	Register([]Profile{p})
	if got := provider.KnownModels("gateway"); len(got) != 1 || got[0] != "gpt-5.2" {
		t.Fatalf("the declared models should be the catalog, got %v", got)
	}
}

func TestDiscoveryDisabled_IsInheritedAndOverridable(t *testing.T) {
	off, on := true, false
	p := Profile{
		Name: "gateway", BaseURL: "https://gw.example/v1", APIKey: "k",
		DiscoveryDisabled: &off,
		Endpoints: []Endpoint{
			{Match: []string{"quiet-*"}, BaseURL: "https://gw.example/quiet"},
			{Match: []string{"loud-*"}, BaseURL: "https://gw.example/loud", DiscoveryDisabled: &on},
		},
	}
	if !p.Route("anything").DiscoveryOff() {
		t.Fatal("the default route should be off")
	}
	if !p.Route("quiet-1").DiscoveryOff() {
		t.Fatal("an endpoint that says nothing should inherit off")
	}
	if p.Route("loud-1").DiscoveryOff() {
		t.Fatal("an endpoint that says false should override the profile")
	}
}

func TestDiscoveryDisabled_RouterAsksOnlyTheEndpointsThatAllowIt(t *testing.T) {
	quiet := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the endpoint with discovery off should never be asked")
	}))
	defer quiet.Close()
	loud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"discovered"}]}`))
	}))
	defer loud.Close()

	off := true
	p := Profile{
		Name: "gateway", BaseURL: loud.URL, APIKey: "k",
		Models: []Model{{ID: "gpt-5.2"}},
		Endpoints: []Endpoint{{
			Match: []string{"quiet-*"}, BaseURL: quiet.URL,
			DiscoveryDisabled: &off, Models: []Model{{ID: "quiet-1"}},
		}},
	}
	prov, err := New(p, provider.ResolveOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	lister, ok := prov.(provider.ModelLister)
	if !ok {
		t.Fatal("one endpoint still allows discovery, so the router should offer it")
	}
	names, err := lister.ListModels(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Join(names, ",") != "gpt-5.2,quiet-1,discovered" {
		t.Fatalf("got %v — the quiet endpoint contributes its declared models and nothing else", names)
	}
}

func TestDiscoveryDisabled_SilentRouterOffersNoQuery(t *testing.T) {
	off := true
	p := Profile{
		Name: "gateway", BaseURL: "https://gw.example/v1", APIKey: "k",
		DiscoveryDisabled: &off,
		Endpoints: []Endpoint{{
			Match: []string{"claude-*"}, API: APIAnthropicMessage,
			BaseURL: "https://gw.example/anthropic",
		}},
	}
	prov, err := New(p, provider.ResolveOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := prov.(provider.ModelLister); ok {
		t.Fatal("no route can enumerate, so the router should not offer to")
	}
	// It is still the router underneath: routing has not been given up.
	drainable, ok := prov.(noDiscovery)
	if !ok {
		t.Fatalf("expected the query hidden, not the provider replaced, got %T", prov)
	}
	if _, ok := drainable.Provider.(*router); !ok {
		t.Fatalf("expected a router underneath, got %T", drainable.Provider)
	}
	if prov.Name() != "gateway" {
		t.Fatalf("the name should survive the wrapper, got %q", prov.Name())
	}
}
