// Package subagent orchestrates child agents for `shhh code`: the
// model delegates scoped work to background children via spawn_agent, each
// child is a full internal/agent instance driven by the headless loop, and
// everything consequential a child wants to do — commands, edits, its final
// patch — routes back to the parent session's user for approval. Writer
// children work against an isolated git worktree; their changes only reach
// the real checkout as a user-approved patch.
package subagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/diff"
	"github.com/rfizzle/shhh/internal/digest"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/radius"
	"github.com/rfizzle/shhh/internal/safety"
	"github.com/rfizzle/shhh/internal/scope"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/web"
)

// Role scopes a child's toolset: researchers get read-only tools plus the
// web, writers get the full toolset against an isolated worktree.
type Role string

const (
	RoleResearcher Role = "researcher"
	RoleWriter     Role = "writer"
	// RoleReviewer reads a change and judges it: read-only tools, plan
	// mode so the restriction is visible to the child itself, no
	// worktree because it changes nothing. The backlog runner spawns one
	// for every review it does not do itself.
	RoleReviewer Role = "reviewer"
)

// Hard budgets and bounds. Concurrency and per-child budgets are deliberately
// bounded: a runaway parent cannot fan out or spend without limit.
const (
	// DefaultMaxConcurrent children run at once; further spawns queue.
	DefaultMaxConcurrent = 3
	// MaxChildren caps how many children one session may spawn in total.
	MaxChildren = 16
	// DefaultMaxRounds leaves a child's tool rounds unbounded. The
	// limit used to be a hard stop, and a child that reached one failed with
	// its work half done and nothing to hand over — the one outcome worse
	// than letting it run. It is a check-in now: the child takes stock and
	// carries on with a larger budget. That makes the number a pacing choice
	// rather than a safety one, and a pacing choice nobody asked for does not
	// belong on by default. The token budget below is the guard, and it is
	// the one that should be: it stops a child whatever it happens to be
	// doing, and it counts the thing a round cap was reaching for.
	//
	// A spawn may still name max_rounds to get periodic check-ins, at any
	// size. Nothing clamps it: the cap only decides how often a child pauses
	// to take stock, so a ceiling could only make it check in more often than
	// it was told to, for no reason that could be given to whoever set it.
	DefaultMaxRounds = agent.UnlimitedToolRounds
	// ChildCheckInInterval is how many rounds pass before a child is asked to
	// take stock. It is shorter than a session's because a child has less of
	// everything else watching it: it runs uncapped by the decision above, and
	// there is nobody in front of it. For a child whose readings are turned
	// off this check-in is the only question it will ever be put, and a
	// session's interval — chosen for a turn that also has a cap and a reader
	// — would be the wrong number to inherit.
	//
	// Twenty-five is the figure the budget check-in above is already reasoned
	// against, and for the same reason: often enough early to catch a child
	// working on the wrong thing, rare enough later to stay out of the way.
	// The interval widens the same way.
	ChildCheckInInterval = 25
	// checkInGrowth multiplies the budget at each check-in, so a long task is
	// not stopped at the same interval forever — the escalation behind the
	// parent's grant, applied by a child with nobody to ask. A child
	// capped at 25 takes stock at 25, then 50, then 100: often enough early
	// to catch one working on the wrong thing, rare enough later to stay out
	// of the way of one that is not.
	checkInGrowth = 2
	// DefaultMaxTokens and MaxTokensCeiling bound a child's attention, not
	// its spend. What they count is fresh tokens: the part of each prompt
	// the provider did not serve from its cache, plus the completion.
	//
	// The two come apart as soon as the prompt is cached, which it is by the
	// second request of every child. Counting the whole prompt charged the
	// same instruction block again on every round, so a child died of having
	// re-read a prefix nobody paid full price for — six requests on a
	// repository with a large instruction file — while a child that filled
	// its window with tool output looked cheap. Fresh tokens are what the
	// child has actually taken in, which is the thing worth bounding in
	// something with nobody watching it. Money is the ledger's business and
	// the session spend cap's, and both count a child's requests already.
	DefaultMaxTokens  = 200_000
	MaxTokensCeiling  = 1_000_000
	minChildMaxTokens = 1_000
)

// State is a child's lifecycle state.
type State int

const (
	StateQueued State = iota
	StateRunning
	StateBlocked // waiting on the parent user's approval
	StateIdle    // turn cancelled; waiting for a steering message
	StateDone
	StateFailed
)

func (s State) String() string {
	switch s {
	case StateQueued:
		return "queued"
	case StateRunning:
		return "running"
	case StateBlocked:
		return "blocked"
	case StateIdle:
		return "idle"
	case StateDone:
		return "done"
	default:
		return "failed"
	}
}

// Status is one child's live snapshot, for progress rows and agent_report.
type Status struct {
	Name      string
	Role      Role
	Task      string
	Model     string
	Paths     []string
	State     State
	Detail    string
	ToolCalls int
	TokensIn  int64
	TokensOut int64
	// Batch groups the children one parent tool round spawned, so a fan-out
	// can be rendered as one block rather than as interleaved rows.
	// Children spawned before the parent opened a batch share batch zero.
	Batch int
	// Started is when the child was spawned; Elapsed is how long it has been
	// alive, frozen at the moment it finished.
	Started time.Time
	Elapsed time.Duration
	// Step and Steps are progress against the step count the spawn declared.
	// Steps is zero when nobody declared one, and a lane with no denominator
	// gets a spinner rather than an invented ratio.
	Step  int
	Steps int
	// Summary is the first line of the child's final report — what a finished
	// lane keeps once its progress stops meaning anything. Empty
	// until the child reports.
	Summary string
	// CheckIns is how many times the child has reached its round limit and
	// taken stock. Zero for the ordinary child, which runs unbounded
	// and never reaches one; a lane showing several is a task outgrowing the
	// interval its spawn chose, which is worth being able to see.
	CheckIns int
	// Steers is how many times this turn the child has been told it looks to
	// have left its task. It is the count and not a flag because one steer is
	// the machinery working — a child told once and back on task is the case
	// this was built for — and a second is the case nothing above the child
	// could see before: an interruption delivered, answered, and the next
	// reading finding the same departure. It goes back to zero at the child's
	// next turn, so what it reports is a child that is not answering now.
	Steers int
	// Verdict is the last reading of this child's work, in the summariser's
	// own closed vocabulary and never its prose. Empty is a child with no
	// reading yet — one in its first interval, or one whose session turned
	// readings off, which is what the roster's own header exists to say.
	Verdict string
	// End is how this child's attempt stopped, from the closed set in
	// internal/observe, and empty while it is still running. It is the
	// reason rather than the state: "failed" is what a lane says, and a
	// budget spent, a kill and a provider that stopped answering are three
	// different answers to the only question anybody asks of a fan-out
	// afterwards.
	End string
	// SteerFrom is where the last message put in front of this child came
	// from. Empty is a child nobody and nothing has redirected. It is the
	// last steer's own source rather than the turn's, so it outlives the
	// turn boundary a steer opens — for an idle child a steer *is* the next
	// turn's instruction, and a source cleared at that boundary would be
	// erased exactly where it is worth reading.
	//
	// It is beside the count rather than folded into it because the count
	// answers whether the child is answering its reader and this answers who
	// spoke to it last: a child steered twice by its own reading and once by
	// the orchestrator is not the same as one steered three times by its
	// reader, and only this tells them apart.
	SteerFrom SteerSource
	// Seeded is how many of the parent's uncommitted paths the child's
	// worktree was started from. Zero is a reader, a writer started from a
	// checkout with nothing uncommitted in it, or a writer that has not
	// started yet — a queued one has no copy of the tree to have been
	// seeded from, because the copy is taken when its slot comes free.
	Seeded int
	// Held is whether the child has reached its own round boundary while the
	// parent's hold stands. It rides beside the state rather than replacing
	// it because a held child is still a running one — it keeps its slot,
	// its worktree and its conversation, and one release puts it back to
	// work with the round it was about to ask for.
	Held bool
}

// EntryKind tags one child transcript entry: the attached view
// renders a child's session with the same components as the orchestrator's.
type EntryKind int

const (
	EntryUser EntryKind = iota
	EntryAssistant
	EntryTool
	EntrySystem
)

// TranscriptEntry is one item of a child's live transcript. Entries are
// append-only; a tool entry is appended when the call starts (Pending) and
// completed in place when its result lands, so indices stay stable for the
// front-end's expansion state.
type TranscriptEntry struct {
	Kind EntryKind
	Text string // user / assistant / system entries
	Tool string // EntryTool: tool name
	Args string // EntryTool: raw arguments
	// Result is a tool entry's result text, and a system entry's fold
	// expansion — the long form of a notice whose row is the short one.
	Result  string
	Pending bool // EntryTool: still executing or awaiting approval
	// AllowedBy names what let a gated call run without the parent being
	// asked — the mode machine's own word for the rule it matched, or the
	// classifier — and rides on the act's own row. A feed states an act
	// once, so the account of an auto-approval is a field of the call it
	// approved rather than a notice above it repeating the call's verb and
	// its target. Empty on a call the parent answered at the card, and on
	// every call that was never gated.
	AllowedBy string
	// AllowElapsed is what that judgement took, where it took anything,
	// which is the classifier and nothing else.
	AllowElapsed time.Duration
}

// Env is everything a child needs to run, assembled by the CLI so this
// package stays free of provider and config plumbing.
type Env struct {
	// SystemPrompt is the child's role-specific system prompt.
	SystemPrompt string
	// Stream opens completion streams bound to the child's context and
	// role-scoped tool definitions.
	Stream agent.StreamFunc
	// Executor runs the child's auto-run (non-gated) tools, already rooted at
	// the child's workspace and wrapped in output reduction.
	Executor agent.ToolExecutor
	// ExecuteGated runs approved non-exec gated calls (file mutations,
	// web_fetch); the supervisor roots the arguments before calling it.
	ExecuteGated agent.ToolExecutor
	// RunCommand executes an approved shell command in the child's workspace
	// (contained when a mechanism is available).
	RunCommand func(ctx context.Context, command string) (output string, exitCode int)
	// Reduce runs a command's output through the session's reduction
	// pipeline before it becomes the child's tool result: a head, a tail,
	// every line that names an error or a failure, and an id that pages the
	// whole output back out of the evidence store. Nil reduces nothing,
	// which leaves a child reading the first few kilobytes of a long
	// command and nothing after them — for a test run, forty passing
	// packages and never the verdict.
	//
	// It is a field rather than a wrap around RunCommand because the
	// reduction sits between the scrub and the format: the store keeps the
	// text the child was shown, and reducing an already-formatted result
	// would let the notice count the exit-code line as output.
	// See docs/capabilities/evidence.md#reduction-is-for-unbounded-output.
	Reduce func(tool, result string) string
	// KeepResult reports a tool result the window trim must leave where it
	// is. A result is elided on the assumption that it was consumed when it
	// arrived; a skill's instructions are the exception, because they are
	// what the rest of the child's work is meant to follow — and losing them
	// fails silently, since the child carries on without them. Nil keeps
	// nothing back.
	KeepResult func(content string) bool
	// Archive is where a tool result goes just before the trim replaces it,
	// answering with the id the placeholder then names, so what left the
	// child's window is still somewhere the child's own evidence tool can
	// ask for. False is a result that could not be kept and the trim goes
	// ahead with the bare placeholder: a child at the end of its window is
	// exactly the child that most needs the room back. Nil makes elision
	// permanent.
	// See docs/capabilities/evidence.md#a-trim-makes-the-same-promise.
	Archive func(tool, content string) (string, bool)
	// Gated names the tools that must go through approval routing.
	Gated map[string]bool
	// Scrub, when set, is installed on the child's agent so its
	// conversation never holds a session secret; nil scrubs nothing.
	Scrub func(provider.Message) provider.Message
	// Summarizer, when set and enabled, takes periodic readings of the child
	// so a run that has drifted or that already has what it needs is
	// interrupted the way a session is. Nil takes no readings, which is what
	// summary.subagents=false leaves behind: a child nothing reads is never
	// listed as steered, so the roster says so rather than leaving the parent
	// to read an empty field as good news.
	Summarizer *agent.Summarizer
	// Steering is the interruption machinery's tuning as the config file
	// left it. A child runs the same machinery a session does, so the same
	// wordings and thresholds reach it — all but the interval, which is the
	// surface's own.
	Steering agent.Steering
	// Retries bounds a child's stalls as the config file left it; nil keeps
	// the built-in bound. A fan-out is where waiting one out matters most,
	// because a limit refuses every child at once.
	Retries *int
	// Window is the child model's context window as the downloaded price
	// table answers it, and 0 where the table has no row for the model. The
	// table is asked in the CLI because that is where it is open, and it is
	// asked first: the family floor this package can reach on its own is a
	// conservative reading of a model name, so a child on a model the table
	// knows would otherwise recover its window against a smaller number than
	// it has, or — for a name no family matches — against nothing at all.
	Window int64
	// ToolTokens is what the child's registered definitions cost on every
	// request. They are not in the conversation, so a child that left them
	// out of the estimate would think it had a toolset's worth of room it
	// does not have — and a child's toolset is now most of a session's.
	ToolTokens int64
	// WrapAuto and WrapGated put the surface's own seams around the child's
	// two tool dispatchers: the calls that run on their own, and the calls
	// that are decided about. They are what carries the person's own
	// commands into a child — a rule that stopped at the session would be a
	// rule anybody could walk around by delegating the act
	// (docs/capabilities/hooks.md#a-hook-fires-in-a-child-too).
	//
	// They wrap rather than replace: what comes back runs the dispatcher it
	// was handed, so a surface that installs nothing changes nothing. The
	// gated wrap goes around the whole resolution — the policy, the
	// classifier and the card included — which is where a session's own
	// approver meets the same seam, and which keeps the tier rule: a call is
	// dispatched by the kind of call it is, whatever a seam said about it.
	// Nil leaves the dispatcher exactly as this Env built it.
	WrapAuto  func(Seam, agent.ToolExecutor) agent.ToolExecutor
	WrapGated func(Seam, func(provider.ToolCall) string) func(provider.ToolCall) string
	// Sweeps is where this child's circling detector is asked what ground it
	// has been over without writing anything, for the digest its readings are
	// made of. The surface owns the detector because the surface is what
	// wrapped the child's two dispatchers with it, and a count taken anywhere
	// else would be counting a different window. Nil where the surface wired
	// no detector, which leaves the field out of the digest.
	//
	// A child is the surface this matters most on: it is the least
	// supervised thing a session runs, its rounds are spent out of sight, and
	// one that read for twenty-seven rounds and wrote nothing was called on
	// target by all three of its readings.
	Sweeps func() []string
	// TreeCheck, when set, is the reading that tells the child's turn its
	// workspace moved under it, as the surface configured it. Own is filled
	// in by this package rather than there: what the child has written is
	// the child's own record, and the reading has to subtract it or every
	// file the child edits comes back at the next boundary as somebody
	// else's work.
	//
	// A child is what the reading is for as much as a session is — it works
	// beside its siblings, beside the session that spawned it and beside
	// whoever else has the checkout open, and it is the party with nobody
	// watching its screen. Nil takes no reading.
	TreeCheck *agent.TreeCheck
}

// Seam is what a child's dispatchers hand whoever wraps them: where the child
// has got to, where a line about it goes on its own transcript, and where a
// verdict about one of its calls is filed.
//
// All three are the child's, and the child does not exist when the surface
// builds its Env — a wrap is built once for the surface and a Seam is passed
// per child, which is why these are arguments to the wrap rather than fields
// captured in it.
type Seam struct {
	// At is where the child has got to: which of its turns, and which round
	// of that turn.
	At func() observe.Pos
	// Note puts one line on the child's transcript. A child has no screen of
	// its own, and a surface that wrote to stderr instead would draw over the
	// session that spawned it.
	Note func(text string)
	// Record files a verdict at the codes a session records its own at. An
	// approval rate that covered the parent and not its children would be a
	// rate over the half of the work a person was looking at.
	Record func(decision, code string)
}

// autoExecutor is the child's auto-run dispatcher with the surface's seam
// around it, or the bare dispatcher where the surface installed none.
func (e Env) autoExecutor(s Seam) agent.ToolExecutor {
	if e.WrapAuto == nil {
		return e.Executor
	}
	return e.WrapAuto(s, e.Executor)
}

// execResult is a command's output as the child's tool result: reduced, then
// formatted. Every route from a child's command to tools.FormatExecResult
// runs through here, because the formatter on its own keeps the first
// MaxExecOutputBytes and drops the rest — and what a command has to say about
// itself is at the end.
func (e Env) execResult(output string, exitCode int) string {
	if e.Reduce != nil {
		output = e.Reduce(tools.ExecCommandName, output)
	}
	return tools.FormatExecResult(output, exitCode)
}

// childCompactor is a child's window-recovery step, or nothing where the
// window cannot be established. The price table's answer comes first, carried
// on the Env because that is where the table is open, and the family floor is
// the fallback — a name neither can place leaves the child running exactly as
// it did before, which is the cheaper of the two mistakes: recovering against
// a guessed window would throw away the work of a child that had most of its
// room left.
//
// The summary is asked of the child's own model. The one door a child has out
// is its stream, and a request on another model would have to be built
// somewhere that knows what the child's tools are.
func childCompactor(model string, env Env) *agent.Compactor {
	window := env.Window
	if window <= 0 {
		window, _ = provider.ContextWindowFor(model)
	}
	if window <= 0 {
		return nil
	}
	return &agent.Compactor{Model: model, Window: window, ToolTokens: env.ToolTokens}
}

