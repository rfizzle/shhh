package chat

// Session observability: the Model's adaptation to the observer contract in
// internal/observe. The codes and the closed sets live there, because every
// surface reports the same ones; what lives here is where the model's own
// accounting — the turn, the round, the ledger, the close state — is read
// off to fill them in.

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/components"
)

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
	// tailSeen is the last line of a running command's output the clock was
	// told about, so a line is counted once however many frames show it.
	tailSeen string
	// request is the request in flight as the frame's waiting state reads
	// it, marked at the same stamps the turn's clock is.
	request requestHeard
	// idle is provider.stream_idle_seconds as the file spells it: zero for
	// the built-in deadline, a negative for none.
	idle time.Duration
}

// requestHeard is what one request has heard back: when it went out, when
// its last event arrived, how many events have, how many of them were
// reasoning, and whether the answer itself — prose, or a call being written —
// has begun. A request that has heard nothing drawable is a wait on the
// model, and these are what the frame says about it
// (docs/interface/surfaces.md#the-input-frame).
type requestHeard struct {
	asked, last time.Time
	events      int
	reasoning   int
	answering   bool
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
	if m.wiring.Observer.Usage == nil {
		return
	}
	if m.wiring.Ledger == nil {
		cost, priced := m.usageTotalCost(m.TotalTokensIn, m.TotalTokensOut)
		m.wiring.Observer.Usage(m.turnCount, m.TotalTokensIn, m.TotalTokensOut, cost, priced)
		return
	}
	t := m.wiring.Ledger.Total()
	m.wiring.Observer.Usage(m.turnCount, t.In, t.Out, t.Cost, t.Priced)
}

// usageTotalCost prices a token pair against the session's current model, for
// the ledgerless case.
func (m Model) usageTotalCost(in, out int64) (float64, bool) {
	if m.wiring.Prices == nil || m.modelName == "" {
		return 0, false
	}
	inCost, outCost, found := m.wiring.Prices.Cost(m.modelName, in, out)
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
	if m.wiring.Observer.ToolCall != nil {
		m.wiring.Observer.ToolCall(m.pos(), tool, duration, outcome, class, purpose)
	}
}

func (m *Model) recordDecision(decision, reason string) {
	if m.wiring.Observer.Decision != nil {
		m.wiring.Observer.Decision(m.pos(), decision, reason)
	}
}

// recordVerdict is recordDecision for a verdict the classifier reached, with
// the time it took to reach it.
func (m *Model) recordVerdict(decision, reason string, took time.Duration) {
	m.wiring.Observer.Decided(m.pos(), decision, reason, took)
}

// recordTurn reports the turn that is closing. It runs from the one place
// every turn ends (appendTurnClose), so no turn can end unrecorded.
func (m *Model) recordTurn(outcome string) {
	if m.wiring.Observer.Turn == nil {
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
	if m.wiring.Observer.TurnTimed != nil && !end.IsZero() && m.timing.turn.Started().Equal(m.turnStarted) {
		split := m.timing.turn.Split(end)
		if outcome == observe.TurnCapPaused {
			// The pause is a card in front of the person, and the turn's
			// clock runs on through it: granted more rounds, the turn reports
			// again from its first stamp, and the wait for the answer is time
			// it spent.
			m.timing.turn.Ask(end)
		}
		m.wiring.Observer.TurnTimed(m.turnCount, int64(m.agent.Rounds()), elapsed, outcome, split)
		return
	}
	m.wiring.Observer.Turn(m.turnCount, int64(m.agent.Rounds()), elapsed, outcome)
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
		m.timing.request = requestHeard{asked: now}
	case stateRetryWait:
		m.timing.turn.Request(now)
		m.timing.request = requestHeard{asked: now}
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

// noteHeard marks what a stream event carried, for the frame's account of
// the request: reasoning, the answer itself, or neither — a keepalive. It is
// stamped on the clock the turn's own marks read.
func (m *Model) noteHeard(reasoning, answering bool) {
	r := &m.timing.request
	if !m.turnOpen || r.asked.IsZero() {
		return
	}
	r.last = clock()
	r.events++
	if reasoning {
		r.reasoning++
	}
	r.answering = r.answering || answering
}

// heardKeepalive takes a batch of a gateway's pings. A ping holds the
// connection open and says nothing: it is heard, and counts in the stretch it
// arrived in, but it draws nothing, stores nothing and does not end the stall
// the turn may be in.
func (m Model) heardKeepalive() (tea.Model, tea.Cmd, bool) {
	m.noteEvent(false)
	m.noteHeard(false, false)
	return m, waitForEvent(m.events), true
}

// noteTail marks a running command's output reaching the screen. The runner
// sets the tail line from its own goroutine and no message says so, so the
// line is read where the screen's tick already looks. A new line is an event
// the way a stream delta is: it counts in the stretch it arrived in, and the
// stretch stays the tool's, where a command that printed nothing is waiting.
func (m *Model) noteTail() {
	if !m.turnOpen || m.state != stateRunningCmd || m.runTail == nil {
		return
	}
	line := m.runTail.Line()
	if line == "" || line == m.timing.tailSeen {
		return
	}
	m.timing.tailSeen = line
	m.timing.turn.Event(clock(), false)
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
	if m.wiring.Observer.Signal != nil {
		m.wiring.Observer.Signal(m.pos(), code, reason)
	}
}
