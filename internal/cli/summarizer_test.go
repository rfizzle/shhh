package cli

import (
	"testing"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/provider"
)

// The bounded calls run on the provider's small model, fall back to the
// session's own where the provider names none, and yield outright to a model
// the person named.
func TestAuxiliaryModel(t *testing.T) {
	cheap := provider.Defaults("anthropic").CheapModel
	if cheap == "" {
		t.Fatal("the anthropic provider should name a cheap model")
	}
	if got := auxiliaryModel(config.Config{}, "anthropic", "claude-opus-5"); got != cheap {
		t.Errorf("unset should take the provider's small model, got %q", got)
	}
	// A local endpoint serves whatever weights were pulled, so there is no
	// small model to name and the session's own is the only safe answer.
	if got := auxiliaryModel(config.Config{}, "openai-compatible", "qwen3:8b"); got != "qwen3:8b" {
		t.Errorf("a provider naming none should fall back to the session model, got %q", got)
	}
	// A provider nobody registered — a gateway profile — answers the same way.
	if got := auxiliaryModel(config.Config{}, "not-a-provider", "some-model"); got != "some-model" {
		t.Errorf("an unregistered provider should fall back, got %q", got)
	}
	if got := modelOr("gpt-4o", auxiliaryModel(config.Config{}, "anthropic", "claude-opus-5")); got != "gpt-4o" {
		t.Errorf("a configured model must win, got %q", got)
	}
}

// The chain is four links, each answering only where the one before it is
// unset: the flow's own key, provider.cheap_model, the provider's small
// model, the session's own — and the step each answer reports is the link
// that gave it, which is what the doctor row says.
func TestResolveFlow_WalksTheChainInOrder(t *testing.T) {
	small := provider.Defaults("anthropic").CheapModel
	var cfg config.Config
	cases := []struct {
		name    string
		set     func(*config.Config)
		prov    string
		want    string
		step    flowStep
		wantKey string
		flow    boundedFlow
	}{
		{"nothing set takes the provider's small model", func(*config.Config) {}, "anthropic", small, stepSmallModel, "", flowClassifier},
		{"a provider naming none takes the session's", func(*config.Config) {}, "openai-compatible", "session", stepSessionModel, "", flowClassifier},
		{"the cheap key outranks the provider's small model", func(c *config.Config) { c.Provider.CheapModel = "cheap" }, "anthropic", "cheap", stepCheapKey, "provider.cheap_model", flowReading},
		{"the flow key outranks the cheap key", func(c *config.Config) { c.Provider.CheapModel = "cheap"; c.Todo.Model = "mine" }, "anthropic", "mine", stepFlowKey, "todo.model", flowBacklog},
		{"the backlog no longer lands on the session by default", func(*config.Config) {}, "anthropic", small, stepSmallModel, "", flowBacklog},
		{"the profile drafter joins the chain", func(c *config.Config) { c.Provider.CheapModel = "cheap" }, "anthropic", "cheap", stepCheapKey, "provider.cheap_model", flowDrafter},
		{"the explanation reads the classifier's key behind its own", func(c *config.Config) { c.Behavior.ClassifierModel = "judge" }, "anthropic", "judge", stepFlowKey, "behavior.classifier_model", flowExplanation},
		{"compaction never assumes the small model's window", func(*config.Config) {}, "anthropic", "session", stepSessionModel, "", flowCompaction},
		{"compaction takes the cheap key, under its window rule", func(c *config.Config) { c.Provider.CheapModel = "cheap" }, "anthropic", "cheap", stepCheapKey, "provider.cheap_model", flowCompaction},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := cfg
			c.set(&got)
			answer := resolveFlow(got, c.flow, c.prov, "session")
			if answer.model != c.want || answer.step != c.step || answer.key != c.wantKey {
				t.Fatalf("got %q via %s (%q), want %q via %s (%q)", answer.model, answer.step, answer.key, c.want, c.step, c.wantKey)
			}
		})
	}
}

// Every flow the table lists answers, in the table's order, so a listing read
// off resolveFlows names each flow once.
func TestResolveFlows_AnswersForEveryFlow(t *testing.T) {
	got := resolveFlows(config.Config{}, "anthropic", "session")
	if len(got) != len(boundedFlows) {
		t.Fatalf("%d answers for %d flows", len(got), len(boundedFlows))
	}
	for i, a := range got {
		if a.flow.name != boundedFlows[i].name || a.model == "" {
			t.Errorf("answer %d = %+v", i, a)
		}
	}
}

// Every small model a provider names has to be one the model data can price
// and describe. A name the table has never heard of bills at nothing, is
// sent a reasoning field its family may not take, and — because these calls
// swallow their own failures — says so nowhere. The table is the same one
// the session loads, so a retired id is caught here rather than in a
// classifier that quietly stopped answering.
func TestCheapModelsAreInTheModelData(t *testing.T) {
	table := pricing.Snapshot()
	for _, name := range provider.Available() {
		cheap := provider.Defaults(name).CheapModel
		if cheap == "" {
			continue
		}
		e, ok := table.Entry(cheap)
		if !ok {
			t.Errorf("%s: the model data does not know %q", name, cheap)
			continue
		}
		if !e.SupportsReasoning {
			t.Errorf("%s: %q is described as having no reasoning knob", name, cheap)
		}
		if e.InputCostPerToken <= 0 || e.OutputCostPerToken <= 0 {
			t.Errorf("%s: %q has no price, so its spend would not be counted", name, cheap)
		}
	}
}