// roundCap is the cap a child's agent is running under as the record spells
// it: the number, or 0 for none. MaxRounds alone cannot say the second,
// because it answers with the default for an uncapped agent.
func roundCap(a *agent.Agent) int {
	if a.Uncapped() {
		return 0
	}
	return a.MaxRounds()
}

// newChildAgent builds a child's agent with everything a child's agent needs.
//
// It exists because there are two paths to one — a spawn and a retry — and a
// setting added to the first quietly does not reach the second. That already
// happened once: a retried child would have inherited a session's check-in
// interval, and the only symptom would have been a long child nobody asked
// anything, which is precisely the failure the interval exists to prevent and
// precisely the one that leaves no trace.
func newChildAgent(env Env, maxRounds int) *agent.Agent {
	a := agent.New([]provider.Message{{Role: provider.RoleSystem, Content: env.SystemPrompt}}, env.Stream)
	a.SetMaxRounds(maxRounds)
	a.SetSteering(env.Steering)
	// After the steering, and deliberately: the configured interval is a
	// session's, and a child has none of what makes a session's long one
	// safe. Everything else in the set — the wordings, the widening, what a
	// steer quotes — is the same question asked of the same machinery.
	a.SetCheckInInterval(ChildCheckInInterval)
	// And its exit, in the same place and for the same kind of reason: a
	// child has no person to say "the work is finished" to, and one that says
	// it into its own transcript carries on reading. Its final report is what
	// ends its turn, so that is what its check-ins point at — every route to
	// one, not just the round cap that used to name it.
	a.SetFinished(agent.FinishedAsSubAgent)
	// And both halves of what a trim promises, here for the same reason the
	// interval and the exit are. A child's window is recovered at every round
	// boundary (childCompactor) rather than ahead of a person's request, so it
	// trims far more often than a session does, and there is nobody watching
	// it to notice a finding gone or a skill's instructions stop being
	// followed.
	if env.KeepResult != nil {
		a.KeepResults(env.KeepResult)
	}
	if env.Archive != nil {
		a.StoreElided(env.Archive)
	}
	if env.Scrub != nil {
		a.SetScrub(env.Scrub)
	}
	return a
}

// Spec is everything the CLI needs to build one child's runtime: its role,
// its working directory, and the model it runs on (already resolved by the
// supervisor's ModelFor).
type Spec struct {
	// Name is the child's session-unique name. It is in the spec because a
	// fan-out bills several children at once, and what a child spends is
	// only attributable if the thing building its environment knows which
	// child it is building for.
	Name  string
	Role  Role
	Root  string
	Model string
	// Paths is the writer's declared write scope, so its prompt can say what
	// it may touch while other agents work elsewhere; nil means unscoped.
	Paths []string
	// Worktree reports that Root is an isolated checkout seeded from the
	// parent's tree rather than the parent's own directory. What the child
	// is told about the workspace turns on it: git in there answers about a
	// detached seed commit and a clean tree, so a child handed the parent's
	// branch and dirty count without being told where it is standing would
	// read its own `git status` as a contradiction and go looking for the
	// changes it was promised.
	Worktree bool
	// Mode is the permission mode the child starts in, after the profile
	// and the clamp to the parent have had their say, and MaxRounds its
	// per-turn round cap (0 when it has none). Neither builds the runtime —
	// the supervisor holds both — but the record of what the child ran
	// under has to be stamped from somewhere, and the CLI is what stamps it.
	Mode      agent.Mode
	MaxRounds int
	// Attempt is which run of this child the row being opened is, from 1. A
	// retry keeps the child's name and its place in the batch but is a
	// separate run with its own conversation, budget and spend, so it gets a
	// row of its own — and this number is the only thing that joins that row
	// to the one it replaces.
	Attempt int
}

// EnvFactory builds a child's Env; ctx is the child's context (cancelling it
// must abort the child's streams).
type EnvFactory func(ctx context.Context, spec Spec) (Env, error)

// Recorder is how a child reports itself: the observer contract every
// surface reports through, plus the end of its own session row. End is here
// and not on the contract because a child's life begins and ends inside the
// supervisor — there is no caller outside holding a defer that could close
// the row for it.
type Recorder struct {
	observe.Observer
	// End closes the attempt's row with how it ended. The end is a value
	// rather than nothing because a child's row is the one place the answer
	// to "was the budget right" can be assembled: the reason it stopped, the
	// last reading of its work and the steers it took sit beside the tokens
	// it spent, on the same row, for the same attempt.
	End func(observe.ChildEnd)
}

// Options configures a Supervisor.
type Options struct {
	// Root is the parent session's workspace directory.
	Root string
	// NewEnv builds each child's runtime; required.
	NewEnv EnvFactory
	// Record opens a child's observability recorder; nil disables recording.
	// It takes the whole spec because a child's spend has to be recorded
	// against the model the child actually ran on — which is resolved per
	// child and is routinely not the session's — and the system prompt as
	// sent, because a child's provenance is its own: it routinely runs a
	// different model under a different prompt, and a row inheriting the
	// parent's would say it ran under something it did not.
	Record func(spec Spec, sysPrompt string) Recorder
	// CommandAllowlist is the parent's config allowlist, inherited by
	// children (inheriting it keeps the child at most as permissive).
	CommandAllowlist []string
	// CommandDenylist is the parent's config deny list, inherited for the
	// opposite reason: a refusal that stopped at the session is a refusal a
	// fan-out walks around, and a child has no card to draw and nobody to
	// draw it for.
	CommandDenylist []string
	// AllowHosts and DenyHosts are the parent's config host lists
	// (web.allow_hosts, web.deny_hosts), inherited for the two reasons the
	// command lists are: what the person answered for once is answered for
	// every agent, and what they refused is refused for every agent.
	AllowHosts []string
	DenyHosts  []string
	// ReadOnlyExtra and ReadOnlyDisabled mirror the parent's read-only
	// inspection allowlist settings, so a child's reads are as quiet as the
	// parent's.
	ReadOnlyExtra    []string
	ReadOnlyDisabled bool
	// ScopeDirs reports the parent session's working scope — the
	// directories a child's commands may write to on top of its own
	// worktree. Nil leaves children scoped to their worktree alone, which is
	// what they did before the scope existed. A child's *file edits* are
	// pinned to its worktree by RootArgs regardless.
	ScopeDirs func() []string
	// Classifier judges, in auto mode, what the static policy would ask about
	// — the same classifier path the parent uses. Nil routes those calls to the
	// user instead, which is what made auto-mode children prompt for every
	// command they ran.
	Classifier *agent.Classifier
	// ModelFor resolves a child's model from its role and the model the
	// spawn call asked for (empty when it asked for none). Nil means every
	// child runs on the session model.
	ModelFor func(role Role, requested string) string
	// Profiles is the set of roles a spawn may name; nil means the two
	// built-in ones.
	Profiles Profiles
	// MaxConcurrent bounds simultaneously running children; <= 0 uses
	// DefaultMaxConcurrent.
	MaxConcurrent int
	// Untracked reports the files the parent session created that git does
	// not know about, so a writer's worktree can start from them alongside
	// everything `git diff HEAD` already reports. It is asked at each spawn,
	// because a session goes on writing while its children run. Nil carries
	// none, which is a writer starting from the last commit.
	Untracked func() []string
}

// EventKind tags a supervisor event.
type EventKind int

const (
	// EventUpdate is a status change (progress rows re-render).
	EventUpdate EventKind = iota
	// EventAsk is an approval request routed to the parent user.
	EventAsk
	// EventDone marks a child finished (done or failed).
	EventDone
	// EventPatch reports a child's patch landing in the parent's workspace,
	// with both sides of every file it touched, so the parent can record it
	// in the session changeset.
	EventPatch
)

// Event is one supervisor notification for the parent front-end.
type Event struct {
	Kind   EventKind
	Ask    *Ask
	Status Status
	Patch  *PatchApplied
}

// PatchApplied is what a child's applied patch changed, file by file. The
// parent's changeset store is the only reader: a child's edits happen in an
// isolated worktree, so this — the moment the patch lands on the real
// checkout — is when the session actually changed.
type PatchApplied struct {
	Agent string
	Files []PatchedFile
}

// PatchedFile is one file of an applied patch, read from the real checkout
// either side of `git apply`. Exists distinguishes an empty file from one the
// patch created or removed.
type PatchedFile struct {
	Path                      string
	Before, After             string
	BeforeExists, AfterExists bool
	// BeforeMode is the permission bits the file had when the patch found
	// it, zero where there was no file to have any. It is what puts a script
	// the patch deleted back executable rather than at the default: once the
	// file is gone, nothing else on disk remembers that it was one.
	// See docs/capabilities/coding-agent.md#a-turn-ends-with-what-changed.
	BeforeMode os.FileMode
	// AfterMode is the same reading taken once the patch has landed, and it
	// is the whole of a patch that changed a mode and not a byte: git
	// carries one as an `old mode`/`new mode` header with no hunk, so both
	// sides hold identical content and this pair is the only thing that
	// tells them apart. It is read here because this is the one moment the
	// mode can still be seen at all; the session changeset takes both sides
	// and undo puts the old one back.
	AfterMode os.FileMode
}

// AskKind selects the approval card a routed request renders with.
type AskKind int

const (
	AskCommand AskKind = iota
	AskEdit
	AskGeneric
	AskPatch
)

// Ask is one child approval request routed into the parent's approval flow:
// never silently parked, never auto-denied — the child blocks until the user
// answers (or the child is cancelled).
type Ask struct {
	// Agent is the child's name, shown as the card label.
	Agent    string
	Kind     AskKind
	Title    string
	Command  string      // AskCommand: the command text
	Warnings []string    // AskCommand / AskPatch: safety.Check risks, patch clashes
	Hunks    []diff.Hunk // AskEdit / AskPatch: the change to review
	Summary  string      // AskGeneric: one-line description
	Path     string      // AskEdit: the file, spelled as the child's own row spells it

	// The rest is what the parent's approval card needs to say more about a
	// routed request than its title. A card that only says what the action
	// *is* asks the reader to do the risk assessment themselves, at speed,
	// and a child's request is the one where they have least to go on — so
	// the ask carries where its paths live, and the card resolves the blast
	// radius against that rather than against the parent's own checkout.

	// Root is the directory the ask's relative paths are measured from: the
	// child's own working directory for a call it is about to make, and the
	// parent's checkout for a patch, which is the tree a patch lands in.
	Root string
	// Worktree says Root is an isolated checkout rather than the reader's
	// own files. It is the difference between an approval that changes the
	// reader's workspace now and one that changes a copy they will be shown
	// as a patch afterwards, and nothing else on the ask carries it.
	Worktree bool
	// Files are the paths an AskPatch writes in the parent's checkout, as
	// git names them. They are the patch's blast radius: unlike an edit,
	// whose diff is the whole of it, a patch's diff can be longer than the
	// panel and the count is what survives the fold.
	Files []string

	once sync.Once
	resp chan bool
}

// NewAsk builds an approval request; the supervisor uses it internally and
// front-end tests use it to drive the routing surface.
func NewAsk(agentName string, kind AskKind, title string) *Ask {
	return &Ask{Agent: agentName, Kind: kind, Title: title, resp: make(chan bool, 1)}
}

// Respond records the user's decision. Safe to call more than once; only the
// first decision counts.
func (a *Ask) Respond(approved bool) {
	a.once.Do(func() { a.resp <- approved })
}

// Answered non-blockingly consumes the recorded decision, for tests.
func (a *Ask) Answered() (approved, ok bool) {
	select {
	case v := <-a.resp:
		return v, true
	default:
		return false, false
	}
}

// child is one sub-agent: an internal/agent instance plus its runtime and
// live status.
type child struct {
	name     string
	parent   string // spawning agent's name; "" means the orchestrator
	role     Role
	task     string
	profile  Profile  // what the role means: worktree, patch, mode, budgets
	model    string   // the model this child runs on
	paths    []string // declared write scope (writers); nil means unscoped
	batch    int      // the parent tool round that spawned it
	steps    int      // step count the spawn declared; 0 means none
	root     string   // working directory (worktree subdir for writers)
	worktree string   // worktree top dir; "" for researchers
	repoTop  string   // parent repo toplevel; "" for researchers
	seeded   int      // parent paths the worktree was started from
	// maxRounds is the per-turn round cap the next agent built for this
	// child is given. It is the child's own field rather than a reading of
	// the agent because there is not always an agent to ask: a writer's is
	// built when its slot comes free, and a retry has to know the cap the
	// attempt before it grew to at a moment when nothing is running.
	maxRounds int
	maxTokens int64

	ctx      context.Context
	cancel   context.CancelFunc
	agent    *agent.Agent
	headless *agent.Headless
	env      Env
	rec      Recorder
	done     chan struct{}
	// steerWake nudges an idle child that new steering arrived (buffered 1).
	steerWake chan struct{}

	mu      sync.Mutex
	mode    agent.Mode
	state   State
	detail  string
	started time.Time
	ended   time.Time
	step    int // announcements made, i.e. steps entered
	// turns counts the turns this attempt has run, so a child's events are
	// placed the way a session's are: a tool call in round 30 of turn 3 is a
	// different fact from the same call in round 2 of turn 1.
	turns     int
	toolCalls int
	// wrote is the set of files this attempt's own mutating calls have
	// written, which is what its readings are told the child has changed.
	//
	// It is counted off the calls rather than read from the parent's
	// changeset because a writer edits in an isolated worktree and the parent
	// learns nothing until the patch lands, which is after the last reading
	// this child will ever take. A digest of twenty-four rows that are all
	// reads, taken while five files have been rewritten, is the evidence that
	// makes a reader call a child sufficient when it is in the middle of
	// acting.
	wrote     map[string]bool
	tokensIn  int64
	tokensOut int64
	// fresh is what the token budget is measured against: tokensIn less the
	// part every prompt was served from the provider's cache, plus
	// tokensOut. It is a second counter rather than a narrower tokensIn
	// because the two answer different questions — every surface that prices
	// a child, and the session row it writes, wants the tokens it was billed
	// for, and only the budget wants the tokens it took in.
	fresh int64
	// priorIn/priorOut carry the spend of earlier attempts across a retry.
	// The live counters are the attempt's own, as fresh is, so each attempt
	// gets the budget it was spawned with, and they are what that attempt's
	// own session row is told. The status adds the carried spend back,
	// because money already spent does not stop being spent when the child
	// runs again and a lane shows one child rather than one attempt.
	priorIn  int64
	priorOut int64
	// attempt is which run of this child is current, from 1. It is the
	// child's rather than the attempt's for the reason the carried spend is:
	// a lane shows one child, and only the record separates its attempts.
	attempt   int
	budgetHit bool
	// killed marks a child a person ended from the manager. Both a kill and
	// a session shutting down reach the child as a cancelled context, and
	// the record must not report them as the same thing: one is somebody
	// deciding a child was not worth finishing, and the other is the child
	// having been going fine when the process left.
	killed bool
	// endReason is how this attempt stopped, from the closed set in
	// internal/observe, and empty until it does.
	endReason string
	checkIns  int
	// steers is what Status.Steers reports and verdict what Status.Verdict
	// does. They are the child's own copies under this lock rather than
	// readings of the agent: a status is taken from whichever goroutine asked
	// — the parent's tool call, the lane's next frame — and reaching into the
	// loop's own state from there is a race. The lock is owed on the writing
	// side too, and by more than the run's goroutine: the reading that sets
	// verdict can land after the run it was reading has returned.
	steers int
	// steersAll is every steer this attempt has been given, which the record
	// takes; steers above is the current turn's, which the lane shows.
	steersAll int
	verdict   string
	// verdictCode is the same reading in the record's own closed vocabulary,
	// which verdict above is not: that one is the wording the roster shows a
	// person, and a column filled from it would hold a second spelling of
	// every state the rest of the record already has a word for — and would
	// change spelling the day somebody reworded a lane.
	verdictCode string
	steerFrom   SteerSource
	report      string
	patchNote   string
	// prologue is what the next attempt's first turn opens with, ahead of
	// the task: what the attempt it replaces hit, and the handoff it left.
	// It is a field rather than an argument to run because a retry can be
	// started from three places and none of them is the goroutine that will
	// read it, and it is taken once, so a second turn on the same attempt is
	// the ordinary conversation.
	prologue string
	// Live session surface: transcript entries, the in-flight
	// assistant text, queued steering messages, and the current turn's
	// interrupt channel.
	transcript []TranscriptEntry
	// callRow is the transcript row each live tool call has open, keyed by
	// the call's own id — the same id the conversation routes its result by.
	// A round's reads run concurrently, so several rows are open at once and
	// nothing else tells them apart: a result settles the row its own call
	// opened, and a decision taken while a call runs — the account of an
	// auto-approval — lands on that call's row rather than on a notice above
	// it. A call whose row is missing settles nothing rather than the first
	// row it finds.
	callRow   map[string]int
	streaming string
	steering  []queuedSteer
	intCh     chan struct{}
	intClosed bool
	// heldOn is the hold this child is parked on, and nil when it is not
	// parked. It is separate from state and detail rather than a state of its
	// own, because a held child is still running in every sense the lifecycle
	// cares about — it holds its slot, its worktree and its conversation, and
	// one release puts it straight back to work.
	//
	// It is the channel and not a flag so that a release can tell its own
	// hold from a later one: a hold taken again while a release is still
	// working through the children would otherwise have its freshly parked
	// child un-marked by the release before it, and the rail would report a
	// child as running that is going nowhere.
	heldOn chan struct{}
}

