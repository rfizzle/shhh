package subagent

import (
	"errors"
	"fmt"
	"strings"

	wtree "github.com/rfizzle/shhh/internal/subagent/worktree"
)

// landing is a patch that landed in the parent's checkout, and which writer
// it was.
type landing struct {
	from  string
	patch string
}

// Note appends a front-end entry (scoped command output, mode changes) to a
// child's transcript so it survives attach/detach.
func (s *Supervisor) Note(name string, e TranscriptEntry) error {
	c, err := s.lookup(name)
	if err != nil {
		return err
	}
	if s.isClosed() {
		return ErrClosed
	}
	c.appendEntry(e)
	s.emitUpdate(c)
	return nil
}

// Steer queues a message for a child (steering semantics): injected
// before its next stream request when running, starting a fresh turn when
// the child is idle after a cancelled turn, and starting a follow-up turn on
// its own conversation when the child has finished. A failed child cannot be
// steered: running it again is Retry's, on a fresh conversation.
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
	if s.isClosed() {
		return ErrClosed
	}
	// A writer asked something new writes again, so its claim has to be one
	// no live writer has taken in the meantime. It is asked before the child
	// is locked, because the check reads every child's status.
	if st := c.status(); st.State == StateDone && c.profile.Writes {
		if holder, claim, conflict := s.claimConflict(st.Paths, c.overlap); conflict {
			return fmt.Errorf("agent %s cannot take a follow-up: %s now holds %s, which overlaps its paths", name, holder, claim)
		}
	}
	c.mu.Lock()
	switch c.state {
	case StateFailed:
		c.mu.Unlock()
		return fmt.Errorf("agent %s has finished (failed); nothing to steer — agent_retry runs it again on its task", name)
	case StateDone:
		if err := s.followUpRefusal(c); err != nil {
			c.mu.Unlock()
			return err
		}
		c.claimFollowUp(text)
	}
	c.steering = append(c.steering, queuedSteer{text: text, from: from})
	if c.steered != nil {
		close(c.steered)
		c.steered = nil
	}
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

// SessionSteering is the session telling the supervisor how many steering
// messages wait to join its own conversation. A count that grows ends every
// agent_report wait the session is inside, and a wait that starts while it is
// above zero returns at once: the redirect is read at the next round, which
// is after the tool call, and a wait on the slowest child would otherwise
// hold it there.
// See docs/capabilities/subagents.md#a-wait-only-ever-points-down-the-tree.
func (s *Supervisor) SessionSteering(queued int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if queued > s.sessionQueued && s.sessionSteered != nil {
		close(s.sessionSteered)
		s.sessionSteered = nil
	}
	s.sessionQueued = queued
}

