package provider

import (
	"fmt"
	"strings"
)

type ResolveOpts struct {
	APIKey  string
	Model   string
	BaseURL string
	Name    string

	ConfigAPIKey  string
	ConfigBaseURL string
	ConfigName    string
}

type ProviderDefaults struct {
	Model   string
	BaseURL string
}

type Factory func(ResolveOpts) (Provider, error)

var registry = map[string]Factory{}
var defaults = map[string]ProviderDefaults{}

func Register(name string, f Factory) {
	registry[name] = f
}

func RegisterDefaults(name string, d ProviderDefaults) {
	defaults[name] = d
}

func Defaults(name string) ProviderDefaults {
	return defaults[normalizeName(name)]
}

func normalizeName(name string) string {
	return strings.ReplaceAll(name, "_", "-")
}

func Resolve(name string, opts ...ResolveOpts) (Provider, error) {
	f, ok := registry[normalizeName(name)]
	if !ok {
		return nil, fmt.Errorf("unknown provider: %q", name)
	}
	var o ResolveOpts
	if len(opts) > 0 {
		o = opts[0]
	}
	return f(o)
}

func Available() []string {
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	return names
}