func (c *child) set(state State, detail string) {
	c.mu.Lock()
	c.state = state
	c.detail = detail
	// Any transition ends a hold. A child sits in its wait between two of
	// these, so nothing that is still parked passes through here — but a
	// killed one comes out of the wait by a route the release never took,
	// and a finished lane still reading "held · waiting for release" would
	// be offering a release that can no longer do anything.
	c.heldOn = nil
	// A finished child's elapsed stops moving: its lane reports what the work
	// took, not how long ago it happened.
	switch state {
	case StateDone, StateFailed:
		if c.ended.IsZero() {
			c.ended = time.Now()
		}
	}
	c.mu.Unlock()
}

// park marks the child as waiting on hold.
func (c *child) park(hold chan struct{}) {
	c.mu.Lock()
	c.heldOn = hold
	c.mu.Unlock()
}

// unpark takes the child off hold, but only off the one being released, and
// reports whether that changed anything — so a release neither emits an
// update for every child that never reached its boundary nor un-marks one
// that has since parked on a hold taken after this release began.
func (c *child) unpark(hold chan struct{}) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.heldOn != hold {
		return false
	}
	c.heldOn = nil
	return true
}

func (c *child) status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	end := c.ended
	if end.IsZero() {
		end = time.Now()
	}
	summary := ""
	if c.state == StateDone {
		summary = firstLine(c.report)
	}
	detail := c.detail
	if c.heldOn != nil {
		detail = "held · waiting for release"
	}
	return Status{
		Name:      c.name,
		Role:      c.role,
		Task:      c.task,
		Model:     c.model,
		Paths:     c.paths,
		State:     c.state,
		Detail:    detail,
		ToolCalls: c.toolCalls,
		TokensIn:  c.priorIn + c.tokensIn,
		TokensOut: c.priorOut + c.tokensOut,
		Batch:     c.batch,
		Started:   c.started,
		Elapsed:   end.Sub(c.started),
		Step:      min(c.step, c.steps),
		Steps:     c.steps,
		Summary:   summary,
		CheckIns:  c.checkIns,
		End:       c.endReason,
		Steers:    c.steers,
		Verdict:   c.verdict,
		SteerFrom: c.steerFrom,
		Seeded:    c.seeded,
		Held:      c.heldOn != nil,
	}
}

// attemptSpend is what the attempt now running has cost, which is what that
// attempt's own session row is told — deliberately not the carried pair the
// status reports. A retry opens a second row for the same child and leaves
// the first one holding the failed attempt's spend; a row update sets
// absolute totals, so handing the new row the carried figure would write
// that spend onto both rows and count it twice. A writer that burns 50k,
// fails, is retried and burns 30k would leave two rows summing to 130k for
// 80k of real work, and the cost derived from those tokens inflates with
// them.
func (c *child) attemptSpend() (in, out int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tokensIn, c.tokensOut
}

// takePrologue returns what this attempt's first turn opens with and clears
// it, so only that turn carries it.
func (c *child) takePrologue() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	p := c.prologue
	c.prologue = ""
	return p
}

// appendEntry adds one transcript entry.
func (c *child) appendEntry(e TranscriptEntry) {
	c.mu.Lock()
	c.transcript = append(c.transcript, e)
	c.mu.Unlock()
}

// flushStreaming commits accumulated streamed text as an assistant entry.
func (c *child) flushStreaming() {
	c.mu.Lock()
	if c.streaming != "" {
		c.transcript = append(c.transcript, TranscriptEntry{Kind: EntryAssistant, Text: c.streaming})
		c.streaming = ""
	}
	c.mu.Unlock()
}

// beginToolEntry appends a pending tool entry, flushing any streamed text
// first (the round's assistant text precedes its calls), and opens the row
// under the call's own id so settleToolEntry and noteAllowed find it again.
func (c *child) beginToolEntry(id, tool, args string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.streaming != "" {
		c.transcript = append(c.transcript, TranscriptEntry{Kind: EntryAssistant, Text: c.streaming})
		c.streaming = ""
		c.step++
	}
	c.transcript = append(c.transcript, TranscriptEntry{Kind: EntryTool, Tool: tool, Args: args, Pending: true})
	if c.callRow == nil {
		c.callRow = map[string]int{}
	}
	c.callRow[id] = len(c.transcript) - 1
}

// settleToolEntry records a call's result on the row it opened and closes
// that row, so a call that is over can no longer be written to.
func (c *child) settleToolEntry(id, result string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	idx, ok := c.callRow[id]
	if !ok {
		return
	}
	delete(c.callRow, id)
	c.transcript[idx].Result = result
	c.transcript[idx].Pending = false
}

// noteAllowed records on a call's own row what let it run without the parent
// being asked, and what that judgement cost. It is the child's half of the
// rule a session's feed follows: an act is stated once, so the account of an
// auto-approval is a field of the act rather than a row above it repeating
// the same verb and the same target.
// See docs/interface/surfaces.md#the-activity-row.
func (c *child) noteAllowed(id, rule string, elapsed time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if idx, ok := c.callRow[id]; ok {
		c.transcript[idx].AllowedBy, c.transcript[idx].AllowElapsed = rule, elapsed
	}
}

// noteWrite records a file this child's own call wrote. A call that came back
// an error wrote nothing, and a file written twice is one file — the same
// reading of a write every surface without a changeset takes.
func (c *child) noteWrite(call provider.ToolCall, result string) {
	path := tools.WrittenPath(call.Name, call.Arguments)
	if path == "" || digest.Outcome(result) == digest.OutcomeError {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.wrote == nil {
		c.wrote = map[string]bool{}
	}
	c.wrote[path] = true
}

// changed is this child's changeset for the digest its readings are made of:
// the files it has written, and no line counts, since nothing here reads a
// file either side of a write. It survives the turn boundary of a child
// given a second instruction — the worktree it wrote does — and is taken
// under the child's lock, since the reading that asks runs on its own
// goroutine.
func (c *child) changed() (files, added, removed int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.wrote), 0, 0
}

// budget is what the spend clock reads off this attempt: the fresh tokens it
// has taken in, and the budget those are measured against. It is the same
// pair addUsage compares, asked from the round boundary rather than from
// inside a response — a clock that only ticked where the budget is enforced
// would only ever fire on the round that killed the child.
func (c *child) budget() (spent, budget int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.fresh, c.maxTokens
}

// written is what this attempt has changed, as the check-in names it back.
//
// The paths are the model's own spelling and are deliberately not rooted the
// way ownPaths roots them: this goes back to the child that wrote them, and a
// writer standing in a worktree would be handed its own edits under a
// directory it has never seen. Sorted so two check-ins over the same set read
// the same, which a map's order does not give.
func (c *child) written() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(maps.Keys(c.wrote))
}

// ownPaths is what this attempt has written, in the tree the reading is taken
// in. A call names a path the way the model wrote it, and a writer's model is
// standing in a worktree this process is not, so a relative path is joined to
// the child's own root before it goes out — resolved against the process's
// directory it would name a file in the parent's checkout, and the reading
// would report the child's own edits as somebody else's.
func (c *child) ownPaths() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, 0, len(c.wrote))
	for p := range c.wrote {
		if !filepath.IsAbs(p) {
			p = filepath.Join(c.root, p)
		}
		out = append(out, p)
	}
	return out
}

// seam is what this child hands a surface wrapping one of its dispatchers.
// The recorder is read at the call rather than captured: a retried child is
// given a new one, and a wrap built for the first attempt goes on serving the
// second.
func (c *child) seam() Seam {
	return Seam{
		At:   func() observe.Pos { return c.pos() },
		Note: func(text string) { c.appendEntry(TranscriptEntry{Kind: EntrySystem, Text: text}) },
		Record: func(decision, code string) {
			if c.rec.Decision != nil {
				c.rec.Decision(c.pos(), decision, code)
			}
		},
	}
}

// watchTree turns this attempt's tree reading on, with the child's own writes
// as the subtrahend. The reading is taken where the child is standing, which
// for a writer is its worktree and not the checkout that worktree came from.
func (c *child) watchTree(a *agent.Agent, env Env) {
	if env.TreeCheck == nil {
		return
	}
	cfg := *env.TreeCheck
	cfg.Own = c.ownPaths
	a.SetTreeCheck(cfg)
}

// queuedSteer is one message waiting to join a child's conversation, with
// who sent it. The source travels with the message rather than being read off
// the child when it lands: two of them can be queued at once, from two
// different parties, and the record has to say which was which.
type queuedSteer struct {
	text string
	from SteerSource
}

// drainSteering pops all queued steering messages, appending each to the
// transcript as a user entry (they join the conversation now) and recording
// each in the session record by its source.
//
// The record is written here, where the message reaches the child, rather
// than where it was queued: the position an event is placed at is read off
// the child's own agent, which is a passive state machine only the child's
// goroutine may touch, and every caller of this is that goroutine. What goes
// in is the source and nothing else — the message is the parent's or the
// person's own words, and the record holds no content.
func (c *child) drainSteering() []string {
	c.mu.Lock()
	queued := c.steering
	c.steering = nil
	msgs := make([]string, 0, len(queued))
	for _, q := range queued {
		msgs = append(msgs, q.text)
		c.transcript = append(c.transcript, TranscriptEntry{Kind: EntryUser, Text: q.text})
	}
	sig := c.rec.Signal
	c.mu.Unlock()
	if sig != nil {
		for _, q := range queued {
			sig(c.pos(), observe.SignalSteer, string(q.from))
		}
	}
	return msgs
}

// beginTurn arms a fresh interrupt channel for the next h.Run.
func (c *child) beginTurn() {
	c.mu.Lock()
	c.intCh = make(chan struct{})
	c.intClosed = false
	c.turns++
	// A turn is steered about the instruction it was given. The next one has
	// a new instruction — a person's redirection through the lane, usually
	// the answer to the very count this carries — and starting it on the last
	// one's tally would report a child as ignoring a steer it has just been
	// taken off.
	c.steers = 0
	c.mu.Unlock()
}

// pos is where the child is now: the turn it is on and the tool round within
// it, read off the same counters its status is.
//
// Only the goroutine running the child may call it. The round comes off the
// agent, which is a passive state machine that goroutine drives and no lock
// guards; anything raised from elsewhere — a reading that lands on its own
// goroutine — states the round it is about and takes signalAt instead.
func (c *child) pos() observe.Pos {
	c.mu.Lock()
	turn, a := int64(c.turns), c.agent
	c.mu.Unlock()
	return observe.Pos{Turn: turn, Round: int64(a.Rounds())}
}

// endRound is the round an attempt's closing event is filed at: the live one
// where there is an agent to ask for it, and zero for an attempt cancelled
// in the queue, which never had one.
func (c *child) endRound() int {
	c.mu.Lock()
	a := c.agent
	c.mu.Unlock()
	if a == nil {
		return 0
	}
	return a.Rounds()
}

// end is what this attempt's row is closed with: how it stopped, the last
// reading of its work, every steer it was given and which attempt it was.
//
// The steers are the attempt's total and not the live count its lane shows.
// A lane answers "is this child ignoring its reader right now", and goes back
// to zero at every turn for it; the record answers "did steering this child
// help", and a count that forgot the first two turns' steers would say no
// child is ever steered more than once.
func (c *child) end() observe.ChildEnd {
	c.mu.Lock()
	defer c.mu.Unlock()
	return observe.ChildEnd{
		Reason: c.endReason, Verdict: c.verdictCode,
		Steers: c.steersAll, Attempt: c.attempt,
	}
}

// signalAt records one signal about a round the caller names, for the events
// that do not come from the child's own goroutine. The turn and the recorder
// are taken under the lock, and the agent is never touched: a caller that
// asked for the live round would be reading a counter another goroutine is
// advancing, and a retried child has its recorder replaced under that same
// lock while an earlier attempt's reading is still out.
func (c *child) signalAt(round int, code, reason string) {
	c.mu.Lock()
	turn, sig := int64(c.turns), c.rec.Signal
	c.mu.Unlock()
	if sig != nil {
		sig(observe.Pos{Turn: turn, Round: int64(round)}, code, reason)
	}
}

// interruptTurn closes the current turn's interrupt channel (idempotent),
// unblocking any approval wait.
func (c *child) interruptTurn() {
	c.mu.Lock()
	if c.intCh != nil && !c.intClosed {
		close(c.intCh)
		c.intClosed = true
	}
	c.mu.Unlock()
}

// stop cancels the current attempt. A retry replaces cancel, so it is read
// under the lock rather than off the struct.
//
// The runner is interrupted as well as the context, because a child waiting
// out a provider holds no stream for a cancelled context to abort: cancelling
// alone leaves a killed child sitting in its backoff — worktree and all —
// until a countdown it no longer has any reason to finish runs out.
func (c *child) stop() {
	c.mu.Lock()
	cancel, h := c.cancel, c.headless
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if h != nil {
		h.Interrupt()
	}
}

// parentUntracked is what the session says it has created and git does not
// know about, asked fresh for each worktree. A supervisor that was never told
// carries nothing, which is what every writer did before it could start from
// the parent's tree at all.
func (s *Supervisor) parentUntracked() []string {
	if s.opts.Untracked == nil {
		return nil
	}
	return s.opts.Untracked()
}

// workspace is the attempt's isolated worktree and its parent repo top, read
// under the lock for the same reason.
func (c *child) workspace() (worktree, repoTop string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.worktree, c.repoTop
}

// interruptCh is the current turn's interrupt channel (nil before any turn).
func (c *child) interruptCh() <-chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.intCh
}

// addUsage accumulates provider-reported usage and reports whether the
// child's token budget is now exceeded.
//
// The budget is measured against fresh tokens — the prompt less what the
// provider served from its cache, plus the completion — because a cached
// prefix costs a fraction of the input rate and is nothing at all the child
// has newly taken in. PromptTokens includes the cached part by contract
// (provider.Usage), and a dialect that reports no cache at all reports zero,
// which leaves the two figures equal and the budget where it was.
func (c *child) addUsage(u *provider.Usage) (over bool) {
	if u == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tokensIn += int64(u.PromptTokens)
	c.tokensOut += int64(u.CompletionTokens)
	c.fresh += int64(max(u.PromptTokens-u.CachedTokens, 0)) + int64(u.CompletionTokens)
	if c.maxTokens > 0 && c.fresh > c.maxTokens {
		c.budgetHit = true
		return true
	}
	return false
}

// Supervisor owns a session's children: spawning, bounded concurrency,
// approval routing, cancellation, and worktree cleanup.
type Supervisor struct {
	opts   Options
	ctx    context.Context
	cancel context.CancelFunc
	events chan Event
	sem    chan struct{}

	mu       sync.Mutex
	children []*child
	byName   map[string]*child
	counters map[Role]int
	// parentMode is the ceiling children are clamped to; parentGrants are the
	// parent's session grants ([a] on a prompt, /mode allow), which children
	// inherit for the same reason they inherit the mode.
	parentMode   agent.Mode
	parentGrants agent.Grants
	// appliedFiles records which agent's patch last landed each file, so a
	// later patch touching the same file is flagged before it is applied.
	appliedFiles map[string]string
	// batch numbers the parent tool rounds that spawned children.
	// The supervisor does not know where a round begins — the parent front-end
	// does, and says so with BeginBatch — so children spawned by a host that
	// never opens one all share batch zero.
	batch int
	// held is the hold as the children see it: nil when nothing is held, and
	// an open channel otherwise, which Release closes. It is replaced rather
	// than reopened because a closed channel cannot be un-closed and a hold
	// has to be able to come back — every hold after the first would
	// otherwise let the fan-out straight through.
	held chan struct{}

	wg        sync.WaitGroup
	closeOnce sync.Once
}

// New builds a Supervisor. The parent-mode ceiling starts at manual (the
// safest) until SetParentMode reports the session's real mode.
func New(ctx context.Context, opts Options) *Supervisor {
	if opts.MaxConcurrent <= 0 {
		opts.MaxConcurrent = DefaultMaxConcurrent
	}
	if opts.Profiles == nil {
		opts.Profiles = BuiltinProfiles()
	}
	sctx, cancel := context.WithCancel(ctx)
	return &Supervisor{
		opts:         opts,
		ctx:          sctx,
		cancel:       cancel,
		events:       make(chan Event, 64),
		sem:          make(chan struct{}, opts.MaxConcurrent),
		byName:       map[string]*child{},
		counters:     map[Role]int{},
		parentMode:   agent.ModeManual,
		appliedFiles: map[string]string{},
	}
}

// Events is the supervisor's notification stream for the parent front-end.
func (s *Supervisor) Events() <-chan Event { return s.events }

// AddProfile makes a role spawnable from now on: a profile drafted in the
// session joins the ones it opened with, replacing one of the same name.
// The spawn tool's definition is the caller's to refresh; the supervisor
// only decides what a spawn may name.
func (s *Supervisor) AddProfile(p Profile) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := make(Profiles, len(s.opts.Profiles)+1)
	for k, v := range s.opts.Profiles {
		next[k] = v
	}
	next[p.Name] = p
	s.opts.Profiles = next
}

// Profiles is the set of roles a spawn may name now.
func (s *Supervisor) Profiles() Profiles {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.opts.Profiles
}

// BeginBatch opens a new spawn batch and returns its number. The parent
// front-end calls it once per tool round, so the children one round spawns
// share a batch and can be rendered as one fan-out block rather than
// as rows interleaved with everything else the round did.
func (s *Supervisor) BeginBatch() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.batch++
	return s.batch
}

// Batch is the batch spawns are currently joining.
func (s *Supervisor) Batch() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.batch
}

// BatchSize counts the children of one batch, whatever state they are in: a
// fan-out is two or more children spawned in one round, and it stays a
// fan-out after they finish.
func (s *Supervisor) BatchSize(batch int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, c := range s.children {
		if c.batch == batch {
			n++
		}
	}
	return n
}

