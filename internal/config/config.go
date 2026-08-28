package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Provider   ProviderConfig   `toml:"provider"`
	Behavior   BehaviorConfig   `toml:"behavior"`
	Sandbox    SandboxConfig    `toml:"sandbox"`
	Web        WebConfig        `toml:"web"`
	LSP        LSPConfig        `toml:"lsp"`
	Appearance AppearanceConfig `toml:"appearance"`
	History    HistoryConfig    `toml:"history"`
	Agents     AgentsConfig     `toml:"agents"`
}

// LSPConfig tunes the language-server integration (S-071) `shhh code` uses
// for after-edit diagnostics and the definition/references tools. Servers are
// auto-detected on PATH (gopls, rust-analyzer, typescript-language-server,
// pyright); none found means the integration is a clean no-op.
type LSPConfig struct {
	// Disabled turns the LSP integration off entirely: no servers started, no
	// navigation tools registered, no after-edit diagnostics.
	Disabled bool `toml:"disabled"`
	// RequestTimeoutSeconds bounds each server request, including the
	// initialize handshake (default 15).
	RequestTimeoutSeconds int `toml:"request_timeout_seconds"`
	// DiagnosticsTimeoutSeconds is how long an applied edit waits for fresh
	// diagnostics before giving up quietly (default 3).
	DiagnosticsTimeoutSeconds int `toml:"diagnostics_timeout_seconds"`
}

// WebConfig tunes the guarded web tools (S-066) `shhh code` registers.
type WebConfig struct {
	// AllowPrivate permits fetching private, loopback, link-local, and CGNAT
	// addresses (for intranet or local-dev targets) and lifts the 80/443 port
	// allowlist. Cloud metadata endpoints stay blocked regardless.
	AllowPrivate bool `toml:"allow_private"`
	// FetchMaxBytes is the download ceiling per fetch (default 2 MiB).
	FetchMaxBytes int64 `toml:"fetch_max_bytes"`
	// FetchTimeoutSeconds bounds one fetch including redirects and the body
	// read (default 30).
	FetchTimeoutSeconds int `toml:"fetch_timeout_seconds"`
	// CacheTTLMinutes is how long a cached response stays fresh (default 60).
	CacheTTLMinutes int `toml:"cache_ttl_minutes"`
	// SearchProvider names the web_search backend; "brave" (the default) is
	// the only provider so far.
	SearchProvider string `toml:"search_provider"`
	// SearchAPIKey enables the web_search tool; without it the tool is not
	// registered.
	SearchAPIKey string `toml:"search_api_key"`
}

// SandboxConfig tunes process containment for agent-executed commands
// (S-062). The built-in deny mask (~/.ssh, ~/.aws, ~/.config/gh, shhh's own
// config and state dirs) is deliberately not configurable — it cannot be
// disabled, only extended.
type SandboxConfig struct {
	// Profile is "workspace" (network preserved, the default) or
	// "workspace-netless".
	Profile string `toml:"profile"`
	// DenyExtra paths join the built-in deny mask; contained commands see
	// them as empty.
	DenyExtra []string `toml:"deny_extra"`
	// WriteExtra paths are writable inside containment, in addition to the
	// workspace, scratch, and toolchain caches.
	WriteExtra []string `toml:"write_extra"`
	// ContainerEngine forces the container-sandbox engine ("podman" or
	// "docker"); empty auto-detects, preferring a rootless engine.
	ContainerEngine string `toml:"container_engine"`
	// ContainerImage is the digest-pinned image (name@sha256:…) sandbox
	// containers run; container sandboxes are unavailable until it is set.
	ContainerImage string `toml:"container_image"`
	// ImageAllowlist, when set, restricts sandbox images to these
	// digest-pinned references.
	ImageAllowlist []string `toml:"image_allowlist"`
	// ContainerMemory / ContainerCPUs / ContainerPids are the sandbox
	// resource ceilings (defaults: 2g, 2, 256).
	ContainerMemory string `toml:"container_memory"`
	ContainerCPUs   string `toml:"container_cpus"`
	ContainerPids   int    `toml:"container_pids"`
	// ContainerTTLHours is how long a sandbox container may live before
	// startup reconciliation reaps it (default 24).
	ContainerTTLHours int `toml:"container_ttl_hours"`
	// RequireIsolation refuses sandbox creation below this verified level
	// ("process", "container", or "vm"); an unverifiable requirement fails
	// creation instead of downgrading. Empty requires none.
	RequireIsolation string `toml:"require_isolation"`
}

