package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

const defaultAnthropicModel = "claude-opus-5"

// Streaming requests can afford a generous output ceiling — billing is by
// actual usage and streaming avoids HTTP timeouts.
const defaultAnthropicMaxTokens = 64000

type Anthropic struct {
	client   anthropic.Client
	model    string
	classify func(error) error
}

func NewAnthropic(opts ResolveOpts) (*Anthropic, error) {
	key := first(opts.APIKey, os.Getenv("SHHH_API_KEY"), os.Getenv("ANTHROPIC_API_KEY"), opts.ConfigAPIKey)
	if key == "" {
		return nil, fmt.Errorf("SHHH_API_KEY or ANTHROPIC_API_KEY is not set")
	}

	clientOpts := []option.RequestOption{option.WithAPIKey(key)}
	if baseURL := first(opts.BaseURL, opts.ConfigBaseURL); baseURL != "" {
		clientOpts = append(clientOpts, option.WithBaseURL(baseURL))
	}

	return &Anthropic{
		client:   anthropic.NewClient(clientOpts...),
		model:    first(opts.Model, defaultAnthropicModel),
		classify: newClassifier("anthropic", "SHHH_API_KEY or ANTHROPIC_API_KEY", key),
	}, nil
}

// NewAnthropicWith builds a provider over an already-configured client, for
// gateway profiles (internal/profile) that supply their own base URL, auth,
// and HTTP transport.
func NewAnthropicWith(client anthropic.Client, model string) *Anthropic {
	return NewAnthropicNamed(client, model, "anthropic")
}

// NewAnthropicNamed is NewAnthropicWith under a caller-chosen name, so a
// gateway profile speaking the Messages API classifies its failures as
// itself rather than as Anthropic (S-106).
func NewAnthropicNamed(client anthropic.Client, model, name string) *Anthropic {
	name = first(name, "anthropic")
	return &Anthropic{
		client:   client,
		model:    first(model, defaultAnthropicModel),
		classify: newClassifier(name, "SHHH_API_KEY or ANTHROPIC_API_KEY", ""),
	}
}

func (a *Anthropic) Name() string { return "anthropic" }

func (a *Anthropic) StreamCompletion(ctx context.Context, messages []Message, opts CompletionOpts) (<-chan StreamEvent, error) {
	model := a.model
	if opts.Model != "" {
		model = opts.Model
	}

	maxTokens := opts.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultAnthropicMaxTokens
	}

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(model),
		MaxTokens: int64(maxTokens),
	}

	system, converted := toAnthropicMessages(messages)
	if system != "" {
		params.System = []anthropic.TextBlockParam{{Text: system}}
	}
	params.Messages = converted

	if len(opts.Tools) > 0 {
		params.Tools = toAnthropicTools(opts.Tools)
	}

	ch := make(chan StreamEvent)
	go func() {
		defer close(ch)

		stream := a.client.Messages.NewStreaming(ctx, params)
		accumulated := anthropic.Message{}
		for stream.Next() {
			event := stream.Current()
			if err := accumulated.Accumulate(event); err != nil {
				ch <- StreamEvent{ToolCalls: CompletedToolCalls(anthropicToolCalls(accumulated)), Err: a.classify(err), Done: true}
				return
			}
			if delta, ok := event.AsAny().(anthropic.ContentBlockDeltaEvent); ok {
				if text, ok := delta.Delta.AsAny().(anthropic.TextDelta); ok && text.Text != "" {
					ch <- StreamEvent{Token: text.Text}
				}
			}
		}
		if err := stream.Err(); err != nil {
			// The blocks the model had finished travel with the failure, so a
			// dropped stream can be continued rather than only re-asked
			// (S-107).
			ch <- StreamEvent{ToolCalls: CompletedToolCalls(anthropicToolCalls(accumulated)), Err: a.classify(err), Done: true}
			return
		}

		if accumulated.StopReason == anthropic.StopReasonRefusal {
			ch <- StreamEvent{Err: a.classify(errors.New("request was declined by the model's safety system")), Done: true}
			return
		}

		usage := &Usage{
			PromptTokens:     int(accumulated.Usage.InputTokens),
			CompletionTokens: int(accumulated.Usage.OutputTokens),
			CachedTokens:     int(accumulated.Usage.CacheReadInputTokens),
		}

		ch <- StreamEvent{ToolCalls: anthropicToolCalls(accumulated), Usage: usage, Done: true}
	}()

	return ch, nil
}

