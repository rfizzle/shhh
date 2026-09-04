// Package config loads and writes shhh's settings. Every value resolves
// most-specific-first — flag, environment, the checkout's file, the user's,
// default — and no setting reverses that order, because a user who can
// predict where a value came from can fix it
// (docs/capabilities/configuration.md#two-files-one-resolution-order).
package config

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/rfizzle/shhh/internal/hook"
)

type Config struct {
	Provider   ProviderConfig   `toml:"provider"`
	Behavior   BehaviorConfig   `toml:"behavior"`
	Sandbox    SandboxConfig    `toml:"sandbox"`
	Web        WebConfig        `toml:"web"`
	LSP        LSPConfig        `toml:"lsp"`
	Appearance AppearanceConfig `toml:"appearance"`
	History    HistoryConfig    `toml:"history"`
	Chats      ChatsConfig      `toml:"chats"`
	Reports    ReportsConfig    `toml:"reports"`
	Observe    ObserveConfig    `toml:"observe"`
	Otel       OtelConfig       `toml:"otel"`
	Agents     AgentsConfig     `toml:"agents"`
	Summary    SummaryConfig    `toml:"summary"`
	Secrets    SecretsConfig    `toml:"secrets"`
	MCP        MCPConfig        `toml:"mcp"`
	Prompts    PromptsConfig    `toml:"prompts"`
	Todo       TodoConfig       `toml:"todo"`
	Hooks      HooksConfig      `toml:"hooks"`
}

// HooksConfig is the user's own hooks: their own commands at the seams a
// session already has. The entries are a table each, keyed by a name the
// person picks, so a diagnostic and the doctor row can say which one — and so
// an entry copied into a checkout's own hooks file keeps the name it had
// (docs/capabilities/hooks.md#where-a-hook-is-written).
type HooksConfig struct {
	// Disabled fires nothing, whatever the files define. It is the answer for
	// a session where a hook is misbehaving and the person wants the session
	// rather than the hook.
	Disabled bool `toml:"disabled"`
	// TimeoutSeconds is the longest any hook may take, and the cap on a
	// hook's own timeout. Zero takes the session's command ceiling, which is
	// what a hook is: a command the session runs with nobody watching it.
	TimeoutSeconds int `toml:"timeout_seconds"`
	// Entries are the hooks themselves, keyed by name.
	Entries map[string]hook.Entry `toml:"entries"`
}

// TodoConfig is what a backlog run does when it is not told otherwise.
type TodoConfig struct {
	// Commit says whether a run ends in a commit. Unset is a commit,
	// because a commit is what the runner treats as done and an item
	// archived beside an uncommitted tree is an item that says it landed
	// and did not. False ends the run after the review and leaves the
	// change in the working tree, which is the answer for a directory that
	// is not a repository and for a project whose commits are made
	// elsewhere. See
	// docs/capabilities/todo.md#a-run-is-turns-with-gates-between-them.
	Commit *bool `toml:"commit"`
	// ItemTimeoutMinutes bounds how long one item of a sprint may take
	// before the sprint gives up on it. Zero is no cap, which is the
	// default: the cap ends a run whose work is already in the tree, and
	// how long is too long for an item is a fact about the project's
	// checks and its provider rather than about shhh. It is read at the
	// boundary between two of the run's stages, so it bounds the item
	// rather than cutting a turn in half and leaving a tree nothing has
	// read.
	// See docs/capabilities/todo.md#a-sprint-is-runs-with-a-session-between-them.
	ItemTimeoutMinutes int `toml:"item_timeout_minutes"`
	// GroomStaleCommits is how many commits the tree may take after an item
	// was last read against it before the surfaces say the reading has
	// fallen behind. Zero — the unset value — takes the backlog's own
	// default; a negative number turns the warning off, for a project that
	// keeps its backlog current by hand. It counts commits
	// rather than days because what makes an item stale is the tree moving
	// under it, and a quiet month moves nothing.
	// See docs/capabilities/todo.md#an-item-is-checked-before-it-is-worked.
	GroomStaleCommits int `toml:"groom_stale_commits"`
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

	// The backlog runner's stage instructions. Each names a file that
	// replaces what one stage of a run tells the model; the blocks the run
	// hands the model and the answer shape it reads back are the runner's
	// and are placed and appended whatever the file says
	// (docs/capabilities/todo.md#the-stage-prompts-are-yours-to-edit).
	//
	// TodoStandards is a key of its own rather than a line inside each of
	// the others because it is one sentence shared by every stage that
	// changes the tree, and it is the line a project most often has to
	// change: a monorepo's own conventions, a language the sentence does
	// not fit.
	TodoStandards string `toml:"todo_standards,omitempty"`
	// TodoResearch may name `{{item}}` and `{{answers}}`.
	TodoResearch string `toml:"todo_research,omitempty"`
	// TodoImplement may name `{{item}}`, `{{plan}}` and `{{answers}}`.
	TodoImplement string `toml:"todo_implement,omitempty"`
	// TodoReview may name `{{item}}`, `{{plan}}` and `{{diff}}`, which for
	// the stage that reads the change itself is the instruction that finds
	// it.
	TodoReview string `toml:"todo_review,omitempty"`
	// TodoReviewTask is the reviewer child's, and may name the same three;
	// `{{diff}}` there is the change itself, since the child has no
	// commands to go and read it with.
	TodoReviewTask string `toml:"todo_review_task,omitempty"`
	// TodoRemediate may name `{{item}}` and `{{findings}}`.
	TodoRemediate string `toml:"todo_remediate,omitempty"`
	// TodoCommit may name `{{item}}`. The sentence about the repository's
	// own commit style follows it whatever the file says, because whether
	// there is a history to read one out of is a fact about the machine.
	TodoCommit string `toml:"todo_commit,omitempty"`
}

