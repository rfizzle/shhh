package resolve

import "os"

const DefaultProvider = "openai"

// defaultModels is what each built-in provider is asked for when nothing
// named a model. It is a second copy of provider.Defaults, kept because this
// package answers "what would this session run on" for surfaces that must not
// construct a provider to find out — `shhh providers`, the config screen —
// and importing the provider registry to read one string would pull every
// vendor SDK into all of them.
//
// A copy that disagrees is the whole risk, and it is not theoretical: the
// gateway entry was a hyphen where OpenRouter writes a dot, in both tables at
// once. The test beside this one is what holds them together.
var defaultModels = map[string]string{
	"openai":            "gpt-4o",
	"openai-responses":  "gpt-4.1",
	"anthropic":         "claude-opus-5",
	"gemini":            "gemini-2.5-flash",
	"openrouter":        "anthropic/claude-sonnet-4.6",
	"openai-compatible": "llama3",
}

type Opts struct {
	FlagProvider string
	FlagModel    string
	FlagAPIKey   string
	// FlagReasoning is --reasoning: the level of thinking a session starts
	// on. It resolves like the model does — flag, then SHHH_REASONING, then
	// the config file — and an unset chain means the default level.
	FlagReasoning string

	ConfigProvider  string
	ConfigModel     string
	ConfigReasoning string
}

type Resolved struct {
	Provider string
	Model    string
	// Reasoning is the session's starting reasoning level, unparsed; "" is
	// the default (provider.DefaultEffort). provider.ParseEffort turns it
	// into a level, and rejects a value nobody meant rather than quietly
	// reading it as the default.
	Reasoning string
}

func Resolve(opts Opts) Resolved {
	provider := First(opts.FlagProvider, os.Getenv("SHHH_PROVIDER"), opts.ConfigProvider, DefaultProvider)
	model := First(opts.FlagModel, os.Getenv("SHHH_MODEL"), opts.ConfigModel, defaultModels[provider])
	return Resolved{
		Provider:  provider,
		Model:     model,
		Reasoning: First(opts.FlagReasoning, os.Getenv("SHHH_REASONING"), opts.ConfigReasoning),
	}
}

// ModelOutranks names what is deciding the model ahead of the config file, or
// "" when nothing is. The order above is a precedence nobody can see, and a
// setting overruled by something invisible is indistinguishable from one that
// was never saved — which is exactly how `/model default` came to look broken
// while writing the file correctly every time.
//
// Only the two ranks above the config file count. Below it there is nothing
// to report: a config value that is set is the answer.
func ModelOutranks(opts Opts) string {
	if opts.FlagModel != "" {
		return "--model " + opts.FlagModel + " is on the command line"
	}
	if v := os.Getenv("SHHH_MODEL"); v != "" {
		return "SHHH_MODEL is set to " + v
	}
	return ""
}

// ReasoningOutranks is ModelOutranks for the reasoning level, and exists for
// the same reason: a level written to the config file and then overruled by a
// flag or an env var is indistinguishable from one that was never saved.
func ReasoningOutranks(opts Opts) string {
	if opts.FlagReasoning != "" {
		return "--reasoning " + opts.FlagReasoning + " is on the command line"
	}
	if v := os.Getenv("SHHH_REASONING"); v != "" {
		return "SHHH_REASONING is set to " + v
	}
	return ""
}

// ProviderOutranks is ModelOutranks for the provider, and exists for the same
// reason: `provider.default` is as quietly overrulable as `provider.model`.
func ProviderOutranks(opts Opts) string {
	if opts.FlagProvider != "" {
		return "--provider " + opts.FlagProvider + " is on the command line"
	}
	if v := os.Getenv("SHHH_PROVIDER"); v != "" {
		return "SHHH_PROVIDER is set to " + v
	}
	return ""
}

func DefaultModel(provider string) string {
	return defaultModels[provider]
}

func First(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
