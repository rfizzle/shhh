package provider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"sync/atomic"

	"google.golang.org/genai"
)

const defaultGeminiModel = "gemini-2.5-flash"

// cheapGeminiModel is the small model the bounded calls run on: the
// current flash, which is the tier below every pro model a session is
// likely to be on.
const cheapGeminiModel = "gemini-3.7-flash"

type Gemini struct {
	client   *genai.Client
	model    string
	classify func(error) error
}

func NewGemini(opts ResolveOpts) (*Gemini, error) {
	key := first(opts.APIKey, os.Getenv("SHHH_API_KEY"), os.Getenv("GEMINI_API_KEY"), opts.ConfigAPIKey)
	if key == "" {
		return nil, fmt.Errorf("SHHH_API_KEY or GEMINI_API_KEY is not set")
	}

	model := first(opts.Model, defaultGeminiModel)

	client, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		APIKey:  key,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create Gemini client: %w", err)
	}

	return &Gemini{
		client:   client,
		model:    model,
		classify: newClassifier("gemini", "SHHH_API_KEY or GEMINI_API_KEY", key),
	}, nil
}

func (g *Gemini) Name() string { return "gemini" }

func (g *Gemini) StreamCompletion(ctx context.Context, messages []Message, opts CompletionOpts) (<-chan StreamEvent, error) {
	model := g.model
	if opts.Model != "" {
		model = opts.Model
	}

	contents, systemInstruction := toGeminiContents(messages)

	config := &genai.GenerateContentConfig{
		SystemInstruction: systemInstruction,
	}
	if opts.Temperature != nil {
		t := float32(*opts.Temperature)
		config.Temperature = &t
	}
	if opts.MaxTokens > 0 {
		config.MaxOutputTokens = int32(opts.MaxTokens)
	}
	// Gemini's knob is a token budget rather than a named level, and
	// the ladder is capped at the smaller of the 2.5 maxima so one setting
	// serves flash and pro. Off sends no thinking config at all: the models
	// that cannot turn thinking off should keep their own default rather
	// than be handed a zero they will refuse.
	if budget := opts.Effort.Fit(CapabilitiesFor(model)).ThinkingBudget(0); budget > 0 {
		b := int32(budget)
		config.ThinkingConfig = &genai.ThinkingConfig{ThinkingBudget: &b}
	}
	if len(opts.Tools) > 0 {
		config.Tools = toGeminiTools(opts.Tools)
		if opts.ToolChoice != "" {
			config.ToolConfig = toGeminiToolConfig(opts.ToolChoice)
		}
	}

	ch := make(chan StreamEvent)
	go func() {
		defer close(ch)
		var toolCalls []ToolCall
		var reasoning []ReasoningBlock
		var usage *Usage
		for resp, err := range g.client.Models.GenerateContentStream(ctx, model, contents, config) {
			if err != nil {
				// The function calls already delivered travel with the
				// failure, so a dropped stream can be continued.
				ch <- StreamEvent{
					ToolCalls: CompletedToolCalls(toolCalls),
					Reasoning: reasoning,
					Err:       g.classify(err),
					Done:      true,
				}
				return
			}
			if resp == nil {
				continue
			}
			if resp.UsageMetadata != nil {
				usage = &Usage{
					PromptTokens:     int(resp.UsageMetadata.PromptTokenCount),
					CompletionTokens: int(resp.UsageMetadata.CandidatesTokenCount),
					CachedTokens:     int(resp.UsageMetadata.CachedContentTokenCount),
				}
			}
			if len(resp.Candidates) > 0 {
				candidate := resp.Candidates[0]
				if candidate.Content != nil {
					for _, part := range candidate.Content.Parts {
						switch {
						case part.FunctionCall != nil:
							args, _ := json.Marshal(part.FunctionCall.Args)
							toolCalls = append(toolCalls, ToolCall{
								// The Gemini API leaves functionCall.id
								// empty, and a call with no id is
								// one a dropped stream discards (partial.go)
								// and no tool result can be paired with. The
								// id is ours to invent, so we invent one.
								ID:        first(part.FunctionCall.ID, nextGeminiCallID()),
								Name:      part.FunctionCall.Name,
								Arguments: string(args),
								Signature: encodeSignature(part.ThoughtSignature),
							})
						case part.Thought:
							// Thinking is not the answer: it goes back on
							// the next request as a thought part, and it
							// travels on its own channel rather than as a
							// token, which would have printed the model's
							// thinking as its reply.
							reasoning = appendThought(reasoning, part.Text, part.ThoughtSignature)
							if part.Text != "" {
								ch <- StreamEvent{Thinking: part.Text}
							}
						case part.Text != "":
							ch <- StreamEvent{Token: part.Text}
						}
					}
				}
			}
		}
		ch <- StreamEvent{ToolCalls: toolCalls, Reasoning: reasoning, Usage: usage, Done: true}
	}()

	return ch, nil
}

