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
	// cheapResponsesModel is the same nano rung the chat-completions
	// provider names; this API serves it too.
	cheapResponsesModel = "gpt-5.4-nano"
	// includeEncryptedReasoning asks for the round's thinking to come back
	// sealed. It is the only way to get it from a request that stores
	// nothing, and the whole of what makes reasoning replayable here.
	includeEncryptedReasoning = "reasoning.encrypted_content"
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
	// Include names the parts of the answer that are otherwise left out. It
	// travels with the reasoning object and for the same reason: a model
	// with no reasoning to seal is a model that rejects being asked for it.
	Include []string `json:"include,omitempty"`
	// Reasoning is sent only when the session asked for a level:
	// the Responses API serves non-reasoning models too, and they reject it.
	Reasoning *responsesReasoning `json:"reasoning,omitempty"`
	// Text carries the structured-output format. This API keeps it beside
	// the answer's other text settings rather than in a `response_format`
	// of its own, which is where chat completions puts the same thing.
	Text *responsesText `json:"text,omitempty"`
}

// responsesText and responsesTextFormat are the schema an answer is asked to
// match. Strict is the reason to ask at all: the answer is validated before
// it is sent, and a schema that does not close every object and require
// every key is refused rather than validated loosely.
type responsesText struct {
	Format responsesTextFormat `json:"format"`
}

type responsesTextFormat struct {
	Type   string          `json:"type"`
	Name   string          `json:"name"`
	Schema json.RawMessage `json:"schema"`
	Strict bool            `json:"strict"`
}

// responsesReasoning is the reasoning object: a named effort, which is the
// whole of what shhh sets.
type responsesReasoning struct {
	Effort string `json:"effort,omitempty"`
}

// responseItem is one element of the flat input list: a message, a call the
// assistant made, or the output that call produced.
type responseItem struct {
	Type string `json:"type"`
	// ID is the item's own id, which only a replayed reasoning item carries:
	// the API asks for such an item back as it was handed over, and the id is
	// part of it.
	ID        string            `json:"id,omitempty"`
	Role      string            `json:"role,omitempty"`
	Content   []responseContent `json:"content,omitempty"`
	CallID    string            `json:"call_id,omitempty"`
	Name      string            `json:"name,omitempty"`
	Arguments string            `json:"arguments,omitempty"`
	Output    string            `json:"output,omitempty"`
	// Summary and EncryptedContent are a reasoning item's two halves. shhh
	// asks for no summary, so the readable half goes back as the empty list
	// the item's shape still requires, and the sealed half goes back as it
	// arrived.
	Summary          json.RawMessage `json:"summary,omitempty"`
	EncryptedContent string          `json:"encrypted_content,omitempty"`
}

type responseContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	// The attachment forms: an inline image is a data URL, an inline
	// document is a filename plus the same data URL under another name, and
	// an inline recording is a nested object of its own.
	ImageURL   string              `json:"image_url,omitempty"`
	Filename   string              `json:"filename,omitempty"`
	FileData   string              `json:"file_data,omitempty"`
	InputAudio *responseInputAudio `json:"input_audio,omitempty"`
}

// responseInputAudio is an inline recording. It is the one attachment form on
// this API that is not a data URL: the bytes go in bare base64 with no
// `data:` prefix and no media type in front of them, and the format is named
// beside them as a bare token — `mp3`, not `audio/mpeg`.
type responseInputAudio struct {
	Data   string `json:"data"`
	Format string `json:"format"`
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
	// Fitted to the model: a rung it lacks becomes the highest it has, and
	// a model with no reasoning gets no field.
	effort := opts.Effort.Fit(CapabilitiesFor(model)).OpenAIEffort()
	// Asking for the sealed thinking and sending it back are one decision. A
	// request that did not ask has nothing sealed to send, and a model with
	// no reasoning refuses a reasoning item outright — which is what a
	// history carried across a switch to such a model would hand it.
	input, instructions := toResponseItems(messages, effort != "")
	req := responsesRequest{
		Model:        model,
		Input:        input,
		Instructions: instructions,
		Stream:       true,
		// shhh sends the whole conversation every turn, so there is nothing
		// to gain from server-side retention — with one exception, and it is
		// why the request asks for the sealed reasoning below. The model's
		// own thinking is the one part of a round the harness cannot rebuild
		// from its own history, so what retention would have kept has to come
		// back on the response instead and go out again on the next request.
		Store:       false,
		Temperature: opts.Temperature,
		MaxOutput:   opts.MaxTokens,
	}
	// One or the other, never both (provider.go). The tool choice goes with
	// the tools: it is a sentence about calling one, and there is nothing
	// to call on a request that asked for an object instead.
	if schema := opts.SchemaFor(model); schema != nil {
		req.Text = &responsesText{Format: responsesTextFormat{
			Type:   "json_schema",
			Name:   schema.Name,
			Schema: schema.Schema,
			Strict: true,
		}}
	} else {
		req.Tools = toResponsesTools(opts.Tools)
		req.ToolChoice = opts.ToolChoice
	}
	if effort != "" {
		req.Reasoning = &responsesReasoning{Effort: effort}
		req.Include = []string{includeEncryptedReasoning}
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
// replayReasoning says whether the assistant turns' thinking goes back with
// them, which is the caller's judgement because it depends on the model
// rather than on the conversation.
func toResponseItems(messages []Message, replayReasoning bool) ([]responseItem, string) {
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
			// Thinking leads the turn, in the order and the form it arrived.
			// Nothing here refuses a request that dropped it, which is what
			// makes it easy to drop: the model derives the plan behind its
			// own calls again, every round, from the tool results alone.
			// See docs/capabilities/providers.md#thinking-goes-back-to-the-model-that-did-it.
			for _, r := range m.Reasoning {
				// Both halves or nothing. A block missing either is one
				// another dialect produced — a session that changed provider
				// mid-conversation carries its old thinking with it — and
				// sending it describes an item this endpoint never issued.
				if !replayReasoning || r.Signature == "" || r.Redacted == "" {
					continue
				}
				items = append(items, responseItem{
					Type:             "reasoning",
					ID:               r.Signature,
					Summary:          json.RawMessage("[]"),
					EncryptedContent: r.Redacted,
				})
			}
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
// This API takes an image, a document and a recording inline, so text
// attachments use the shared text form — and so does a recording in either of
// the formats the audio part does not name.
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
		case AttachmentAudio:
			if format, ok := openAIAudioFormat(a.MediaType); ok {
				out = append(out, responseContent{
					Type:       "input_audio",
					InputAudio: &responseInputAudio{Data: a.Base64(), Format: format},
				})
			} else {
				out = append(out, responseContent{Type: "input_text", Text: a.AsText()})
			}
		default:
			out = append(out, responseContent{Type: "input_text", Text: a.AsText()})
		}
	}
	return out
}

// openAIAudioFormat is the token OpenAI's inline audio part names a format
// by, and whether the format is one of the two that part takes: MP3 and WAV.
// An AIFF or an Ogg goes as the fallback note instead. The field is an
// enumeration, so a third value is a 400 on the whole request rather than a
// part the model quietly skips, and a turn lost to a voice memo is worse than
// a voice memo the model is told about in words.
// See docs/capabilities/chat.md#what-can-ride-with-a-message.
func openAIAudioFormat(mediaType string) (string, bool) {
	switch mediaType {
	case "audio/mpeg":
		return "mp3", true
	case "audio/wav":
		return "wav", true
	}
	return "", false
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
		Model:      defaultResponsesModel,
		BaseURL:    defaultResponsesBaseURL,
		CheapModel: cheapResponsesModel,
	})
}
