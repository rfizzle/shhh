package subagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/meter"
	"github.com/rfizzle/shhh/internal/provider"
	wtree "github.com/rfizzle/shhh/internal/subagent/worktree"
)

// Spawn starts a child from the spawn tool's own arguments, for a caller
// that is not the model — the backlog runner's review stage. It is the
// same path the tool takes, limits and all; nothing about being called
// from code exempts a child from the attention budget.
func (s *Supervisor) Spawn(raw json.RawMessage) (string, error) { return s.spawnFrom("", raw) }

// CheckModel puts the model a spawn_agent call named to the session's check,
// for the surfaces that must refuse it before the call reaches a card: the
// session's approval queue, and an unattended run's verdict. A call that
// names no model, or whose arguments do not parse, passes — the second is
// refused by the spawn's own parse, which says what is wrong with it.
// See docs/capabilities/subagents.md#the-model-is-offered-the-models-it-can-name.
func (s *Supervisor) CheckModel(raw json.RawMessage) (note string, err error) {
	var args struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(raw, &args) != nil {
		return "", nil
	}
	return s.checkModel(args.Model)
}

// checkModel is CheckModel over a name already read off the call.
func (s *Supervisor) checkModel(model string) (string, error) {
	model = strings.TrimSpace(model)
	if model == "" || s.opts.CheckModel == nil {
		return "", nil
	}
	return s.opts.CheckModel(model)
}

// MaxDepth is the deepest an agent may sit, counting the session as
// SessionDepth. A surface builds a child's toolset from it: an agent with no
// level left below it is handed no delegation tools.
func (s *Supervisor) MaxDepth() int { return s.opts.MaxDepth }

// Spawned is how many children the session has started and how many it may
// start in all. A finished child keeps its place in the first number, which
// is what a surface drawing the pair says by the count never going down.
// See docs/capabilities/subagents.md#limits-are-about-attention-not-resources.
//
// A nil supervisor answers zero for both, which a surface reads as a session
// with no cap to state.
func (s *Supervisor) Spawned() (started, limit int) {
	if s == nil {
		return 0, 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.children), s.opts.MaxChildren
}

// depthOf is how far under the session an agent sits, for the agent that is
// about to spawn: the session itself is SessionDepth and a child is its
// parent's depth plus one.
func (s *Supervisor) depthOf(name string) int {
	if name == "" {
		return SessionDepth
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.byName[name]; ok {
		return c.depth
	}
	return SessionDepth
}

// slots is the set of concurrency slots one depth draws from, made on first
// use. Each depth has its own so that a descendant never queues behind its
// own ancestor.
// See docs/capabilities/subagents.md#a-wait-only-ever-points-down-the-tree.
func (s *Supervisor) slots(depth int) chan struct{} {
	s.semsMu.Lock()
	defer s.semsMu.Unlock()
	sem, ok := s.sems[depth]
	if !ok {
		sem = make(chan struct{}, s.opts.MaxConcurrent)
		s.sems[depth] = sem
	}
	return sem
}

// workspace is one attempt's place to work and everything built against it:
// the isolated checkout a writer gets, the working root inside it, the
// environment rooted there, the agent driving it and the attempt's record.
// The five travel together because all five are decided by a directory that,
// for a writer, does not exist until the child starts.
type workspace struct {
	root  string
	wt    wtree.WorktreeHandle
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
		if w.wt, err = wtree.AddWorktreeContext(ctx, s.opts.Root, s.parentUntracked()); err != nil {
			return workspace{}, fmt.Errorf("cannot create an isolated worktree for a writer agent: %w", err)
		}
		w.root = w.wt.Root
		// An integration writer's copy starts holding everything of the
		// patch it reconciles that merges cleanly, taken against the tree
		// as it stands now rather than as it stood at the conflict.
		if c.integrates != nil {
			if err = c.integrates.seed(w.wt); err != nil {
				wtree.RemoveWorktree(w.wt.RepoTop, w.wt.Dir)
				return workspace{}, err
			}
		}
	}
	w.env, err = s.opts.NewEnv(ctx, Spec{Name: c.name, Role: c.role, Root: w.root, Model: c.model, Paths: c.paths,
		Parent: c.parent, Depth: c.depth,
		Worktree: w.wt.Dir != "", MaxTokens: c.maxTokens, AdmissionFloor: c.admissionFloor, Attempt: attempt,
		Inherit: c.inheritTurns, Integrates: c.integrates.sourceName()})
	if err != nil {
		wtree.RemoveWorktree(w.wt.RepoTop, w.wt.Dir)
		return workspace{}, fmt.Errorf("the agent's environment could not be built: %w", err)
	}
	// Its checks take the session's slots, whatever the surface built.
	w.env = s.throttled(ctx, c, w.env)
	turnEnv := w.env
	turnEnv.Stream = c.interruptible(w.env.Stream)
	w.agent = newChildAgent(turnEnv, maxRounds)
	// The auto-run executor is the env's rooted, reduced chain, inside
	// whatever the surface puts on its own dispatchers.
	w.agent.SetExecutor(c.stepsExecutor(w.env.autoExecutor(c.seam())))
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
	// And the plan the child names itself, which every check-in names the
	// step of — installed here for the reason the budget is.
	w.agent.SetCheckInSteps(c.ownProgress)
	// The mode recorded is the one in force — the profile's or the parent's
	// after the clamp — not the one asked for; c.mode alone is the request.
	if s.opts.Record != nil {
		w.rec = s.opts.Record(Spec{Name: c.name, Role: c.role, Root: w.root, Model: c.model, Paths: c.paths,
			Parent: c.parent, Depth: c.depth,
			Worktree: w.wt.Dir != "", Mode: s.childMode(c), MaxRounds: roundCap(w.agent),
			MaxTokens: c.maxTokens, AdmissionFloor: c.admissionFloor, Attempt: attempt}, w.env.SystemPrompt)
	}
	return w, nil
}

