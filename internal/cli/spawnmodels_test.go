package cli

// The models a spawn may name: the /model picker's choices and the
// configured ones, the endpoint's own list asked once and shared with the
// picker, and the list following the session when its model moves.

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/meter"
	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/testhttp"
	openai "github.com/sashabaranov/go-openai"
)

func TestSpawnableModelsAreThePickersChoicesAndTheConfiguredOnes(t *testing.T) {
	configured := config.Config{Agents: config.AgentsConfig{
		Model: "agents-wide",
		Depths: map[string]config.AgentDepth{
			"10": {Model: "depth-ten"},
			"2":  {Model: "depth-two"},
			"3":  {Model: config.InheritModel},
		},
		Profiles: map[string]config.AgentProfile{
			"writer":     {Model: "role-writer"},
			"researcher": {Model: "catalog-b"},
		},
	}}
	files := &agentProfiles{definitions: map[string]config.AgentDefinition{
		"critic": {Name: "critic", Model: "file-critic"},
		"scout":  {Name: "scout", Model: config.InheritModel},
	}}
	catalog := []string{"catalog-a", "catalog-b"}
	tests := []struct {
		name    string
		cfg     config.Config
		agents  *agentProfiles
		session string
		strict  bool
		want    []string
	}{
		{
			name:    "the catalog as the picker lists it, the session's model among it",
			session: "catalog-b",
			want:    []string{"catalog-a", "catalog-b"},
		},
		{
			name:    "a session model the catalog does not list goes first, as the picker puts it",
			session: "typed-in",
			want:    []string{"typed-in", "catalog-a", "catalog-b"},
		},
		{
			name:    "then every model the configuration names for an agent, each once",
			cfg:     configured,
			agents:  files,
			session: "catalog-a",
			want: []string{"catalog-a", "catalog-b",
				"agents-wide", "depth-two", "depth-ten", "role-writer", "file-critic"},
		},
		{
			name:    "a strict catalog is the list and nothing else",
			cfg:     configured,
			agents:  files,
			session: "catalog-a",
			strict:  true,
			want:    []string{"catalog-a", "catalog-b"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := spawnableModels(tc.cfg, tc.agents, catalog, tc.session, tc.strict)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("spawnable models\n got %q\nwant %q", got, tc.want)
			}
		})
	}
}

// listingEndpoint is an openai-compatible endpoint whose /models answers
// with ids, or with status where it is not 200, counting every request.
func listingEndpoint(t *testing.T, status int, ids ...string) (provider.Provider, *atomic.Int32) {
	t.Helper()
	var asked atomic.Int32
	var fixtures testhttp.Registry
	srv := fixtures.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		if status != http.StatusOK {
			http.Error(w, "unavailable", status)
			return
		}
		data := make([]map[string]string, len(ids))
		for i, id := range ids {
			data[i] = map[string]string{"id": id, "object": "model"}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})
	}))
	t.Cleanup(srv.Close)
	cfg := openai.DefaultConfig("test-key")
	cfg.BaseURL = srv.URL + "/v1"
	cfg.HTTPClient = fixtures.Client()
	return provider.NewOpenAICompatNamed(openai.NewClientWithConfig(cfg), "session-model", cfg.BaseURL, "listing-test"), &asked
}

// endpointSession is a session on prov with the endpoint's list wired the
// way buildSessionEnv wires it.
func endpointSession(prov provider.Provider) spawnModels {
	env := &sessionEnv{prov: prov, modelName: "session-model", endpointModels: newEndpointModels(modelListerFor(prov))}
	return spawnModels{env: env, agents: &agentProfiles{profiles: subagent.BuiltinProfiles()}}
}

func TestSpawnAcceptsAnIdTheEndpointLists(t *testing.T) {
	prov, _ := listingEndpoint(t, http.StatusOK, "qwen3:8b", "llama-3.1")
	spawn := endpointSession(prov)
	if !spawn.offer().Endpoint {
		t.Fatal("a session whose endpoint lists its models must say an id it lists is valid too")
	}
	tests := []struct {
		model   string
		refused bool
	}{
		{model: "session-model"},
		{model: "qwen3:8b"},
		{model: "gpt-typo", refused: true},
	}
	for _, tc := range tests {
		t.Run(tc.model, func(t *testing.T) {
			note, err := spawn.check(tc.model)
			if note != "" {
				t.Fatalf("a listing that answered left a note: %q", note)
			}
			if !tc.refused {
				if err != nil {
					t.Fatalf("an id the session or the endpoint lists was refused: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("an id neither the session nor the endpoint lists was accepted")
			}
			for _, w := range []string{`"gpt-typo"`, "endpoint does not list it", "name one of session-model"} {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("the refusal never says %s: %v", w, err)
				}
			}
		})
	}
}

func TestSpawnModelListIsFetchedOnceAndSharedWithThePicker(t *testing.T) {
	tests := []struct {
		name        string
		pickerFirst bool
	}{
		{name: "a spawn asks, and the picker reads its answer"},
		{name: "the picker asks, and a spawn reads its answer", pickerFirst: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			prov, asked := listingEndpoint(t, http.StatusOK, "qwen3:8b")
			spawn := endpointSession(prov)
			if asked.Load() != 0 {
				t.Fatal("the endpoint was asked before anything wanted its list")
			}
			picker := spawn.env.endpointModels.picker()
			if tc.pickerFirst {
				if names, err := picker(t.Context()); err != nil || !slices.Equal(names, []string{"qwen3:8b"}) {
					t.Fatalf("the picker's list: %q %v", names, err)
				}
			}
			if _, err := spawn.check("qwen3:8b"); err != nil {
				t.Fatalf("an id the endpoint lists was refused: %v", err)
			}
			if _, err := spawn.check("not-listed"); err == nil {
				t.Fatal("an id the endpoint does not list was accepted")
			}
			if names, err := picker(context.Background()); err != nil || !slices.Equal(names, []string{"qwen3:8b"}) {
				t.Fatalf("the picker's list: %q %v", names, err)
			}
			if got := asked.Load(); got != 1 {
				t.Fatalf("the endpoint was asked %d times, want once for the session", got)
			}
		})
	}
}

