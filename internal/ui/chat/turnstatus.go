package chat

// The running turn's status (
// docs/interface/surfaces.md#the-input-frame). The frame's activity slot used
// to say `WORKING` — which is true of every moment of every turn and
// therefore says nothing — and what the turn was doing was reported only
// after the fact. This is that slot given the turn's live account of itself:
// which of the five phases it is in, and how long the turn has been running.
//
// The call it is in that phase for is not on it. The feed already draws the
// act as a row — the command whole, its outcome, and its last line of output
// under it while it runs (activity.go, paint.go) — and the slot the rail
// leaves is a fraction of that width, so a copy of the command here was the
// same words a second time and cut off mid-word besides. The elapsed that
// stays says whose clock it is, because the row below is ticking the
// command's (docs/interface/surfaces.md#the-input-frame).
//
// What the turn is spending is not on it. The tokens are on the vitals rail a
// row below, which already carries the running turn's estimate inside the
// session's total (vitals.go), and the cost is on the close row the turn
// leaves in the transcript, where the ledger states what each request was
// actually billed. Priced here it could only be freshRateLabel's upper bound
// on a live pair, which charges every cached prompt read at the fresh rate:
// the newest figure on the frame would be the one wrong number among the
// right ones beside it (attach.go, render.go).
//
// Nothing here is a second source of truth. The phase is read off the state
// the turn is already in, the elapsed off the turn's own start stamp, and the
// resolved line's outcome off the turn's close block — so the status line and
// the row it leaves in the transcript cannot disagree about how it ended.
//
// The clock is stated once on the screen, and this line has it only while the
// turn is running: the rail's THIS TURN block counts the turn's files and
// tools and no span, and the summary this line resolves into leaves the
// finished span on the close row, which is still carrying it when the turn
// has scrolled away. The summary leaves the close row's tool count and bill
// there too: it says the turn is over and how it ended, which is the one
// thing the reader watching the cursor wants from it
// (docs/interface/surfaces.md#the-input-frame).

