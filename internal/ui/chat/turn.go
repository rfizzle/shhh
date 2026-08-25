package chat

import "time"

// Turn state and surfaces (S-087).
//
// Two different things used to share Model.state: what the session's own
// turn is doing (streaming, running a command, waiting on the classifier)
// and which transient view owns the screen (focus mode, the full-screen
// diff, a picker). Conflating them is why nothing could be opened or run
// mid-turn — a surface that overwrote the state made the turn's own messages
// unroutable, so every command was refused while the agent worked. With
// sub-agents that ruled out the whole management surface exactly when it
// matters: children only exist while the parent turn is in flight.
//
// The split keeps Model.state as the one field the renderer switches on and
// parks the turn's state in turnBack while a surface borrows the screen. The
// turn keeps running underneath: its messages route by turnState(), its
// transitions land through setTurnState(), and leaving the surface hands the
// screen back to whatever the turn became in the meantime.

// isSurface reports whether s is a transient view borrowing the screen
// rather than a stage of the session's own turn.
func (s state) isSurface() bool {
	switch s {
	case stateFocus, stateDiffFull, stateRewindPick, statePick, stateModelList:
		return true
	}
	return false
}

// turnState is what the session's own turn is doing, ignoring any surface
// currently borrowing Model.state.
func (m Model) turnState() state {
	if m.state.isSurface() {
		return m.turnBack
	}
	return m.state
}

// setTurnState moves the turn to s, writing through a surface that has the
// screen instead of closing it: a turn that finishes (or asks for approval)
// while the user is reading a diff waits for them to come back.
func (m *Model) setTurnState(s state) {
	// A turn going idle stamps its end, so the inspector rail's elapsed time
	// freezes at what the turn took instead of counting on (S-092).
	if s == stateInput && m.working() && !m.turnStarted.IsZero() {
		m.turnEnded = time.Now()
		// The turn's usage joins the session's history with its wall time
		// (S-093), which is why the ring is closed here and not on the
		// last usage report — a turn is more than its final request.
		m.vitals.endTurn(m.turnEnded.Sub(m.turnStarted))
		// And the turn closes with what it did (S-098). It happens here for
		// the same reason: every path back to the input passes through this
		// one transition, so no turn can end without a summary.
		m.appendTurnClose()
	}
	if m.state.isSurface() {
		m.turnBack = s
		return
	}
	m.state = s
}

// enterSurface gives the screen to a transient view, remembering the turn
// state it borrows. Opening one surface from another (the full-screen diff
// from focus mode) keeps the same parked turn.
func (m *Model) enterSurface(s state) {
	if !m.state.isSurface() {
		m.turnBack = m.state
	}
	m.state = s
}

// leaveSurface hands the screen back to the turn, which may have moved on
// while the surface was up.
func (m *Model) leaveSurface() {
	if m.state.isSurface() {
		m.state = m.turnBack
	}
	m.turnBack = stateInput
}

// working reports whether the session's own turn is in flight — streaming,
// running a command, or waiting on the permission classifier. The input is
// live in all three (S-058), and so are the commands that leave the running
// conversation alone (S-087).
func (m Model) working() bool {
	switch m.turnState() {
	case stateStreaming, stateRunningCmd, stateClassifying:
		return true
	}
	return false
}

// inputLive reports whether the textarea owns the keyboard: the turn states
// that keep it (idle and working), with no surface or prompt over it.
func (m Model) inputLive() bool {
	if m.state.isSurface() {
		return false
	}
	switch m.state {
	case stateInput, stateStreaming, stateRunningCmd, stateClassifying:
		return true
	}
	return false
}
