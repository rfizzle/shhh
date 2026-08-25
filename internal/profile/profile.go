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
// A file lives in <config-dir>/providers/<name>.toml:
//
//	name        = "gateway"
//	api         = "openai-chat"
//	base_url    = "https://llm-gateway.internal/v1"
//	api_key_env = "GATEWAY_API_KEY"
//
//	[headers]
//	X-Title = "shhh"
//
//	[[models]]
//	id             = "gemini-3.1-pro"
//	context_window = 1048576
//	cost           = { input = 2.0, output = 12.0 }
//
//	[[rewrite]]
//	when  = { model = "gemini-*" }
//	op    = "cut-at"
//	path  = "messages[].tool_calls[].id"
//	value = "__thought__"
package profile

import (
	"fmt"
	"os"
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
// and how its requests and responses differ from the vanilla format.
type Profile struct {
	// Name is the provider name to register — what `--provider` and
	// `provider.default` accept, and what the session displays.
	Name string `toml:"name"`
	// API is the wire dialect: "openai-chat" (default), "openai-responses",
	// or "anthropic-messages".
	API string `toml:"api"`
	// BaseURL is the endpoint root, including any version or dialect path
	// segment the gateway expects ("…/v1", "…/anthropic").
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
	// Models declares what the gateway hosts, and the metadata its catalog
	// endpoint typically omits. Ids listed here seed the /model picker even
	// before discovery runs; metadata missing here falls back to the public
	// pricing table.
	Models []Model `toml:"models"`
	// Rewrite holds the quirk rules, applied in file order.
	Rewrite []Rule `toml:"rewrite"`

	// Path is the file this profile was read from (for diagnostics).
	Path string `toml:"-"`
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
func (p Profile) Key() string {
	if p.APIKey != "" {
		return p.APIKey
	}
	if p.APIKeyEnv != "" {
		return os.Getenv(p.APIKeyEnv)
	}
	return ""
}

// ModelIDs are the declared model ids, in file order.
func (p Profile) ModelIDs() []string {
	ids := make([]string, 0, len(p.Models))
	for _, m := range p.Models {
		ids = append(ids, m.ID)
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

// Load reads every profile in the given directories. A file that fails to
// parse or validate is reported and skipped — one bad profile must not take
// the session down with it. The first directory holding a given profile name
// wins, matching the config search order.
func Load(dirs []string) ([]Profile, []error) {
	var profiles []Profile
	var errs []error
	seen := map[string]bool{}

	for _, dir := range dirs {
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
			path := filepath.Join(dir, name)
			p, err := LoadFile(path)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			if seen[p.Name] {
				continue
			}
			seen[p.Name] = true
			profiles = append(profiles, p)
		}
	}
	return profiles, errs
}

// LoadFile reads and validates one profile file.
func LoadFile(path string) (Profile, error) {
	var p Profile
	if _, err := toml.DecodeFile(path, &p); err != nil {
		return Profile{}, fmt.Errorf("%s: %w", path, err)
	}
	p.Path = path
	if p.Name == "" {
		// A nameless profile takes the file's name, so gateway.toml is enough.
		p.Name = strings.TrimSuffix(filepath.Base(path), ".toml")
	}
	if err := p.Validate(); err != nil {
		return Profile{}, fmt.Errorf("%s: %w", path, err)
	}
	return p, nil
}

// Validate checks the profile is usable, naming the field at fault. It is
// deliberately strict: a typo in a rewrite rule that silently did nothing
// would be worse than a refusal at load.
func (p *Profile) Validate() error {
	if p.Name == "" {
		return fmt.Errorf("name is required")
	}
	if p.BaseURL == "" {
		return fmt.Errorf("base_url is required")
	}
	switch p.API {
	case "":
		p.API = APIOpenAIChat
	case APIOpenAIChat, APIOpenAIResponses, APIAnthropicMessage:
	default:
		return fmt.Errorf("api %q is not supported (want %q, %q, or %q)",
			p.API, APIOpenAIChat, APIOpenAIResponses, APIAnthropicMessage)
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
	return nil
}
