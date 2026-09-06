package chat

// The session's shared notebook, on the screen: the slot it binds to, the
// command the person reads it with, and the line a turn closes with when a
// delegate wrote in it.
//
// It is not a conversation's surface. A coding session's children are the
// case the notebook was always for — a fan-out of four researchers finding
// the same thing four times is the cost it exists to remove — so the model
// holds it whichever session it is drawing.
// See docs/capabilities/subagents.md#what-they-share.

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/rfizzle/shhh/internal/notebook"
)

// WithNotebook attaches the session's shared notebook. The model owns the
// session slot's name, so it is the one that binds the notebook to it —
// here, and again wherever the name changes.
func (m Model) WithNotebook(nb *notebook.Store) Model {
	m.notebook = nb
	m.bindNotebook()
	return m
}

// bindNotebook points the notebook at the current session slot and tells it
// which turn is open. A bind that fails leaves the notebook in memory, which
// is the session's working state either way; only the resume would have lost
// it.
func (m *Model) bindNotebook() {
	if m.notebook == nil {
		return
	}
	_ = m.notebook.Bind(m.sessionName)
	m.notebook.SetTurn(m.turnCount)
}

// nextTurn moves the session on to its next turn number and tells the
// notebook, so a note a delegate writes carries the turn its parent spawned
// it in. The two are one call because a turn that moved without saying so
// would stamp a child's note with the turn before it, and the close that
// counts the fan-out's notes would report them against the wrong turn.
func (m *Model) nextTurn() {
	m.turnCount++
	m.notebook.SetTurn(m.turnCount)
}

// notesCommand is /notes: the notebook as the person sees it. Bare lists
// every note by author; drop <n> removes one; clear empties it. It exists
// because a store agents write to without asking has to be one the person
// can read and correct — and dropping a note is only ever the person's, so
// this is the only route to it (docs/capabilities/subagents.md#what-they-share).
func (m *Model) notesCommand(args []string) string {
	if m.notebook == nil {
		return "This session has no notebook."
	}
	if len(args) == 0 {
		notes := m.notebook.List()
		if len(notes) == 0 {
			return "The notebook is empty. Agents write to it with write_note; /notes drop <n> removes one."
		}
		return notebook.FormatByAuthor(notes)
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

// turnNotesClause is what the turn's delegates left in the notebook, for the
// turn's close. A child's report comes back to the model and is never shown
// in full; a note is the other half — what a sibling will need — and without
// this line the person would have no way of knowing anything was written at
// all until they typed /notes.
//
// The orchestrator's own notes are left out: the close reports what came
// back from the fan-out, and what the session wrote for itself is already in
// front of the person as the rows that wrote it.
func (m Model) turnNotesClause() string {
	notes := notebook.WrittenIn(m.notebook.List(), m.turnCount, notebook.Orchestrator)
	if len(notes) == 0 {
		return ""
	}
	var authors []string
	seen := map[string]bool{}
	for _, n := range notes {
		if !seen[n.Author] {
			seen[n.Author] = true
			authors = append(authors, n.Author)
		}
	}
	return plural(len(notes), "note") + " from " + strings.Join(authors, ", ")
}
