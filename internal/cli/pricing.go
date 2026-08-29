package cli

// Pricing for a session: the public table shhh downloads, with any gateway
// profile's declared costs and context windows layered on top. A private
// gateway's catalog returns bare ids, so without the overlay its models have
// no price column, no spend, and no context meter.

import (
	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/profile"
	"github.com/rfizzle/shhh/internal/provider"
)

// loadPricing returns the model-data table for this process, and points the
// providers' reasoning ladder at it: the same file that prices a request
// says how the model spells its thinking level. A failed download is not
// fatal — the built-in snapshot and the profile overlay still answer.
func loadPricing() *pricing.Table {
	table, err := pricing.Load()
	overlay := profile.Pricing(profile.Loaded())
	if err != nil || table == nil {
		table = pricing.Snapshot()
	}
	table.Overlay(overlay)
	installCapabilities(table)
	return table
}

// installCapabilities makes the table the providers' answer for what a model
// can be asked to think.
func installCapabilities(table *pricing.Table) {
	provider.SetCapabilityLookup(func(model string) (provider.Capabilities, bool) {
		e, ok := table.Entry(model)
		if !ok {
			return provider.Capabilities{}, false
		}
		return provider.Capabilities{
			Known:           true,
			Reasoning:       e.SupportsReasoning,
			Adaptive:        e.AdaptiveThinking,
			Legacy:          e.LegacyThinking,
			AlwaysOn:        e.ThinkingAlwaysOn,
			XHigh:           e.XHighEffort,
			Max:             e.MaxEffort,
			MaxOutputTokens: e.MaxOutputTokens,
		}, true
	})
}
