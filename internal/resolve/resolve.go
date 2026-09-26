package resolve

import "os"

const DefaultProvider = "openai-responses"

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
	"openai":            "gpt-5.6-terra",
	"openai-responses":  "gpt-5.6-terra",
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

	// SurfaceKey and SurfaceModel are the key the resolving surface reads
	// its own model from — `provider.code_model` for `shhh code` — and what
	// that key holds. The model resolves flag, environment, this, then
	// ConfigModel, so a surface that names no key of its own resolves as it
	// always did.
	// See docs/capabilities/configuration.md#each-surface-can-have-a-model-of-its-own.
	SurfaceKey   string
	SurfaceModel string
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
	model := First(opts.FlagModel, os.Getenv("SHHH_MODEL"), opts.SurfaceModel, opts.ConfigModel, defaultModels[provider])
	return Resolved{
		Provider:  provider,
		Model:     model,
		Reasoning: First(opts.FlagReasoning, os.Getenv("SHHH_REASONING"), opts.ConfigReasoning),
	}
}

// ModelOutranks names what is deciding the model ahead of provider.model, or
// "" when nothing is. The order above is a precedence nobody can see, and a
// setting overruled by something invisible is indistinguishable from one that
// was never saved — which is exactly how `/model default` came to look broken
// while writing the file correctly every time.
//
// Only the three ranks above provider.model count: the flag, the
// environment, and the surface's own key. Below it there is nothing to
// report: a provider.model that is set is the answer.
func ModelOutranks(opts Opts) string {
	if opts.FlagModel != "" {
		return "--model " + opts.FlagModel + " is on the command line"
	}
	if v := os.Getenv("SHHH_MODEL"); v != "" {
		return "SHHH_MODEL is set to " + v
	}
	if opts.SurfaceModel != "" {
		return opts.SurfaceKey + " is set to " + opts.SurfaceModel
	}
	return ""
}

// ModelFrom names the rank that decided the model Resolve answers with, in
// the words a reader would type to change it: the flag, the variable, the
// surface's own key, provider.model, or the provider's default. It is the
// same order as Resolve and read off the same values, so the surface that
// says which key chose the model cannot disagree with the one that chose it.
func ModelFrom(opts Opts) string {
	switch {
	case opts.FlagModel != "":
		return "--model"
	case os.Getenv("SHHH_MODEL") != "":
		return "SHHH_MODEL"
	case opts.SurfaceModel != "":
		return opts.SurfaceKey
	case opts.ConfigModel != "":
		return "provider.model"
	}
	return "the provider's default"
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
