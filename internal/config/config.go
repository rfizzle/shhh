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
	"strconv"
	"strings"
	"time"

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
	Reports    ReportsConfig    `toml:"reports"`
	Agents     AgentsConfig     `toml:"agents"`
	Summary    SummaryConfig    `toml:"summary"`
	Secrets    SecretsConfig    `toml:"secrets"`
	MCP        MCPConfig        `toml:"mcp"`
	Prompts    PromptsConfig    `toml:"prompts"`
}

// PromptsConfig names files whose contents replace shhh's own wordings. The
// mechanism stays in the code and the sentences come out of it, so tuning
// how a session is steered costs an edit and a restart rather than a build
// (docs/capabilities/configuration.md#the-mechanism-is-code-its-wording-is-configuration).
//
// A path that cannot be read stops the session with the path and the reason.
// The failure it guards against is a session running the built-in wording
// while its operator believes it is running theirs, and no later reading of
// the record recovers from that.
type PromptsConfig struct {
	// Steer is the message a drifting turn is given. It may name
	// `{{target}}`, the instruction the turn was judged against, and
	// `{{reason}}`, the reading's own account of the departure.
	Steer string `toml:"steer,omitempty"`
	// CheckIn is the message a turn that has reached its interval is given.
	// It may name `{{rounds}}` and `{{finished}}`, the closing line that
	// differs between a session and a sub-agent.
	CheckIn string `toml:"check_in,omitempty"`
	// Summary is the reading instruction the summarizing model is sent. The
	// digest it judges is appended after it, and takes no placeholders.
	Summary string `toml:"summary,omitempty"`
	// Classifier is the instruction auto mode's permission classifier is
	// sent. The proposed call is appended after it, and takes no
	// placeholders.
	Classifier string `toml:"classifier,omitempty"`
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
	// Headless takes readings in a non-interactive run (`shhh code -p`).
	// Unset means on: a headless run costs one reading per interval, the same
	// as a session, and it is the surface with nobody in front of it — the
	// verdict is the only thing that can tell it that it has drifted or that
	// it already has what it needs.
	Headless *bool `toml:"headless"`
	// Subagents takes readings in each spawned sub-agent. Unset means off,
	// and the reason is arithmetic rather than principle: a fan-out of six
	// children is six more readings per interval, so this one is opt-in even
	// though a child is exactly as unwatched as a headless run.
	Subagents *bool `toml:"subagents"`
	// InterveneCooldownIntervals is how many reading intervals must pass
	// between two verdict-driven interventions (default 2, which any
	// non-positive keeps). It is counted in intervals rather than rounds so
	// it scales with whatever the reading interval is set to.
	InterveneCooldownIntervals int `toml:"intervene_cooldown_intervals"`
	// SteerTargetChars bounds the instruction a steer quotes back to a
	// drifting turn (default 400); any negative quotes it whole. It sits
	// here rather than under behavior because what a steer is worth is a
	// question about the reading that produced it.
	SteerTargetChars int `toml:"steer_target_chars"`
	// Title asks the summary model to name an unnamed session after its
	// first turn, for the saved-chat listings. Unset means on when Model is
	// set and off otherwise: on the session model the question is not
	// cheap, and a title nobody asked for should not cost anything. A name
	// the user gives a session always wins over it.
	Title *bool `toml:"title"`
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
	MaxToolRounds int `toml:"max_tool_rounds"`
	// TreeCheck tells a turn when the working tree moved in a way its own
	// edits do not explain — another session, an editor, a pull. Unset means
	// on; false turns it off for a checkout where `git status` is too slow to
	// pay at every round boundary.
	TreeCheck *bool `toml:"tree_check"`
	// CommandTimeoutSeconds bounds how long one command the assistant runs
	// may take before it is cancelled: zero leaves DefaultCommandTimeout in
	// place, and any negative removes the ceiling. It does not apply to a
	// command the reader typed themselves — they are there, and they chose
	// it.
	CommandTimeoutSeconds int    `toml:"command_timeout_seconds"`
	SafetyWarnings        *bool  `toml:"safety_warnings"`
	SystemPromptExtra     string `toml:"system_prompt_extra"`
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
	// CheckInIntervalRounds is how many tool rounds pass before a turn is
	// asked to take stock; zero — or any negative, since an interval of none
	// is not a thing to ask for — keeps the built-in interval. It is the
	// session's and the headless run's — a sub-agent's is shorter and is not
	// this key's to set
	// (docs/capabilities/coding-agent.md#the-interval-is-the-last-thing-watching).
	CheckInIntervalRounds int `toml:"check_in_interval_rounds"`
	// CheckInMaxDoublings bounds how far that interval widens over one turn:
	// zero keeps the built-in bound, and any negative fixes the interval so
	// a long turn is asked at the same rate throughout.
	CheckInMaxDoublings int `toml:"check_in_max_doublings"`
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
	// Mouse turns terminal mouse reporting on. It is on by default so the
	// wheel scrolls the transcript, click-drag selects it (scrolling past the
	// edge of the pane and copying on release), and clicks open rows or answer
	// decision cards. When turned off (ctrl+x, or `/ui mouse off`), the
	// terminal keeps its native click-drag selection.
	Mouse *bool `toml:"mouse"`
	// Notify lets a session raise a desktop notification when a turn stops while
	// the terminal has said the window is not the one in front
	// (docs/interface/surfaces.md#when-you-are-not-there). It is on when unset,
	// because unlike Mouse it takes nothing away: it cannot fire while you are
	// looking at the screen, and the thing it exists for — a turn that stopped
	// on an approval four minutes ago — is invisible until it does.
	Notify *bool `toml:"notify"`
	// WindowTitle lets a session name the terminal's own tab after itself
	// (docs/interface/surfaces.md#what-the-tab-says). It is on when unset,
	// for Notify's reason: it takes nothing away, and the reader it is for —
	// the one hunting for which of eight tabs is waiting on them — cannot ask
	// for it from inside a window they cannot find. It is not Title: that one
	// names the saved conversation, this one names the window.
	WindowTitle *bool `toml:"window_title"`
	// PasteLines and PasteColumns are the shape past which a paste is staged
	// as an attachment instead of typed into the draft
	// (docs/interface/surfaces.md#the-input-frame). Zero on either means the
	// default and any negative turns that half of the test off, which is how
	// a person who wants every paste typed says so. They are here rather than
	// under behavior for the reason Mouse and Notify are: what they set is
	// how the input surface treats what the reader does at it, not what the
	// session does with the answer.
	PasteLines   int `toml:"paste_lines"`
	PasteColumns int `toml:"paste_columns"`
}