func TestAFailedListingDoesNotRefuseTheSpawn(t *testing.T) {
	prov, asked := listingEndpoint(t, http.StatusServiceUnavailable)
	spawn := endpointSession(prov)

	note, err := spawn.check("qwen3:8b")
	if err != nil {
		t.Fatalf("a spawn whose model could not be checked was refused: %v", err)
	}
	if !strings.Contains(note, "could not be checked") {
		t.Fatalf("the note does not say the model could not be checked: %q", note)
	}
	// The failure is kept for the spawns that follow, so each is not put
	// behind the same wait; a spawn on the session's own list never asks.
	if _, err := spawn.check("llama-3.1"); err != nil {
		t.Fatalf("a second unchecked spawn was refused: %v", err)
	}
	if _, err := spawn.check("session-model"); err != nil {
		t.Fatalf("the session's own model was refused: %v", err)
	}
	if got := asked.Load(); got != 1 {
		t.Fatalf("a failed listing was asked %d times for the spawns, want once", got)
	}

	// And the spawn's result carries the note, through the supervisor the
	// session hands the check to.
	sup := subagent.New(t.Context(), subagent.Options{Root: t.TempDir(),
		NewEnv: (&scriptedChildren{steps: []childStep{{text: "done"}}}).factory(), CheckModel: spawn.check})
	t.Cleanup(sup.Close)
	out, err := sup.WrapExecutor("", nil)(subagent.SpawnToolName,
		json.RawMessage(`{"role":"researcher","task":"survey","model":"qwen3:8b"}`))
	if err != nil {
		t.Fatalf("the spawn was refused: %v", err)
	}
	if !strings.Contains(out, "Its model qwen3:8b could not be checked") {
		t.Fatalf("the spawn's result does not say its model could not be checked: %s", out)
	}
}

func TestSpawnModelListFollowsAProviderSwitch(t *testing.T) {
	prices := pricing.NewTable(map[string]pricing.ModelPricing{
		"other-default": {InputCostPerToken: 0.000001, OutputCostPerToken: 0.00001},
	})
	tests := []struct {
		name string
		move func(*testing.T, *sessionEnv, *agentProfiles) error
		want string
	}{
		{
			// Children stay on the opening provider, so the list does not
			// name the model the new provider moved the session to.
			name: "a provider switch",
			move: func(_ *testing.T, env *sessionEnv, _ *agentProfiles) error { return env.switchProvider("other") },
			want: "opening-model.",
		},
		{
			name: "a /model switch",
			move: func(_ *testing.T, env *sessionEnv, _ *agentProfiles) error {
				env.switchModel("picked-model")
				return nil
			},
			want: "picked-model",
		},
		{
			// The save the agent manager's editor ends on, with a model in
			// the file: the role it brings can name the model it was given.
			name: "a profile saved from the agent manager",
			move: func(t *testing.T, env *sessionEnv, agents *agentProfiles) error {
				t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
				sup := subagent.New(t.Context(), subagent.Options{Root: t.TempDir(), Profiles: agents.profiles})
				t.Cleanup(sup.Close)
				path := filepath.Join(t.TempDir(), "critic.toml")
				must(t, os.WriteFile(path, []byte("description = \"reads a diff\"\npermissions = [\"read\"]\nmodel = \"critic-model\"\n"), 0o644))
				return buildPersonas(chatSession{}, env, agents, sup, meter.New(nil), nil).Reload(path)
			},
			want: "opening-model, critic-model",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			current, onProvider := "opening-model", "opening"
			tools := subagent.Definitions(nil, subagent.Offer{})
			env := &sessionEnv{
				prov:        assemblyProvider{},
				modelName:   current,
				provName:    onProvider,
				model:       func() string { return current },
				providerNow: func() string { return onProvider },
				switchModel: func(name string) {
					current = name
				},
				switchProvider: func(name string) error {
					current, onProvider = name+"-default", name
					return nil
				},
				replaceTools: func(edit func([]provider.Tool) []provider.Tool) { tools = edit(tools) },
			}
			agents := &agentProfiles{profiles: subagent.BuiltinProfiles(), definitions: map[string]config.AgentDefinition{}}
			followSpawnModels(env, spawnModels{env: env, agents: agents, prices: prices}, func() subagent.Profiles { return agents.profiles })
			if err := tc.move(t, env, agents); err != nil {
				t.Fatal(err)
			}
			var desc string
			for _, tool := range tools {
				if tool.Name == subagent.SpawnToolName {
					desc = string(tool.Parameters)
				}
			}
			if !strings.Contains(desc, "The models this session can run: "+tc.want) {
				t.Fatalf("the next request's spawn_agent does not offer the session's model now:\n%s", desc)
			}
			if strings.Contains(desc, "other-default") {
				t.Fatalf("spawn_agent names a model of a provider a child cannot run on:\n%s", desc)
			}
		})
	}
}

