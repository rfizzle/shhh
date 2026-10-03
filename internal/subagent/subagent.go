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
	"time"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/meter"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/pricing"
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
	// DefaultMaxConcurrent children run at once at one level of delegation;
	// further spawns queue. It is per depth rather than per session because
	// a descendant that queued behind its own ancestor would wait for an
	// agent that is waiting for it
	// (docs/capabilities/subagents.md#a-wait-only-ever-points-down-the-tree).
	DefaultMaxConcurrent = 3
	// DefaultMaxChildren caps how many children one session may spawn in
	// total, wherever in the tree they were spawned, when nothing configures
	// it. It counts starts, and a batch run of a backlog starts one child
	// per item: a measured run started 31 in one sitting, which sixteen
	// would have stopped halfway.
	// See docs/capabilities/subagents.md#limits-are-about-attention-not-resources.
	DefaultMaxChildren = 32
	// MaxChildrenKey is the config key that sets that cap, named in the
	// refusal for the reason MaxDepthKey is.
	MaxChildrenKey = "agents.max_children"
	// DefaultMaxDepth is how deep delegation goes when nothing configures
	// it, counting the session as depth 1: the orchestrator, its children,
	// and theirs. SessionDepth is the session's own.
	// See docs/capabilities/subagents.md#a-child-may-delegate-to-a-configured-depth.
	DefaultMaxDepth = 3
	// SessionDepth is the depth of the session itself, which every child's
	// depth is counted up from: a child of the session is at 2.
	SessionDepth = 1
	// MaxDepthKey is the config key that sets the limit, named in the
	// refusal so a reader is told what to change rather than that they
	// cannot do this.
	MaxDepthKey = "agents.max_depth"
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
	// DefaultMaxTokens is what one writer spent on one backlog item: four
	// such children, each summed over its requests as cache creation plus
	// fresh input plus output, came to 0.7M to 1.2M new tokens while reading
	// 6M to 80M from cache. A default below that stops the child a quarter of
	// the way in and its doubled retry short again, so it is set at the top
	// of that evidence and the ceiling at twice it, where a retry lands.
	DefaultMaxTokens = 1_200_000
	// MinChildMaxTokens is the smallest explicit budget a bounded child may
	// receive. Admission reserves this much after its fixed prompt and task.
	MinChildMaxTokens = 200_000
	MaxTokensCeiling  = 2_400_000
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
	// Budget is the effective fresh-token budget and AdmissionFloor is the
	// minimum that this task's inherited prompt and setup required.
	Budget, AdmissionFloor int64
	// Tokens separates the child budget's fixed setup, tool evidence, inferred
	// analysis, and final handoff. Fresh is the provider-reported total.
	Tokens observe.ChildTokens
	// Spend is what the child has been billed across every attempt, priced
	// request by request as each answer came back. It is a roll-up rather
	// than a token pair because a pair cannot be priced: the input has to be
	// split into what was read fresh and what the provider served from its
	// prompt cache, and a caller handed only the sum charges the whole of it
	// at the fresh rate — several times the real bill on a child whose
	// prompt prefix is re-sent every round.
	Spend meter.Totals
	// Batch groups the children one parent tool round spawned, so a fan-out
	// can be rendered as one block rather than as interleaved rows.
	// Children spawned before the parent opened a batch share batch zero.
	Batch int
	// Started is when the child was spawned; Elapsed is how long it has been
	// alive, frozen at the moment it finished.
	Started time.Time
	Elapsed time.Duration
	// Steps is how far the child is through its steps: the plan a writer
	// named itself where it wrote one, and otherwise the count the spawn
	// declared. Its Total is zero when neither exists, and a lane with no
	// denominator gets a spinner rather than an invented ratio.
	// See docs/capabilities/subagents.md#how-far-along-is-three-numbers-not-one.
	Steps StepCount
	// Turn and Round are where the child is in its own conversation: the
	// turn this attempt is on and the tool round it last started within it,
	// the position its own events are filed at. Neither is the parent's,
	// which counts a different conversation — a child spawned in the
	// parent's third turn is on its first. Both are zero for a child that
	// has not started, and Round is zero for a turn that has asked for no
	// tool yet.
	Turn, Round int64
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
	//
	// It counts the check's own interruptions and nothing else, which is what
	// its sentence says: a person's redirect is not the child looking to have
	// left its task. The two counts below are the rest of this turn's steers,
	// by the party that gave them.
	Steers int
	// LaneSteers is how many of this turn's steers a person gave — typed at
	// the child's lane, into the field the manager opens on its row, or sent
	// by a client over the protocol — and ParentSteers how many the
	// orchestrator that wrote the task gave. Both go back to zero at the
	// child's next turn, beside Steers.
	//
	// They are counts per party rather than one total because a surface that
	// says "steered twice" of one steer of yours and one of the check's has
	// answered the wrong question: what a reader wants of a mixed count is
	// which of them were theirs, and a total cannot be split back afterwards.
	// SteerFrom below is the last party to speak and not a split of the
	// count: a child steered by two parties has one last speaker and two
	// shares, and the two facts are not recoverable from each other.
	LaneSteers   int
	ParentSteers int
	// Verdict is the last reading of this child's work, in the summariser's
	// own closed vocabulary and never its prose. Empty is a child with no
	// reading yet — one in its first interval, or one whose session turned
	// readings off, which is what the roster's own header exists to say.
	Verdict string
	// Handoff is the durable record a replacement can resume from. It is empty
	// for completed children and when durable storage is unavailable.
	Handoff string
	// PatchKept is whether this child holds a change that never reached the
	// checkout, kept in the evidence store and offered for review from its
	// row. It is one fate for every way a writer can end with work unlanded
	// (docs/capabilities/subagents.md#a-failed-child-leaves-a-handoff).
	PatchKept bool
	// RecommendedBudget is the useful budget for a replacement of this attempt.
	RecommendedBudget int64
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
	// Reseeds is how many patches that landed in the parent's checkout while
	// this writer worked have been carried into its copy since the seed, at
	// its own round boundaries. Zero is every child the landing of another
	// did not reach, which is almost every child.
	// See docs/capabilities/subagents.md#a-writer-starts-from-your-tree.
	Reseeds int
	// Held is whether the child has reached its own round boundary while the
	// parent's hold stands. It rides beside the state rather than replacing
	// it because a held child is still a running one — it keeps its slot,
	// its worktree and its conversation, and one release puts it back to
	// work with the round it was about to ask for.
	//
	// A child whose copy is being moved past a landed patch is held too, for
	// the moment that takes, and Reseeding says that is why: it is parked at
	// the same boundary for the same reason — nothing is asked of the model
	// while its tree changes under it.
	//
	// A child waiting for one of the session's check slots is held as well,
	// and SlotWait says that is why: how many checks were running when its
	// wait began, and zero for every child not waiting for one. It is the
	// same park at a different seam — the child stops before the check it
	// asked for rather than at its round boundary — and it is released by the
	// check ahead of it finishing, never by the person.
	// See docs/capabilities/subagents.md#what-they-share.
	Held      bool
	Reseeding bool
	SlotWait  int
	// WaitsOn names the writer a queued child's claim is waiting behind: it
	// was spawned with wait_for_claim, its paths overlap that writer's, and
	// it starts — with a copy of the tree as it stands then — once no writer
	// spawned ahead of it claims a path its own claim meets. Empty for every
	// other child, a slot-queued one included: that one waits for room, and
	// this one for a file.
	// See docs/capabilities/subagents.md#a-writer-starts-from-your-tree.
	WaitsOn string
	// FollowUp is the first words of the follow-up a finished child was
	// handed and is working on now, and empty for every other child.
	// TakesFollowUp is whether a finished child can still be spoken to —
	// false once the session is ending, or for a child whose ancestor was
	// killed (docs/capabilities/subagents.md#three-can-steer-a-child-and-none-of-them-can-end-it).
	FollowUp      string
	TakesFollowUp bool
	// Inheritance is the estimated tokens of the parent's turns this child
	// was handed ahead of its task (spawn_agent's inherit), and zero for the
	// ordinary child, which is handed its task alone. It is a part of the
	// setup the admission floor counted, stated apart because it is the one
	// part of it the spawn chose.
	// See docs/capabilities/subagents.md#what-they-share.
	Inheritance int64
}

