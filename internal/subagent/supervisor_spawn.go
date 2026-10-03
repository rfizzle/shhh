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
		if w.wt, err = addWorktreeContext(ctx, s.opts.Root, s.parentUntracked()); err != nil {
			return workspace{}, fmt.Errorf("cannot create an isolated worktree for a writer agent: %w", err)
		}
		w.root = w.wt.root
		// An integration writer's copy starts holding everything of the
		// patch it reconciles that merges cleanly, taken against the tree
		// as it stands now rather than as it stood at the conflict.
		if c.integrates != nil {
			if err = c.integrates.seed(w.wt); err != nil {
				removeWorktree(w.wt.repoTop, w.wt.dir)
				return workspace{}, err
			}
		}
	}
	w.env, err = s.opts.NewEnv(ctx, Spec{Name: c.name, Role: c.role, Root: w.root, Model: c.model, Paths: c.paths,
		Parent: c.parent, Depth: c.depth,
		Worktree: w.wt.dir != "", MaxTokens: c.maxTokens, AdmissionFloor: c.admissionFloor, Attempt: attempt,
		Inherit: c.inheritTurns, Integrates: c.integrates.sourceName()})
	if err != nil {
		removeWorktree(w.wt.repoTop, w.wt.dir)
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
			Worktree: w.wt.dir != "", Mode: s.childMode(c), MaxRounds: roundCap(w.agent),
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
	c.root, c.worktree, c.repoTop, c.seeded = w.root, w.wt.dir, w.wt.repoTop, w.wt.seeded
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
func (s *Supervisor) spawn(caller string, raw json.RawMessage, integ *integration) (string, error) {
	args, err := parseSpawnArgs(s.Profiles(), raw)
	if err != nil {
		return "", err
	}
	if s.ctx.Err() != nil {
		return "", ErrClosed
	}
	// The model is checked first of all, for the reason depth is: a name
	// the session cannot run is a child that fails on its first request,
	// and a refusal the model reads now costs nothing it was refusing to
	// spend. An integration writer is the supervisor's own and names none.
	// See docs/capabilities/subagents.md#the-model-is-offered-the-models-it-can-name.
	unchecked, err := s.checkModel(args.Model)
	if err != nil {
		return "", err
	}
	// Depth is checked with the role and before everything else that could
	// claim something, for the reason the token admission is: a refusal that
	// has already taken a slot, cut a worktree or opened a record row is a
	// refusal that cost the session what it was refusing to spend.
	depth := s.depthOf(caller) + 1
	if depth > s.opts.MaxDepth {
		return "", fmt.Errorf("delegation stops at depth %d and this agent would be depth %d; raise %s to let an agent this deep spawn, or report back and let the level above you spawn it",
			s.opts.MaxDepth, depth, MaxDepthKey)
	}
	// A descendant is never given more than the agent that spawned it. The
	// mode clamp below is the same rule for a different grant, and both run
	// before the child exists rather than at its first call, so a role the
	// spawner may not delegate is a refused spawn and not a child that will
	// be refused every tool it reaches for.
	// See docs/capabilities/subagents.md#a-child-may-delegate-to-a-configured-depth.
	if caller != "" && args.profile.Writes {
		if up, err := s.lookup(caller); err == nil && !up.profile.Writes {
			return "", fmt.Errorf("%s changes nothing, so it cannot delegate %s, which writes; an agent may only delegate what it could do itself", caller, args.role)
		}
	}
	resume := Handoff{}
	if args.resumeHandoff != "" {
		if s.opts.LoadHandoff == nil {
			return "", errors.New("failure handoffs are unavailable for this session")
		}
		data, loadErr := s.opts.LoadHandoff(args.resumeHandoff)
		if loadErr != nil {
			return "", fmt.Errorf("cannot resume handoff %q: %w", args.resumeHandoff, loadErr)
		}
		if resume, loadErr = UnmarshalHandoff(data); loadErr != nil {
			return "", fmt.Errorf("cannot resume handoff %q: %w", args.resumeHandoff, loadErr)
		}
		profile, profileErr := s.Profiles().Parse(string(resume.Role))
		if profileErr != nil {
			return "", fmt.Errorf("cannot resume handoff %q: %w", args.resumeHandoff, profileErr)
		}
		args.role, args.profile, args.Task, args.paths = resume.Role, profile, resume.Task, append([]string(nil), resume.Paths...)
		if resume.RecommendedBudget > args.maxTokens {
			args.maxTokens = resume.RecommendedBudget
		}
	}

	if args.profile.Writes {
		s.claimMu.Lock()
		defer s.claimMu.Unlock()
	}
	s.mu.Lock()
	if len(s.children) >= s.opts.MaxChildren {
		s.mu.Unlock()
		// The count is of starts, so a refusal with three children live must
		// not read as a concurrency limit, which is max_concurrent's.
		return "", fmt.Errorf("agent limit reached: this session has started %d of %d agents; the limit counts agents started, not agents running, so a finished agent still holds its slot — steer or retry the agents it has, or raise %s", len(s.children), s.opts.MaxChildren, MaxChildrenKey)
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
	s.spawned++
	seq := s.spawned
	mode := s.parentMode
	batch := s.batch
	s.mu.Unlock()
	// An integration writer joins the round its writer was spawned in, so
	// its lane is drawn in the fan-out it is reconciling.
	if integ != nil {
		batch = integ.batch
	}
	// A descendant's ceiling is the agent that spawned it, not the session:
	// the session's mode is already the ceiling on that agent, so taking the
	// spawner's carries the clamp down the tree and a child in plan mode
	// cannot delegate its way out of plan mode.
	//
	// Read through AgentMode rather than off the child, because a mode is the
	// child's own field under the child's own lock — and taken here it would
	// be a second lock held under the supervisor's.
	if caller != "" {
		if up, ok := s.AgentMode(caller); ok {
			mode = up
		}
	}
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
	//
	// A spawn that asked to wait for the claim is queued behind it instead.
	// It is still admitted below like any other, so a budget that could not
	// start it is refused now rather than when the claim comes free; what it
	// does not take until then is a slot and a copy of the tree.
	// See docs/capabilities/subagents.md#a-writer-starts-from-your-tree.
	waitsOn, waitsFor := "", ""
	if args.profile.Writes {
		if holder, claim, clash := s.claimConflict(args.paths, args.overlap); clash {
			if !args.WaitForClaim {
				// Allowing overlap on one side only is not a shared claim:
				// the writer already holding the file was spawned to have
				// it to itself.
				if args.overlap {
					return "", fmt.Errorf("%s already claims %s, which overlaps this agent's paths, and was not spawned with overlap: allowed, so the claim is not shared; wait for it with agent_report, or narrow the paths so the two do not share files", holder, claim)
				}
				return "", fmt.Errorf("%s already claims %s, which overlaps this agent's paths; wait for it with agent_report, or narrow the paths so the two do not share files", holder, claim)
			}
			// Named as the writer it follows rather than the first of the
			// claims ahead of it, the way its lane goes on naming it.
			waitsOn, waitsFor = holder, claim
			if h, c, ok := s.claimAhead(args.paths, seq, args.overlap); ok {
				waitsOn, waitsFor = h, c
			}
		}
	}

	model := args.Model
	if s.opts.ModelFor != nil {
		model = s.opts.ModelFor(args.role, depth, args.Model)
	}

	// The turns the child inherits are chosen before its environment is
	// built, because the prompt that environment carries says how many it
	// was given; they are rendered after, because the scrub and the store an
	// elided result goes into are the environment's.
	//
	// Read only where there are turns to hand over: an integration writer is
	// spawned from a child's goroutine, and the session's conversation is
	// only ever read on the goroutine that approved a spawn.
	var turns []provider.Message
	inheritTurns := 0
	if args.inherit > 0 {
		turns, inheritTurns = lastTurns(s.conversationOf(caller), args.inherit)
	}

	// The context is the child's from here, whether or not it has anywhere
	// to work yet: a writer queued behind a full set of slots is one a kill
	// has to reach, and the cancel is what reaches it.
	cctx, cancel := context.WithCancel(s.ctx)
	// Construct the role environment before admitting the child, but never its
	// worktree or record. A doomed budget must not consume either resource.
	// A spawn is the first attempt, and says so as a retry's preflight does.
	preflight, preflightErr := s.opts.NewEnv(cctx, Spec{Name: name, Role: args.role, Root: s.opts.Root,
		Parent: caller, Depth: depth,
		Model: model, Paths: args.paths, Worktree: args.profile.Writes, MaxTokens: args.maxTokens, Attempt: 1,
		Inherit: inheritTurns, Integrates: integ.sourceName()})
	if preflightErr != nil {
		cancel()
		return "", fmt.Errorf("the agent's environment could not be built: %w", preflightErr)
	}
	// A review's evidence is part of what it is admitted for: it arrives in
	// the child's first turn, so a budget that could not carry it is a
	// budget that cannot start this review, and finding that out after the
	// slot is open is the thing admission exists to prevent.
	evidence := ""
	switch {
	case integ != nil:
		// An integration writer's evidence is the conflict itself, and it is
		// admitted for it the way a review is for its diff.
		evidence = integ.evidence
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
		resumeText = resumePrologue(resume, s.opts.EvidenceExists)
	}
	inheritance := inheritedPrologue(turns, inheritTurns, preflight.Scrub, measuringArchive(preflight.Archive))
	inherited, setup, floor := admissionFloor(preflight, inheritance+evidence+resumeText+args.Task)
	if args.maxTokens < floor {
		cancel()
		return "", errors.New(admissionRefusal(args.maxTokens, floor, inheritTurns, agent.EstimateTokens(inheritance)))
	}
	if preflight.Archive != nil {
		inheritance = inheritedPrologue(turns, inheritTurns, preflight.Scrub, preflight.Archive)
	}
	prologue := inheritance + evidence + resumeText

	c := &child{
		name:            name,
		parent:          caller,
		depth:           depth,
		seq:             seq,
		role:            args.role,
		profile:         args.profile,
		task:            args.Task,
		model:           model,
		paths:           args.paths,
		batch:           batch,
		steps:           args.steps,
		evidence:        evidence,
		inheritance:     inheritance,
		inheritTurns:    inheritTurns,
		inheritTokens:   agent.EstimateTokens(inheritance),
		prologue:        prologue,
		root:            s.opts.Root,
		mode:            mode,
		maxRounds:       args.maxRounds,
		maxTokens:       args.maxTokens,
		inheritedTokens: inherited,
		setupTokens:     setup,
		admissionFloor:  floor,
		ctx:             cctx,
		cancel:          cancel,
		done:            make(chan struct{}),
		steerWake:       make(chan struct{}, 1),
		state:           StateQueued,
		detail:          queuedDetail(waitsOn),
		waitClaim:       args.WaitForClaim && args.profile.Writes,
		overlap:         args.overlap,
		integrates:      integ,
		waitsOn:         waitsOn,
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
		w, wErr := s.openWorkspace(c, cctx, args.maxRounds, c.attempt)
		if wErr != nil {
			cancel()
			return "", wErr
		}
		c.install(w)
	}

	s.mu.Lock()
	at, _ := slices.BinarySearchFunc(s.children, c.seq, func(k *child, seq int) int { return k.seq - seq })
	s.children = slices.Insert(s.children, at, c)
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
			if args.overlap {
				note = " It edits an isolated copy of the workspace; its changes come back as a single patch the user reviews. It claims " +
					strings.Join(args.paths, ", ") + " and allows overlap: another writer spawned with overlap: allowed may claim the same files, both patches land through the merge, and where the two change the same lines an integration writer is started to reconcile them."
				if holder, claim, ok := s.claimShared(args.paths); ok && holder != name {
					note += fmt.Sprintf(" It shares %s with %s.", claim, holder)
				}
			}
		} else {
			note += " It declared no paths, so nothing stops a second writer from touching the same files — pass paths when you fan out writers."
		}
		if waitsOn != "" {
			note += fmt.Sprintf(" It has not started: %s claims %s, and it waits behind that claim without a slot or a copy of the workspace, starting from the tree as it stands once the claim is released.", waitsOn, waitsFor)
		}
	}
	// The evidence a review opened on, so the caller knows whether it is
	// judging the change or looking for it. Declaring nothing is worth
	// saying for the reason an undeclared writer's scope is: the review is
	// about to spend its pass finding what it was meant to be reading.
	if args.profile.Reviews {
		if evidence != "" {
			note = " It opens on the declared change under " + strings.Join(args.paths, ", ") + " and reports once it has examined it."
		} else {
			note = " It declared no paths, so it starts from your task text alone — pass paths and it opens on their diff instead of surveying for the change."
		}
	}
	modelNote := ""
	if model != "" {
		modelNote = ", " + model
	}
	resumed := ""
	if args.resumeHandoff != "" {
		resumed = fmt.Sprintf(" It resumes the verified handoff %s without replaying the failed child's transcript.", args.resumeHandoff)
	}
	// What it was handed of this conversation, in the unit the call asked
	// in, so a caller that asked for more turns than there were learns it.
	if inheritTurns > 0 {
		resumed += fmt.Sprintf(" It was handed your %s (~%s tokens) ahead of its task.",
			lastTurnsPhrase(inheritTurns), formatTokens(agent.EstimateTokens(inheritance)))
	}
	if unchecked != "" {
		resumed += " " + unchecked
	}
	return fmt.Sprintf("Spawned %s (%s%s, %s, ~%s token budget).%s%s It works in the background: call agent_report with name=%q in a later step to wait for and collect its final report, or agent_report with no arguments for a status overview.",
		name, args.role, modelNote, roundBudgetLabel(args.maxRounds), formatTokens(args.maxTokens), note, resumed, name), nil
}
