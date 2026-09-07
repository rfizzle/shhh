package chat

// Delivering an interruption the agent decided on.
//
// The decision — steer, early check-in, or the interval's own — is
// agent.NextIntervention, shared with every headless run and every sub-agent
// (internal/agent/intervene.go). What is left here is the two things a
// session does that a background run does not: it shows the reader what
// happened, and it records the signal.

import (
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/provider"
)

// considerVerdict offers a fresh reading to the agent's policy. It only ever
// queues; the round boundary delivers.
//
// Both bounds are counted in the interval in force rather than the configured
// one: a session backing off from a failing summariser reads half as often,
// and a cooldown that did not widen with it would let two interventions land
// on consecutive readings.
//
// A reading is judged for age where it is applied, which for a session is the
// moment it lands rather than a later boundary — there is nowhere else it
// waits. An interruption withheld because the reading described a round the
// session has long since passed is recorded and nothing more: no message
// joins the conversation, so there is nothing to show the reader, and the row
// on the rail is already the reading itself.
func (m *Model) considerVerdict(v agent.SummaryVerdict) {
	m.agent.SetInterveneBounds(m.summaryInterval(), m.summarizer.Config().CooldownIntervals())
	if reason := m.agent.ConsiderVerdict(v, m.agent.Rounds(), m.working()); reason != "" {
		m.signal(observe.SignalIntervene, reason)
	}
}

// WithSteering installs the interruption machinery's tuning: the thresholds
// and the wordings the config file overrode, or a zero value for the
// built-in set.
func (m Model) WithSteering(s agent.Steering) Model {
	m.agent.SetSteering(s)
	return m
}

// injectInterventions delivers whatever the round boundary owes, and shows it.
func (m *Model) injectInterventions() {
	iv, ok := m.agent.NextIntervention(m.summaryTarget)
	if !ok {
		return
	}
	m.agent.Append(provider.Message{Role: provider.RoleUser, Content: iv.Message})
	m.appendEntry(entry{kind: entrySystem, text: iv.Notice})
	m.signal(observe.SignalIntervene, iv.Kind.Signal())
	// Written down where the reading schedule can see it (summary.go): a
	// turn that ends on the model's answer to this message still closes on a
	// fresh reading even though no further round was taken, the next reading
	// falls due a few rounds from here rather than a whole interval away,
	// and the reader taking it is told what was said instead of being handed
	// the evidence that earned it and asked to revise its own verdict.
	m.summary.noteIntervention(iv, m.agent.Rounds())
	// A row was appended, so the pane is redrawn the way every other system
	// row is; the resize hook alone would leave it unseen until the next
	// stream flush.
	m.viewport.SetLines(m.renderHistoryLines())
	m.viewport.GotoBottom()
}
