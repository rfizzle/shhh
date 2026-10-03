package subagent

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/meter"
	"github.com/rfizzle/shhh/internal/plan"
)

// CancelTurn interrupts a child's current turn: the in-flight stream
// aborts, outstanding calls get synthetic results, and the child parks idle
// awaiting steering — Ctrl+C semantics without killing the agent.
func (s *Supervisor) CancelTurn(name string) error {
	c, err := s.lookup(name)
	if err != nil {
		return err
	}
	if s.isClosed() {
		return ErrClosed
	}
	// The state is read and the cancel marked under one lock, so a child
	// that goes idle in between is not left holding a cancel for a turn it
	// has not started.
	c.mu.Lock()
	state := c.state
	h := c.headless
	switch state {
	case StateRunning, StateBlocked:
	default:
		c.mu.Unlock()
		return fmt.Errorf("agent %s has no turn in progress (%s)", name, state)
	}
	c.interruptTurnLocked()
	c.mu.Unlock()
	if h != nil {
		h.Interrupt()
	}
	return nil
}

// Kill cancels a child outright: its context is cancelled, its run finishes
// as failed/cancelled with a well-formed conversation, and (for writers) its
// worktree is removed. The transcript stays inspectable.
//
// A kill takes the subtree with it. An agent that delegated is the reason its
// descendants are running at all — a reviewer under a writer whose worktree is
// about to be discarded has nothing left to judge — so the agents under the
// one named are cancelled first, deepest first, and the agent named goes last
// of all. Their endings say so: the cancelled category, with the kill they
// went with named on the lane.
// See docs/capabilities/subagents.md#what-nesting-does-to-the-rest-of-it.
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
	for _, d := range s.subtree(name) {
		d.mu.Lock()
		live := d.state != StateDone && d.state != StateFailed
		if live {
			d.cancelledBy = name
		}
		listening := d.state == StateDone && d.listening
		d.mu.Unlock()
		if listening {
			// A descendant that has answered stays answered, but it can no
			// longer be asked anything: what it would be asked about is the
			// work the kill is throwing away, and what it read may be a copy
			// that is about to be removed.
			d.stop()
			continue
		}
		if !live {
			continue
		}
		d.appendEntry(TranscriptEntry{Kind: EntrySystem, Text: name + " was killed, so this agent ends with it."})
		d.stop()
	}
	c.stop()
	return nil
}

// subtree is every agent below name, deepest first, so a caller that walks it
// reaches a grandchild before the child that spawned it. Finished agents are
// in it: what a kill's teardown waits for is a goroutine, and one belonging to
// an agent that has already said it failed may still be removing its worktree.
func (s *Supervisor) subtree(name string) []*child {
	if name == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*child
	for _, c := range s.children {
		if s.descendsLocked(name, c.name) {
			out = append(out, c)
		}
	}
	// Stable, so agents at one level keep spawn order and only the levels are
	// reordered.
	slices.SortStableFunc(out, func(a, b *child) int { return b.depth - a.depth })
	return out
}

// Under names the agents a kill of one agent would take with it: the live ones
// below it, deepest first. It is what a surface asks before it kills, so the
// confirm can say how many rather than leaving the count to be discovered
// afterwards on the roster.
func (s *Supervisor) Under(name string) []string {
	var names []string
	for _, c := range s.subtree(name) {
		c.mu.Lock()
		state := c.state
		c.mu.Unlock()
		switch state {
		case StateDone, StateFailed:
		default:
			names = append(names, c.name)
		}
	}
	return names
}