// EarlierReport is a report a child gave before a follow-up asked it
// something else: its words, and the turn it closed on the child's
// conversation, which is what a lane heads it with.
type EarlierReport struct {
	Turn int
	Text string
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
	// ApprovedBy names the person who answered the card this call was routed
	// to, and is the other half of the same question AllowedBy answers: how
	// the act came to be allowed. The two never both hold — a call somebody
	// was asked about is not one a rule waved through — and it is a second
	// field rather than a value of AllowedBy because a rule's yes and a
	// person's yes are read for different next acts. Without it a call the
	// parent approved at its own card mirrors back into the parent's feed
	// with no account at all, which is the one row in a fan-out where the
	// account is a person.
	// See docs/interface/principles.md#two-denials-are-not-one-denial.
	ApprovedBy string
	// Checkpoint marks assistant prose that was the public status this
	// child's run was asked for, so the parent's mirror draws it a rung
	// under an answer the way the parent draws its own.
	// See docs/interface/surfaces.md#the-progress-checkpoint.
	Checkpoint bool
}

// ApprovedByUser is what ApprovedBy holds: the account a row gives of an act
// the person at the parent's card allowed. It is a word rather than a flag
// because the field answers "who", and the session's own rows answer it in
// exactly this word — a mirrored row and the row for the session's identical
// call have to read the same.
const ApprovedByUser = "you"

