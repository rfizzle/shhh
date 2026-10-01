package subagent

// What spawn_agent tells the model about the models it may name, and the
// check that refuses one the session cannot run before anything is spent on
// it — a slot, a card, a classifier round.

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/rfizzle/shhh/internal/provider"
)

// listedModels is a session's check over a fixed list, refusing every other
// id with the list named, the way the session's own check refuses one. The
// list can be replaced while a supervisor holds the check, which is what a
// /model switch does to the session's.
type listedModels struct {
	mu  sync.Mutex
	ids []string
}

func (l *listedModels) set(ids ...string) {
	l.mu.Lock()
	l.ids = ids
	l.mu.Unlock()
}

func (l *listedModels) check(model string) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if slices.Contains(l.ids, model) {
		return "", nil
	}
	return "", fmt.Errorf("model %q is not one this session can run; name one of %s", model, strings.Join(l.ids, ", "))
}

// spawnDefinition is spawn_agent as Definitions builds it for offer.
func spawnDefinition(t *testing.T, offer Offer) provider.Tool {
	t.Helper()
	for _, d := range Definitions(nil, offer) {
		if d.Name == SpawnToolName {
			return d
		}
	}
	t.Fatal("Definitions has no spawn_agent")
	return provider.Tool{}
}

// modelArgument is the model property of spawn_agent's schema.
func modelArgument(t *testing.T, offer Offer) map[string]any {
	t.Helper()
	var schema struct {
		Properties map[string]map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(spawnDefinition(t, offer).Parameters, &schema); err != nil {
		t.Fatalf("spawn_agent's schema does not parse: %v", err)
	}
	return schema.Properties["model"]
}

func TestSpawnModelDescriptionListsTheSpawnableModels(t *testing.T) {
	many := make([]SpawnModel, MaxListedModels+3)
	for i := range many {
		many[i] = SpawnModel{ID: fmt.Sprintf("model-%02d", i)}
	}
	tests := []struct {
		name    string
		offer   Offer
		want    []string
		notWant []string
	}{
		{
			name:    "a session that offers nothing says nothing about a list",
			offer:   Offer{},
			want:    []string{"Optional model for this agent"},
			notWant: []string{"can run", "refused"},
		},
		{
			name: "each id with the price the picker prints, and the refusal",
			offer: Offer{Models: []SpawnModel{
				{ID: "claude-a", Price: "$3.00 in / $15.00 out per Mtok"},
				{ID: "local-b"},
			}},
			want: []string{
				"The models this session can run: claude-a ($3.00 in / $15.00 out per Mtok), local-b.",
				"An id not among them is refused, so leave it out rather than guess one.",
			},
			notWant: []string{"endpoint"},
		},
		{
			name:  "an endpoint that lists its models is named as the other way in",
			offer: Offer{Models: []SpawnModel{{ID: "local-b"}}, Endpoint: true},
			want:  []string{"An id not among them is refused unless the provider's endpoint lists it"},
		},
		{
			name:    "the list stops at the cap and counts the rest",
			offer:   Offer{Models: many},
			want:    []string{many[MaxListedModels-1].ID, "and 3 more, which a refusal lists."},
			notWant: []string{many[MaxListedModels].ID},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			arg := modelArgument(t, tc.offer)
			if _, ok := arg["enum"]; ok {
				t.Fatal("the model argument is an enum, which would refuse an id the endpoint lists before the session could ask")
			}
			desc, _ := arg["description"].(string)
			for _, w := range tc.want {
				if !strings.Contains(desc, w) {
					t.Errorf("the description does not say %q:\n%s", w, desc)
				}
			}
			for _, w := range tc.notWant {
				if strings.Contains(desc, w) {
					t.Errorf("the description says %q:\n%s", w, desc)
				}
			}
		})
	}
}

// The list rides every request the session makes, so however long a catalog
// a gateway declares, what it adds to spawn_agent stays under what one of
// the small orchestration tools costs on its own.
func TestSpawnDefinitionGrowthIsBounded(t *testing.T) {
	size := func(d provider.Tool) int {
		b, err := json.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}
		return len(b)
	}
	long := make([]SpawnModel, 200)
	for i := range long {
		long[i] = SpawnModel{
			ID:    fmt.Sprintf("anthropic/claude-sonnet-4.6-%04d-preview", i),
			Price: "$15.00 in / $75.00 out per Mtok",
		}
	}
	bare := size(spawnDefinition(t, Offer{}))
	grew := size(spawnDefinition(t, Offer{Models: long, Endpoint: true})) - bare
	oneTool := 0
	for _, d := range Definitions(nil, Offer{}) {
		if d.Name == SteerToolName {
			oneTool = size(d)
		}
	}
	t.Logf("spawn_agent %d bytes bare, +%d with %d models listed; agent_steer is %d", bare, grew, len(long), oneTool)
	if grew > oneTool {
		t.Errorf("the model list grew spawn_agent by %d bytes, more than agent_steer's %d", grew, oneTool)
	}
}

