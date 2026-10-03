package subagent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/provider"
)

// run drives one child to completion on its own goroutine. It owns what
// is scoped to the attempt as a whole — the deferred release of the slot,
// the worktree, the attempt's record and the done channel — and hands each
// phase between to a helper: endAttempt for how an attempt ends,
// prepareAttempt for the workspace and the start seam, newHeadless for the
// run that drives the turns, and afterTurn for what each turn ended on.
// None of them is called with the supervisor's mutex or the child's held.
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
	// started says the seam at the child's start let it run, which is what
	// makes there be an end for the seam at its end to be told about.
	started := false
	// finish ends the attempt (endAttempt) and keeps the status it ended on
	// for the event the way out sends.
	finish := func(state State, reason, detail string) {
		ended = s.endAttempt(c, started, state, reason, detail)
	}
	// done is the channel a caller waiting on this child is waiting on. A
	// finished child that is spoken to again is handed a new one for the
	// follow-up (claimFollowUp), so a wait started mid-follow-up waits for
	// the follow-up's answer; announced marks the one already closed, which
	// a child parked after answering has done before it waits.
	c.mu.Lock()
	done := c.done
	c.mu.Unlock()
	announced := false
	announce := func() {
		close(done)
		s.emit(Event{Kind: EventDone, Status: ended})
		announced = true
	}
	defer func() {
		if !announced {
			announce()
		}
	}()
	// The attempt's record and its workspace are captured rather than read
	// in the defers: a retry gives the child new ones, and this goroutine
	// closes and removes its own. Both are set again below, because a
	// writer's are not opened until it has a slot — a writer cancelled in
	// the queue never had either, and closes and removes nothing.
	c.mu.Lock()
	ctx, cancel, maxRounds, attempt, endRec := c.ctx, c.cancel, c.maxRounds, c.attempt, c.rec.End
	c.mu.Unlock()
	var worktree, repoTop string
	// recorded marks a row already closed on how the attempt last answered.
	// A child that answered closes its row there, as one that could not be
	// spoken to again did; a follow-up turn after it writes to the same row,
	// and whatever it ended on is written over the close on the way out.
	recorded := false
	defer func() {
		if endRec != nil && !recorded {
			endRec(c.end())
		}
		if worktree != "" {
			// A killed agent's copy of the checkout goes only once the
			// subtree the same kill cancelled has ended in it.
			s.awaitSubtree(c)
			removeWorktree(repoTop, worktree)
		}
	}()

	// Bounded concurrency: take a slot at this child's own depth, or notice
	// cancellation while queued. The slots are per depth so that what this
	// child waits behind is other agents at its level and never one of its
	// own ancestors — an ancestor blocked in agent_report waiting for this
	// child would otherwise be holding the slot this child is waiting for.
	// See docs/capabilities/subagents.md#a-wait-only-ever-points-down-the-tree.
	// A writer that asked to wait for an overlapping claim waits for it
	// first, ahead of the slot: what it is waiting for is a file, and a slot
	// held through that wait is one a writer with nothing in its way cannot
	// have.
	if c.waitClaim && !s.awaitClaim(ctx, c) {
		finish(StateFailed, observe.ChildCancelled, "cancelled")
		return
	}
	sem := s.slots(c.depth)
	holding := false
	defer func() {
		if holding {
			<-sem
		}
	}()
	select {
	case sem <- struct{}{}:
		holding = true
	case <-ctx.Done():
		// Nothing of this attempt ever ran, so there is nothing for it to
		// have ended of: whatever cancelled a queued child cancelled it.
		finish(StateFailed, observe.ChildCancelled, "cancelled")
		return
	}

	var prepared bool
	worktree, repoTop, endRec, prepared = s.prepareAttempt(c, ctx, cancel, maxRounds, attempt, endRec, finish)
	if !prepared {
		return
	}
	started = true

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
	h := s.newHeadless(c)

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
		next, answered, ok := s.afterTurn(c, report, err, worktree, endTurn, finish)
		if !ok {
			return
		}
		if !answered {
			turn = next
			continue
		}

		// A child that has answered can still be spoken to. It gives up
		// the slot it ran in — a finished child is not one running at
		// once, and holding it would stall every fan-out after this one
		// — and says it has answered, which is what a wait on it is
		// waiting for. Everything else it keeps: its conversation, its
		// claimed paths and, for a writer, its copy of the workspace, so a
		// follow-up is one more turn on the ground it already read. It
		// lets go of those when the session ends or it is killed, which
		// is the same place a child that could not be spoken to again
		// let go of them
		// (docs/capabilities/subagents.md#three-can-steer-a-child-and-none-of-them-can-end-it).
		<-sem
		holding = false
		if endRec != nil {
			endRec(c.end())
		}
		recorded = true
		announce()
		next, ok = s.awaitFollowUp(c)
		c.mu.Lock()
		if ok && c.state == StateDone {
			// A message sent while the child was still running, and not
			// read before it answered: it is a follow-up all the same.
			c.claimFollowUp(next)
		}
		claimed := c.state != StateDone
		if claimed {
			done = c.done
			announced, recorded = false, false
		}
		c.mu.Unlock()
		if !ok {
			if claimed {
				// The session ended, or the child was killed, between the
				// follow-up being claimed and its turn starting.
				finish(StateFailed, observe.ChildCancelled, "cancelled")
			}
			return
		}
		select {
		case sem <- struct{}{}:
		default:
			c.set(StateQueued, "queued · follow-up waiting for a slot")
			s.emitUpdate(c)
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				finish(StateFailed, observe.ChildCancelled, "cancelled")
				return
			}
		}
		holding = true
		c.mu.Lock()
		if c.profile.Reviews && c.reporting {
			// A review that has reported is asked something new, so its
			// inspection allowance is back rather than the few rounds its
			// report was given.
			c.reporting = false
			c.agent.SetMaxRounds(c.maxRounds)
		}
		detail := followUpDetail(c.followUp)
		c.mu.Unlock()
		c.set(StateRunning, detail)
		s.emitUpdate(c)
		// What the follow-up asks is what its readings are judged
		// against, on top of the task: a child answering the question it
		// was just asked has not drifted from the one it was spawned for.
		h.Summary.Extend(next)
		turn = next
		continue
	}
}