// install puts an opened workspace on the child, clearing the headless loop
// the workspace it replaces was driven by. The lock is the caller's, where
// there is anything to lock: a retry installs one in the middle of a page of
// counter resets and no reader should ever see half of that, while a spawn
// installs into a child no other goroutine can reach yet.
func (c *child) install(w workspace) {
	c.root, c.worktree, c.repoTop, c.seeded = w.root, w.wt.Dir, w.wt.RepoTop, w.wt.Seeded
	c.landings, c.reseeds = nil, 0
	c.agent, c.env, c.headless, c.rec = w.agent, w.env, nil, w.rec
}

// admissionFloor is the budget required before a child can do useful work:
// its inherited prompt and everything its first turn carries — the declared
// task and whatever prologue precedes it — plus the working reserve. Tool
// definitions are prompt cost even though they are not conversation messages.
func admissionFloor(env Env, opening string) (inherited, setup, floor int64) {
	inherited = agent.EstimateTokens(env.SystemPrompt) + env.ToolTokens
	setup = agent.EstimateTokens(opening)
	return inherited, setup, inherited + setup + MinChildMaxTokens
}

// admissionRefusal is the sentence a spawn and a retry are both refused with
// when a budget cannot cover the floor admissionFloor measured. It names every
// part the floor is made of, so the number it states can be checked against
// the rule rather than taken on trust.
// See docs/capabilities/subagents.md#limits-are-about-attention-not-resources.
func admissionRefusal(budget, floor int64, inheritTurns int, inheritTokens int64) string {
	return fmt.Sprintf("max_tokens %d cannot admit this task: at least %d is required for the inherited prompt and tool definitions, the declared task%s and the context its first turn opens on (review evidence, a resume or retry prologue), plus the %d-token working reserve",
		budget, floor, inheritedClause(inheritTurns, inheritTokens), MinChildMaxTokens)
}

// spawnFrom validates the arguments, gives the child everything that does not
// depend on where it will work, and starts it in the background. A reader's
// workspace is opened here; a writer's is opened when its slot comes free
// (openWorkspace).
//
// caller is the agent asking — "" for the session — and it decides three
// things before any of the rest runs: how deep the new child would sit, what
// it may be given (never more than its spawner has), and what is written as
// its parent.
func (s *Supervisor) spawnFrom(caller string, raw json.RawMessage) (string, error) {
	return s.spawn(caller, raw, nil)
}

