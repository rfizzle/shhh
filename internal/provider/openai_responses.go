package provider

// The OpenAI Responses API (POST /v1/responses) — the dialect the GPT-5
// family is served through, and the one gateways route those models to.
//
// It is not the chat-completions shape with different field names: the
// conversation is a flat list of typed input items rather than messages with
// attached tool calls, tools are flattened, and the stream is a sequence of
// named events instead of choice deltas. That is enough difference to want
// its own provider rather than a translation layer inside the compat one.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	openai "github.com/sashabaranov/go-openai"
)

const (
	defaultResponsesModel   = "gpt-4.1"
	defaultResponsesBaseURL = "https://api.openai.com/v1"
)

// OpenAIResponses speaks the Responses API over a plain HTTP client, so a
// gateway profile's rewriting transport (internal/profile) applies to it the
// same way it applies to every other openai-shaped provider.
type OpenAIResponses struct {
	client   *http.Client
	apiKey   string
	baseURL  string
	model    string
	name     string
	classify func(error) error
}

func NewOpenAIResponses(opts ResolveOpts) (*OpenAIResponses, error) {
	key := first(opts.APIKey, os.Getenv("SHHH_API_KEY"), os.Getenv("OPENAI_API_KEY"), opts.ConfigAPIKey)
	if key == "" {
		return nil, fmt.Errorf("SHHH_API_KEY or OPENAI_API_KEY is not set")
	}
	baseURL := first(opts.BaseURL, os.Getenv("SHHH_BASE_URL"), opts.ConfigBaseURL, defaultResponsesBaseURL)
	return NewOpenAIResponsesWith(nil, key, baseURL, first(opts.Model, defaultResponsesModel), "openai-responses"), nil
}

// NewOpenAIResponsesWith builds the provider over a caller-supplied HTTP
// client and name, for gateway profiles.
func NewOpenAIResponsesWith(client *http.Client, apiKey, baseURL, model, name string) *OpenAIResponses {
	if client == nil {
		client = &http.Client{}
	}
	return &OpenAIResponses{
		client:   client,
		apiKey:   apiKey,
		baseURL:  strings.TrimSuffix(first(baseURL, defaultResponsesBaseURL), "/"),
		model:    first(model, defaultResponsesModel),
		name:     first(name, "openai-responses"),
		classify: newClassifier(first(name, "openai-responses"), "SHHH_API_KEY or OPENAI_API_KEY", apiKey),
	}
}

func (o *OpenAIResponses) Name() string { return o.name }

// ListModels enumerates the endpoint's catalog. The models endpoint is the
// same one the chat dialects use, so it goes through the shared client.
func (o *OpenAIResponses) ListModels(ctx context.Context) ([]string, error) {
	cfg := openai.DefaultConfig(o.apiKey)
	cfg.BaseURL = o.baseURL
	cfg.HTTPClient = o.client
	names, err := listOpenAIModels(ctx, openai.NewClientWithConfig(cfg))
	if err != nil {
		return nil, o.classify(err)
	}
	return names, nil
}

// responsesRequest is the wire request. Fields shhh does not set are omitted
// rather than sent as zero values: a reasoning model rejects some of them,
// and a profile rule is the place to add back whatever a gateway needs.
type responsesRequest struct {
	Model        string          `json:"model"`
	Input        []responseItem  `json:"input"`
	Instructions string          `json:"instructions,omitempty"`
	Stream       bool            `json:"stream"`
	Store        bool            `json:"store"`
	Tools        []responsesTool `json:"tools,omitempty"`
	ToolChoice   string          `json:"tool_choice,omitempty"`
	Temperature  *float64        `json:"temperature,omitempty"`
	MaxOutput    int             `json:"max_output_tokens,omitempty"`
}

// responseItem is one element of the flat input list: a message, a call the
// assistant made, or the output that call produced.
type responseItem struct {
	Type      string            `json:"type"`
	Role      string            `json:"role,omitempty"`
	Content   []responseContent `json:"content,omitempty"`
	CallID    string            `json:"call_id,omitempty"`
	Name      string            `json:"name,omitempty"`
	Arguments string            `json:"arguments,omitempty"`
	Output    string            `json:"output,omitempty"`
}

type responseContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	// The attachment forms (S-134): an inline image is a data URL, an inline
	// document is a filename plus the same data URL under another name.
	ImageURL string `json:"image_url,omitempty"`
	Filename string `json:"filename,omitempty"`
	FileData string `json:"file_data,omitempty"`
}

