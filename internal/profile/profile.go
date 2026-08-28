// Package profile turns a TOML file into a working provider: base URL, auth,
// headers, a declared model catalog, and — the reason it exists — ordered
// rewrite rules that reshape the JSON on the wire.
//
// Private gateways are OpenAI-compatible in the sense that they accept the
// shape and return the shape, but each one has its own quirks: a parameter
// the upstream rejects, an id that must not be echoed back, a field that
// arrives missing. Those are per-deployment facts with no place in provider
// code, and they change without warning. A profile keeps them in the user's
// config, where fixing one is an edit rather than a release.
//
// One gateway is often several addresses. The Claude models answer on a
// dialect and a path of their own, the rest on the OpenAI-shaped root, and
// both are the same deployment behind the same key — so a profile is a
// provider with endpoints inside it (S-142). An [[endpoint]] overrides only
// what differs; everything it leaves unset it inherits, and a request routes
// to it by the model it names.
//
// Profiles live in <config-dir>/providers.toml — one file, a [[provider]]
// block each — or in <config-dir>/providers/<name>.toml, one profile per
// file. Both forms load, and the single file is read first.
//
//	[[provider]]
//	name        = "gateway"
//	api         = "openai-chat"
//	base_url    = "https://llm-gateway.internal/v1"
//	api_key_env = "GATEWAY_API_KEY"
//
//	  [provider.headers]
//	  X-Title = "shhh"
//
//	  [[provider.models]]
//	  id             = "gemini-3.1-pro"
//	  context_window = 1048576
//	  cost           = { input = 2.0, output = 12.0 }
//
//	  [[provider.endpoint]]
//	  match    = ["claude-*"]
//	  api      = "anthropic-messages"
//	  base_url = "https://llm-gateway.internal/anthropic"
package profile

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// API names the wire dialect a profile speaks. Both are backed by providers
// shhh already has; the profile supplies the endpoint and the quirks.
const (
	APIOpenAIChat       = "openai-chat"
	APIOpenAIResponses  = "openai-responses"
	APIAnthropicMessage = "anthropic-messages"
)

// Profile is one gateway: where it is, how to authenticate, what it hosts,
// and how its requests and responses differ from the vanilla format. Its own
// fields describe the default endpoint — the one a model routes to when no
// [[endpoint]] claims it — and are the values every endpoint inherits.
type Profile struct {
	// Name is the provider name to register — what `--provider` and
	// `provider.default` accept, and what the session displays.
	Name string `toml:"name"`
	// API is the wire dialect: "openai-chat" (default), "openai-responses",
	// or "anthropic-messages".
	API string `toml:"api"`
	// BaseURL is the endpoint root, including any version or dialect path
	// segment the gateway expects ("…/v1", "…/anthropic"). It is required
	// even when every model is routed, because it is the answer to where a
	// model no endpoint claims should go.
	BaseURL string `toml:"base_url"`
	// APIKey is a literal key; APIKeyEnv names an environment variable to
	// read it from instead, which is how a profile stays shareable.
	APIKey    string `toml:"api_key"`
	APIKeyEnv string `toml:"api_key_env"`
	// Headers are added to every request.
	Headers map[string]string `toml:"headers"`
	// ModelsPath overrides the model-discovery endpoint for gateways that
	// publish their catalog somewhere other than {base_url}/models. An
	// absolute path ("/v1/models/simple") is resolved against the host.
	ModelsPath string `toml:"models_path"`
	// DiscoveryDisabled turns off the catalog query — the GET
	// {base_url}/models the /model picker makes — so the picker offers the
	// declared models and nothing else. A pointer because it is inherited:
	// nil means "not said here", which is not the same as false.
	DiscoveryDisabled *bool `toml:"discovery_disabled"`
	// Models declares what the gateway hosts, and the metadata its catalog
	// endpoint typically omits. Ids listed here seed the /model picker even
	// before discovery runs; metadata missing here falls back to the public
	// pricing table.
	Models []Model `toml:"models"`
	// Rewrite holds the quirk rules, applied in file order. They apply to
	// every endpoint, ahead of that endpoint's own rules.
	Rewrite []Rule `toml:"rewrite"`
	// Endpoints are the addresses inside this provider that differ from the
	// default one — another dialect, another path, another set of quirks.
	// A request routes to the endpoint that declares its model, or whose
	// `match` glob claims it; anything unclaimed goes to the default.
	Endpoints []Endpoint `toml:"endpoint"`

	// Path is the file this profile was read from (for diagnostics).
	Path string `toml:"-"`
}