// endAttempt is the one way an attempt ends, and it returns the status the
// attempt ended on, taken at the transition. It settles the end reason (a
// kill or an ancestor's kill outranks what the run made of it), meets the
// seam at the child's end for an attempt that started and failed, keeps a
// failed writer's patch and writes its handoff, sets the state, releases
// the attempt's claims and files the end signal once per answer.
//
// It is called on the child's own goroutine with neither the supervisor's
// mutex nor the child's held. It takes c.mu itself to read cancelledBy,
// killed, report and answered and to write endReason and answered; the
// supervisor's mutex is taken only inside the methods it calls, such as
// releaseClaims.
func (s *Supervisor) endAttempt(c *child, started bool, state State, reason, detail string) Status {
	c.mu.Lock()
	// An agent the kill of an ancestor took with it ended of nothing of
	// its own, which is what the cancelled category says; the lane says
	// whose kill it went with, because "cancelled" on its own would send
	// a reader looking for a turn or a session that ended and there was
	// neither.
	if by := c.cancelledBy; by != "" && state == StateFailed {
		reason, detail = observe.ChildCancelled, "cancelled · "+by+" was killed"
	}
	// A kill outranks whatever the run made of the cancellation it was
	// handed. Everything above this reads a cancelled context, and only
	// the child itself knows which of the two cancellations it was.
	if c.killed {
		reason = observe.ChildKilled
	}
	c.endReason = reason
	final := c.report
	c.mu.Unlock()
	// The seam at a child's end, for an attempt that ended any way but
	// answering: that one met it in the loop below, where a steer can
	// still keep it working. Here there is nothing left to carry on, so
	// what a hook answers is only what it said. An attempt a start hook
	// refused never started, and has no end for a hook to be told about.
	if state == StateFailed && started && c.env.Stop != nil {
		c.env.Stop(c.life(), final)
	}
	if state == StateFailed {
		// What the attempt wrote is kept before anything is torn down,
		// and before the handoff that names it is written.
		c.keepStoppedPatch(s.opts.Generators)
		// The detail stays the reason alone. The handle is on
		// Status.Handoff, and each surface says it as far as it has room:
		// a line that carried it would be trimmed back out by every
		// surface too narrow for an identifier.
		s.persistHandoff(c, reason, detail, c.endRound())
	}
	c.set(state, detail)
	// An ending is a claim released, so a writer queued behind it asks
	// again (awaitClaim).
	s.releaseClaims()
	ended := c.status()
	// A follow-up that answers again is the same ending said twice, so
	// only the first answer is filed; a follow-up that fails is an ending
	// of its own and is.
	c.mu.Lock()
	again := state == StateDone && c.answered
	if state == StateDone {
		c.answered = true
	}
	c.mu.Unlock()
	// The attempt says how it ended on its own record, once, here — the
	// one place every route out of the loop passes through. It goes to
	// the child's row and not the parent's because that is where the
	// attempt's model, its budget and what it spent already are, and a
	// budget is only answerable beside the spend it bounded
	// (docs/capabilities/sessions-and-memory.md#a-child-ends-for-a-reason).
	if !again {
		c.signalAt(c.endRound(), observe.SignalSubagent, reason)
	}
	return ended
}

