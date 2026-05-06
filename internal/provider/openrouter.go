package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"

	openai "github.com/sashabaranov/go-openai"
)

const (
	defaultOpenRouterBaseURL = "https://openrouter.ai/api/v1"
	defaultOpenRouterModel   = "anthropic/claude-sonnet-4-6"
)

type OpenRouter struct {
	client *openai.Client
	model  string
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
		Transport: &openRouterTransport{base: http.DefaultTransport},
	}

	return &OpenRouter{
		client: openai.NewClientWithConfig(cfg),
		model:  model,
	}, nil
}

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
	return &OpenRouter{client: client, model: model}
}

func (o *OpenRouter) Name() string { return "openrouter" }

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
		req.MaxTokens = opts.MaxTokens
	}
	if len(opts.Tools) > 0 {
		req.Tools = toOpenAITools(opts.Tools)
		if opts.ToolChoice != "" {
			req.ToolChoice = opts.ToolChoice
		}
	}

	stream, err := o.client.CreateChatCompletionStream(ctx, req)
	if err != nil {
		return nil, classifyOpenRouterError(err)
	}

	return streamOpenAIToolCalls(stream, classifyOpenRouterError), nil
}

var (
	ErrOpenRouterUnauthorized = fmt.Errorf("invalid API key — check SHHH_API_KEY or OPENROUTER_API_KEY")
	ErrOpenRouterRateLimited  = fmt.Errorf("rate limited — try again shortly")
)

func classifyOpenRouterError(err error) error {
	var apiErr *openai.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.HTTPStatusCode {
		case http.StatusUnauthorized:
			return fmt.Errorf("%w: %s", ErrOpenRouterUnauthorized, apiErr.Message)
		case http.StatusTooManyRequests:
			return fmt.Errorf("%w: %s", ErrOpenRouterRateLimited, apiErr.Message)
		}
	}
	var reqErr *openai.RequestError
	if errors.As(err, &reqErr) {
		switch reqErr.HTTPStatusCode {
		case http.StatusUnauthorized:
			return fmt.Errorf("%w", ErrOpenRouterUnauthorized)
		case http.StatusTooManyRequests:
			return fmt.Errorf("%w", ErrOpenRouterRateLimited)
		}
	}
	return err
}

func init() {
	Register("openrouter", func(opts ResolveOpts) (Provider, error) {
		return NewOpenRouter(opts)
	})
	RegisterDefaults("openrouter", ProviderDefaults{
		Model:   defaultOpenRouterModel,
		BaseURL: defaultOpenRouterBaseURL,
	})
}