// Endpoint is one address within a profile. Every field it leaves unset it
// inherits from the profile, so an endpoint that only moves the Claude models
// onto the Messages dialect says exactly that and nothing more.
type Endpoint struct {
	// Label names the endpoint in `shhh providers` and in errors. It
	// defaults to the base URL, which is usually the more useful of the two.
	Label string `toml:"label"`
	// Match are globs against the requested model ("claude-*"), tried in
	// file order after every endpoint's declared model ids. An endpoint that
	// declares its models needs no glob.
	Match []string `toml:"match"`

	// The rest mirror the profile's own fields, and override them.
	API               string            `toml:"api"`
	BaseURL           string            `toml:"base_url"`
	APIKey            string            `toml:"api_key"`
	APIKeyEnv         string            `toml:"api_key_env"`
	Headers           map[string]string `toml:"headers"`
	ModelsPath        string            `toml:"models_path"`
	DiscoveryDisabled *bool             `toml:"discovery_disabled"`
	Models            []Model           `toml:"models"`
	Rewrite           []Rule            `toml:"rewrite"`
}

// DiscoveryOff reports whether this endpoint's catalog query is turned off,
// leaving the declared models as the whole answer.
func (e Endpoint) DiscoveryOff() bool {
	return e.DiscoveryDisabled != nil && *e.DiscoveryDisabled
}

// Model is a declared model and the metadata a catalog endpoint that returns
// bare ids can't give us.
type Model struct {
	ID string `toml:"id"`
	// ContextWindow is the model's input ceiling, feeding the context meter.
	ContextWindow int64 `toml:"context_window"`
	// MaxTokens is the model's output ceiling. It is metadata only — shhh
	// does not add it to requests; a gateway that needs it set can get it
	// from a `set-default` rewrite rule.
	MaxTokens int64 `toml:"max_tokens"`
	// Cost is in dollars per million tokens, the unit model cards publish.
	Cost Cost `toml:"cost"`
}

// Cost is per-million-token pricing. CacheRead and CacheWrite are accepted
// and reported, but the spend meter does not use them yet: shhh's usage
// accounting has no cached-token counters to bill against.
type Cost struct {
	Input      float64 `toml:"input"`
	Output     float64 `toml:"output"`
	CacheRead  float64 `toml:"cache_read"`
	CacheWrite float64 `toml:"cache_write"`
}

// HasPricing reports whether the entry carries token prices.
func (c Cost) HasPricing() bool { return c.Input != 0 || c.Output != 0 }

// Key returns the resolved API key: the literal one, or the named
// environment variable's value.
func (p Profile) Key() string { return resolveKey(p.APIKey, p.APIKeyEnv) }

// Key returns the endpoint's resolved API key. An endpoint that named
// neither has already inherited the profile's, so this is the whole answer.
func (e Endpoint) Key() string { return resolveKey(e.APIKey, e.APIKeyEnv) }

func resolveKey(literal, env string) string {
	if literal != "" {
		return literal
	}
	if env != "" {
		return os.Getenv(env)
	}
	return ""
}

// Routes returns the profile's endpoints with inheritance applied, the
// default one first. Every request goes to one of these, and Route decides
// which.
func (p Profile) Routes() []Endpoint {
	routes := make([]Endpoint, 0, len(p.Endpoints)+1)
	routes = append(routes, p.defaultRoute())
	for _, e := range p.Endpoints {
		routes = append(routes, p.inherit(e))
	}
	return routes
}

// defaultRoute is the endpoint the profile's own fields describe: where a
// model that no [[endpoint]] claims is sent.
func (p Profile) defaultRoute() Endpoint {
	api := p.API
	if api == "" {
		api = APIOpenAIChat
	}
	return Endpoint{
		Label:             "default",
		API:               api,
		BaseURL:           p.BaseURL,
		APIKey:            p.APIKey,
		APIKeyEnv:         p.APIKeyEnv,
		Headers:           p.Headers,
		ModelsPath:        p.ModelsPath,
		DiscoveryDisabled: p.DiscoveryDisabled,
		Models:            p.Models,
		Rewrite:           p.Rewrite,
	}
}

// inherit fills an endpoint's unset fields from the profile. Headers merge —
// the endpoint's own win on a collision — and the profile's rewrite rules run
// ahead of the endpoint's, so a rule that is true of the whole gateway is
// written once.
func (p Profile) inherit(e Endpoint) Endpoint {
	out := e
	if out.API == "" {
		out.API = p.API
	}
	if out.API == "" {
		out.API = APIOpenAIChat
	}
	if out.BaseURL == "" {
		out.BaseURL = p.BaseURL
	}
	if out.APIKey == "" && out.APIKeyEnv == "" {
		out.APIKey, out.APIKeyEnv = p.APIKey, p.APIKeyEnv
	}
	if out.ModelsPath == "" {
		out.ModelsPath = p.ModelsPath
	}
	if out.DiscoveryDisabled == nil {
		out.DiscoveryDisabled = p.DiscoveryDisabled
	}
	out.Headers = mergeHeaders(p.Headers, e.Headers)
	if len(p.Rewrite) > 0 {
		out.Rewrite = append(append([]Rule(nil), p.Rewrite...), e.Rewrite...)
	}
	if out.Label == "" {
		out.Label = out.BaseURL
	}
	return out
}

