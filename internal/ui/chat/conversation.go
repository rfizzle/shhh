package chat

// A conversation is `shhh chat`: the same model as the coding agent with
// the coding surfaces not drawn. The gates here are the whole difference
// on the TUI side — the CLI already registered no tool that could write,
// so what these hide is the accounting for work that cannot happen: the
// changes rail, the review and undo views, the plan checklist, the backlog.
// See docs/capabilities/chat.md#the-transcript-is-the-conversation.

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/rfizzle/shhh/internal/notebook"
)

// WithConversation marks the session as a conversation. It has no start
// screen: the empty session is a prompt, not a survey of the checkout
// (docs/capabilities/chat.md#it-starts-where-you-are-not-with-what-you-have).
func (m Model) WithConversation() Model {
	m.conversation = true
	m.start = nil
	return m
}

// WithNotebook attaches the session's shared notebook. The model owns the
// session slot's name, so it is the one that binds the notebook to it —
// here, and again wherever the name changes.
func (m Model) WithNotebook(nb *notebook.Store) Model {
	m.notebook = nb
	m.bindNotebook()
	return m
}

// bindNotebook points the notebook at the current session slot. A bind that
// fails leaves the notebook in memory, which is the session's working state
// either way; only the resume would have lost it.
func (m *Model) bindNotebook() {
	if m.notebook == nil {
		return
	}
	_ = m.notebook.Bind(m.sessionName)
}

// codingSurfaces reports whether the coding agent's accounting — changes,
// review, undo, plan, backlog — is drawn. It is the one predicate the
// command table and the rail consult.
func (m *Model) codingSurfaces() bool { return !m.conversation }

// unavailableCommand reports a command the session knows but has not
// wired — a coding surface asked for in a conversation. The completion menu
// already hides it; this is the answer when it is typed anyway, so the line
// is not sent to the model as a question.
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

// notesCommand is /notes: the notebook as the person sees it. Bare lists
// every note; drop <n> removes one; clear empties it. It exists because a
// store agents write to without asking has to be one the person can read
// and correct (docs/capabilities/chat.md#what-they-share).
func (m *Model) notesCommand(args []string) string {
	if m.notebook == nil {
		return "This session has no notebook."
	}
	if len(args) == 0 {
		notes := m.notebook.List()
		if len(notes) == 0 {
			return "The notebook is empty. Agents write to it with write_note; /notes drop <n> removes one."
		}
		return notebook.Format(notes)
	}
	switch args[0] {
	case "clear":
		n := 0
		for _, note := range m.notebook.List() {
			if err := m.notebook.Delete(note.ID); err == nil {
				n++
			}
		}
		return fmt.Sprintf("Dropped %d notes.", n)
	case "drop":
		if len(args) < 2 {
			return "Usage: /notes drop <n>"
		}
		id, err := strconv.ParseInt(strings.TrimPrefix(args[1], "n"), 10, 64)
		if err != nil {
			return "Usage: /notes drop <n>"
		}
		if err := m.notebook.Delete(id); err != nil {
			return "Error: " + err.Error()
		}
		return fmt.Sprintf("Dropped note n%d.", id)
	}
	return "Usage: /notes [drop <n>|clear]"
}
