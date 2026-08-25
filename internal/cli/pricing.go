package cli

// Pricing for a session: the public table shhh downloads, with any gateway
// profile's declared costs and context windows layered on top. A private
// gateway's catalog returns bare ids, so without the overlay its models have
// no price column, no spend, and no context meter.

import (
	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/profile"
)

// loadPricing returns the pricing table for this process. A failed download
// is not fatal — the profile overlay alone still prices a gateway's models.
func loadPricing() *pricing.Table {
	table, err := pricing.Load()
	overlay := profile.Pricing(profile.Loaded())
	if err != nil || table == nil {
		if len(overlay) == 0 {
			return table
		}
		table = pricing.NewTable(nil)
	}
	table.Overlay(overlay)
	return table
}