// prepareAttempt readies an attempt that holds its slot: a writer's
// isolated checkout is opened, seeded and installed on the child, and the
// seam at the child's start is asked. ctx, cancel, maxRounds and attempt are
// the attempt's own, captured by run, and endRec is its record as run last
// read it. It returns the worktree and repository it installed (empty for
// a reader), the record to close on the way out, and false when the
// attempt ended here — finish already called, the returned values still
// the ones run's defers must release.
//
// It is called on the child's own goroutine with neither the supervisor's
// mutex nor the child's held. It takes c.mu itself around install and the
// read of the installed record (rec.End), in one hold.
func (s *Supervisor) prepareAttempt(c *child, ctx context.Context, cancel context.CancelFunc, maxRounds, attempt int,
	endRec func(observe.ChildEnd), finish func(State, string, string)) (worktree, repoTop string, rec func(observe.ChildEnd), ok bool) {
	rec = endRec
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
			return "", "", rec, false
		}
		c.mu.Lock()
		c.install(w)
		rec = c.rec.End
		c.mu.Unlock()
		worktree, repoTop = w.wt.dir, w.wt.repoTop
	}

	// The seam at the child's start, once it has everything it will run with
	// and before its first request: a hook that refuses it costs the child
	// nothing it has spent, and the refusal is what the parent reads for it.
	// What the seam can answer is a refusal and nothing else — no role, no
	// tool, no mode — so the child that runs is the child that was spawned.
	// See docs/capabilities/hooks.md#a-child-starts-and-ends-at-a-seam.
	if c.env.Start != nil {
		if refusal := c.env.Start(c.life()); refusal != "" {
			cancel()
			finish(StateFailed, observe.ChildHook, refusal)
			s.emitUpdate(c)
			return worktree, repoTop, rec, false
		}
	}
	return worktree, repoTop, rec, true
}