type HistoryConfig struct {
	RetentionDays int `toml:"retention_days"`
}

// ReportsConfig governs the report store. Reports share history's default
// retention because both answer the same question — how long a session's
// residue stays useful — and a page someone reopens next Tuesday is exactly
// the residue worth keeping.
type ReportsConfig struct {
	RetentionDays int `toml:"retention_days"`
}

const DefaultRetentionDays = 90

const DefaultContextMaxTokens = 8000

const (
	DefaultMemoryMaxEntries = 20
	DefaultMemoryMaxTokens  = 1200
)

// DefaultCommandTimeout bounds one assistant-run command.
//
// It is a backstop, not a policy, and the number is chosen so it never
// arrives during real work: a full test suite, a cold dependency install and
// a release build all finish well inside it, and anything past it is a
// command that is not going to finish at all — a prompt nobody will answer, a
// watcher started in the foreground, a network read with no timeout of its
// own.
//
// The cost of the two mistakes is not symmetric. Cutting a command short
// costs one round and an obvious error the model can act on. Not cutting one
// short costs the whole run, and costs it in exactly the situation where
// there is nobody to notice — a session left alone, or a headless run in CI
// that holds its executor until something outside kills it.
const DefaultCommandTimeout = 10 * time.Minute

// CommandTimeout is how long an assistant-run command may take. Zero means
// unset and keeps the default; any negative removes the ceiling, for a
// machine whose builds really do run for hours.
func (c Config) CommandTimeout() time.Duration {
	switch n := c.Behavior.CommandTimeoutSeconds; {
	case n < 0:
		return 0
	case n > 0:
		return time.Duration(n) * time.Second
	default:
		return DefaultCommandTimeout
	}
}

// ReadOnlyAutoEnabled reports whether the built-in inspection allowlist
// auto-runs (the default).
func (c Config) ReadOnlyAutoEnabled() bool {
	if c.Behavior.ReadOnlyAuto == nil {
		return true
	}
	return *c.Behavior.ReadOnlyAuto
}

