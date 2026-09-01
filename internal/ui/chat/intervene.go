package chat

// Interrupting a turn that is not going to stop on its own.
//
// Two failures, two messages, one place they arrive.
//
// A turn can fail by going somewhere it was not asked to go, and it can fail
// by arriving and not noticing. The summarizer reads for both — a departure
// is off target, and a session that has what it needs is sufficient — and
// this file is what acts on those readings instead of only rendering them.
//
// The clock underneath is not replaced by either. A reading needs a
// summarizer that is configured, enabled and answering, and the sessions with
// none of that are exactly the ones with nobody watching either, so the
// check-in still fires on the interval for every turn that has no reading to
// go on. What a reading buys is timing: the same question, asked as soon as
// there is a reason to ask it rather than when the counter comes round.
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

// interveneCooldownIntervals is how many summary intervals must pass between
// two verdict-driven interventions. A reading stands for several rounds, and
// acting on each of them would be the same message three times while the turn
// is still acting on the first. Two intervals gives one intervention two
// readings to take effect before another is allowed.
const interveneCooldownIntervals = 2

// interveneKind is what a queued verdict has earned.
type interveneKind int

const (
	// interveneSteer: the run has left its instruction, and the message says
	// which instruction and why the reading thinks so.
	interveneSteer interveneKind = iota
	// interveneEnough: the run is still on its instruction and has what it
	// needs. The message is the ordinary check-in, arriving early — there is
	// nothing to accuse it of, only a question to ask sooner.
	interveneEnough
)

// interveneState is what the session knows about interrupting its own turn:
// the verdict it has decided to act on but not yet delivered, and the two
// marks that stop it acting twice.
type interveneState struct {
	// pending is the verdict waiting for a round boundary, or nil, and kind
	// is what it earned.
	pending *agent.SummaryVerdict
	kind    interveneKind
	// verdictRound is the reading already acted on, so one reading interrupts
	// once however long it stands.
	verdictRound int
	// lastRound is the tool round the last verdict-driven intervention was
	// delivered at, for the cooldown. Zero means none this turn.
	lastRound int
}

// startTurn scopes intervening to the turn beginning: a verdict about the last
// instruction must never be delivered against the next one, and the cooldown
// is measured in a round counter that has just gone back to zero.
func (s *interveneState) startTurn() {
	s.pending = nil
	s.kind = interveneSteer
	s.verdictRound = 0
	s.lastRound = 0
}

// considerIntervention decides whether a fresh reading is worth interrupting
// the turn for, and as what. It only ever queues: delivery is
// injectInterventions.
//
// SummaryOnTarget and SummaryUncertain do nothing. An intervention on a shrug
// is worse than no intervention, and the enum exists so that judgement is made
// by the reading rather than by parsing its prose.
func (m *Model) considerIntervention(v agent.SummaryVerdict) {
	if !m.working() {
		return
	}
	var kind interveneKind
	switch {
	case v.State.Drifting():
		kind = interveneSteer
	case v.State.Sufficient():
		kind = interveneEnough
	default:
		return
	}
	if v.Round == m.intervene.verdictRound {
		return // this reading has already had its say
	}
	if m.intervene.lastRound > 0 && m.agent.Rounds()-m.intervene.lastRound < m.interveneCooldown() {
		return
	}
	verdict := v
	m.intervene.pending = &verdict
	m.intervene.kind = kind
}

// interveneCooldown is the minimum rounds between two verdict-driven
// interventions, derived from the reading interval so it scales with whatever
// the summarizer is configured to.
func (m Model) interveneCooldown() int {
	return interveneCooldownIntervals * m.summaryInterval()
}

// injectInterventions delivers whichever mechanism is owed, at the round
// boundary. A reading wins over the clock: it is the same question asked for a
// reason, and asking both in one round is asking twice.
func (m *Model) injectInterventions() {
	if m.injectVerdict() {
		return
	}
	m.injectCheckIn()
}

// injectVerdict delivers a queued reading and reports whether it did.
func (m *Model) injectVerdict() bool {
	v := m.intervene.pending
	if v == nil {
		return false
	}
	kind := m.intervene.kind
	m.intervene.pending = nil
	m.intervene.verdictRound = v.Round
	m.intervene.lastRound = m.agent.Rounds()

	var content, notice, reason string
	if kind == interveneSteer {
		content = m.agent.TakeSteer(m.summaryTarget, v.Reason)
		notice = steerNotice(v.Reason)
		reason = "steer"
	} else {
		// The message is the ordinary check-in. What the reading bought is
		// the timing, not different words: the turn is not accused of
		// anything, it is asked the same question sooner.
		content = m.agent.ForceCheckIn()
		notice = enoughNotice(v.Reason)
		reason = "enough"
	}
	m.agent.Append(provider.Message{Role: provider.RoleUser, Content: content})
	m.appendEntry(entry{kind: entrySystem, text: notice})
	m.signal(signalIntervene, reason)
	m.syncViewport()
	return true
}

// enoughNotice is the transcript row for a check-in a reading pulled forward,
// so the reader can tell it from one the clock produced.
func enoughNotice(reason string) string {
	if reason == "" {
		return "Check-in — the session looked to have what it needs, so it was asked to take stock early."
	}
	return fmt.Sprintf("Check-in — %s. Asked to take stock early.", reason)
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
