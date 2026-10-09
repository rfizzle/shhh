package profile

import (
	"context"
	"net/http"
	"testing"

	"github.com/rfizzle/shhh/internal/provider"
)

// A model offers the Decisions API where the profile declares it, on the
// endpoint the model routes to, and nowhere else: a declaration on the
// Messages dialect offers nothing, an undeclared model offers nothing, and a
// request goes to the address a completion for the same model would.
func TestRouter_DecisionsFollowTheModelsEndpoint(t *testing.T) {
	answer := func(hit, tier *string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			*hit = r.URL.Path
			*tier = r.Header.Get("X-Tier")
			_, _ = w.Write([]byte(`{"answers":[{"type":"predicate","name":"q","probability":0.5}]}`))
		})
	}
	var defPath, defTier, routedPath, routedTier string
	def := profileTestHTTP.NewServer(answer(&defPath, &defTier))
	defer def.Close()
	routed := profileTestHTTP.NewServer(answer(&routedPath, &routedTier))
	defer routed.Close()

	yes := true
	p := Profile{
		Name: "gateway", API: APIOpenAIChat, BaseURL: def.URL + "/v1", APIKey: "k",
		DiscoveryDisabled: &yes, StrictModels: true,
		Models: []Model{{ID: "gpt-6-luna", Decisions: true}, {ID: "gpt-5.6-luna"}},
		Endpoints: []Endpoint{{
			API: APIOpenAIResponses, BaseURL: routed.URL + "/responses",
			Headers: map[string]string{"X-Tier": "private"},
			Models:  []Model{{ID: "luna-gw", Decisions: true}},
		}, {
			API: APIAnthropicMessage, BaseURL: routed.URL + "/anthropic",
			Models: []Model{{ID: "claude-opus-5", Decisions: true}},
		}},
	}
	prov, err := New(p, provider.ResolveOpts{Model: "gpt-5.6-luna", HTTPClient: profileTestHTTP.Client()})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	d, ok := prov.(provider.Decider)
	if !ok {
		t.Fatal("a profile's provider should keep the capability through its wrappers")
	}
	for model, want := range map[string]bool{
		"gpt-6-luna": true, "luna-gw": true,
		"gpt-5.6-luna": false, "claude-opus-5": false, "not-declared": false,
	} {
		if got := d.OffersDecisions(model); got != want {
			t.Errorf("OffersDecisions(%s) = %v, want %v", model, got, want)
		}
		if got := p.DeclaresDecisions(model); got != want {
			t.Errorf("DeclaresDecisions(%s) = %v, want %v", model, got, want)
		}
	}

	ask := func(model string) error {
		_, err := d.Decide(context.Background(), provider.DecisionRequest{Model: model, Input: "x",
			Questions: []provider.DecisionQuestion{{Type: provider.QuestionPredicate, Name: "q", Instructions: "p"}}})
		return err
	}
	if err := ask("luna-gw"); err != nil {
		t.Fatalf("routed: %v", err)
	}
	if routedPath != "/responses/decisions" || routedTier != "private" || defPath != "" {
		t.Fatalf("routed request reached %q (tier %q), default %q", routedPath, routedTier, defPath)
	}
	if err := ask("gpt-6-luna"); err != nil {
		t.Fatalf("default: %v", err)
	}
	if defPath != "/v1/decisions" || defTier != "" {
		t.Fatalf("default request reached %q (tier %q)", defPath, defTier)
	}
	if err := ask("not-declared"); err == nil {
		t.Fatal("a strict catalog should refuse a decisions request for a model it does not declare")
	}
}

// The declaration is read from the file, and a profile's registration is
// what the surfaces that build no provider ask.
func TestDecisions_AreReadFromTheFileAndRegistered(t *testing.T) {
	path := writeProfile(t, t.TempDir(), "luna.toml", `
name     = "luna-gateway"
base_url = "https://gw.example/v1"

[[models]]
id        = "gpt-6-luna"
decisions = true

[[models]]
id = "gpt-5.6-luna"
`)
	loaded, err := LoadFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	Register(loaded)
	t.Cleanup(func() { provider.RegisterDecisions("luna-gateway", nil) })
	if !provider.DecisionsDeclared("luna-gateway", "gpt-6-luna") {
		t.Error("a declared model should be registered as offering it")
	}
	if provider.DecisionsDeclared("luna-gateway", "gpt-5.6-luna") {
		t.Error("an undeclared model should not")
	}
}