// geminiCallSeq numbers the ids we invent for function calls the API sent
// without one. It is process-wide so two rounds of the same session cannot
// hand out the same id, which would let a later tool result pair with an
// earlier call.
var geminiCallSeq atomic.Uint64

func nextGeminiCallID() string {
	return fmt.Sprintf("gemini_call_%d", geminiCallSeq.Add(1))
}

// encodeSignature/decodeSignature carry Gemini's binary thought signature
// through the neutral message types, which are stored as JSON when a session
// is saved. Base64 is the form that survives that round trip.
func encodeSignature(sig []byte) string {
	if len(sig) == 0 {
		return ""
	}
	return base64.StdEncoding.EncodeToString(sig)
}

func decodeSignature(sig string) []byte {
	if sig == "" {
		return nil
	}
	out, err := base64.StdEncoding.DecodeString(sig)
	if err != nil {
		return nil
	}
	return out
}

// appendThought folds one streamed thought part into the reasoning blocks.
// Thinking arrives in chunks and its signature lands on the chunk that closes
// it, so chunks join the open block until one is signed; a signed block is
// finished and the next chunk starts a new one.
func appendThought(blocks []ReasoningBlock, text string, sig []byte) []ReasoningBlock {
	if text == "" && len(sig) == 0 {
		return blocks
	}
	if n := len(blocks); n > 0 && blocks[n-1].Signature == "" {
		blocks[n-1].Text += text
		blocks[n-1].Signature = encodeSignature(sig)
		return blocks
	}
	return append(blocks, ReasoningBlock{Text: text, Signature: encodeSignature(sig)})
}

// toGeminiContents converts the neutral message history to Gemini's shape.
//
// A tool result is addressed by the *name* of the function it answers —
// functionResponse.name has to match the functionCall.name it came from, and
// the ids the rest of shhh pairs on are ours, not the API's. So the calls of
// the assistant turn just passed are kept, and each result takes its name
// from the call it answers. Consecutive results merge into one function turn,
// in call order, which is also how Gemini tells apart two calls of the same
// function made in the same round.
func toGeminiContents(messages []Message) ([]*genai.Content, *genai.Content) {
	var contents []*genai.Content
	var systemInstruction *genai.Content

	// lastCalls is the tool calls of the most recent assistant turn and
	// answered how many of them have already been matched; pending collects
	// the results owed to that turn until something else ends the run.
	var lastCalls []ToolCall
	answered := 0
	var pending []*genai.Part

	flushPending := func() {
		if len(pending) > 0 {
			contents = append(contents, &genai.Content{Role: "function", Parts: pending})
			pending = nil
		}
	}

	for _, msg := range messages {
		switch msg.Role {
		case RoleSystem:
			systemInstruction = &genai.Content{
				Parts: []*genai.Part{{Text: msg.Content}},
				Role:  "user",
			}
		case RoleUser:
			flushPending()
			// Attachments lead, the sentence follows: inline blobs
			// for the bytes Gemini reads natively, the shared text form for
			// the rest.
			parts := geminiAttachmentParts(msg.Attachments)
			if msg.Content != "" || len(parts) == 0 {
				parts = append(parts, &genai.Part{Text: msg.Content})
			}
			contents = append(contents, &genai.Content{Parts: parts, Role: "user"})
		case RoleAssistant:
			flushPending()
			content := &genai.Content{Role: "model"}
			// Thinking leads the turn, carrying the signatures back on the
			// parts they arrived on: a Gemini 3 turn whose function calls
			// come back unsigned is one the model cannot pick up where it
			// left off, and it re-plans from the top instead.
			for _, r := range msg.Reasoning {
				if r.Text == "" && r.Signature == "" {
					continue
				}
				content.Parts = append(content.Parts, &genai.Part{
					Text:             r.Text,
					Thought:          true,
					ThoughtSignature: decodeSignature(r.Signature),
				})
			}
			if msg.Content != "" {
				content.Parts = append(content.Parts, &genai.Part{Text: msg.Content})
			}
			for _, tc := range msg.ToolCalls {
				args := map[string]any{}
				_ = json.Unmarshal([]byte(tc.Arguments), &args)
				content.Parts = append(content.Parts, &genai.Part{
					FunctionCall: &genai.FunctionCall{
						Name: tc.Name,
						Args: args,
					},
					ThoughtSignature: decodeSignature(tc.Signature),
				})
			}
			contents = append(contents, content)
			lastCalls, answered = msg.ToolCalls, 0
		case RoleTool:
			name := msg.ToolCallID
			if i, ok := matchGeminiCall(lastCalls, msg.ToolCallID, answered); ok {
				name = lastCalls[i].Name
				answered = i + 1
			}
			pending = append(pending, &genai.Part{
				FunctionResponse: &genai.FunctionResponse{
					Name:     name,
					Response: map[string]any{"result": msg.Content},
				},
			})
		}
	}
	flushPending()

	return contents, systemInstruction
}