// awaitSubtree holds a killed agent's teardown until the agents under it have
// ended. Kill cancels the subtree ahead of the agent named, but cancelling is
// not ending: a reviewer still reading the copy of the checkout it was given
// would find it deleted underneath it, and would report on a tree that went
// away mid-read. The wait is on the ending agent's own goroutine rather than
// on the keystroke that asked for the kill, for the reason the retry's is —
// a surface that blocked on a teardown would stop redrawing everything else
// for as long as it took — and it is bounded the same way, because a
// descendant that is stuck is not a reason to leave a worktree on disk.
// The wait belongs to every agent the kill took and not only to the one the
// person named: a writer two levels down is an ancestor to whatever it
// delegated to, and its copy of the checkout is being read by exactly the same
// kind of agent.
func (s *Supervisor) awaitSubtree(c *child) {
	c.mu.Lock()
	ending := c.killed || c.cancelledBy != ""
	c.mu.Unlock()
	if !ending {
		return
	}
	timer := time.NewTimer(retryTeardownWait)
	defer timer.Stop()
	for _, d := range s.subtree(c.name) {
		d.mu.Lock()
		done := d.done
		d.mu.Unlock()
		select {
		case <-done:
		case <-timer.C:
			return
		case <-s.ctx.Done():
			return
		}
	}
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
		return ErrClosed
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
		return ErrClosed
	}
	// A retry comes back minutes later, and a writer spawned in between may
	// hold the files this one claims. It is asked the question a spawn is
	// asked, before it takes a slot or a copy of the tree: refused naming
	// the writer, or — where the spawn asked to wait for a claim — queued
	// behind it by run, as the first attempt would have been.
	// See docs/capabilities/subagents.md#a-failed-child-can-be-run-again.
	if c.profile.Writes && !c.waitClaim {
		if holder, claim, clash := s.claimAround(c); clash {
			return fmt.Errorf("cannot set up the retry: %s now holds %s, which overlaps this agent's paths; wait for it with agent_report, then retry", holder, claim)
		}
	}
	// Read before anything is replaced: the handoff a child stopped by its
	// budget was asked for is sitting in the report field this attempt is
	// about to clear, and every retry that did not carry it threw away the
	// one thing the failed attempt produced.
	c.mu.Lock()
	handoff := c.report
	evidence := c.evidence
	inheritance, inheritTurns := c.inheritance, c.inheritTurns
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
	preflight, preflightErr := s.opts.NewEnv(cctx, Spec{Name: c.name, Role: c.role, Root: s.opts.Root,
		Parent: c.parent, Depth: c.depth,
		Model: c.model, Paths: c.paths, Worktree: c.profile.Writes, MaxTokens: budget, Attempt: attempt, Inherit: inheritTurns})
	if preflightErr != nil {
		cancel()
		return fmt.Errorf("cannot set up the retry: the agent's environment could not be built: %w", preflightErr)
	}
	// The inherited turns go first again, ahead of the handoff, exactly as
	// the first attempt was handed them: the handoff is written against that
	// context, and an attempt that read it without the turns it assumes would
	// be reading notes about a conversation it was never shown.
	inherited, setup, floor := admissionFloor(preflight, inheritance+evidence+retryPrologue(detail, handoff)+c.task)
	if budget < floor {
		cancel()
		return errors.New("cannot set up the retry: " + admissionRefusal(budget, floor, inheritTurns, agent.EstimateTokens(inheritance)))
	}
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
	// Whom the replaced attempt waited behind is that attempt's; this one
	// asks again, and its lane names whoever it finds.
	c.waitsOn = ""
	c.started, c.ended = c.at(), time.Time{}
	c.maxTokens = budget
	// The attempt is told what the one before it hit and handed over. A
	// retry on the identical prompt is an attempt with no reason to come out
	// differently: the child that died re-reading a large file dies
	// re-reading it, and the budget the session spent on the second attempt
	// bought the first one over again.
	c.prologue = inheritance + evidence + retryPrologue(detail, handoff)
	// Each attempt is measured against the budget it was spawned with; what
	// the earlier attempts spent is carried, not forgotten.
	c.priorSpend = c.priorSpend.Plus(c.spend.Total())
	c.spend = meter.New(c.prices)
	c.fresh, c.budgetHit = 0, false
	c.inheritedTokens, c.setupTokens, c.toolResultTokens = inherited, setup, 0
	c.admissionFloor = floor
	c.checkIns = 0
	c.reporting = false
	// A retry is a fresh conversation on the same task: no steer has reached
	// this attempt, whatever the last one was told.
	c.steers, c.steersAll, c.verdict, c.verdictCode, c.steerFrom = 0, 0, "", "", ""
	c.laneSteers, c.parentSteers = 0, 0
	// And the attempt it replaces ended for a reason that is that attempt's,
	// already on that attempt's own row. A retry that inherited it would
	// report the child as having ended twice the same way — and a child
	// killed once would report every attempt after it as killed too.
	c.endReason, c.killed, c.cancelledBy = "", false, ""
	c.turns, c.round = 0, 0
	c.toolCalls, c.step = 0, 0
	c.nudges = nil
	// A retry is a fresh conversation, which names its own plan.
	c.own = plan.Checklist{}
	// A retry starts from a worktree of its own, so what the attempt it
	// replaces wrote is not in the tree this one is reading.
	c.wrote = nil
	c.report, c.patchNote, c.streaming, c.progress = "", "", "", nil
	c.checkpointNext = false
	// A retry is the person asking for the work again, so the attempt it
	// replaces stops offering its patch. The patch is still in the evidence
	// store, and the handoff the new attempt opens on names it.
	c.handoff, c.handoffID, c.kept = Handoff{}, "", nil
	// And what earlier turns of the replaced attempt answered is that
	// attempt's: its conversation is gone, so there is nothing left to follow
	// up on.
	c.earlier, c.followUp, c.answered = nil, "", false
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
