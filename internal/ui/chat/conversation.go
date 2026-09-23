package chat

import "github.com/rfizzle/shhh/internal/agent"

// A conversation is `shhh chat`: the same model as the coding agent with
// the coding surfaces not drawn. The gates here are the whole difference
// on the TUI side — the CLI already registered no tool that could write,
// so what these hide is the accounting for work that cannot happen: the
// changes rail, the review and undo views, the plan checklist, the backlog.
// See docs/capabilities/chat.md#the-transcript-is-the-conversation.

// WithConversation marks the session as a conversation. It has no start
// screen: the empty session is a prompt, not a survey of the checkout
// (docs/capabilities/chat.md#it-starts-where-you-are-not-with-what-you-have).
//
// It also has no modes. The toolset is the bound, so the one policy it runs
// in is fixed here: manual underneath, because what still reaches a decision
// — a spawn, a memory — is a question for the person, and a fetch answered by
// the conversation's own rule ahead of the mode (agent.ModePolicy's
// Conversation). Plan mode goes with the rest, and with it the plan card and
// the plan it would carry into a new session.
// See docs/capabilities/chat.md#a-conversation-has-one-mode.
func (m Model) WithConversation() Model {
	m.conversation = true
	m.start = nil
	m.policy.mode = agent.ModeManual
	if m.subagents != nil {
		m.subagents.SetParentMode(m.policy.mode)
		m.subagents.SetConversationPolicy()
	}
	return m
}

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
func (m *Model) codingSurfaces() bool { return !m.conversation }

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
