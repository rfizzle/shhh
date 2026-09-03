package provider

// What a model can be asked to think, and how. The answer comes from the
// model-data table first — fetched, current, and the same file that prices
// the session — and from a by-family floor for the models the table has not
// caught up with, so a brand-new model is sent the shape its family takes
// rather than nothing
// (docs/capabilities/providers.md#model-data-is-fetched-and-a-snapshot-ships).

import "strings"

// Capabilities is what is known about a model's reasoning knob.
type Capabilities struct {
	// Known is whether anything described the model. When it is false every
	// other field is meaningless and Fit leaves the level alone.
	Known bool
	// Reasoning is whether the model has a thinking knob at all.
	Reasoning bool
	// Adaptive is an Anthropic model whose knob is a named effort under
	// adaptive thinking; Legacy is one that takes a token budget. The 4.6
	// generation is both, and adaptive wins there.
	Adaptive bool
	Legacy   bool
	// AlwaysOn is a model that thinks whether or not it is asked to.
	AlwaysOn bool
	// XHigh and Max are the rungs above high the model accepts.
	XHigh bool
	Max   bool
	// MaxOutputTokens is the model's output ceiling, or 0 when unknown.
	MaxOutputTokens int64
	// StructuredOutputs is whether the model can be told to answer with
	// JSON matching a schema. It is the one field the table never fills —
	// it has no column for it — so it is answered by the family floor even
	// for a model the table describes.
	StructuredOutputs bool
}

// capabilityLookup is the table-backed answer, installed by the CLI once it
// has loaded the model data. The provider package cannot import the table
// itself without dragging the network into every provider test.
var capabilityLookup func(model string) (Capabilities, bool)

// SetCapabilityLookup installs the table-backed answer for CapabilitiesFor.
func SetCapabilityLookup(fn func(model string) (Capabilities, bool)) {
	capabilityLookup = fn
}

// CapabilitiesFor is what is known about a model: the table's entry when it
// has one, the family floor otherwise.
func CapabilitiesFor(model string) Capabilities {
	if capabilityLookup != nil {
		if c, ok := capabilityLookup(model); ok {
			c.Known = true
			// The table says what a model costs, how much context it has
			// and how it spells its thinking level, and nothing about
			// whether it takes a schema. A field nobody filled reads as
			// "no" and would switch structured output off for every model
			// the table knows, which is nearly all of them — so the floor
			// answers this one whether or not the table answered the rest.
			//
			// A model in no family the floor names is therefore sent no
			// schema even where the table describes it fully, and that is
			// the right way round: the caller's fallback is the tools,
			// which every model takes, while a schema sent to a model that
			// does not take one is a refused request. The two ways to be
			// wrong are not symmetric here, so silence means the tools.
			c.StructuredOutputs = familyCapabilities(model).StructuredOutputs
			return c
		}
	}
	return familyCapabilities(model)
}

// family is one row of the floor: the longest matching prefix wins, so
// "claude-opus-4-6" outranks "claude-".
type family struct {
	prefix string
	caps   Capabilities
}

var (
	// The current Anthropic generation: adaptive thinking, every rung.
	anthropicCurrent = Capabilities{Known: true, Reasoning: true, Adaptive: true, XHigh: true, Max: true, MaxOutputTokens: 128_000, StructuredOutputs: true}
	// The same, on a model that cannot be told not to think.
	anthropicAlwaysOn = Capabilities{Known: true, Reasoning: true, Adaptive: true, AlwaysOn: true, XHigh: true, Max: true, MaxOutputTokens: 128_000, StructuredOutputs: true}
	// The 4.6 generation: adaptive with a legacy budget still accepted, no
	// xhigh.
	anthropic46 = Capabilities{Known: true, Reasoning: true, Adaptive: true, Legacy: true, Max: true, MaxOutputTokens: 128_000, StructuredOutputs: true}
	// Budgeted thinking, low/medium/high only. The 4.5 generation is where
	// the Messages API learned to constrain an answer to a schema; the
	// generations under it take the same budget and none of them takes a
	// schema, which is why the two rows are not one row.
	anthropic45     = Capabilities{Known: true, Reasoning: true, Legacy: true, MaxOutputTokens: 64_000, StructuredOutputs: true}
	anthropicLegacy = Capabilities{Known: true, Reasoning: true, Legacy: true, MaxOutputTokens: 64_000}
	noReasoning     = Capabilities{Known: true}
	// A model with no thinking knob that still takes a schema: the two
	// GPT-4 lines that were given one. The pair of fields is not one
	// question — the older models in the same family answer no to both,
	// and these answer no to one.
	noReasoningStructured = Capabilities{Known: true, StructuredOutputs: true}
	namedEffort           = Capabilities{Known: true, Reasoning: true, StructuredOutputs: true}
	namedEffortX          = Capabilities{Known: true, Reasoning: true, XHigh: true, StructuredOutputs: true}
	budgeted              = Capabilities{Known: true, Reasoning: true, StructuredOutputs: true}
)

// knownFamilies is the floor. A new model in a family lands on its family's
// row, which is right far more often than "unknown" — the newest Claude is
// adaptive, the newest GPT has xhigh — and the table overrides it the day
// it catches up.
var knownFamilies = []family{
	{"claude-fable", anthropicAlwaysOn},
	{"claude-mythos", anthropicAlwaysOn},
	{"claude-opus-4-6", anthropic46},
	{"claude-sonnet-4-6", anthropic46},
	{"claude-opus-4-5", anthropic45},
	{"claude-sonnet-4-5", anthropic45},
	{"claude-haiku-4-5", anthropic45},
	{"claude-opus-4", anthropicLegacy},
	{"claude-sonnet-4", anthropicLegacy},
	{"claude-3-7", anthropicLegacy},
	{"claude-3", noReasoning},
	{"claude-", anthropicCurrent},
	{"gpt-5.2", namedEffortX},
	{"gpt-5", namedEffort},
	// The strict schema arrived partway through the GPT-4 line, so the two
	// generations that have it are named and the bare prefix keeps the
	// snapshots that predate it. A row for the whole line would send the
	// field to a 2023 snapshot, which answers a 400.
	{"gpt-4o", noReasoningStructured},
	{"gpt-4.1", noReasoningStructured},
	{"gpt-4", noReasoning},
	{"gpt-3", noReasoning},
	{"chatgpt-", noReasoning},
	{"o1", namedEffort},
	{"o3", namedEffort},
	{"o4", namedEffort},
	{"gemini-2.5", budgeted},
	{"gemini-3", budgeted},
	// Gemini 2.0 takes a schema only in the OpenAPI-subset field, which is
	// not the field shhh sends, and it is silent rather than loud about the
	// one it does not know. A capability is a claim about what a request
	// will do, so it is claimed only where the request shhh writes carries
	// it: the 2.5 generation up.
	{"gemini-2", noReasoning},
	{"gemini-1", noReasoning},
}

// familyCapabilities is the floor's answer, on the model half of a
// vendor-qualified name and without a "[1m]" marker.
func familyCapabilities(model string) Capabilities {
	name := strings.ToLower(strings.TrimSpace(model))
	name = strings.TrimSuffix(name, "[1m]")
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	best, bestLen := Capabilities{}, 0
	for _, f := range knownFamilies {
		if strings.HasPrefix(name, f.prefix) && len(f.prefix) > bestLen {
			best, bestLen = f.caps, len(f.prefix)
		}
	}
	return best
}