// spawn is spawnFrom with what only the supervisor can hand a child: integ is
// the conflict an integration writer is started to reconcile (integrate.go),
// nil for every spawn a model asked for. It rides this path rather than one
// of its own so an integration writer is admitted, claimed, counted and
// slotted exactly as any other writer is.
//
// It runs in four phases — validate and claim, admit, build and register,
// reply — each a helper over one spawnPlan. The locks stay here so their
// scopes read in one place: a writer holds claimMu from before it takes its
// place in spawn order until after it is in the list the claim check reads,
// and mu is held twice, once to reserve the name and sequence and once to
// register the child, never across anything that takes a child's lock.
func (s *Supervisor) spawn(caller string, raw json.RawMessage, integ *integration) (string, error) {
	p, err := s.validateSpawn(caller, raw, integ)
	if err != nil {
		return "", err
	}

	if p.args.profile.Writes {
		s.claimMu.Lock()
		defer s.claimMu.Unlock()
	}
	s.mu.Lock()
	err = s.reserveSpawn(p)
	s.mu.Unlock()
	if err != nil {
		return "", err
	}
	if err := s.claimSpawn(p); err != nil {
		return "", err
	}

	if err := s.admitSpawn(p); err != nil {
		return "", err
	}

	c, err := s.buildChild(p)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	at, _ := slices.BinarySearchFunc(s.children, c.seq, func(k *child, seq int) int { return k.seq - seq })
	s.children = slices.Insert(s.children, at, c)
	s.byName[p.name] = c
	s.mu.Unlock()

	s.wg.Add(1)
	go s.run(c)
	s.emitUpdate(c)

	// The claim an overlapping writer shares is read now, with the child in
	// the list and claimMu still held, so the reply is built from values.
	sharedHolder, sharedClaim := "", ""
	if p.args.profile.Writes && len(p.args.paths) > 0 && p.args.overlap {
		if holder, claim, ok := s.claimShared(p.args.paths); ok && holder != p.name {
			sharedHolder, sharedClaim = holder, claim
		}
	}
	return spawnReply(p, sharedHolder, sharedClaim), nil
}

// spawnPlan is what the phases of one spawn hand each other. Each field is
// written by one phase and read by the ones after it; the plan is the spawn
// call's own and no other goroutine sees it.
type spawnPlan struct {
	caller string
	integ  *integration
	args   spawnArgs
	// Written by validateSpawn.
	unchecked string
	depth     int
	resume    Handoff
	// Written by reserveSpawn.
	name  string
	seq   int
	mode  agent.Mode
	batch int
	// Written by claimSpawn.
	waitsOn, waitsFor string
	model             string
	turns             []provider.Message
	inheritTurns      int
	// Written by admitSpawn.
	cctx                    context.Context
	cancel                  context.CancelFunc
	evidence                string
	inheritance             string
	prologue                string
	inherited, setup, floor int64
}