// responsesTool is a function tool. The Responses API flattens what chat
// completions nests under "function".
type responsesTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
	// Strict is sent explicitly: shhh's tool schemas are permissive, and
	// strict mode rejects a schema that does not close every object.
	Strict bool `json:"strict"`
}

func (o *OpenAIResponses) StreamCompletion(ctx context.Context, messages []Message, opts CompletionOpts) (<-chan StreamEvent, error) {
	model := o.model
	if opts.Model != "" {
		model = opts.Model
	}
	input, instructions := toResponseItems(messages)
	req := responsesRequest{
		Model:        model,
		Input:        input,
		Instructions: instructions,
		Stream:       true,
		// shhh sends the whole conversation every turn, so there is nothing
		// to gain from server-side retention.
		Store:       false,
		Tools:       toResponsesTools(opts.Tools),
		ToolChoice:  opts.ToolChoice,
		Temperature: opts.Temperature,
		MaxOutput:   opts.MaxTokens,
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/responses", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	if o.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+o.apiKey)
	}

	resp, err := o.client.Do(httpReq)
	if err != nil {
		return nil, o.classify(err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, o.classify(responsesHTTPError(resp))
	}
	return streamResponses(resp.Body, o.classify), nil
}

// toResponseItems flattens shhh's messages into the Responses input list,
// lifting the system prompt into `instructions` where the API expects it.
func toResponseItems(messages []Message) ([]responseItem, string) {
	var items []responseItem
	var instructions []string
	for _, m := range messages {
		switch m.Role {
		case RoleSystem:
			if m.Content != "" {
				instructions = append(instructions, m.Content)
			}
		case RoleUser:
			content := responsesAttachmentContent(m.Attachments)
			if m.Content != "" || len(content) == 0 {
				content = append(content, responseContent{Type: "input_text", Text: m.Content})
			}
			items = append(items, responseItem{
				Type:    "message",
				Role:    string(RoleUser),
				Content: content,
			})
		case RoleAssistant:
			if m.Content != "" {
				items = append(items, responseItem{
					Type:    "message",
					Role:    string(RoleAssistant),
					Content: []responseContent{{Type: "output_text", Text: m.Content}},
				})
			}
			// A call and its output are separate items here, tied by call_id
			// rather than by nesting.
			for _, tc := range m.ToolCalls {
				items = append(items, responseItem{
					Type:      "function_call",
					CallID:    tc.ID,
					Name:      tc.Name,
					Arguments: tc.Arguments,
				})
			}
		case RoleTool:
			items = append(items, responseItem{
				Type:   "function_call_output",
				CallID: m.ToolCallID,
				Output: m.Content,
			})
		}
	}
	return items, strings.Join(instructions, "\n\n")
}

// responsesAttachmentContent renders attachments as Responses input parts.
// This API takes both an image and a document inline, so only text
// attachments use the shared text form.
func responsesAttachmentContent(atts []Attachment) []responseContent {
	var out []responseContent
	for _, a := range atts {
		switch a.Kind {
		case AttachmentImage:
			out = append(out, responseContent{Type: "input_image", ImageURL: a.DataURL()})
		case AttachmentDocument:
			out = append(out, responseContent{
				Type:     "input_file",
				Filename: a.Name,
				FileData: a.DataURL(),
			})
		default:
			out = append(out, responseContent{Type: "input_text", Text: a.AsText()})
		}
	}
	return out
}

func toResponsesTools(tools []Tool) []responsesTool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]responsesTool, len(tools))
	for i, t := range tools {
		out[i] = responsesTool{
			Type:        "function",
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.Parameters,
		}
	}
	return out
}

// responsesHTTPError turns a non-200 into an error carrying the API's own
// message, classified so an expired key or a rate limit reads as itself.
func responsesHTTPError(resp *http.Response) error {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	var body struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	message := strings.TrimSpace(string(raw))
	if err := json.Unmarshal(raw, &body); err == nil && body.Error.Message != "" {
		message = body.Error.Message
	}
	return &openai.APIError{
		HTTPStatusCode: resp.StatusCode,
		Message:        message,
		Type:           body.Error.Type,
	}
}

func init() {
	Register("openai-responses", func(opts ResolveOpts) (Provider, error) {
		return NewOpenAIResponses(opts)
	})
	RegisterDefaults("openai-responses", ProviderDefaults{
		Model:   defaultResponsesModel,
		BaseURL: defaultResponsesBaseURL,
	})
}
