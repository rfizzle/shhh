// Package subagent orchestrates child agents for `shhh code` (S-068): the
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
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/diff"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/radius"
	"github.com/rfizzle/shhh/internal/safety"
	"github.com/rfizzle/shhh/internal/scope"
	"github.com/rfizzle/shhh/internal/tools"
)

// Role scopes a child's toolset: researchers get read-only tools plus the
// web, writers get the full toolset against an isolated worktree.
type Role string

const (
	RoleResearcher Role = "researcher"
	RoleWriter     Role = "writer"
)

// ParseRole maps a spawn_agent role argument to its Role.
func ParseRole(s string) (Role, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "researcher":
		return RoleResearcher, nil
	case "writer":
		return RoleWriter, nil
	}
	return "", fmt.Errorf("unknown role %q (valid: researcher, writer)", s)
}

// Hard budgets and bounds. Concurrency and per-child budgets are deliberately
// bounded: a runaway parent cannot fan out or spend without limit.
const (
	// DefaultMaxConcurrent children run at once; further spawns queue.
	DefaultMaxConcurrent = 3
	// MaxChildren caps how many children one session may spawn in total.
	MaxChildren = 16
	// DefaultMaxRounds leaves a child's tool rounds unbounded (S-144). The
	// limit used to be a hard stop, and a child that reached one failed with
	// its work half done and nothing to hand over — the one outcome worse
	// than letting it run. It is a check-in now: the child takes stock and
	// carries on with a larger budget. That makes the number a pacing choice
	// rather than a safety one, and a pacing choice nobody asked for does not
	// belong on by default. The token budget below is the guard, and it is
	// the one that should be: spend is what actually needs stopping, and it
	// stops a child whatever it happens to be doing.
	//
	// A spawn may still name max_rounds to get periodic check-ins, at any
	// size. Nothing clamps it: the cap only decides how often a child pauses
	// to take stock, so a ceiling could only make it check in more often than
	// it was told to, for no reason that could be given to whoever set it.
	DefaultMaxRounds = agent.UnlimitedToolRounds
	// checkInGrowth multiplies the budget at each check-in, so a long task is
	// not stopped at the same interval forever — the escalation behind the
	// parent's grant (S-109), applied by a child with nobody to ask. A child
	// capped at 25 takes stock at 25, then 50, then 100: often enough early
	// to catch one working on the wrong thing, rare enough later to stay out
	// of the way of one that is not.
	checkInGrowth = 2
	// DefaultMaxTokens and MaxTokensCeiling bound a child's token spend
	// (prompt + completion, provider-reported).
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
	StateIdle    // turn cancelled; waiting for a steering message (S-077)
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
	// can be rendered as one block rather than as interleaved rows (S-110).
	// Children spawned before the parent opened a batch share batch zero.
	Batch int
	// Started is when the child was spawned; Elapsed is how long it has been
	// alive, frozen at the moment it finished.
	Started time.Time
	Elapsed time.Duration
	// Step and Steps are progress against the step count the spawn declared.
	// Steps is zero when nobody declared one, and a lane with no denominator
	// gets a spinner rather than an invented ratio (S-094).
	Step  int
	Steps int
	// Summary is the first line of the child's final report — what a finished
	// lane keeps once its progress stops meaning anything (S-110). Empty
	// until the child reports.
	Summary string
	// CheckIns is how many times the child has reached its round limit and
	// taken stock (S-144). Zero for the ordinary child, which runs unbounded
	// and never reaches one; a lane showing several is a task outgrowing the
	// interval its spawn chose, which is worth being able to see.
	CheckIns int
}

// EntryKind tags one child transcript entry (S-077): the attached view
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
	Kind    EntryKind
	Text    string // user / assistant / system entries
	Tool    string // EntryTool: tool name
	Args    string // EntryTool: raw arguments
	Result  string // EntryTool: result text
	Pending bool   // EntryTool: still executing or awaiting approval
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
	// Gated names the tools that must go through approval routing.
	Gated map[string]bool
}