// validateSpawn parses the arguments and refuses what can be refused before
// anything is claimed: a closed session, a model the session cannot run, a
// depth past the limit, a role the spawner may not delegate, a handoff that
// cannot be resumed. It runs under no lock of the caller's; depthOf and
// lookup take mu themselves. It reads the supervisor's options and writes
// only the plan it returns.
func (s *Supervisor) validateSpawn(caller string, raw json.RawMessage, integ *integration) (*spawnPlan, error) {
	args, err := parseSpawnArgs(s.Profiles(), raw)
	if err != nil {
		return nil, err
	}
	if s.ctx.Err() != nil {
		return nil, ErrClosed
	}
	// The model is checked first of all, for the reason depth is: a name
	// the session cannot run is a child that fails on its first request,
	// and a refusal the model reads now costs nothing it was refusing to
	// spend. An integration writer is the supervisor's own and names none.
	// See docs/capabilities/subagents.md#the-model-is-offered-the-models-it-can-name.
	unchecked, err := s.checkModel(args.Model)
	if err != nil {
		return nil, err
	}
	// Depth is checked with the role and before everything else that could
	// claim something, for the reason the token admission is: a refusal that
	// has already taken a slot, cut a worktree or opened a record row is a
	// refusal that cost the session what it was refusing to spend.
	depth := s.depthOf(caller) + 1
	if depth > s.opts.MaxDepth {
		return nil, fmt.Errorf("delegation stops at depth %d and this agent would be depth %d; raise %s to let an agent this deep spawn, or report back and let the level above you spawn it",
			s.opts.MaxDepth, depth, MaxDepthKey)
	}
	// A descendant is never given more than the agent that spawned it. The
	// mode clamp in claimSpawn is the same rule for a different grant, and
	// both run before the child exists rather than at its first call, so a
	// role the spawner may not delegate is a refused spawn and not a child
	// that will be refused every tool it reaches for.
	// See docs/capabilities/subagents.md#a-child-may-delegate-to-a-configured-depth.
	if caller != "" && args.profile.Writes {
		if up, err := s.lookup(caller); err == nil && !up.profile.Writes {
			return nil, fmt.Errorf("%s changes nothing, so it cannot delegate %s, which writes; an agent may only delegate what it could do itself", caller, args.role)
		}
	}
	resume := Handoff{}
	if args.resumeHandoff != "" {
		if s.opts.LoadHandoff == nil {
			return nil, errors.New("failure handoffs are unavailable for this session")
		}
		data, loadErr := s.opts.LoadHandoff(args.resumeHandoff)
		if loadErr != nil {
			return nil, fmt.Errorf("cannot resume handoff %q: %w", args.resumeHandoff, loadErr)
		}
		if resume, loadErr = UnmarshalHandoff(data); loadErr != nil {
			return nil, fmt.Errorf("cannot resume handoff %q: %w", args.resumeHandoff, loadErr)
		}
		profile, profileErr := s.Profiles().Parse(string(resume.Role))
		if profileErr != nil {
			return nil, fmt.Errorf("cannot resume handoff %q: %w", args.resumeHandoff, profileErr)
		}
		args.role, args.profile, args.Task, args.paths = resume.Role, profile, resume.Task, append([]string(nil), resume.Paths...)
		if resume.RecommendedBudget > args.maxTokens {
			args.maxTokens = resume.RecommendedBudget
		}
	}
	return &spawnPlan{caller: caller, integ: integ, args: args, unchecked: unchecked, depth: depth, resume: resume}, nil
}

// reserveSpawn takes the child's place in spawn order: it refuses a session
// at its limit or a name already taken, and otherwise names the child and
// numbers it. It runs under mu, which the caller holds, and claimMu for a
// writer. It reads the supervisor's children, names, parent mode and batch,
// writes its role counter and spawn count, and writes the plan's name, seq,
// mode and batch.
func (s *Supervisor) reserveSpawn(p *spawnPlan) error {
	if len(s.children) >= s.opts.MaxChildren {
		// The count is of starts, so a refusal with three children live must
		// not read as a concurrency limit, which is max_concurrent's.
		return fmt.Errorf("agent limit reached: this session has started %d of %d agents; the limit counts agents started, not agents running, so a finished agent still holds its slot — steer or retry the agents it has, or raise %s", len(s.children), s.opts.MaxChildren, MaxChildrenKey)
	}
	name := p.args.Name
	if name == "" {
		s.counters[p.args.role]++
		name = fmt.Sprintf("%s-%d", p.args.role, s.counters[p.args.role])
	}
	if _, exists := s.byName[name]; exists {
		return fmt.Errorf("an agent named %q already exists", name)
	}
	s.spawned++
	p.name, p.seq, p.mode, p.batch = name, s.spawned, s.parentMode, s.batch
	return nil
}

