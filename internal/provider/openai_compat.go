package provider

import (
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
	defaultCompatBaseURL = "http://localhost:11434/v1"
	defaultCompatModel   = "llama3"
)

type OpenAICompat struct {
	client *openai.Client
	// httpc and apiKey are how the endpoint's catalog is read for the fields
	// the client's model type drops — the context length, which only the
	// runtime serving the weights knows. Both are empty on a provider built
	// over a caller's own client (a gateway profile), which sends through a
	// transport this package did not configure and declares its models'
	// windows in the profile file anyway.
	httpc    *http.Client
	apiKey   string
	model    string
	baseURL  string
	name     string
	classify func(error) error
}

func NewOpenAICompat(opts ResolveOpts) (*OpenAICompat, error) {
	baseURL := first(opts.BaseURL, os.Getenv("SHHH_BASE_URL"), opts.ConfigBaseURL, defaultCompatBaseURL)
	model := first(opts.Model, defaultCompatModel)
	key := first(opts.APIKey, os.Getenv("SHHH_API_KEY"), opts.ConfigAPIKey)

	name := first(opts.Name, opts.ConfigName, "openai-compatible")

	cfg := openai.DefaultConfig(key)
	cfg.BaseURL = baseURL
	return &OpenAICompat{
		client:   openai.NewClientWithConfig(cfg),
		httpc:    http.DefaultClient,
		apiKey:   key,
		model:    model,
		baseURL:  baseURL,
		name:     name,
		classify: newClassifier(name, "SHHH_API_KEY", key),
	}, nil
}

func first(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func NewOpenAICompatWith(client *openai.Client, model, baseURL string) *OpenAICompat {
	if model == "" {
		model = defaultCompatModel
	}
	return &OpenAICompat{client: client, model: model, baseURL: baseURL, name: "openai-compatible", classify: newClassifier("openai-compatible", "SHHH_API_KEY", "")}
}

// NewOpenAICompatNamed builds a compat provider over an already-configured
// client under a caller-chosen name, for gateway profiles
// (internal/profile) that supply their own transport.
func NewOpenAICompatNamed(client *openai.Client, model, baseURL, name string) *OpenAICompat {
	p := NewOpenAICompatWith(client, model, baseURL)
	if name != "" {
		// The classifier is rebound too: a failure behind a gateway has to
		// say which gateway, and the profile's name is the only
		// place that is known.
		p.name = name
		p.classify = newClassifier(name, "SHHH_API_KEY", "")
	}
	return p
}

func (o *OpenAICompat) Name() string { return o.name }

// ListModels enumerates whatever the endpoint hosts (GET {base_url}/models).
// This is the only way to know an openai-compatible endpoint's catalog, so
// the /model picker leans on it rather than on a curated list.
func (o *OpenAICompat) ListModels(ctx context.Context) ([]string, error) {
	names, err := listOpenAIModels(ctx, o.client)
	if err != nil {
		return nil, o.classify(err)
	}
	return names, nil
}

// maxModelsBody bounds the catalog read. A models listing is a few hundred
// bytes per model and this is a probe nobody asked for, so an endpoint
// answering with something enormous is dropped rather than buffered.
const maxModelsBody = 1 << 20

// endpointModel is one entry of the catalog, read for the context length
// beside the id. There is no standard field for it: vLLM writes
// max_model_len, llama.cpp nests n_ctx under meta, LM Studio writes
// max_context_length, and Ollama writes none at all. A runtime that reports
// nothing is not an answer, and the table and the family floor take it from
// there.
type endpointModel struct {
	ID            string `json:"id"`
	MaxModelLen   int64  `json:"max_model_len"`
	MaxContextLen int64  `json:"max_context_length"`
	ContextLength int64  `json:"context_length"`
	Meta          struct {
		NCtx      int64 `json:"n_ctx"`
		NCtxTrain int64 `json:"n_ctx_train"`
	} `json:"meta"`
}

// window is the length the endpoint will actually serve, preferred over the
// length the weights were trained at — llama.cpp reports both, and a server
// started with a smaller n_ctx than the training window serves the smaller
// one.
func (m endpointModel) window() int64 {
	for _, n := range []int64{m.MaxModelLen, m.MaxContextLen, m.ContextLength, m.Meta.NCtx, m.Meta.NCtxTrain} {
		if n > 0 {
			return n
		}
	}
	return 0
}

// ModelWindows asks the endpoint what context length it serves each model at
// — GET {base_url}/models, the request the catalog already comes from — and
// returns the models that answered, keyed by lower-cased id.
//
// The response is read here rather than through the client because the
// client's model type keeps the five standard fields and drops the one this
// is for. Nothing is classified or logged: this is a background probe against
// an endpoint that may not serve a catalog at all, and a 404 nobody asked for
// is not a failure worth a line in the diagnostic log. The endpoint's real
// problems reach the user through the /model picker's own query.
func (o *OpenAICompat) ModelWindows(ctx context.Context) (map[string]int64, error) {
	if o.httpc == nil {
		return nil, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(o.baseURL, "/")+"/models", nil)
	if err != nil {
		return nil, err
	}
	if o.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+o.apiKey)
	}
	resp, err := o.httpc.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: models endpoint returned %s", o.name, resp.Status)
	}
	var body struct {
		Data []endpointModel `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxModelsBody)).Decode(&body); err != nil {
		return nil, err
	}
	var windows map[string]int64
	for _, m := range body.Data {
		w := m.window()
		if m.ID == "" || w <= 0 {
			continue
		}
		if windows == nil {
			windows = make(map[string]int64, len(body.Data))
		}
		windows[strings.ToLower(m.ID)] = w
	}
	return windows, nil
}

func (o *OpenAICompat) StreamCompletion(ctx context.Context, messages []Message, opts CompletionOpts) (<-chan StreamEvent, error) {
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
		req.MaxTokens = opts.MaxTokens
	}
	// Reasoning effort is sent only when the model is known to take one.
	// An openai-compatible endpoint hosts whatever it hosts, and a level
	// fitted to a model nobody could describe is a field a local runtime
	// may refuse; the table and the family floor are the only judges here.
	if caps := CapabilitiesFor(req.Model); caps.Known {
		if effort := opts.Effort.Fit(caps).OpenAIEffort(); effort != "" {
			req.ReasoningEffort = effort
		}
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

func init() {
	Register("openai-compatible", func(opts ResolveOpts) (Provider, error) {
		return NewOpenAICompat(opts)
	})
	// No cheap model: this provider points at whatever endpoint the user
	// runs, and the models it serves are the ones they pulled. Naming one
	// here would send the bounded calls to a model the endpoint answers 404
	// for, so they stay on the session model.
	RegisterDefaults("openai-compatible", ProviderDefaults{
		Model:   defaultCompatModel,
		BaseURL: defaultCompatBaseURL,
	})
}
