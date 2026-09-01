package chat

// The running turn's status (
// docs/interface/surfaces.md#the-input-frame). The frame's activity slot used
// to say `WORKING` — which is true of every moment of every turn and
// therefore says nothing — and the numbers that would have made it useful
// were reported only after the fact. This is that slot given the turn's live
// account of itself: which of the four phases it is in, how long it has been
// there, what it has spent, and what that has cost.
//
// Nothing here is a second source of truth. The phase is read off the state
// the turn is already in, the elapsed off the same clock the inspector rail's
// THIS TURN block reads, the tokens off the vitals the session already
// records, and the resolved line off the turn's own close block — so
// the status line and the row it leaves in the transcript state the same four
// facts in the two orders the turn status asks for and cannot disagree.

import (
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/digest"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// turnStatus is the line the frame's activity slot shows, and whether there
// is one: a running turn's live status, or the summary the last turn resolved
// into. A session that has not run a turn yet has neither, and the slot says
// `idle`.
func (m Model) turnStatus() (components.TurnStatus, bool) {
	phase, tool, running := m.turnPhase()
	if !running {
		return m.resolvedTurnStatus()
	}
	s := components.TurnStatus{Frame: m.spinFrame, Phase: phase, Tool: tool}
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
	in, out := m.liveTurnTokens()
	if in > 0 || out > 0 {
		s.Up, s.Down = formatTokenCount(in), formatTokenCount(out)
	}
	// Derived from the live counts, not from the last thing a response
	// reported — which is also why an unpriced model states tokens here
	// instead of a made-up zero.
	s.Cost = m.spendLabel(in, out)
	return s, true
}

// turnPhase is which of the four phases the turn is in, the argument to name
// beside `running`, and whether the turn is in any of them at all. The
// vocabulary is closed: a state that is not one of the four picks the nearest
// rather than becoming a fifth.
func (m Model) turnPhase() (components.TurnPhase, string, bool) {
	switch m.turnState() {
	case stateClassifying:
		// The vitals rail's `✦ checking`, seen from the frame.
		return components.PhaseDeciding, "", true
	case stateRunningCmd:
		return components.PhaseRunning, firstLine(m.runningCommand), true
	case stateStreaming:
		switch {
		case m.agent.Executing():
			return components.PhaseRunning, m.runningToolLabel(), true
		case m.streaming != "":
			return components.PhaseStreaming, "", true
		}
		// Nothing has arrived yet: the model is reasoning before it acts,
		// which is the phase a reasoning stream would fill in.
		return components.PhaseThinking, "", true
	}
	return components.PhaseThinking, "", false
}

// runningToolLabel names the call being executed the way the activity grid
// already names it — its verb and its argument, minus the verb where
// the verb is `run`, because `running run go test` says it twice.
//
// A round executing several calls at once is named by none of them: picking
// the first would report one of three as if it were the only one, and `⠋
// running` is a form the drop ladder already defines.
func (m Model) runningToolLabel() string {
	if len(m.runningTools) != 1 {
		return ""
	}
	tc := m.runningTools[0]
	verb, arg := activityVerb(tc.Name), digest.Arg(tc.Name, tc.Arguments)
	switch {
	case arg == "":
		return verb
	case verb == "run":
		return arg
	}
	return verb + " " + arg
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

// resolvedTurnStatus is the summary the live line becomes when the turn ends
// : the same line finished, in place. It is read off the turn's own
// close block rather than recomputed, so the two cannot disagree.
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
		return components.TurnStatus{
			Done:     true,
			Outcome:  e.close.State,
			Duration: e.close.Elapsed,
			Tools:    e.close.Tools,
			Cost:     e.close.Spend,
		}, true
	}
	return components.TurnStatus{}, false
}
