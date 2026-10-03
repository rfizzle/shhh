package subagent

import (
	"context"
	"fmt"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/meter"
	"github.com/rfizzle/shhh/internal/nudge"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/provider"
)

// steerReceipt is the row a person's steer leaves on the child's lane at the
// boundary that takes it. It states the round because that is the whole of
// what the reader is waiting to know: a redirect typed three rounds ago and
// one taken up just now are the same words on the same lane, and only the
// round tells them apart.
func steerReceipt(round int) string {
	return fmt.Sprintf("↳ steer delivered · round %d", round)
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
// transcript as a user entry (they join the conversation now), counting each
// against the party that sent it, and recording each in the session record by
// its source.
//
// The record is written here, where the message reaches the child, rather
// than where it was queued: the position an event is placed at is read off
// the child's own agent, which is a passive state machine only the child's
// goroutine may touch, and every caller of this is that goroutine. What goes
// in is the source and nothing else — the message is the parent's or the
// person's own words, and the record holds no content.
//
// A person's own steer also leaves a receipt row beside the message. What
// they typed went into a queue and joined the conversation some rounds later,
// and until this row lands the lane shows a message with no answer under it
// and no way to tell a redirect the child has read from one still waiting.
// The orchestrator's needs none: it is told at the tool call, and its own
// steer is not a thing it sits watching a lane for. The check's own
// interruption already leaves one (OnIntervene).
func (c *child) drainSteering() []string {
	c.mu.Lock()
	queued := c.steering
	c.steering = nil
	// The round the receipt states, off the agent this goroutine drives —
	// the same counter pos reads. Nil is a child with no attempt running,
	// which has nothing queued to drain.
	round := 0
	if c.agent != nil {
		round = c.agent.Rounds()
	}
	msgs := make([]string, 0, len(queued))
	for _, q := range queued {
		msgs = append(msgs, q.text)
		c.transcript = append(c.transcript, TranscriptEntry{Kind: EntryUser, Text: q.text})
		switch q.from {
		case SteerFromParent:
			c.parentSteers++
			continue
		case SteerFromLanding:
			// Nobody's message, and so nobody's share and no receipt: the
			// machinery wrote it at the boundary it is delivered at, and the
			// source on the status is what names it.
			continue
		}
		c.laneSteers++
		c.transcript = append(c.transcript,
			TranscriptEntry{Kind: EntrySystem, Text: steerReceipt(round)})
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
	if c.intPending {
		close(c.intCh)
		c.intClosed = true
		c.intPending = false
	}
	c.turns++
	c.round = 0
	// A turn is steered about the instruction it was given. The next one has
	// a new instruction — a person's redirection through the lane, usually
	// the answer to the very count this carries — and starting it on the last
	// one's tally would report a child as ignoring a steer it has just been
	// taken off. Every party's share goes with it: what the turn before was
	// told is that turn's, whoever said it.
	c.steers, c.laneSteers, c.parentSteers = 0, 0, 0
	c.mu.Unlock()
}

// pos is where the child is now: the turn it is on and the tool round within
// it, read off the same counters its status is.
//
// Only the goroutine running the child may call it. The round comes off the
// agent, which is a passive state machine that goroutine drives and no lock
// guards; anything raised from elsewhere — a reading that lands on its own
// goroutine — states the round it is about and takes signalAt instead.
// nudgeTurn is the attempt's record of the tools its shell reads have been
// pointed at, made on first use. The mutex is the child's, because a retry
// clears it from the supervisor's goroutine.
func (c *child) nudgeTurn() *nudge.Turn {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.nudges == nil {
		c.nudges = &nudge.Turn{}
	}
	return c.nudges
}

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
	tokens := observe.ChildTokens{
		Inherited: c.inheritedTokens,
		Setup:     c.setupTokens,
		Tools:     c.toolResultTokens,
		Handoff:   estimateReportTokens(c.report),
		Fresh:     c.fresh,
	}
	tokens.Analysis = max(c.fresh-tokens.Inherited-tokens.Setup-tokens.Tools-tokens.Handoff, 0)
	return observe.ChildEnd{
		Reason: c.endReason, Verdict: c.verdictCode,
		Steers: c.steersAll, Attempt: c.attempt,
		Budget: c.maxTokens, AdmissionFloor: c.admissionFloor, Tokens: tokens,
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

// interruptTurnLocked closes the current turn's interrupt channel
// (idempotent), unblocking any approval wait, and keeps the cancel for the
// turn beginTurn arms next in case this one is not armed yet. The caller
// holds c.mu.
func (c *child) interruptTurnLocked() {
	if c.intCh != nil && !c.intClosed {
		close(c.intCh)
		c.intClosed = true
	}
	c.intPending = true
}

// interruptible is the child's stream with a cancelled turn's interrupt
// delivered again at every request. Headless.Run clears an interrupt as it
// starts a turn, so one that arrived between beginTurn and that reset would be
// lost and the turn would run on as if nobody had asked; the request is the
// first point after the reset the child can answer at, and the runner checks
// for an interrupt as soon as the stream is open. The request itself is never
// made — a cancelled turn has nothing to ask the provider.
func (c *child) interruptible(open agent.StreamFunc) agent.StreamFunc {
	return func(msgs []provider.Message, choice string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		c.mu.Lock()
		cancelled, h := c.intClosed, c.headless
		c.mu.Unlock()
		if cancelled && h != nil {
			h.Interrupt()
			ch := make(chan provider.StreamEvent)
			close(ch)
			return ch, func() {}, nil
		}
		return open(msgs, choice)
	}
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

// addUsage bills one request to the child and reports whether the child's
// token budget is now exceeded.
//
// The request is priced where it arrives, against the child's own model and
// off its own cache split, because that is the only place both are in hand:
// by the time the totals reach a recorder they are a sum, and a sum can only
// be charged at the fresh input rate. The classifier's rounds are billed here
// too, at the child's model rather than the classifier's own — they already
// count against the child's budget, and the model they ran on is not
// something the verdict carries.
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
	c.spend.Record(meter.Origin{Source: meter.SourceSubagent, Label: c.name}, c.model, *u)
	c.fresh += int64(max(u.PromptTokens-u.CachedTokens, 0)) + int64(u.CompletionTokens)
	if c.maxTokens > 0 && c.fresh > c.maxTokens {
		c.budgetHit = true
		return true
	}
	return false
}
