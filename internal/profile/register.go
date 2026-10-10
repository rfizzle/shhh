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
	for _, p := range registered {
		provider.RegisterDeclarations(p.Name, nil)
	}
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
		provider.RegisterDecisions(p.Name, p.DeclaresDecisions)
		// What a model's line narrows is registered as the decisions are,
		// rather than carried on the price table: a line that declares only
		// a narrowing is not a price, and the doctor reads the answer
		// without loading prices at all.
		provider.RegisterDeclarations(p.Name, p.declarations())
	}
}

// declarations is what each declared model's line narrows, by model id.
func (p Profile) declarations() map[string]provider.Declared {
	out := map[string]provider.Declared{}
	for _, m := range p.declaredModels() {
		if d, ok := m.declared(); ok {
			out[m.ID] = d
		}
	}
	return out
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
	if err := p.permits(opts.Model); err != nil {
		return nil, err
	}
	if opts.BaseURL != "" {
		pinned := p.defaultRoute()
		pinned.BaseURL = opts.BaseURL
		inner, err := newEndpoint(p, pinned, opts)
		if err != nil {
			return nil, err
		}
		return p.restrict(inner), nil
	}
	routes := p.Routes()
	if len(routes) == 1 {
		inner, err := newEndpoint(p, routes[0], opts)
		if err != nil {
			return nil, err
		}
		return p.restrict(inner), nil
	}
	r := &router{profile: p, routes: routes, opts: opts, built: map[int]provider.Provider{}}
	if silent(routes) {
		// Every address is either dialect-less or told not to be asked, so
		// the router has nothing to enumerate. Keeping ListModels here would
		// send the picker through a query that can only return the catalog
		// it already has.
		return p.restrict(noDiscovery{r}), nil
	}
	return p.restrict(r), nil
}

// permits verifies only an opt-in declared catalog. Profiles that discover a
// gateway's catalog retain their existing open-ended behavior.
func (p Profile) permits(model string) error {
	if !p.StrictModels || model == "" {
		return nil
	}
	for _, id := range p.ModelIDs() {
		if model == id {
			return nil
		}
	}
	return fmt.Errorf("provider %q: model %q is not in the declared catalog", p.Name, model)
}

func (p Profile) restrict(inner provider.Provider) provider.Provider {
	if !p.StrictModels {
		return inner
	}
	return declaredCatalog{Provider: inner, profile: p}
}

// declaredCatalog is the check that also covers a /model switch after the
// provider was resolved. ResolveOpts can reject only the opening model.
type declaredCatalog struct {
	provider.Provider
	profile Profile
}

func (p declaredCatalog) StreamCompletion(ctx context.Context, messages []provider.Message, opts provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
	if err := p.profile.permits(opts.Model); err != nil {
		return nil, err
	}
	return p.Provider.StreamCompletion(ctx, messages, opts)
}

// OffersDecisions and Decide hold the same catalog check over a decisions
// request that StreamCompletion holds over a turn.
func (p declaredCatalog) OffersDecisions(model string) bool {
	return p.profile.permits(model) == nil && offersDecisions(p.Provider, model)
}

func (p declaredCatalog) Decide(ctx context.Context, req provider.DecisionRequest) (provider.DecisionResult, error) {
	if err := p.profile.permits(req.Model); err != nil {
		return provider.DecisionResult{}, err
	}
	return decide(ctx, p.Provider, req)
}

// offersDecisions and decide reach the Decider under one of this package's
// wrappers. Each wrapper embeds the provider interface, which promotes only
// its two methods, so without them a wrapper would hide the capability the
// way noDiscovery hides the catalog — on purpose there, by accident here.
func offersDecisions(p provider.Provider, model string) bool {
	d, ok := p.(provider.Decider)
	return ok && d.OffersDecisions(model)
}

func decide(ctx context.Context, p provider.Provider, req provider.DecisionRequest) (provider.DecisionResult, error) {
	d, ok := p.(provider.Decider)
	if !ok {
		return provider.DecisionResult{}, fmt.Errorf("provider %q: model %q is not served the Decisions API here", p.Name(), req.Model)
	}
	return d.Decide(ctx, req)
}

