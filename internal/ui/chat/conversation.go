package chat

import "github.com/rfizzle/shhh/internal/agent"

// A conversation is `shhh chat`: the same model as the coding agent with
// the coding surfaces not drawn. The gates here are the whole difference
// on the TUI side — the CLI already registered no tool that could write,
// so what these hide is the accounting for work that cannot happen: the
// changes rail, the review and undo views, the plan checklist, the backlog.
// See docs/capabilities/chat.md#the-transcript-is-the-conversation.

// conversationModeWord is what the frame says a conversation runs in. It is
// the read-only mode's own spelling, because that is the promise — nothing is
// changed — and a second word for it would be a second name for one bound.
var conversationModeWord = agent.ModeReadOnly.Word()

// conversationModeNote is the answer to every way a coding session changes
// its mode — the chord, the picker, a mode named to /permissions — so none of
// them is silent in a conversation and none of them is sent to the model.
const conversationModeNote = "A conversation has one mode: read-only. Nothing it can do changes the " +
	"tree or the machine, so there is nothing to choose — it reads files and the web without asking. " +
	"shhh code is the session with modes."

// noteOneMode puts conversationModeNote in the transcript and brings it into
// view: nothing else repaints an idle session, so a row left for the next
// frame to find would not be seen until the next keystroke.
func (m *Model) noteOneMode() {
	m.appendEntry(entry{kind: entrySystem, text: conversationModeNote})
	if m.ready {
		m.viewport.SetLines(m.renderHistoryLines())
		m.viewport.GotoBottom()
	}
}

// codingSurfaces reports whether the coding agent's accounting — changes,
// review, undo, plan, backlog — is drawn. It is the one predicate the
// command table and the rail consult.
func (m *Model) codingSurfaces() bool { return !m.wiring.Conversation }

// unavailableCommand reports a command the session knows but has not
// wired — a coding surface asked for in a conversation. The completion menu
// and /help both leave it out, since all three read the same table; this is
// the answer when it is typed anyway, so the line is not sent to the model as
// a question.
func (m *Model) unavailableCommand(name string) bool {
	if m.codingSurfaces() {
		// The coding agent's commands answer for themselves when their
		// source is missing (no db, no runner); the guard is for the
		// surfaces a conversation deliberately does not have.
		return false
	}
	for _, c := range slashCommands() {
		if c.enabled == nil || c.enabled(m) {
			continue
		}
		if c.name == name {
			return true
		}
		for _, a := range c.aliases {
			if a == name {
				return true
			}
		}
	}
	return false
}
