package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/profile"
	"github.com/rfizzle/shhh/internal/provider"
)

// A profile's structured_outputs = false is registered from the profile, as
// the decisions are, and so decides what SchemaFor answers.
func TestSchemaFor_AProfileDeclarationReachesTheAnswer(t *testing.T) {
	loaded := registerProfile(t, `
name     = "gw"
base_url = "https://gw.example"
api      = "anthropic-messages"

[[models]]
id                 = "claude-sonnet-5-5"
structured_outputs = false

[[models]]
id = "claude-opus-5-5"
`)
	table := pricing.Snapshot()
	table.Overlay(profile.Pricing(loaded))
	installCapabilities(table)
	t.Cleanup(func() { provider.SetCapabilityLookup(nil) })

	opts := provider.CompletionOpts{ResponseSchema: &provider.ResponseSchema{Name: "v", Schema: []byte(`{"type":"object"}`)}}
	if opts.SchemaFor("claude-sonnet-5-5") != nil {
		t.Error("the declared model should be sent no schema")
	}
	if opts.SchemaFor("claude-opus-5-5") == nil {
		t.Error("a model that declared nothing keeps the floor")
	}
}

func TestProfile_StructuredOutputsTrueIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gw.toml")
	body := "name = \"gw\"\nbase_url = \"https://gw.example\"\n[[models]]\nid = \"m\"\nstructured_outputs = true\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := profile.LoadFile(path); err == nil {
		t.Fatal("structured_outputs = true should be refused")
	}
}

// A line that declares only a narrowing creates no price-table entry, so a
// model the table lacks keeps the floor's reasoning and output ceiling, on
// every flow and the main agent's request alike.
func TestCapabilities_ADeclarationAloneKeepsTheFloorsReasoning(t *testing.T) {
	const model = "claude-sonnet-5-5"
	if _, ok := pricing.Snapshot().Entry(model); ok {
		t.Fatalf("the snapshot describes %s; the test needs a model it lacks", model)
	}
	floor := provider.CapabilitiesFor(model)
	for _, line := range []string{
		"structured_outputs = false",
		"flows = { classifier = { structured_outputs = false } }",
	} {
		loaded := registerProfile(t, "name = \"gw\"\nbase_url = \"https://gw.example\"\n\n[[models]]\nid = \""+model+"\"\n"+line+"\n")
		table := pricing.Snapshot()
		table.Overlay(profile.Pricing(loaded))
		installCapabilities(table)
		if _, ok := table.Entry(model); ok {
			t.Errorf("%s: a declaration alone made a price-table entry", line)
		}
		for _, flow := range []provider.Flow{"", provider.FlowClassifier, provider.FlowTitle} {
			c := provider.CompletionOpts{Flow: flow}.Capabilities(model)
			if got := provider.EffortLow.Fit(c); got != provider.EffortLow {
				t.Errorf("%s, flow %q: effort fits to %v, want low", line, flow, got)
			}
			if c.MaxOutputTokens != floor.MaxOutputTokens || c.MaxOutputTokens == 0 {
				t.Errorf("%s, flow %q: output ceiling %d, want the floor's %d", line, flow, c.MaxOutputTokens, floor.MaxOutputTokens)
			}
		}
		provider.SetCapabilityLookup(nil)
	}
}

// A downloaded table landing after registration does not undo a
// declaration: the table answers what it describes, and the declarations
// narrow it afterwards.
func TestDeclarations_SurviveAPricingLoad(t *testing.T) {
	registerProfile(t, `
name     = "gw"
base_url = "https://gw.example"

[[models]]
id                 = "claude-sonnet-5-5"
structured_outputs = false

[[models]]
id    = "claude-opus-5-5"
flows = { classifier = { structured_outputs = false } }
`)
	downloaded := pricing.NewTable(map[string]pricing.ModelPricing{
		"claude-sonnet-5-5": {InputCostPerToken: 1, SupportsReasoning: true, AdaptiveThinking: true},
		"claude-opus-5-5":   {InputCostPerToken: 1, SupportsReasoning: true, AdaptiveThinking: true},
	})
	installCapabilities(downloaded)
	t.Cleanup(func() { provider.SetCapabilityLookup(nil) })

	if c := provider.CapabilitiesFor("claude-sonnet-5-5"); c.StructuredOutputs || !c.NoSchema || !c.Adaptive {
		t.Errorf("the model-wide declaration must survive the table, got %+v", c)
	}
	if c := (provider.CompletionOpts{Flow: provider.FlowClassifier}).Capabilities("claude-opus-5-5"); c.StructuredOutputs || !c.Adaptive {
		t.Errorf("the flow scope must survive the table, got %+v", c)
	}
	if c := (provider.CompletionOpts{Flow: provider.FlowBacklog}).Capabilities("claude-opus-5-5"); !c.StructuredOutputs {
		t.Errorf("an unscoped flow keeps the floor's schema under the table, got %+v", c)
	}
}