// Todo is the file a key names for one of a run's step wordings, and empty
// for a step no key here has. The keys are the built-in run's step names; a
// run whose steps a project states for itself has more of them than a
// settings file has fields for, and those are found by convention under a
// prompts directory — which is how a wording is found in the ordinary case
// either way (docs/capabilities/todo.md#the-stage-prompts-are-yours-to-edit).
func (p PromptsConfig) Todo(key string) string {
	switch key {
	case "standards":
		return p.TodoStandards
	case "research":
		return p.TodoResearch
	case "implement":
		return p.TodoImplement
	case "review":
		return p.TodoReview
	case "review_task":
		return p.TodoReviewTask
	case "remediate":
		return p.TodoRemediate
	case "commit":
		return p.TodoCommit
	}
	return ""
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
	// EnvMask keeps the variables that hold a credential by convention out
	// of the environment an assistant command inherits. Unset means on: the
	// machine shhh runs on is a developer's, its environment is full of
	// tokens nobody meant to lend to a model, and Env is the list of the
	// ones that were meant.
	EnvMask *bool `toml:"env_mask"`
}

// SummaryConfig tunes the session summary: the periodic reading a
// cheap model takes of the session, drawn as the inspector rail's SUMMARY
// block. It is its own section rather than more `behavior.summary_*` keys
// because auto-steering's knobs land beside these ones.
type SummaryConfig struct {
	// Model is the summarizing model. Empty means the provider's own small
	// model, and the session model where the provider names none — the same
	// rule behavior.classifier_model follows.
	Model string `toml:"model"`
	// IntervalRounds is how many tool rounds pass between readings (default
	// 10). Higher is cheaper and staler.
	IntervalRounds int `toml:"interval_rounds"`
	// MinGapSeconds floors the wall-clock time between two readings (default
	// 20), so a burst of fast rounds cannot rewrite the block repeatedly.
	MinGapSeconds int `toml:"min_gap_seconds"`
	// TimeoutSeconds bounds one reading (default 20).
	TimeoutSeconds int `toml:"timeout_seconds,omitempty"`
	// MaxTokens caps a reading's response, the reasoning it does before
	// answering included (default 8192).
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
	// set and off otherwise: a provider that names no small model of its own
	// reads titles on the session's, and a title nobody asked for should not
	// cost that. A name the user gives a session always wins over it.
	Title *bool `toml:"title"`
}

// LSPConfig tunes the language-server integration `shhh code` uses for
// diagnostics and the navigation tools. Servers are auto-detected on PATH
// (gopls, rust-analyzer, typescript-language-server, pyright); none found
// means the integration is a clean no-op.
type LSPConfig struct {
	// Disabled turns the LSP integration off entirely: no servers started, no
	// navigation tools registered, no diagnostics.
	Disabled bool `toml:"disabled"`
	// RequestTimeoutSeconds bounds each server request, including the
	// initialize handshake (default 15).
	RequestTimeoutSeconds int `toml:"request_timeout_seconds"`
	// DiagnosticsTimeoutSeconds is how long an applied edit waits for the
	// server to re-check the file before moving on (default 3). Raising it
	// buys nothing that waiting does not: a check that lands after it is held
	// and delivered with the next tool result rather than dropped, which is
	// why the default stays short enough not to be felt.
	// See docs/capabilities/coding-agent.md#diagnostics-that-arrive-late-still-arrive.
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
	// registered. It holds the key itself, so every copy of this file is a
	// copy of the key — SearchAPIKeyEnv is the form to prefer, and this one
	// is here for the machines that were set up before it existed.
	SearchAPIKey string `toml:"search_api_key"`
	// SearchAPIKeyEnv names the environment variable the search key is read
	// from at start. It is read ahead of SearchAPIKey.
	// See docs/capabilities/secrets.md#where-a-value-comes-from.
	SearchAPIKeyEnv string `toml:"search_api_key_env"`
}

