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

// cheapAnthropicModel is the small model the session's bounded calls run
// on. Haiku is the family's judgement-sized model: it reads the same
// evidence the frontier model would and costs a fraction of it.
const cheapAnthropicModel = "claude-haiku-4-5-20251001"

// Streaming requests can afford a generous output ceiling — billing is by
// actual usage and streaming avoids HTTP timeouts.
const defaultAnthropicMaxTokens = 64000

// anthropicAnswerFloor is the output the thinking budget may not eat: the
// budget shares max_tokens with the reply, and a session that has capped its
// output should still get an answer under whatever cap it chose.
const anthropicAnswerFloor = 4096

type Anthropic struct {
	client anthropic.Client
	model  string
	// cacheTTL is how long the request's fixed head is cached for (cache.go).
	cacheTTL CacheTTL
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
		cacheTTL: cacheTTLOrDefault(opts.CacheTTL),
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
// itself rather than as Anthropic.
func NewAnthropicNamed(client anthropic.Client, model, name string) *Anthropic {
	name = first(name, "anthropic")
	return &Anthropic{
		client:   client,
		model:    first(model, defaultAnthropicModel),
		cacheTTL: DefaultCacheTTL,
		classify: newClassifier(name, "SHHH_API_KEY or ANTHROPIC_API_KEY", ""),
	}
}

func (a *Anthropic) Name() string { return "anthropic" }

// applyAnthropicThinking puts the session's level on the request in the
// shape this model takes. The current generation takes a named effort under
// adaptive thinking; the older one takes a token budget, which has to leave
// the reply room to exist, so it is clamped to the output ceiling less a
// floor for the answer itself, and a ceiling too small for the API's minimum
// budget asks for no thinking rather than sending a request the API will
// refuse. Off sends nothing either way — on a model that always thinks that
// is its own default, which is the most "off" it has.
func applyAnthropicThinking(params *anthropic.MessageNewParams, effort Effort, model string, maxTokens int) {
	caps := CapabilitiesFor(model)
	effort = effort.Fit(caps)
	if !effort.On() {
		return
	}
	if caps.Adaptive || !caps.Known {
		params.Thinking = anthropic.ThinkingConfigParamUnion{OfAdaptive: &anthropic.ThinkingConfigAdaptiveParam{}}
		params.OutputConfig = anthropic.OutputConfigParam{Effort: anthropic.OutputConfigEffort(effort.String())}
		return
	}
	// A ceiling with no room left under the answer floor asks for no
	// thinking at all. The subtraction has to be guarded rather than handed
	// to the budget: a ceiling of zero means "no ceiling" there, which is
	// what Gemini passes, so a negative or exactly-zero remainder would sail
	// through as an unbounded budget and go out larger than max_tokens — a
	// request the Messages API refuses outright.
	room := maxTokens - anthropicAnswerFloor
	if room <= 0 {
		return
	}
	if budget := effort.ThinkingBudget(room); budget > 0 {
		params.Thinking = anthropic.ThinkingConfigParamOfEnabled(int64(budget))
	}
}

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

	applyAnthropicThinking(&params, opts.Effort, model, maxTokens)

	system, converted := toAnthropicMessages(messages)
	if system != "" {
		params.System = []anthropic.TextBlockParam{{Text: system}}
	}
	params.Messages = converted

	if len(opts.Tools) > 0 {
		params.Tools = toAnthropicTools(opts.Tools)
		if choice, ok := toAnthropicToolChoice(opts.ToolChoice); ok {
			params.ToolChoice = choice
		}
	}

	// Last, so the markers land on the request as it will actually be sent:
	// the head is only stable once the tools and the system prompt are both
	// on it (cache.go).
	markAnthropicCache(&params, a.cacheTTL)

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
				switch d := delta.Delta.AsAny().(type) {
				case anthropic.TextDelta:
					if d.Text != "" {
						ch <- StreamEvent{Token: d.Text}
					}
				case anthropic.InputJSONDelta:
					// A tool call's arguments as the model writes them. The
					// id is read off the accumulation rather than the delta:
					// this dialect names a block once, when it starts, and
					// every fragment after that carries only the JSON.
					if id := anthropicBlockID(accumulated, delta.Index); id != "" && d.PartialJSON != "" {
						ch <- StreamEvent{ToolCallDelta: &ToolCallDelta{ID: id, Arguments: d.PartialJSON}}
					}
				case anthropic.ThinkingDelta:
					// The thinking as it is written. The signed block the next
					// request needs is read off the accumulation below — this
					// is the same text a frame early, so the screen can show
					// the model thinking rather than a spinner.
					if d.Thinking != "" {
						ch <- StreamEvent{Thinking: d.Thinking}
					}
				}
			}
		}
		if err := stream.Err(); err != nil {
			// The blocks the model had finished travel with the failure, so a
			// dropped stream can be continued rather than only re-asked.
			ch <- StreamEvent{
				ToolCalls: CompletedToolCalls(anthropicToolCalls(accumulated)),
				Reasoning: anthropicReasoning(accumulated),
				Err:       a.classify(err),
				Done:      true,
			}
			return
		}

		if accumulated.StopReason == anthropic.StopReasonRefusal {
			ch <- StreamEvent{Err: a.classify(errors.New(anthropicRefusal(accumulated.StopDetails))), Done: true}
			return
		}

		// InputTokens here counts only what was read fresh — this dialect
		// reports the cached parts beside it rather than inside it. Usage
		// promises a prompt count that already contains them, so the sum is
		// made here and not left to every reader of the ledger.
		cached := int(accumulated.Usage.CacheReadInputTokens)
		created := int(accumulated.Usage.CacheCreationInputTokens)
		usage := &Usage{
			PromptTokens:        int(accumulated.Usage.InputTokens) + cached + created,
			CompletionTokens:    int(accumulated.Usage.OutputTokens),
			CachedTokens:        cached,
			CacheCreationTokens: created,
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
// breaks mid-reply — the SDK only materialises a block once its own
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

// anthropicBlockID is the tool-use id of the accumulated content block at
// index i, and empty where that index holds something else or has not been
// accumulated yet. An argument fragment with no id to address it is dropped
// rather than guessed at: it would count toward a call nobody made.
func anthropicBlockID(accumulated anthropic.Message, i int64) string {
	if i < 0 || i >= int64(len(accumulated.Content)) {
		return ""
	}
	tu, ok := accumulated.Content[i].AsAny().(anthropic.ToolUseBlock)
	if !ok {
		return ""
	}
	return tu.ID
}

// anthropicReasoning reads the thinking blocks out of the accumulated
// message so they can be replayed on the next request. Like
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
// tool results merge into a single user turn (required for parallel tool
// use).
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
			// better when the sentence about it comes after.
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
			// that requested tools and dropped the reasoning behind them.
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
		out = append(out, anthropic.ToolUnionParam{
			OfTool: &anthropic.ToolParam{
				Name:        t.Name,
				Description: anthropic.String(t.Description),
				InputSchema: anthropicInputSchema(t.Parameters),
			},
		})
	}
	return out
}