func mergeHeaders(base, over map[string]string) map[string]string {
	if len(base) == 0 && len(over) == 0 {
		return nil
	}
	out := make(map[string]string, len(base)+len(over))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range over {
		out[k] = v
	}
	return out
}

// Route picks the endpoint a model belongs to. A declared id is the strongest
// claim — it is the user naming this model and this address in one breath —
// and a `match` glob is the fallback for the models nobody enumerated.
// Anything unclaimed goes to the default route, which is what makes a profile
// with no endpoints behave exactly as it did before endpoints existed.
func (p Profile) Route(model string) Endpoint {
	routes := p.Routes()
	if model != "" {
		for _, r := range routes {
			for _, m := range r.Models {
				if m.ID == model {
					return r
				}
			}
		}
		for _, r := range routes[1:] {
			for _, glob := range r.Match {
				if ok, err := path.Match(glob, model); err == nil && ok {
					return r
				}
			}
		}
	}
	return routes[0]
}

// ModelIDs are every declared model id, the default endpoint's first and each
// endpoint's after it, in file order.
func (p Profile) ModelIDs() []string {
	var ids []string
	seen := map[string]bool{}
	for _, r := range p.Routes() {
		for _, m := range r.Models {
			if seen[m.ID] {
				continue
			}
			seen[m.ID] = true
			ids = append(ids, m.ID)
		}
	}
	return ids
}

// Dirs are the profile directories, in config search order: a "providers"
// directory beside each config file location.
func Dirs(configPaths []string) []string {
	seen := map[string]bool{}
	var dirs []string
	for _, p := range configPaths {
		dir := filepath.Join(filepath.Dir(p), "providers")
		if !seen[dir] {
			seen[dir] = true
			dirs = append(dirs, dir)
		}
	}
	return dirs
}

// Files are the single-file profile locations, one beside each profile
// directory: <config-dir>/providers.toml. It is the file that holds every
// provider, and it is read before the directory beside it.
func Files(dirs []string) []string {
	files := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		files = append(files, dir+".toml")
	}
	return files
}

// Load reads every profile in the given directories, and the providers.toml
// beside each. A file that fails to parse or validate is reported and
// skipped — one bad profile must not take the session down with it. The
// first source holding a given profile name wins, matching the config search
// order, with providers.toml ahead of the directory beside it.
func Load(dirs []string) ([]Profile, []error) {
	var profiles []Profile
	seen := map[string]bool{}

	keep := func(found []Profile) {
		for _, p := range found {
			if seen[p.Name] {
				continue
			}
			seen[p.Name] = true
			profiles = append(profiles, p)
		}
	}

	paths, errs := Sources(dirs)
	for _, path := range paths {
		found, err := LoadFile(path)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		keep(found)
	}
	return profiles, errs
}

// Sources lists every profile file that exists, in load order: for each
// config directory, the providers.toml first and then providers/*.toml by
// name. It is what Load reads and what `shhh providers migrate` folds
// together, so both agree on what is out there.
func Sources(dirs []string) ([]string, []error) {
	var paths []string
	var errs []error
	for _, dir := range dirs {
		if file := dir + ".toml"; exists(file) {
			paths = append(paths, file)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			// A missing profile directory is the normal case.
			if !os.IsNotExist(err) {
				errs = append(errs, fmt.Errorf("%s: %w", dir, err))
			}
			continue
		}
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".toml") {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		for _, name := range names {
			paths = append(paths, filepath.Join(dir, name))
		}
	}
	return paths, errs
}

func exists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// LoadFile reads and validates one profile file, in either form: a file of
// [[provider]] blocks, or a single profile written at the top level.
func LoadFile(path string) ([]Profile, error) {
	var single Profile
	if _, err := toml.DecodeFile(path, &single); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var multi struct {
		Providers []Profile `toml:"provider"`
	}
	if _, err := toml.DecodeFile(path, &multi); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	if len(multi.Providers) == 0 {
		single.Path = path
		if single.Name == "" {
			// A nameless profile takes the file's name, so gateway.toml is
			// enough. A file of [[provider]] blocks has no such fallback:
			// one filename cannot name several providers.
			single.Name = strings.TrimSuffix(filepath.Base(path), ".toml")
		}
		if err := single.Validate(); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		return []Profile{single}, nil
	}
	if single.declaresProvider() {
		return nil, fmt.Errorf("%s: set [[provider]] blocks or one top-level provider, not both", path)
	}

	out := make([]Profile, 0, len(multi.Providers))
	named := map[string]bool{}
	for i := range multi.Providers {
		p := multi.Providers[i]
		p.Path = path
		if p.Name == "" {
			return nil, fmt.Errorf("%s: provider[%d]: name is required — a file of several providers cannot borrow the filename", path, i)
		}
		if named[p.Name] {
			return nil, fmt.Errorf("%s: provider %q is declared twice", path, p.Name)
		}
		named[p.Name] = true
		if err := p.Validate(); err != nil {
			return nil, fmt.Errorf("%s: provider %q: %w", path, p.Name, err)
		}
		out = append(out, p)
	}
	return out, nil
}

