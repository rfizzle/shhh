package profile

// Registration turns loaded profiles into providers the rest of shhh can
// resolve by name, with the rewrite transport underneath and the declared
// catalog feeding the /model picker.
//
// A profile with endpoints registers one provider all the same. What the
// session holds is a router: it reads the model off each request and hands it
// to the endpoint that claims it, building that endpoint's client the first
// time it is needed and keeping it. The model travels per request
// (provider.CompletionOpts), so a mid-session /model switch crosses from the
// OpenAI-shaped root to the Messages dialect without rebuilding anything the
// session is holding.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
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
	ids := p.ModelIDs()
	if len(ids) == 0 {
		return ""
	}
	return ids[0]
}

// New builds the provider a profile describes. A profile with no endpoints is
// the single client it always was; one with endpoints is a router over them.
//
// An explicit base URL — --base-url, provider.base_url — collapses the whole
// profile onto that one address. Routing is a map from models to endpoints,
// and an override that names one endpoint for everything has already answered
// the question the map exists to answer.
func New(p Profile, opts provider.ResolveOpts) (provider.Provider, error) {
	if opts.BaseURL != "" {
		pinned := p.defaultRoute()
		pinned.BaseURL = opts.BaseURL
		return newEndpoint(p, pinned, opts)
	}
	routes := p.Routes()
	if len(routes) == 1 {
		return newEndpoint(p, routes[0], opts)
	}
	r := &router{profile: p, routes: routes, opts: opts, built: map[int]provider.Provider{}}
	if silent(routes) {
		// Every address is either dialect-less or told not to be asked, so
		// the router has nothing to enumerate. Keeping ListModels here would
		// send the picker through a query that can only return the catalog
		// it already has.
		return noDiscovery{r}, nil
	}
	return r, nil
}

// silent reports whether no route can contribute a discovered model.
func silent(routes []Endpoint) bool {
	for _, e := range routes {
		if e.API != APIAnthropicMessage && !e.DiscoveryOff() {
			return false
		}
	}
	return true
}

// router is a profile's several endpoints behind one provider name.
type router struct {
	profile Profile
	routes  []Endpoint
	opts    provider.ResolveOpts

	mu    sync.Mutex
	built map[int]provider.Provider
}

func (r *router) Name() string { return r.profile.Name }

// StreamCompletion sends the request to the endpoint that claims its model.
func (r *router) StreamCompletion(ctx context.Context, messages []provider.Message, opts provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
	p, err := r.providerFor(opts.Model)
	if err != nil {
		return nil, err
	}
	return p.StreamCompletion(ctx, messages, opts)
}

// providerFor returns the built client for a model's endpoint, building it on
// first use. A profile may name several endpoints a session never touches;
// none of them should cost a client, and — more to the point — an endpoint
// whose key is unset should not fail a session that was never going to send
// it anything.
func (r *router) providerFor(model string) (provider.Provider, error) {
	route := r.profile.Route(model)
	idx := r.indexOf(route)
	r.mu.Lock()
	defer r.mu.Unlock()
	if p, ok := r.built[idx]; ok {
		return p, nil
	}
	p, err := newEndpoint(r.profile, route, r.opts)
	if err != nil {
		return nil, err
	}
	r.built[idx] = p
	return p, nil
}

// indexOf identifies a route by position, which is what Route returned it
// from. Two endpoints can differ only in their match globs, so the position
// is the only reliable identity.
func (r *router) indexOf(route Endpoint) int {
	for i, candidate := range r.routes {
		if candidate.Label == route.Label && candidate.BaseURL == route.BaseURL && candidate.API == route.API {
			return i
		}
	}
	return 0
}

// ListModels is every model the profile declares, plus whatever its endpoints
// can enumerate. An endpoint that refuses discovery — the Messages dialect
// has no catalog at all — contributes its declared models and nothing else,
// which is the same answer a single-endpoint profile gives.
func (r *router) ListModels(ctx context.Context) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	add := func(names []string) {
		for _, name := range names {
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, name)
		}
	}
	add(r.profile.ModelIDs())

	var lastErr error
	for i, route := range r.routes {
		if route.API == APIAnthropicMessage || route.DiscoveryOff() {
			continue
		}
		p, err := r.providerAt(i, route)
		if err != nil {
			lastErr = err
			continue
		}
		lister, ok := p.(provider.ModelLister)
		if !ok {
			continue
		}
		names, err := lister.ListModels(ctx)
		if err != nil {
			lastErr = err
			continue
		}
		add(names)
	}
	if len(out) == 0 && lastErr != nil {
		return nil, lastErr
	}
	return out, nil
}

func (r *router) providerAt(idx int, route Endpoint) (provider.Provider, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p, ok := r.built[idx]; ok {
		return p, nil
	}
	p, err := newEndpoint(r.profile, route, r.opts)
	if err != nil {
		return nil, err
	}
	r.built[idx] = p
	return p, nil
}

