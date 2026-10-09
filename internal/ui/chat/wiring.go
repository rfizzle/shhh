package chat

// What a session is made of, as one value.
//
// A session is the conversation, the stream that answers it, and everything
// the screen is given besides: what it runs commands through, what it may do
// without asking, the stores it writes, the readers that watch it, the
// children it may spawn, the surfaces it offers and which of its
// conveniences are on. That last part is this value. It is built by the
// host in phases and handed to the constructor whole, so it can be read,
// compared and asserted before any terminal exists, and built by anything
// that can fill a struct.
//
// The value is inert: it holds what the session was given, never what it has
// done. A field the update loop goes on to write is seeded from it at
// construction and lives on the model from there; the value keeps what it
// was handed. A value nobody filled in is today's defaults, which is why the
// conveniences that are on unless the config says otherwise are named for
// the off state.
// See docs/architecture.md#the-screen-is-handed-its-wiring-as-one-value.

import (
	"context"
	"time"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/hook"
	"github.com/rfizzle/shhh/internal/meter"
	"github.com/rfizzle/shhh/internal/notebook"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/scope"
	"github.com/rfizzle/shhh/internal/skill"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/web"
)

// Wiring is everything a session is given besides its conversation and its
// stream.
type Wiring struct {
	// --- Where it is.

	// Title overrides the header title (default "shhh chat"), so `shhh code`
	// can reuse the screen under its own name.
	Title string
	// Workspace is the directory the session's relative paths are resolved
	// against — where a saved plan lands, what a command's blast radius is
	// measured from, where an attached file is read. A session that is not
	// told stays on the process's working directory. It is also what lets a
	// surface be exercised against a scratch directory without moving the
	// process: a test that chdirs makes its package uncacheable.
	Workspace string
	// Checkout is the survey a session with no start screen names the
	// directory and branch from in its header. A conversation draws no start
	// screen but is still opened in a checkout, and a header naming only the
	// model leaves the reader to ask where they are.
	Checkout *project.Info
	// ProviderName names the provider the session resolved to; ReplaceKey and
	// SwitchProvider are the two things a provider failure can offer to do
	// about it. Either may be nil — the failure row then does not offer that
	// key rather than offering one that does nothing.
	ProviderName   string
	ReplaceKey     func(string) error
	SwitchProvider func(string) error
	// ModelName is the model the session opened on.
	ModelName string
	// Defaults is the persisted-defaults surface.
	Defaults Defaults
	// ProjectContextTokens is the estimated cost of the project context
	// (AGENTS.md and friends) the system prompt carries, so the occupancy
	// breakdown can name it separately.
	ProjectContextTokens int64
	// ToolDefinitions are the registered tool definitions and their
	// estimated cost. The total is the occupancy breakdown's tool category,
	// and the rows are what the context surface itemises it into.
	ToolDefinitions []ToolTokens

	// --- The policy.

	// Mode and Cycle are the starting permission mode and the Shift+Tab
	// cycle order; an empty cycle keeps the default order. A conversation
	// keeps the one mode it has whatever Mode says (conversation.go).
	Mode  agent.Mode
	Cycle []agent.Mode
	// Conversation marks the session as a conversation: no start screen and
	// one policy (conversation.go).
	Conversation bool
	// CommandAllowlist runs a command whose leading words match an entry
	// without a card, unless safety-flagged; CommandDenylist refuses one
	// before a card is drawn, in every mode.
	CommandAllowlist []string
	CommandDenylist  []string
	// AllowHosts and DenyHosts are the config's host lists: a fetch to an
	// allowed host runs without a card, and a fetch to a denied one is
	// refused before a card is drawn, in every mode.
	AllowHosts []string
	DenyHosts  []string
	// HostGrants is the sink the session's reachable hosts are pushed to
	// whenever they change. The fetcher is what takes them, and it is what
	// answers a redirect: a hop that starts on a granted host and ends on an
	// ungranted one is a decision nobody made, and the fetcher is the only
	// place that hop is visible.
	// See docs/capabilities/approvals-and-safety.md#a-host-is-granted-once.
	HostGrants func([]string)
	// CommandTimeout bounds how long one assistant-run command may take; zero
	// or less removes the ceiling. A command the reader typed is never bounded
	// by it: they chose to run the thing, so the key that cancels it is the
	// ceiling.
	// See docs/capabilities/containment.md#a-command-that-will-not-finish-is-not-waited-on-forever.
	CommandTimeout time.Duration
	// ReadOnlyCommands extends the built-in read-only inspection allowlist,
	// and ReadOnlyOff stops the built-in list from auto-running at all.
	ReadOnlyCommands []string
	ReadOnlyOff      bool
	// Scope is the session's working scope: the directory it was opened in
	// plus whatever has been added since. A session without one treats every
	// path as in scope. It is a pointer because the runners that wrap
	// contained commands read it off the UI goroutine, and because a grant
	// made on a card has to be the grant the sandbox sees on the next command.
	Scope *scope.Scope
	// Containment is what assistant commands run inside.
	Containment Containment
	// Secrets enables /secret, and its scrub is installed on the loop so the
	// conversation this screen saves, shows and replays never holds a value.
	// See docs/capabilities/secrets.md#the-value-is-scrubbed-at-every-door.
	Secrets Secrets
	// CommitSecretIgnore is commit.secret_ignore: the fixtures a commit made
	// from this session may carry a credential shape in.
	CommitSecretIgnore []string
	// GatedTools are the tools that must be approved before they run through
	// the executor, each with the card's preview of its call; they never run
	// on the auto-run path. GatedChecks are the refusals that stand in front
	// of a preview: the preview stays a pure reading of the arguments, and
	// the check is what may be slow.
	GatedTools  map[string]GatedPreviewFunc
	GatedChecks map[string]GatedCheckFunc
	// MutationHook sees a write or an edit's result, which reaches no
	// executor.
	MutationHook MutationHook
	// Hooks is the person's own commands at the session's seams. Nil is a
	// session with no hooks, which every seam is safe under.
	Hooks *hook.Runner

	// --- The loop's settings.

	// Executor runs the calls the loop dispatches without a card. It is
	// wrapped with the hooks last of all, at construction.
	Executor ToolExecutor
	// Repeats is the detector the executor is wrapped with, so the calls this
	// screen dispatches itself — a command the reader approved, an edit
	// applied through the mutating tools — are counted in the same window as
	// the rest. It is the detector rather than a wrap because the two tiers
	// meet nowhere else, and only the screen knows which of its commands is
	// the agent's own rather than a `/run` the reader typed.
	Repeats *agent.RepeatDetector
	// MaxToolRounds overrides the per-turn round cap; zero keeps the default
	// and a negative one starts the session with no checkpoint at all.
	MaxToolRounds int
	// Steering is the interruption machinery's tuning; zero is the built-in
	// set.
	Steering agent.Steering
	// ProgressCalls and ProgressElapsed are the two public-status clocks;
	// zero keeps each one's default.
	ProgressCalls   int
	ProgressElapsed time.Duration
	// RetryLimit bounds a stall at the attempts a setting names; nil is a
	// file that named none and keeps the built-in bound.
	RetryLimit *int
	// StreamIdle is the stream's idle deadline, so a silent wait can say when
	// the retry is coming.
	StreamIdle time.Duration
	// TreeCheck turns the moved-tree reading on; nil leaves it off. Its Own
	// is filled from the session's changeset when left unset, and its
	// Instructions from the same walk the prompt made.
	TreeCheck *agent.TreeCheck
	// Runner enables /run; TailRunner runs assistant commands and /run with a
	// live tail, and without one they run with none.
	Runner     RunFunc
	TailRunner TailFunc
	// GitSnapshots captures the git state each rewind checkpoint records;
	// nil records none.
	GitSnapshots func() GitSnapshot

	// --- The stores.

	// DB is the local store, which is also where the session's slot comes
	// from (the constructor claims it). PersistenceError is why it did not
	// open, kept for the exit banner because the alternate screen clears the
	// startup warning before the session ends.
	DB               *storage.DB
	PersistenceError error
	// Changeset replaces the store every session has with one of a different
	// bound or one persisted into the local store; Tracker answers whether a
	// file was tracked when it was edited. A conversation, having nothing to
	// change, is given neither.
	Changeset *changeset.Store
	Tracker   *changeset.Tracker
	// Notebook and Sources are the session's shared notebook and sources
	// ledger. The screen owns the slot's name, so it binds both to it — at
	// construction, and again wherever the name changes.
	Notebook *notebook.Store
	Sources  *web.Ledger
	// Evidence enables output reduction, /evidence, and the recovery of
	// what a window trim elides.
	Evidence Evidence

	// --- The readers.

	// Classifier is auto mode's permission classifier: gated calls the
	// static policy would ask about are judged by it, and its failures fall
	// back to asking.
	Classifier *agent.Classifier
	// Explainer is the command card's explanation key. Without one the card
	// offers nothing and the key does nothing.
	Explainer *agent.Explainer
	// Summarizer writes the session summary; nil leaves the block undrawn.
	Summarizer *agent.Summarizer
	// Titler names the session's rows, and Titles says it starts on.
	Titler *agent.Titler
	Titles bool
	// Accountant writes the standing account every AccountEvery turns; nil,
	// or zero turns, asks nothing.
	Accountant   *agent.Accountant
	AccountEvery int
	// Suggester writes the offered next step, and Suggestions says the
	// session starts with offers on.
	Suggester   *agent.Suggester
	Suggestions bool
	// StartOfferer writes the start screen's offers, from what
	// StartOffersGather reads off the UI goroutine. Nil asks nothing.
	StartOfferer      *agent.StartOfferer
	StartOffersGather func(context.Context) agent.StartOffersRequest
	// Patterns enables /patterns.
	Patterns Patterns
	// Observer records the session; the zero Observer records nothing.
	Observer observe.Observer
	// FirstPaint is told when the first frame with the prompt in it has been
	// drawn. It is called from every such frame and has to answer only the
	// first, which is the caller's to arrange: View is a value receiver.
	FirstPaint func()

	// --- The children.

	// Subagents is the sub-agent supervisor; the screen listens for its
	// events and keeps its parent-mode ceiling current.
	Subagents *subagent.Supervisor
	// Personas wires the drafting flow.
	Personas Personas

	// --- The surfaces.

	// Todos enables /todo and the backlog block.
	Todos Todos
	// Memory enables /memory and the remember card.
	Memory Memory
	// Skills is the session's skill catalog and SkillsList the listing
	// /skills prints. Activated skill content is exempted from context
	// trimming: the instructions are guidance for every later turn.
	// See docs/capabilities/skills.md#a-skill-is-read-in-three-tiers.
	Skills     *skill.Catalog
	SkillsList func(*skill.Catalog) string
	// MCP enables /mcp and tells the transcript which rows are server calls.
	MCP MCP
	// ToolSources are the tools screen's readings.
	ToolSources ToolSources
	// Safety is what /safety reads that only the command package can.
	Safety Safety
	// Scaffold enables the scaffolding offer and /init.
	Scaffold Scaffold
	// Processes enables /ps and process-start approval.
	Processes Processes
	// Gate enables /gate.
	Gate Gate
	// ConfigScreen is what `/config` opens, and ConfigWriter makes a setting
	// stick. A session without either still runs, and says so rather than
	// drawing a screen with nothing behind it.
	ConfigScreen ConfigOpener
	ConfigWriter ConfigWriter
	// ModelOptions is what the bare /model picker offers, and ModelLister the
	// live discovery that replaces it once asked; SwitchModel makes the next
	// request use the model picked.
	ModelOptions []string
	ModelLister  func(context.Context) ([]string, error)
	SwitchModel  func(string)
	// Effort is the reasoning level the session starts on and SwitchEffort
	// makes a change reach the next request (nil is display-only);
	// EffortDefault and EffortOutranked are the persisted level and whatever
	// outranks it.
	Effort          provider.Effort
	SwitchEffort    func(provider.Effort)
	EffortDefault   string
	EffortOutranked string
	// EndpointWindows is the endpoint's own answer for a model's context
	// length, which outranks the pricing table.
	EndpointWindows func(string) (int64, bool)
	// Ledger is what every request made through the provider gate cost; nil
	// leaves the totals on the main agent's own figures.
	// See docs/architecture.md#spend-is-counted-at-the-provider.
	Ledger *meter.Ledger
	// Prices is the price table the session's spend is read against.
	Prices *pricing.Table
	// Sessions backs /sessions.
	// See docs/capabilities/sessions-and-memory.md#a-session-knows-it-is-not-alone.
	Sessions func() string
	// NewSession is the half of a session boundary outside the screen: the
	// record closed and reopened, the prompt built again.
	NewSession NewSession
	// WorkspaceBlock answers with the checkout reading a rebuilt
	// conversation is given, as the tree stands when it is called.
	WorkspaceBlock func() string
	// FetchWaiting and AbandonFetchWaits are the fetcher's two answers about
	// a paced fetch: how much of a host's refusal is still being sat out, and
	// how to give those waits up.
	// See docs/capabilities/evidence.md#a-site-is-read-at-the-pace-it-answers.
	FetchWaiting      func(host string) (time.Duration, bool)
	AbandonFetchWaits func()
	// Start is the start screen's first-contact facts. Without it the empty
	// session keeps the plain welcome line; a conversation draws none.
	Start *StartInfo
	// Ask says the session registered the question tool, which is the only
	// condition under which its card is ever drawn
	// (docs/capabilities/coding-agent.md#nobody-to-ask).
	Ask bool

	// --- The conveniences.

	// MouseOff, NotifyOff and WindowTitleOff turn off what is on unless the
	// config says otherwise.
	MouseOff       bool
	NotifyOff      bool
	WindowTitleOff bool
	// Verbosity is the rung the session starts on. A word the ladder does not
	// have starts it on normal rather than refusing it.
	Verbosity string
	// RailWidth fixes the rail's column count; zero leaves it to the ladder.
	RailWidth int
	// PasteLines and PasteColumns are the shape past which a paste is staged;
	// zero on either keeps that half at its default.
	PasteLines   int
	PasteColumns int
}