func TestSpawnRefusesAModelOutsideTheList(t *testing.T) {
	tests := []struct {
		name    string
		args    string
		refused bool
	}{
		{name: "an id on the list", args: `{"role":"researcher","task":"survey","model":"m-a"}`},
		{name: "no model at all", args: `{"role":"researcher","task":"survey"}`},
		{name: "an id outside it", args: `{"role":"researcher","task":"survey","model":"m-typo"}`, refused: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			models := &listedModels{ids: []string{"m-a", "m-b"}}
			sup := New(t.Context(), Options{Root: t.TempDir(), NewEnv: (&scriptedEnv{steps: readRounds(1)}).factory(), CheckModel: models.check})
			t.Cleanup(sup.Close)
			exec := sup.WrapExecutor("", nil)
			out, err := exec(SpawnToolName, json.RawMessage(tc.args))
			started, _ := sup.Spawned()
			if !tc.refused {
				if err != nil || started != 1 {
					t.Fatalf("want the spawn accepted, got %d started: %q %v", started, out, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("an id outside the list was spawned: %s", out)
			}
			for _, w := range []string{`"m-typo"`, "m-a, m-b"} {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("the refusal never says %s: %v", w, err)
				}
			}
			if started != 0 || len(sup.Snapshot()) != 0 {
				t.Fatalf("a refused spawn started %d agents", started)
			}
		})
	}

	// A retry runs the agent the session already accepted, on the model it
	// was accepted with; the list moving since is not a reason to refuse it.
	t.Run("a retry of an accepted spawn", func(t *testing.T) {
		models := &listedModels{ids: []string{"m-a"}}
		env := &scriptedEnv{}
		sup := New(t.Context(), Options{Root: t.TempDir(), NewEnv: env.factory(), CheckModel: models.check})
		t.Cleanup(sup.Close)
		execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"survey","model":"m-a"}`)
		waitState(t, sup, "researcher-1", StateFailed)
		models.set("m-b")
		env.mu.Lock()
		env.steps = []streamStep{{text: "surveyed"}}
		env.mu.Unlock()
		if err := sup.Retry("researcher-1"); err != nil {
			t.Fatalf("a retry of an accepted spawn was refused: %v", err)
		}
		waitState(t, sup, "researcher-1", StateDone)
	})
}

// A child's own spawn is gated, and a model the session cannot run is
// answered ahead of the card: the child reads the refusal as its result, no
// card reaches the person, and no second agent is started. The same spawn
// naming a listed model is put to the person as it always was.
func TestSpawnRefusalTakesNoSlotAndRaisesNoCard(t *testing.T) {
	tests := []struct {
		name  string
		model string
		card  bool
	}{
		{name: "an id outside the list", model: "m-typo"},
		{name: "an id on the list", model: "m-a", card: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := &scriptedEnv{
				steps: []streamStep{
					{calls: []provider.ToolCall{{ID: "s1", Name: SpawnToolName,
						Arguments: `{"role":"researcher","task":"read the exporter","model":"` + tc.model + `"}`}}},
					{text: "done"},
				},
				gated: map[string]bool{SpawnToolName: true},
			}
			models := &listedModels{ids: []string{"m-a"}}
			sup := New(t.Context(), Options{Root: t.TempDir(), NewEnv: env.factory(), CheckModel: models.check})
			t.Cleanup(sup.Close)
			sup.SetAttended()
			execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"survey the importer"}`)

			if tc.card {
				ask := nextAsk(t, sup)
				if !strings.Contains(ask.Summary+ask.Title, "spawn_agent") {
					t.Fatalf("the card is not the child's spawn: %+v", ask)
				}
				ask.Respond(false)
				return
			}
			execTool(t, sup, ReportToolName, `{"name":"researcher-1"}`)
			for {
				select {
				case ev := <-sup.Events():
					if ev.Kind == EventAsk {
						t.Fatalf("a spawn the session cannot run reached a card: %+v", ev.Ask)
					}
					continue
				default:
				}
				break
			}
			if started, _ := sup.Spawned(); started != 1 {
				t.Fatalf("the refused spawn took a slot: %d agents started", started)
			}
			env.mu.Lock()
			defer env.mu.Unlock()
			last := env.requests[len(env.requests)-1]
			var result string
			for _, m := range last {
				if m.Role == provider.RoleTool {
					result = m.Content
				}
			}
			if !strings.Contains(result, `model "m-typo" is not one this session can run; name one of m-a`) {
				t.Fatalf("the child did not read the refusal as its result: %q", result)
			}
		})
	}
}

// A child that delegates is held to the list its parent is, through the
// same check the session's own spawns go through.
func TestAChildsSpawnIsCheckedAgainstTheSameList(t *testing.T) {
	models := &listedModels{ids: []string{"m-a"}}
	sup := New(t.Context(), Options{Root: t.TempDir(), NewEnv: (&scriptedEnv{steps: readRounds(40)}).factory(), CheckModel: models.check})
	t.Cleanup(sup.Close)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"survey the importer","name":"child"}`)

	if _, err := spawnFromAgent(sup, "child", `{"role":"researcher","task":"read the exporter","model":"m-typo"}`); err == nil ||
		!strings.Contains(err.Error(), "name one of m-a") {
		t.Fatalf("a child's spawn naming a model outside the list must be refused with the list: %v", err)
	}
	if started, _ := sup.Spawned(); started != 1 {
		t.Fatalf("the child's refused spawn took a slot: %d started", started)
	}
	if _, err := spawnFromAgent(sup, "child", `{"role":"researcher","task":"read the exporter","model":"m-a","name":"grandchild"}`); err != nil {
		t.Fatalf("a child's spawn naming a listed model must start: %v", err)
	}
}
