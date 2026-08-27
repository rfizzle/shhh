package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"google.golang.org/genai"
)

const defaultGeminiModel = "gemini-2.5-flash"

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
		var usage *Usage
		for resp, err := range g.client.Models.GenerateContentStream(ctx, model, contents, config) {
			if err != nil {
				// The function calls already delivered travel with the
				// failure, so a dropped stream can be continued (S-107).
				ch <- StreamEvent{ToolCalls: CompletedToolCalls(toolCalls), Err: g.classify(err), Done: true}
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
						if part.Text != "" {
							ch <- StreamEvent{Token: part.Text}
						}
						if part.FunctionCall != nil {
							args, _ := json.Marshal(part.FunctionCall.Args)
							toolCalls = append(toolCalls, ToolCall{
								ID:        part.FunctionCall.ID,
								Name:      part.FunctionCall.Name,
								Arguments: string(args),
							})
						}
					}
				}
			}
		}
		if len(toolCalls) > 0 {
			ch <- StreamEvent{ToolCalls: toolCalls, Usage: usage, Done: true}
		} else {
			ch <- StreamEvent{Usage: usage, Done: true}
		}
	}()

	return ch, nil
}

func toGeminiContents(messages []Message) ([]*genai.Content, *genai.Content) {
	var contents []*genai.Content
	var systemInstruction *genai.Content

	for _, msg := range messages {
		switch msg.Role {
		case RoleSystem:
			systemInstruction = &genai.Content{
				Parts: []*genai.Part{{Text: msg.Content}},
				Role:  "user",
			}
		case RoleUser:
			// Attachments lead, the sentence follows (S-134): inline blobs
			// for the bytes Gemini reads natively, the shared text form for
			// the rest.
			parts := geminiAttachmentParts(msg.Attachments)
			if msg.Content != "" || len(parts) == 0 {
				parts = append(parts, &genai.Part{Text: msg.Content})
			}
			contents = append(contents, &genai.Content{Parts: parts, Role: "user"})
		case RoleAssistant:
			content := &genai.Content{Role: "model"}
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
				})
			}
			contents = append(contents, content)
		case RoleTool:
			result := map[string]any{"result": msg.Content}
			contents = append(contents, &genai.Content{
				Role: "function",
				Parts: []*genai.Part{{
					FunctionResponse: &genai.FunctionResponse{
						Name:     msg.ToolCallID,
						Response: result,
					},
				}},
			})
		}
	}

	return contents, systemInstruction
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
		Model: defaultGeminiModel,
	})
}