type ProviderConfig struct {
	Default string `toml:"default"`
	Model   string `toml:"model"`
	APIKey  string `toml:"api_key"`
	BaseURL string `toml:"base_url"`
	Name    string `toml:"name"`
	// Reasoning is the level of thinking sessions start on: "off", "low",
	// "medium" or "high" (S-139). Empty means off, which is what every
	// session did before the setting existed — no reasoning field is sent at
	// all, so models without the knob are unaffected.
	Reasoning string `toml:"reasoning"`
}

type BehaviorConfig struct {
	SilentMode       bool   `toml:"silent_mode"`
	Shell            string `toml:"shell"`
	ContextMaxTokens int    `toml:"context_max_tokens"`
	// MaxToolRounds caps the consecutive tool-call rounds one turn may take:
	// zero leaves agent.DefaultMaxToolRounds in place, and any negative
	// removes the cap for every run in scope — the config-file form of
	// `--max-rounds 0`, for a machine that only ever runs unattended.
	MaxToolRounds     int    `toml:"max_tool_rounds"`
	SafetyWarnings    *bool  `toml:"safety_warnings"`
	SystemPromptExtra string `toml:"system_prompt_extra"`
	// CommandAllowlist entries auto-approve matching agent commands in chat
	// sessions ("go test" approves "go test ./..."); safety-flagged commands
	// always prompt regardless. Empty (the default) means every command asks.
	CommandAllowlist []string `toml:"command_allowlist"`
	// ReadOnlyCommands extends the built-in inspection allowlist that runs
	// without prompting in every mode (agent.ReadOnlyCommands). Entries are
	// the user's own call: they skip the built-in flag guards.
	ReadOnlyCommands []string `toml:"read_only_commands"`
	// ReadOnlyAuto controls whether the built-in inspection allowlist
	// auto-runs (default true). Setting it false makes reads prompt like
	// anything else; plan mode still inspects.
	ReadOnlyAuto *bool `toml:"read_only_auto"`
	// DefaultMode is the permission mode agent sessions start in: "manual",
	// "accept-edits", "auto", or "plan". Empty means manual (everything
	// prompts).
	DefaultMode string `toml:"default_mode"`
	// ModeCycle overrides the Shift+Tab mode order (same names as
	// DefaultMode). Empty means manual → accept-edits → auto → plan.
	ModeCycle []string `toml:"mode_cycle"`
	// ClassifierModel is the model auto mode's permission classifier uses
	// (S-060). Empty means the session model.
	ClassifierModel string `toml:"classifier_model"`
	// ClassifierTimeoutSeconds bounds each classifier request (default 30).
	ClassifierTimeoutSeconds int `toml:"classifier_timeout_seconds"`
	// ClassifierMaxTokens caps the classifier's response (default 1024).
	ClassifierMaxTokens int `toml:"classifier_max_tokens"`
	// ClassifierRetries is how many extra attempts an invalid or failed
	// classifier response gets before failing closed (default 1).
	ClassifierRetries int `toml:"classifier_retries"`
	// MemoryDisabled turns off durable memory (S-070): no memories are
	// injected into the system prompt and the remember tool is not registered.
	MemoryDisabled bool `toml:"memory_disabled"`
	// MemoryMaxEntries caps how many memories are injected per session
	// (default 20).
	MemoryMaxEntries int `toml:"memory_max_entries"`
	// MemoryMaxTokens is the hard token budget for the injected memory block
	// (default 1200).
	MemoryMaxTokens int `toml:"memory_max_tokens"`
}

