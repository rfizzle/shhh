package subagent

import (
	"context"
	"maps"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/digest"
	"github.com/rfizzle/shhh/internal/meter"
	"github.com/rfizzle/shhh/internal/nudge"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/plan"
	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/tools"
)

// child is one sub-agent: an internal/agent instance plus its runtime and
// live status.
type child struct {
	name   string
	parent string // spawning agent's name; "" means the orchestrator
	// depth is how far under the session this child sits, counting the
	// session as SessionDepth. It is stored rather than walked up the
	// parent links on each reading because it decides which set of
	// concurrency slots the child draws from, which is read on a path where
	// the supervisor's lock is not held.
	depth int
	// seq is the child's place in spawn order, taken with its name. It is
	// what the supervisor's list is kept in, because two spawns in one batch
	// finish setting up in whatever order they finish and a kill removes
	// nothing, so neither may move a child along the map a chord walks.
	seq      int
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
	// inheritedTokens and setupTokens are the fixed cost admission reserved
	// before this child claimed a slot. Tool result and handoff sizes are
	// collected as the attempt runs; analysis is the fresh-token remainder.
	inheritedTokens, setupTokens, toolResultTokens, admissionFloor int64

	ctx      context.Context
	cancel   context.CancelFunc
	agent    *agent.Agent
	headless *agent.Headless
	env      Env
	rec      Recorder
	done     chan struct{}
	// endsOwed counts the attempt ends whose EventDone has not gone out
	// yet, and updateHeld says an update was held back meanwhile
	// (emitUpdate). Both are guarded by mu.
	endsOwed   int
	updateHeld bool
	// steerWake nudges an idle child that new steering arrived (buffered 1).
	steerWake chan struct{}
	// steered is closed and replaced each time a steer is queued, under mu,
	// so every agent_report wait this child is inside comes out of it. It is
	// a broadcast rather than a token like steerWake because a round can hold
	// several waits at once, and a token would wake one of them and leave the
	// rest behind the slowest agent they name.
	steered chan struct{}

	mu      sync.Mutex
	mode    agent.Mode
	state   State
	detail  string
	started time.Time
	ended   time.Time
	step    int // announcements made, i.e. steps entered
	// own is the working checklist the child named itself and the steps its
	// progress lines have marked (steps.go); empty for a child that named
	// none.
	own plan.Checklist
	// turns counts the turns this attempt has run, so a child's events are
	// placed the way a session's are: a tool call in round 30 of turn 3 is a
	// different fact from the same call in round 2 of turn 1.
	turns     int
	toolCalls int
	// nudges is which built-in tools this attempt's shell reads have been
	// pointed at in the turn it is on; nil until its first command, and
	// dropped with the rest of an attempt's counts on a retry, whose
	// conversation has been told nothing.
	nudges *nudge.Turn
	// clock splits the running turn by what it waited on. Only the goroutine
	// that drives the child touches it: the run marks it, the call that went
	// to the parent marks its wait (tookAnswer), and the turn's close reads
	// it.
	clock agent.TurnClock
	// askedAt and answeredAt are when the child last went blocked on an
	// approval and when it came out of it, stamped by set where its state
	// says so and taken by the goroutine that owns the clock.
	askedAt, answeredAt time.Time
	// round is the tool round the child last started, copied off its agent
	// by the goroutine that drives it, so a status taken from anywhere can
	// state it: the agent's own counter is written unguarded by that
	// goroutine, and reading it from another is a race.
	round int64
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
	wrote map[string]bool
	// spend is this attempt's bill, one ledger entry per model, each request
	// priced as it came back off its own cache split. It is a ledger rather
	// than a token pair because the pair is the defect: a child re-sends its
	// prompt every round and the provider serves nearly all of it from cache,
	// so a total charged at the fresh input rate reads several times what the
	// child cost. prices may be nil, and then the ledger counts tokens and
	// prices nothing, which is what leaves the recorder its own fallback.
	spend  *meter.Ledger
	prices *pricing.Table
	// now is the clock started and ended are read off: Options.Now, and nil
	// for the wall clock.
	now func() time.Time
	// fresh is what the token budget is measured against: the input less the
	// part every prompt was served from the provider's cache, plus the
	// output. It is a counter of its own because it answers a different
	// question from the bill — every surface that prices a child, and the
	// session row it writes, wants the tokens it was billed for, and only the
	// budget wants the tokens it newly took in.
	fresh int64
	// priorSpend carries the bill of earlier attempts across a retry. The
	// live ledger is the attempt's own, as fresh is, so each attempt gets the
	// budget it was spawned with, and it is what that attempt's own session
	// row is told. The status adds the carried spend back, because money
	// already spent does not stop being spent when the child runs again and a
	// lane shows one child rather than one attempt.
	priorSpend meter.Totals
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
	// cancelledBy names the ancestor whose kill took this agent with it, and
	// is empty for every other ending. A kill takes the subtree, and what
	// separates one of those endings from the agent the person actually named
	// is only this: the agent named ended because somebody ended it, and the
	// ones under it ended because it did.
	// See docs/capabilities/subagents.md#what-nesting-does-to-the-rest-of-it.
	cancelledBy string
	// endReason is how this attempt stopped, from the closed set in
	// internal/observe, and empty until it does.
	endReason string
	checkIns  int
	// reporting marks a review whose inspection pass is over and which has
	// been told to write its report. It exists so the round cap can be a
	// stop for a review and a check-in for everything else without the
	// stop becoming a loop: the second time a review reaches its cap it has
	// spent the report allowance too, and it ends there with what it has.
	reporting bool
	// steers is what Status.Steers reports and verdict what Status.Verdict
	// does. They are the child's own copies under this lock rather than
	// readings of the agent: a status is taken from whichever goroutine asked
	// — the parent's tool call, the lane's next frame — and reaching into the
	// loop's own state from there is a race. The lock is owed on the writing
	// side too, and by more than the run's goroutine: the reading that sets
	// verdict can land after the run it was reading has returned.
	steers int
	// laneSteers and parentSteers are what Status.LaneSteers and
	// Status.ParentSteers report: this turn's steers from the two parties
	// that are somebody rather than something, counted where the message
	// reaches the child (drainSteering) rather than where it was queued. A
	// message still waiting at the boundary has not steered the child yet,
	// and a count that took it as given would be describing a redirect the
	// child has never read.
	laneSteers   int
	parentSteers int
	// steersAll is every steer the check gave this attempt, which the record
	// takes; steers above is the current turn's, which the lane shows. It
	// counts the check's interruptions and not a person's or the
	// orchestrator's: each of theirs is already a steered signal of its own
	// carrying its source, and a column that added them in would count them
	// twice and put a party's redirects into the one figure that says
	// whether the check's own steering helps.
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
	progress    []string
	handoff     Handoff
	handoffID   string
	// earlier is every report this attempt gave before a follow-up asked it
	// something else, oldest first, each with the turn it closed. followUp is
	// the first words of the follow-up it is working on now, and empty
	// otherwise. listening marks a finished child whose goroutine is still
	// there to take one, which is what Steer asks of a done child.
	earlier   []EarlierReport
	followUp  string
	listening bool
	// answered marks an attempt that has finished once, so the record is
	// told the attempt answered once however many follow-ups it answers.
	answered bool
	// kept is a writer's change that never reached the checkout: declined,
	// cancelled, refused by the apply, or stopped short by a budget, a kill or
	// a cancel. Nil for every child that has none.
	kept *keptPatch
	// prologue is what the next attempt's first turn opens with, ahead of
	// the task: what the attempt it replaces hit, and the handoff it left.
	// It is a field rather than an argument to run because a retry can be
	// started from three places and none of them is the goroutine that will
	// read it, and it is taken once, so a second turn on the same attempt is
	// the ordinary conversation.
	prologue string
	// evidence is a review's declared paths and their diff, held because a
	// retry is a fresh conversation: an attempt that opens on the task alone
	// is the unbounded review the second attempt least needs to be.
	evidence string
	// inheritance is the parent's turns this child was spawned with, as the
	// text its first turn opened on, and inheritTurns how many turns that is.
	// Held for the reason evidence is: a retry re-issues exactly what the
	// first attempt was handed, not a fresh read of a conversation that has
	// moved on since (docs/capabilities/subagents.md#what-they-share).
	// inheritTokens is its estimate, which the lane's budget line states.
	inheritance   string
	inheritTurns  int
	inheritTokens int64
	// Live session surface: transcript entries, the in-flight
	// assistant text, queued steering messages, and the current turn's
	// interrupt channel.
	transcript []TranscriptEntry
	// checkpointNext latches a round whose prose was the public status the
	// run had been asked for, for the assistant entry the round's first call
	// is about to flush it into (beginToolEntry). The status arrives on its
	// own hook and the words arrive as tokens, so the two are only ever put
	// back together here.
	checkpointNext bool
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
	// intPending is a cancel that arrived for a turn whose interrupt channel
	// was not armed yet: the child reads running from the moment it starts,
	// well before its first turn is, and a cancel in that gap — or between a
	// turn and the one steering starts after it — would otherwise close a
	// channel beginTurn is about to replace. beginTurn carries it over.
	intPending bool
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
	// landings are patches other writers have landed in the parent's
	// checkout since this writer's copy was taken, waiting for its next round
	// boundary to be carried in (reseed). reseeding names the writer whose
	// patch is being carried in now, and is empty otherwise; reseeds is how
	// many have been. All three belong to the attempt's copy, so a retry,
	// which takes a fresh copy of the tree as it stands, starts them again.
	landings  []landing
	reseeding string
	reseeds   int
	// waitClaim is a writer spawned with wait_for_claim: its run waits for
	// the writers ahead of it to release an overlapping claim before it takes
	// a slot (awaitClaim), and waitsOn names the one it is waiting behind
	// now, empty once it has none.
	waitClaim bool
	waitsOn   string
	// overlap is a writer spawned with overlap: allowed, whose claim may be
	// shared with another writer that allowed it too (claimHeld).
	overlap bool
	// integrates is what an integration writer is reconciling; nil for
	// every other child (integrate.go).
	integrates *integration
	// slotWait is how many checks were running when this child began to wait
	// for a check slot, and zero while it is not waiting for one
	// (takeCheckSlot); slotWaiters is how many of its checks are waiting,
	// since one round can ask for several.
	slotWait    int
	slotWaiters int
}