// The session's own spawn is refused before its card is drawn, and an
// unattended run's before its verdict: the check the screen runs ahead of the
// preview refuses it, no classifier round is spent, and no agent is started. A spawn
// naming a listed model, or none, reaches both as it always did.
func TestSpawnRefusalTakesNoSlotAndRaisesNoCard(t *testing.T) {
	env := &sessionEnv{prov: assemblyProvider{}, modelName: "m-a"}
	spawn := spawnModels{env: env, agents: &agentProfiles{profiles: subagent.BuiltinProfiles()}}
	tests := []struct {
		name    string
		args    string
		refused bool
	}{
		{name: "an id outside the list", args: `{"role":"researcher","task":"survey","model":"m-typo"}`, refused: true},
		{name: "an id on the list", args: `{"role":"researcher","task":"survey","model":"m-a"}`},
		{name: "no model at all", args: `{"role":"researcher","task":"survey"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sup := subagent.New(t.Context(), subagent.Options{Root: t.TempDir(),
				NewEnv: (&scriptedChildren{steps: []childStep{{text: "done"}}}).factory(), CheckModel: spawn.check})
			t.Cleanup(sup.Close)

			err := spawnModelCheck(sup)(json.RawMessage(tc.args))
			if tc.refused != (err != nil) {
				t.Fatalf("the session's check: refused %v (%v)", err != nil, err)
			}
			if tc.refused && !strings.Contains(err.Error(), `model "m-typo" is not one this session can run; name one of m-a`) {
				t.Fatalf("the refusal does not name the list: %v", err)
			}

			// Run with neither --yes nor a classifier, so a spawn that
			// reached the verdict is refused in the verdict's own words.
			var ran []string
			resolve := headlessApprover(t.Context(), headlessApproval{run: fakeRun(&ran), un: unattended{sup: sup}})
			result := resolve(provider.ToolCall{ID: "s1", Name: subagent.SpawnToolName, Arguments: tc.args})
			if reachedVerdict := strings.Contains(result, "headless mode denies"); tc.refused == reachedVerdict {
				t.Fatalf("an unattended run's verdict: %s", result)
			}
			if started, _ := sup.Spawned(); started != 0 {
				t.Fatalf("%d agents started", started)
			}
		})
	}
}

// A child is bound to the provider the session opened on, so after a
// provider switch the session layer is the opening model and the list names
// no model of the provider the session moved to
// (docs/capabilities/subagents.md#the-model-a-depth-runs-on).
func TestAChildFollowsTheSessionsProviderSwitch(t *testing.T) {
	current, onProvider := "opening-model", "opening"
	env := &sessionEnv{
		prov:        assemblyProvider{},
		provName:    "opening",
		modelName:   "opening-model",
		model:       func() string { return current },
		providerNow: func() string { return onProvider },
	}
	agents := &agentProfiles{profiles: subagent.BuiltinProfiles(), definitions: map[string]config.AgentDefinition{}}
	onProvider, current = "other", "other-default"
	if got := agents.modelFor(config.Config{}, subagent.Role("explore"), 1, "", env.childModel()); got != "opening-model" {
		t.Fatalf("a child after a provider switch runs on %q, want the opening model", got)
	}
	ids := spawnModels{env: env, agents: agents}.ids()
	if slices.Contains(ids, "other-default") || !slices.Contains(ids, "opening-model") {
		t.Fatalf("the list after a provider switch is %v, want the opening provider's models only", ids)
	}
}

// A /model pick on the opening provider reaches a child that names no model.
func TestAChildFollowsTheSessionsModelSwitch(t *testing.T) {
	current := "opening-model"
	env := &sessionEnv{
		prov:        assemblyProvider{},
		provName:    "opening",
		modelName:   "opening-model",
		model:       func() string { return current },
		providerNow: func() string { return "opening" },
	}
	agents := &agentProfiles{profiles: subagent.BuiltinProfiles(), definitions: map[string]config.AgentDefinition{}}
	current = "picked-model"
	if got := agents.modelFor(config.Config{}, subagent.Role("explore"), 1, "", env.childModel()); got != "picked-model" {
		t.Fatalf("a child that names no model runs on %q, want the /model pick", got)
	}
}