import (
	"fmt"
	"math"
	"time"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// turnStatus is the line the frame's activity slot shows, and whether there
// is one: a running turn's live status, or the summary the last turn resolved
// into. A session that has not run a turn yet has neither, and the slot says
// `idle`.
func (m Model) turnStatus() (components.TurnStatus, bool) {
	phase, running := m.turnPhase()
	if !running {
		return m.resolvedTurnStatus()
	}
	s := components.TurnStatus{Frame: m.spinFrame, Phase: phase, Wait: m.modelWait(phase)}
	// A turn with no start stamp reports no elapsed rather than counting from
	// the zero time; every turn the user starts has one.
	if !m.turnStarted.IsZero() {
		age := m.turnElapsed()
		s.Elapsed = components.FormatElapsed(age)
		// The label materialises over the turn's first second. Its age
		// is the turn's own — the number the line is already printing beside
		// it — so the entrance borrows a clock the session keeps rather than
		// asking for a second one, and the frames it advances on are still
		// the one tick source's.
		s.Arriving = components.AnimArriving(age)
	}
	return s, true
}

// turnPhase is which of the five phases the turn is in, and whether the turn
// is in any of them at all. The vocabulary is closed: a state that is not one
// of the five picks the nearest rather than becoming a fifth.
//
// What is running is read here and not reported: the phase is the answer, and
// the call that produced it has a row of its own in the feed.
func (m Model) turnPhase() (components.TurnPhase, bool) {
	switch m.turnState() {
	case stateClassifying:
		// The vitals rail's `✦ deciding`, seen from the frame.
		return components.PhaseDeciding, true
	case stateRunningCmd:
		return components.PhaseActing, true
	case stateStreaming:
		switch {
		case m.agent.Executing():
			return components.PhaseActing, true
		case m.streaming != "" || m.timing.request.answering:
			// Prose, or a call being written: the answer is arriving.
			return components.PhaseStreaming, true
		case m.timing.request.reasoning > 0 || (m.timing.request.asked.IsZero() && m.thinkIdx > 0):
			// Reasoning has arrived, which is the one thing that says
			// the model is thinking.
			return components.PhaseThinking, true
		}
		// Nothing the answer is made of has arrived: the turn is waiting
		// on the model, and the line says so rather than guessing that it
		// is thinking (docs/interface/surfaces.md#the-input-frame).
		return components.PhaseWaiting, true
	}
	return components.PhaseThinking, false
}

// retryWarning is how long before the stream's idle deadline a silent wait
// says the retry is coming. Inside it the countdown is the one thing about
// the wait the reader can act on; before it, a countdown from two minutes
// would be a second clock with nothing to say.
const retryWarning = 30 * time.Second

// modelWait is what the frame says about a request with nothing drawable
// back yet: how long since it went out, and what has arrived — nothing,
// events that draw nothing, or reasoning — read off the same stamps the
// turn's record is split at. A request the clock is not timing states
// nothing, rather than a figure counted from somewhere else.
// See docs/interface/surfaces.md#the-input-frame.
func (m Model) modelWait(phase components.TurnPhase) components.ModelWait {
	r := m.timing.request
	if r.asked.IsZero() || (phase != components.PhaseWaiting && phase != components.PhaseThinking) {
		return components.ModelWait{}
	}
	now := clock()
	w := components.ModelWait{Since: components.FormatElapsed(now.Sub(r.asked))}
	switch {
	case r.reasoning > 0:
		w.Stretch = reasoningCount(r.reasoning)
		w.Heard = "last " + sinceLast(now.Sub(r.last)) + " ago"
	case phase == components.PhaseThinking:
		// Reasoning this request's clock did not hear arrive: the count
		// would be wrong, so only the clock is stated.
	case r.events > 0:
		w.Stretch = observe.StretchQuiet
		w.Heard = "keepalives only, last " + sinceLast(now.Sub(r.last)) + " ago"
	default:
		w.Stretch = observe.StretchSilent
		if in, ok := m.retryIn(now.Sub(r.asked)); ok && m.backoff.Remaining() > 0 {
			w.RetryIn = in
		} else if ok {
			w.FailsIn = in
		} else {
			w.Heard = "nothing arrived"
		}
	}
	return w
}

// retryIn is how long until a silent request's idle deadline gives it up,
// where that is close enough to say: within retryWarning of it, and never
// where the deadline is off. Whether the deadline asks again is the caller's
// to say, from the bound the stall has left (agent.Backoff.Remaining).
func (m Model) retryIn(waited time.Duration) (string, bool) {
	idle := m.timing.idle
	switch {
	case idle < 0:
		return "", false
	case idle == 0:
		idle = provider.DefaultStreamIdle
	}
	left := max(idle-waited, 0)
	if left > retryWarning {
		return "", false
	}
	return fmt.Sprintf("%ds", int(math.Ceil(left.Seconds()))), true
}

// reasoningCount is the reasoning a request has heard, in events: the count
// the record's quiet stretch keeps, so the screen and the row agree.
func reasoningCount(n int) string {
	if n == 1 {
		return "1 reasoning event"
	}
	return fmt.Sprintf("%d reasoning events", n)
}

// sinceLast is the time since a request's last event: tenths under a second,
// where a live stream's gaps are, and whole seconds above it.
func sinceLast(d time.Duration) string {
	switch {
	case d < time.Second:
		return fmt.Sprintf("%.1fs", d.Seconds())
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return components.FormatElapsed(d)
}

// liveTurnTokens is what the turn has spent so far: the requests it has
// already been billed for, plus an estimate of what the round in flight is
// adding to them. The estimate is the same len/4 the context accounting
// uses — the point is that the number moves while the tokens do, and it is
// replaced by the provider's own count the moment the request reports one.
func (m Model) liveTurnTokens() (in, out int64) {
	in, out = m.vitals.current.In, m.vitals.current.Out
	// A turn whose first request has not reported yet has no billed prompt
	// to state, and stating zero would be stating something the session
	// knows is wrong: what was sent is the conversation, which the context
	// accounting has already measured. Later rounds keep the billed figure
	// instead — their prompt is the conversation again, so adding the
	// estimate on top of it would count the same words twice.
	if in == 0 && m.turnState() == stateStreaming {
		in = m.estimatedContextTokens()
	}
	if m.streaming != "" {
		out += agent.EstimateTokens(m.streaming)
	}
	// Reasoning is billed as output too, and on a thinking model it is most
	// of what the opening seconds of a round produce — the seconds where the
	// counters would otherwise sit still. It is counted only while the round
	// is open, which is what the event channel says: the row settles when the
	// prose starts (think.go) but stays on screen for the rest of the turn,
	// and the usage event that closed its round already counted it.
	if m.events != nil && m.thinkIdx > 0 && m.thinkIdx <= len(m.transcript) {
		out += agent.EstimateTokens(m.transcript[m.thinkIdx-1].text)
	}
	return in, out
}

// easeCounts re-aims the session's counters at what has actually been spent,
// and advances them one step per frame of the one tick source. It is called
// from the tail of every update rather than from the paths that change a
// count, for the reason the spinner's own rule is applied there: a usage
// report, a chunk of prose, the turn opening and the turn closing all move
// these numbers, and a rule fifteen handlers have to remember is a rule three
// of them will not.
//
// Calling it twice on one frame — once as the tick lands, once as the update
// carrying the tick finishes — is deliberate and free: the odometer steps on
// a frame it has not seen and re-aims on every call, so the tick's own call
// is what lets the chain end on the frame a climb arrives rather than one
// frame later.
func (m *Model) easeCounts() {
	// The counters are aimed on every call, at rest as well as mid-turn,
	// because the vitals rail states them at rest too — and their target
	// survives a turn being reopened: a turn stopped at its round ceiling is
	// closed, and granting it more rounds puts everything it spent back on
	// the books (rounds.go), but that spend only moves between the two halves
	// of the session's sum and never leaves it (vitals.go).
	sin, sout := m.sessionTokensFrom(m.liveTurnTokens())
	m.sessionUp.Toward(sin, m.spinFrame)
	m.sessionDown.Toward(sout, m.spinFrame)
}

// countsEasing reports whether either counter is still climbing. The spinner
// asks it, because a climb is something moving on screen and the one tick
// source is what moves it (spin.go) — and because it goes false on the frame
// the last count lands, the chain it keeps alive ends there rather than
// running on over an idle session.
func (m Model) countsEasing() bool {
	return m.sessionUp.Easing() || m.sessionDown.Easing()
}

// countsLive reports whether the counters are carrying a turn in flight
// rather than the session's settled totals, which is what decides the
// resolution they print at.
func (m Model) countsLive() bool {
	_, running := m.turnPhase()
	return running || m.countsEasing()
}

// resolvedTurnStatus is the summary the live line becomes when the turn ends
// : the same line finished, in place. Its outcome is read off the turn's
// own close block rather than recomputed, so the two cannot disagree.
//
// A turn that closed without one — a round-limit pause states its own
// checkpoint instead — resolves into nothing, and the slot goes back
// to `idle` rather than reporting an older turn as if it were this one.
func (m Model) resolvedTurnStatus() (components.TurnStatus, bool) {
	if m.turnCount == 0 {
		return components.TurnStatus{}, false
	}
	for i := len(m.transcript) - 1; i >= 0; i-- {
		e := m.transcript[i]
		if e.kind != entryTurnClose || e.close == nil {
			continue
		}
		if e.turn != m.turnCount {
			break
		}
		return components.TurnStatus{Done: true, Outcome: e.close.State}, true
	}
	return components.TurnStatus{}, false
}
