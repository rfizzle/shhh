package profile

// Profile metadata feeding the spend meter and the context gauge. A gateway
// that returns bare ids from its catalog leaves shhh blind on both: no price
// column in the /model picker, no cost in the status bar, no context
// pressure. Declared metadata fills that in, and anything a profile leaves
// out still falls back to the public pricing table shhh already downloads.

import (
	"strings"

	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/provider"
)

// tokensPerMillion converts the per-million-token prices model cards publish
// into the per-token costs the pricing table uses.
const tokensPerMillion = 1_000_000

// Pricing turns declared model metadata into pricing-table entries. Models
// that declare no prices, no context window and no reasoning are left out,
// so the public table keeps answering for them.
func Pricing(profiles []Profile) map[string]pricing.ModelPricing {
	out := map[string]pricing.ModelPricing{}
	for _, p := range profiles {
		for _, m := range p.declaredModels() {
			if !m.Cost.HasPricing() && m.ContextWindow == 0 && !m.Reasoning.Declared() {
				continue
			}
			entry := pricing.ModelPricing{
				InputCostPerToken:  m.Cost.Input / tokensPerMillion,
				OutputCostPerToken: m.Cost.Output / tokensPerMillion,
				MaxInputTokens:     m.ContextWindow,
				MaxOutputTokens:    m.MaxTokens,
			}
			m.Reasoning.fill(&entry)
			out[m.ID] = entry
		}
	}
	return out
}

// fill writes a declared reasoning shape onto a table entry. A declaration
// of "none" is a statement too — it marks the entry known so it overrides
// whatever the public table believed.
func (r Reasoning) fill(entry *pricing.ModelPricing) {
	if !r.Declared() {
		return
	}
	entry.ReasoningKnown = true
	kind := strings.ToLower(r.Kind)
	if kind == "none" {
		return
	}
	entry.SupportsReasoning = true
	entry.AdaptiveThinking = kind == "adaptive"
	entry.LegacyThinking = kind == "budget"
	entry.ThinkingAlwaysOn = r.AlwaysOn
	entry.XHighEffort = r.hasLevel(provider.EffortXHigh)
	entry.MaxEffort = r.hasLevel(provider.EffortMax)
}

// declaredModels is every model a profile declares, at the top level and
// inside its endpoints. Metadata is metadata wherever it was written; the
// endpoint it belongs to matters to routing, not to the spend meter.
func (p Profile) declaredModels() []Model {
	var out []Model
	seen := map[string]bool{}
	for _, r := range p.Routes() {
		for _, m := range r.Models {
			if seen[m.ID] {
				continue
			}
			seen[m.ID] = true
			out = append(out, m)
		}
	}
	return out
}
