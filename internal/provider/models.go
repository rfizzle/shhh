package provider

// knownModels is the curated per-provider model catalog backing the /model
// interactive picker. It is a convenience list, not a gate — /model <name>
// accepts any name, and providers registered without a catalog (e.g.
// openai-compatible endpoints, whose models are whatever the endpoint hosts)
// simply have no picker entries beyond the session's own model.
var knownModels = map[string][]string{
	"anthropic": {
		"claude-fable-5",
		"claude-opus-5",
		"claude-sonnet-5",
		"claude-haiku-4-5",
	},
	"openai": {
		"gpt-4o",
		"gpt-4o-mini",
		"gpt-4.1",
		"gpt-4.1-mini",
		"o3",
		"o4-mini",
	},
	"gemini": {
		"gemini-2.5-pro",
		"gemini-2.5-flash",
		"gemini-2.5-flash-lite",
	},
	"openrouter": {
		"anthropic/claude-opus-5",
		"anthropic/claude-sonnet-4-6",
		"openai/gpt-4o",
		"google/gemini-2.5-pro",
	},
}

// KnownModels returns the curated model names for a registered provider, or
// nil when the provider's models can't be known ahead of time.
func KnownModels(name string) []string {
	models := knownModels[normalizeName(name)]
	if len(models) == 0 {
		return nil
	}
	return append([]string(nil), models...)
}
