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

// anthropicAnswerFloor is the output the thinking budget may not eat: the
// budget shares max_tokens with the reply, and a session that has capped its
// output should still get an answer under whatever cap it chose (S-139).
const anthropicAnswerFloor = 4096

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

	// Extended thinking (S-139). The budget has to leave the reply room to
	// exist, so it is clamped to the output ceiling less a floor for the
	// answer itself; a ceiling too small for the API's minimum budget asks
	// for no thinking rather than sending a request the API will refuse.
	if budget := opts.Effort.ThinkingBudget(maxTokens - anthropicAnswerFloor); budget > 0 {
		params.Thinking = anthropic.ThinkingConfigParamOfEnabled(int64(budget))
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
				ch <- StreamEvent{
					ToolCalls: CompletedToolCalls(anthropicToolCalls(accumulated)),
					Reasoning: anthropicReasoning(accumulated),
					Err:       a.classify(err),
					Done:      true,
				}
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
			ch <- StreamEvent{
				ToolCalls: CompletedToolCalls(anthropicToolCalls(accumulated)),
				Reasoning: anthropicReasoning(accumulated),
				Err:       a.classify(err),
				Done:      true,
			}
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

		ch <- StreamEvent{
			ToolCalls: anthropicToolCalls(accumulated),
			Reasoning: anthropicReasoning(accumulated),
			Usage:     usage,
			Done:      true,
		}
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

// anthropicReasoning reads the thinking blocks out of the accumulated
// message so they can be replayed on the next request (S-139). Like
// anthropicToolCalls it runs over a partial accumulation too: a stream that
// broke after the model finished thinking kept the thinking.
func anthropicReasoning(accumulated anthropic.Message) []ReasoningBlock {
	var blocks []ReasoningBlock
	for _, block := range accumulated.Content {
		switch b := block.AsAny().(type) {
		case anthropic.ThinkingBlock:
			// A block whose signature has not arrived is not one the API
			// will take back, and sending it unsigned fails the request it
			// was kept for.
			if b.Signature == "" {
				continue
			}
			blocks = append(blocks, ReasoningBlock{Text: b.Thinking, Signature: b.Signature})
		case anthropic.RedactedThinkingBlock:
			blocks = append(blocks, ReasoningBlock{Redacted: b.Data})
		}
	}
	return blocks
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
			// Attachments lead the message: the Messages API reads an image
			// better when the sentence about it comes after (S-134).
			blocks := anthropicAttachmentBlocks(msg.Attachments)
			if msg.Content != "" || len(blocks) == 0 {
				blocks = append(blocks, anthropic.NewTextBlock(msg.Content))
			}
			out = append(out, anthropic.NewUserMessage(blocks...))
		case RoleAssistant:
			flushToolResults()
			var blocks []anthropic.ContentBlockParamUnion
			// Thinking leads the turn, in the order and the form it arrived:
			// with extended thinking on, the API rejects an assistant turn
			// that requested tools and dropped the reasoning behind them
			// (S-139).
			for _, r := range msg.Reasoning {
				if r.Redacted != "" {
					blocks = append(blocks, anthropic.NewRedactedThinkingBlock(r.Redacted))
					continue
				}
				if r.Signature != "" {
					blocks = append(blocks, anthropic.NewThinkingBlock(r.Signature, r.Text))
				}
			}
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

// anthropicAttachmentBlocks carries a user message's attachments as native
// blocks. Images and PDFs the API takes inline; everything else falls back to
// the shared text form, which is also what a text attachment always is.
func anthropicAttachmentBlocks(atts []Attachment) []anthropic.ContentBlockParamUnion {
	var blocks []anthropic.ContentBlockParamUnion
	for _, a := range atts {
		switch {
		case a.Kind == AttachmentImage:
			blocks = append(blocks, anthropic.NewImageBlockBase64(a.MediaType, a.Base64()))
		case a.Kind == AttachmentDocument && a.MediaType == "application/pdf":
			blocks = append(blocks, anthropic.NewDocumentBlock(anthropic.Base64PDFSourceParam{
				Data: a.Base64(),
			}))
		default:
			blocks = append(blocks, anthropic.NewTextBlock(a.AsText()))
		}
	}
	return blocks
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
