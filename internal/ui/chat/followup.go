package chat

// The follow-up queue (docs/interface/surfaces.md#the-input-frame). Typing
// while the agent works is steering: the sentence joins the running
// conversation before the next model request. A follow-up is the other
// intent — "when this is done, then…" — and it waits for the turn to
// finish before going out as the next user message. One chord separates
// them: enter steers, the queue chord queues a follow-up — and on an empty
// draft the same chord takes the newest queued message back, since a box
// with nothing in it has nothing to queue and the two halves cannot be
// confused (keys.Draft.Queue).
//
// A cancel does not send what was queued for a turn that no longer exists:
// the queue survives, marked held, and the rail offers the way to take a
// line back into the draft. Sending after a cancel is the reader's call,
// because the follow-up may only have made sense after the work that was
// just abandoned.

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// queueFollowUp answers the queue chord on the orchestrator draft: with a
// turn live and something typed, the draft joins the follow-up queue. It
// reports false when the chord is not its to claim — idle, attached, or an
// empty box — and the chord's other half is asked.
func (m Model) queueFollowUp() (tea.Model, tea.Cmd, bool) {
	if !m.inputLive() || m.attachedTo != "" || !m.turnInFlight() {
		return m, nil, false
	}
	text := strings.TrimSpace(m.input.Value())
	if text == "" {
		return m, nil, false
	}
	if reason, held := m.todoRunHoldsInput(); held {
		next, cmd := m.systemNotice("not queued: " + reason)
		return next, cmd, true
	}
	// A command is not a message, and a queued one would go out as raw
	// text — the gutter's `!` and the slash both promise a dispatch this
	// queue does not run. Refused rather than reinterpreted: run it when
	// the turn is finished.
	if _, _, bang := bangCommand(text); bang || commandName(text) != "" {
		next, cmd := m.systemNotice("not queued: a command is not a message — run it once the turn is finished")
		return next, cmd, true
	}
	if !secretInput(text) {
		m.recordInput(text)
	}
	m.input.Reset()
	m.followUps = append(m.followUps, steeringItem{text: text, id: m.queue.next(), atts: m.takeAttachments()})
	// Queueing again is asking for the automatic send back: whatever a
	// cancel held, the reader has now written something meant for after
	// the current turn.
	m.followUpsHeld = false
	// The count surfaces on the notice rail.
	m.syncViewport()
	return m, nil, true
}

// pullQueued is the queue chord's other half: on an empty draft, the newest
// queued message comes back into the draft. It is the queue's own pull-back
// aimed at the last row — a follow-up first, else a steering line, which is
// the order the queue lists them in — so the message leaves the queue the
// same way whichever key took it (msgqueue.go). A line the session queued is
// passed over, since it was never the draft's. It reports false with a draft
// in the box or nothing to pull.
func (m Model) pullQueued() (tea.Model, tea.Cmd, bool) {
	if !m.inputLive() || m.attachedTo != "" || strings.TrimSpace(m.input.Value()) != "" {
		return m, nil, false
	}
	newest := 0
	for _, r := range m.queuedRows() {
		if r.kind == "" && r.id != 0 {
			newest = r.id
		}
	}
	if newest == 0 {
		return m, nil, false
	}
	m.pullBack(newest)
	// The queue lost a row, and the box may have grown a line.
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
//
// It is always an ordinary message and never the answer to an outstanding
// question, and that is a property of when it runs rather than a choice made
// here: a question blocks the turn on its own call, so a turn cannot reach
// its end with one still waiting (question.go). A sentence queued while a
// question is waiting keeps the promise it was queued under — it goes out
// after the turn the answer lets finish.
func (m Model) dispatchFollowUp() (tea.Model, tea.Cmd, bool) {
	if m.followUpsHeld || len(m.followUps) == 0 {
		return m, nil, false
	}
	if m.todo.runner.state != nil && !m.todo.runner.state.Over() {
		return m, nil, false
	}
	item := m.followUps[0]
	m.followUps = m.followUps[1:]
	next, cmd := m.sendUserMessageWith(item.text, item.text, item.atts)
	// The turn that just ended has not been autosaved yet — this dispatch
	// returns before the done handler's own save — so the save rides here,
	// with the follow-up already in the conversation, the way steering's
	// does.
	if nm, ok := next.(Model); ok {
		cmd = tea.Batch(cmd, nm.autosaveCmd())
	}
	return next, cmd, true
}

// followUpNotice is the notice rail's part about the queue: the key that
// moves the keyboard into it, which the rows above the box cannot carry
// themselves, and the held state when a cancel stopped the automatic send.
// The messages are counted by their own rows (msgqueue.go), so the rail does
// not count them a second time.
//
// Attached, the draft is a child's and the key is not answered, so the rail
// offers nothing: the queue is the session's.
func (m Model) followUpNotice() string {
	if m.attachedTo != "" || len(m.queuedRows()) == 0 {
		return ""
	}
	offer := keys.Bracket(keys.Draft.Queued) + " edit the queue"
	if m.followUpsHeld && len(m.followUps) > 0 {
		return "follow-ups held — " + offer
	}
	return offer
}
