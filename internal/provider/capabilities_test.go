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
