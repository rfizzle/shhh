package subagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/provider"
)

// Supervisor owns a session's children: spawning, bounded concurrency,
// approval routing, cancellation, and worktree cleanup.
type Supervisor struct {
	opts   Options
	ctx    context.Context
	cancel context.CancelFunc
	events chan Event

	// sems is one set of concurrency slots per depth, made on first use and
	// keyed by the depth that draws from it. Slots are held per depth so
	// that a descendant queues behind other agents at its own level and
	// never behind its own ancestor — which is half of what keeps a tree of
	// agents from waiting on itself forever
	// (docs/capabilities/subagents.md#a-wait-only-ever-points-down-the-tree).
	semsMu sync.Mutex
	sems   map[int]chan struct{}
	// checks is the session's throttle on checks, shared by every child and
	// by the session's own gate (CheckSlot).
	checks *CheckSlots

	mu       sync.Mutex
	children []*child
	byName   map[string]*child
	counters map[Role]int
	// spawned counts the spawns that were given a name, and is each child's
	// place in spawn order (child.seq).
	spawned int
	// parentMode is the ceiling children are clamped to; parentGrants are the
	// parent's session grants ([a] on a prompt, /mode allow), which children
	// inherit for the same reason they inherit the mode.
	parentMode   agent.Mode
	parentGrants agent.Grants
	// conversationPolicy is set when the parent is a conversation: its
	// fetches are reads, and so are its children's (SetConversationPolicy).
	conversationPolicy bool
	// attended is set when a person answers the requests children route up
	// (SetAttended): a child's classifier no is then put to them as a card,
	// and refused where it is not.
	attended bool
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
	// claimsFreed is closed and replaced whenever a child ends, which is
	// when a claim can be released: a writer queued behind a claim
	// (awaitClaim) wakes on it and asks again. Under mu.
	claimsFreed chan struct{}
	// claimMu is held by a writer's spawn from the moment it takes its place
	// in spawn order until it is in the list the claim check reads. A
	// round's calls run at once, so without it two writers handed over in
	// one round would each check their claims before the other was there to
	// be seen, and both would start over the same files.
	claimMu sync.Mutex

	wg        sync.WaitGroup
	closeOnce sync.Once

	// sendMu and closed guard the event channel against its own close. A
	// send on a closed channel panics even inside a select with a default, so
	// every sender reads closed under the read lock and sends while holding
	// it, and Close takes the write lock to close the channel. Close cancels
	// ctx first, which is what lets a must-see send blocked under the read
	// lock give up and the write lock be had.
	sendMu sync.RWMutex
	closed bool

	// sessionQueued is how many steering messages the session says wait to
	// join its own conversation, and sessionSteered is the session's
	// counterpart of child.steered: closed and replaced when that count
	// grows. Both under mu. The session's queue is its front-end's, not the
	// supervisor's, so SessionSteering is how it is told.
	sessionQueued  int
	sessionSteered chan struct{}

	// conversation reads the session's own messages for a child of the
	// session that inherits turns (SetConversation). Under mu.
	conversation func() []provider.Message
}

// ErrClosed is what a supervisor answers once Close has run: a steer, a note,
// a retry or a mode change asked of a session that is ending is refused in
// words rather than sent into a stream nobody is reading.
var ErrClosed = errors.New("the agent supervisor is shut down")

// New builds a Supervisor. The parent-mode ceiling starts at manual (the
// safest) until SetParentMode reports the session's real mode.
func New(ctx context.Context, opts Options) *Supervisor {
	if opts.MaxConcurrent <= 0 {
		opts.MaxConcurrent = DefaultMaxConcurrent
	}
	if opts.MaxDepth <= 0 {
		opts.MaxDepth = DefaultMaxDepth
	}
	if opts.MaxChildren <= 0 {
		opts.MaxChildren = DefaultMaxChildren
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
		sems:         map[int]chan struct{}{},
		byName:       map[string]*child{},
		counters:     map[Role]int{},
		parentMode:   agent.ModeManual,
		appliedFiles: map[string]string{},
		claimsFreed:  make(chan struct{}),
		checks:       NewCheckSlots(opts.CheckSlots),
	}
}

// CheckSlot takes one of the session's check slots for a check that is not a
// child's — the session's own gate — so the person's run and a child's never
// load the machine at once. It answers the release, and false where ctx or
// the supervisor ended first.
func (s *Supervisor) CheckSlot(ctx context.Context) (func(), bool) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(s.ctx, cancel)
	defer stop()
	return s.checks.Take(ctx, nil)
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

// SetConversationPolicy says the parent is a conversation, whose policy
// answers a fetch without a card. A child is part of the conversation that spawned it,
// so its fetches are answered the same way — a card routed up from a child
// would be the question the parent no longer asks, asked one level down.
// See docs/capabilities/chat.md#a-conversation-has-one-mode.
func (s *Supervisor) SetConversationPolicy() {
	s.mu.Lock()
	s.conversationPolicy = true
	s.mu.Unlock()
}

// SetAttended says a person answers the requests this supervisor's children
// route up — the interactive session, and no other surface. A child's
// classifier no is then routed to that person's card with the classifier's
// sentence on it, the way the session's own is put to them; without it the
// no is refused, since the answerer on the other end of the route is a rule
// that would decline it in somebody else's name.
// See docs/capabilities/subagents.md#a-child-answers-to-the-session.
func (s *Supervisor) SetAttended() {
	s.mu.Lock()
	s.attended = true
	s.mu.Unlock()
}