// SetParentMode records the parent session's permission mode; children are
// clamped to it at every decision, so a child can never be more permissive
// than its parent.
func (s *Supervisor) SetParentMode(m agent.Mode) {
	s.mu.Lock()
	s.parentMode = m
	s.mu.Unlock()
}

// SetParentGrants records the parent's session grants ([a] on a confirm
// prompt, /mode allow): what the user waved through for the session is waved
// through for children too, so one grant is not re-asked once per agent. The
// scoped grants travel with the blanket ones — a child editing under a
// directory the parent granted is doing the thing that was granted.
func (s *Supervisor) SetParentGrants(g agent.Grants) {
	s.mu.Lock()
	s.parentGrants = g
	s.mu.Unlock()
}

// childPolicy assembles the approval policy one child decides with: its
// clamped mode, the parent's session grants and command allowlist, and the
// read-only inspection settings.
func (s *Supervisor) childPolicy(c *child) agent.ModePolicy {
	s.mu.Lock()
	g := s.parentGrants
	s.mu.Unlock()
	allowlist := s.opts.CommandAllowlist
	if len(g.Commands) > 0 {
		allowlist = append(append([]string(nil), allowlist...), g.Commands...)
	}
	// The hosts a child may reach are the parent's, and only the parent's:
	// they arrive here and there is no path back, so a child cannot widen
	// the set for itself or for anyone else.
	// See docs/capabilities/approvals-and-safety.md#a-host-is-granted-once.
	hosts := s.opts.AllowHosts
	if len(g.Hosts) > 0 {
		hosts = append(append([]string(nil), hosts...), g.Hosts...)
	}
	return agent.ModePolicy{
		Mode:             s.childMode(c),
		AllowEdits:       g.AllEdits,
		AllowCommands:    g.AllCommands,
		EditDirs:         g.EditDirs,
		CommandAllowlist: allowlist,
		CommandDenylist:  s.opts.CommandDenylist,
		AllowHosts:       hosts,
		DenyHosts:        s.opts.DenyHosts,
		ReadOnlyExtra:    s.opts.ReadOnlyExtra,
		ReadOnlyDisabled: s.opts.ReadOnlyDisabled,
	}
}

func (s *Supervisor) childMode(c *child) agent.Mode {
	s.mu.Lock()
	ceiling := s.parentMode
	s.mu.Unlock()
	c.mu.Lock()
	mode := c.mode
	c.mu.Unlock()
	return agent.ClampMode(mode, ceiling)
}

// ParentMode is the current mode ceiling children are clamped to.
func (s *Supervisor) ParentMode() agent.Mode {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.parentMode
}

// Snapshot returns every child's live status in spawn order.
func (s *Supervisor) Snapshot() []Status {
	s.mu.Lock()
	kids := make([]*child, len(s.children))
	copy(kids, s.children)
	s.mu.Unlock()
	out := make([]Status, len(kids))
	for i, c := range kids {
		out[i] = c.status()
	}
	return out
}

// ActiveCounts reports how many children are still working and how many of
// those are blocked on the user, for the status bar badge.
func (s *Supervisor) ActiveCounts() (active, blocked int) {
	for _, st := range s.Snapshot() {
		switch st.State {
		case StateQueued, StateRunning, StateIdle:
			active++
		case StateBlocked:
			active++
			blocked++
		}
	}
	return active, blocked
}

// lookup resolves a child by name.
func (s *Supervisor) lookup(name string) (*child, error) {
	s.mu.Lock()
	c, ok := s.byName[name]
	s.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("no agent named %q", name)
	}
	return c, nil
}

// Get returns one child's live status.
func (s *Supervisor) Get(name string) (Status, bool) {
	c, err := s.lookup(name)
	if err != nil {
		return Status{}, false
	}
	return c.status(), true
}

// Parent returns the name of the agent that spawned name ("" means the
// orchestrator), for breadcrumbs and esc-pops.
func (s *Supervisor) Parent(name string) (string, bool) {
	c, err := s.lookup(name)
	if err != nil {
		return "", false
	}
	return c.parent, true
}

// Transcript snapshots a child's live transcript for rendering.
func (s *Supervisor) Transcript(name string) []TranscriptEntry {
	c, err := s.lookup(name)
	if err != nil {
		return nil
	}
	c.mu.Lock()
	out := make([]TranscriptEntry, len(c.transcript))
	copy(out, c.transcript)
	c.mu.Unlock()
	return out
}

// StreamingText is the child's in-flight assistant text, for live rendering.
func (s *Supervisor) StreamingText(name string) string {
	c, err := s.lookup(name)
	if err != nil {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.streaming
}

// Note appends a front-end entry (scoped command output, mode changes) to a
// child's transcript so it survives attach/detach.
func (s *Supervisor) Note(name string, e TranscriptEntry) error {
	c, err := s.lookup(name)
	if err != nil {
		return err
	}
	c.appendEntry(e)
	s.emitUpdate(c)
	return nil
}

// Steer queues a message for a child (steering semantics): injected
// before its next stream request when running, or starting a fresh turn when
// the child is idle after a cancelled turn. Finished children cannot be
// steered.
//
// from is who is speaking, and every route in goes through here: the person
// typing at the child's lane and the orchestrator calling the steer tool are
// one mechanism, so a redirect from either has the same consequences for the
// turn it lands in — the target extended, the reading in flight retired, the
// reckoning started again — and the surfaces need only one word to tell them
// apart. The source is carried with the message and recorded where the child
// takes it (drainSteering); the message itself is content and never reaches
// the record.
// See docs/capabilities/subagents.md#three-can-steer-a-child-and-none-of-them-can-end-it.
func (s *Supervisor) Steer(name, text string, from SteerSource) error {
	c, err := s.lookup(name)
	if err != nil {
		return err
	}
	c.mu.Lock()
	switch c.state {
	case StateDone, StateFailed:
		state := c.state
		c.mu.Unlock()
		return fmt.Errorf("agent %s has finished (%s); nothing to steer", name, state)
	}
	c.steering = append(c.steering, queuedSteer{text: text, from: from})
	// The source is on the status the moment the message is queued, not when
	// the child takes it: the orchestrator reads the roster in the round
	// after it steered, and a redirect still waiting at a round boundary is
	// exactly what it needs to see there.
	c.steerFrom = from
	c.mu.Unlock()
	select {
	case c.steerWake <- struct{}{}:
	default:
	}
	s.emitUpdate(c)
	return nil
}

// QueuedSteering is how many steering messages wait to join the child's
// conversation, for the attached status bar.
func (s *Supervisor) QueuedSteering(name string) int {
	c, err := s.lookup(name)
	if err != nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.steering)
}

// Hold parks every child at its own round boundary. Nothing stops where it
// is: a child in the middle of a round finishes it, and only then waits — an
// open provider stream cannot be paused, and a reader that stops reading
// backs the socket up until the provider gives up on the request. So a hold
// asked of a fan-out of four arrives four times, once per child, at four
// different moments. Idempotent: asking twice is one hold.
// See docs/capabilities/subagents.md#a-hold-reaches-the-whole-fan-out.
func (s *Supervisor) Hold() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.held == nil {
		s.held = make(chan struct{})
	}
}

// Release lets every held child go on, in one act. The hold was asked of the
// session rather than of a child, and letting them out one at a time would be
// a list nobody could be expected to keep. Releasing an unheld supervisor
// does nothing.
func (s *Supervisor) Release() {
	s.mu.Lock()
	ch, kids := s.held, append([]*child(nil), s.children...)
	s.held = nil
	s.mu.Unlock()
	if ch == nil {
		return
	}
	close(ch)
	for _, c := range kids {
		if c.unpark(ch) {
			s.emitUpdate(c)
		}
	}
}

// Holding reports whether a hold stands.
func (s *Supervisor) Holding() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.held != nil
}

// holdFor is what a child's round tail asks: nil to run on, or the channel to
// wait on, with the child marked parked as it arrives. The read and the mark
// are one locked step so a release that lands between them cannot leave a
// child marked held with nothing left to un-mark it — the release either sees
// the mark and clears it, or has already emptied the hold and hands back nil.
func (s *Supervisor) holdFor(c *child) <-chan struct{} {
	s.mu.Lock()
	ch := s.held
	if ch != nil {
		c.park(ch)
	}
	s.mu.Unlock()
	if ch != nil {
		s.emitUpdate(c)
	}
	return ch
}

// CancelTurn interrupts a child's current turn: the in-flight stream
// aborts, outstanding calls get synthetic results, and the child parks idle
// awaiting steering — Ctrl+C semantics without killing the agent.
func (s *Supervisor) CancelTurn(name string) error {
	c, err := s.lookup(name)
	if err != nil {
		return err
	}
	c.mu.Lock()
	state := c.state
	h := c.headless
	c.mu.Unlock()
	switch state {
	case StateRunning, StateBlocked:
	default:
		return fmt.Errorf("agent %s has no turn in progress (%s)", name, state)
	}
	c.interruptTurn()
	if h != nil {
		h.Interrupt()
	}
	return nil
}

// Kill cancels a child outright: its context is cancelled, its run finishes
// as failed/cancelled with a well-formed conversation, and (for writers) its
// worktree is removed. The transcript stays inspectable.
func (s *Supervisor) Kill(name string) error {
	c, err := s.lookup(name)
	if err != nil {
		return err
	}
	c.mu.Lock()
	state := c.state
	c.mu.Unlock()
	switch state {
	case StateDone, StateFailed:
		return fmt.Errorf("agent %s has already finished (%s)", name, state)
	}
	c.appendEntry(TranscriptEntry{Kind: EntrySystem, Text: "Killed by the user."})
	// Marked before the cancel rather than read off it afterwards: the child
	// comes out of its wait on a cancelled context, which is also what a
	// session shutting down hands it, and the mark is the only thing that
	// tells the record which of the two ended this attempt.
	c.mu.Lock()
	c.killed = true
	c.mu.Unlock()
	c.stop()
	return nil
}

// Retry runs a failed child again on its original task. Only a
// failed child can be retried: a finished one has nothing to redo, and a live
// one is already doing it.
//
// The attempt is what restarts, not the agent. The child keeps its name, its
// place in the batch and its transcript — the failed attempt stays there,
// with the reason it failed and the retry appended after it, so the list and
// the attached view both keep their history. Everything the attempt owns is
// new: a fresh conversation, a fresh workspace for a writer, and a fresh
// token budget, because an attempt that inherits the spend that killed it
// fails again before it has done anything. New is not blind, though: the
// conversation opens with how the attempt it replaces ended and whatever
// handoff that one wrote (restart), which is the difference between a second
// attempt and the same attempt run twice.
//
// It returns when the child is claimed rather than when the new attempt
// starts. A child whose previous attempt has not finished stopping waits for
// it — the lane says what it is waiting for — because the alternative is
// asking somebody to press the key again for a window they cannot see.
// See docs/capabilities/subagents.md#a-failed-child-can-be-run-again.
func (s *Supervisor) Retry(name string) error {
	c, err := s.lookup(name)
	if err != nil {
		return err
	}
	if s.ctx.Err() != nil {
		return errors.New("the agent supervisor is shut down")
	}
	var wctx context.Context
	var wcancel context.CancelFunc
	var done chan struct{}
	// Reading the state and claiming the child are one step. Two presses that
	// both read "failed" would each start an attempt on the same child, and
	// the one that lost would leave a worktree and a goroutine behind with
	// nothing pointing at them. The claim is the queued state itself, so the
	// second press meets the refusal every other live state meets.
	c.mu.Lock()
	state, detail := c.state, c.detail
	if state == StateFailed {
		c.state, c.detail = StateQueued, retryWaitDetail
		// The channel is taken here and not read at the select below: what
		// has to stop is the attempt being replaced, and a restart gives the
		// child a new one.
		done = c.done
		// A child queued behind its own teardown is not finished, so it is
		// still offered a kill — and that kill has to reach the retry, since
		// the attempt it would otherwise cancel has already stopped. The wait
		// gets a context for exactly that: stop() cancels whatever is
		// current, and until the new attempt has one of its own, this is it.
		wctx, wcancel = context.WithCancel(s.ctx)
		c.ctx, c.cancel = wctx, wcancel
	}
	c.mu.Unlock()
	if state != StateFailed {
		return fmt.Errorf("agent %s is %s; only a failed agent can be retried", name, state)
	}

	// The previous attempt's goroutine owns the worktree cleanup and closes
	// the done channel last of all, so a retry joins it rather than racing
	// it. A parent acting on the event that says the child failed finds
	// nothing to wait for, because that event goes out after the channel
	// closes; a surface that draws its offer from the child's state sees it
	// fail before any of the teardown has run, and that is the press that
	// waits — what it is waiting on is a git process, which takes exactly as
	// long as the machine is busy.
	select {
	case <-done:
		err := s.restart(c, detail)
		// The new attempt brought its own context; this one has nothing left
		// to govern either way.
		wcancel()
		if err != nil {
			// The failure goes back as an error rather than onto the
			// transcript: this caller has it in hand, and the one path where
			// nobody does is the wait below, which writes it there itself.
			c.set(StateFailed, detail)
			s.emitUpdate(c)
			return err
		}
		return nil
	default:
	}
	// Waiting is the child's business rather than the caller's: the press
	// comes from a keystroke, and a surface that blocked on a teardown would
	// stop redrawing everything else for as long as it took. The lane says
	// what it is waiting for instead.
	s.emitUpdate(c)
	// Safe against a Close racing this: the attempt this waits on has not
	// closed its done channel, so it still holds a count of its own.
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer wcancel()
		timer := time.NewTimer(retryTeardownWait)
		defer timer.Stop()
		select {
		case <-done:
			if err := s.restart(c, detail); err != nil {
				s.abandonRetry(c, detail, err.Error())
			}
		case <-timer.C:
			s.abandonRetry(c, detail, "the previous attempt has not stopped")
		case <-wctx.Done():
			reason := "the agent was killed"
			if s.ctx.Err() != nil {
				reason = "the session is shutting down"
			}
			s.abandonRetry(c, detail, reason)
		}
	}()
	return nil
}

// retryWaitDetail is the lane while a retry waits out the attempt it
// replaces. A retry that has to wait is the ordinary queue with one more
// thing in front of it, so it is the queued state and names what it is
// queued behind — every surface that draws a child reads this detail, and a
// lane that said only "queued" would be a retry that looked like it had
// started.
const retryWaitDetail = "queued · waiting for the last attempt to stop"

// retryTeardownWait bounds that wait at the bound a stopping child already
// has: the handoff a child stopped by its budget is given is the longest
// step in any teardown, so a teardown that outlasts it is stuck rather than
// slow. Waiting on a stuck one forever would leave the lane queued behind
// something that is never coming back, with no way to ask again.
const retryTeardownWait = finalCheckInTimeout

// abandonRetry puts the child back where the retry found it and says on its
// transcript why nothing happened. The failure and its offer both stand:
// what could not be started is this attempt, so pressing again is still the
// right thing to do.
func (s *Supervisor) abandonRetry(c *child, detail, reason string) {
	c.appendEntry(TranscriptEntry{Kind: EntrySystem, Text: "The retry did not start — " + reason + "."})
	c.set(StateFailed, detail)
	s.emitUpdate(c)
}

// restart gives the child a new attempt on the same task: a fresh
// conversation, a fresh workspace for a writer and a fresh budget. detail is
// what the attempt it replaces ended saying, which the transcript note
// repeats. The caller owns the child's state, so a setup that fails here
// leaves the child claimed and hands back the error for the caller to put
// it right with.
func (s *Supervisor) restart(c *child, detail string) error {
	if s.ctx.Err() != nil {
		return errors.New("the agent supervisor is shut down")
	}
	// Read before anything is replaced: the handoff a child stopped by its
	// budget was asked for is sitting in the report field this attempt is
	// about to clear, and every retry that did not carry it threw away the
	// one thing the failed attempt produced.
	c.mu.Lock()
	handoff := c.report
	had := c.maxTokens
	budget, grew := retryBudget(c.maxTokens, c.budgetHit)
	// The cap the attempt being replaced grew to, so a retry does not start
	// back at a ceiling its predecessor had already talked its way past.
	maxRounds := c.maxRounds
	// And the number this attempt is. It is claimed here rather than with
	// the counter resets below because a reader's record is opened before
	// them, and a row stamped with the number of the attempt it replaces
	// would join a retry to itself.
	c.attempt++
	attempt := c.attempt
	c.mu.Unlock()

	cctx, cancel := context.WithCancel(s.ctx)
	// A reader's workspace is opened here, where a failure is still this
	// caller's to report. A writer's waits for the slot the new attempt has
	// to take anyway — and waits for a second reason of its own: the parent's
	// tree is read again rather than reused, because a retry happens minutes
	// after the first attempt and the session has usually gone on working in
	// between, so the later the copy is taken the truer its base.
	w := workspace{root: s.opts.Root}
	if !c.profile.Writes {
		var wErr error
		if w, wErr = s.openWorkspace(c, cctx, maxRounds, attempt); wErr != nil {
			cancel()
			// The number goes back with it: nothing ran under it, and a
			// second press must not leave a gap in the child's attempts.
			c.mu.Lock()
			c.attempt--
			c.mu.Unlock()
			return fmt.Errorf("cannot set up the retry: %w", wErr)
		}
	}

	retryNote := "Retrying — the previous attempt " + detail + "."
	if grew {
		retryNote += fmt.Sprintf(" This attempt is given ~%s new tokens, up from ~%s.", formatTokens(budget), formatTokens(had))
	}
	c.appendEntry(TranscriptEntry{Kind: EntrySystem, Text: retryNote})

	// The workspace is installed under the lock — a retried child is one the
	// parent can steer while this runs, and that is the one read of these
	// fields from another goroutine. A writer's is empty but for the
	// parent's root: it has no copy until run opens one, and everything the
	// attempt it replaces left in these fields describes a worktree that has
	// already been torn down.
	c.mu.Lock()
	c.ctx, c.cancel = cctx, cancel
	c.install(w)
	c.maxRounds = maxRounds
	c.done = make(chan struct{})
	c.state, c.detail = StateQueued, "queued · retry"
	c.started, c.ended = time.Now(), time.Time{}
	c.maxTokens = budget
	// The attempt is told what the one before it hit and handed over. A
	// retry on the identical prompt is an attempt with no reason to come out
	// differently: the child that died re-reading a large file dies
	// re-reading it, and the budget the session spent on the second attempt
	// bought the first one over again.
	c.prologue = retryPrologue(detail, handoff)
	// Each attempt is measured against the budget it was spawned with; what
	// the earlier attempts spent is carried, not forgotten.
	c.priorIn, c.priorOut = c.priorIn+c.tokensIn, c.priorOut+c.tokensOut
	c.tokensIn, c.tokensOut, c.fresh, c.budgetHit = 0, 0, 0, false
	c.checkIns = 0
	// A retry is a fresh conversation on the same task: no steer has reached
	// this attempt, whatever the last one was told.
	c.steers, c.steersAll, c.verdict, c.verdictCode, c.steerFrom = 0, 0, "", "", ""
	// And the attempt it replaces ended for a reason that is that attempt's,
	// already on that attempt's own row. A retry that inherited it would
	// report the child as having ended twice the same way — and a child
	// killed once would report every attempt after it as killed too.
	c.endReason, c.killed = "", false
	c.turns = 0
	c.toolCalls, c.step = 0, 0
	// A retry starts from a worktree of its own, so what the attempt it
	// replaces wrote is not in the tree this one is reading.
	c.wrote = nil
	c.report, c.patchNote, c.streaming = "", "", ""
	c.mu.Unlock()

	s.wg.Add(1)
	go s.run(c)
	s.emitUpdate(c)
	return nil
}