// SandboxConfig tunes process containment for agent-executed commands
// . The built-in deny mask (~/.ssh, ~/.aws, ~/.config/gh, shhh's own
// config and state dirs) is deliberately not configurable — it cannot be
// disabled, only extended.
type SandboxConfig struct {
	// Require refuses an assistant command outright on a host where no
	// mechanism is in force, instead of running it unconfined. It is off by
	// default because a machine with no bubblewrap is still a machine
	// somebody has to work on; turning it on is how running bare stops being
	// the answer nobody chose.
	// See docs/capabilities/containment.md#containment-can-be-required.
	Require bool `toml:"require"`
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
	// APIKey holds the key itself, so every copy of this file — a backup, a
	// dotfiles commit, a screen share — is a copy of the key. APIKeyEnv is
	// the form to prefer, and this one is here for the machines that were
	// set up before it existed.
	APIKey string `toml:"api_key"`
	// APIKeyEnv names the environment variable the provider key is read from
	// at start, which is how every other credential shhh takes is written:
	// the file carries the name and the environment carries the value. It is
	// read ahead of APIKey.
	// See docs/capabilities/secrets.md#where-a-value-comes-from.
	APIKeyEnv string `toml:"api_key_env"`
	BaseURL   string `toml:"base_url"`
	Name      string `toml:"name"`
	// Reasoning is the level of thinking sessions start on: "off", "low",
	// "medium", "high", "xhigh" or "max". Empty means medium — a session
	// never starts without thinking unless it was told to
	// (docs/capabilities/providers.md#a-session-never-starts-without-thinking).
	// The level is fitted to each model before it is sent, so a rung the
	// model lacks lowers to the one it has and a model with no reasoning
	// knob is not handed one.
	Reasoning string `toml:"reasoning"`
	// CacheTTL is how long the opening a session repeats every round — the
	// tool schemas, the system prompt, the project context and the skills
	// catalog — stays cached between rounds: "5m" or "1h". Empty means an
	// hour, because an interactive session idles past five minutes
	// constantly. It reaches only the dialects that have to be told what to
	// cache; the ones that cache by themselves ignore it
	// (docs/capabilities/providers.md#the-prompt-prefix-is-paid-for-once).
	CacheTTL string `toml:"cache_ttl"`
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
	// CommandDenylist entries refuse matching agent commands outright, by the
	// same leading-words rule the allowlist uses and before it. It is the
	// answer given once for every mode: a command on it never reaches a card,
	// a classifier, or a headless run's --yes. Empty (the default) means
	// nothing is refused in advance.
	CommandDenylist []string `toml:"command_denylist"`
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
	// ClassifierModel is the model auto mode's permission classifier uses.
	// Empty means the provider's own small model, and the session model
	// where the provider names none.
	ClassifierModel string `toml:"classifier_model"`
	// ClassifierTimeoutSeconds bounds each classifier request (default 30).
	ClassifierTimeoutSeconds int `toml:"classifier_timeout_seconds"`
	// ClassifierMaxTokens caps the classifier's response, the reasoning it
	// does before answering included (default 8192).
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
	// ProviderRetries bounds how many times one stall — a rate limit, an
	// overloaded provider, a connection that died before a token — is asked
	// again before the failure stands. Unset keeps the built-in bound; zero
	// is a machine that would rather see the failure than sit out a wait.
	//
	// Those two are different answers, which is why this is a pointer and
	// not one of the counts whose zero means unset: a key whose absence read
	// as zero would take the waiting away from everyone who never wrote it,
	// and the symptom — an unattended run ending on its first rate limit —
	// looks like the provider rather than like a default.
	// See docs/capabilities/providers.md#a-stall-is-waited-out-on-one-schedule.
	ProviderRetries *int `toml:"provider_retries"`
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
	// Theme is which of the shipped colour tables every surface draws with:
	// `auto`, which asks the terminal what its own background is and takes
	// the table chosen against that ground, or one of them by name. It is a
	// word rather than a set of colours because the tables are the
	// product's — a palette a file could extend would be a file that can
	// reach for a colour no token names
	// (docs/interface/principles.md#a-colour-is-three-values-and-a-ground).
	// Empty is `auto`, the way an unset key is everywhere else.
	Theme string `toml:"theme"`
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
	// RailWidth is how many columns the chat surface's inspector rail takes
	// on a terminal wide enough to show one
	// (docs/interface/surfaces.md#the-inspector-rail): "auto", which widens
	// the rail with the terminal, or a column count for a person whose rail
	// has to fit a pane they chose the size of. It is a word or a number, so
	// it is a string here and the surface that owns the rail reads it; an
	// empty value is auto, the way an unset key is everywhere else.
	RailWidth string `toml:"rail_width"`
}