// steerSignal is what a wait by caller watches for a steer: a channel closed
// when the caller is next steered, and whether a steer already waits in its
// queue — read together, under the lock that writes both, so a steer cannot
// land between the check and the wait and be missed.
func (s *Supervisor) steerSignal(caller string) (<-chan struct{}, bool) {
	if caller == "" {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.sessionSteered == nil {
			s.sessionSteered = make(chan struct{})
		}
		return s.sessionSteered, s.sessionQueued > 0
	}
	c, err := s.lookup(caller)
	if err != nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.steered == nil {
		c.steered = make(chan struct{})
	}
	return c.steered, len(c.steering) > 0
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
//
// It is also where a landed patch reaches a live writer (reseed): the round
// tail is the one moment nothing is being asked of the child's model and no
// call of its own is writing, so it is the one moment its tree may move.
func (s *Supervisor) holdFor(c *child) <-chan struct{} {
	s.reseed(c)
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

// queueLanding hands a patch that has just landed in the parent's checkout to
// every other live writer whose copy was taken from that checkout, to be
// carried in at each one's own next round boundary. A writer still queued has
// no copy yet, and takes the tree with this patch in it when it starts; a
// failed one's copy is gone or going.
func (s *Supervisor) queueLanding(from, repoTop, patch string) {
	s.mu.Lock()
	kids := append([]*child(nil), s.children...)
	s.mu.Unlock()
	for _, k := range kids {
		if k.name == from || !k.profile.Writes {
			continue
		}
		k.mu.Lock()
		if k.worktree != "" && k.repoTop == repoTop && k.state != StateFailed {
			k.landings = append(k.landings, landing{from: from, patch: patch})
		}
		k.mu.Unlock()
	}
}

// reseed carries every patch that has landed since the child's last boundary
// into its copy of the checkout, on the child's own goroutine at its round
// tail — the hold's boundary, and never mid-round. The child is parked for
// the moment it takes and reads `reseeding` while it is. A patch that carries
// is silent to the child's model; one that meets the child's own work is not
// forced: the copy is left as it was and the child is steered with where the
// two met, from the landing, through the one door into its conversation.
//
// A review's report turn is left alone. It is reporting on the evidence it
// was handed, and its tree moving under the report would make the report
// about a different change; a writer never has one.
// See docs/capabilities/subagents.md#a-writer-starts-from-your-tree.
func (s *Supervisor) reseed(c *child) {
	c.mu.Lock()
	// A writer on the last step of its own plan is left alone as well: it is
	// finishing the change the patch will carry, and moving the tree under
	// that is the collision the reseed exists to avoid, spent where it costs
	// most. What landed meanwhile stays queued, and the merge at its own
	// landing is what meets it (docs/capabilities/subagents.md#how-far-along-is-three-numbers-not-one).
	if len(c.landings) == 0 || c.worktree == "" || c.reporting || c.nearDone() {
		c.mu.Unlock()
		return
	}
	pending, worktree := c.landings, c.worktree
	c.landings = nil
	c.reseeding = pending[0].from
	c.mu.Unlock()
	s.emitUpdate(c)
	carried := 0
	for _, l := range pending {
		c.mu.Lock()
		c.reseeding = l.from
		c.mu.Unlock()
		regen, err := wtree.ReseedWorktree(c.ctx, worktree, l.patch, s.opts.Generators)
		if err == nil {
			carried++
			text := "↳ " + l.from + "'s landed patch carried into this copy · " + wtree.PatchPaths(wtree.PatchFiles(l.patch))
			if len(regen.Ran) > 0 {
				text += " · regenerated by " + strings.Join(regen.Ran, ", ")
			}
			if regen.Failed != nil {
				text += " · the generated files hold the landed text: " + firstLine(regen.Failed.Error())
			}
			c.appendEntry(TranscriptEntry{Kind: EntrySystem, Text: text})
			continue
		}
		var clash *wtree.ReseedCollision
		if !errors.As(err, &clash) {
			// git itself would not do it. The copy is as it was either way,
			// so the child is told the same thing a collision tells it.
			landed := wtree.PatchFiles(l.patch)
			clash = &wtree.ReseedCollision{Landed: landed, Files: landed, Reason: firstLine(err.Error())}
		}
		_ = s.Steer(c.name, landingSteer(l.from, clash), SteerFromLanding)
	}
	c.mu.Lock()
	c.reseeding = ""
	c.reseeds += carried
	c.mu.Unlock()
	s.emitUpdate(c)
}

// landingSteer is what a writer is told when a landed patch will not carry
// into its copy: which writer landed what, where it met the writer's own
// work, and what that means for the writer's own patch. It is advice and
// carries no authority — the writer's task is unchanged.
func landingSteer(from string, clash *wtree.ReseedCollision) string {
	return fmt.Sprintf("%s's patch has landed in the real checkout (%s) and could not be carried into your copy: it collides with your copy over %s. "+
		"Your copy still has the older text there, so your own patch to those files will be reviewed against a checkout that has moved. "+
		"Carry on with your task, and name those files in your final report so the reviewer knows where your change meets %s's.",
		from, wtree.PatchPaths(clash.Landed), wtree.PatchPaths(clash.Files), from)
}