// anthropicInputSchema puts a tool's schema on the request as it was
// written.
//
// Rebuilding it from properties and required — the two keys the SDK's struct
// has fields for — drops everything else a schema can carry: the $defs a
// nested shape is factored into and the $ref that reaches them, enums,
// additionalProperties, a description on the schema itself. The model is
// then described a tool whose arguments are looser than the ones that will
// be validated, and the failure is arguments that do not fit, from a model
// that was told they would.
//
// Every key travels as an extra field, which the SDK writes over its own
// rendering of the struct — so a schema naming its type keeps it, and one
// that does not gets the "object" the struct spells by default.
func anthropicInputSchema(raw json.RawMessage) anthropic.ToolInputSchemaParam {
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		return anthropic.ToolInputSchemaParam{}
	}
	return anthropic.ToolInputSchemaParam{ExtraFields: fields}
}

// toAnthropicToolChoice renders what the request says about calling a tool,
// and reports whether there is anything to send. Anything else — the empty
// string most of all — sends no field, which is this dialect's own auto.
//
// Sending none rather than dropping the tools is what keeps a request that
// wants prose from costing the cached prefix: the tools sit in front of the
// system prompt in what the API hashes, so a request without them shares no
// prefix with the session's other requests and rebuilds the whole head. The
// choice itself is not part of that prefix (cache.go).
// See docs/capabilities/providers.md#the-prompt-prefix-is-paid-for-once.
func toAnthropicToolChoice(choice string) (anthropic.ToolChoiceUnionParam, bool) {
	switch choice {
	case ToolChoiceAuto:
		return anthropic.ToolChoiceUnionParam{OfAuto: &anthropic.ToolChoiceAutoParam{}}, true
	case ToolChoiceNone:
		return anthropic.ToolChoiceUnionParam{OfNone: &anthropic.ToolChoiceNoneParam{}}, true
	}
	return anthropic.ToolChoiceUnionParam{}, false
}

// anthropicRefusal is what a declined request says. The dialect carries a
// policy category and a human-readable explanation beside the stop reason,
// and a fixed sentence in their place tells the reader only that something
// was refused — which is the one thing the failure already told them. Either
// part can be absent, and on a refusal from an older model both are.
func anthropicRefusal(details anthropic.RefusalStopDetails) string {
	msg := "request was declined by the model's safety system"
	if details.Category != "" {
		msg += " (" + string(details.Category) + ")"
	}
	if details.Explanation != "" {
		msg += ": " + details.Explanation
	}
	return msg
}

func init() {
	Register("anthropic", func(opts ResolveOpts) (Provider, error) {
		return NewAnthropic(opts)
	})
	RegisterDefaults("anthropic", ProviderDefaults{
		Model:      defaultAnthropicModel,
		CheapModel: cheapAnthropicModel,
	})
}