// retryBudget is what a second attempt is given, and whether that is more
// than the first had.
//
// A child stopped by its budget and restarted on the same one spends it the
// same way and stops at the same place, which makes the retry a full budget
// spent to learn nothing. So the budget grows by the step the round cap
// already grows by — the escalation behind a check-in, applied by a child
// with nobody to ask — and is clamped to the ceiling a spawn is clamped to,
// since a retry must not be the way past a bound the model cannot otherwise
// cross. A child that failed for any other reason was not short of
// attention and gets what it had.
func retryBudget(maxTokens int64, budgetHit bool) (budget int64, grew bool) {
	if !budgetHit {
		return maxTokens, false
	}
	grown := min(maxTokens*checkInGrowth, int64(MaxTokensCeiling))
	return grown, grown > maxTokens
}

// retryPrologue is what a second attempt is told about the first, ahead of
// the task it is being given again.
//
// The failed attempt's conversation is gone by design — an attempt that
// inherited it would inherit the context that killed it — but "gone" was
// being read as "never happened": the retry opened on the identical prompt,
// took the identical first steps, and on a budget failure spent the identical
// budget the identical way. What is carried instead is the two facts that
// cost nothing to carry: how it ended, and the handoff it wrote on its way
// out. The task itself follows verbatim, and is named as such, so a child
// cannot read the prologue as an amendment to what it was asked for.
func retryPrologue(detail, handoff string) string {
	var sb strings.Builder
	sb.WriteString("A previous attempt at this task ended: " + detail + ".\n\n")
	if handoff = strings.TrimSpace(handoff); handoff != "" {
		sb.WriteString("What it handed over before it stopped:\n\n" + handoff + "\n\n")
	}
	sb.WriteString("That attempt's conversation is gone, and so is any file it changed whose patch was not approved: what you have of it is what is written above. Do not spend this attempt establishing again what it already established — carry on from it, and where it ran out or got stuck, take a different route.\n\nThe task, unchanged:\n\n")
	return sb.String()
}

// AgentMode is the child's effective (ceiling-clamped) permission mode.
func (s *Supervisor) AgentMode(name string) (agent.Mode, bool) {
	c, err := s.lookup(name)
	if err != nil {
		return agent.ModeManual, false
	}
	return s.childMode(c), true
}

// SetAgentMode sets a child's permission mode, clamped to the parent
// ceiling; the effective mode is returned.
func (s *Supervisor) SetAgentMode(name string, mode agent.Mode) (agent.Mode, error) {
	c, err := s.lookup(name)
	if err != nil {
		return agent.ModeManual, err
	}
	s.mu.Lock()
	ceiling := s.parentMode
	s.mu.Unlock()
	eff := agent.ClampMode(mode, ceiling)
	c.mu.Lock()
	c.mode = eff
	c.mu.Unlock()
	s.emitUpdate(c)
	return eff, nil
}

// WorktreeDiff returns a writer child's cumulative patch against its
// worktree base, for the attached /diff command. Researchers share the real
// workspace and have nothing scoped to diff.
func (s *Supervisor) WorktreeDiff(name string) (string, error) {
	c, err := s.lookup(name)
	if err != nil {
		return "", err
	}
	worktree, _ := c.workspace()
	if worktree == "" {
		if c.profile.Writes {
			// A writer's copy is made when it starts and torn down when it
			// stops, so there is one to diff only while it runs.
			return "", fmt.Errorf("agent %s is %s and has no copy of the workspace to diff", name, c.status().State)
		}
		return "", fmt.Errorf("agent %s has no isolated workspace (%s role) — nothing to diff", name, c.role)
	}
	return worktreePatch(worktree)
}

// CancelAll cancels every child; blocked approval waits unblock and each
// child finishes as failed/cancelled with a well-formed conversation.
func (s *Supervisor) CancelAll() {
	s.mu.Lock()
	kids := make([]*child, len(s.children))
	copy(kids, s.children)
	s.mu.Unlock()
	for _, c := range kids {
		c.stop()
	}
}

// Close cancels everything, waits for children to finish, removes leftover
// worktrees, and closes the event stream. Idempotent.
func (s *Supervisor) Close() {
	s.closeOnce.Do(func() {
		s.cancel()
		s.CancelAll()
		s.wg.Wait()
		s.mu.Lock()
		kids := make([]*child, len(s.children))
		copy(kids, s.children)
		s.mu.Unlock()
		for _, c := range kids {
			if worktree, repoTop := c.workspace(); worktree != "" {
				removeWorktree(repoTop, worktree)
			}
		}
		close(s.events)
	})
}

// WrapExecutor intercepts the orchestration tools on the parent session's
// executor chain; every other call passes through.
func (s *Supervisor) WrapExecutor(next agent.ToolExecutor) agent.ToolExecutor {
	return func(name string, args json.RawMessage) (string, error) {
		switch name {
		case SpawnToolName:
			return s.spawn(args)
		case ReportToolName:
			return s.report(args)
		case SteerToolName:
			return s.steer(args)
		case RetryToolName:
			return s.retry(args)
		}
		return next(name, args)
	}
}

// Spawn starts a child from the spawn tool's own arguments, for a caller
// that is not the model — the backlog runner's review stage. It is the
// same path the tool takes, limits and all; nothing about being called
// from code exempts a child from the attention budget.
func (s *Supervisor) Spawn(raw json.RawMessage) (string, error) { return s.spawn(raw) }

// FinalReport is a child's own final message as it wrote it, with the
// state it ended in — for a caller that grades the report rather than
// shows it, and must not mistake the parent-facing placeholder a failed
// child gets for something the child said.
func (s *Supervisor) FinalReport(name string) (report string, state State, ok bool) {
	s.mu.Lock()
	c, found := s.byName[name]
	s.mu.Unlock()
	if !found {
		return "", 0, false
	}
	st := c.status()
	c.mu.Lock()
	report = c.report
	c.mu.Unlock()
	return report, st.State, true
}

// Report is a child's report text now, without waiting for it to finish.
func (s *Supervisor) Report(name string) (string, error) {
	s.mu.Lock()
	c, ok := s.byName[name]
	s.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("no agent named %q", name)
	}
	return c.reportText(), nil
}

// workspace is one attempt's place to work and everything built against it:
// the isolated checkout a writer gets, the working root inside it, the
// environment rooted there, the agent driving it and the attempt's record.
// The five travel together because all five are decided by a directory that,
// for a writer, does not exist until the child starts.
type workspace struct {
	root  string
	wt    worktreeHandle
	env   Env
	agent *agent.Agent
	rec   Recorder
}

// openWorkspace opens one: for a writer, a detached checkout seeded from the
// parent's tree; for a reader, the parent's own root, which needs nothing
// taken and nothing torn down.
//
// A reader's is opened at spawn, where a failure can still be handed back as
// the tool's answer. A writer's is opened in run, once the child holds a
// slot. Four lanes over three slots is one lane waiting, and a waiting lane
// that already had its copy would hold a whole checkout on disk for as long
// as it queued — sixteen spawned writers is sixteen copies of the repository
// with three of them running — seeded from a tree the session has since
// moved on from. What a lane starts from should be the parent's work as it
// stands when the lane starts, not as it stood when the fan-out was planned.
// See docs/capabilities/subagents.md#a-writer-starts-from-your-tree.
//
// ctx, maxRounds and attempt are passed rather than read off the child
// because the attempt they belong to is the caller's: a retry has installed
// none of the three by the time it opens the workspace its new attempt will
// run in.
func (s *Supervisor) openWorkspace(c *child, ctx context.Context, maxRounds, attempt int) (workspace, error) {
	w := workspace{root: s.opts.Root}
	var err error
	if c.profile.Writes {
		if w.wt, err = addWorktree(s.opts.Root, s.parentUntracked()); err != nil {
			return workspace{}, fmt.Errorf("cannot create an isolated worktree for a writer agent: %w", err)
		}
		w.root = w.wt.root
	}
	w.env, err = s.opts.NewEnv(ctx, Spec{Name: c.name, Role: c.role, Root: w.root, Model: c.model, Paths: c.paths,
		Worktree: w.wt.dir != ""})
	if err != nil {
		removeWorktree(w.wt.repoTop, w.wt.dir)
		return workspace{}, fmt.Errorf("the agent's environment could not be built: %w", err)
	}
	w.agent = newChildAgent(w.env, maxRounds)
	// The auto-run executor is the env's rooted, reduced chain, inside
	// whatever the surface puts on its own dispatchers.
	w.agent.SetExecutor(w.env.autoExecutor(c.seam()))
	// And the reading that tells the child its workspace moved under it,
	// baselined here so the first boundary compares against the tree the
	// child was started on.
	c.watchTree(w.agent, w.env)
	// And the child's second clock, here rather than in newChildAgent for the
	// reason the tree reading is: what a child has spent and what it has
	// written are the child's own record, and newChildAgent is handed an
	// environment. Every attempt comes through here, which is the property
	// newChildAgent exists for.
	w.agent.SetCheckInBudget(c.budget, c.written)
	// The mode recorded is the one in force — the profile's or the parent's
	// after the clamp — not the one asked for; c.mode alone is the request.
	if s.opts.Record != nil {
		w.rec = s.opts.Record(Spec{Name: c.name, Role: c.role, Root: w.root, Model: c.model, Paths: c.paths,
			Worktree: w.wt.dir != "", Mode: s.childMode(c), MaxRounds: roundCap(w.agent),
			Attempt: attempt}, w.env.SystemPrompt)
	}
	return w, nil
}

// install puts an opened workspace on the child, clearing the headless loop
// the workspace it replaces was driven by. The lock is the caller's, where
// there is anything to lock: a retry installs one in the middle of a page of
// counter resets and no reader should ever see half of that, while a spawn
// installs into a child no other goroutine can reach yet.
func (c *child) install(w workspace) {
	c.root, c.worktree, c.repoTop, c.seeded = w.root, w.wt.dir, w.wt.repoTop, w.wt.seeded
	c.agent, c.env, c.headless, c.rec = w.agent, w.env, nil, w.rec
}

// spawn validates the arguments, gives the child everything that does not
// depend on where it will work, and starts it in the background. A reader's
// workspace is opened here; a writer's is opened when its slot comes free
// (openWorkspace).
func (s *Supervisor) spawn(raw json.RawMessage) (string, error) {
	args, err := parseSpawnArgs(s.Profiles(), raw)
	if err != nil {
		return "", err
	}
	if s.ctx.Err() != nil {
		return "", errors.New("the agent supervisor is shut down")
	}

	s.mu.Lock()
	if len(s.children) >= MaxChildren {
		s.mu.Unlock()
		return "", fmt.Errorf("agent limit reached (%d per session)", MaxChildren)
	}
	name := args.Name
	if name == "" {
		s.counters[args.role]++
		name = fmt.Sprintf("%s-%d", args.role, s.counters[args.role])
	}
	if _, exists := s.byName[name]; exists {
		s.mu.Unlock()
		return "", fmt.Errorf("an agent named %q already exists", name)
	}
	mode := s.parentMode
	batch := s.batch
	s.mu.Unlock()
	// A profile may start its children stricter than the parent (a
	// reviewer in plan mode under an auto session); childMode clamps it to
	// the parent either way, so it can never start looser.
	if args.profile.HasMode {
		mode = args.profile.Mode
	}

	// Writers work in isolated worktrees, so they cannot overwrite each
	// other's files — but two patches over the same file conflict when they
	// land. A declared scope is refused up front rather than discovered at
	// apply time.
	if args.profile.Writes {
		if holder, claim, clash := s.claimConflict(args.paths); clash {
			return "", fmt.Errorf("%s already claims %s, which overlaps this agent's paths; wait for it with agent_report, or narrow the paths so the two do not share files", holder, claim)
		}
	}

	model := args.Model
	if s.opts.ModelFor != nil {
		model = s.opts.ModelFor(args.role, args.Model)
	}

	// The context is the child's from here, whether or not it has anywhere
	// to work yet: a writer queued behind a full set of slots is one a kill
	// has to reach, and the cancel is what reaches it.
	cctx, cancel := context.WithCancel(s.ctx)

	c := &child{
		name:      name,
		role:      args.role,
		profile:   args.profile,
		task:      args.Task,
		model:     model,
		paths:     args.paths,
		batch:     batch,
		steps:     args.steps,
		root:      s.opts.Root,
		mode:      mode,
		maxRounds: args.maxRounds,
		maxTokens: args.maxTokens,
		ctx:       cctx,
		cancel:    cancel,
		done:      make(chan struct{}),
		steerWake: make(chan struct{}, 1),
		state:     StateQueued,
		detail:    "queued",
		started:   time.Now(),
		attempt:   1,
	}
	// A reader's workspace is the parent's own root and costs nothing to
	// hold, so it is opened here where a failure is still this call's answer
	// rather than a child that appears and immediately fails. A writer's
	// waits for its slot (openWorkspace).
	if !args.profile.Writes {
		w, wErr := s.openWorkspace(c, cctx, args.maxRounds, c.attempt)
		if wErr != nil {
			cancel()
			return "", wErr
		}
		c.install(w)
	}

	s.mu.Lock()
	s.children = append(s.children, c)
	s.byName[name] = c
	s.mu.Unlock()

	s.wg.Add(1)
	go s.run(c)
	s.emitUpdate(c)

	note := ""
	if args.profile.Writes {
		note = " It edits an isolated copy of the workspace; its changes come back as a single patch the user reviews."
		if len(args.paths) > 0 {
			note += " It claims " + strings.Join(args.paths, ", ") + "; another writer cannot claim overlapping paths while it runs."
		} else {
			note += " It declared no paths, so nothing stops a second writer from touching the same files — pass paths when you fan out writers."
		}
	}
	modelNote := ""
	if model != "" {
		modelNote = ", " + model
	}
	return fmt.Sprintf("Spawned %s (%s%s, %s, ~%s token budget).%s It works in the background: call agent_report with name=%q in a later step to wait for and collect its final report, or agent_report with no arguments for a status overview.",
		name, args.role, modelNote, roundBudgetLabel(args.maxRounds), formatTokens(args.maxTokens), note, name), nil
}

// claimConflict reports whether a writer's declared paths overlap those of a
// live writer. Two claims that both declare paths and share any file are a
// conflict; an undeclared claim conflicts with nothing (it is flagged at
// patch time instead), so existing callers keep working.
func (s *Supervisor) claimConflict(paths []string) (holder, claim string, conflict bool) {
	if len(paths) == 0 {
		return "", "", false
	}
	s.mu.Lock()
	kids := make([]*child, len(s.children))
	copy(kids, s.children)
	s.mu.Unlock()
	for _, c := range kids {
		st := c.status()
		if !c.profile.Writes || len(st.Paths) == 0 {
			continue
		}
		// A killed child holds its claim until its goroutine notices the
		// cancel; a claim that is going is not one to wait for.
		if c.ctx.Err() != nil {
			continue
		}
		switch st.State {
		case StateDone, StateFailed:
			continue
		}
		for _, theirs := range st.Paths {
			for _, ours := range paths {
				if pathsOverlap(ours, theirs) {
					return st.Name, theirs, true
				}
			}
		}
	}
	return "", "", false
}

// pathsOverlap reports whether two path claims can name the same file. Each
// claim is reduced to the literal prefix before its first wildcard; claims
// overlap when either prefix contains the other, which is deliberately
// generous — a false conflict costs one sequenced agent, a missed one costs
// a mangled patch.
func pathsOverlap(a, b string) bool {
	pa, pb := literalPrefix(a), literalPrefix(b)
	return strings.HasPrefix(pa, pb) || strings.HasPrefix(pb, pa)
}