// newHeadless builds the run that drives the attempt's turns and installs
// it on the child. Its Resolve is where a child's gated calls meet the
// gating ladder: the containment refusal (refuseUncontained) in front of
// resolveGated, inside whatever the surface wraps around it (WrapGated).
// Every callback it wires reports to the child's lane and record.
//
// It is called on the child's own goroutine with neither the supervisor's
// mutex nor the child's held. It takes c.mu itself to write c.headless;
// the callbacks it installs run later, on the run's goroutine or (OnSummary)
// the reading's, and each takes c.mu itself around the child fields it
// reads or writes.
func (s *Supervisor) newHeadless(c *child) *agent.Headless {
	signal := func(code, reason string) {
		if c.rec.Signal != nil {
			c.rec.Signal(c.pos(), code, reason)
		}
	}
	// The gated tier's dispatcher, inside whatever the surface puts around
	// it. It is built once for the attempt rather than per call: the wrap is
	// a chain of closures, and a child's rounds are the last place to be
	// rebuilding one.
	//
	// The containment refusal stands in front of the wrap rather than inside
	// it, where a session answers its own: a call no decision can let run is
	// not handed to a seam that fires the person's pre-tool hook for it.
	// See docs/capabilities/containment.md#containment-can-be-required.
	gated := func(tc provider.ToolCall) string { return s.resolveGated(c, tc) }
	if c.env.WrapGated != nil {
		gated = c.env.WrapGated(c.seam(), gated)
	}
	resolve := func(tc provider.ToolCall) string {
		if refusal, refused := s.refuseUncontained(c, tc); refused {
			return refusal
		}
		return gated(tc)
	}
	compact := childCompactor(c.model, c.env)
	if compact != nil {
		compact.Carry = c.stepsCarried
	}
	if compact != nil && c.env.Compaction != nil {
		c.env.Compaction(c.life(), compact)
	}
	h := &agent.Headless{
		Agent:   c.agent,
		Compact: compact,
		// A child is as unwatched as a headless run, and its task is the
		// instruction every reading is judged against. Nil where
		// summary.subagents turned the reading off.
		Summary: agent.NewSummaryRun(c.env.Summarizer, agent.NewRecorder(0), c.task).
			WithChanges(c.changed).
			WithSteps(c.ownProgress).
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
			if !n.Unavailable {
				signal(observe.SignalTree, n.Signal())
			}
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
				// Already priced: each request was billed off its own cache
				// split as it came back, and the recorder's own fallback —
				// the pair charged whole at the fresh input rate — is what
				// the split is here to stop it reaching for.
				t := c.attemptSpend()
				c.rec.Usage(c.pos().Turn, t.In, t.Out, t.Cost, t.Priced)
			}
		},
		OnText: func(text string) {
			c.mu.Lock()
			c.streaming += text
			c.mu.Unlock()
			s.emitUpdate(c)
		},
		OnProgress: func(text string) {
			c.mu.Lock()
			// A status is buffered rather than streamed — the run holds it
			// back so a text-only answer stays the answer — so the words
			// arrive whole here instead of token by token through OnText.
			// They join the same buffer all the same, and settle into the
			// round's assistant entry through the same flush, marked as the
			// note they are: a lane a reader attaches to draws the child's
			// public status where the session draws its own, and a lane that
			// silently dropped it would leave the longest, quietest runs the
			// only ones with nothing to read
			// (docs/interface/surfaces.md#the-progress-checkpoint).
			c.streaming += text
			c.checkpointNext = true
			if note := handoffText(text); note != "" && len(c.progress) < maxHandoffProgress {
				c.progress = append(c.progress, note)
			}
			c.mu.Unlock()
			s.emitUpdate(c)
		},
		OnToolCall: func(tc provider.ToolCall) {
			c.beginToolEntry(tc.ID, tc.Name, tc.Arguments)
			at := c.pos()
			c.mu.Lock()
			c.round = at.Round
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
				c.rec.ToolCall(c.pos(), r.Call.Name, r.Duration, outcome, class,
					observe.ToolPurpose(r.Call.Name, r.Call.Arguments))
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
	return h
}