// AgentsConfig configures sub-agent (S-068) defaults: which model children
// run and per-role overrides. A model of "inherit" (or empty) means the
// session's own model, so one setting moves parent and children together.
type AgentsConfig struct {
	// Model is the default model for every sub-agent.
	Model string `toml:"model"`
	// Profiles override per role ("researcher", "writer"), keyed by role name.
	Profiles map[string]AgentProfile `toml:"profiles"`
	// MaxConcurrent bounds simultaneously running children (default 3).
	MaxConcurrent int `toml:"max_concurrent"`
}

// AgentProfile is one role's overrides.
type AgentProfile struct {
	Model string `toml:"model"`
}

// InheritModel is the model name that defers to the session model.
const InheritModel = "inherit"

// AgentModel resolves the model for a sub-agent role: the role profile wins,
// then the agents-wide default, then the session model. "inherit" at any
// level falls through to the session model.
func (c Config) AgentModel(role, sessionModel string) string {
	for _, candidate := range []string{c.Agents.Profiles[role].Model, c.Agents.Model} {
		candidate = strings.TrimSpace(candidate)
		switch {
		case candidate == "":
			continue // unset: fall through to the next level
		case strings.EqualFold(candidate, InheritModel):
			// An explicit "inherit" pins that level to the session model
			// rather than deferring to a broader default.
			return sessionModel
		default:
			return candidate
		}
	}
	return sessionModel
}

type AppearanceConfig struct {
	AccentColor string `toml:"accent_color"`
	// Mouse turns terminal mouse reporting on. It is off by default because
	// reporting costs the terminal its own click-drag selection, and a
	// transcript is text people copy out of — while scrolling it already has
	// pgup/pgdn, ctrl+e and j/k. The wheel is the thing with a substitute, so
	// the wheel is the thing you opt into (ctrl+x, or `/ui mouse on`).
	Mouse bool `toml:"mouse"`
}

type HistoryConfig struct {
	RetentionDays int `toml:"retention_days"`
}

const DefaultRetentionDays = 90

const DefaultContextMaxTokens = 8000

const (
	DefaultMemoryMaxEntries = 20
	DefaultMemoryMaxTokens  = 1200
)

// ReadOnlyAutoEnabled reports whether the built-in inspection allowlist
// auto-runs (the default).
func (c Config) ReadOnlyAutoEnabled() bool {
	if c.Behavior.ReadOnlyAuto == nil {
		return true
	}
	return *c.Behavior.ReadOnlyAuto
}

func (c Config) SafetyWarningsEnabled() bool {
	if c.Behavior.SafetyWarnings == nil {
		return true
	}
	return *c.Behavior.SafetyWarnings
}

func (c Config) EffectiveContextMaxTokens() int {
	if c.Behavior.ContextMaxTokens > 0 {
		return c.Behavior.ContextMaxTokens
	}
	return DefaultContextMaxTokens
}

func (c Config) EffectiveMemoryMaxEntries() int {
	if c.Behavior.MemoryMaxEntries > 0 {
		return c.Behavior.MemoryMaxEntries
	}
	return DefaultMemoryMaxEntries
}

func (c Config) EffectiveMemoryMaxTokens() int {
	if c.Behavior.MemoryMaxTokens > 0 {
		return c.Behavior.MemoryMaxTokens
	}
	return DefaultMemoryMaxTokens
}

func (c Config) EffectiveRetentionDays() int {
	if c.History.RetentionDays > 0 {
		return c.History.RetentionDays
	}
	return DefaultRetentionDays
}