// literalPrefix trims a glob to the part before its first wildcard and
// normalizes it to a comparable form.
func literalPrefix(p string) string {
	p = strings.TrimPrefix(strings.TrimSpace(filepath.ToSlash(p)), "./")
	if i := strings.IndexAny(p, "*?["); i >= 0 {
		p = p[:i]
		// Back off to the last complete segment so "internal/u*" cannot
		// match "internal/ui" by accident.
		if j := strings.LastIndex(p, "/"); j >= 0 {
			p = p[:j+1]
		} else {
			p = ""
		}
	}
	return p
}

// run drives one child to completion on its own goroutine.
func (s *Supervisor) run(c *child) {
	defer s.wg.Done()
	// How the attempt ended, and the one event that says so. It is captured
	// where the state is set and sent on the way out, after the defers below
	// have released everything the attempt held — the slot, the worktree, and
	// last of all the done channel a retry joins. A parent that acts on this
	// event therefore cannot act while the attempt still owns its workspace;
	// one that watches the child's state instead sees it fail before any of
	// the teardown has run, which is why the retry waits rather than refusing.
	//
	// The status is taken at the transition rather than read here, because by
	// the time this runs a retry may already have started: the child would
	// then report itself queued in the event that says it finished.
	var ended Status
	finish := func(state State, reason, detail string) {
		c.mu.Lock()
		// A kill outranks whatever the run made of the cancellation it was
		// handed. Everything above this reads a cancelled context, and only
		// the child itself knows which of the two cancellations it was.
		if c.killed {
			reason = observe.ChildKilled
		}
		c.endReason = reason
		c.mu.Unlock()
		c.set(state, detail)
		ended = c.status()
		// The attempt says how it ended on its own record, once, here — the
		// one place every route out of the loop passes through. It goes to
		// the child's row and not the parent's because that is where the
		// attempt's model, its budget and what it spent already are, and a
		// budget is only answerable beside the spend it bounded
		// (docs/capabilities/sessions-and-memory.md#a-child-ends-for-a-reason).
		c.signalAt(c.endRound(), observe.SignalSubagent, reason)
	}
	defer func() { s.emit(Event{Kind: EventDone, Status: ended}) }()
	defer close(c.done)
	// The attempt's record and its workspace are captured rather than read
	// in the defers: a retry gives the child new ones, and this goroutine
	// closes and removes its own. Both are set again below, because a
	// writer's are not opened until it has a slot — a writer cancelled in
	// the queue never had either, and closes and removes nothing.
	c.mu.Lock()
	ctx, cancel, maxRounds, attempt, endRec := c.ctx, c.cancel, c.maxRounds, c.attempt, c.rec.End
	c.mu.Unlock()
	var worktree, repoTop string
	defer func() {
		if endRec != nil {
			endRec(c.end())
		}
		removeWorktree(repoTop, worktree)
	}()

	// Bounded concurrency: take a slot or notice cancellation while queued.
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	case <-ctx.Done():
		// Nothing of this attempt ever ran, so there is nothing for it to
		// have ended of: whatever cancelled a queued child cancelled it.
		finish(StateFailed, observe.ChildCancelled, "cancelled")
		return
	}

	// A writer's isolated checkout is taken here and not at spawn, so that a
	// lane waiting for a slot is a queued task rather than a copy of the
	// repository sitting on disk, and so that what it starts from is the
	// parent's tree as it stands now (openWorkspace). A seed that cannot be
	// carried therefore fails the child rather than the spawn: the fan-out
	// that asked for it has long since returned.
	if c.profile.Writes {
		// The lane says what the wait is now for. Copying and seeding a
		// large checkout is seconds of git, and a child that read "queued"
		// through all of it would look like one still waiting for a slot it
		// is in fact already holding. It is still the queued state because
		// it is still a child with no agent behind it yet.
		c.set(StateQueued, "queued · copying the workspace")
		s.emitUpdate(c)
		w, err := s.openWorkspace(c, ctx, maxRounds, attempt)
		if err != nil {
			// This attempt's own cancel, captured above: a failure sets the
			// state a retry claims the child by, and by the time the child
			// is marked failed c.cancel may already govern that retry's wait
			// rather than anything of this attempt's.
			cancel()
			finish(StateFailed, observe.ChildFailed, "failed · "+firstLine(err.Error()))
			s.emitUpdate(c)
			return
		}
		c.mu.Lock()
		c.install(w)
		endRec = c.rec.End
		c.mu.Unlock()
		worktree, repoTop = w.wt.dir, w.wt.repoTop
	}

	c.set(StateRunning, "running")
	// An attempt that replaces another says so on its own record, at the
	// start. The row the failed attempt wrote is closed by the time anything
	// replaces it, so a retry is invisible from there — and a reader who is
	// not joining rows by their attempt number has nothing else that says
	// this run is a second go at work that already failed once.
	if attempt > 1 {
		c.signalAt(0, observe.SignalSubagent, observe.ChildRetry)
	}
	s.emitUpdate(c)

	// The rows this attempt's calls have open start empty: whatever the
	// attempt before it left open belongs to a conversation nothing will
	// answer, and a row still claiming a call's id would take the next
	// attempt's result for that call.
	c.mu.Lock()
	c.callRow = map[string]int{}
	c.mu.Unlock()
	signal := func(code, reason string) {
		if c.rec.Signal != nil {
			c.rec.Signal(c.pos(), code, reason)
		}
	}
	// The gated tier's dispatcher, inside whatever the surface puts around
	// it. It is built once for the attempt rather than per call: the wrap is
	// a chain of closures, and a child's rounds are the last place to be
	// rebuilding one.
	resolve := func(tc provider.ToolCall) string { return s.resolveGated(c, tc) }
	if c.env.WrapGated != nil {
		resolve = c.env.WrapGated(c.seam(), resolve)
	}
	h := &agent.Headless{
		Agent:   c.agent,
		Compact: childCompactor(c.model, c.env),
		// A child is as unwatched as a headless run, and its task is the
		// instruction every reading is judged against. Nil where
		// summary.subagents turned the reading off.
		Summary: agent.NewSummaryRun(c.env.Summarizer, agent.NewRecorder(0), c.task).
			WithChanges(c.changed).
			WithSweeps(c.env.Sweeps),
		// A child that recycled its conversation says so on its lane, which
		// is the only place anyone is looking: a child whose answer came out
		// of a summary of its own work is a different reading from one that
		// still had every result in front of it.
		OnCompact: func(n agent.CompactNotice) {
			c.appendEntry(TranscriptEntry{Kind: EntrySystem, Text: n.Notice})
			if n.Elided > 0 {
				signal(observe.SignalTrim, observe.TrimReason(n.Elided, n.BeforePct, n.AfterPct))
			}
			if n.Compacted {
				signal(observe.SignalCompact, observe.CompactPressure)
			}
			s.emitUpdate(c)
		},
		// A child that reached the model's output ceiling says so on its
		// lane, which is the only place anyone is looking: an answer that
		// came back in two halves, or a round that lost a call it had not
		// finished writing, is a different reading from a clean one.
		OnContinue: func(notice string) {
			c.appendEntry(TranscriptEntry{Kind: EntrySystem, Text: notice})
			s.emitUpdate(c)
		},
		OnIntervene: func(iv agent.Intervention) {
			c.appendEntry(TranscriptEntry{Kind: EntrySystem, Text: iv.Notice})
			signal(observe.SignalIntervene, iv.Kind.Signal())
			// A steered child says so where the parent looks — its lane and
			// the roster — because the transcript row above is on a surface
			// nobody has attached to, and a child that is not answering its
			// steer is the parent's to redirect or end
			// (docs/capabilities/subagents.md#they-are-visible-while-they-run).
			if iv.Kind == agent.InterveneSteer {
				c.mu.Lock()
				c.steers++
				c.steersAll++
				c.steerFrom = SteerFromReading
				c.mu.Unlock()
			}
			s.emitUpdate(c)
		},
		OnSummary: func(v agent.SummaryVerdict) {
			// The reading's own round, not the child's live one. This is the
			// one hook that can be called from the reading's goroutine while
			// the child goes on running — a closing reading is handed over
			// whenever it comes back, and the attempt it was reading may
			// have been retried by then — so the live counter here is both a
			// read of another goroutine's field and an answer to the wrong
			// question. The verdict is about the round its evidence was
			// taken at, which is the round it states.
			c.signalAt(v.Round, observe.SignalSummary, observe.SummaryCode(v.State))
			// A reading that did not happen leaves the last one standing:
			// the surfaces mark a failed reading stale rather than blanking
			// what it was revising.
			//
			// The word is recorded and nothing is emitted. This hook is the
			// one that can be called from the reading's own goroutine after
			// the run it was reading has returned — a closing reading is
			// handed straight over rather than parked for a boundary that
			// will never come — and an update pushed from there is a send on
			// the event channel the supervisor has already shut. The next
			// update the child's own goroutine emits carries the word, and a
			// child whose last act was a reading has nothing left to draw.
			if !v.Failed {
				c.mu.Lock()
				c.verdict, c.verdictCode = v.State.String(), observe.SummaryCode(v.State)
				c.mu.Unlock()
			}
		},
		// An interruption a reading earned and did not get. Nothing was said
		// to the child, so nothing goes on its lane; the record is the only
		// place a fan-out whose readings all land too late can be told from
		// one whose children never drifted.
		OnWithheld: func(reason string) {
			signal(observe.SignalIntervene, reason)
		},
		// The workspace moved under the child. The message has already
		// joined its conversation by the time this is called; the row is so
		// that a parent attaching to the lane can see why the child went
		// back and re-read a file it had already read.
		OnTree: func(n agent.TreeNotice) {
			c.appendEntry(TranscriptEntry{Kind: EntrySystem, Text: n.Notice})
			signal(observe.SignalTree, n.Signal())
			s.emitUpdate(c)
		},
		Gate:    func(tc provider.ToolCall) bool { return c.env.Gated[tc.Name] },
		Resolve: resolve,
		Steer: func() []string {
			msgs := c.drainSteering()
			if len(msgs) > 0 {
				// A person redirecting the child mid-turn is answering the
				// very count this carries, and the loop starts the turn's
				// reckoning again for exactly that reason. The child's own
				// copy owes the same reset: left standing, the lane and the
				// roster would go on reporting a child as not answering its
				// steer while it works on the instruction that answered it.
				c.mu.Lock()
				c.steers = 0
				c.mu.Unlock()
				s.emitUpdate(c)
			}
			return msgs
		},
		Hold: func() <-chan struct{} { return s.holdFor(c) },
		OnUsage: func(u *provider.Usage) {
			if c.addUsage(u) {
				c.cancel()
			}
			if c.rec.Usage != nil {
				in, out := c.attemptSpend()
				// A child runs its whole life on one model, so its totals go
				// out unpriced for the recorder to price at that model —
				// unlike a session's, which are a mixture only the ledger
				// that billed each request can price.
				c.rec.Usage(c.pos().Turn, in, out, 0, false)
			}
		},
		OnText: func(text string) {
			c.mu.Lock()
			c.streaming += text
			c.mu.Unlock()
			s.emitUpdate(c)
		},
		OnToolCall: func(tc provider.ToolCall) {
			c.beginToolEntry(tc.ID, tc.Name, tc.Arguments)
			c.mu.Lock()
			c.toolCalls++
			n := c.toolCalls
			c.mu.Unlock()
			c.set(StateRunning, "running · "+plural(n, "tool"))
			s.emitUpdate(c)
		},
		OnToolResult: func(r agent.ToolResult) {
			c.settleToolEntry(r.Call.ID, r.Result)
			c.noteWrite(r.Call, r.Result)
			if c.rec.ToolCall != nil {
				outcome, class := observe.ToolOutcome(r.Result)
				c.rec.ToolCall(c.pos(), r.Call.Name, r.Duration, outcome, class)
			}
			if agent.IsRepeatNotice(r.Result) {
				signal(observe.SignalRepeat, r.Call.Name)
			}
			s.emitUpdate(c)
		},
		// A child's retry is a status update on its lane: the one place a
		// parent watching a fan-out can see that a writer is waiting out a
		// limit rather than thinking.
		OnRetry: func(n agent.RetryNotice) {
			if n.Partial != "" {
				// What the broken stream wrote is dropped rather than
				// flushed: the retry asks the whole question again, and a
				// transcript holding both would show the child answering
				// twice with the first answer cut off mid-sentence.
				c.mu.Lock()
				c.streaming = ""
				c.mu.Unlock()
			}
			c.set(StateRunning, fmt.Sprintf("waiting · retry %d of %d", n.Attempt, n.Max))
			signal(observe.SignalRetry, n.Signal())
			s.emitUpdate(c)
		},
	}
	h.SetRetryLimit(c.env.Retries)
	c.mu.Lock()
	c.headless = h
	c.mu.Unlock()

	// The turn loop: a cancelled turn parks the child idle until a
	// steering message starts the next one; kill (context cancellation) ends
	// the loop from any point.
	//
	// The task is what every later reading of this child is judged against
	// and what its roster row states, so it stays as it was written; what a
	// retry adds goes in front of it, in this turn alone.
	turn := c.task
	if p := c.takePrologue(); p != "" {
		turn = p + turn
	}
	c.appendEntry(TranscriptEntry{Kind: EntryUser, Text: turn})
	// A child's turn closes with the same event a session's does, so the two
	// populations answer "how many rounds did that take, and how did it end"
	// the same way. The rounds ride the position, as they do everywhere else.
	var turnStart time.Time
	endTurn := func(outcome string) {
		if c.rec.Turn == nil {
			return
		}
		at := c.pos()
		c.rec.Turn(at.Turn, at.Round, time.Since(turnStart), outcome)
	}
	for {
		c.beginTurn()
		turnStart = time.Now()
		report, err := h.Run(turn)
		c.flushStreaming()

		if err == nil && c.ctx.Err() != nil {
			// The turn finished and the child's context is gone; which of the
			// two ways that happened decides what this is.
			//
			// The budget is measured after the fact (addUsage), so the
			// response that trips it is one the child had already finished:
			// it did the work and the session has already paid for it, and
			// the overrun only becomes visible with the answer in hand.
			// Calling that "cancelled" names the mechanism rather than the
			// reason and throws the report away — so a child that overspent
			// on its way past the post stops for the reason it actually
			// stopped for, with its own final report where the
			// handoff would otherwise go.
			//
			// A kill is the other way, and a killed child whose provider
			// closed the stream quietly must never report success.
			c.agent.CancelTurn()
			c.mu.Lock()
			budgetHit := c.budgetHit
			if budgetHit && c.report == "" {
				c.report = report
			}
			c.mu.Unlock()
			reason, end := "cancelled", observe.ChildCancelled
			if budgetHit {
				reason, end = budgetReason(c), observe.ChildBudget
			}
			endTurn(observe.TurnCancelled)
			finish(StateFailed, end, reason)
			return
		}

		if err == nil {
			c.mu.Lock()
			c.report = report
			tools := c.toolCalls
			c.mu.Unlock()

			// Steering that arrived during the final stream becomes the next
			// turn instead of being dropped (the TUI's dispatchSteering
			// semantics).
			if msgs := c.drainSteering(); len(msgs) > 0 {
				endTurn(observe.TurnDone)
				turn = strings.Join(msgs, "\n\n")
				c.set(StateRunning, "running")
				s.emitUpdate(c)
				continue
			}

			if c.profile.Writes {
				s.reviewPatch(c)
				c.mu.Lock()
				note := c.patchNote
				c.mu.Unlock()
				if note != "" {
					c.appendEntry(TranscriptEntry{Kind: EntrySystem, Text: note})
				}
			}

			endTurn(observe.TurnDone)
			finish(StateDone, observe.ChildDone, "done · "+plural(tools, "tool"))
			return
		}

		if errors.Is(err, agent.ErrInterrupted) && c.ctx.Err() == nil {
			// Turn cancelled by the user: the conversation is already
			// well-formed (synthetic results); wait for steering or a kill.
			endTurn(observe.TurnCancelled)
			c.appendEntry(TranscriptEntry{Kind: EntrySystem, Text: "Turn cancelled — send a message to continue."})
			c.set(StateIdle, "idle · turn cancelled")
			s.emitUpdate(c)
			next, ok := s.awaitSteering(c)
			if !ok {
				finish(StateFailed, observe.ChildCancelled, "cancelled")
				return
			}
			turn = next
			c.set(StateRunning, "running")
			s.emitUpdate(c)
			continue
		}

		if errors.Is(err, agent.ErrRoundCap) && c.ctx.Err() == nil {
			// The round limit is a check-in, not a stop. The cap is
			// tested between rounds, after the last round's results were
			// recorded, so the conversation is already well-formed: the child
			// picks up exactly where it left off, with the check-in as its
			// next turn and a budget that has grown.
			used := c.agent.Rounds()
			// The cap is a check-in and not a stop, which is exactly what a
			// session's cap-paused turn is: the turn that reached it closed
			// there, and the check-in it prompts is the next one.
			endTurn(observe.TurnCapPaused)
			grown := c.agent.MaxRounds() * checkInGrowth
			c.agent.SetMaxRounds(grown)
			c.mu.Lock()
			c.checkIns++
			n := c.checkIns
			// The widened cap, so a retry of this attempt starts where it
			// talked its way to rather than back at the spawn's number.
			c.maxRounds = grown
			c.mu.Unlock()
			c.appendEntry(TranscriptEntry{Kind: EntrySystem,
				Text: fmt.Sprintf("Check-in %d — %d rounds used. Taking stock, then carrying on.", n, used)})
			// Through the child's own agent, so a configured wording and the
			// child's own closing line reach this check-in as well as the
			// interval's.
			turn = c.agent.CheckInMessage()
			c.set(StateRunning, fmt.Sprintf("running · check-in %d", n))
			s.emitUpdate(c)
			continue
		}

		// Keep the child's conversation well-formed for inspection.
		outcome := observe.TurnFailed
		if errors.Is(err, agent.ErrInterrupted) {
			// A killed child's turn was interrupted, not defeated: the
			// distinction is the one a session already draws, and folding it
			// into failure would count every kill as a failing turn.
			outcome = observe.TurnCancelled
		}
		endTurn(outcome)
		c.agent.CancelTurn()
		s.finalCheckIn(c)
		finish(StateFailed, childEndReason(c, err), s.failReason(c, err))
		return
	}
}

