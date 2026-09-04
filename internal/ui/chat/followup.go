package chat

// The follow-up queue (docs/interface/surfaces.md#the-input-frame). Typing
// while the agent works is steering: the sentence joins the running
// conversation before the next model request. A follow-up is the other
// intent — "when this is done, then…" — and it waits for the turn to
// finish before going out as the next user message. One chord separates
// them: enter steers, alt+enter queues a follow-up. Idle, alt+enter is the
// newline it has always been, because there is no turn to follow.
//
// A cancel does not send what was queued for a turn that no longer exists:
// the queue survives, marked held, and the rail offers the way to take a
// line back into the draft. Sending after a cancel is the reader's call,
// because the follow-up may only have made sense after the work that was
// just abandoned.

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// queueFollowUp answers alt+enter on the orchestrator draft: with a turn
// live and something typed, the draft joins the follow-up queue. It reports
// false when the chord is not its to claim — idle, attached, or an empty
// box — and the key falls through to the textarea's newline.
func (m Model) queueFollowUp() (tea.Model, tea.Cmd, bool) {
	if !m.inputLive() || m.attachedTo != "" || !m.turnInFlight() {
		return m, nil, false
	}
	text := strings.TrimSpace(m.input.Value())
	if text == "" {
		return m, nil, false
	}
	if reason, held := m.todoRunHoldsInput(); held {
		next, cmd := m.systemNotice("Not queued: " + reason + ".")
		return next, cmd, true
	}
	// A command is not a message, and a queued one would go out as raw
	// text — the gutter's `!` and the slash both promise a dispatch this
	// queue does not run. Refused rather than reinterpreted: run it when
	// the turn is finished.
	if _, _, bang := bangCommand(text); bang || commandName(text) != "" {
		next, cmd := m.systemNotice("Not queued: a command is not a message — run it once the turn is finished.")
		return next, cmd, true
	}
	if !secretInput(text) {
		m.recordInput(text)
	}
	m.input.Reset()
	m.followUps = append(m.followUps, text)
	// Queueing again is asking for the automatic send back: whatever a
	// cancel held, the reader has now written something meant for after
	// the current turn.
	m.followUpsHeld = false
	// The count surfaces on the notice rail.
	m.syncViewport()
	return m, nil, true
}

// pullQueued answers alt+↑: the newest queued message — a follow-up first,
// else a steering line — comes back into the draft, above whatever is
// already there. It reports false when there is nothing to pull.
func (m Model) pullQueued() (tea.Model, tea.Cmd, bool) {
	if !m.inputLive() || m.attachedTo != "" {
		return m, nil, false
	}
	var pulled string
	switch {
	case len(m.followUps) > 0:
		pulled = m.followUps[len(m.followUps)-1]
		m.followUps = m.followUps[:len(m.followUps)-1]
		if len(m.followUps) == 0 {
			m.followUpsHeld = false
		}
	case len(m.steering) > 0:
		pulled = m.steering[len(m.steering)-1]
		m.steering = m.steering[:len(m.steering)-1]
	default:
		return m, nil, false
	}
	if cur := m.input.Value(); strings.TrimSpace(cur) != "" {
		pulled += "\n" + cur
	}
	m.input.SetValue(pulled)
	m.input.MoveToEnd()
	// The rail's count shrank, and the box may have grown a line.
	m.syncViewport()
	return m, nil, true
}

// holdFollowUps marks the queue held. Every abnormal end of a turn — a
// cancel, a broken stream, a cancelled retry wait — calls it: what was
// queued was written against work that did not finish, so nothing sends it
// unasked.
func (m *Model) holdFollowUps() {
	if len(m.followUps) > 0 {
		m.followUpsHeld = true
	}
}

// dispatchFollowUp sends the oldest queued follow-up as the next user turn.
// It runs where a turn truly ends and the session is idle — one follow-up
// per turn end, so each answer is read against the message that asked for
// it — and never after a cancel put the queue on hold, or while a backlog
// run owns the session's turns.
func (m Model) dispatchFollowUp() (tea.Model, tea.Cmd, bool) {
	if m.followUpsHeld || len(m.followUps) == 0 {
		return m, nil, false
	}
	if m.todoRunner.state != nil && !m.todoRunner.state.Over() {
		return m, nil, false
	}
	text := m.followUps[0]
	m.followUps = m.followUps[1:]
	next, cmd := m.sendUserMessage(text)
	// The turn that just ended has not been autosaved yet — this dispatch
	// returns before the done handler's own save — so the save rides here,
	// with the follow-up already in the conversation, the way steering's
	// does.
	if nm, ok := next.(Model); ok {
		cmd = tea.Batch(cmd, nm.autosaveCmd())
	}
	return next, cmd, true
}

// followUpNotice is the notice rail's count of what waits for the turn to
// end, with the held state and its way out when a cancel stopped the
// automatic send.
func (m Model) followUpNotice() string {
	n := len(m.followUps)
	if n == 0 {
		return ""
	}
	label := fmt.Sprintf("%d follow-up", n)
	if n > 1 {
		label += "s"
	}
	if m.followUpsHeld {
		label += " held — " + keys.Shown(keys.Draft.PullQueued) + " recalls"
	}
	return label
}
