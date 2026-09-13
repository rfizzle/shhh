package chat

// The session's shared notebook, on the screen: the slot it binds to, the
// screen the person reads it on, and the line a turn closes with when a
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

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/notebook"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// notesEmpty is what a notebook nobody has written in says. It is a printed
// line rather than an empty screen because there is nothing to point at: a
// screen whose list says "nothing here" costs the transcript and answers the
// question no better than one sentence does.
const notesEmpty = "The notebook is empty. Agents write to it with write_note; /notes drop <n> removes one."

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

// nextTurn moves the session on to its next turn number and tells the three
// per-turn records — the notebook, the sources ledger and the conversation —
// so a note or a fetch a delegate makes carries the turn its parent spawned
// it in. They are one call because a turn that moved without saying so would
// stamp a child's work with the turn before it, and the close that counts
// the fan-out's notes would report them against the wrong turn.
//
// The conversation is told for the same reason and one more: the messages
// appended from here carry the position the record files their events under,
// which is what lets a recorded round be read back against what was said in
// it (docs/capabilities/sessions-and-memory.md#a-round-can-be-read-back).
func (m *Model) nextTurn() {
	m.turnCount++
	// And the turn's budget for questions, which is spent here rather than
	// where a turn ends for the same reason the number is a count: what the
	// reader is being protected from is how often one instruction interrupts
	// them, and a question still outstanding when the next instruction
	// arrives was asked by the turn before it (question.go).
	m.questionsAsked = 0
	m.notebook.SetTurn(m.turnCount)
	m.sourceLedger.SetTurn(m.turnCount)
	m.agent.SetTurn(m.turnCount)
}

// notesCommand is /notes: the notebook as the person sees it. Bare opens the
// screen; drop <n> removes one from the prompt; clear opens the screen with
// the question over the whole notebook already asked. It exists because a
// store agents write to without asking has to be one the person can read and
// correct — and dropping a note is only ever the person's, so this is the
// only route to it (docs/capabilities/subagents.md#what-they-share).
func (m Model) notesCommand(args []string) (tea.Model, tea.Cmd) {
	if m.notebook == nil {
		return m.surfaceNotice("This session has no notebook.")
	}
	empty := m.notebook.Len() == 0
	switch {
	case len(args) == 0:
		if empty {
			return m.surfaceNotice(notesEmpty)
		}
		return m.openNotes(false)
	case args[0] == "clear":
		if empty {
			return m.surfaceNotice(notesEmpty)
		}
		// Clear asks before it acts, and it asks on the screen so the notes
		// it would take are in front of the reader while they answer: what
		// goes is every note in the session at once, and no agent has a tool
		// that could write one back.
		return m.openNotes(true)
	case args[0] == "drop":
		return m.surfaceNotice(m.dropNoteByName(args[1:]))
	}
	return m.surfaceNotice("Usage: /notes [drop <n>|clear]")
}

// dropNoteByName is `/notes drop <n>` from the prompt, which goes on
// working: the screen is how a note is found, and a reader who already knows
// its number should not have to open one.
func (m Model) dropNoteByName(args []string) string {
	if len(args) == 0 {
		return "Usage: /notes drop <n>"
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(args[0], "n"), 10, 64)
	if err != nil {
		return "Usage: /notes drop <n>"
	}
	if err := m.notebook.Delete(id); err != nil {
		return "Error: " + err.Error()
	}
	return fmt.Sprintf("Dropped note n%d.", id)
}

// openNotes puts the screen up and marks what the notebook holds as read:
// what the turn's close counts as unread is what has arrived since the
// reader last looked at the notebook, and looking at it is this.
//
// It is built once per opening, like the sources screen — what it lists is
// what the notebook held when the reader asked, and a screen that grew a row
// under them mid-read would be answering a question they had stopped asking.
func (m Model) openNotes(clear bool) (tea.Model, tea.Cmd) {
	screen := m.notesScreenData()
	if clear {
		screen.AskClear()
	}
	m.notes = &screen
	m.notesSeen = notebook.Newest(m.notebook.List())
	m.enterSurface(stateNotes)
	return m, nil
}

// updateNotes routes keys while the screen is up. A confirmed drop is
// carried out with the screen still standing — the reader is walking a list
// and correcting it, and leaving after each correction would take the list
// away — and `[enter]` opens the note whole, which comes back here for the
// same reason.
func (m Model) updateNotes(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.notes == nil {
		return m.closeNotes()
	}
	m.notes.Notice = ""
	done, result := m.notes.Update(msg)
	if len(result.Dropped) > 0 {
		return m.dropNotes(result.Dropped)
	}
	if !done {
		return m, nil
	}
	if result.Read {
		return m.openNote(result.ID)
	}
	return m.closeNotes()
}

