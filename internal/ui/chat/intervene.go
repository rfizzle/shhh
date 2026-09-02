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
// The cooldown is counted in reading intervals rather than rounds, and in the
// interval in force rather than the configured one: a session backing off
// from a failing summariser reads half as often, and a cooldown that did not
// widen with it would let two interventions land on consecutive readings.
func (m *Model) considerVerdict(v agent.SummaryVerdict) {
	m.agent.SetInterveneCooldown(m.summarizer.Config().CooldownIntervals() * m.summaryInterval())
	m.agent.ConsiderVerdict(v, m.working())
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
	m.syncViewport()
}
