// Package config loads and writes shhh's settings. Every value resolves
// most-specific-first — flag, environment, file, default — and no setting
// reverses that order, because a user who can predict where a value came from
// can fix it
// (docs/capabilities/configuration.md#one-file-one-format-one-resolution-order).
package config

import (
	"fmt"
	"os"
	"path/filepath"
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
	Summary    SummaryConfig    `toml:"summary"`
	Secrets    SecretsConfig    `toml:"secrets"`
	MCP        MCPConfig        `toml:"mcp"`
}

// MCPConfig is the user's MCP servers and how they are started. shhh
// speaks the protocol only: a server is a command or a URL, and whatever
// authorisation a remote one wants is the job of the forwarder the user put
// in front of it (docs/capabilities/mcp.md#shhh-speaks-the-protocol-and-nothing-else).
type MCPConfig struct {
	// Disabled starts no server and registers no tool, whatever is defined.
	Disabled bool `toml:"disabled"`
	// StartupTimeoutSeconds bounds each server's connect and tool listing
	// (default 20). A server that has not answered by then is reported and
	// left out; the session starts without it.
	StartupTimeoutSeconds int `toml:"startup_timeout_seconds"`
	// Servers are the user's own definitions, keyed by name.
	Servers map[string]MCPServer `toml:"servers"`
}

// MCPServer is one server definition as written in the config file. A
// command makes a stdio server; a url makes a remote one.
type MCPServer struct {
	// Command and Args are the argv of a stdio server; Env is added to its
	// environment. `${NAME}` anywhere in a value is read from the
	// environment at startup, and an unset name keeps the server from
	// starting rather than sending an empty value.
	Command string            `toml:"command,omitempty"`
	Args    []string          `toml:"args,omitempty"`
	Env     map[string]string `toml:"env,omitempty"`
	// URL reaches a remote server over streamable HTTP, or SSE when Type
	// says so. Headers go on every request — a token belongs here as an
	// environment reference, never as the value
	// (docs/capabilities/mcp.md#a-value-in-the-file-is-a-value-in-a-backup).
	URL     string            `toml:"url,omitempty"`
	Headers map[string]string `toml:"headers,omitempty"`
	// Type is "stdio", "http" (the default for a url) or "sse".
	Type string `toml:"type,omitempty"`
	// ReadOnly is the user's statement that nothing this server does needs
	// an answer: its tools run the way a file read does, and it is the only
	// kind of server a conversation takes. The server's own read-only hints
	// are shown and grant nothing
	// (docs/capabilities/mcp.md#a-server-cannot-vouch-for-itself).
	ReadOnly bool `toml:"read_only,omitempty"`
	// Disabled keeps the definition and starts nothing.
	Disabled bool `toml:"disabled,omitempty"`
	// TimeoutSeconds overrides the startup timeout for this server.
	TimeoutSeconds int `toml:"timeout_seconds,omitempty"`
}

// SecretsConfig names the values the model may use but never see. Only
// names live here: the values come from the environment at session start,
// because a config file is read by more things than shhh and a token in it
// is a token in a backup.
// See docs/capabilities/secrets.md#where-a-value-comes-from.
type SecretsConfig struct {
	// Env names environment variables to declare as secrets in every
	// session; one that is unset is skipped with a warning.
	Env []string `toml:"env"`
}

// SummaryConfig tunes the session summary: the periodic reading a
// cheap model takes of the session, drawn as the inspector rail's SUMMARY
// block. It is its own section rather than more `behavior.summary_*` keys
// because auto-steering's knobs land beside these ones.
type SummaryConfig struct {
	// Model is the summarizing model. Empty means the session model, the same
	// rule behavior.classifier_model follows — which is also why setting a
	// fast model here is the one tuning worth doing: the readings are
	// frequent, and the session model is usually the expensive one.
	Model string `toml:"model"`
	// IntervalRounds is how many tool rounds pass between readings (default
	// 10). Higher is cheaper and staler.
	IntervalRounds int `toml:"interval_rounds"`
	// MinGapSeconds floors the wall-clock time between two readings (default
	// 20), so a burst of fast rounds cannot rewrite the block repeatedly.
	MinGapSeconds int `toml:"min_gap_seconds"`
	// TimeoutSeconds bounds one reading (default 20).
	TimeoutSeconds int `toml:"timeout_seconds,omitempty"`
	// MaxTokens caps a reading's response (default 512).
	MaxTokens int `toml:"max_tokens"`
	// Disabled turns the mechanism off entirely: no requests are made and the
	// block is never drawn.
	Disabled bool `toml:"disabled"`
}

// LSPConfig tunes the language-server integration `shhh code` uses
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