// anthropicToolCalls reads the tool-use blocks out of the accumulated
// message. It is called on a partially accumulated one too, when a stream
// breaks mid-reply (S-107) — the SDK only materialises a block once its own
// deltas have been folded in, so what is there is what the model finished.
func anthropicToolCalls(accumulated anthropic.Message) []ToolCall {
	var calls []ToolCall
	for _, block := range accumulated.Content {
		if tu, ok := block.AsAny().(anthropic.ToolUseBlock); ok {
			calls = append(calls, ToolCall{
				ID:        tu.ID,
				Name:      tu.Name,
				Arguments: tu.JSON.Input.Raw(),
			})
		}
	}
	return calls
}

// toAnthropicMessages converts the neutral message history to Anthropic's
// shape: system content moves to the top-level system prompt, and consecutive
// tool results merge into a single user turn (required for parallel tool use).
func toAnthropicMessages(messages []Message) (system string, out []anthropic.MessageParam) {
	var systemParts []string
	var pendingToolResults []anthropic.ContentBlockParamUnion

	flushToolResults := func() {
		if len(pendingToolResults) > 0 {
			out = append(out, anthropic.NewUserMessage(pendingToolResults...))
			pendingToolResults = nil
		}
	}

	for _, msg := range messages {
		switch msg.Role {
		case RoleSystem:
			systemParts = append(systemParts, msg.Content)
		case RoleUser:
			flushToolResults()
			out = append(out, anthropic.NewUserMessage(anthropic.NewTextBlock(msg.Content)))
		case RoleAssistant:
			flushToolResults()
			var blocks []anthropic.ContentBlockParamUnion
			if msg.Content != "" {
				blocks = append(blocks, anthropic.NewTextBlock(msg.Content))
			}
			for _, tc := range msg.ToolCalls {
				var input any
				if err := json.Unmarshal([]byte(tc.Arguments), &input); err != nil {
					input = map[string]any{}
				}
				blocks = append(blocks, anthropic.ContentBlockParamUnion{
					OfToolUse: &anthropic.ToolUseBlockParam{
						ID:    tc.ID,
						Name:  tc.Name,
						Input: input,
					},
				})
			}
			if len(blocks) > 0 {
				out = append(out, anthropic.NewAssistantMessage(blocks...))
			}
		case RoleTool:
			isError := strings.HasPrefix(msg.Content, "error:")
			pendingToolResults = append(pendingToolResults,
				anthropic.NewToolResultBlock(msg.ToolCallID, msg.Content, isError))
		}
	}
	flushToolResults()

	return strings.Join(systemParts, "\n\n"), out
}

func toAnthropicTools(tools []Tool) []anthropic.ToolUnionParam {
	out := make([]anthropic.ToolUnionParam, 0, len(tools))
	for _, t := range tools {
		var schema struct {
			Properties map[string]any `json:"properties"`
			Required   []string       `json:"required"`
		}
		_ = json.Unmarshal(t.Parameters, &schema)
		out = append(out, anthropic.ToolUnionParam{
			OfTool: &anthropic.ToolParam{
				Name:        t.Name,
				Description: anthropic.String(t.Description),
				InputSchema: anthropic.ToolInputSchemaParam{
					Properties: schema.Properties,
					Required:   schema.Required,
				},
			},
		})
	}
	return out
}

func init() {
	Register("anthropic", func(opts ResolveOpts) (Provider, error) {
		return NewAnthropic(opts)
	})
	RegisterDefaults("anthropic", ProviderDefaults{
		Model: defaultAnthropicModel,
	})
}