// ListModels returns the allowlist without consulting the gateway. A strict
// catalog is a promise that these are the only choices, so a discovered name
// must not turn up in the picker after the request gate would refuse it.
func (p declaredCatalog) ListModels(context.Context) ([]string, error) {
	return p.profile.ModelIDs(), nil
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

// OffersDecisions asks the endpoint the model routes to, which is where a
// decisions request for it would go.
func (r *router) OffersDecisions(model string) bool {
	p, err := r.providerFor(model)
	return err == nil && offersDecisions(p, model)
}

// Decide sends the request to the endpoint that claims its model, as a turn
// is sent.
func (r *router) Decide(ctx context.Context, req provider.DecisionRequest) (provider.DecisionResult, error) {
	p, err := r.providerFor(req.Model)
	if err != nil {
		return provider.DecisionResult{}, err
	}
	return decide(ctx, p, req)
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
	// A route speaking the OpenAI dialect can still be talking to the Messages
	// API: what decides is the model the request names and not the shape it is
	// written in, and a gateway in front of Anthropic models is the case
	// profiles were built for. So that dialect goes out over the marking
	// transport, and the marking sits *under* the profile's own rewrites —
	// what carries the breakpoints has to be the body as it will leave, since
	// a rewrite rule may be what puts the routed model id on it.
	//
	// The empty dialect is this switch's own default and is spelled here for
	// the same reason it is spelled there: an endpoint arrives with the field
	// already filled in, and a route built by hand should not silently lose
	// its breakpoints if that ever stops being true.
	base := http.RoundTripper(http.DefaultTransport)
	if opts.HTTPClient != nil && opts.HTTPClient.Transport != nil {
		base = opts.HTTPClient.Transport
	}
	if e.API == "" || e.API == APIOpenAIChat {
		base = provider.NewCacheMarkTransport(base, opts.CacheTTL)
	}
	httpClient := &http.Client{Transport: NewTransport(e, base)}

	// Each dialect is told the session's idle deadline after it is built:
	// these three constructors take a finished client rather than the resolve
	// options the deadline lives in, and a route through a gateway is no less
	// able to go quiet than the direct path.
	switch e.API {
	case APIOpenAIResponses:
		inner := provider.NewOpenAIResponsesWith(httpClient, key, e.BaseURL, opts.Model, p.Name)
		inner.SetStreamIdle(opts.StreamIdleSeconds)
		return withDiscovery(e, &responsesProfile{OpenAIResponses: inner, endpoint: e, client: httpClient, decides: p.DeclaresDecisions}), nil
	case APIAnthropicMessage:
		inner := provider.NewAnthropicNamed(anthropic.NewClient(
			option.WithAPIKey(key),
			option.WithBaseURL(e.BaseURL),
			option.WithHTTPClient(httpClient),
		), opts.Model, p.Name, opts.CacheTTL)
		inner.SetStreamIdle(opts.StreamIdleSeconds)
		return &anthropicProfile{Anthropic: inner, name: p.Name}, nil
	default:
		cfg := openai.DefaultConfig(key)
		cfg.BaseURL = e.BaseURL
		cfg.HTTPClient = httpClient
		inner := provider.NewOpenAICompatNamed(openai.NewClientWithConfig(cfg), opts.Model, e.BaseURL, p.Name)
		inner.SetStreamIdle(opts.StreamIdleSeconds)
		return withDiscovery(e, &openAIProfile{OpenAICompat: inner, endpoint: e, client: httpClient, key: key, decides: p.DeclaresDecisions}), nil
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

// OffersDecisions and Decide are passed through: hiding the catalog is all
// this type is for.
func (n noDiscovery) OffersDecisions(model string) bool { return offersDecisions(n.Provider, model) }

func (n noDiscovery) Decide(ctx context.Context, req provider.DecisionRequest) (provider.DecisionResult, error) {
	return decide(ctx, n.Provider, req)
}

// openAIProfile is a profile-backed openai-chat provider. It inherits
// streaming and discovery from OpenAICompat, overriding discovery only when
// the gateway publishes its catalog somewhere else.
type openAIProfile struct {
	*provider.OpenAICompat
	endpoint Endpoint
	client   *http.Client
	// key is the endpoint's resolved key, and decides the profile's
	// declaration of which models this gateway serves the Decisions API
	// for — the only answer a gateway route has.
	key     string
	decides func(model string) bool
}

func (o *openAIProfile) OffersDecisions(model string) bool { return o.decides(model) }

// Decide sends a Decisions API request over the endpoint's own client, so
// the profile's headers and rewrite rules apply to it as they do to a turn.
func (o *openAIProfile) Decide(ctx context.Context, req provider.DecisionRequest) (provider.DecisionResult, error) {
	return provider.PostDecisions(ctx, o.client, o.endpoint.BaseURL, o.key, o.Name(), req)
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
	// decides is the profile's declaration, in place of the native
	// provider's floor: what OpenAI serves says nothing about a gateway.
	decides func(model string) bool
}

func (r *responsesProfile) OffersDecisions(model string) bool { return r.decides(model) }

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