// WebConfig tunes the guarded web tools `shhh code` registers.
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
// . The built-in deny mask (~/.ssh, ~/.aws, ~/.config/gh, shhh's own
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
	// "medium", "high", "xhigh" or "max". Empty means medium — a session
	// never starts without thinking unless it was told to
	// (docs/capabilities/providers.md#a-session-never-starts-without-thinking).
	// The level is fitted to each model before it is sent, so a rung the
	// model lacks lowers to the one it has and a model with no reasoning
	// knob is not handed one.
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
	// ScopeDirs are directories added to a session's working scope at start
	// — the config form of /add-dir. The session is always scoped to
	// the directory it was opened in; these are the ones beside it that the
	// work legitimately reaches, so edits there are not treated as leaving
	// the scope and contained commands can write there.
	ScopeDirs []string `toml:"scope_dirs"`
	// DefaultMode is the permission mode agent sessions start in: "manual",
	// "accept-edits", "auto", or "plan". Empty means manual (everything
	// prompts).
	DefaultMode string `toml:"default_mode"`
	// ModeCycle overrides the Shift+Tab mode order (same names as
	// DefaultMode). Empty means manual → accept-edits → auto → plan.
	ModeCycle []string `toml:"mode_cycle"`
	// ClassifierModel is the model auto mode's permission classifier uses
	//. Empty means the session model.
	ClassifierModel string `toml:"classifier_model"`
	// ClassifierTimeoutSeconds bounds each classifier request (default 30).
	ClassifierTimeoutSeconds int `toml:"classifier_timeout_seconds"`
	// ClassifierMaxTokens caps the classifier's response (default 1024).
	ClassifierMaxTokens int `toml:"classifier_max_tokens"`
	// ClassifierRetries is how many extra attempts an invalid or failed
	// classifier response gets before failing closed (default 1).
	ClassifierRetries int `toml:"classifier_retries"`
	// MemoryDisabled turns off durable memory: no memories are
	// injected into the system prompt and the remember tool is not registered.
	MemoryDisabled bool `toml:"memory_disabled"`
	// MemoryMaxEntries caps how many memories are injected per session
	// (default 20).
	MemoryMaxEntries int `toml:"memory_max_entries"`
	// MemoryMaxTokens is the hard token budget for the injected memory block
	// (default 1200).
	MemoryMaxTokens int `toml:"memory_max_tokens"`
}

// AgentsConfig configures sub-agent defaults: which model children
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
	// pgup/pgdn, ctrl+e and j/k. So it is the thing you opt into (ctrl+x, or
	// `/ui mouse on`), and what it buys is both halves of what it cost: the
	// wheel, and a selection shhh owns — one that scrolls past the edge of
	// the pane and copies on release, which the terminal's own
	// cannot do.
	Mouse bool `toml:"mouse"`
	// Notify lets a session raise a desktop notification when a turn stops while
	// the terminal has said the window is not the one in front
	// (docs/interface/surfaces.md#when-you-are-not-there). It is on when unset,
	// because unlike Mouse it takes nothing away: it cannot fire while you are
	// looking at the screen, and the thing it exists for — a turn that stopped
	// on an approval four minutes ago — is invisible until it does.
	Notify *bool `toml:"notify"`
	// PasteLines and PasteColumns are the shape past which a paste is staged
	// as an attachment instead of typed into the draft
	// (docs/interface/surfaces.md#the-input-frame). Zero on either means the
	// default. They are here rather than under behavior for the reason Mouse
	// and Notify are: what they set is how the input surface treats what the
	// reader does at it, not what the session does with the answer.
	PasteLines   int `toml:"paste_lines"`
	PasteColumns int `toml:"paste_columns"`
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

// NotifyEnabled reports whether a session may raise desktop notifications
// (the default).
func (c Config) NotifyEnabled() bool {
	if c.Appearance.Notify == nil {
		return true
	}
	return *c.Appearance.Notify
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
	case "appearance.notify":
		v := value == "true"
		cfg.Appearance.Notify = &v
	case "appearance.paste_lines":
		n := 0
		fmt.Sscanf(value, "%d", &n)
		cfg.Appearance.PasteLines = n
	case "appearance.paste_columns":
		n := 0
		fmt.Sscanf(value, "%d", &n)
		cfg.Appearance.PasteColumns = n
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
	case "secrets.env":
		cfg.Secrets.Env = splitList(value)
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
	case "behavior.scope_dirs":
		cfg.Behavior.ScopeDirs = splitList(value)
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
	case "summary.model":
		cfg.Summary.Model = value
	case "summary.interval_rounds":
		n := 0
		fmt.Sscanf(value, "%d", &n)
		cfg.Summary.IntervalRounds = n
	case "summary.min_gap_seconds":
		n := 0
		fmt.Sscanf(value, "%d", &n)
		cfg.Summary.MinGapSeconds = n
	case "summary.timeout_seconds":
		n := 0
		fmt.Sscanf(value, "%d", &n)
		cfg.Summary.TimeoutSeconds = n
	case "summary.max_tokens":
		n := 0
		fmt.Sscanf(value, "%d", &n)
		cfg.Summary.MaxTokens = n
	case "summary.disabled":
		cfg.Summary.Disabled = value == "true"
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
	case "mcp.disabled":
		cfg.MCP.Disabled = value == "true"
	case "mcp.startup_timeout_seconds":
		n := 0
		fmt.Sscanf(value, "%d", &n)
		cfg.MCP.StartupTimeoutSeconds = n
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
//
// One layout on every platform: XDG_CONFIG_HOME if it is set, then
// ~/.config/shhh. macOS used to be read from ~/Library/Application Support
// first, which meant the answer to "where are my settings" depended on the
// operating system and on which of the two files happened to exist — and a
// Mac that also had an XDG directory had two config files, only one of which
// was ever read. See docs/capabilities/configuration.md#one-layout-everywhere.
// A machine still holding the old directory is detected by `shhh doctor`,
// which offers to move it (internal/migrate).
func Paths() []string {
	var out []string
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		out = append(out, filepath.Join(xdg, "shhh", "config.toml"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		out = append(out, filepath.Join(home, ".config", "shhh", "config.toml"))
	}
	return out
}
