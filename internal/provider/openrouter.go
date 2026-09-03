package provider

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"

	openai "github.com/sashabaranov/go-openai"
)

const (
	defaultOpenRouterBaseURL = "https://openrouter.ai/api/v1"
	defaultOpenRouterModel   = "anthropic/claude-sonnet-4-6"
	// cheapOpenRouterModel is the gateway's spelling of the same Haiku the
	// Anthropic provider names, because the gateway's own default is an
	// Anthropic model and the two should not disagree about which small
	// model that family has. OpenRouter writes the generation with a dot.
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
			base:    http.DefaultTransport,
			headTTL: cacheTTLOrDefault(opts.CacheTTL),
		},
	}

	return &OpenRouter{
		client:   openai.NewClientWithConfig(cfg),
		model:    model,
		classify: newClassifier("openrouter", "SHHH_API_KEY or OPENROUTER_API_KEY", key),
	}, nil
}

// openRouterTransport is what the gateway needs said that the OpenAI client
// has no field for: the two attribution headers, and the cache breakpoints on
// a request bound for an Anthropic model. Both are properties of the wire and
// not of the conversation, and the encoded body is the last place either can
// still be reached.
type openRouterTransport struct {
	base http.RoundTripper
	// headTTL is how long the marked head is cached for; the zero value is
	// the default, so a client assembled elsewhere still marks (cache.go).
	headTTL CacheTTL
}

func (t *openRouterTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("HTTP-Referer", "https://github.com/rfizzle/shhh")
	req.Header.Set("X-Title", "shhh")
	if err := t.markCache(req); err != nil {
		return nil, err
	}
	return t.base.RoundTrip(req)
}

// markCache annotates the outgoing body with cache breakpoints where the
// model it names is one the gateway forwards to the Messages API. That API
// caches only what a request asked it to, and a request assembled in the
// OpenAI shape has asked for nothing — which is why a session here paid full
// price for its whole opening on every round of every turn.
//
// A body it cannot read, or one for a model routed anywhere else, goes out
// exactly as it arrived. The bytes are restored from what was read, so a
// dialect that would not have understood the field is not sent a request that
// differs from today's in any way at all.
func (t *openRouterTransport) markCache(req *http.Request) error {
	if req.Body == nil {
		return nil
	}
	body, err := io.ReadAll(req.Body)
	req.Body.Close()
	if err != nil {
		return err
	}
	body = markOpenAICacheBody(body, t.headTTL)
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	return nil
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
