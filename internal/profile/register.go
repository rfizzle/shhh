package profile

// Registration turns loaded profiles into providers the rest of shhh can
// resolve by name, with the rewrite transport underneath and the declared
// catalog feeding the /model picker.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/rfizzle/shhh/internal/provider"
	openai "github.com/sashabaranov/go-openai"
)

// discoveryTimeout bounds a catalog request to a profile's models_path.
const discoveryTimeout = 15 * time.Second

// registered holds the profiles this process loaded, so the parts of shhh
// that need their metadata — pricing, the `providers` command — can reach
// them without threading the list through every call site, mirroring how the
// provider registry itself works.
var registered []Profile

// Loaded returns the profiles registered in this process, in search order.
func Loaded() []Profile {
	return append([]Profile(nil), registered...)
}

// Register makes every profile resolvable as a provider by its name, with its
// declared models seeding the picker's catalog.
func Register(profiles []Profile) {
	registered = append([]Profile(nil), profiles...)
	for _, p := range profiles {
		p := p
		provider.Register(p.Name, func(opts provider.ResolveOpts) (provider.Provider, error) {
			return New(p, opts)
		})
		provider.RegisterDefaults(p.Name, provider.ProviderDefaults{
			Model:   defaultModel(p),
			BaseURL: p.BaseURL,
		})
		provider.RegisterModels(p.Name, p.ModelIDs())
	}
}

// defaultModel is the profile's first declared model, if any — the model a
// session starts on when nothing else names one.
func defaultModel(p Profile) string {
	if len(p.Models) == 0 {
		return ""
	}
	return p.Models[0].ID
}

// New builds the provider a profile describes: the dialect's own client,
// pointed at the gateway, over the rewriting transport.
func New(p Profile, opts provider.ResolveOpts) (provider.Provider, error) {
	key := opts.APIKey
	if key == "" {
		key = p.Key()
	}
	if key == "" && p.APIKeyEnv != "" {
		return nil, fmt.Errorf("provider %q: %s is not set", p.Name, p.APIKeyEnv)
	}
	baseURL := p.BaseURL
	if opts.BaseURL != "" {
		baseURL = opts.BaseURL
	}
	httpClient := &http.Client{Transport: NewTransport(p, nil)}

	switch p.API {
	case APIOpenAIResponses:
		inner := provider.NewOpenAIResponsesWith(httpClient, key, baseURL, opts.Model, p.Name)
		return &responsesProfile{OpenAIResponses: inner, profile: p, client: httpClient}, nil
	case APIAnthropicMessage:
		inner := provider.NewAnthropicWith(anthropic.NewClient(
			option.WithAPIKey(key),
			option.WithBaseURL(baseURL),
			option.WithHTTPClient(httpClient),
		), opts.Model)
		return &anthropicProfile{Anthropic: inner, name: p.Name}, nil
	default:
		cfg := openai.DefaultConfig(key)
		cfg.BaseURL = baseURL
		cfg.HTTPClient = httpClient
		inner := provider.NewOpenAICompatNamed(openai.NewClientWithConfig(cfg), opts.Model, baseURL, p.Name)
		return &openAIProfile{OpenAICompat: inner, profile: p, client: httpClient}, nil
	}
}

// openAIProfile is a profile-backed openai-chat provider. It inherits
// streaming and discovery from OpenAICompat, overriding discovery only when
// the gateway publishes its catalog somewhere else.
type openAIProfile struct {
	*provider.OpenAICompat
	profile Profile
	client  *http.Client
}

func (o *openAIProfile) ListModels(ctx context.Context) ([]string, error) {
	if o.profile.ModelsPath == "" {
		return o.OpenAICompat.ListModels(ctx)
	}
	return listFrom(ctx, o.client, o.profile)
}

// responsesProfile is a profile-backed openai-responses provider, with the
// same catalog override the chat dialect gets.
type responsesProfile struct {
	*provider.OpenAIResponses
	profile Profile
	client  *http.Client
}

func (r *responsesProfile) ListModels(ctx context.Context) ([]string, error) {
	if r.profile.ModelsPath == "" {
		return r.OpenAIResponses.ListModels(ctx)
	}
	return listFrom(ctx, r.client, r.profile)
}

// anthropicProfile is a profile-backed anthropic-messages provider. The
// Messages API has no catalog endpoint, so its models are the declared ones.
type anthropicProfile struct {
	*provider.Anthropic
	name string
}

func (a *anthropicProfile) Name() string { return a.name }

// listFrom reads a gateway's catalog from a profile's models_path, accepting
// the shapes these endpoints use in practice: {"data":[{"id":…}]}, a bare
// array of objects, or a bare array of strings.
func listFrom(ctx context.Context, client *http.Client, p Profile) ([]string, error) {
	endpoint, err := discoveryURL(p)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, discoveryTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	if key := p.Key(); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", endpoint, resp.StatusCode)
	}
	var raw json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("%s: %w", endpoint, err)
	}
	names, err := parseCatalog(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", endpoint, err)
	}
	return names, nil
}

// discoveryURL resolves models_path against the profile's base URL: an
// absolute path replaces the base's path, a relative one extends it.
func discoveryURL(p Profile) (string, error) {
	base, err := url.Parse(p.BaseURL)
	if err != nil {
		return "", err
	}
	ref, err := url.Parse(p.ModelsPath)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(p.ModelsPath, "/") {
		return base.ResolveReference(ref).String(), nil
	}
	base.Path = strings.TrimSuffix(base.Path, "/") + "/" + strings.TrimPrefix(p.ModelsPath, "/")
	return base.String(), nil
}

// catalogEntry is one model as the catalog shapes describe it.
type catalogEntry struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// parseCatalog extracts model ids from the catalog shapes gateways return.
func parseCatalog(raw json.RawMessage) ([]string, error) {
	var wrapped struct {
		Data []catalogEntry `json:"data"`
	}
	if err := json.Unmarshal(raw, &wrapped); err == nil && len(wrapped.Data) > 0 {
		return idsFrom(wrapped.Data), nil
	}
	var objects []catalogEntry
	if err := json.Unmarshal(raw, &objects); err == nil && len(objects) > 0 {
		return idsFrom(objects), nil
	}
	var strs []string
	if err := json.Unmarshal(raw, &strs); err == nil {
		return strs, nil
	}
	return nil, fmt.Errorf("unrecognized catalog shape")
}

func idsFrom(items []catalogEntry) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		switch {
		case it.ID != "":
			out = append(out, it.ID)
		case it.Name != "":
			out = append(out, it.Name)
		}
	}
	return out
}
