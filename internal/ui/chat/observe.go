package chat

// Session observability: the Model's adaptation to the observer contract in
// internal/observe. The codes and the closed sets live there, because every
// surface reports the same ones; what lives here is where the model's own
// accounting — the turn, the round, the ledger, the close state — is read
// off to fill them in.

import (
	"time"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// WithObserver wires session observability; the zero Observer
// disables it.
func (m Model) WithObserver(o observe.Observer) Model {
	m.observer = o
	return m
}

// timing is what the session times for the record beyond the observer's
// events: the running turn's split by what it waited on, and who is told
// the first frame has been drawn. They are one value because both are the
// record's and neither is any mode's.
type timing struct {
	// turn splits the running turn's time (agent.TurnClock).
	turn agent.TurnClock
	// firstPaint is told when a frame with the prompt in it is drawn, for
	// the startup row that measures how long the person waited for one;
	// nil tells nobody.
	firstPaint func()
}

// WithFirstPaint is told when the first frame with the prompt in it has
// been drawn. It is called from every such frame and has to answer only the
// first, which is the caller's to arrange: View is a value receiver, and the
// model has nowhere of its own to remember that it already said so.
func (m Model) WithFirstPaint(f func()) Model {
	m.timing.firstPaint = f
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
// and, for a failure, its class — and, for a command, what it was for.
func (m *Model) recordToolResult(call provider.ToolCall, duration time.Duration, result string) {
	outcome, class := observe.ToolOutcome(result)
	m.recordToolEvent(call.Name, duration, outcome, class, observe.ToolPurpose(call.Name, call.Arguments))
}

func (m *Model) recordToolEvent(tool string, duration time.Duration, outcome, class, purpose string) {
	if m.observer.ToolCall != nil {
		m.observer.ToolCall(m.pos(), tool, duration, outcome, class, purpose)
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
	var end time.Time
	if !m.turnStarted.IsZero() {
		end = m.turnEnded
		if end.IsZero() {
			end = time.Now()
		}
		elapsed = end.Sub(m.turnStarted)
	}
	// The split goes with the turn where the clock timed this turn: it was
	// begun at the same stamp the elapsed is measured from and is read at the
	// same end, so its four parts are the elapsed and not an estimate of it.
	if m.observer.TurnTimed != nil && !end.IsZero() && m.timing.turn.Started().Equal(m.turnStarted) {
		split := m.timing.turn.Split(end)
		if outcome == observe.TurnCapPaused {
			// The pause is a card in front of the person, and the turn's
			// clock runs on through it: granted more rounds, the turn reports
			// again from its first stamp, and the wait for the answer is time
			// it spent.
			m.timing.turn.Ask(end)
		}
		m.observer.TurnTimed(m.turnCount, int64(m.agent.Rounds()), elapsed, outcome, split)
		return
	}
	m.observer.Turn(m.turnCount, int64(m.agent.Rounds()), elapsed, outcome)
}

// noteWait moves the turn's clock to what the turn waits on in the state it
// is entering. Every request, card, command and check passes through a state
// change, which is what makes this the one place the waits are read from
// rather than a stamp at each of them. A state no wait names leaves the clock
// where it is: the turn is still waiting on whatever it was.
// See docs/capabilities/sessions-and-memory.md#startup-and-waits-are-timed.
func (m *Model) noteWait(s state) {
	if !m.turnOpen {
		return
	}
	now := clock()
	switch s {
	case stateStreaming:
		// Entering the stream is a request going out; the first of a turn
		// begins its clock at the turn's own start stamp.
		if !m.timing.turn.Started().Equal(m.turnStarted) {
			m.timing.turn.Begin(m.turnStarted)
		}
		m.timing.turn.Request(now)
	case stateRetryWait:
		m.timing.turn.Request(now)
	case stateRunningCmd, stateClassifying, stateCloseGate:
		m.timing.turn.Tool(now)
	case stateConfirmRun, stateQuestion, statePlanApprove:
		m.timing.turn.Ask(now)
	case stateInput:
		// A turn parked at a round boundary is waiting on the person who
		// parked it.
		if m.heldAtBoundary() {
			m.timing.turn.Ask(now)
		}
	}
}

// noteEvent marks a stream event arriving, and whether it put anything on
// the screen.
func (m *Model) noteEvent(drew bool) {
	if m.turnOpen {
		m.timing.turn.Event(clock(), drew)
	}
}

// noteDrew marks a row the turn's own work landed on the screen.
func (m *Model) noteDrew() {
	if m.turnOpen {
		m.timing.turn.Drew(clock())
	}
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