// afterTurn reads what one turn of the run ended on and acts on it. It
// returns the next turn's message with ok and not answered when the child
// carries on (steering, a cancelled turn that was steered again, a review's
// report directive, a check-in); answered when the child answered and
// finish has marked it done, for run to hand it a follow-up; and not ok
// when finish has ended the attempt. The answer is read last: the branches
// are exclusive, and each that ends or redirects a turn tests err first.
//
// It is called on the child's own goroutine with neither the supervisor's
// mutex nor the child's held. It takes c.mu itself to clear intPending, to
// read budgetHit, toolCalls and patchNote, and to write report, reporting,
// checkIns, maxRounds and listening. A cancelled turn waits here for
// steering (awaitSteering).
func (s *Supervisor) afterTurn(c *child, report string, err error, worktree string,
	endTurn func(string), finish func(State, string, string)) (next string, answered, ok bool) {
	// A cancel is for the turn it arrived in, and that turn is over.
	c.mu.Lock()
	c.intPending = false
	c.mu.Unlock()
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
		return "", false, false
	}

	if errors.Is(err, agent.ErrInterrupted) && c.ctx.Err() == nil {
		// Turn cancelled by the user: the conversation is already
		// well-formed (synthetic results); wait for steering or a kill.
		endTurn(observe.TurnCancelled)
		c.appendEntry(TranscriptEntry{Kind: EntrySystem, Text: "Turn cancelled — send a message to continue."})
		c.set(StateIdle, "idle · turn cancelled")
		s.emitUpdate(c)
		next, ok = s.awaitSteering(c)
		if !ok {
			finish(StateFailed, observe.ChildCancelled, "cancelled")
			return "", false, false
		}
		c.set(StateRunning, "running")
		s.emitUpdate(c)
		return next, false, true
	}

	if errors.Is(err, agent.ErrRoundCap) && c.ctx.Err() == nil && c.reviewPassOver() {
		// A review's cap is the end of its inspection, not a pause in
		// it. Every other child is asked what it will do next and given
		// more room to do it; a review has read its declared evidence
		// and is told to report on that, with only the rounds a report
		// needs. The conversation is well-formed at a cap — the last
		// round's results are recorded — so the directive is simply its
		// next turn.
		used := c.agent.Rounds()
		endTurn(observe.TurnCapPaused)
		// The allowance is the report turn's whole cap, not an increment
		// on the pass that just ended: a turn starts its round counter at
		// zero, so adding the rounds already spent would hand the report
		// a second pass the size of the first — twenty-three rounds of
		// unrestricted tool calls for a reviewer that had twenty.
		//
		// The child's own cap is left where the spawn set it. It is what
		// a retry starts from, and a retry is owed the inspection pass
		// again, not the three rounds this attempt had left to write with.
		c.agent.SetMaxRounds(reviewReportRounds)
		c.mu.Lock()
		c.reporting = true
		c.mu.Unlock()
		c.appendEntry(TranscriptEntry{Kind: EntrySystem,
			Text: fmt.Sprintf("Inspection pass over — %d rounds used. Reporting on the declared evidence.", used)})
		c.set(StateRunning, "reporting")
		s.emitUpdate(c)
		return reviewReportDirective, false, true
	}

	if errors.Is(err, agent.ErrRoundCap) && c.ctx.Err() == nil && !c.profile.Reviews {
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
		next = c.agent.CheckInMessage()
		c.set(StateRunning, fmt.Sprintf("running · check-in %d", n))
		s.emitUpdate(c)
		return next, false, true
	}

	if err != nil {
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
		return "", false, false
	}

	c.mu.Lock()
	c.report = report
	tools := c.toolCalls
	c.mu.Unlock()

	// Steering that arrived during the final stream becomes the next
	// turn instead of being dropped (the TUI's dispatchSteering
	// semantics).
	msgs := c.drainSteering()
	// The seam at the child's end, met here rather than on the way
	// out because here the child can still be kept: a hook that does
	// not take this answer as the end sends it back in through Steer,
	// the one door into a child's conversation, and the turn it opens
	// is the one below. It is asked only of an answer that is about to
	// be the end — a child with steering already waiting is not
	// ending — and each answer is asked afresh, so what bounds a hook
	// that never accepts one is the child's own budget.
	// See docs/capabilities/hooks.md#a-child-starts-and-ends-at-a-seam.
	if len(msgs) == 0 && c.env.Stop != nil {
		if steer := c.env.Stop(c.life(), report); steer != "" && s.Steer(c.name, steer, SteerFromHook) == nil {
			msgs = c.drainSteering()
		}
	}
	if len(msgs) > 0 {
		endTurn(observe.TurnDone)
		c.set(StateRunning, "running")
		s.emitUpdate(c)
		return strings.Join(msgs, "\n\n"), false, true
	}

	if c.profile.Writes {
		landed := s.reviewPatch(c)
		c.mu.Lock()
		note := c.patchNote
		c.mu.Unlock()
		if note != "" {
			c.appendEntry(TranscriptEntry{Kind: EntrySystem, Text: note})
		}
		if landed {
			// What landed is the parent's now, so it becomes the copy's
			// base: a follow-up's patch is then what the follow-up
			// wrote, and not this turn's change handed over twice.
			if err := commitBase(worktree, landedBaseMessage); err != nil {
				c.appendEntry(TranscriptEntry{Kind: EntrySystem,
					Text: "The copy's base could not be moved past the landed patch: " + firstLine(err.Error())})
			}
		}
	}

	endTurn(observe.TurnDone)
	c.mu.Lock()
	c.listening = true
	c.mu.Unlock()
	finish(StateDone, observe.ChildDone, "done · "+plural(tools, "tool"))
	return "", true, true
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

// awaitFollowUp is awaitSteering for a child that has answered, and the end
// of the time it can be spoken to: the mark Steer asks before it hands a
// finished child a new turn is set before the child says it is done — a
// caller that sees it done and steers at once must not find it deaf — and
// cleared here, once the wait is over either way.
func (s *Supervisor) awaitFollowUp(c *child) (string, bool) {
	next, ok := s.awaitSteering(c)
	c.mu.Lock()
	c.listening = false
	c.mu.Unlock()
	return next, ok
}