// SetParentGrants records the parent's grants ([a] on a confirm prompt,
// /mode allow): what the user waved through is waved through for children
// too, so one grant is not re-asked once per agent. The scoped grants travel
// with the blanket ones — a child editing under a directory the parent
// granted is doing the thing that was granted.
//
// A grant that ends with the parent's turn arrives here as one of these and
// leaves the same way: the parent pushes its grants again at the turn's
// close, so a child spawned under a turn grant loses it when the turn that
// made it ends, exactly as the session does.
// See docs/capabilities/approvals-and-safety.md#a-grant-says-when-it-ends.
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
	conversation := s.conversationPolicy
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
	// The role's own refusals join the person's and never replace them: a
	// profile may only take commands away from its child, and a copy rather
	// than an append so one child's list never lands in the shared slice.
	// See docs/capabilities/subagents.md#a-profile-is-a-file.
	denylist := s.opts.CommandDenylist
	if len(c.profile.Deny) > 0 {
		denylist = append(append([]string(nil), denylist...), c.profile.Deny...)
	}
	return agent.ModePolicy{
		Mode:             s.childMode(c),
		AllowEdits:       g.AllEdits,
		AllowCommands:    g.AllCommands,
		EditDirs:         g.EditDirs,
		EditPaths:        g.EditPaths,
		ExactCommands:    g.ExactCommands,
		CommandAllowlist: allowlist,
		CommandDenylist:  denylist,
		AllowHosts:       hosts,
		DenyHosts:        s.opts.DenyHosts,
		ReadOnlyExtra:    s.opts.ReadOnlyExtra,
		ReadOnlyDisabled: s.opts.ReadOnlyDisabled,
		Conversation:     conversation,
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

// Snapshot returns every child's live status in spawn order: the order the
// names were given (child.seq), not the order the spawns finished setting up,
// and a kill or a retry leaves a child where it was. The session map the
// agent chords walk is this order, so two presses land where they landed
// before (docs/interface/surfaces.md#the-inspector-rail).
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
	if s.isClosed() {
		return agent.ModeManual, ErrClosed
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
			// A writer's copy is made when it starts and torn down once it
			// can no longer be spoken to, so there is one to diff only
			// while it runs or waits for a follow-up.
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
		s.sendMu.Lock()
		s.closed = true
		close(s.events)
		s.sendMu.Unlock()
	})
}

// isClosed reports whether Close has closed the event stream, so a caller
// can refuse before it changes a child it could no longer report on.
func (s *Supervisor) isClosed() bool {
	s.sendMu.RLock()
	defer s.sendMu.RUnlock()
	return s.closed
}

// WrapExecutor intercepts the orchestration tools on one agent's executor
// chain; every other call passes through. caller is the agent doing the
// calling — "" for the session itself, and a child's own name where the
// chain being wrapped is a child's, the way the web toolset and the notebook
// take the name of whoever is calling them.
//
// The caller is what makes delegation a tree rather than a flat roster: it
// is written as the spawned child's parent, and it bounds what the other
// three tools can reach to what this agent spawned. An agent that could
// report on, steer or retry its siblings could also come to wait on one that
// is waiting on it, and neither of them would ever finish
// (docs/capabilities/subagents.md#a-wait-only-ever-points-down-the-tree).
func (s *Supervisor) WrapExecutor(caller string, next agent.ToolExecutor) agent.ToolExecutor {
	return func(name string, args json.RawMessage) (string, error) {
		switch name {
		case SpawnToolName:
			return s.spawnFrom(caller, args)
		case ReportToolName:
			return s.report(caller, args)
		case SteerToolName:
			return s.steer(caller, args)
		case RetryToolName:
			return s.retry(caller, args)
		}
		return next(name, args)
	}
}

// descends reports whether name is strictly below caller in the spawn tree,
// which is what the orchestration tools other than the spawn are bounded to.
// Strictly, so an agent cannot report on, steer or retry itself. The session
// — caller "" — is above everything and reaches all of it.
//
// The walk is bounded by the number of children there are, for the reason
// every walk of these links is: a cycle in them would hang the tool round
// rather than refuse one call.
func (s *Supervisor) descends(caller, name string) bool {
	if caller == "" {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.descendsLocked(caller, name)
}

// descendsLocked is descends with s.mu already held and without the session's
// blanket reach, for a caller walking the roster under the lock it took to
// read it.
func (s *Supervisor) descendsLocked(caller, name string) bool {
	for at, hops := name, 0; at != "" && hops <= len(s.children); hops++ {
		c, ok := s.byName[at]
		if !ok {
			return false
		}
		if c.parent == caller {
			return true
		}
		at = c.parent
	}
	return false
}

// reachable resolves a name one of the orchestration tools was given, or
// says that this caller has no such agent. A sibling is reported as unknown
// rather than as refused: what an agent may act on is what it spawned, and
// naming the rest of the roster in a refusal would describe a session the
// child is not part of.
func (s *Supervisor) reachable(caller, name string) error {
	if s.descends(caller, name) {
		return nil
	}
	if caller == "" {
		return fmt.Errorf("no agent named %q", name)
	}
	return fmt.Errorf("no agent named %q among the ones you spawned; agent_report with no arguments lists them", name)
}