// newEndpoint builds the client one endpoint describes: the dialect's own
// client, pointed at that address, over the rewriting transport.
func newEndpoint(p Profile, e Endpoint, opts provider.ResolveOpts) (provider.Provider, error) {
	key := opts.APIKey
	if key == "" {
		key = e.Key()
	}
	if key == "" && e.APIKeyEnv != "" {
		return nil, fmt.Errorf("provider %q: %s is not set", p.Name, e.APIKeyEnv)
	}
	httpClient := &http.Client{Transport: NewTransport(e, nil)}

	switch e.API {
	case APIOpenAIResponses:
		inner := provider.NewOpenAIResponsesWith(httpClient, key, e.BaseURL, opts.Model, p.Name)
		return withDiscovery(e, &responsesProfile{OpenAIResponses: inner, endpoint: e, client: httpClient}), nil
	case APIAnthropicMessage:
		inner := provider.NewAnthropicNamed(anthropic.NewClient(
			option.WithAPIKey(key),
			option.WithBaseURL(e.BaseURL),
			option.WithHTTPClient(httpClient),
		), opts.Model, p.Name)
		return &anthropicProfile{Anthropic: inner, name: p.Name}, nil
	default:
		cfg := openai.DefaultConfig(key)
		cfg.BaseURL = e.BaseURL
		cfg.HTTPClient = httpClient
		inner := provider.NewOpenAICompatNamed(openai.NewClientWithConfig(cfg), opts.Model, e.BaseURL, p.Name)
		return withDiscovery(e, &openAIProfile{OpenAICompat: inner, endpoint: e, client: httpClient}), nil
	}
}

// withDiscovery hides the catalog query when the endpoint turned it off.
//
// Hiding the method rather than answering it with the declared models is the
// difference between "there is nothing to ask" and "I asked and this is what
// came back". The picker reads the capability, not the answer: without it,
// bare /model opens straight onto the declared catalog, with no query
// surface, no ten-second budget, and no request to a gateway whose /models
// the user has told us not to call.
func withDiscovery(e Endpoint, p provider.Provider) provider.Provider {
	if e.DiscoveryOff() {
		return noDiscovery{p}
	}
	return p
}

// noDiscovery is a provider with its catalog query removed. Embedding the
// interface rather than the concrete type is what does it: only the two
// interface methods are promoted, so a ModelLister assertion fails.
type noDiscovery struct{ provider.Provider }

// openAIProfile is a profile-backed openai-chat provider. It inherits
// streaming and discovery from OpenAICompat, overriding discovery only when
// the gateway publishes its catalog somewhere else.
type openAIProfile struct {
	*provider.OpenAICompat
	endpoint Endpoint
	client   *http.Client
}

func (o *openAIProfile) ListModels(ctx context.Context) ([]string, error) {
	if o.endpoint.ModelsPath == "" {
		return o.OpenAICompat.ListModels(ctx)
	}
	return listFrom(ctx, o.client, o.endpoint)
}

// responsesProfile is a profile-backed openai-responses provider, with the
// same catalog override the chat dialect gets.
type responsesProfile struct {
	*provider.OpenAIResponses
	endpoint Endpoint
	client   *http.Client
}

func (r *responsesProfile) ListModels(ctx context.Context) ([]string, error) {
	if r.endpoint.ModelsPath == "" {
		return r.OpenAIResponses.ListModels(ctx)
	}
	return listFrom(ctx, r.client, r.endpoint)
}

// anthropicProfile is a profile-backed anthropic-messages provider. The
// Messages API has no catalog endpoint, so its models are the declared ones.
type anthropicProfile struct {
	*provider.Anthropic
	name string
}

func (a *anthropicProfile) Name() string { return a.name }

// listFrom reads a gateway's catalog from an endpoint's models_path,
// accepting the shapes these endpoints use in practice:
// {"data":[{"id":…}]}, a bare array of objects, or a bare array of strings.
func listFrom(ctx context.Context, client *http.Client, e Endpoint) ([]string, error) {
	endpoint, err := discoveryURL(e)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, discoveryTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	if key := e.Key(); key != "" {
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

// discoveryURL resolves models_path against the endpoint's base URL: an
// absolute path replaces the base's path, a relative one extends it.
func discoveryURL(e Endpoint) (string, error) {
	base, err := url.Parse(e.BaseURL)
	if err != nil {
		return "", err
	}
	ref, err := url.Parse(e.ModelsPath)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(e.ModelsPath, "/") {
		return base.ResolveReference(ref).String(), nil
	}
	base.Path = strings.TrimSuffix(base.Path, "/") + "/" + strings.TrimPrefix(e.ModelsPath, "/")
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
