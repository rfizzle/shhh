package provider

import (
	"context"
	"fmt"
	"os"

	openai "github.com/sashabaranov/go-openai"
)

const (
	defaultOpenAIModel = "gpt-4o"
	// cheapOpenAIModel is the small model the bounded calls run on: the
	// nano rung of the current generation, the smallest thing the catalog
	// offers that still reasons and still calls a tool.
	cheapOpenAIModel = "gpt-5.4-nano"
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
		classify: newClassifier("openai", "SHHH_API_KEY or OPENAI_API_KEY", key),
	}, nil
}

func NewOpenAIWithConfig(client *openai.Client, model string) *OpenAI {
	if model == "" {
		model = defaultOpenAIModel
	}
	return &OpenAI{client: client, model: model, classify: newClassifier("openai", "OPENAI_API_KEY", "")}
}

func (o *OpenAI) Name() string { return "openai" }

// ListModels enumerates the account's models from GET /v1/models.
func (o *OpenAI) ListModels(ctx context.Context) ([]string, error) {
	names, err := listOpenAIModels(ctx, o.client)
	if err != nil {
		return nil, o.classify(err)
	}
	return names, nil
}

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
		// Chat completions carry a mixed message as a parts array, and the
		// two content fields are mutually exclusive — setting both is an SDK
		// error, so Content is cleared once the parts exist.
		if parts := openAIAttachmentParts(m); len(parts) > 0 {
			msg.Content, msg.MultiContent = "", parts
		}
		out[i] = msg
	}
	return out
}

// openAIAttachmentParts renders a message's attachments as chat-completion
// content parts, with the sentence last. It returns nil when there is nothing
// to attach, which is what keeps ordinary messages on the plain string form.
// Chat completions can take an image but not a document, so a PDF degrades to
// the shared text note rather than disappearing.
func openAIAttachmentParts(m Message) []openai.ChatMessagePart {
	if len(m.Attachments) == 0 {
		return nil
	}
	var parts []openai.ChatMessagePart
	for _, a := range m.Attachments {
		if a.Kind == AttachmentImage {
			parts = append(parts, openai.ChatMessagePart{
				Type:     openai.ChatMessagePartTypeImageURL,
				ImageURL: &openai.ChatMessageImageURL{URL: a.DataURL()},
			})
			continue
		}
		parts = append(parts, openai.ChatMessagePart{
			Type: openai.ChatMessagePartTypeText,
			Text: a.AsText(),
		})
	}
	if m.Content != "" {
		parts = append(parts, openai.ChatMessagePart{
			Type: openai.ChatMessagePartTypeText,
			Text: m.Content,
		})
	}
	return parts
}

func init() {
	Register("openai", func(opts ResolveOpts) (Provider, error) {
		return NewOpenAI(opts)
	})
	RegisterDefaults("openai", ProviderDefaults{
		Model:      defaultOpenAIModel,
		BaseURL:    defaultOpenAIBaseURL,
		CheapModel: cheapOpenAIModel,
	})
}
