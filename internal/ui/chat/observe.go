package chat

// Session observability: the Model's adaptation to the observer contract in
// internal/observe. The codes and the closed sets live there, because every
// surface reports the same ones; what lives here is where the model's own
// accounting — the turn, the round, the ledger, the close state — is read
// off to fill them in.

import (
	"time"

	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// WithObserver wires session observability; the zero Observer
// disables it.
func (m Model) WithObserver(o observe.Observer) Model {
	m.observer = o
	return m
}

// pos is where the session is now.
func (m Model) pos() observe.Pos {
	return observe.Pos{Turn: m.turnCount, Round: int64(m.agent.Rounds())}
}

// notifyUsage reports what the whole session has spent — the agent's turns,
// the classifier, the summary and every sub-agent — from the ledger the
// provider gate fills. Without a ledger there is only the agent's own
// accounting to report, which is what a session assembled without one has.
func (m *Model) notifyUsage() {
	if m.observer.Usage == nil {
		return
	}
	if m.ledger == nil {
		cost, priced := m.usageTotalCost(m.TotalTokensIn, m.TotalTokensOut)
		m.observer.Usage(m.turnCount, m.TotalTokensIn, m.TotalTokensOut, cost, priced)
		return
	}
	t := m.ledger.Total()
	m.observer.Usage(m.turnCount, t.In, t.Out, t.Cost, t.Priced)
}

// usageTotalCost prices a token pair against the session's current model, for
// the ledgerless case.
func (m Model) usageTotalCost(in, out int64) (float64, bool) {
	if m.prices == nil || m.modelName == "" {
		return 0, false
	}
	inCost, outCost, found := m.prices.Cost(m.modelName, in, out)
	if !found {
		return 0, false
	}
	return inCost + outCost, true
}

// recordToolResult records a tool call from its result text: the outcome
// and, for a failure, its class.
func (m *Model) recordToolResult(tool string, duration time.Duration, result string) {
	outcome, class := observe.ToolOutcome(result)
	m.recordToolEvent(tool, duration, outcome, class)
}

func (m *Model) recordToolEvent(tool string, duration time.Duration, outcome, class string) {
	if m.observer.ToolCall != nil {
		m.observer.ToolCall(m.pos(), tool, duration, outcome, class)
	}
}

func (m *Model) recordDecision(decision, reason string) {
	if m.observer.Decision != nil {
		m.observer.Decision(m.pos(), decision, reason)
	}
}

// recordTurn reports the turn that is closing. It runs from the one place
// every turn ends (appendTurnClose), so no turn can end unrecorded.
func (m *Model) recordTurn(outcome string) {
	if m.observer.Turn == nil {
		return
	}
	var elapsed time.Duration
	if !m.turnStarted.IsZero() {
		end := m.turnEnded
		if end.IsZero() {
			end = time.Now()
		}
		elapsed = end.Sub(m.turnStarted)
	}
	m.observer.Turn(m.turnCount, int64(m.agent.Rounds()), elapsed, outcome)
}

// turnOutcomeCode is the turn's close state as the recorder's closed set.
func (m Model) turnOutcomeCode() string {
	if m.pausedAtRoundLimit() {
		return observe.TurnCapPaused
	}
	switch m.turnOutcome {
	case components.TurnCancelled:
		return observe.TurnCancelled
	case components.TurnFailed:
		return observe.TurnFailed
	}
	return observe.TurnDone
}

func (m *Model) signal(code, reason string) {
	if m.observer.Signal != nil {
		m.observer.Signal(m.pos(), code, reason)
	}
}