// claimSpawn settles what the child is given once it has its place: its
// batch and mode ceiling, the writer's claim (refused, or queued behind the
// claim ahead of it), its model and the parent turns it inherits. It runs
// under claimMu for a writer, which the caller holds, and never under mu:
// AgentMode takes a child's lock, and the claim walk and conversationOf take
// mu themselves. It reads the claims of the live writers and writes the
// plan's batch, mode, waitsOn, waitsFor, model, turns and inheritTurns.
func (s *Supervisor) claimSpawn(p *spawnPlan) error {
	args := p.args
	// An integration writer joins the round its writer was spawned in, so
	// its lane is drawn in the fan-out it is reconciling.
	if p.integ != nil {
		p.batch = p.integ.batch
	}
	// A descendant's ceiling is the agent that spawned it, not the session:
	// the session's mode is already the ceiling on that agent, so taking the
	// spawner's carries the clamp down the tree and a child in plan mode
	// cannot delegate its way out of plan mode.
	//
	// Read through AgentMode rather than off the child, because a mode is the
	// child's own field under the child's own lock — and taken here it would
	// be a second lock held under the supervisor's.
	if p.caller != "" {
		if up, ok := s.AgentMode(p.caller); ok {
			p.mode = up
		}
	}
	// A profile may start its children stricter than the parent (a
	// reviewer in plan mode under an auto session); childMode clamps it to
	// the parent either way, so it can never start looser.
	if args.profile.HasMode {
		p.mode = args.profile.Mode
	}

	// Writers work in isolated worktrees, so they cannot overwrite each
	// other's files — but two patches over the same file conflict when they
	// land. A declared scope is refused up front rather than discovered at
	// apply time.
	//
	// A spawn that asked to wait for the claim is queued behind it instead.
	// It is still admitted in admitSpawn like any other, so a budget that
	// could not start it is refused now rather than when the claim comes
	// free; what it does not take until then is a slot and a copy of the
	// tree.
	// See docs/capabilities/subagents.md#a-writer-starts-from-your-tree.
	if args.profile.Writes {
		if holder, claim, clash := s.claimConflict(args.paths, args.overlap); clash {
			if !args.WaitForClaim {
				// Allowing overlap on one side only is not a shared claim:
				// the writer already holding the file was spawned to have
				// it to itself.
				if args.overlap {
					return fmt.Errorf("%s already claims %s, which overlaps this agent's paths, and was not spawned with overlap: allowed, so the claim is not shared; wait for it with agent_report, or narrow the paths so the two do not share files", holder, claim)
				}
				return fmt.Errorf("%s already claims %s, which overlaps this agent's paths; wait for it with agent_report, or narrow the paths so the two do not share files", holder, claim)
			}
			// Named as the writer it follows rather than the first of the
			// claims ahead of it, the way its lane goes on naming it.
			p.waitsOn, p.waitsFor = holder, claim
			if h, c, ok := s.claimAhead(args.paths, p.seq, args.overlap); ok {
				p.waitsOn, p.waitsFor = h, c
			}
		}
	}

	p.model = args.Model
	if s.opts.ModelFor != nil {
		p.model = s.opts.ModelFor(args.role, p.depth, args.Model)
	}

	// The turns the child inherits are chosen before its environment is
	// built, because the prompt that environment carries says how many it
	// was given; they are rendered after, because the scrub and the store an
	// elided result goes into are the environment's.
	//
	// Read only where there are turns to hand over: an integration writer is
	// spawned from a child's goroutine, and the session's conversation is
	// only ever read on the goroutine that approved a spawn.
	if args.inherit > 0 {
		p.turns, p.inheritTurns = lastTurns(s.conversationOf(p.caller), args.inherit)
	}
	return nil
}

