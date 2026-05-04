package provider

import (
	"context"
	"os"

	openai "github.com/sashabaranov/go-openai"
)

const (
	defaultCompatBaseURL = "http://localhost:11434/v1"
	defaultCompatModel   = "llama3"
)

type OpenAICompat struct {
	client   *openai.Client
	model    string
	baseURL  string
	name     string
	classify func(error) error
}

func NewOpenAICompat(opts ResolveOpts) (*OpenAICompat, error) {
	baseURL := first(opts.BaseURL, os.Getenv("SHHH_BASE_URL"), opts.ConfigBaseURL, defaultCompatBaseURL)
	model := first(opts.Model, defaultCompatModel)
	key := first(opts.APIKey, os.Getenv("SHHH_API_KEY"), opts.ConfigAPIKey)

	name := first(opts.Name, opts.ConfigName, "openai-compatible")

	cfg := openai.DefaultConfig(key)
	cfg.BaseURL = baseURL
	return &OpenAICompat{
		client:   openai.NewClientWithConfig(cfg),
		model:    model,
		baseURL:  baseURL,
		name:     name,
		classify: newClassifyError("SHHH_API_KEY"),
	}, nil
}

func first(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func NewOpenAICompatWith(client *openai.Client, model, baseURL string) *OpenAICompat {
	if model == "" {
		model = defaultCompatModel
	}
	return &OpenAICompat{client: client, model: model, baseURL: baseURL, name: "openai-compatible", classify: newClassifyError("SHHH_API_KEY")}
}

func (o *OpenAICompat) Name() string { return o.name }

func (o *OpenAICompat) StreamCompletion(ctx context.Context, messages []Message, opts CompletionOpts) (<-chan StreamEvent, error) {
	model := o.model
	if opts.Model != "" {
		model = opts.Model
	}

	req := openai.ChatCompletionRequest{
		Model:    model,
		Messages: toOpenAIMessages(messages),
		Stream:   true,
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
		return nil, o.classify(err)
	}

	return streamOpenAIToolCalls(stream, o.classify), nil
}

func init() {
	Register("openai-compatible", func(opts ResolveOpts) (Provider, error) {
		return NewOpenAICompat(opts)
	})
}
