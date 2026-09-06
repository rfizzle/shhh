package chat

// A conversation is `shhh chat`: the same model as the coding agent with
// the coding surfaces not drawn. The gates here are the whole difference
// on the TUI side — the CLI already registered no tool that could write,
// so what these hide is the accounting for work that cannot happen: the
// changes rail, the review and undo views, the plan checklist, the backlog.
// See docs/capabilities/chat.md#the-transcript-is-the-conversation.

// WithConversation marks the session as a conversation. It has no start
// screen: the empty session is a prompt, not a survey of the checkout
// (docs/capabilities/chat.md#it-starts-where-you-are-not-with-what-you-have).
func (m Model) WithConversation() Model {
	m.conversation = true
	m.start = nil
	return m
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