func (c *child) set(state State, detail string) {
	c.mu.Lock()
	// A blocked child is waiting on the person its parent puts the card to,
	// which its turn's clock is told once the call is back (tookAnswer).
	switch {
	case state == StateBlocked && c.state != StateBlocked:
		c.askedAt, c.answeredAt = c.at(), time.Time{}
	case state != StateBlocked && c.state == StateBlocked:
		c.answeredAt = c.at()
	}
	c.state = state
	c.detail = detail
	// Any transition ends a hold. A child sits in its wait between two of
	// these, so nothing that is still parked passes through here — but a
	// killed one comes out of the wait by a route the release never took,
	// and a finished lane still reading "held · waiting for release" would
	// be offering a release that can no longer do anything.
	c.heldOn = nil
	// A cancel waiting for the next turn is only for a child still running;
	// one that went idle or finished first has no turn left for it, and the
	// turn a steer or a follow-up starts later is not the one it was for.
	if state != StateRunning && state != StateBlocked {
		c.intPending = false
	}
	// A finished child's elapsed stops moving: its lane reports what the work
	// took, not how long ago it happened.
	switch state {
	case StateDone, StateFailed:
		if c.ended.IsZero() {
			c.ended = c.at()
		}
		c.followUp = ""
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

// tookAnswer books the approval the call just resolved waited on, if it went
// to the parent, to the turn's clock: the person's time from the card to the
// answer, and the call's own again from there. A call ended before an answer
// came is the person's up to now.
func (c *child) tookAnswer() {
	c.mu.Lock()
	asked, answered := c.askedAt, c.answeredAt
	c.askedAt, c.answeredAt = time.Time{}, time.Time{}
	c.mu.Unlock()
	if asked.IsZero() {
		return
	}
	if answered.IsZero() {
		answered = c.at()
	}
	c.clock.Ask(asked)
	c.clock.Tool(answered)
}

// at is the time on the child's clock.
func (c *child) at() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// clock is the clock a new child's age is read off.
func (s *Supervisor) clock() func() time.Time {
	if s.opts.Now != nil {
		return s.opts.Now
	}
	return time.Now
}

func (c *child) status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.statusLocked()
}

// statusLocked is status for a caller already holding c.mu.
func (c *child) statusLocked() Status {
	end := c.ended
	if end.IsZero() {
		end = c.at()
	}
	summary := ""
	if c.state == StateDone {
		summary = firstLine(c.report)
	}
	detail := c.detail
	if c.heldOn != nil {
		detail = "held · waiting for release"
	}
	if c.reseeding != "" {
		detail = "reseeding · carrying " + c.reseeding + "'s landed patch into its copy"
	}
	if c.slotWait > 0 {
		detail = slotWaitDetail(c.slotWait)
	}
	tokens := observe.ChildTokens{
		Inherited: c.inheritedTokens,
		Setup:     c.setupTokens,
		Tools:     c.toolResultTokens,
		Handoff:   estimateReportTokens(c.report),
		Fresh:     c.fresh,
	}
	tokens.Analysis = max(c.fresh-tokens.Inherited-tokens.Setup-tokens.Tools-tokens.Handoff, 0)
	return Status{
		Name:              c.name,
		Role:              c.role,
		Task:              c.task,
		Model:             c.model,
		Paths:             c.paths,
		State:             c.state,
		Detail:            detail,
		ToolCalls:         c.toolCalls,
		Budget:            c.maxTokens,
		AdmissionFloor:    c.admissionFloor,
		Tokens:            tokens,
		Spend:             c.priorSpend.Plus(c.spend.Total()),
		Batch:             c.batch,
		Started:           c.started,
		Elapsed:           end.Sub(c.started),
		Steps:             c.stepCount(),
		Turn:              int64(c.turns),
		Round:             c.round,
		Summary:           summary,
		CheckIns:          c.checkIns,
		End:               c.endReason,
		Handoff:           c.handoffID,
		PatchKept:         c.kept != nil,
		RecommendedBudget: c.handoff.RecommendedBudget,
		Steers:            c.steers,
		LaneSteers:        c.laneSteers,
		ParentSteers:      c.parentSteers,
		Verdict:           c.verdict,
		SteerFrom:         c.steerFrom,
		Seeded:            c.seeded,
		Reseeds:           c.reseeds,
		Inheritance:       c.inheritTokens,
		Held:              c.heldOn != nil || c.reseeding != "" || c.slotWait > 0,
		Reseeding:         c.reseeding != "",
		SlotWait:          c.slotWait,
		WaitsOn:           c.waitsOn,
		FollowUp:          c.followUp,
		TakesFollowUp:     c.state == StateDone && c.listening,
	}
}

// attemptSpend is what the attempt now running has cost, which is what that
// attempt's own session row is told — deliberately not the carried total the
// status reports. A retry opens a second row for the same child and leaves
// the first one holding the failed attempt's spend; a row update sets
// absolute totals, so handing the new row the carried figure would write
// that spend onto both rows and count it twice. A writer that burns 50k,
// fails, is retried and burns 30k would leave two rows summing to 130k for
// 80k of real work, and the cost derived from those tokens inflates with
// them.
func (c *child) attemptSpend() meter.Totals {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.spend.Total()
}

// reviewPassOver reports that this child is a review whose inspection pass
// has just ended and which has not yet been told to report. It is false for
// every other child — whose cap is a check-in — and false for a review that
// has already had the directive, so a review that spends its report
// allowance stops rather than being told to report a second time.
func (c *child) reviewPassOver() bool {
	if !c.profile.Reviews {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.reporting
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
		c.transcript = append(c.transcript,
			TranscriptEntry{Kind: EntryAssistant, Text: c.streaming, Checkpoint: c.checkpointNext})
		c.streaming = ""
		c.step++
	}
	// The latch is the round's, whether or not this round wrote prose for it
	// to land on.
	c.checkpointNext = false
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
	c.toolResultTokens += agent.EstimateTokens(result)
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

// noteApproved records on a call's own row that the parent's user answered
// its card. It is noteAllowed's other half: a rule's yes and a person's yes
// are the same question answered by different things, and a feed states an
// act once, so both ride the act rather than a notice above it.
// See docs/interface/surfaces.md#the-activity-row.
func (c *child) noteApproved(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if idx, ok := c.callRow[id]; ok {
		c.transcript[idx].ApprovedBy = ApprovedByUser
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

// life is this child as the seams at its start, end and compactions are told
// about it.
func (c *child) life() Life {
	return Life{Name: c.name, Role: string(c.role), Parent: c.parent, Seam: c.seam()}
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
