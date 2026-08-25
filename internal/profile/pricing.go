package profile

// Profile metadata feeding the spend meter and the context gauge. A gateway
// that returns bare ids from its catalog leaves shhh blind on both: no price
// column in the /model picker, no cost in the status bar, no context
// pressure. Declared metadata fills that in, and anything a profile leaves
// out still falls back to the public pricing table shhh already downloads.

import "github.com/rfizzle/shhh/internal/pricing"

// tokensPerMillion converts the per-million-token prices model cards publish
// into the per-token costs the pricing table uses.
const tokensPerMillion = 1_000_000

// Pricing turns declared model metadata into pricing-table entries. Models
// that declare neither prices nor a context window are left out, so the
// public table keeps answering for them.
func Pricing(profiles []Profile) map[string]pricing.ModelPricing {
	out := map[string]pricing.ModelPricing{}
	for _, p := range profiles {
		for _, m := range p.Models {
			if !m.Cost.HasPricing() && m.ContextWindow == 0 {
				continue
			}
			out[m.ID] = pricing.ModelPricing{
				InputCostPerToken:  m.Cost.Input / tokensPerMillion,
				OutputCostPerToken: m.Cost.Output / tokensPerMillion,
				MaxInputTokens:     m.ContextWindow,
			}
		}
	}
	return out
}
