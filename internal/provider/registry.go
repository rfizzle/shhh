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
	// CacheTTL is the configured lifetime of the request's fixed head, as
	// the file spells it. A dialect that caches on its own ignores it; one
	// that has to be told reads it through cacheTTLOrDefault, so an empty or
	// unreadable value is the default rather than a refusal (cache.go).
	CacheTTL string
}

type ProviderDefaults struct {
	Model   string
	BaseURL string
	// CheapModel is the small, fast model this provider's bounded calls run
	// on: the permission classifier, the session summary and the title it
	// shares. They are judgements over evidence the session has already
	// assembled, and the classifier alone fires on every gated call, so
	// running them on whatever the session picked is the harness's largest
	// avoidable cost. Empty means the provider has no small model anyone can
	// name ahead of time — a local endpoint serves whatever was loaded — and
	// those calls fall back to the session model.
	// See docs/capabilities/providers.md#a-bounded-call-runs-on-the-small-model.
	CheapModel string
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
