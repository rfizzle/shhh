package chat

// Interrupting a turn that is not going to stop on its own.
//
// Two failures, two mechanisms, one place they arrive.
//
// A turn can fail by going somewhere it was not asked to go, and it can fail
// by arriving and not noticing. The summarizer catches the first: it already
// reads every few rounds whether the run still serves the instruction it
// started from, and this file is what finally acts on that verdict instead of
// only rendering it. Nothing catches the second by evidence — a session that
// spends a hundred rounds reading files in service of the instruction is on
// target at every reading — so the check-in catches it by the clock, asks a
// generic question, and costs nothing but the round it takes.
//
// Both arrive at the round boundary, because that is the only place a message
// may join a conversation: an assistant message carrying tool calls must be
// followed by a result for every one of its ids before any user message is
// legal, and a round now dispatches several at once. So a verdict that lands
// mid-round waits here until the round closes.
//
// Neither resets the round counter. injectSteering does, because a person
// typing into a running turn is asking for it to continue; an automatic
// mechanism doing the same would quietly postpone the round cap, which is the
// checkpoint the person is there for. What they do reset is each other: a
// steer is a check-in with better evidence, so it counts as one.
// See docs/capabilities/coding-agent.md#two-failures-two-interruptions.

import (
	"fmt"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/provider"
)

// steerCooldownIntervals is how many summary intervals must pass between two
// automatic steers. A drifting reading can stand for several readings, and
// steering on each of them would be the same message three times while the
// turn is still acting on the first. Two intervals gives a steer two readings
// to take effect before another is allowed.
const steerCooldownIntervals = 2

// steerState is what the session knows about its own steering: the reading it
// has decided to act on but not yet delivered, and the two marks that stop it
// acting twice.
type steerState struct {
	// pending is the verdict waiting for a round boundary, or nil.
	pending *agent.SummaryVerdict
	// verdictRound is the reading already acted on, so one reading steers
	// once however long it stands.
	verdictRound int
	// lastRound is the tool round the last steer was delivered at, for the
	// cooldown. Zero means none this turn.
	lastRound int
}

// startTurn scopes steering to the turn beginning: a verdict about the last
// instruction must never be delivered against the next one, and the cooldown
// is measured in a round counter that has just gone back to zero.
func (s *steerState) startTurn() {
	s.pending = nil
	s.verdictRound = 0
	s.lastRound = 0
}

// considerSteer decides whether a fresh reading is worth interrupting the
// turn for. It only ever queues: delivery is injectInterventions.
//
// SummaryUncertain never steers. An intervention on a shrug is worse than no
// intervention, and the enum exists so that judgement is made by the reading
// rather than by parsing its prose.
func (m *Model) considerSteer(v agent.SummaryVerdict) {
	if !m.working() || !v.State.Drifting() {
		return
	}
	if v.Round == m.steer.verdictRound {
		return // this reading has already had its say
	}
	if m.steer.lastRound > 0 && m.agent.Rounds()-m.steer.lastRound < m.steerCooldown() {
		return
	}
	verdict := v
	m.steer.pending = &verdict
}

// steerCooldown is the minimum rounds between two steers, derived from the
// reading interval so it scales with whatever the summarizer is configured to.
func (m Model) steerCooldown() int {
	return steerCooldownIntervals * m.summaryInterval()
}

// injectInterventions delivers whichever of the two mechanisms is owed, at
// the round boundary. A steer wins: it is the same question with an actual
// reason attached, and asking both in one round is asking twice.
func (m *Model) injectInterventions() {
	if m.injectSteer() {
		return
	}
	m.injectCheckIn()
}

// injectSteer delivers a queued drift verdict and reports whether it did.
func (m *Model) injectSteer() bool {
	v := m.steer.pending
	if v == nil {
		return false
	}
	m.steer.pending = nil
	m.steer.verdictRound = v.Round
	m.steer.lastRound = m.agent.Rounds()

	m.agent.Append(provider.Message{
		Role:    provider.RoleUser,
		Content: m.agent.TakeSteer(m.summaryTarget, v.Reason),
	})
	m.appendEntry(entry{kind: entrySystem, text: steerNotice(v.Reason)})
	m.signal(signalIntervene, "steer")
	m.syncViewport()
	return true
}

// steerNotice is the transcript row. The steer is visible and attributed on
// purpose: a message the reader did not write, changing what their agent
// does, is how a transcript stops being something they can trust.
func steerNotice(reason string) string {
	if reason == "" {
		return "Steered — the session looked off target, so it was asked to check its work against the instruction."
	}
	return fmt.Sprintf("Steered — %s. The session was asked to check its work against the instruction.", reason)
}

// injectCheckIn asks the turn to take stock and carries straight on. It is
// steering the session gives itself: the same message a reader sends when
// they ask whether it has enough yet, at a point the reader should not have
// to be watching for.
func (m *Model) injectCheckIn() {
	prompt, ok := m.agent.TakeCheckIn()
	if !ok {
		return
	}
	m.agent.Append(provider.Message{Role: provider.RoleUser, Content: prompt})
	m.appendEntry(entry{kind: entrySystem, text: fmt.Sprintf(
		"Check-in — %d rounds used. Taking stock, then carrying on.", m.agent.Rounds())})
	m.signal(signalIntervene, "check-in")
	m.syncViewport()
}
