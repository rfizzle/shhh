package subagent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/plan"
	"github.com/rfizzle/shhh/internal/provider"
)

// followUpRefusal is why a finished child cannot take a follow-up, or nil
// where it can. The caller holds c.mu.
//
// The admission floor is not asked again: the child's opening is already
// paid for and in its conversation. What is asked is the part of admission
// that still means something — whether what is left of the budget covers the
// working reserve a turn is admitted with — because a follow-up started on
// less stops for its budget part-way through and hands back a handoff where
// an answer was wanted.
func (s *Supervisor) followUpRefusal(c *child) error {
	if s.ctx.Err() != nil {
		return ErrClosed
	}
	if !c.listening || c.ctx.Err() != nil {
		return fmt.Errorf("agent %s has finished (done) and can no longer be spoken to; nothing to steer", c.name)
	}
	if c.maxTokens > 0 {
		if left := c.maxTokens - c.fresh; left < MinChildMaxTokens {
			return fmt.Errorf("agent %s cannot take a follow-up: ~%s of its ~%s new-token budget is left, under the %d-token working reserve a turn is admitted with; spawn a new agent for it", c.name, formatTokens(max(left, 0)), formatTokens(c.maxTokens), MinChildMaxTokens)
		}
	}
	return nil
}

// claimFollowUp turns a finished child into one working on a follow-up. The
// caller holds c.mu and has seen the child done.
//
// It is claimed where the message is sent rather than where the child's
// goroutine wakes to it, so the state, the report a wait returns and the
// channel a wait waits on all move in the one step: a caller that steers and
// then asks for the report in the same breath waits for the follow-up's
// answer rather than being handed the answer it was following up.
//
// The report the child had given is kept, headed with the turn it closed,
// and cleared from the field the next report is written into: a follow-up
// that fails must not be read as having answered with its predecessor's
// words.
func (c *child) claimFollowUp(text string) {
	if c.report != "" {
		c.earlier = append(c.earlier, EarlierReport{Turn: c.turns, Text: c.report})
	}
	c.report, c.patchNote = "", ""
	// A follow-up is a new ask the list was not written for, so it starts
	// with none and names its own.
	c.own = plan.Checklist{}
	c.followUp = followUpWords(text)
	c.done = make(chan struct{})
	c.state, c.detail = StateRunning, followUpDetail(c.followUp)
	c.ended = time.Time{}
}

// followUpWordsMax bounds how much of a follow-up its lane quotes: the words
// that say which question this is, not the question.
const followUpWordsMax = 48

// followUpWords is the first line of a follow-up, bounded for a row.
func followUpWords(text string) string {
	line := firstLine(strings.TrimSpace(text))
	if r := []rune(line); len(r) > followUpWordsMax {
		line = strings.TrimSpace(string(r[:followUpWordsMax-1])) + "…"
	}
	return line
}

// followUpDetail is the lane's detail while a follow-up runs, before its
// first call replaces it with a count.
func followUpDetail(words string) string {
	return "running · follow-up · " + words
}

// turnDone is the channel a wait on this child waits on: closed when the
// turn it is working on answers. A follow-up replaces it, which is why it is
// read under the lock rather than off the field.
func (c *child) turnDone() <-chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.done
}

// finalCheckInTimeout bounds the handoff completion. It is short on purpose:
// the child is over its budget and on its way out either way, and a handoff
// that takes longer than this is worth less than the delay it adds before the
// parent hears that the child failed.
const finalCheckInTimeout = 30 * time.Second

// finalCheckInPrompt asks a child that ran out of budget to hand over. It
// does not ask for more work, and says so: the child has nothing left to
// spend, and a handoff that starts another edit is worse than none.
const finalCheckInPrompt = `You have reached your token budget and are stopping now. Do not start any new work or call any tools.

Write a short handoff for whoever picks this up: what you established or changed, what is left, and what you would do next.`

// finalCheckIn asks a child that exhausted its token budget to say where it
// got to, and records the answer as its report. The budget stays a
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
	hit, root, model, paths, attempt := c.budgetHit, c.root, c.model, c.paths, c.attempt
	worktree := c.worktree != ""
	c.mu.Unlock()
	if !hit {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), finalCheckInTimeout)
	defer cancel()
	env, err := s.opts.NewEnv(ctx, Spec{Name: c.name, Role: c.role, Root: root, Model: model, Paths: paths,
		Parent: c.parent, Depth: c.depth, Worktree: worktree, Attempt: attempt})
	if err != nil {
		return
	}
	msgs := append(c.agent.RequestMessages(),
		provider.Message{Role: provider.RoleUser, Content: finalCheckInPrompt})
	// The prompt tells the child not to call a tool and the request enforces
	// it, because nothing here reads a tool call: a handoff that arrived as
	// one leaves the builder below empty and the parent with no report at
	// all, which is the failure this whole call exists to prevent.
	events, stop, err := env.Stream(msgs, provider.ToolChoiceNone)
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
// wording every path that stops for the budget uses. It says "new" because
// the spend beside it on the same row is the billed figure, which a cached
// prompt puts well above the budget without ever reaching it (addUsage).
func budgetReason(c *child) string {
	return fmt.Sprintf("failed · token budget (~%s new) exceeded · %s",
		formatTokens(c.maxTokens), wroteNote(len(c.written())))
}

// wroteNote is what a child had to show for a budget when the budget ran out.
//
// It is on the failure line and not only in the record because that line is
// what the parent reads, and the two endings behind one budget failure want
// different answers from it: a child that ran out mid-edit is worth retrying
// on the work it left, and one that ran out having written nothing spent a
// whole budget on reading and wants a narrower task instead. A spend figure
// alone cannot tell them apart.
func wroteNote(files int) string {
	if files == 0 {
		return "nothing written"
	}
	return fmt.Sprintf("%d %s written", files, plural(files, "file"))
}

// childEndReason is the same fork failReason takes, in the record's closed
// vocabulary. The two are written together and read the same fields on
// purpose: a lane that says one thing and a row that says another about the
// same attempt is the failure the closed set exists to prevent, and the only
// way they stay in step is being one decision.
//
// A provider failure is separated from everything else because it is the one
// end nobody chose: a budget is a number somebody set, a cap is a number
// somebody set, a kill is somebody pressing a key, and a run that stops
// because the provider stopped answering says nothing about any of them.
// Folded together they would all read as the fan-out being tuned wrong.
func childEndReason(c *child, err error) string {
	c.mu.Lock()
	budgetHit := c.budgetHit
	c.mu.Unlock()
	switch {
	case budgetHit:
		return observe.ChildBudget
	case errors.Is(err, agent.ErrRoundCap):
		return observe.ChildCap
	case c.ctx.Err() != nil:
		return observe.ChildCancelled
	}
	if _, ok := provider.AsFailure(err); ok {
		return observe.ChildProvider
	}
	return observe.ChildFailed
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