// MouseEnabled reports whether a session starts with terminal mouse reporting
// enabled (the default).
func (c Config) MouseEnabled() bool {
	if c.Appearance.Mouse == nil {
		return true
	}
	return *c.Appearance.Mouse
}

// TreeCheckEnabled reports whether a turn is told the tree moved under it.
// Unset is on: the reading costs one status call per boundary and is the
// only thing that tells a session it is not alone in the checkout.
func (c *Config) TreeCheckEnabled() bool {
	return c.Behavior.TreeCheck == nil || *c.Behavior.TreeCheck
}

// HeadlessSummaryEnabled reports whether a non-interactive run takes readings:
// what summary.headless says, or — unset — yes, because a run with nobody
// watching it is the one the verdict exists for.
func (c *Config) HeadlessSummaryEnabled() bool {
	if c.Summary.Disabled {
		return false
	}
	if c.Summary.Headless == nil {
		return true
	}
	return *c.Summary.Headless
}

// SubagentSummaryEnabled reports whether each spawned child takes readings:
// what summary.subagents says, or — unset — no, because a wide fan-out
// multiplies the cost by its width.
func (c *Config) SubagentSummaryEnabled() bool {
	if c.Summary.Disabled {
		return false
	}
	if c.Summary.Subagents == nil {
		return false
	}
	return *c.Summary.Subagents
}

// TitlesEnabled reports whether sessions are titled: what summary.title
// says, or — unset — whether a summary model is configured to ask.
func (c Config) TitlesEnabled() bool {
	if c.Summary.Title == nil {
		return c.Summary.Model != ""
	}
	return *c.Summary.Title
}

// NotifyEnabled reports whether a session may raise desktop notifications
// (the default).
func (c Config) NotifyEnabled() bool {
	if c.Appearance.Notify == nil {
		return true
	}
	return *c.Appearance.Notify
}

