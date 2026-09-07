package provider

import (
	"context"
	"fmt"
	"net/http"
	"os"

	openai "github.com/sashabaranov/go-openai"
)

const (
	defaultOpenRouterBaseURL = "https://openrouter.ai/api/v1"
	// OpenRouter writes the generation with a dot where Anthropic's own API
	// writes a hyphen, and the gateway does not forgive the near miss: it
	// answers `404 No endpoints found for anthropic/claude-sonnet-4-6` to
	// the first request of every session that took the default, and no price
	// table has heard of that id either, so the meter bills the whole
	// conversation at nothing.
	defaultOpenRouterModel = "anthropic/claude-sonnet-4.6"
	// cheapOpenRouterModel is the gateway's spelling of the same Haiku the
	// Anthropic provider names, because the gateway's own default is an
	// Anthropic model and the two should not disagree about which small
	// model that family has.
	cheapOpenRouterModel = "anthropic/claude-haiku-4.5"
)

type OpenRouter struct {
	client   *openai.Client
	model    string
	classify func(error) error
}

func NewOpenRouter(opts ResolveOpts) (*OpenRouter, error) {
	key := first(opts.APIKey, os.Getenv("SHHH_API_KEY"), os.Getenv("OPENROUTER_API_KEY"), opts.ConfigAPIKey)
	if key == "" {
		return nil, fmt.Errorf("SHHH_API_KEY or OPENROUTER_API_KEY is not set")
	}

	model := first(opts.Model, defaultOpenRouterModel)
	baseURL := first(opts.BaseURL, os.Getenv("SHHH_BASE_URL"), opts.ConfigBaseURL, defaultOpenRouterBaseURL)

	cfg := openai.DefaultConfig(key)
	cfg.BaseURL = baseURL
	cfg.HTTPClient = &http.Client{
		Transport: &openRouterTransport{
			base: NewCacheMarkTransport(http.DefaultTransport, opts.CacheTTL),
		},
	}

	return &OpenRouter{
		client:   openai.NewClientWithConfig(cfg),
		model:    model,
		classify: newClassifier("openrouter", "SHHH_API_KEY or OPENROUTER_API_KEY", key),
	}, nil
}

// openRouterTransport is the one thing this gateway needs said that the
// OpenAI client has no field for: the two attribution headers. They are a
// property of the wire and not of the conversation, so this is where they can
// be reached.
//
// The cache breakpoints are the other such property and are no longer here.
// They belong to every path that sends this dialect to a gateway, not to this
// provider, so they sit underneath as their own round tripper
// (NewCacheMarkTransport) — which is what a profile route wraps too.
type openRouterTransport struct {
	base http.RoundTripper
}

func (t *openRouterTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("HTTP-Referer", "https://github.com/rfizzle/shhh")
	req.Header.Set("X-Title", "shhh")
	return t.base.RoundTrip(req)
}

func NewOpenRouterWith(client *openai.Client, model string) *OpenRouter {
	if model == "" {
		model = defaultOpenRouterModel
	}
	return &OpenRouter{
		client:   client,
		model:    model,
		classify: newClassifier("openrouter", "SHHH_API_KEY or OPENROUTER_API_KEY", ""),
	}
}

func (o *OpenRouter) Name() string { return "openrouter" }

// ListModels enumerates OpenRouter's catalog from GET /v1/models — hundreds
// of ids, so the picker shows them filtered to the chat-capable ones.
func (o *OpenRouter) ListModels(ctx context.Context) ([]string, error) {
	return listOpenAIModels(ctx, o.client)
}

func (o *OpenRouter) StreamCompletion(ctx context.Context, messages []Message, opts CompletionOpts) (<-chan StreamEvent, error) {
	model := o.model
	if opts.Model != "" {
		model = opts.Model
	}

	req := openai.ChatCompletionRequest{
		Model:    model,
		Messages: toOpenAIMessages(messages),
		Stream:   true,
		StreamOptions: &openai.StreamOptions{
			IncludeUsage: true,
		},
	}
	if opts.Temperature != nil {
		req.Temperature = float32(*opts.Temperature)
	}
	if opts.MaxTokens > 0 {
		// The ceiling goes in `max_completion_tokens`: chat completions
		// deprecated `max_tokens` and the reasoning families refuse it
		// outright, answering a 400 that names the field. Only a bounded
		// auxiliary call ever sets a ceiling here — a turn sends none — so
		// the old field failed exactly where nobody was watching.
		req.MaxCompletionTokens = opts.MaxTokens
	}
	// Reasoning effort, fitted to the model: a rung it lacks becomes the
	// highest it has, and a model with no reasoning gets no field, because
	// `reasoning_effort` on gpt-4o is a 400.
	if effort := opts.Effort.Fit(CapabilitiesFor(req.Model)).OpenAIEffort(); effort != "" {
		req.ReasoningEffort = effort
	}
	if len(opts.Tools) > 0 {
		req.Tools = toOpenAITools(opts.Tools)
		if opts.ToolChoice != "" {
			req.ToolChoice = opts.ToolChoice
		}
	}

	stream, err := o.client.CreateChatCompletionStream(ctx, req)
	if err != nil {
		return nil, o.classify(err)
	}

	return streamOpenAIToolCalls(stream, o.classify), nil
}

func init() {
	Register("openrouter", func(opts ResolveOpts) (Provider, error) {
		return NewOpenRouter(opts)
	})
	RegisterDefaults("openrouter", ProviderDefaults{
		Model:      defaultOpenRouterModel,
		BaseURL:    defaultOpenRouterBaseURL,
		CheapModel: cheapOpenRouterModel,
	})
}
