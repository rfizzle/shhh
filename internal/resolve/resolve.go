package resolve

import "os"

const (
	DefaultProvider = "openai"
	DefaultModel    = "gpt-4o"
)

type Opts struct {
	FlagProvider string
	FlagModel    string
	FlagAPIKey   string

	ConfigProvider string
	ConfigModel    string
	ConfigAPIKey   string
}

type Resolved struct {
	Provider string
	Model    string
	APIKey   string
}

func Resolve(opts Opts) Resolved {
	return Resolved{
		Provider: First(opts.FlagProvider, os.Getenv("SHHH_PROVIDER"), opts.ConfigProvider, DefaultProvider),
		Model:    First(opts.FlagModel, os.Getenv("SHHH_MODEL"), opts.ConfigModel, DefaultModel),
		APIKey:   First(opts.FlagAPIKey, opts.ConfigAPIKey, ""),
	}
}

func First(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
