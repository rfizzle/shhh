package cli

import (
	"testing"

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
	if got := auxiliaryModel("anthropic", "claude-opus-5"); got != cheap {
		t.Errorf("unset should take the provider's small model, got %q", got)
	}
	// A local endpoint serves whatever weights were pulled, so there is no
	// small model to name and the session's own is the only safe answer.
	if got := auxiliaryModel("openai-compatible", "qwen3:8b"); got != "qwen3:8b" {
		t.Errorf("a provider naming none should fall back to the session model, got %q", got)
	}
	// A provider nobody registered — a gateway profile — answers the same way.
	if got := auxiliaryModel("not-a-provider", "some-model"); got != "some-model" {
		t.Errorf("an unregistered provider should fall back, got %q", got)
	}
	if got := modelOr("gpt-4o", auxiliaryModel("anthropic", "claude-opus-5")); got != "gpt-4o" {
		t.Errorf("a configured model must win, got %q", got)
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
