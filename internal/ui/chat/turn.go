package chat

import "time"

// Turn state and surfaces.
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
	case stateFocus, stateDiffFull, statePreview, stateReview, stateContext, stateRewindPick, statePick, stateTodoPropose, statePasteDrop, statePersona, stateTodoPause, stateModelList, stateUndoConfirm, stateQuitConfirm, stateKeyEntry, statePressure:
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
	// Every arrival at a decision, and every departure from one, passes
	// through here — so this is where the keyboard is decided. A card can
	// never inherit the gate the last one was given, and one
	// arriving on a draft nobody is typing into holds the keyboard itself
	// rather than charging a handover for a sentence that is not there.
	m.armDecision(s)
	// A turn going idle stamps its end, so the inspector rail's elapsed time
	// freezes at what the turn took instead of counting on.
	if s == stateInput && m.working() && !m.turnStarted.IsZero() {
		m.turnEnded = time.Now()
		// A turn can have edited the backlog files; the rail reads the
		// store, so the store is re-read here rather than per frame.
		m.reloadTodos()
		// The turn's usage joins the session's history with its wall time
		//, which is why the ring is closed here and not on the
		// last usage report — a turn is more than its final request.
		m.vitals.endTurn(m.turnEnded.Sub(m.turnStarted))
		// And the turn closes with what it did. It happens here for
		// the same reason: every path back to the input passes through this
		// one transition, so no turn can end without a summary.
		m.appendTurnClose()
		// A turn that ends at the alert threshold ends with the decision
		// surface, not with a notice about a trim that already happened
		//. Every path back to the input passes through here, which
		// is what makes "once per crossing" a property rather than a habit.
		defer m.armPressureCard()
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
	// A surface that borrows the screen takes the transcript's selection with
	// it (select.go): the full-screen viewers replace the pane, and
	// reading mode re-renders the history through a cursor gutter and can
	// expand a row under coordinates that were taken before it did.
	m.cancelSelection()
	m.state = s
}

// leaveSurface hands the screen back to the turn, which may have moved on
// while the surface was up.
func (m *Model) leaveSurface() {
	if m.state.isSurface() {
		m.state = m.turnBack
	}
	m.turnBack = stateInput
	// A decision the turn reached while the surface had the screen is only
	// arriving now, so it is armed now, the way every arrival is: holding
	// the keyboard over an empty draft, with the grace window absorbing the
	// keystroke that closed the surface and whatever followed it
	// (interrupt.go). One that was already holding the keyboard keeps it:
	// the reader took it on purpose, and the surface they just closed was
	// most likely the card's own full-screen diff.
	if m.arrivalGates(m.state) && !m.decisionHeld {
		// (arrivesHeld answers the summoned case too, so a /run confirm
		// picked from the block picker lands holding the keyboard.)
		m.decisionHeld = m.arrivesHeld()
		m.heldOnArrival = m.decisionHeld
		m.armGrace()
	}
}

// working reports whether the session's own turn is in flight — streaming,
// running a command, or waiting on the permission classifier. The input is
// live in all three, and so are the commands that leave the running
// conversation alone.
func (m Model) working() bool {
	switch m.turnState() {
	case stateStreaming, stateRunningCmd, stateClassifying, stateRetryWait:
		// A turn waiting out a retry is still a turn in flight: it
		// has not closed, its accounting is open, and the next thing it does
		// is the request it was already making.
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
	// A decision on screen that has not been given the keyboard has not taken
	// it from the draft either: the frame is live, and so is
	// everything the input offers.
	if m.decisionUngated() {
		return true
	}
	switch m.state {
	case stateInput, stateStreaming, stateRunningCmd, stateClassifying:
		return true
	}
	return false
}