// admitSpawn gives the child its context, builds its environment as a
// preflight and refuses a budget that cannot cover the admission floor:
// the inherited prompt, the evidence, a resume prologue and the task. It
// runs under claimMu for a writer, which the caller holds, and never under
// mu. It reads the plan the earlier phases wrote and writes the plan's
// context and cancel, evidence, inheritance, prologue and floor figures; on
// a refusal it cancels the context it made, so nothing it took outlives it.
func (s *Supervisor) admitSpawn(p *spawnPlan) error {
	args := p.args
	// The context is the child's from here, whether or not it has anywhere
	// to work yet: a writer queued behind a full set of slots is one a kill
	// has to reach, and the cancel is what reaches it.
	cctx, cancel := context.WithCancel(s.ctx)
	// Construct the role environment before admitting the child, but never its
	// worktree or record. A doomed budget must not consume either resource.
	// A spawn is the first attempt, and says so as a retry's preflight does.
	preflight, preflightErr := s.opts.NewEnv(cctx, Spec{Name: p.name, Role: args.role, Root: s.opts.Root,
		Parent: p.caller, Depth: p.depth,
		Model: p.model, Paths: args.paths, Worktree: args.profile.Writes, MaxTokens: args.maxTokens, Attempt: 1,
		Inherit: p.inheritTurns, Integrates: p.integ.sourceName()})
	if preflightErr != nil {
		cancel()
		return fmt.Errorf("the agent's environment could not be built: %w", preflightErr)
	}
	// A review's evidence is part of what it is admitted for: it arrives in
	// the child's first turn, so a budget that could not carry it is a
	// budget that cannot start this review, and finding that out after the
	// slot is open is the thing admission exists to prevent.
	evidence := ""
	switch {
	case p.integ != nil:
		// An integration writer's evidence is the conflict itself, and it is
		// admitted for it the way a review is for its diff.
		evidence = p.integ.evidence
	case args.profile.Reviews:
		evidence = declaredEvidence(s.opts.Root, args.paths)
	}
	// The evidence goes ahead of everything else the first turn opens with,
	// including how a resumed attempt ended: a review that reads the change
	// before it reads the story of the last try is the ordering the whole
	// contract is about.
	//
	// It is built here, ahead of the floor, because the floor is what it has
	// to be measured against. A resumed handoff runs to whatever the failed
	// child read and wrote down — thousands of tokens of paths and progress —
	// and built after admission that is a cost the child pays and was never
	// admitted for: the resume starts on a budget its opening turn alone
	// exhausts, and dies where the handoff it was given ends. A retry
	// measures the same prologue before it commits, and both must.
	//
	// The parent's turns go ahead of all of it: they are what the evidence
	// and the task were written in the middle of. They are measured with a
	// stand-in for the store, so a spawn the floor refuses leaves nothing in
	// it; the real placeholders are the same length and are written only
	// once the child is admitted.
	resumeText := ""
	if args.resumeHandoff != "" {
		resumeText = resumePrologue(p.resume, s.opts.EvidenceExists)
	}
	inheritance := inheritedPrologue(p.turns, p.inheritTurns, preflight.Scrub, measuringArchive(preflight.Archive))
	inherited, setup, floor := admissionFloor(preflight, inheritance+evidence+resumeText+args.Task)
	if args.maxTokens < floor {
		cancel()
		return errors.New(admissionRefusal(args.maxTokens, floor, p.inheritTurns, agent.EstimateTokens(inheritance)))
	}
	if preflight.Archive != nil {
		inheritance = inheritedPrologue(p.turns, p.inheritTurns, preflight.Scrub, preflight.Archive)
	}
	p.cctx, p.cancel = cctx, cancel
	p.evidence, p.inheritance, p.prologue = evidence, inheritance, inheritance+evidence+resumeText
	p.inherited, p.setup, p.floor = inherited, setup, floor
	return nil
}

// buildChild makes the admitted child, queued, and opens a reader's
// workspace. It runs under claimMu for a writer, which the caller holds, and
// never under mu; the child it builds is not yet in the list, so no other
// goroutine can reach it. It reads the plan and the supervisor's options and
// writes nothing shared; on a failure it cancels the plan's context.
func (s *Supervisor) buildChild(p *spawnPlan) (*child, error) {
	args := p.args
	c := &child{
		name:            p.name,
		parent:          p.caller,
		depth:           p.depth,
		seq:             p.seq,
		role:            args.role,
		profile:         args.profile,
		task:            args.Task,
		model:           p.model,
		paths:           args.paths,
		batch:           p.batch,
		steps:           args.steps,
		evidence:        p.evidence,
		inheritance:     p.inheritance,
		inheritTurns:    p.inheritTurns,
		inheritTokens:   agent.EstimateTokens(p.inheritance),
		prologue:        p.prologue,
		root:            s.opts.Root,
		mode:            p.mode,
		maxRounds:       args.maxRounds,
		maxTokens:       args.maxTokens,
		inheritedTokens: p.inherited,
		setupTokens:     p.setup,
		admissionFloor:  p.floor,
		ctx:             p.cctx,
		cancel:          p.cancel,
		done:            make(chan struct{}),
		steerWake:       make(chan struct{}, 1),
		state:           StateQueued,
		detail:          queuedDetail(p.waitsOn),
		waitClaim:       args.WaitForClaim && args.profile.Writes,
		overlap:         args.overlap,
		integrates:      p.integ,
		waitsOn:         p.waitsOn,
		started:         s.clock()(),
		now:             s.opts.Now,
		attempt:         1,
		prices:          s.opts.Prices,
		spend:           meter.New(s.opts.Prices),
	}
	// A reader's workspace is the parent's own root and costs nothing to
	// hold, so it is opened here where a failure is still this call's answer
	// rather than a child that appears and immediately fails. A writer's
	// waits for its slot (openWorkspace).
	if !args.profile.Writes {
		w, wErr := s.openWorkspace(c, p.cctx, args.maxRounds, c.attempt)
		if wErr != nil {
			p.cancel()
			return nil, wErr
		}
		c.install(w)
	}
	return c, nil
}