type HistoryConfig struct {
	RetentionDays int `toml:"retention_days"`
}

// ChatsConfig governs the saved conversations. It is the one window in the
// product that is off until somebody sets it: history, reports and the
// session record are residue a session leaves behind, and a conversation is
// the work itself.
// See docs/capabilities/sessions-and-memory.md#a-conversation-is-kept-for-a-window.
type ChatsConfig struct {
	// RetentionDays is how long a conversation nobody has written to is
	// kept. Zero — the unset value — keeps every one of them forever, which
	// is what the product did before there was a key here.
	RetentionDays int `toml:"retention_days"`
}

// ReportsConfig governs the report store. Reports share history's default
// retention because both answer the same question — how long a session's
// residue stays useful — and a page someone reopens next Tuesday is exactly
// the residue worth keeping.
type ReportsConfig struct {
	RetentionDays int `toml:"retention_days"`
}

// ObserveConfig governs the session record. It has a window of its own, and
// a longer one, because the reader is a different reader: history's window is
// about a person remembering a command they ran, and this one is read by a
// comparison of two cohorts, which over a change made last quarter wants the
// quarter before it as well.
// See docs/capabilities/sessions-and-memory.md#the-record-is-kept-for-a-window.
type ObserveConfig struct {
	RetentionDays int `toml:"retention_days"`
}

// OtelConfig sends the session record somewhere other than this machine.
//
// One key, and it is the endpoint, because there is nothing else to decide.
// What is exported is the record, which is fixed; whether to export is
// answered by whether an endpoint is written down; and the record is
// content-free by construction, so there is no subset of it anyone would
// want to choose. A second key here would be a knob over a decision the
// product has already made.
// See docs/capabilities/sessions-and-memory.md#the-record-can-leave-this-machine.
type OtelConfig struct {
	// Endpoint is where an OTLP collector listens, as a URL with its scheme
	// — `http://localhost:4318`, or the full path where a gateway mounts the
	// receiver somewhere other than the root. Empty is off, which is the
	// default: a machine that has not been told where to send the record
	// keeps it.
	Endpoint string `toml:"endpoint"`
}

const DefaultRetentionDays = 90

// DefaultObserveRetentionDays is twice history's window, rounded to the two
// quarters a before-and-after reading needs: a comparison split on a change
// made three months ago has to have the sessions from either side of it, and
// ninety days would leave one of the two cohorts empty at exactly the moment
// somebody asks.
const DefaultObserveRetentionDays = 180

const DefaultContextMaxTokens = 8000

const (
	DefaultMemoryMaxEntries = 20
	DefaultMemoryMaxTokens  = 1200
)

// DefaultHookCeiling bounds one hook. It is short because every seam a hook
// sits on has something waiting on the other side of it, and a hook is a
// formatter or a path check rather than a build.
// See docs/capabilities/hooks.md#a-hook-that-runs-too-long-has-failed.
const DefaultHookCeiling = 30 * time.Second

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

