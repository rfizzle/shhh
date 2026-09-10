package provider

import (
	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/rfizzle/shhh/internal/testhttp"
)

// providerTestHTTP keeps provider fixtures in process. A TCP listener proves
// nothing about a dialect's request or response shape and is unavailable to
// the contained quality suite.
var providerTestHTTP testhttp.Registry

func newTestAnthropic(opts ResolveOpts) *Anthropic {
	clientOpts := []option.RequestOption{
		option.WithAPIKey(first(opts.APIKey, "sk-test")),
		option.WithHTTPClient(providerTestHTTP.Client()),
	}
	if opts.BaseURL != "" {
		clientOpts = append(clientOpts, option.WithBaseURL(opts.BaseURL))
	}
	p := NewAnthropicNamed(
		anthropic.NewClient(clientOpts...), opts.Model, "anthropic", opts.CacheTTL,
	)
	p.idleDeadline = idleDeadlineOf(opts.StreamIdleSeconds)
	return p
}
