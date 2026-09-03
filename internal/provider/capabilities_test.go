package provider

import "testing"

func TestCapabilitiesFor_TableOutranksFamily(t *testing.T) {
	SetCapabilityLookup(func(model string) (Capabilities, bool) {
		if model == "claude-opus-5" {
			return Capabilities{Reasoning: true, Adaptive: true, XHigh: true, Max: true}, true
		}
		return Capabilities{}, false
	})
	defer SetCapabilityLookup(nil)

	c := CapabilitiesFor("claude-opus-5")
	if !c.Known || !c.Adaptive || !c.XHigh {
		t.Errorf("table entry should answer, got %+v", c)
	}
	// Not in the table: the family floor answers.
	if c := CapabilitiesFor("claude-haiku-4-5"); !c.Known || !c.Legacy || c.Adaptive {
		t.Errorf("haiku 4.5 should be budgeted thinking, got %+v", c)
	}
}

func TestFamilyCapabilities(t *testing.T) {
	cases := map[string]func(Capabilities) bool{
		"claude-opus-5":              func(c Capabilities) bool { return c.Adaptive && c.XHigh && c.Max && !c.AlwaysOn },
		"claude-fable-5":             func(c Capabilities) bool { return c.Adaptive && c.AlwaysOn },
		"claude-opus-4-6":            func(c Capabilities) bool { return c.Adaptive && c.Legacy && c.Max && !c.XHigh },
		"claude-sonnet-4-5-20250929": func(c Capabilities) bool { return c.Legacy && !c.Adaptive },
		"claude-3-5-sonnet":          func(c Capabilities) bool { return c.Known && !c.Reasoning },
		"claude-opus-7":              func(c Capabilities) bool { return c.Adaptive && c.XHigh },
		"anthropic/claude-opus-5":    func(c Capabilities) bool { return c.Adaptive },
		"gpt-4o":                     func(c Capabilities) bool { return c.Known && !c.Reasoning },
		"gpt-5":                      func(c Capabilities) bool { return c.Reasoning && !c.XHigh },
		"gpt-5.2":                    func(c Capabilities) bool { return c.Reasoning && c.XHigh },
		"o4-mini":                    func(c Capabilities) bool { return c.Reasoning },
		"gemini-2.5-flash":           func(c Capabilities) bool { return c.Reasoning && !c.Adaptive },
		"gemini-2.0-flash":           func(c Capabilities) bool { return c.Known && !c.Reasoning },
		"llama3":                     func(c Capabilities) bool { return !c.Known },
		"google/gemini-2.5-pro[1m]":  func(c Capabilities) bool { return c.Reasoning },
	}
	for model, ok := range cases {
		if c := familyCapabilities(model); !ok(c) {
			t.Errorf("%s: %+v", model, c)
		}
	}
}

// The table has no column for structured output, so the family floor answers
// that one even for a model the table describes. Without this a table entry
// would read as "takes no schema" for nearly every model there is.
func TestCapabilitiesFor_TheFloorAnswersStructuredOutput(t *testing.T) {
	SetCapabilityLookup(func(model string) (Capabilities, bool) {
		return Capabilities{Reasoning: true, Adaptive: true}, true
	})
	defer SetCapabilityLookup(nil)

	if c := CapabilitiesFor("claude-opus-5"); !c.StructuredOutputs {
		t.Errorf("a described model still takes a schema, got %+v", c)
	}
	if c := CapabilitiesFor("claude-3-5-sonnet"); c.StructuredOutputs {
		t.Errorf("a generation that predates the field takes none, got %+v", c)
	}
}

func TestFamilyCapabilities_StructuredOutput(t *testing.T) {
	takes := []string{
		"claude-opus-5", "claude-fable-5", "claude-opus-4-6", "claude-haiku-4-5",
		"gpt-4o-mini", "gpt-4.1", "gpt-5", "gpt-5.2", "o4-mini", "gemini-2.5-pro",
		"gemini-3-pro",
	}
	for _, model := range takes {
		if !familyCapabilities(model).StructuredOutputs {
			t.Errorf("%s should take a schema", model)
		}
	}
	// The generations under the field, and everything nothing describes:
	// an endpoint serving a name no table has seen is sent the tools.
	refuses := []string{"claude-3-5-sonnet", "claude-sonnet-4-20250514", "claude-3-7-sonnet",
		"gpt-4-turbo", "gpt-3.5-turbo", "chatgpt-4o-latest", "gemini-2.0-flash",
		"gemini-1.0-pro", "llama3"}
	for _, model := range refuses {
		if familyCapabilities(model).StructuredOutputs {
			t.Errorf("%s should be sent no schema", model)
		}
	}
}
