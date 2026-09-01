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
	"github.com/rfizzle/shhh/internal/provider"
)

// interveneCooldownIntervals is how many summary intervals must pass between
// two verdict-driven interventions. Two gives one intervention two readings
// to take effect before another is allowed.
const interveneCooldownIntervals = 2

// considerVerdict offers a fresh reading to the agent's policy. It only ever
// queues; the round boundary delivers.
func (m *Model) considerVerdict(v agent.SummaryVerdict) {
	m.agent.SetInterveneCooldown(interveneCooldownIntervals * m.summaryInterval())
	m.agent.ConsiderVerdict(v, m.working())
}

// injectInterventions delivers whatever the round boundary owes, and shows it.
func (m *Model) injectInterventions() {
	iv, ok := m.agent.NextIntervention(m.summaryTarget)
	if !ok {
		return
	}
	m.agent.Append(provider.Message{Role: provider.RoleUser, Content: iv.Message})
	m.appendEntry(entry{kind: entrySystem, text: iv.Notice})
	m.signal(signalIntervene, iv.Kind.Signal())
	m.syncViewport()
}
