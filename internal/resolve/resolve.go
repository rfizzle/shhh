package resolve

import "os"

const DefaultProvider = "openai"

var defaultModels = map[string]string{
	"openai":            "gpt-4o",
	"anthropic":         "claude-opus-5",
	"gemini":            "gemini-2.5-flash",
	"openrouter":        "anthropic/claude-sonnet-4-6",
	"openai-compatible": "llama3",
}

type Opts struct {
	FlagProvider string
	FlagModel    string
	FlagAPIKey   string

	ConfigProvider string
	ConfigModel    string
}

type Resolved struct {
	Provider string
	Model    string
}

func Resolve(opts Opts) Resolved {
	provider := First(opts.FlagProvider, os.Getenv("SHHH_PROVIDER"), opts.ConfigProvider, DefaultProvider)
	model := First(opts.FlagModel, os.Getenv("SHHH_MODEL"), opts.ConfigModel, defaultModels[provider])
	return Resolved{
		Provider: provider,
		Model:    model,
	}
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
