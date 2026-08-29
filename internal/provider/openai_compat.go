package provider

import (
	"context"
	"os"

	openai "github.com/sashabaranov/go-openai"
)

const (
	defaultCompatBaseURL = "http://localhost:11434/v1"
	defaultCompatModel   = "llama3"
)

type OpenAICompat struct {
	client   *openai.Client
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
	RegisterDefaults("openai-compatible", ProviderDefaults{
		Model:   defaultCompatModel,
		BaseURL: defaultCompatBaseURL,
	})
}
