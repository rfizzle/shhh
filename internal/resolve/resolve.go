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
}

type Resolved struct {
	Provider string
	Model    string
	APIKey   string
}

func Resolve(opts Opts) Resolved {
	return Resolved{
		Provider: first(opts.FlagProvider, os.Getenv("SHHH_PROVIDER"), DefaultProvider),
		Model:    first(opts.FlagModel, os.Getenv("SHHH_MODEL"), DefaultModel),
		APIKey:   first(opts.FlagAPIKey, ""),
	}
}

func first(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
