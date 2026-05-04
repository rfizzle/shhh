package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"google.golang.org/genai"
)

const defaultGeminiModel = "gemini-2.5-flash"

type Gemini struct {
	client *genai.Client
	model  string
}

func NewGemini(opts ResolveOpts) (*Gemini, error) {
	key := first(opts.APIKey, os.Getenv("GEMINI_API_KEY"))
	if key == "" {
		return nil, fmt.Errorf("GEMINI_API_KEY is not set")
	}

	model := first(opts.Model, defaultGeminiModel)

	client, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		APIKey:  key,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create Gemini client: %w", err)
	}

	return &Gemini{client: client, model: model}, nil
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
		for resp, err := range g.client.Models.GenerateContentStream(ctx, model, contents, config) {
			if err != nil {
				ch <- StreamEvent{Err: classifyGeminiError(err), Done: true}
				return
			}
			if resp != nil && len(resp.Candidates) > 0 {
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
			ch <- StreamEvent{ToolCalls: toolCalls, Done: true}
		} else {
			ch <- StreamEvent{Done: true}
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
			contents = append(contents, &genai.Content{
				Parts: []*genai.Part{{Text: msg.Content}},
				Role:  "user",
			})
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

var (
	ErrGeminiUnauthorized = fmt.Errorf("invalid API key — check GEMINI_API_KEY")
	ErrGeminiRateLimited  = fmt.Errorf("rate limited — try again shortly")
)

func classifyGeminiError(err error) error {
	errStr := err.Error()
	for _, pattern := range []struct {
		code int
		msg  string
		wrap error
	}{
		{http.StatusUnauthorized, "401", ErrGeminiUnauthorized},
		{http.StatusForbidden, "403", ErrGeminiUnauthorized},
		{http.StatusTooManyRequests, "429", ErrGeminiRateLimited},
	} {
		if strings.Contains(errStr, pattern.msg) {
			return fmt.Errorf("%w: %s", pattern.wrap, err)
		}
	}
	return err
}

func init() {
	Register("gemini", func(opts ResolveOpts) (Provider, error) {
		return NewGemini(opts)
	})
}
