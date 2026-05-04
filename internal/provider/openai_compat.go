package provider

import (
	"context"
	"errors"
	"io"
	"os"

	openai "github.com/sashabaranov/go-openai"
)

const (
	defaultCompatBaseURL = "http://localhost:11434/v1"
	defaultCompatModel   = "llama3"
)

type OpenAICompat struct {
	client  *openai.Client
	model   string
	baseURL string
}

func NewOpenAICompat(opts ResolveOpts) (*OpenAICompat, error) {
	baseURL := first(opts.BaseURL, os.Getenv("SHHH_COMPAT_BASE_URL"), defaultCompatBaseURL)
	model := first(opts.Model, os.Getenv("SHHH_COMPAT_MODEL"), defaultCompatModel)
	key := first(opts.APIKey, os.Getenv("SHHH_COMPAT_API_KEY"))

	cfg := openai.DefaultConfig(key)
	cfg.BaseURL = baseURL
	return &OpenAICompat{
		client:  openai.NewClientWithConfig(cfg),
		model:   model,
		baseURL: baseURL,
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
	return &OpenAICompat{client: client, model: model, baseURL: baseURL}
}

func (o *OpenAICompat) Name() string { return "openai-compatible" }

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

	stream, err := o.client.CreateChatCompletionStream(ctx, req)
	if err != nil {
		return nil, classifyError(err)
	}

	ch := make(chan StreamEvent)
	go func() {
		defer close(ch)
		defer stream.Close()
		for {
			resp, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				ch <- StreamEvent{Done: true}
				return
			}
			if err != nil {
				ch <- StreamEvent{Err: classifyError(err), Done: true}
				return
			}
			if len(resp.Choices) > 0 {
				delta := resp.Choices[0].Delta.Content
				if delta != "" {
					ch <- StreamEvent{Token: delta}
				}
			}
		}
	}()

	return ch, nil
}

func init() {
	Register("openai-compatible", func(opts ResolveOpts) (Provider, error) {
		return NewOpenAICompat(opts)
	})
}