// awaitSteering blocks an idle child until steering arrives (ok=true, with
// the joined message that starts the next turn) or the child is killed.
// Queued messages were already added to the transcript by drainSteering.
func (s *Supervisor) awaitSteering(c *child) (string, bool) {
	for {
		select {
		case <-c.steerWake:
			if msgs := c.drainSteering(); len(msgs) > 0 {
				return strings.Join(msgs, "\n\n"), true
			}
		case <-c.ctx.Done():
			return "", false
		}
	}
}

// finalCheckInTimeout bounds the handoff completion. It is short on purpose:
// the child is over its budget and on its way out either way, and a handoff
// that takes longer than this is worth less than the delay it adds before the
// parent hears that the child failed.
const finalCheckInTimeout = 30 * time.Second

// finalCheckInPrompt asks a child that ran out of budget to hand over. It
// does not ask for more work, and says so: the child has nothing left to
// spend, and a handoff that starts another edit is worse than none.
const finalCheckInPrompt = `You have reached your token budget and are stopping now. Do not start any new work or call any tools.

Write a short handoff for whoever picks this up: what you established or changed, what is left, and what you would do next.`

// finalCheckIn asks a child that exhausted its token budget to say where it
// got to, and records the answer as its report. The budget stays a
// hard stop — the child is finished either way — but a stop that explains
// itself leaves the parent something to act on rather than a spend figure and
// a shrug.
//
// All of it is best-effort. The child's own context was cancelled the moment
// the budget tripped, so this runs on a fresh one; the spend it costs is
// counted, and it is past a bound that the response which tripped it had
// already passed (addUsage measures after the fact). Every failure returns
// silently: the handoff improves the failure message, it is never a
// precondition for it, and a child that cannot produce one must still fail
// for the reason it actually failed for. In particular a child that ran out
// of context rather than money cannot answer this, and must not be made to
// look like it failed for a different reason because the handoff also failed.
func (s *Supervisor) finalCheckIn(c *child) {
	c.mu.Lock()
	hit, root, model, paths := c.budgetHit, c.root, c.model, c.paths
	worktree := c.worktree != ""
	c.mu.Unlock()
	if !hit {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), finalCheckInTimeout)
	defer cancel()
	env, err := s.opts.NewEnv(ctx, Spec{Name: c.name, Role: c.role, Root: root, Model: model, Paths: paths,
		Worktree: worktree})
	if err != nil {
		return
	}
	msgs := append(c.agent.RequestMessages(),
		provider.Message{Role: provider.RoleUser, Content: finalCheckInPrompt})
	// The prompt tells the child not to call a tool and the request enforces
	// it, because nothing here reads a tool call: a handoff that arrived as
	// one leaves the builder below empty and the parent with no report at
	// all, which is the failure this whole call exists to prevent.
	events, stop, err := env.Stream(msgs, provider.ToolChoiceNone)
	if err != nil {
		return
	}
	defer stop()

	var b strings.Builder
	for e := range events {
		if e.Err != nil {
			return
		}
		b.WriteString(e.Token)
		if e.Usage != nil {
			// The handoff is the child's spend like anything else it did.
			c.addUsage(e.Usage)
		}
		if e.Done {
			break
		}
	}
	text := strings.TrimSpace(b.String())
	if text == "" {
		return
	}
	c.appendEntry(TranscriptEntry{Kind: EntryAssistant, Text: text})
	c.mu.Lock()
	if c.report == "" {
		c.report = text
	}
	c.mu.Unlock()
}

// budgetReason is how a child that ran out of tokens says so, in the one
// wording every path that stops for the budget uses. It says "new" because
// the spend beside it on the same row is the billed figure, which a cached
// prompt puts well above the budget without ever reaching it (addUsage).
func budgetReason(c *child) string {
	return fmt.Sprintf("failed · token budget (~%s new) exceeded · %s",
		formatTokens(c.maxTokens), wroteNote(len(c.written())))
}

// wroteNote is what a child had to show for a budget when the budget ran out.
//
// It is on the failure line and not only in the record because that line is
// what the parent reads, and the two endings behind one budget failure want
// different answers from it: a child that ran out mid-edit is worth retrying
// on the work it left, and one that ran out having written nothing spent a
// whole budget on reading and wants a narrower task instead. A spend figure
// alone cannot tell them apart.
func wroteNote(files int) string {
	if files == 0 {
		return "nothing written"
	}
	return fmt.Sprintf("%d %s written", files, plural(files, "file"))
}

// childEndReason is the same fork failReason takes, in the record's closed
// vocabulary. The two are written together and read the same fields on
// purpose: a lane that says one thing and a row that says another about the
// same attempt is the failure the closed set exists to prevent, and the only
// way they stay in step is being one decision.
//
// A provider failure is separated from everything else because it is the one
// end nobody chose: a budget is a number somebody set, a cap is a number
// somebody set, a kill is somebody pressing a key, and a run that stops
// because the provider stopped answering says nothing about any of them.
// Folded together they would all read as the fan-out being tuned wrong.
func childEndReason(c *child, err error) string {
	c.mu.Lock()
	budgetHit := c.budgetHit
	c.mu.Unlock()
	switch {
	case budgetHit:
		return observe.ChildBudget
	case errors.Is(err, agent.ErrRoundCap):
		return observe.ChildCap
	case c.ctx.Err() != nil:
		return observe.ChildCancelled
	}
	if _, ok := provider.AsFailure(err); ok {
		return observe.ChildProvider
	}
	return observe.ChildFailed
}

func (s *Supervisor) failReason(c *child, err error) string {
	c.mu.Lock()
	budgetHit := c.budgetHit
	c.mu.Unlock()
	switch {
	case budgetHit:
		return budgetReason(c)
	case errors.Is(err, agent.ErrRoundCap):
		return fmt.Sprintf("failed · round limit (%d) reached", c.agent.MaxRounds())
	case c.ctx.Err() != nil:
		return "cancelled"
	default:
		return "failed · " + firstLine(err.Error())
	}
}

// resolveGated is the child's approval path: the child's clamped mode policy
// decides, and anything it would ask about routes to the parent user. Every
// verdict along the way is recorded at the codes a session records its own
// at — an approval rate that covered the parent and not its children would
// be a rate over the half of the work a person was looking at.
func (s *Supervisor) resolveGated(c *child, tc provider.ToolCall) string {
	raw := json.RawMessage(tc.Arguments)
	rooted, err := RootArgs(c.root, tc.Name, raw)
	if err != nil {
		return "error: " + err.Error()
	}

	action, actionErr := actionFor(tc.Name, rooted)
	if actionErr != nil {
		return "error: " + actionErr.Error()
	}
	action = s.scopedAction(c, action)
	policy := s.childPolicy(c)
	title := askTitle(tc.Name, action)
	decision, reason := policy.Decide(action)
	// The policy's reason is free text until it goes through ReasonCode,
	// which is where it stops being able to carry the path it names.
	record := func(d, code string) {
		if c.rec.Decision != nil {
			c.rec.Decision(c.pos(), d, code)
		}
	}
	// The static policy denies for a command the user's deny list names,
	// which is refused whatever this child's mode is; in plan mode, which
	// refuses the call with the result that tells the model why nothing ran;
	// and for a path no grant can reach, which says which path and why.
	if decision == agent.Deny {
		record(observe.DecisionDeny, observe.ReasonCode(reason))
		if reason == agent.DenyReasonDenylist {
			c.appendEntry(TranscriptEntry{Kind: EntrySystem, Text: "Refused: " + title + " — " + reason})
			return agent.DenylistResult
		}
		if strings.HasPrefix(reason, "outside the working scope") {
			c.appendEntry(TranscriptEntry{Kind: EntrySystem, Text: "Refused: " + title + " — " + reason})
			return agent.ScopeRefusedResult(reason)
		}
		return agent.PlanModeResult
	}
	// Whether the classifier is what decided, because from here the rule has
	// a name of its own and a duration behind it — which the policy's reason
	// never has, and which the record's code can never carry.
	classified := false
	var cost time.Duration
	if decision == agent.Ask {
		var denial string
		decision, cost, denial = s.classify(c, policy.Mode, tc, action)
		classified = true
		if decision == agent.Deny {
			record(observe.DecisionDeny, observe.ReasonClassifier)
			c.appendEntry(TranscriptEntry{Kind: EntrySystem,
				Text: "Refused (" + classifierAccount(cost) + "): " + title + " — " + denial})
			return "error: auto mode denied this tool call: " + denial
		}
	}
	switch decision {
	case agent.Allow:
		code, rule := observe.ReasonCode(reason), reason
		if classified {
			code, rule = observe.ReasonClassifier, classifierRule
		}
		record(observe.DecisionAllow, code)
		// The account rides the act, in the field the call's own row keeps
		// for it. A refusal keeps its row because there is no act under it
		// to carry the reason; an approval has one.
		c.noteAllowed(tc.ID, rule, cost)
	case agent.Ask:
		// Two events, as a session records: what put the call in front of a
		// person, and what they said. The first is what a prompt-rate is
		// made of and the second is what an approval-rate is, and one event
		// carrying both could answer neither.
		record(observe.DecisionAsk, observe.AskReason(action))
		ask, askErr := s.buildAsk(c, tc.Name, rooted, action)
		if askErr != nil {
			// A child's refusals are rows in its own transcript, which the
			// parent mirrors when it attaches — so a file that moved under
			// the child reads exactly as one that moved under the session.
			var stale tools.StaleError
			if errors.As(askErr, &stale) {
				c.appendEntry(TranscriptEntry{
					Kind:   EntrySystem,
					Text:   stale.Skipped(displayPath(c.root, stale.Path)),
					Result: stale.Error(),
				})
			}
			return "error: " + askErr.Error()
		}
		approved, ok := s.await(c, ask)
		if !ok {
			return agent.CancelledResult
		}
		if !approved {
			record(observe.DecisionDeny, observe.ReasonUser)
			if action.Kind == agent.ActionCommand {
				return "error: the user declined to run this command"
			}
			return "error: the user declined this tool call"
		}
		record(observe.DecisionAllow, observe.ReasonUser)
	}

	if tc.Name == tools.ExecCommandName {
		if c.env.RunCommand == nil {
			return "error: command execution is not available to this agent"
		}
		out, code := c.env.RunCommand(c.ctx, action.Command)
		// The runner has already scrubbed the output, so the reduction — and
		// the copy the evidence store keeps of it — is over the text the
		// child is allowed to see, as it is on the parent.
		return c.env.execResult(out, code)
	}
	return agent.ExecuteWith(c.env.ExecuteGated, provider.ToolCall{ID: tc.ID, Name: tc.Name, Arguments: string(rooted)})
}

// classify runs the auto-mode permission classifier for a call the
// static policy would have asked about, giving children the same treatment
// the parent gets. Anything other than auto mode, a missing classifier, or a
// safety-flagged action leaves the decision at Ask — the classifier can only
// ever remove a prompt it is allowed to remove, never add permission.
// It returns the decision, what the judgement took — the one rule whose cost
// the child's transcript states — and, for a denial, the reason the model is
// told.
func (s *Supervisor) classify(c *child, mode agent.Mode, tc provider.ToolCall, action agent.Action) (decision agent.Decision, cost time.Duration, denial string) {
	if mode != agent.ModeAuto || s.opts.Classifier == nil || action.SafetyFlagged {
		return agent.Ask, 0, ""
	}
	v := s.opts.Classifier.Judge(c.ctx, agent.ClassifierRequest{
		Tool:      tc.Name,
		Arguments: tc.Arguments,
		CWD:       c.root,
		Recent:    c.agent.RequestMessages(),
	})
	// Classifier spend is the child's spend: it counts toward the child's
	// token budget, and exhausting it cancels the child like any other
	// overrun.
	if c.addUsage(&v.Usage) {
		c.cancel()
	}
	if c.rec.Usage != nil {
		in, out := c.attemptSpend()
		// The turn count goes back with it: the totals are a whole-row
		// update, so reporting spend without it would blank the column the
		// last turn wrote.
		c.rec.Usage(c.pos().Turn, in, out, 0, false)
	}
	verdict, reason := agent.ResolveAuto(action, v)
	switch {
	case verdict == agent.Allow:
		return agent.Allow, v.Elapsed, ""
	case verdict == agent.Deny:
		return agent.Deny, v.Elapsed, reason
	case v.Failed:
		// Fails closed: the user decides, and sees why they were asked.
		c.appendEntry(TranscriptEntry{Kind: EntrySystem, Text: "Classifier unavailable (" + v.Reason + "); asking the user instead."})
	}
	return agent.Ask, 0, ""
}

// classifierRule is what a child's transcript names as the rule when the
// auto-mode judge is what allowed a call. It is the word the session's own
// row uses for the same decision, so a parent mirroring a child reads the
// same account it reads in its own feed.
const classifierRule = "classifier"

// classifierAccount is the same rule written for a row that has no field to
// put the cost in: a refusal is its own notice, and the seconds the
// judgement took go in the notice's own parenthesis.
func classifierAccount(cost time.Duration) string {
	return fmt.Sprintf("%s, %.1fs", classifierRule, cost.Seconds())
}

// askTitle is the one-line description of a gated call for the child's own
// transcript.
func askTitle(name string, action agent.Action) string {
	if action.Kind == agent.ActionCommand {
		return "run " + firstLine(action.Command)
	}
	return "use " + name
}

// actionFor classifies a gated call for the mode policy.
func actionFor(name string, args json.RawMessage) (agent.Action, error) {
	switch {
	case name == tools.ExecCommandName:
		var a struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal(args, &a); err != nil || strings.TrimSpace(a.Command) == "" {
			return agent.Action{}, errors.New("invalid command arguments")
		}
		cmd := strings.TrimSpace(a.Command)
		return agent.Action{
			Kind:          agent.ActionCommand,
			Command:       cmd,
			SafetyFlagged: len(safety.Check(cmd)) > 0,
		}, nil
	case name == web.FetchToolName:
		// A child's fetch is decided on the same host the parent's card
		// would have named, so a granted host is as quiet in a child as it
		// is in the session that granted it.
		return agent.Action{Kind: agent.ActionFetch, Host: web.FetchHost(args)}, nil
	case tools.IsMutating(name):
		return agent.Action{Kind: agent.ActionEdit}, nil
	}
	return agent.Action{Kind: agent.ActionOther}, nil
}

// scopedAction fills in what a child's command reaches outside the working
// scope: its own worktree plus whatever the parent session has put in
// scope. A child's file edits never need this — RootArgs already refuses a
// path outside the worktree — so it applies to commands, which can name any
// path they like.
func (s *Supervisor) scopedAction(c *child, a agent.Action) agent.Action {
	if s.opts.ScopeDirs == nil || a.Kind != agent.ActionCommand || c.root == "" {
		return a
	}
	sc, _ := scope.New(c.root, s.opts.ScopeDirs()...)
	if sc == nil {
		return a
	}
	dirs := sc.Outside(radius.WritePaths(a.Command)...)
	if len(dirs) == 0 {
		return a
	}
	a.OutOfScope = dirs
	for _, d := range dirs {
		class, reason := scope.Classify(d)
		switch class {
		case scope.Refused:
			a.ScopeRefused, a.ScopeReason = true, reason
		case scope.Sensitive:
			if !a.ScopeRefused {
				a.ScopeSensitive, a.ScopeReason = true, reason
			}
		}
	}
	return a
}

// buildAsk assembles the approval request the parent user reviews, and stamps
// it with where the child's paths live. The stamp is applied in one place
// rather than per kind so a request added later cannot reach the parent's
// card with nothing behind its blast-radius block.
func (s *Supervisor) buildAsk(c *child, name string, rooted json.RawMessage, action agent.Action) (*Ask, error) {
	ask, err := askFor(c, name, rooted, action)
	if err != nil {
		return nil, err
	}
	ask.Root, ask.Worktree = c.root, c.worktree != ""
	return ask, nil
}

// askFor is the request itself: the variant, and what only that variant knows.
func askFor(c *child, name string, rooted json.RawMessage, action agent.Action) (*Ask, error) {
	switch action.Kind {
	case agent.ActionCommand:
		ask := NewAsk(c.name, AskCommand, "run "+firstLine(action.Command))
		ask.Command = action.Command
		for _, w := range safety.Check(action.Command) {
			ask.Warnings = append(ask.Warnings, w.Risk)
		}
		return ask, nil
	case agent.ActionEdit:
		mut, err := tools.PreviewMutation(name, rooted)
		if err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		path := displayPath(c.root, mut.Path)
		ask := NewAsk(c.name, AskEdit, mut.Action+" "+path)
		ask.Path = path
		ask.Hunks = diff.Compute(mut.OldText, mut.NewText)
		return ask, nil
	}
	ask := NewAsk(c.name, AskGeneric, "use "+name)
	ask.Summary = compactArgs(rooted)
	return ask, nil
}