// spawnReply is what the spawn tool answers with: the child, its budget and
// what the caller should know of how it works. It is pure string building
// over the plan; sharedHolder and sharedClaim are the claim an overlapping
// writer shares with another, read by the caller, empty when there is none.
func spawnReply(p *spawnPlan, sharedHolder, sharedClaim string) string {
	args := p.args
	note := ""
	if args.profile.Writes {
		note = " It edits an isolated copy of the workspace; its changes come back as a single patch the user reviews."
		if len(args.paths) > 0 {
			note += " It claims " + strings.Join(args.paths, ", ") + "; another writer cannot claim overlapping paths while it runs."
			if args.overlap {
				note = " It edits an isolated copy of the workspace; its changes come back as a single patch the user reviews. It claims " +
					strings.Join(args.paths, ", ") + " and allows overlap: another writer spawned with overlap: allowed may claim the same files, both patches land through the merge, and where the two change the same lines an integration writer is started to reconcile them."
				if sharedHolder != "" {
					note += fmt.Sprintf(" It shares %s with %s.", sharedClaim, sharedHolder)
				}
			}
		} else {
			note += " It declared no paths, so nothing stops a second writer from touching the same files — pass paths when you fan out writers."
		}
		if p.waitsOn != "" {
			note += fmt.Sprintf(" It has not started: %s claims %s, and it waits behind that claim without a slot or a copy of the workspace, starting from the tree as it stands once the claim is released.", p.waitsOn, p.waitsFor)
		}
	}
	// The evidence a review opened on, so the caller knows whether it is
	// judging the change or looking for it. Declaring nothing is worth
	// saying for the reason an undeclared writer's scope is: the review is
	// about to spend its pass finding what it was meant to be reading.
	if args.profile.Reviews {
		if p.evidence != "" {
			note = " It opens on the declared change under " + strings.Join(args.paths, ", ") + " and reports once it has examined it."
		} else {
			note = " It declared no paths, so it starts from your task text alone — pass paths and it opens on their diff instead of surveying for the change."
		}
	}
	modelNote := ""
	if p.model != "" {
		modelNote = ", " + p.model
	}
	resumed := ""
	if args.resumeHandoff != "" {
		resumed = fmt.Sprintf(" It resumes the verified handoff %s without replaying the failed child's transcript.", args.resumeHandoff)
	}
	// What it was handed of this conversation, in the unit the call asked
	// in, so a caller that asked for more turns than there were learns it.
	if p.inheritTurns > 0 {
		resumed += fmt.Sprintf(" It was handed your %s (~%s tokens) ahead of its task.",
			lastTurnsPhrase(p.inheritTurns), formatTokens(agent.EstimateTokens(p.inheritance)))
	}
	if p.unchecked != "" {
		resumed += " " + p.unchecked
	}
	return fmt.Sprintf("Spawned %s (%s%s, %s, ~%s token budget).%s%s It works in the background: call agent_report with name=%q in a later step to wait for and collect its final report, or agent_report with no arguments for a status overview.",
		p.name, args.role, modelNote, roundBudgetLabel(args.maxRounds), formatTokens(args.maxTokens), note, resumed, p.name)
}