// matchGeminiCall finds the call a tool result answers: the one carrying its
// id, or failing that the next call not yet answered — results are appended
// in call order, so position is a sound fallback for the histories (a resumed
// session, a hand-built message list) whose ids do not line up.
func matchGeminiCall(calls []ToolCall, id string, answered int) (int, bool) {
	if id != "" {
		for i, tc := range calls {
			if tc.ID == id {
				return i, true
			}
		}
	}
	if answered < len(calls) {
		return answered, true
	}
	return 0, false
}

// geminiAttachmentParts carries a user message's attachments as inline data.
// Gemini takes images and PDFs as blobs; text attachments stay text.
func geminiAttachmentParts(atts []Attachment) []*genai.Part {
	var parts []*genai.Part
	for _, a := range atts {
		switch a.Kind {
		case AttachmentImage, AttachmentDocument:
			parts = append(parts, &genai.Part{
				InlineData: &genai.Blob{Data: a.Data, MIMEType: a.MediaType},
			})
		default:
			parts = append(parts, &genai.Part{Text: a.AsText()})
		}
	}
	return parts
}

func toGeminiTools(tools []Tool) []*genai.Tool {
	decls := make([]*genai.FunctionDeclaration, len(tools))
	for i, t := range tools {
		decls[i] = &genai.FunctionDeclaration{
			Name:                 t.Name,
			Description:          t.Description,
			ParametersJsonSchema: jsonSchemaToAny(t.Parameters),
		}
	}
	return []*genai.Tool{{FunctionDeclarations: decls}}
}

func toGeminiToolConfig(choice string) *genai.ToolConfig {
	var mode genai.FunctionCallingConfigMode
	switch choice {
	case "auto":
		mode = genai.FunctionCallingConfigModeAuto
	case "any", "required":
		mode = genai.FunctionCallingConfigModeAny
	case "none":
		mode = genai.FunctionCallingConfigModeNone
	default:
		mode = genai.FunctionCallingConfigModeAuto
	}
	return &genai.ToolConfig{
		FunctionCallingConfig: &genai.FunctionCallingConfig{Mode: mode},
	}
}

func jsonSchemaToAny(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var v any
	_ = json.Unmarshal(raw, &v)
	return v
}

func init() {
	Register("gemini", func(opts ResolveOpts) (Provider, error) {
		return NewGemini(opts)
	})
	RegisterDefaults("gemini", ProviderDefaults{
		Model:      defaultGeminiModel,
		CheapModel: cheapGeminiModel,
	})
}