// await routes one ask to the parent and blocks the child until the user
// answers or the child is cancelled (killed, or its turn interrupted); ok is
// false on cancellation.
func (s *Supervisor) await(c *child, ask *Ask) (approved, ok bool) {
	c.set(StateBlocked, "waiting approval: "+ask.Title)
	s.emit(Event{Kind: EventAsk, Ask: ask, Status: c.status()})
	select {
	case v := <-ask.resp:
		c.set(StateRunning, "running")
		s.emitUpdate(c)
		return v, true
	case <-c.interruptCh():
		return false, false
	case <-c.ctx.Done():
		return false, false
	}
}

// reviewPatch computes the writer's worktree patch and routes it through the
// approval flow before anything touches the real checkout.
func (s *Supervisor) reviewPatch(c *child) {
	patch, err := worktreePatch(c.worktree)
	if err != nil {
		c.mu.Lock()
		c.patchNote = "the worktree patch could not be computed: " + firstLine(err.Error()) + "; no files were changed"
		c.mu.Unlock()
		return
	}
	if strings.TrimSpace(patch) == "" {
		c.mu.Lock()
		c.patchNote = "no file changes were made"
		c.mu.Unlock()
		return
	}

	hunks, files := PatchHunks(patch)
	adds, dels := diff.Stats(hunks)
	title := fmt.Sprintf("apply patch (+%d −%d, %d file(s))", adds, dels, files)
	touched := PatchFiles(patch)
	ask := NewAsk(c.name, AskPatch, title)
	ask.Hunks = hunks
	// A patch is the one child request that writes the reader's own files, so
	// it is measured in the reader's own checkout: the worktree the child
	// edited in is not where any of this lands.
	ask.Root, ask.Files = c.repoTop, touched
	// Two writers can hold the same file in separate worktrees; the collision
	// only becomes visible when the second patch lands on top of the first.
	// Say so on the card, before it is applied.
	if clashes := s.patchClashes(c.name, touched); len(clashes) > 0 {
		ask.Warnings = append(ask.Warnings, "overwrites changes already applied by "+strings.Join(clashes, ", "))
	}

	// What the patch names is the whole of what the parent has to know to
	// integrate it, and it is already in hand here: a note that gave only a
	// count sent the parent to `git status` for the names, one round and one
	// approval after the patch had already landed.
	held := "; the patch would have touched " + patchPaths(touched)

	note := ""
	approved, ok := s.await(c, ask)
	switch {
	case !ok:
		note = "cancelled before the patch was reviewed; no files were changed" + savedPatchNote(c.name, patch) + held
	case !approved:
		note = "the user declined the patch; no files were changed" + savedPatchNote(c.name, patch) + held
	default:
		// Both sides are read around `git apply`, in the real checkout: the
		// child's own worktree edits never touched these files, so this is
		// the only place the session can see what its workspace lost and
		// gained.
		before := readSides(c.repoTop, touched)
		if applyErr := applyPatch(c.repoTop, patch); applyErr != nil {
			note = "the patch failed to apply cleanly: " + firstLine(applyErr.Error()) + savedPatchNote(c.name, patch) + held
		} else {
			s.recordApplied(c.name, touched)
			s.emit(Event{
				Kind:   EventPatch,
				Status: c.status(),
				Patch: &PatchApplied{
					Agent: c.name,
					Files: patchedFiles(s.opts.Root, c.repoTop, touched, before, readSides(c.repoTop, touched)),
				},
			})
			note = fmt.Sprintf("patch applied to the workspace (+%d −%d, %d file(s)): %s", adds, dels, files, patchPaths(touched))
		}
	}
	c.mu.Lock()
	c.patchNote = note
	c.mu.Unlock()
}

// maxNotedPatchPaths bounds the file list a patch note carries. The file
// count is stated beside the list either way, so a patch longer than this
// loses the names past it and nothing about its size; what it buys is that a
// mechanical change over two hundred files cannot spend a page of the
// parent's context on a list the parent would then have to summarise.
const maxNotedPatchPaths = 20

// patchPaths renders a patch's own file list for the note the parent reads.
func patchPaths(files []string) string {
	if len(files) <= maxNotedPatchPaths {
		return strings.Join(files, ", ")
	}
	return strings.Join(files[:maxNotedPatchPaths], ", ") +
		fmt.Sprintf(" and %d more", len(files)-maxNotedPatchPaths)
}

// patchClashes names the other agents whose applied patches already touched
// any of these files, most recent writer per file.
func (s *Supervisor) patchClashes(name string, files []string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	seen := map[string]bool{}
	for _, f := range files {
		other := s.appliedFiles[f]
		if other == "" || other == name || seen[other] {
			continue
		}
		seen[other] = true
		out = append(out, other+" ("+f+")")
	}
	return out
}

// recordApplied remembers which agent's patch last touched each file.
func (s *Supervisor) recordApplied(name string, files []string) {
	s.mu.Lock()
	for _, f := range files {
		s.appliedFiles[f] = name
	}
	s.mu.Unlock()
}

// savedPatchNote persists an unapplied patch so nothing is lost, returning
// the note fragment naming where it went (empty if saving failed).
func savedPatchNote(name, patch string) string {
	f, err := os.CreateTemp("", "shhh-"+name+"-*.patch")
	if err != nil {
		return ""
	}
	defer f.Close()
	if err := f.Chmod(0o600); err == nil {
		if _, err := f.WriteString(patch); err == nil {
			return " (patch saved to " + f.Name() + ")"
		}
	}
	return ""
}

// report implements agent_report: a status overview with no name, or a
// blocking wait for one child's final report.
func (s *Supervisor) report(raw json.RawMessage) (string, error) {
	var args struct {
		Name string `json:"name"`
		Wait *bool  `json:"wait"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("invalid arguments: %w", err)
		}
	}
	if args.Name == "" {
		return s.statusOverview(), nil
	}

	s.mu.Lock()
	c, ok := s.byName[args.Name]
	s.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("no agent named %q; spawn it first, or call agent_report with no arguments for the roster", args.Name)
	}

	if args.Wait == nil || *args.Wait {
		select {
		case <-c.done:
		case <-s.ctx.Done():
			return "", errors.New("cancelled")
		}
	}
	return c.reportText(), nil
}

// steer implements agent_steer: the orchestrator's own words onto the same
// path the child's lane writes to. It is the whole of the tool — no card, no
// second mechanism — because what is new here is who may speak and not what
// happens when they do. There is deliberately no tool beside it for ending a
// child: a writer stopped part-way leaves an unfinished change in a copy of
// the workspace that nobody has judged, and the parent's only evidence is a
// roster line.
// See docs/capabilities/subagents.md#three-can-steer-a-child-and-none-of-them-can-end-it.
func (s *Supervisor) steer(raw json.RawMessage) (string, error) {
	args, err := parseSteerArgs(raw)
	if err != nil {
		return "", err
	}
	// The state is read before the message is delivered, not after: an idle
	// child wakes on the steer, so a status taken afterwards would say
	// "running" about the very child whose turn this message is starting.
	before, _ := s.Get(args.Name)
	if err := s.Steer(args.Name, args.Message, SteerFromParent); err != nil {
		return "", err
	}
	if before.State == StateIdle {
		return fmt.Sprintf("Steered %s. Its turn had been cancelled, so your message starts its next one; collect it with agent_report.", args.Name), nil
	}
	return fmt.Sprintf("Steered %s. It joins the agent's conversation at its next tool round, is judged as part of what the agent was asked for, and the reading that was running is dropped rather than argued with. Do not steer it again in this round — give it rounds to answer, then read the roster.", args.Name), nil
}

// retry implements agent_retry: a second attempt at a failed child's task,
// on the child the session already has.
//
// It is the door the person's own retry key opens, given to the model, and
// the reasons a spawn is put to a card do not reach it: no new agent is
// started, the task is the one already approved, the paths are the ones
// already claimed, and the slot is one already spent. What it spends again
// is the child's own budget, which the session's spend cap and its ledger
// count like every other request. The refusals are the supervisor's — only a
// failed agent can be run again — so a parent that calls this on a running
// child is told what state it is in rather than quietly given nothing.
// See docs/capabilities/subagents.md#a-failed-child-can-be-run-again.
func (s *Supervisor) retry(raw json.RawMessage) (string, error) {
	args, err := parseRetryArgs(raw)
	if err != nil {
		return "", err
	}
	c, err := s.lookup(args.Name)
	if err != nil {
		return "", fmt.Errorf("no agent named %q; call agent_report with no arguments for the roster", args.Name)
	}
	// Read before the attempt is claimed, not after: a retry with nothing to
	// wait for restarts inside the call below, and by the time that returns
	// the budget has already grown and the flag that grew it is cleared.
	c.mu.Lock()
	had := c.maxTokens
	budget, grew := retryBudget(c.maxTokens, c.budgetHit)
	c.mu.Unlock()
	if err := s.Retry(args.Name); err != nil {
		return "", err
	}
	msg := fmt.Sprintf("Retrying %s on its original task. It keeps its name, its slot and any paths it claimed, so this costs no agent slot; the attempt is a fresh conversation that opens with how the last one ended and whatever handoff it left.", args.Name)
	if grew {
		msg += fmt.Sprintf(" It ran out of budget, so this attempt is given ~%s new tokens, up from ~%s.", formatTokens(budget), formatTokens(had))
	}
	return msg + fmt.Sprintf(" It works in the background: call agent_report with name=%q in a later step to collect it.", args.Name), nil
}

// steerMark is what the roster says about a child the machinery has had to
// interrupt: the last reading of its work, then how many times this turn it
// has been told the reading says it has left its task.
//
// Both are words and numbers this package owns — the reading's state comes
// from a closed set and the count is a count — so nothing a child's tools
// read can reach the parent's conversation through here. Neither is stated
// when there is nothing to state: a child on task and inside its first
// reading interval has neither, and a roster that printed an empty verdict on
// every row would be teaching the parent to skip the field. That a row can be
// bare for want of a reader instead is the roster header's to say, once, and
// not every line's.
func steerMark(st Status) string {
	var parts []string
	if st.Verdict != "" {
		parts = append(parts, st.Verdict)
	}
	if mark := steerCount(st); mark != "" {
		parts = append(parts, mark)
	}
	if len(parts) == 0 {
		return ""
	}
	return " · " + strings.Join(parts, " · ")
}

// steerCount is the steering half of that mark: how often the child has been
// steered this turn and who spoke to it last, in the words the lane's own
// note uses (the components package cannot see a Status, so the shape is
// stated twice on purpose and the vocabulary is what must not drift).
//
// The source is stated even where the count is zero, which is what a
// redirect the child has already taken up looks like: the count it answered
// went back to zero when the child took the message, and without the source
// nothing would be left saying the orchestrator had spoken to it at all. The
// count with no source cannot come out of this package — every steer records
// one — and is rendered anyway, because a Status is a value a caller can
// build and a count dropped for want of a word beside it is the worse
// failure.
func steerCount(st Status) string {
	switch {
	case st.Steers > 0 && st.SteerFrom != "":
		return plural(st.Steers, "steer") + " · from " + string(st.SteerFrom)
	case st.Steers > 0:
		return plural(st.Steers, "steer")
	case st.SteerFrom != "":
		return "steered from " + string(st.SteerFrom)
	}
	return ""
}

// readingsNote is the roster's header when nothing is reading the children on
// it. Without it an empty steer field is ambiguous in the one direction that
// costs something: it reads as "every child is on task" when it means "no
// child is being checked", and the parent waits for a verdict that cannot
// arrive. The note names the trigger left standing in its place, because a
// roster that only says a mechanism is off has moved the problem rather than
// solved it.
const readingsNote = "Readings are off for sub-agents (summary.subagents), so no row below will ever say a reading's word or count a steer: an empty field here means nothing is checking, not that nothing is wrong. What is left to judge a child by is its own line — a child whose detail has not moved between two reads several rounds apart is the one to steer."

// readingsOff reports whether no child on the roster has a reader behind it.
// It is asked of the children rather than of a setting because the supervisor
// is handed no config: what decides it is the summariser each child's Env was
// built with, and one that is nil or disabled is a child nothing will read.
// Asking every child rather than the first means a fan-out spawned across a
// change of setting says the reassuring thing only when it is true of all of
// them.
func (s *Supervisor) readingsOff() bool {
	s.mu.Lock()
	kids := make([]*child, len(s.children))
	copy(kids, s.children)
	s.mu.Unlock()
	for _, c := range kids {
		// Under the child's own lock: an environment is installed when the
		// attempt that runs in it starts, which for a writer is on its own
		// goroutine while the roster this answers is being drawn.
		c.mu.Lock()
		enabled := c.env.Summarizer.Enabled()
		c.mu.Unlock()
		if enabled {
			return false
		}
	}
	return true
}

// slotsLine says how much of the session's one spawn budget is gone. Without
// it the ceiling is something the parent discovers by having a spawn refused
// — a round spent, on a plan for a fan-out that was never going to fit — and
// the count is not one it can keep for itself either, since the person, a
// profile drafter and the backlog runner all spawn into the same sixteen.
//
// "Used" and not "in use": a finished agent keeps its slot, because the limit
// is on how many one session may start rather than on how many run at once.
// That is the half a reader assumes wrongly, so the line says it rather than
// leaving a parent to wonder why four finished agents left it twelve.
// See docs/capabilities/subagents.md#limits-are-about-attention-not-resources.
func slotsLine(used int) string {
	line := fmt.Sprintf("%d of %d agent slots used", used, MaxChildren)
	if used >= MaxChildren {
		return line + " — this session can spawn no more; what is left is to steer or retry the agents it has."
	}
	return line + " (a finished agent keeps its slot: the limit is on how many this session may start, not on how many run at once)."
}

func (s *Supervisor) statusOverview() string {
	statuses := s.Snapshot()
	if len(statuses) == 0 {
		return "No agents have been spawned this session."
	}
	var sb strings.Builder
	sb.WriteString(slotsLine(len(statuses)) + "\n\n")
	if s.readingsOff() {
		sb.WriteString(readingsNote + "\n\n")
	}
	for _, st := range statuses {
		label := string(st.Role)
		if st.Model != "" {
			label += ", " + st.Model
		}
		if len(st.Paths) > 0 {
			label += "; " + strings.Join(st.Paths, ", ")
		}
		fmt.Fprintf(&sb, "%s (%s): %s%s — %s\n", st.Name, label, st.Detail, steerMark(st), firstLine(st.Task))
	}
	return strings.TrimRight(sb.String(), "\n")
}

// reportText is what the parent model receives about a child: its status
// line, its final report, and (for writers) what happened to its patch.
//
// The head line carries what the roster carries — the last reading's word,
// how often the child has been steered, who spoke to it last — because the
// two are read by the same model minutes apart, and a fact that reaches one
// and not the other is a fact the parent has to spend a round asking for. A
// child steered three times that comes back calling its own work sufficient
// is a report to check rather than to integrate, and that is only legible
// beside the report itself. Check-ins are said where there were any: a task
// that outgrew the interval its spawn chose several times over covered more
// ground than the spawn asked for.
// See docs/capabilities/subagents.md#what-comes-back-says-what-happened-to-it.
func (c *child) reportText() string {
	st := c.status()
	c.mu.Lock()
	report := c.report
	patchNote := c.patchNote
	c.mu.Unlock()

	// The status line counts the tools itself wherever it has a count to
	// give — `running · 3 tools`, `done · 3 tools` — so the header says it
	// only for the states that do not, rather than saying it twice.
	var sb strings.Builder
	head := fmt.Sprintf("%s (%s) — %s%s", st.Name, st.Role, st.Detail, steerMark(st))
	switch st.State {
	case StateRunning, StateDone:
	default:
		head += " · " + plural(st.ToolCalls, "tool call")
	}
	if st.CheckIns > 0 {
		head += " · " + plural(st.CheckIns, "check-in")
	}
	fmt.Fprintf(&sb, "%s · ~%s tokens\n\n", head, formatTokens(st.TokensIn+st.TokensOut))
	switch {
	case st.State == StateFailed && report == "":
		sb.WriteString("The agent did not finish; no final report was produced.")
	case report == "":
		sb.WriteString("(the agent produced no final report)")
	default:
		sb.WriteString(report)
	}
	if patchNote != "" {
		sb.WriteString("\n\n[" + patchNote + "]")
	}
	return sb.String()
}

// emit delivers a must-see event (asks, completions), giving up only when the
// supervisor is shut down.
func (s *Supervisor) emit(ev Event) {
	select {
	case s.events <- ev:
	case <-s.ctx.Done():
	}
}

// emitUpdate delivers a best-effort progress update; drops are fine because
// rendering reads live snapshots.
func (s *Supervisor) emitUpdate(c *child) {
	select {
	case s.events <- Event{Kind: EventUpdate, Status: c.status()}:
	default:
	}
}

// plural renders "1 tool" / "3 tools", so a status line that counts what a
// child did reads as a sentence rather than as a field.
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i] + " …"
	}
	return s
}

func formatTokens(n int64) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	return fmt.Sprintf("%.0fk", float64(n)/1000)
}

// compactArgs renders tool arguments as a short "k=v" line for generic
// approval summaries.
func compactArgs(raw json.RawMessage) string {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return string(raw)
	}
	var parts []string
	for k, v := range m {
		switch val := v.(type) {
		case string:
			parts = append(parts, k+"="+val)
		default:
			b, _ := json.Marshal(val)
			parts = append(parts, k+"="+string(b))
		}
	}
	return strings.Join(parts, " ")
}
