package chat

// Holding a turn between rounds (
// docs/interface/surfaces.md#the-input-frame).
//
// Cancelling was the only way to stop a turn, which made "I am about to lose
// this network" and "stop, you are doing the wrong thing" the same keystroke
// with the same cost: the work so far kept, the instruction gone, and the
// question to ask again from the top. A hold is the other answer. The turn
// stops where a turn can stop, the conversation keeps everything the round
// put in it, and one key later the model is asked the question it was about
// to be asked anyway.
//
// **A hold is not an interrupt, and it cannot be.** An open provider stream
// is a socket somebody has to keep reading: a reader that stops backs it up
// until the provider gives up on the request, which is the same reason
// suspending is refused while a turn streams. So the key marks the turn and
// the round boundary is where the mark is acted on — the one moment the
// round's results are in the conversation and nothing has been asked of the
// model yet. The chip says which of the two states the turn is in, because
// "it will stop" and "it has stopped" are different promises.
//
// The round-limit checkpoint is the same park under another name, and this is
// that mechanism given a key: the turn's accounting stays open, the counter
// stays on the rail, and letting it go asks for the next round rather than
// starting a turn.

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// turnHold is a turn parked at a round boundary: which turn it is, and where
// it had got to when it stopped. The two counts are what the autosave writes
// beside the conversation, so a session quit while held and started again
// comes back to the same place rather than to an idle prompt with an
// unanswered round in front of it.
type turnHold struct {
	// turn is the turn that is parked. A held mark belonging to an older
	// turn is spent, the way the round pause's is.
	turn int64
	// rounds is the counter at the moment it parked, and granted whatever
	// the turn had been given on top of its ceiling. Both are the mark's,
	// not the session's: the Agent's own counter starts over in a new
	// process, and the grant has to come back with the turn or the resumed
	// round stops again at a bound the reader had already lifted.
	rounds, granted int
}

// heldAtBoundary reports whether the session's own turn is parked. It is the
// question every other file asks — the chip, the rail's counter, the close
// that must not happen, the input that queues rather than starts a turn.
func (m Model) heldAtBoundary() bool {
	return m.hold != nil && m.hold.turn == m.turnCount
}

// turnInFlight reports whether the session has a turn that is not over: the
// three states in which what is typed belongs to the round that comes next
// rather than to a turn of its own — working, stopped in front of a decision,
// and parked at a round boundary.
//
// It is deliberately not working(), which answers a different question — is
// there a stream nobody may stop reading — and is what refuses the suspend
// chord and the editor. A held turn has no stream and is not over, so the two
// answers part company there, which is the whole of why this exists.
func (m Model) turnInFlight() bool {
	return m.working() || m.decisionUngated() || m.heldAtBoundary()
}

// toggleHold is the key. One binding for both halves because it is one act
// read twice: a working turn is marked to stop, a parked one is let go, and
// a request not yet honoured is taken back by pressing again — which is what
// makes the key safe to press by mistake.
func (m Model) toggleHold() (tea.Model, tea.Cmd) {
	switch {
	case m.heldAtBoundary():
		return m.releaseHold()
	case m.holdAsked:
		m.holdAsked = false
		m.releaseChildren()
		return m, nil
	case m.working():
		m.holdAsked = true
		// Children are asked now rather than when the parent's own turn
		// parks: each one reaches its own boundary in its own time, and a
		// fan-out whose parent is between rounds may have four writers
		// halfway through theirs.
		if m.subagents != nil {
			m.subagents.Hold()
		}
		return m, nil
	}
	return m.systemNotice("Nothing is running to hold.")
}

// holdTurn parks the turn. It is the round-limit pause without the row: the
// conversation is untouched, the counter is kept, the turn's accounting stays
// open — the turn has not ended, and a close block saying it did would be the
// session's own record disagreeing with the chip beside it.
func (m Model) holdTurn() (tea.Model, tea.Cmd) {
	m.holdAsked = false
	m.hold = &turnHold{turn: m.turnCount, rounds: m.agent.Rounds(), granted: m.roundGrant}
	m.setTurnState(stateInput)
	m.invalidateRenderCache()
	m.syncViewport()
	m.viewport.SetLines(m.renderHistoryLines())
	m.viewport.GotoBottom()
	return m, m.autosaveCmd()
}

// releaseHold lets the turn go on. It goes back through the tool loop rather
// than straight to a request, because everything that boundary owed the next
// round is owed again: the steering typed while the turn was parked, a tree
// that moved while nobody was looking, the ceiling the turn is still under.
func (m Model) releaseHold() (tea.Model, tea.Cmd) {
	m.dropHold()
	m.invalidateRenderCache()
	if !m.turnOpen {
		// A conversation reopened held is continuing a turn that began in
		// another sitting, and this session has no accounting for it: what
		// it costs from here is what this sitting is billed for, so the turn
		// is opened rather than reopened.
		m.turnCount++
		m.turnStarted, m.turnEnded = time.Now(), time.Time{}
		m.turnOpen, m.turnOutcome = true, components.TurnDone
		m.turnTokensIn, m.turnTokensOut = 0, 0
		m.vitals.startTurn()
	}
	// The turn is in flight again before the loop is re-entered, so a release
	// that lands on the ceiling can park on the checkpoint the ordinary way.
	m.setTurnState(stateStreaming)
	m.streaming = ""
	m.atBottom = true
	next, cmd := m.resumeToolLoop()
	resumed, ok := next.(Model)
	if !ok {
		return next, cmd
	}
	resumed.viewport.SetLines(resumed.renderHistoryLines())
	resumed.viewport.GotoBottom()
	return resumed, tea.Batch(cmd, resumed.autosaveCmd())
}

// dropHold takes the hold off the session and off every child. It is called
// wherever the held turn stops being the turn the session is on — released,
// cancelled, or moved past by fresh input — because a child parked at its own
// boundary has nothing else that could ever let it go.
func (m *Model) dropHold() {
	m.hold, m.holdAsked = nil, false
	m.releaseChildren()
}

// releaseChildren lets every held child go on. One release for the whole
// fan-out: the hold was asked of the session, not of a child, and letting
// them out one at a time is a list nobody could be expected to keep.
func (m *Model) releaseChildren() {
	if m.subagents != nil {
		m.subagents.Release()
	}
}

// holdChip is the top rail's activity slot while a hold stands, or empty.
// Both states wear the chip a waiting decision wears, because both are the
// same fact about the session: it has stopped, and it is waiting on you.
func (m Model) holdChip() string {
	switch {
	case m.heldAtBoundary():
		return "⏸ held · " + keys.Shown(keys.Draft.Pause) + " resumes"
	case m.holdAsked && m.working():
		return "⏸ holding after this round"
	}
	return ""
}

// heldNotice is the row a conversation reopened held opens with. The chip
// says the turn is parked; this says where it was parked, which is the fact
// the mark carries and the screen has nowhere else to put — the Agent's round
// counter belongs to this process and starts at nothing.
func (m Model) heldNotice() string {
	return fmt.Sprintf("This conversation was held mid-turn after %s — %s lets it go on.",
		plural(m.hold.rounds, "round"), keys.Shown(keys.Draft.Pause))
}

// holdMarker is what the autosave writes beside the conversation, or nil when
// there is nothing to say. A turn that has been asked to hold and has not
// reached its boundary is not one: the round it is in is still going, and the
// conversation on disk is not the one that round will leave behind.
func (m Model) holdMarker() *storage.ChatHold {
	if !m.heldAtBoundary() {
		return nil
	}
	return &storage.ChatHold{Rounds: m.hold.rounds, Granted: m.hold.granted}
}
