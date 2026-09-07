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
			if !m.Cost.anyRate() && m.ContextWindow == 0 && !m.Reasoning.Declared() {
				continue
			}
			// The cache rates travel with the other two. A gateway session
			// reads most of its prompt out of the provider's cache, so the
			// cached rate is the dominant term in the bill; dropping it here
			// left the meter charging the full input price for every one of
			// those tokens, which on a well-cached round is an order of
			// magnitude too much.
			// See docs/capabilities/providers.md#the-prompt-prefix-is-paid-for-once.
			entry := pricing.ModelPricing{
				InputCostPerToken:         m.Cost.Input / tokensPerMillion,
				OutputCostPerToken:        m.Cost.Output / tokensPerMillion,
				CacheReadCostPerToken:     m.Cost.CacheRead / tokensPerMillion,
				CacheCreationCostPerToken: m.Cost.CacheWrite / tokensPerMillion,
				MaxInputTokens:            m.ContextWindow,
				MaxOutputTokens:           m.MaxTokens,
			}
			m.Reasoning.fill(&entry)
			out[m.ID] = entry
		}
	}
	return out
}

// anyRate reports whether the entry declares a price of any kind. It is
// wider than HasPricing on purpose: a profile that says only what a cached
// read costs has told the meter something the public table does not know,
// and Overlay fills the rest of the entry back in from that table.
func (c Cost) anyRate() bool {
	return c.HasPricing() || c.CacheRead != 0 || c.CacheWrite != 0
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