// WindowTitleEnabled reports whether a session names the terminal's tab:
// what appearance.window_title says, or — unset — yes.
func (c Config) WindowTitleEnabled() bool {
	if c.Appearance.WindowTitle == nil {
		return true
	}
	return *c.Appearance.WindowTitle
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

func (c Config) EffectiveReportsRetentionDays() int {
	if c.Reports.RetentionDays > 0 {
		return c.Reports.RetentionDays
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

// LoadFrom reads the first of paths that exists. A file naming a key no
// setting reads is refused as an UnknownKeyError rather than loaded past:
// the agent profiles and the quality suite a clone brings already refuse one,
// and the loosest file must not be the one the user wrote by hand.
func LoadFrom(paths ...string) (Config, error) {
	var cfg Config
	for _, p := range paths {
		meta, err := toml.DecodeFile(p, &cfg)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return Config{}, err
		}
		if err := unknownKeys(p, meta.Undecoded()); err != nil {
			return Config{}, err
		}
		return cfg, nil
	}
	return Config{}, nil
}

// Set applies one value, as a person typed it, to the setting a key names.
// The value is parsed for the type the setting has and nothing is written
// when the parse fails: the alternative is a key that reports success and
// holds a number nobody chose, which for a retention key means the next
// startup prunes everything
// (docs/capabilities/configuration.md#a-value-is-refused-before-it-is-written).
//
// The words a value may be — a permission mode, a reasoning level, a
// containment profile — are not judged here. Those vocabularies belong to
// the packages that own them, and this one imports none of them; the surface
// that writes a config value checks them before it calls this.
func Set(cfg *Config, key, value string) error {
	if p, signed := intField(cfg, key); p != nil {
		n, err := intValue(key, value, signed)
		if err != nil {
			return err
		}
		*p = n
		return nil
	}
	if p := boolField(cfg, key); p != nil {
		b, err := boolValue(key, value)
		if err != nil {
			return err
		}
		*p = b
		return nil
	}
	if p := triStateField(cfg, key); p != nil {
		b, err := triState(key, value)
		if err != nil {
			return err
		}
		*p = b
		return nil
	}
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
	case "behavior.shell":
		cfg.Behavior.Shell = value
	case "behavior.system_prompt_extra":
		cfg.Behavior.SystemPromptExtra = value
	case "behavior.command_allowlist":
		cfg.Behavior.CommandAllowlist = splitList(value)
	case "behavior.read_only_commands":
		cfg.Behavior.ReadOnlyCommands = splitList(value)
	case "secrets.env":
		cfg.Secrets.Env = splitList(value)
	case "agents.model":
		cfg.Agents.Model = value
	case "agents.researcher_model", "agents.writer_model", "agents.reviewer_model":
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
	case "summary.model":
		cfg.Summary.Model = value
	case "prompts.steer":
		cfg.Prompts.Steer = value
	case "prompts.check_in":
		cfg.Prompts.CheckIn = value
	case "prompts.summary":
		cfg.Prompts.Summary = value
	case "prompts.classifier":
		cfg.Prompts.Classifier = value
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
	case "sandbox.require_isolation":
		cfg.Sandbox.RequireIsolation = value
	case "web.fetch_max_bytes":
		n, err := intValue(key, value, false)
		if err != nil {
			return err
		}
		cfg.Web.FetchMaxBytes = int64(n)
	case "web.search_provider":
		cfg.Web.SearchProvider = value
	case "web.search_api_key":
		cfg.Web.SearchAPIKey = value
	case "appearance.accent_color":
		cfg.Appearance.AccentColor = value
	default:
		return fmt.Errorf("unknown config key: %s", key)
	}
	return nil
}

// ValueError is a value that is not the shape the key it was given to takes.
// It names both, because the two questions a refused write raises are which
// key was refused and what it wanted instead, and an error that answers
// neither leaves the person guessing at a file they cannot see the effect of
// (docs/capabilities/configuration.md#a-value-is-refused-before-it-is-written).
type ValueError struct {
	Key   string
	Value string
	// Want is the shape, phrased to follow "is not": "a whole number",
	// "true or false".
	Want string
}

func (e *ValueError) Error() string {
	return fmt.Sprintf("config key %s: %q is not %s", e.Key, e.Value, e.Want)
}

// intField is the integer setting a key names, or nil when the key is not
// one, and whether a negative is a value that key has a meaning for.
//
// The integer keys are parsed in one place rather than one case each so that
// they are parsed strictly: a value that is not a number is refused, because
// a zero is the line coming out of the file, and a typo must not delete a
// setting the person had.
//
// The second answer is the difference between `-1` as a decision and `-1` as
// a slipped finger. A handful of keys say in their own comment what a
// negative means — no round cap, no command timeout, an interval that never
// widens, a paste that is never staged on that count — and everywhere else a
// negative is a ceiling nothing can satisfy, which is a setting that looks
// present and turns its feature off.
func intField(cfg *Config, key string) (*int, bool) {
	switch key {
	case "behavior.context_max_tokens":
		return &cfg.Behavior.ContextMaxTokens, false
	case "appearance.paste_lines":
		return &cfg.Appearance.PasteLines, true
	case "appearance.paste_columns":
		return &cfg.Appearance.PasteColumns, true
	case "behavior.max_tool_rounds":
		return &cfg.Behavior.MaxToolRounds, true
	case "behavior.command_timeout_seconds":
		return &cfg.Behavior.CommandTimeoutSeconds, true
	case "agents.max_concurrent":
		return &cfg.Agents.MaxConcurrent, false
	case "behavior.classifier_timeout_seconds":
		return &cfg.Behavior.ClassifierTimeoutSeconds, false
	case "behavior.classifier_max_tokens":
		return &cfg.Behavior.ClassifierMaxTokens, false
	case "behavior.classifier_retries":
		return &cfg.Behavior.ClassifierRetries, false
	case "summary.interval_rounds":
		return &cfg.Summary.IntervalRounds, false
	case "summary.min_gap_seconds":
		return &cfg.Summary.MinGapSeconds, false
	case "summary.timeout_seconds":
		return &cfg.Summary.TimeoutSeconds, false
	case "summary.max_tokens":
		return &cfg.Summary.MaxTokens, false
	case "summary.intervene_cooldown_intervals":
		return &cfg.Summary.InterveneCooldownIntervals, true
	case "summary.steer_target_chars":
		return &cfg.Summary.SteerTargetChars, true
	case "behavior.check_in_interval_rounds":
		return &cfg.Behavior.CheckInIntervalRounds, true
	case "behavior.check_in_max_doublings":
		return &cfg.Behavior.CheckInMaxDoublings, true
	case "behavior.memory_max_entries":
		return &cfg.Behavior.MemoryMaxEntries, false
	case "behavior.memory_max_tokens":
		return &cfg.Behavior.MemoryMaxTokens, false
	case "sandbox.container_pids":
		return &cfg.Sandbox.ContainerPids, false
	case "sandbox.container_ttl_hours":
		return &cfg.Sandbox.ContainerTTLHours, false
	case "web.fetch_timeout_seconds":
		return &cfg.Web.FetchTimeoutSeconds, false
	case "web.cache_ttl_minutes":
		return &cfg.Web.CacheTTLMinutes, false
	case "mcp.startup_timeout_seconds":
		return &cfg.MCP.StartupTimeoutSeconds, false
	case "lsp.request_timeout_seconds":
		return &cfg.LSP.RequestTimeoutSeconds, false
	case "lsp.diagnostics_timeout_seconds":
		return &cfg.LSP.DiagnosticsTimeoutSeconds, false
	case "history.retention_days":
		return &cfg.History.RetentionDays, false
	case "reports.retention_days":
		return &cfg.Reports.RetentionDays, false
	}
	return nil, false
}

// boolField is the plain boolean setting a key names, or nil when the key is
// not one. Like the integers they are parsed together so that they are
// parsed strictly: every one of these was `value == "true"` once, which
// turned `yes`, `on` and `True` into a silent false.
func boolField(cfg *Config, key string) *bool {
	switch key {
	case "behavior.silent_mode":
		return &cfg.Behavior.SilentMode
	case "behavior.memory_disabled":
		return &cfg.Behavior.MemoryDisabled
	case "summary.disabled":
		return &cfg.Summary.Disabled
	case "web.allow_private":
		return &cfg.Web.AllowPrivate
	case "lsp.disabled":
		return &cfg.LSP.Disabled
	case "mcp.disabled":
		return &cfg.MCP.Disabled
	}
	return nil
}

// triStateField is the tri-state boolean setting a key names, or nil when
// the key is not one. These keys keep unset apart from false because unset
// is a default that is not always false — mouse reporting, notifications and
// the tree check are all on when nothing says otherwise.
func triStateField(cfg *Config, key string) **bool {
	switch key {
	case "appearance.mouse":
		return &cfg.Appearance.Mouse
	case "appearance.notify":
		return &cfg.Appearance.Notify
	case "appearance.window_title":
		return &cfg.Appearance.WindowTitle
	case "behavior.tree_check":
		return &cfg.Behavior.TreeCheck
	case "behavior.safety_warnings":
		return &cfg.Behavior.SafetyWarnings
	case "behavior.read_only_auto":
		return &cfg.Behavior.ReadOnlyAuto
	case "summary.headless":
		return &cfg.Summary.Headless
	case "summary.subagents":
		return &cfg.Summary.Subagents
	case "summary.title":
		return &cfg.Summary.Title
	}
	return nil
}

// intValue parses a number as a person types it; empty is unset. signed says
// the key has a meaning for a negative, and without it one is refused rather
// than stored.
func intValue(key, value string, signed bool) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return 0, &ValueError{Key: key, Value: value, Want: "a whole number"}
	}
	if n < 0 && !signed {
		return 0, &ValueError{Key: key, Value: value, Want: "a whole number, zero or above"}
	}
	return n, nil
}

// boolValue parses a boolean the way Go reads one — true/false, t/f, 1/0 and
// their capitalisations — and refuses everything else. `yes` and `on` are
// among the everything else on purpose: they are words a person reasonably
// types, and a parser that quietly answered false for them is how a key
// nobody could see turned itself off.
func boolValue(key, value string) (bool, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return false, nil
	}
	b, err := strconv.ParseBool(value)
	if err != nil {
		return false, &ValueError{Key: key, Value: value, Want: "true or false"}
	}
	return b, nil
}

// triState is a tri-state key's value: nil for an empty value, so that a
// reset puts the key back to unset — the default, whatever it is — rather
// than to false, which for a key that is on when unset would be the opposite
// of what a reset means.
func triState(key, value string) (*bool, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	b, err := boolValue(key, value)
	if err != nil {
		return nil, err
	}
	return &b, nil
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

// WritePath is the file a write goes to: the first of Paths that exists, or
// the first of them when none does.
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