// dropNotes carries out what the confirm agreed to and rebuilds the list
// under the pointer. The pointer is clamped rather than restored: the rows
// it indexed are not the rows that are there now.
func (m Model) dropNotes(ids []string) (tea.Model, tea.Cmd) {
	var dropped []string
	for _, id := range ids {
		n, err := strconv.ParseInt(strings.TrimPrefix(id, "n"), 10, 64)
		if err != nil {
			continue
		}
		if err := m.notebook.Delete(n); err == nil {
			dropped = append(dropped, id)
		}
	}
	next := m.notesScreenData()
	m.notes.Rows, m.notes.Subject = next.Rows, next.Subject
	m.notes.Focus = min(m.notes.Focus, max(len(next.Rows)-1, 0))
	switch len(dropped) {
	case 0:
		m.notes.Notice = "Nothing was dropped."
	case 1:
		m.notes.Notice = "Dropped note " + dropped[0] + "."
	default:
		m.notes.Notice = fmt.Sprintf("Dropped %d notes.", len(dropped))
	}
	m.notesSeen = notebook.Newest(m.notebook.List())
	return m, nil
}

// openNote takes one note full screen, signed and numbered the way an agent
// reading the notebook is given it. A note is a paragraph, so what the
// viewer adds over the preview is the whole of a body the pane had to wrap.
func (m Model) openNote(id string) (tea.Model, tea.Cmd) {
	for _, n := range m.notebook.List() {
		if noteID(n.ID) != id {
			continue
		}
		return m.openOutputFull(&components.OutputView{
			Title: n.Title,
			Lines: strings.Split(notebook.Format([]notebook.Note{n}), "\n"),
		}, noOutputEntry, stateNotes)
	}
	m.notes.Notice = "That note is no longer in the notebook."
	return m, nil
}

// closeNotes hands the screen back to the turn.
func (m Model) closeNotes() (tea.Model, tea.Cmd) {
	m.notes = nil
	m.leaveSurface()
	m.syncViewport()
	return m, nil
}

// notesLines renders the screen, one row per line.
func (m Model) notesLines() []string {
	if m.notes == nil {
		return nil
	}
	return strings.Split(m.notes.View(m.contentWidth()), "\n")
}

// renderNotesHint is the one line the screen leaves where the draft box was.
// The screen holds the keyboard, so the panel states the way out and nothing
// else, the way the sources screen's does.
func (m Model) renderNotesHint() string {
	return sty.SystemMsg.Render("notes · ") + seg(keys.Notes.Back).render()
}

// notesScreenData builds the screen from the notebook. Every field is
// resolved to its words here rather than in the component, because which
// turn a note belongs to and when it was written are readings of the
// session.
func (m Model) notesScreenData() components.NotesScreen {
	notes := m.notebook.List()
	rows := make([]components.NotesRow, 0, len(notes))
	for _, n := range notes {
		rows = append(rows, notesRow(n))
	}
	return components.NotesScreen{
		Rows:    rows,
		Focus:   max(len(rows)-1, 0),
		Subject: notesSubject(notes),
	}
}

// notesRow is one note as the screen draws it. What it is filed under is the
// root of its signature rather than the whole of it, so a grandchild's notes
// sit under the child that spawned them and a task the session handed out
// reads as one group however deep the agent that did the work was; the
// signature itself is what the note is signed with, which the preview says.
func notesRow(n notebook.Note) components.NotesRow {
	row := components.NotesRow{
		ID: noteID(n.ID), Group: notebook.RootAuthor(n.Author), Signer: n.Author,
		Label: n.Title, Number: noteID(n.ID), Body: strings.Split(n.Body, "\n"),
	}
	if n.Turn > 0 {
		row.Turn = fmt.Sprintf("turn %d", n.Turn)
	}
	return row
}

// noteID is a note's number as every surface prints it, which is also how
// `/notes drop` takes one.
func noteID(id int64) string { return "n" + strconv.FormatInt(id, 10) }

// notesSubject is what the header says the screen is over: how many notes,
// and how many agents wrote them, because a notebook one agent filled and a
// notebook a fan-out filled are read differently.
func notesSubject(notes []notebook.Note) string {
	authors := map[string]bool{}
	for _, n := range notes {
		authors[n.Author] = true
	}
	return plural(len(notes), "note") + " · " + plural(len(authors), "agent")
}

// turnNotesClause is what the turn's delegates left in the notebook, for the
// turn's close. A child's report comes back to the model and is never shown
// in full; a note is the other half — what a sibling will need — and without
// this line the person would have no way of knowing anything was written at
// all until they opened /notes.
//
// The orchestrator's own notes are left out: the close reports what came
// back from the fan-out, and what the session wrote for itself is already in
// front of the person as the rows that wrote it.
func (m Model) turnNotesClause() string {
	all := m.notebook.List()
	notes := notebook.WrittenIn(all, m.turnCount, notebook.Orchestrator)
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
	clause := plural(len(notes), "note") + " from " + strings.Join(authors, ", ")
	// And what is waiting on the screen, stated only where it says something
	// the count before it does not: a turn whose notes are the only unread
	// ones has already reported them, and a field that reports nothing is a
	// field the row leaves out
	// (docs/interface/principles.md#a-stat-that-cannot-be-reported-is-left-out).
	unread := len(notebook.WrittenAfter(all, m.notesSeen, notebook.Orchestrator))
	if unread > len(notes) {
		clause += " · " + strconv.Itoa(unread) + " unread"
	}
	return clause
}