// declaresProvider reports whether anything was written at the top level of
// the file — the single-profile form. It exists to catch the half-migrated
// file, where a [[provider]] block was added above settings that are now
// silently ignored.
func (p Profile) declaresProvider() bool {
	return p.Name != "" || p.API != "" || p.BaseURL != "" || p.APIKey != "" ||
		p.APIKeyEnv != "" || p.ModelsPath != "" || p.DiscoveryDisabled != nil ||
		len(p.Headers) > 0 || len(p.Models) > 0 || len(p.Rewrite) > 0 ||
		len(p.Endpoints) > 0
}

// Validate checks the profile is usable, naming the field at fault. It is
// deliberately strict: a typo in a rewrite rule that silently did nothing
// would be worse than a refusal at load.
func (p *Profile) Validate() error {
	if p.Name == "" {
		return fmt.Errorf("name is required")
	}
	if p.BaseURL == "" {
		return fmt.Errorf("base_url is required — it is where a model no [[endpoint]] claims is sent")
	}
	if err := validateAPI(&p.API); err != nil {
		return err
	}
	if p.API == "" {
		// The profile's dialect is the one every endpoint inherits, so it is
		// settled here. An endpoint's own stays empty until inheritance runs:
		// defaulting it would override the profile it meant to follow.
		p.API = APIOpenAIChat
	}
	if p.APIKey != "" && p.APIKeyEnv != "" {
		return fmt.Errorf("set api_key or api_key_env, not both")
	}
	for i, m := range p.Models {
		if m.ID == "" {
			return fmt.Errorf("models[%d]: id is required", i)
		}
	}
	for i := range p.Rewrite {
		if err := p.Rewrite[i].validate(); err != nil {
			return fmt.Errorf("rewrite[%d]: %w", i, err)
		}
	}
	for i := range p.Endpoints {
		if err := p.Endpoints[i].validate(); err != nil {
			return fmt.Errorf("endpoint[%d]: %w", i, err)
		}
	}
	return p.validateRouting()
}

// validateRouting refuses a model claimed by two endpoints. Either could be
// the one meant, and picking silently would send a session's traffic to an
// address the user did not choose.
func (p Profile) validateRouting() error {
	owner := map[string]string{}
	for _, r := range p.Routes() {
		for _, m := range r.Models {
			if prev, ok := owner[m.ID]; ok {
				return fmt.Errorf("model %q is declared by both %s and %s — declare it once", m.ID, prev, r.Label)
			}
			owner[m.ID] = r.Label
		}
	}
	return nil
}

func (e *Endpoint) validate() error {
	if err := validateAPI(&e.API); err != nil {
		return err
	}
	if e.APIKey != "" && e.APIKeyEnv != "" {
		return fmt.Errorf("set api_key or api_key_env, not both")
	}
	for i, glob := range e.Match {
		if glob == "" {
			return fmt.Errorf("match[%d] is empty", i)
		}
		if _, err := path.Match(glob, ""); err != nil {
			return fmt.Errorf("match[%d] %q is not a valid pattern: %w", i, glob, err)
		}
	}
	if len(e.Match) == 0 && len(e.Models) == 0 {
		return fmt.Errorf("declare `match` globs or `models` — an endpoint no model reaches is never used")
	}
	for i, m := range e.Models {
		if m.ID == "" {
			return fmt.Errorf("models[%d]: id is required", i)
		}
	}
	for i := range e.Rewrite {
		if err := e.Rewrite[i].validate(); err != nil {
			return fmt.Errorf("rewrite[%d]: %w", i, err)
		}
	}
	return nil
}

// validateAPI checks a dialect name, defaulting an empty one. An endpoint's
// empty API is filled from the profile later; defaulting it here to the chat
// dialect would override the profile it was meant to inherit, so an endpoint
// leaves it empty and validateAPI is called on the inherited value too.
func validateAPI(api *string) error {
	switch *api {
	case "", APIOpenAIChat, APIOpenAIResponses, APIAnthropicMessage:
		return nil
	default:
		return fmt.Errorf("api %q is not supported (want %q, %q, or %q)",
			*api, APIOpenAIChat, APIOpenAIResponses, APIAnthropicMessage)
	}
}