// HookCeiling is the longest any hook may take, and the cap on a hook's own
// timeout.
//
// Unset is DefaultHookCeiling and not the command ceiling, because something
// is waiting on the other side of every seam a hook sits on — a turn closing
// waits on the goroutine drawing the screen — and ten minutes there is a
// session that has stopped. What it may be raised to is the command ceiling:
// a hook is a command the session runs, and nothing the session runs may
// outlast that.
//
// There is deliberately no way to turn it off, which is the one place a hook
// is bounded more tightly than a command. A command with no ceiling is a dev
// server somebody started on purpose and can see; a hook with no ceiling is a
// seam that never answers, and every seam has something waiting on it.
func (c Config) HookCeiling() time.Duration {
	ceiling := DefaultHookCeiling
	if n := c.Hooks.TimeoutSeconds; n > 0 {
		ceiling = time.Duration(n) * time.Second
	}
	if command := c.CommandTimeout(); command > 0 && command < ceiling {
		return command
	}
	return ceiling
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

// SecretsEnvMaskEnabled reports whether an assistant command runs without
// the credential-shaped variables it would otherwise inherit: what
// secrets.env_mask says, or — unset — yes, because the variables it takes
// away are the ones nobody chose to lend.
// See docs/capabilities/secrets.md#the-names-that-do-not-travel.
func (c *Config) SecretsEnvMaskEnabled() bool {
	return c.Secrets.EnvMask == nil || *c.Secrets.EnvMask
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

// EffectiveChatsRetentionDays is the saved-conversation window, and zero is
// the answer when nobody has set one. There is no default standing behind it,
// unlike every other window here: see ChatsConfig.
func (c Config) EffectiveChatsRetentionDays() int {
	return c.Chats.RetentionDays
}

func (c Config) EffectiveReportsRetentionDays() int {
	if c.Reports.RetentionDays > 0 {
		return c.Reports.RetentionDays
	}
	return DefaultRetentionDays
}

func (c Config) EffectiveObserveRetentionDays() int {
	if c.Observe.RetentionDays > 0 {
		return c.Observe.RetentionDays
	}
	return DefaultObserveRetentionDays
}

// ProviderAPIKey returns the configured API key: the value of the variable
// api_key_env names, and otherwise the literal api_key.
func (c Config) ProviderAPIKey() string {
	return keyFromEnvOrFile(c.Provider.APIKeyEnv, c.Provider.APIKey)
}

// WebSearchAPIKey returns the search backend's key on the same terms as the
// provider's: the named variable first, the literal second.
func (c Config) WebSearchAPIKey() string {
	return keyFromEnvOrFile(c.Web.SearchAPIKeyEnv, c.Web.SearchAPIKey)
}

// keyFromEnvOrFile resolves a credential the config file may hold either way
// round. The name wins because reading it is an environment read, and the
// environment outranks the file for every other setting; a file carrying both
// is one part-way through the move, and honouring the name is what makes
// writing the name the thing that changes what is in force.
//
// A named variable that is not exported resolves to nothing rather than
// falling through to the literal. Falling through would mean a session that
// silently kept working off the copy the person had just stopped meaning to
// use, which is the state this whole shape exists to make visible.
//
// A gateway profile spells the same pair and refuses a file that sets both,
// which is the stricter rule and the right one there: nobody is mid-move in a
// profile, because profiles have taken a name since they existed. Here both
// together is the ordinary state of a file being moved over, so the two are
// ranked instead of refused, and the doctor's row is what keeps the older
// spelling from becoming permanent.
func keyFromEnvOrFile(name, literal string) string {
	if name = strings.TrimSpace(name); name != "" {
		return os.Getenv(name)
	}
	return literal
}

// ProviderBaseURL returns the configured base URL.
func (c Config) ProviderBaseURL() string {
	return c.Provider.BaseURL
}

// ProviderDisplayName returns the configured custom display name.
func (c Config) ProviderDisplayName() string {
	return c.Provider.Name
}

// TodoCommitEnabled reports whether a backlog run ends in a commit. Unset is
// a commit, so only a file that says false turns it off.
func (c *Config) TodoCommitEnabled() bool {
	return c.Todo.Commit == nil || *c.Todo.Commit
}

// TodoItemTimeout is the wall-clock cap a sprint holds one item to, and zero
// where the project set none.
func (c *Config) TodoItemTimeout() time.Duration {
	if c.Todo.ItemTimeoutMinutes <= 0 {
		return 0
	}
	return time.Duration(c.Todo.ItemTimeoutMinutes) * time.Minute
}

// TodoGroomStale is how far behind a grooming may fall before the backlog
// surfaces say so. Zero is passed on as zero rather than resolved here: the
// number the default stands for is the backlog's own, and a copy of it in
// this package would be a second answer to what "stale" means.
func (c *Config) TodoGroomStale() int { return c.Todo.GroomStaleCommits }

// ProviderCacheTTL returns the configured lifetime of the repeated opening.
func (c Config) ProviderCacheTTL() string {
	return c.Provider.CacheTTL
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

// KeymapPaths returns the keybinding file's paths in the same search order,
// one beside each config file: the layout that holds config.toml holds
// everything the user wrote for shhh, the way the agent profiles and the
// skills directories already sit there.
//
// It is a file of its own rather than a table in config.toml, and it is the
// user's alone — a checkout does not layer one. A repository that could move
// a key would be a repository deciding what the keys under someone's hands
// do, which is a worse trade than the one project settings already make.
// See docs/capabilities/configuration.md#the-keymap-file.
func KeymapPaths() []string {
	var out []string
	for _, p := range Paths() {
		out = append(out, filepath.Join(filepath.Dir(p), "keybindings.toml"))
	}
	return out
}