// Spec is everything the CLI needs to build one child's runtime: its role,
// its working directory, and the model it runs on (already resolved by the
// supervisor's ModelFor).
type Spec struct {
	// Name is the child's session-unique name. It is in the spec because a
	// fan-out bills several children at once, and what a child spends is
	// only attributable if the thing building its environment knows which
	// child it is building for.
	Name string
	Role Role
	Root string
	// Parent is the agent that spawned this one, and "" for a child of the
	// session itself. Depth is how far down that puts it, counting the
	// session as SessionDepth: a child of the session is at 2, its own
	// child at 3. Both are in the spec because what the runtime gives a
	// child turns on them — the delegation tools are on a child that has a
	// level below it and off one that does not, and a depth can carry its
	// own default model.
	// See docs/capabilities/subagents.md#a-child-may-delegate-to-a-configured-depth.
	Parent string
	Depth  int
	Model  string
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
	// MaxTokens and AdmissionFloor are the effective budget and the minimum
	// admitted for this declared task. They are carried to the record with the
	// same attempt that spent them.
	MaxTokens, AdmissionFloor int64
	// Attempt is which run of this child the row being opened is, from 1. A
	// retry keeps the child's name and its place in the batch but is a
	// separate run with its own conversation, budget and spend, so it gets a
	// row of its own — and this number is the only thing that joins that row
	// to the one it replaces.
	Attempt int
	// Inherit is how many of its parent's turns the child is handed ahead of
	// its task — the turns it was really given, which is fewer than the spawn
	// asked for where the conversation is shorter — and zero for a child
	// handed its task alone. The prompt says which it is: a child told it
	// cannot see the conversation while holding some of it would doubt the
	// turns, and one told nothing would go looking for the rest.
	Inherit int
	// Integrates names the writer whose conflicting patch this child was
	// started to reconcile, and is empty for every other child. The prompt
	// turns on it: an integration writer is told what its copy holds and
	// how to say it could not reconcile the two.
	// See docs/capabilities/subagents.md#a-conflict-is-a-task-for-a-writer.
	Integrates string
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
	// Handoff persists the opaque, sanitized record a distinct replacement may
	// resume. It is called before End while a writer worktree still exists.
	Handoff func(content []byte) (string, error)
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
	// Prices is what each child's requests are billed against as they come
	// back. It is here rather than at the recorder because pricing is the one
	// thing that has to happen before the totals are summed: the input is
	// charged in parts — read fresh, served from the prompt cache, written to
	// it — and the split survives only per request. nil leaves a child's
	// spend counted in tokens and priced by nobody, which is the case the
	// recorder's own fallback exists for.
	Prices *pricing.Table
	// Now is the clock a child's Started and Elapsed are read off. nil is the
	// wall clock; the field is for a test that draws a live child and needs
	// the age it draws not to depend on how quickly the test ran.
	Now func() time.Time
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
	// ModelFor resolves a child's model from its role, the depth it will run
	// at and the model the spawn call asked for (empty when it asked for
	// none). Nil means every child runs on the session model.
	ModelFor func(role Role, depth int, requested string) string
	// CheckModel answers for a model a spawn call named, before anything is
	// claimed or put to anyone. Nil lets every name through, as a session
	// with nothing to check against has always done.
	CheckModel ModelCheck
	// Profiles is the set of roles a spawn may name; nil means the two
	// built-in ones.
	Profiles Profiles
	// MaxConcurrent bounds simultaneously running children at one depth;
	// <= 0 uses DefaultMaxConcurrent.
	MaxConcurrent int
	// MaxDepth is the deepest an agent may sit, counting the session as
	// SessionDepth; <= 0 uses DefaultMaxDepth. A spawn that would open a
	// level past it is refused before anything is claimed for it.
	MaxDepth int
	// MaxChildren is how many children the session may start in all,
	// wherever in the tree; <= 0 uses DefaultMaxChildren.
	MaxChildren int
	// LoadHandoff resolves an opaque handoff handle for an explicit replacement
	// spawn. It is nil where the session has no durable store.
	LoadHandoff func(handle string) ([]byte, error)
	// SettleHandoff rewrites a stored handoff under its own handle once the
	// kept patch it names has landed, so a later resume reads the work as
	// done. Nil leaves the stored record as it was written.
	SettleHandoff func(handle string, content []byte) error
	// EvidenceExists validates retained evidence before it is offered to a
	// replacement; invalid or expired handles are omitted from its context.
	EvidenceExists func(handle string) bool
	// Untracked reports the files the parent session created that git does
	// not know about, so a writer's worktree can start from them alongside
	// everything `git diff HEAD` already reports. It is asked at each spawn,
	// because a session goes on writing while its children run. Nil carries
	// none, which is a writer starting from the last commit.
	Untracked func() []string
	// Generators is the project's declaration of the files it generates: a
	// writer's patch regenerates those at its landing, and a landing carried
	// into a live writer's copy regenerates them there, rather than merging
	// or applying their bytes. Nil declares none.
	// See docs/capabilities/subagents.md#a-writer-starts-from-your-tree.
	Generators Regenerator
	// CheckSlots is how many checks may run at once across the session — a
	// child's quality gate run, a child's command that is a check, and the
	// session's own gate through CheckSlot; <= 0 uses DefaultCheckSlots.
	// CheckCommands answers the command lines the project's quality config
	// declares, asked at each command, so a child's run of one of them takes
	// a slot as the gate's own run of it would; nil declares none, which
	// leaves HeavyCommands.
	// See docs/capabilities/subagents.md#what-they-share.
	CheckSlots    int
	CheckCommands func() []string
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
