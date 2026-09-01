package chat

// The spinner's tick loop (docs/interface/README.md). A running turn
// drives three animations at once — the frame's activity slot (where the turn
// status sits and where an attached child still reads `WORKING`),
// the transcript's live rows, and the inspector rail's agent lanes — and the
// design's rule for them is that there is one tick source and never three.
// Three timers would be three different truths about one turn.
//
// One source has a consequence the old code did not carry: if the single
// chain is ever dropped, every animation freezes at once. It was dropped
// routinely. The `spinner.TickMsg` case answered only while a hard-coded list
// of states held, so a tick that arrived a moment after a turn state changed
// was discarded without returning the command that continues the chain — and
// the chain is the loop. Restarting it was left to each transition to
// remember with `tea.Batch(m.spinner.Tick, …)`, which twelve of them did and
// the three that actually start a turn — the user's own prompt, the tool
// round, and the request after a tool round — did not. The spinner froze on
// the first turn of every session and thawed only when some later transition
// happened to carry a `Tick` with it.
//
// So the loop is stated here as a rule instead of as a habit. spinnerWanted
// is the one predicate for "something on screen is animating"; spinCmd is the
// one place a chain starts; and Update applies the rule after every message,
// so entering a working state resumes the loop whatever route it took there.
// No transition has to remember anything, which is what the second and third
// acceptance criteria ask for.

import (
	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/subagent"
)

// spinnerWanted reports whether anything on screen is moving this frame.
//
// The list is the surfaces that read spinFrame or m.spinner, not the states a
// turn passes through: the frame's activity slot while the turn thinks,
// decides, runs a tool or streams; the same slot while an attached child
// works; the model picker's wait on the provider's catalog; and the agent
// lanes of a fan-out, which keep moving after the parent's own turn has gone
// quiet on a decision.
func (m Model) spinnerWanted() bool {
	switch m.turnState() {
	case stateStreaming, stateRunningCmd, stateClassifying:
		return true
	}
	// Bare /model is querying the provider before it can open the picker
	//; the wait is the only thing on screen.
	if m.state == stateModelList {
		return true
	}
	// The profile drafter's wait, which is the same case: a surface with
	// nothing on it but the label saying what is being waited for.
	if m.personaDrafting() {
		return true
	}
	// Attached, the top rail is scoped to the child, so the child's state is
	// what the rail is animating.
	if m.frameWorking() {
		return true
	}
	// A child still working keeps the lanes and the rail's agent block moving
	// even while the parent waits on an approval.
	return m.childrenRunning()
}

// childrenRunning reports whether any sub-agent is still working. A queued or
// blocked child is not: its lane draws a state, not a spinner.
func (m Model) childrenRunning() bool {
	if m.subagents == nil {
		return false
	}
	for _, st := range m.subagents.Snapshot() {
		if st.State == subagent.StateRunning {
			return true
		}
	}
	return false
}

// spinCmd starts the tick chain, or returns nil when one is already running
// or nothing is animating. It is the only place in the package that produces
// a spinner tick: a transition that batched its own would be the second of
// the three timers the one-tick rule rules out.
func (m *Model) spinCmd() tea.Cmd {
	if m.spinning || !m.spinnerWanted() {
		return nil
	}
	m.spinning = true
	return m.spinner.Tick
}

// spinTick advances the one frame every animating surface reads, and answers
// with the command that keeps the chain alive.
//
// Two things it deliberately does not do. It does not advance spinFrame for a
// tick bubbles rejected — a tick from a superseded chain answers with no
// command, and counting it would drift the passive surfaces' frame away from
// m.spinner's by one for the rest of the session. And it does not drop a tick
// it cannot use: when nothing is animating any more it lets the chain end and
// records that it has, so the next working state starts a fresh one instead
// of waiting on a loop that is no longer running.
func (m Model) spinTick(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.spinner, cmd = m.spinner.Update(msg)
	if cmd == nil {
		return m, nil
	}
	m.spinFrame++
	// The tick is also what the streaming transcript is repainted on (
	// the streaming render) — the one clock, spent on the one other thing that
	// wants one.
	if m.streamDirty {
		m.flushStream()
	}
	if !m.spinnerWanted() {
		m.spinning = false
		return m, nil
	}
	return m, cmd
}