// Spec is everything the CLI needs to build one child's runtime: its role,
// its working directory, and the model it runs on (already resolved by the
// supervisor's ModelFor).
type Spec struct {
	Role  Role
	Root  string
	Model string
	// Paths is the writer's declared write scope, so its prompt can say what
	// it may touch while other agents work elsewhere; nil means unscoped.
	Paths []string
}

// EnvFactory builds a child's Env; ctx is the child's context (cancelling it
// must abort the child's streams).
type EnvFactory func(ctx context.Context, spec Spec) (Env, error)

// Recorder receives a child's content-free observability events (S-065); any
// callback may be nil.
type Recorder struct {
	Usage    func(turns, tokensIn, tokensOut int64)
	ToolCall func(tool, outcome string)
	End      func()
}

// Options configures a Supervisor.
type Options struct {
	// Root is the parent session's workspace directory.
	Root string
	// NewEnv builds each child's runtime; required.
	NewEnv EnvFactory
	// Record opens a child's observability recorder; nil disables recording.
	Record func(role Role) Recorder
	// CommandAllowlist is the parent's config allowlist, inherited by
	// children (inheriting it keeps the child at most as permissive).
	CommandAllowlist []string
	// ReadOnlyExtra and ReadOnlyDisabled mirror the parent's read-only
	// inspection allowlist settings, so a child's reads are as quiet as the
	// parent's.
	ReadOnlyExtra    []string
	ReadOnlyDisabled bool
	// ScopeDirs reports the parent session's working scope (S-141) — the
	// directories a child's commands may write to on top of its own
	// worktree. Nil leaves children scoped to their worktree alone, which is
	// what they did before the scope existed. A child's *file edits* are
	// pinned to its worktree by RootArgs regardless.
	ScopeDirs func() []string
	// Classifier judges, in auto mode, what the static policy would ask about
	// — the same S-060 path the parent uses. Nil routes those calls to the
	// user instead, which is what made auto-mode children prompt for every
	// command they ran.
	Classifier *agent.Classifier
	// ModelFor resolves a child's model from its role and the model the
	// spawn call asked for (empty when it asked for none). Nil means every
	// child runs on the session model.
	ModelFor func(role Role, requested string) string
	// MaxConcurrent bounds simultaneously running children; <= 0 uses
	// DefaultMaxConcurrent.
	MaxConcurrent int
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
	// in the session changeset (S-097).
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
	Warnings []string    // AskCommand: safety.Check risks
	Hunks    []diff.Hunk // AskEdit / AskPatch: the change to review
	Summary  string      // AskGeneric: one-line description

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
	name      string
	parent    string // spawning agent's name; "" means the orchestrator
	role      Role
	task      string
	model     string   // the model this child runs on
	paths     []string // declared write scope (writers); nil means unscoped
	batch     int      // the parent tool round that spawned it (S-110)
	steps     int      // step count the spawn declared; 0 means none
	root      string   // working directory (worktree subdir for writers)
	worktree  string   // worktree top dir; "" for researchers
	repoTop   string   // parent repo toplevel; "" for researchers
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

	mu        sync.Mutex
	mode      agent.Mode
	state     State
	detail    string
	started   time.Time
	ended     time.Time
	step      int // announcements made, i.e. steps entered (S-090's grammar)
	toolCalls int
	tokensIn  int64
	tokensOut int64
	// priorIn/priorOut carry the spend of earlier attempts across a retry
	// (S-111). The live counters are what the token budget is measured
	// against, so each attempt gets the budget it was spawned with; the
	// status adds the carried spend back, because money already spent does
	// not stop being spent when the child runs again.
	priorIn   int64
	priorOut  int64
	attempt   int
	budgetHit bool
	checkIns  int
	report    string
	patchNote string
	// Live session surface (S-077): transcript entries, the in-flight
	// assistant text, queued steering messages, and the current turn's
	// interrupt channel.
	transcript []TranscriptEntry
	streaming  string
	steering   []string
	intCh      chan struct{}
	intClosed  bool
}

