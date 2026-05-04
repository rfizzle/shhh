package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"

	openai "github.com/sashabaranov/go-openai"
)

const (
	defaultOpenAIModel = "gpt-4o"
)

type OpenAI struct {
	client *openai.Client
	model  string
}

func NewOpenAI() (*OpenAI, error) {
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY is not set")
	}
	return &OpenAI{
		client: openai.NewClient(key),
		model:  defaultOpenAIModel,
	}, nil
}

func NewOpenAIWithConfig(client *openai.Client, model string) *OpenAI {
	if model == "" {
		model = defaultOpenAIModel
	}
	return &OpenAI{client: client, model: model}
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

func toOpenAIMessages(msgs []Message) []openai.ChatCompletionMessage {
	out := make([]openai.ChatCompletionMessage, len(msgs))
	for i, m := range msgs {
		out[i] = openai.ChatCompletionMessage{
			Role:    string(m.Role),
			Content: m.Content,
		}
	}
	return out
}

var (
	ErrUnauthorized = errors.New("invalid API key — check OPENAI_API_KEY")
	ErrRateLimited  = errors.New("rate limited — try again shortly")
)

func classifyError(err error) error {
	var apiErr *openai.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.HTTPStatusCode {
		case http.StatusUnauthorized:
			return fmt.Errorf("%w: %s", ErrUnauthorized, apiErr.Message)
		case http.StatusTooManyRequests:
			return fmt.Errorf("%w: %s", ErrRateLimited, apiErr.Message)
		}
	}
	var reqErr *openai.RequestError
	if errors.As(err, &reqErr) {
		switch reqErr.HTTPStatusCode {
		case http.StatusUnauthorized:
			return fmt.Errorf("%w", ErrUnauthorized)
		case http.StatusTooManyRequests:
			return fmt.Errorf("%w", ErrRateLimited)
		}
	}
	return err
}

func init() {
	Register("openai", func() (Provider, error) {
		return NewOpenAI()
	})
}
