package provider

// What a profile may declare about a model, and at which scope. A model's
// capabilities come from the table, then the family floor, then the
// declarations that narrow them; a declaration may be written for the model
// as a whole or scoped to one flow, the bounded call that builds the request.
// The flow is named on the request by the code that builds it, so the
// provider never guesses which call it is serving.
// See docs/capabilities/providers.md#a-bounded-call-asks-for-the-shape-of-its-answer.

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
)

// Flow names one bounded call outside the main agent and its children. The
// empty Flow is the main agent and every child: nothing may be scoped there.
type Flow string

// The closed set of flows. A word here is what a profile writes under a
// model's `flows` table, and what the call that builds the request sets.
const (
	FlowClassifier       Flow = "classifier"
	FlowExplanation      Flow = "explanation"
	FlowDescription      Flow = "description"
	FlowReading          Flow = "reading"
	FlowTitle            Flow = "title"
	FlowAccount          Flow = "account"
	FlowSuggestion       Flow = "suggestion"
	FlowStartOffers      Flow = "start_offers"
	FlowPatterns         Flow = "patterns"
	FlowCompaction       Flow = "compaction"
	FlowBacklog          Flow = "backlog"
	FlowProfileDrafter   Flow = "profile_drafter"
	FlowToolchainDrafter Flow = "toolchain_drafter"
)

var flows = []Flow{
	FlowClassifier, FlowExplanation, FlowDescription, FlowReading,
	FlowTitle, FlowAccount, FlowSuggestion, FlowStartOffers, FlowPatterns,
	FlowCompaction, FlowBacklog, FlowProfileDrafter, FlowToolchainDrafter,
}

// Flows is the closed set, in the order a listing reads it.
func Flows() []Flow { return slices.Clone(flows) }

// ParseFlow reads a flow word, reporting whether it is one of the set.
func ParseFlow(s string) (Flow, bool) {
	f := Flow(s)
	return f, slices.Contains(flows, f)
}

// FlowWords is the closed set as a profile writes it, for a refusal.
func FlowWords() string {
	words := make([]string, len(flows))
	for i, f := range flows {
		words[i] = string(f)
	}
	return strings.Join(words, ", ")
}

// Declaration is one setting a profile may declare on a model. Every entry
// only narrows what the table and the floor answered, so no declaration,
// however it is scoped, can widen a request. None reaches a prompt: the
// model is never told what was declared about it.
type Declaration struct {
	// Key is what a `[[models]]` line writes, model-wide or under a flow.
	Key string
	// Flows are the flows that may scope it: the ones whose request carries
	// what it narrows. A flow outside them has nothing to narrow, and a
	// profile scoping the setting there is refused at load.
	Flows []Flow
	// Unscoped is why a flow outside Flows cannot scope it, said after the
	// flow's word.
	Unscoped string
	// Check is the rule a written value must meet.
	Check func(value any) error
	// Narrow is what a declared value does to the resolved answer.
	Narrow func(c *Capabilities, value any)
	// Instead is what goes out on a flow the declaration narrowed, in the
	// words the doctor's flows row says it.
	Instead string
}

// Scopes reports whether a profile may scope the setting to flow.
func (d Declaration) Scopes(flow Flow) bool { return slices.Contains(d.Flows, flow) }

// declarations is the registry. The next setting is one entry here and the
// Capabilities field its reader checks; the loader, the resolver and the
// doctor read this table and need nothing new.
var declarations = []Declaration{{
	// The two flows that send a schema at all: the classifier's verdict and
	// the backlog's proposals. Every other bounded call sends none.
	Key:      "structured_outputs",
	Flows:    []Flow{FlowClassifier, FlowBacklog},
	Unscoped: "sends no schema; there is nothing to narrow",
	// There is no true, because a schema sent to a model that cannot take
	// one is a refused request while a tool sent to one that could have
	// taken a schema is free.
	Check: func(value any) error {
		b, ok := value.(bool)
		if !ok || b {
			return fmt.Errorf("structured_outputs can only be false: a schema sent to a model that cannot take one is a refused request")
		}
		return nil
	},
	Narrow: func(c *Capabilities, _ any) {
		c.StructuredOutputs = false
		c.NoSchema = true
	},
	Instead: "its tool, not a schema",
}}

// Declarations is the registry, in the order a listing reads it.
func Declarations() []Declaration { return slices.Clone(declarations) }

// DeclarationFor is the registry's entry for a profile key.
func DeclarationFor(key string) (Declaration, bool) {
	for _, d := range declarations {
		if d.Key == key {
			return d, true
		}
	}
	return Declaration{}, false
}

// DeclarationKeys is every key the registry holds, for a refusal.
func DeclarationKeys() string {
	keys := make([]string, len(declarations))
	for i, d := range declarations {
		keys[i] = d.Key
	}
	return strings.Join(keys, ", ")
}

// Declared is what one source declared about one model: the settings
// written for the model as a whole, and those scoped to a flow.
type Declared struct {
	Model map[string]any
	Flows map[Flow]map[string]any
}

var (
	declaredMu sync.RWMutex
	// declared is each registered source's declarations, by model id.
	declared = map[string]map[string]Declared{}
)

// RegisterDeclarations records what the source called name declares, by
// model id, replacing what it declared before. A profile registers its
// models' lines; nil or empty withdraws them. Two sources declaring the same
// model both apply, which is the safe direction for a rule that only
// narrows.
func RegisterDeclarations(name string, models map[string]Declared) {
	declaredMu.Lock()
	defer declaredMu.Unlock()
	key := normalizeName(name)
	if len(models) == 0 {
		delete(declared, key)
		return
	}
	declared[key] = models
}

// DeclaredOn names the sources whose declaration of key applies to model on
// flow, model-wide or scoped there, in name order. A flow the key cannot
// scope is answered by the model-wide line alone.
func DeclaredOn(model string, flow Flow, key string) []string {
	d, ok := DeclarationFor(key)
	if !ok {
		return nil
	}
	declaredMu.RLock()
	defer declaredMu.RUnlock()
	var out []string
	for name, models := range declared {
		m, ok := models[model]
		if !ok {
			continue
		}
		if _, ok := m.Model[key]; ok {
			out = append(out, name)
			continue
		}
		if _, ok := m.Flows[flow][key]; ok && d.Scopes(flow) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// narrow applies every registered declaration on model: the model-wide
// lines, then the lines scoped to flow, each through its registry entry.
// A value that fails its entry's rule, or a flow the entry cannot scope, is
// passed over, so nothing the loader would refuse reaches a request.
func narrow(c Capabilities, model string, flow Flow) Capabilities {
	declaredMu.RLock()
	defer declaredMu.RUnlock()
	for _, models := range declared {
		m, ok := models[model]
		if !ok {
			continue
		}
		for _, d := range declarations {
			if v, ok := m.Model[d.Key]; ok && d.Check(v) == nil {
				d.Narrow(&c, v)
			}
			if flow == "" || !d.Scopes(flow) {
				continue
			}
			if v, ok := m.Flows[flow][d.Key]; ok && d.Check(v) == nil {
				d.Narrow(&c, v)
			}
		}
	}
	return c
}