func (c *child) set(state State, detail string) {
	c.mu.Lock()
	c.state = state
	c.detail = detail
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
	return Status{
		Name:      c.name,
		Role:      c.role,
		Task:      c.task,
		Model:     c.model,
		Paths:     c.paths,
		State:     c.state,
		Detail:    c.detail,
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
	}
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
// first (the round's assistant text precedes its calls), and returns its
// index for settleToolEntry.
func (c *child) beginToolEntry(tool, args string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.streaming != "" {
		c.transcript = append(c.transcript, TranscriptEntry{Kind: EntryAssistant, Text: c.streaming})
		c.streaming = ""
		c.step++
	}
	c.transcript = append(c.transcript, TranscriptEntry{Kind: EntryTool, Tool: tool, Args: args, Pending: true})
	return len(c.transcript) - 1
}

// settleToolEntry records a pending tool entry's result in place.
func (c *child) settleToolEntry(idx int, result string) {
	c.mu.Lock()
	if idx >= 0 && idx < len(c.transcript) {
		c.transcript[idx].Result = result
		c.transcript[idx].Pending = false
	}
	c.mu.Unlock()
}

// drainSteering pops all queued steering messages, appending each to the
// transcript as a user entry (they join the conversation now).
func (c *child) drainSteering() []string {
	c.mu.Lock()
	msgs := c.steering
	c.steering = nil
	for _, msg := range msgs {
		c.transcript = append(c.transcript, TranscriptEntry{Kind: EntryUser, Text: msg})
	}
	c.mu.Unlock()
	return msgs
}

// beginTurn arms a fresh interrupt channel for the next h.Run.
func (c *child) beginTurn() {
	c.mu.Lock()
	c.intCh = make(chan struct{})
	c.intClosed = false
	c.mu.Unlock()
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
// under the lock rather than off the struct (S-111).
func (c *child) stop() {
	c.mu.Lock()
	cancel := c.cancel
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
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
func (c *child) addUsage(u *provider.Usage) (over bool) {
	if u == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tokensIn += int64(u.PromptTokens)
	c.tokensOut += int64(u.CompletionTokens)
	if c.maxTokens > 0 && c.tokensIn+c.tokensOut > c.maxTokens {
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
	// batch numbers the parent tool rounds that spawned children (S-110).
	// The supervisor does not know where a round begins — the parent front-end
	// does, and says so with BeginBatch — so children spawned by a host that
	// never opens one all share batch zero.
	batch int

	wg        sync.WaitGroup
	closeOnce sync.Once
}

// New builds a Supervisor. The parent-mode ceiling starts at manual (the
// safest) until SetParentMode reports the session's real mode.
func New(ctx context.Context, opts Options) *Supervisor {
	if opts.MaxConcurrent <= 0 {
		opts.MaxConcurrent = DefaultMaxConcurrent
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

// BeginBatch opens a new spawn batch and returns its number. The parent
// front-end calls it once per tool round, so the children one round spawns
// share a batch and can be rendered as one fan-out block (S-110) rather than
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
	return agent.ModePolicy{
		Mode:             s.childMode(c),
		AllowEdits:       g.AllEdits,
		AllowCommands:    g.AllCommands,
		EditDirs:         g.EditDirs,
		CommandAllowlist: allowlist,
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
// orchestrator), for breadcrumbs and esc-pops (S-077).
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

// Steer queues a message for a child (S-077, S-058 semantics): injected
// before its next stream request when running, or starting a fresh turn when
// the child is idle after a cancelled turn. Finished children cannot be
// steered.
func (s *Supervisor) Steer(name, text string) error {
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
	c.steering = append(c.steering, text)
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

// CancelTurn interrupts a child's current turn (S-077): the in-flight stream
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
	c.stop()
	return nil
}

// Retry runs a failed child again on its original task (S-111). Only a
// failed child can be retried: a finished one has nothing to redo, and a live
// one is already doing it.
//
// The attempt is what restarts, not the agent. The child keeps its name, its
// place in the batch and its transcript — the failed attempt stays there,
// with the reason it failed and the retry appended after it, so the list and
// the attached view both keep their history. Everything the attempt owns is
// new: a fresh conversation, a fresh workspace for a writer, and a fresh
// token budget, because an attempt that inherits the spend that killed it
// fails again before it has done anything.
func (s *Supervisor) Retry(name string) error {
	c, err := s.lookup(name)
	if err != nil {
		return err
	}
	if s.ctx.Err() != nil {
		return errors.New("the agent supervisor is shut down")
	}
	c.mu.Lock()
	state, detail := c.state, c.detail
	c.mu.Unlock()
	if state != StateFailed {
		return fmt.Errorf("agent %s is %s; only a failed agent can be retried", name, state)
	}
	// The previous run's goroutine owns the worktree cleanup and the done
	// channel, so a retry waits for it to be gone rather than racing it.
	select {
	case <-c.done:
	default:
		return fmt.Errorf("agent %s is still shutting down; try again in a moment", name)
	}

	root := s.opts.Root
	worktree, repoTop := "", ""
	if c.role == RoleWriter {
		worktree, root, repoTop, err = addWorktree(s.opts.Root)
		if err != nil {
			return fmt.Errorf("cannot create an isolated worktree for the retry: %w", err)
		}
	}
	cctx, cancel := context.WithCancel(s.ctx)
	env, err := s.opts.NewEnv(cctx, Spec{Role: c.role, Root: root, Model: c.model, Paths: c.paths})
	if err != nil {
		cancel()
		if worktree != "" {
			removeWorktree(repoTop, worktree)
		}
		return fmt.Errorf("cannot set up the retry: %w", err)
	}
	a := agent.New([]provider.Message{{Role: provider.RoleSystem, Content: env.SystemPrompt}}, env.Stream)
	a.SetMaxRounds(c.agent.MaxRounds())
	a.SetExecutor(env.Executor)

	c.appendEntry(TranscriptEntry{Kind: EntrySystem, Text: "Retrying — the previous attempt " + detail + "."})

	c.mu.Lock()
	c.ctx, c.cancel = cctx, cancel
	c.agent, c.env, c.headless = a, env, nil
	c.root, c.worktree, c.repoTop = root, worktree, repoTop
	c.done = make(chan struct{})
	c.state, c.detail = StateQueued, "queued · retry"
	c.started, c.ended = time.Now(), time.Time{}
	c.attempt++
	// Each attempt is measured against the budget it was spawned with; what
	// the earlier attempts spent is carried, not forgotten.
	c.priorIn, c.priorOut = c.priorIn+c.tokensIn, c.priorOut+c.tokensOut
	c.tokensIn, c.tokensOut, c.budgetHit = 0, 0, false
	c.checkIns = 0
	c.toolCalls, c.step = 0, 0
	c.report, c.patchNote, c.streaming = "", "", ""
	c.mu.Unlock()
	if s.opts.Record != nil {
		c.rec = s.opts.Record(c.role)
	}

	s.wg.Add(1)
	go s.run(c)
	s.emitUpdate(c)
	return nil
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
		}
		return next(name, args)
	}
}

// spawn validates the arguments, prepares the child's workspace (a git
// worktree for writers), and starts it in the background.
func (s *Supervisor) spawn(raw json.RawMessage) (string, error) {
	args, err := parseSpawnArgs(raw)
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

	// Writers work in isolated worktrees, so they cannot overwrite each
	// other's files — but two patches over the same file conflict when they
	// land. A declared scope is refused up front rather than discovered at
	// apply time.
	if args.role == RoleWriter {
		if holder, claim, clash := s.claimConflict(args.paths); clash {
			return "", fmt.Errorf("%s already claims %s, which overlaps this agent's paths; wait for it with agent_report, or narrow the paths so the two do not share files", holder, claim)
		}
	}

	model := args.Model
	if s.opts.ModelFor != nil {
		model = s.opts.ModelFor(args.role, args.Model)
	}

	root := s.opts.Root
	worktree, repoTop := "", ""
	if args.role == RoleWriter {
		worktree, root, repoTop, err = addWorktree(s.opts.Root)
		if err != nil {
			return "", fmt.Errorf("cannot create an isolated worktree for a writer agent: %w", err)
		}
	}

	cctx, cancel := context.WithCancel(s.ctx)
	env, err := s.opts.NewEnv(cctx, Spec{Role: args.role, Root: root, Model: model, Paths: args.paths})
	if err != nil {
		cancel()
		if worktree != "" {
			removeWorktree(repoTop, worktree)
		}
		return "", fmt.Errorf("cannot set up the agent: %w", err)
	}

	a := agent.New([]provider.Message{{Role: provider.RoleSystem, Content: env.SystemPrompt}}, env.Stream)
	a.SetMaxRounds(args.maxRounds)

	c := &child{
		name:      name,
		role:      args.role,
		task:      args.Task,
		model:     model,
		paths:     args.paths,
		batch:     batch,
		steps:     args.steps,
		root:      root,
		worktree:  worktree,
		repoTop:   repoTop,
		mode:      mode,
		maxTokens: args.maxTokens,
		ctx:       cctx,
		cancel:    cancel,
		agent:     a,
		env:       env,
		done:      make(chan struct{}),
		steerWake: make(chan struct{}, 1),
		state:     StateQueued,
		detail:    "queued",
		started:   time.Now(),
	}
	// The auto-run executor is the env's rooted, reduced chain.
	a.SetExecutor(env.Executor)
	if s.opts.Record != nil {
		c.rec = s.opts.Record(args.role)
	}

	s.mu.Lock()
	s.children = append(s.children, c)
	s.byName[name] = c
	s.mu.Unlock()

	s.wg.Add(1)
	go s.run(c)
	s.emitUpdate(c)

	note := ""
	if args.role == RoleWriter {
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
		if st.Role != RoleWriter || len(st.Paths) == 0 {
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
	defer close(c.done)
	if c.rec.End != nil {
		defer c.rec.End()
	}
	// The attempt's workspace is captured here rather than read in the defer:
	// a retry gives the child a new one, and this goroutine cleans up its own.
	worktree, repoTop := c.workspace()
	defer func() {
		if worktree != "" {
			removeWorktree(repoTop, worktree)
		}
	}()

	// Bounded concurrency: take a slot or notice cancellation while queued.
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	case <-c.ctx.Done():
		c.set(StateFailed, "cancelled")
		s.emit(Event{Kind: EventDone, Status: c.status()})
		return
	}

	c.set(StateRunning, "running")
	s.emitUpdate(c)

	// pendingEntry tracks the transcript index of the tool call in flight, so
	// its result lands on the same row (calls within a child are sequential).
	pendingEntry := -1
	h := &agent.Headless{
		Agent: c.agent,
		Gate:  func(tc provider.ToolCall) bool { return c.env.Gated[tc.Name] },
		Resolve: func(tc provider.ToolCall) string {
			return s.resolveGated(c, tc)
		},
		Steer: func() []string {
			msgs := c.drainSteering()
			if len(msgs) > 0 {
				s.emitUpdate(c)
			}
			return msgs
		},
		OnUsage: func(u *provider.Usage) {
			if c.addUsage(u) {
				c.cancel()
			}
			if c.rec.Usage != nil {
				st := c.status()
				c.rec.Usage(1, st.TokensIn, st.TokensOut)
			}
		},
		OnText: func(text string) {
			c.mu.Lock()
			c.streaming += text
			c.mu.Unlock()
			s.emitUpdate(c)
		},
		OnToolCall: func(tc provider.ToolCall) {
			pendingEntry = c.beginToolEntry(tc.Name, tc.Arguments)
			c.mu.Lock()
			c.toolCalls++
			n := c.toolCalls
			c.mu.Unlock()
			c.set(StateRunning, "running · "+plural(n, "tool"))
			s.emitUpdate(c)
		},
		OnToolResult: func(tc provider.ToolCall, result string) {
			c.settleToolEntry(pendingEntry, result)
			if c.rec.ToolCall != nil {
				outcome := "ok"
				if strings.HasPrefix(result, "error:") {
					outcome = "error"
				}
				c.rec.ToolCall(tc.Name, outcome)
			}
			s.emitUpdate(c)
		},
	}
	c.mu.Lock()
	c.headless = h
	c.mu.Unlock()

	// The turn loop (S-077): a cancelled turn parks the child idle until a
	// steering message starts the next one; kill (context cancellation) ends
	// the loop from any point.
	turn := c.task
	c.appendEntry(TranscriptEntry{Kind: EntryUser, Text: turn})
	for {
		c.beginTurn()
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
			// stopped for (S-144), with its own final report where the
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
			reason := "cancelled"
			if budgetHit {
				reason = budgetReason(c)
			}
			c.set(StateFailed, reason)
			s.emit(Event{Kind: EventDone, Status: c.status()})
			return
		}

		if err == nil {
			c.mu.Lock()
			c.report = report
			tools := c.toolCalls
			c.mu.Unlock()

			// Steering that arrived during the final stream becomes the next
			// turn instead of being dropped (the TUI's dispatchSteering
			// semantics, S-058).
			if msgs := c.drainSteering(); len(msgs) > 0 {
				turn = strings.Join(msgs, "\n\n")
				c.set(StateRunning, "running")
				s.emitUpdate(c)
				continue
			}

			if c.role == RoleWriter {
				s.reviewPatch(c)
				c.mu.Lock()
				note := c.patchNote
				c.mu.Unlock()
				if note != "" {
					c.appendEntry(TranscriptEntry{Kind: EntrySystem, Text: note})
				}
			}

			c.set(StateDone, "done · "+plural(tools, "tool"))
			s.emit(Event{Kind: EventDone, Status: c.status()})
			return
		}

		if errors.Is(err, agent.ErrInterrupted) && c.ctx.Err() == nil {
			// Turn cancelled by the user: the conversation is already
			// well-formed (synthetic results); wait for steering or a kill.
			c.appendEntry(TranscriptEntry{Kind: EntrySystem, Text: "Turn cancelled — send a message to continue."})
			c.set(StateIdle, "idle · turn cancelled")
			s.emitUpdate(c)
			next, ok := s.awaitSteering(c)
			if !ok {
				c.set(StateFailed, "cancelled")
				s.emit(Event{Kind: EventDone, Status: c.status()})
				return
			}
			turn = next
			c.set(StateRunning, "running")
			s.emitUpdate(c)
			continue
		}

		if errors.Is(err, agent.ErrRoundCap) && c.ctx.Err() == nil {
			// The round limit is a check-in, not a stop (S-144). The cap is
			// tested between rounds, after the last round's results were
			// recorded, so the conversation is already well-formed: the child
			// picks up exactly where it left off, with the check-in as its
			// next turn and a budget that has grown.
			used := c.agent.Rounds()
			c.mu.Lock()
			c.checkIns++
			n := c.checkIns
			c.mu.Unlock()
			c.agent.SetMaxRounds(c.agent.MaxRounds() * checkInGrowth)
			c.appendEntry(TranscriptEntry{Kind: EntrySystem,
				Text: fmt.Sprintf("Check-in %d — %d rounds used. Taking stock, then carrying on.", n, used)})
			turn = checkInPrompt(used)
			c.set(StateRunning, fmt.Sprintf("running · check-in %d", n))
			s.emitUpdate(c)
			continue
		}

		// Keep the child's conversation well-formed for inspection.
		c.agent.CancelTurn()
		s.finalCheckIn(c)
		c.set(StateFailed, s.failReason(c, err))
		s.emit(Event{Kind: EventDone, Status: c.status()})
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

// checkInPrompt is the turn a child is given when it reaches its round limit
// (S-144). It asks about the work rather than announcing the budget on
// purpose: a child told it has run out of rounds tends to apologise and stop,
// where one asked what is left states it and keeps going. The budget is
// mentioned only as the reason for the interruption, and the last line is
// there because a check-in is also the moment a child that is quietly
// finished should say so instead of inventing more to do.
func checkInPrompt(used int) string {
	return fmt.Sprintf(`You have used %d tool rounds. This is a routine check-in, not a stop — nothing has gone wrong and nothing is running out.

Briefly take stock:
- what you have established or changed so far
- what is still left to do
- what you are doing next

Then carry on with the task. If the work is in fact finished, give your final report instead.`, used)
}

// finalCheckInTimeout bounds the handoff completion. It is short on purpose:
// the child is over its budget and on its way out either way, and a handoff
// that takes longer than this is worth less than the delay it adds before the
// parent hears that the child failed.
const finalCheckInTimeout = 30 * time.Second

// finalCheckInPrompt asks a child that ran out of budget to hand over. It does
// not ask for more work, and says so: the child has nothing left to spend, and
// a handoff that starts another edit is worse than none.
const finalCheckInPrompt = `You have reached your token budget and are stopping now. Do not start any new work or call any tools.

Write a short handoff for whoever picks this up: what you established or changed, what is left, and what you would do next.`

// finalCheckIn asks a child that exhausted its token budget to say where it
// got to, and records the answer as its report (S-144). The budget stays a
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
	c.mu.Unlock()
	if !hit {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), finalCheckInTimeout)
	defer cancel()
	env, err := s.opts.NewEnv(ctx, Spec{Role: c.role, Root: root, Model: model, Paths: paths})
	if err != nil {
		return
	}
	msgs := append(c.agent.RequestMessages(),
		provider.Message{Role: provider.RoleUser, Content: finalCheckInPrompt})
	events, stop, err := env.Stream(msgs)
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
// wording every path that stops for the budget uses.
func budgetReason(c *child) string {
	return fmt.Sprintf("failed · token budget (~%s) exceeded", formatTokens(c.maxTokens))
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
// decides, and anything it would ask about routes to the parent user.
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
	// The static policy denies in plan mode, which refuses the call with the
	// result that tells the model why nothing ran, and for a path no grant
	// can reach (S-141), which says which path and why.
	if decision == agent.Deny {
		if strings.HasPrefix(reason, "outside the working scope") {
			c.appendEntry(TranscriptEntry{Kind: EntrySystem, Text: "Refused: " + title + " — " + reason})
			return agent.ScopeRefusedResult(reason)
		}
		return agent.PlanModeResult
	}
	if decision == agent.Ask {
		var denial string
		decision, reason, denial = s.classify(c, policy.Mode, tc, action)
		if decision == agent.Deny {
			c.appendEntry(TranscriptEntry{Kind: EntrySystem, Text: "Refused (" + reason + "): " + title + " — " + denial})
			return "error: auto mode denied this tool call: " + denial
		}
	}
	switch decision {
	case agent.Allow:
		c.appendEntry(TranscriptEntry{Kind: EntrySystem, Text: "Auto-approved (" + reason + "): " + title})
	case agent.Ask:
		ask, askErr := s.buildAsk(c, tc.Name, rooted, action)
		if askErr != nil {
			return "error: " + askErr.Error()
		}
		approved, ok := s.await(c, ask)
		if !ok {
			return agent.CancelledResult
		}
		if !approved {
			if action.Kind == agent.ActionCommand {
				return "error: the user declined to run this command"
			}
			return "error: the user declined this tool call"
		}
	}

	if tc.Name == tools.ExecCommandName {
		if c.env.RunCommand == nil {
			return "error: command execution is not available to this agent"
		}
		out, code := c.env.RunCommand(c.ctx, action.Command)
		return tools.FormatExecResult(out, code)
	}
	return agent.ExecuteWith(c.env.ExecuteGated, provider.ToolCall{ID: tc.ID, Name: tc.Name, Arguments: string(rooted)})
}

// classify runs the auto-mode permission classifier (S-060) for a call the
// static policy would have asked about, giving children the same treatment
// the parent gets. Anything other than auto mode, a missing classifier, or a
// safety-flagged action leaves the decision at Ask — the classifier can only
// ever remove a prompt it is allowed to remove, never add permission.
// It returns the decision, the short label for the child's transcript, and —
// for a denial — the reason the model is told.
func (s *Supervisor) classify(c *child, mode agent.Mode, tc provider.ToolCall, action agent.Action) (decision agent.Decision, label, denial string) {
	if mode != agent.ModeAuto || s.opts.Classifier == nil || action.SafetyFlagged {
		return agent.Ask, "", ""
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
		st := c.status()
		c.rec.Usage(0, st.TokensIn, st.TokensOut)
	}
	verdict, reason := agent.ResolveAuto(action, v)
	elapsed := fmt.Sprintf("classifier, %.1fs", v.Elapsed.Seconds())
	switch {
	case verdict == agent.Allow:
		return agent.Allow, elapsed, ""
	case verdict == agent.Deny:
		return agent.Deny, elapsed, reason
	case v.Failed:
		// Fails closed: the user decides, and sees why they were asked.
		c.appendEntry(TranscriptEntry{Kind: EntrySystem, Text: "Classifier unavailable (" + v.Reason + "); asking the user instead."})
	}
	return agent.Ask, "", ""
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
	case tools.IsMutating(name):
		return agent.Action{Kind: agent.ActionEdit}, nil
	}
	return agent.Action{Kind: agent.ActionOther}, nil
}

// scopedAction fills in what a child's command reaches outside the working
// scope (S-141): its own worktree plus whatever the parent session has put in
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

// buildAsk assembles the approval request the parent user reviews.
func (s *Supervisor) buildAsk(c *child, name string, rooted json.RawMessage, action agent.Action) (*Ask, error) {
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
		ask := NewAsk(c.name, AskEdit, mut.Action+" "+displayPath(c.root, mut.Path))
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
	ask := NewAsk(c.name, AskPatch, title)
	ask.Hunks = hunks
	// Two writers can hold the same file in separate worktrees; the collision
	// only becomes visible when the second patch lands on top of the first.
	// Say so on the card, before it is applied.
	touched := PatchFiles(patch)
	if clashes := s.patchClashes(c.name, touched); len(clashes) > 0 {
		ask.Warnings = append(ask.Warnings, "overwrites changes already applied by "+strings.Join(clashes, ", "))
	}

	note := ""
	approved, ok := s.await(c, ask)
	switch {
	case !ok:
		note = "cancelled before the patch was reviewed; no files were changed" + savedPatchNote(c.name, patch)
	case !approved:
		note = "the user declined the patch; no files were changed" + savedPatchNote(c.name, patch)
	default:
		// Both sides are read around `git apply`, in the real checkout: the
		// child's own worktree edits never touched these files, so this is
		// the only place the session can see what its workspace lost and
		// gained (S-097).
		before := readSides(c.repoTop, touched)
		if applyErr := applyPatch(c.repoTop, patch); applyErr != nil {
			note = "the patch failed to apply cleanly: " + firstLine(applyErr.Error()) + savedPatchNote(c.name, patch)
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
			note = fmt.Sprintf("patch applied to the workspace (+%d −%d, %d file(s))", adds, dels, files)
		}
	}
	c.mu.Lock()
	c.patchNote = note
	c.mu.Unlock()
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

func (s *Supervisor) statusOverview() string {
	statuses := s.Snapshot()
	if len(statuses) == 0 {
		return "No agents have been spawned this session."
	}
	var sb strings.Builder
	for _, st := range statuses {
		label := string(st.Role)
		if st.Model != "" {
			label += ", " + st.Model
		}
		if len(st.Paths) > 0 {
			label += "; " + strings.Join(st.Paths, ", ")
		}
		fmt.Fprintf(&sb, "%s (%s): %s — %s\n", st.Name, label, st.Detail, firstLine(st.Task))
	}
	return strings.TrimRight(sb.String(), "\n")
}

// reportText is what the parent model receives about a child: its status
// line, its final report, and (for writers) what happened to its patch.
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
	head := fmt.Sprintf("%s (%s) — %s", st.Name, st.Role, st.Detail)
	switch st.State {
	case StateRunning, StateDone:
	default:
		head += " · " + plural(st.ToolCalls, "tool call")
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