// ProviderAPIKey returns the configured API key.
func (c Config) ProviderAPIKey() string {
	return c.Provider.APIKey
}

// ProviderBaseURL returns the configured base URL.
func (c Config) ProviderBaseURL() string {
	return c.Provider.BaseURL
}

// ProviderDisplayName returns the configured custom display name.
func (c Config) ProviderDisplayName() string {
	return c.Provider.Name
}

func Load() (Config, error) {
	return LoadFrom(Paths()...)
}

func LoadFrom(paths ...string) (Config, error) {
	var cfg Config
	for _, p := range paths {
		if _, err := toml.DecodeFile(p, &cfg); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return Config{}, err
		}
		return cfg, nil
	}
	return Config{}, nil
}

func Set(cfg *Config, key, value string) error {
	switch key {
	case "provider.default":
		cfg.Provider.Default = value
	case "provider.model":
		cfg.Provider.Model = value
	case "provider.api_key":
		cfg.Provider.APIKey = value
	case "provider.base_url":
		cfg.Provider.BaseURL = value
	case "provider.name":
		cfg.Provider.Name = value
	case "provider.reasoning":
		cfg.Provider.Reasoning = value
	case "behavior.silent_mode":
		cfg.Behavior.SilentMode = value == "true"
	case "behavior.shell":
		cfg.Behavior.Shell = value
	case "behavior.context_max_tokens":
		n := 0
		fmt.Sscanf(value, "%d", &n)
		cfg.Behavior.ContextMaxTokens = n
	case "appearance.mouse":
		cfg.Appearance.Mouse = value == "true"
	case "behavior.max_tool_rounds":
		n := 0
		fmt.Sscanf(value, "%d", &n)
		cfg.Behavior.MaxToolRounds = n
	case "behavior.safety_warnings":
		v := value == "true"
		cfg.Behavior.SafetyWarnings = &v
	case "behavior.system_prompt_extra":
		cfg.Behavior.SystemPromptExtra = value
	case "behavior.command_allowlist":
		cfg.Behavior.CommandAllowlist = splitList(value)
	case "behavior.read_only_commands":
		cfg.Behavior.ReadOnlyCommands = splitList(value)
	case "behavior.read_only_auto":
		v := value == "true"
		cfg.Behavior.ReadOnlyAuto = &v
	case "agents.model":
		cfg.Agents.Model = value
	case "agents.max_concurrent":
		n := 0
		fmt.Sscanf(value, "%d", &n)
		cfg.Agents.MaxConcurrent = n
	case "agents.researcher_model", "agents.writer_model":
		role := strings.TrimSuffix(strings.TrimPrefix(key, "agents."), "_model")
		if cfg.Agents.Profiles == nil {
			cfg.Agents.Profiles = map[string]AgentProfile{}
		}
		p := cfg.Agents.Profiles[role]
		p.Model = value
		cfg.Agents.Profiles[role] = p
	case "behavior.default_mode":
		cfg.Behavior.DefaultMode = value
	case "behavior.mode_cycle":
		cfg.Behavior.ModeCycle = splitList(value)
	case "behavior.classifier_model":
		cfg.Behavior.ClassifierModel = value
	case "behavior.classifier_timeout_seconds":
		n := 0
		fmt.Sscanf(value, "%d", &n)
		cfg.Behavior.ClassifierTimeoutSeconds = n
	case "behavior.classifier_max_tokens":
		n := 0
		fmt.Sscanf(value, "%d", &n)
		cfg.Behavior.ClassifierMaxTokens = n
	case "behavior.classifier_retries":
		n := 0
		fmt.Sscanf(value, "%d", &n)
		cfg.Behavior.ClassifierRetries = n
	case "behavior.memory_disabled":
		cfg.Behavior.MemoryDisabled = value == "true"
	case "behavior.memory_max_entries":
		n := 0
		fmt.Sscanf(value, "%d", &n)
		cfg.Behavior.MemoryMaxEntries = n
	case "behavior.memory_max_tokens":
		n := 0
		fmt.Sscanf(value, "%d", &n)
		cfg.Behavior.MemoryMaxTokens = n
	case "sandbox.profile":
		cfg.Sandbox.Profile = value
	case "sandbox.deny_extra":
		cfg.Sandbox.DenyExtra = splitList(value)
	case "sandbox.write_extra":
		cfg.Sandbox.WriteExtra = splitList(value)
	case "sandbox.container_engine":
		cfg.Sandbox.ContainerEngine = value
	case "sandbox.container_image":
		cfg.Sandbox.ContainerImage = value
	case "sandbox.image_allowlist":
		cfg.Sandbox.ImageAllowlist = splitList(value)
	case "sandbox.container_memory":
		cfg.Sandbox.ContainerMemory = value
	case "sandbox.container_cpus":
		cfg.Sandbox.ContainerCPUs = value
	case "sandbox.container_pids":
		n := 0
		fmt.Sscanf(value, "%d", &n)
		cfg.Sandbox.ContainerPids = n
	case "sandbox.container_ttl_hours":
		n := 0
		fmt.Sscanf(value, "%d", &n)
		cfg.Sandbox.ContainerTTLHours = n
	case "sandbox.require_isolation":
		cfg.Sandbox.RequireIsolation = value
	case "web.allow_private":
		cfg.Web.AllowPrivate = value == "true"
	case "web.fetch_max_bytes":
		var n int64
		fmt.Sscanf(value, "%d", &n)
		cfg.Web.FetchMaxBytes = n
	case "web.fetch_timeout_seconds":
		n := 0
		fmt.Sscanf(value, "%d", &n)
		cfg.Web.FetchTimeoutSeconds = n
	case "web.cache_ttl_minutes":
		n := 0
		fmt.Sscanf(value, "%d", &n)
		cfg.Web.CacheTTLMinutes = n
	case "web.search_provider":
		cfg.Web.SearchProvider = value
	case "web.search_api_key":
		cfg.Web.SearchAPIKey = value
	case "lsp.disabled":
		cfg.LSP.Disabled = value == "true"
	case "lsp.request_timeout_seconds":
		n := 0
		fmt.Sscanf(value, "%d", &n)
		cfg.LSP.RequestTimeoutSeconds = n
	case "lsp.diagnostics_timeout_seconds":
		n := 0
		fmt.Sscanf(value, "%d", &n)
		cfg.LSP.DiagnosticsTimeoutSeconds = n
	case "appearance.accent_color":
		cfg.Appearance.AccentColor = value
	case "history.retention_days":
		n := 0
		fmt.Sscanf(value, "%d", &n)
		cfg.History.RetentionDays = n
	default:
		return fmt.Errorf("unknown config key: %s", key)
	}
	return nil
}

// splitList parses a comma-separated config value into its non-empty,
// trimmed entries; an empty value clears the list.
func splitList(value string) []string {
	var list []string
	for _, part := range strings.Split(value, ",") {
		if p := strings.TrimSpace(part); p != "" {
			list = append(list, p)
		}
	}
	return list
}

func Save(cfg Config) error {
	p := WritePath()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	f, err := os.Create(p)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(cfg)
}

func WritePath() string {
	paths := Paths()
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if len(paths) > 0 {
		return paths[0]
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "shhh", "config.toml")
}

// Paths returns config file paths in search order (highest priority first).
func Paths() []string {
	var out []string
	if runtime.GOOS == "darwin" {
		if home, err := os.UserHomeDir(); err == nil {
			out = append(out, filepath.Join(home, "Library", "Application Support", "shhh", "config.toml"))
		}
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		out = append(out, filepath.Join(xdg, "shhh", "config.toml"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		out = append(out, filepath.Join(home, ".config", "shhh", "config.toml"))
	}
	return out
}
