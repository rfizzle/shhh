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
	defaultOpenAIModel = "gpt-4o"
)

type OpenAI struct {
	client   *openai.Client
	model    string
	classify func(error) error
}

const defaultOpenAIBaseURL = "https://api.openai.com/v1"

func NewOpenAI(opts ResolveOpts) (*OpenAI, error) {
	key := first(opts.APIKey, os.Getenv("SHHH_API_KEY"), os.Getenv("OPENAI_API_KEY"), opts.ConfigAPIKey)
	if key == "" {
		return nil, fmt.Errorf("SHHH_API_KEY or OPENAI_API_KEY is not set")
	}
	model := first(opts.Model, defaultOpenAIModel)
	baseURL := first(opts.BaseURL, os.Getenv("SHHH_BASE_URL"), opts.ConfigBaseURL, defaultOpenAIBaseURL)

	cfg := openai.DefaultConfig(key)
	cfg.BaseURL = baseURL

	return &OpenAI{
		client:   openai.NewClientWithConfig(cfg),
		model:    model,
		classify: newClassifyError("SHHH_API_KEY or OPENAI_API_KEY"),
	}, nil
}

func NewOpenAIWithConfig(client *openai.Client, model string) *OpenAI {
	if model == "" {
		model = defaultOpenAIModel
	}
	return &OpenAI{client: client, model: model, classify: newClassifyError("OPENAI_API_KEY")}
}

func (o *OpenAI) Name() string { return "openai" }

func (o *OpenAI) StreamCompletion(ctx context.Context, messages []Message, opts CompletionOpts) (<-chan StreamEvent, error) {
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
		return nil, o.classify(err)
	}

	return streamOpenAIToolCalls(stream, o.classify), nil
}

func toOpenAITools(tools []Tool) []openai.Tool {
	out := make([]openai.Tool, len(tools))
	for i, t := range tools {
		out[i] = openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		}
	}
	return out
}

func toOpenAIMessages(msgs []Message) []openai.ChatCompletionMessage {
	out := make([]openai.ChatCompletionMessage, len(msgs))
	for i, m := range msgs {
		msg := openai.ChatCompletionMessage{
			Role:    string(m.Role),
			Content: m.Content,
		}
		if len(m.ToolCalls) > 0 {
			for _, tc := range m.ToolCalls {
				msg.ToolCalls = append(msg.ToolCalls, openai.ToolCall{
					ID:   tc.ID,
					Type: openai.ToolTypeFunction,
					Function: openai.FunctionCall{
						Name:      tc.Name,
						Arguments: tc.Arguments,
					},
				})
			}
		}
		if m.ToolCallID != "" {
			msg.ToolCallID = m.ToolCallID
		}
		out[i] = msg
	}
	return out
}

var (
	ErrUnauthorized = errors.New("invalid API key")
	ErrRateLimited  = errors.New("rate limited — try again shortly")
)

func newClassifyError(keyHint string) func(error) error {
	return func(err error) error {
		var apiErr *openai.APIError
		if errors.As(err, &apiErr) {
			switch apiErr.HTTPStatusCode {
			case http.StatusUnauthorized:
				return fmt.Errorf("%w — check %s: %s", ErrUnauthorized, keyHint, apiErr.Message)
			case http.StatusTooManyRequests:
				return fmt.Errorf("%w: %s", ErrRateLimited, apiErr.Message)
			}
		}
		var reqErr *openai.RequestError
		if errors.As(err, &reqErr) {
			switch reqErr.HTTPStatusCode {
			case http.StatusUnauthorized:
				return fmt.Errorf("%w — check %s", ErrUnauthorized, keyHint)
			case http.StatusTooManyRequests:
				return fmt.Errorf("%w", ErrRateLimited)
			}
		}
		return err
	}
}

func init() {
	Register("openai", func(opts ResolveOpts) (Provider, error) {
		return NewOpenAI(opts)
	})
	RegisterDefaults("openai", ProviderDefaults{
		Model:   defaultOpenAIModel,
		BaseURL: defaultOpenAIBaseURL,
	})
}
