package chat

// /notes: the notebook as the person reads and corrects it.

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/notebook"
	"github.com/rfizzle/shhh/internal/todo/run"
)

// notesModel is a sized session whose notebook a fan-out and the
// orchestrator have both written in.
func notesModel(t *testing.T, width int) Model {
	t.Helper()
	m := frameModel(t, width, 30).WithNotebook(notebook.New(nil))
	m.notebook.SetTurn(4)
	_, _, _ = m.notebook.Write("researcher-1", "Where the goldens live", "testdata/golden")
	_, _, _ = m.notebook.Write(notebook.Orchestrator, "The freeze is the target", "better, not wider")
	_, _, _ = m.notebook.Write("researcher-1", "And the widths", "80, 110, 130")
	return m
}

// The screen groups by the agent that wrote each entry, because a fan-out's
// notes arrive interleaved and the question somebody asks their own session
// is who found what.
func TestNotes_TheScreenGroupsByTheAgentThatWroteEachNote(t *testing.T) {
	m := sendText(t, notesModel(t, 110), "/notes")
	if m.state != stateNotes || m.notes == nil {
		t.Fatalf("/notes left the session in state %v", m.state)
	}
	view := strings.Join(m.notesLines(), "\n")
	if strings.Index(view, "researcher-1") > strings.Index(view, notebook.Orchestrator) {
		t.Fatalf("authors are not in first-write order:\n%s", view)
	}
	headers := 0
	for _, line := range strings.Split(ansi.Strip(view), "\n") {
		if strings.HasPrefix(line, "researcher-1") {
			headers++
		}
	}
	if headers != 1 {
		t.Fatalf("an author heads the list %d times:\n%s", headers, view)
	}
	for _, want := range []string{
		"/notes",
		"3 notes · 2 agents", // what the header says the screen is over
		"Where the goldens live",
		"And the widths",
		"back", // the way out, in the header
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the screen is missing %q:\n%s", want, view)
		}
	}
}

// An empty notebook is a sentence rather than an empty screen: there is
// nothing to point at.
func TestNotes_AnEmptyNotebookPrintsTheNotice(t *testing.T) {
	m := frameModel(t, 100, 30).WithNotebook(notebook.New(nil))
	m = sendText(t, m, "/notes")
	if m.state == stateNotes {
		t.Fatal("an empty notebook opened the screen anyway")
	}
	if !strings.Contains(strings.Join(m.renderHistoryLines(), "\n"), "The notebook is empty") {
		t.Error("nothing said the notebook was empty")
	}
}

// `[d]` asks before it takes anything, and the screen stays up afterwards:
// the reader is walking a list and correcting it.
func TestNotes_DropAsksAndKeepsTheScreen(t *testing.T) {
	m := sendText(t, notesModel(t, 110), "/notes")
	m.notes.Focus = 0
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	m = updated.(Model)
	if m.notebook.Len() != 3 {
		t.Fatalf("[d] dropped a note before it was confirmed: %d left", m.notebook.Len())
	}
	if !strings.Contains(strings.Join(m.notesLines(), "\n"), `Drop "Where the goldens live"?`) {
		t.Errorf("the confirm did not name the note:\n%s", strings.Join(m.notesLines(), "\n"))
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m = updated.(Model)
	if m.state != stateNotes {
		t.Fatalf("the drop left the screen: state %v", m.state)
	}
	if m.notebook.Len() != 2 {
		t.Fatalf("the confirmed drop left %d notes", m.notebook.Len())
	}
	if !strings.Contains(strings.Join(m.notesLines(), "\n"), "Dropped note n1.") {
		t.Error("the screen did not say what went")
	}
}

// Declining takes nothing, which is what esc means everywhere
// (invariant 3).
func TestNotes_DecliningTheDropTakesNothing(t *testing.T) {
	m := sendText(t, notesModel(t, 110), "/notes")
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	updated, _ = updated.(Model).Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)
	if m.state != stateNotes {
		t.Fatalf("declining the drop left the screen: state %v", m.state)
	}
	if m.notebook.Len() != 3 {
		t.Fatalf("declining the drop took %d notes", 3-m.notebook.Len())
	}
}

// `/notes clear` asks over the whole notebook, on the screen, because kill
// asks and this takes more.
func TestNotes_ClearAsksBeforeItEmptiesTheNotebook(t *testing.T) {
	m := sendText(t, notesModel(t, 110), "/notes clear")
	if m.state != stateNotes {
		t.Fatalf("/notes clear left the session in state %v", m.state)
	}
	if m.notebook.Len() != 3 {
		t.Fatalf("clear emptied the notebook before it asked: %d left", m.notebook.Len())
	}
	if !strings.Contains(strings.Join(m.notesLines(), "\n"), "Drop 3 notes?") {
		t.Errorf("the question did not count what it would take:\n%s",
			strings.Join(m.notesLines(), "\n"))
	}
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m = updated.(Model)
	if m.notebook.Len() != 0 {
		t.Fatalf("the confirmed clear left %d notes", m.notebook.Len())
	}
	if !strings.Contains(strings.Join(m.notesLines(), "\n"), "Dropped 3 notes.") {
		t.Error("the screen did not say what went")
	}
}

// `[enter]` opens the note whole, and esc comes back to the list rather than
// to the prompt: reading one note is not leaving the notebook.
func TestNotes_EnterReadsTheNoteWhole(t *testing.T) {
	m := sendText(t, notesModel(t, 110), "/notes")
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if m.state != stateOutputFull || m.fullOutput == nil {
		t.Fatalf("enter on a note left the session in state %v", m.state)
	}
	if !strings.Contains(strings.Join(m.fullOutput.Lines, "\n"), "80, 110, 130") {
		t.Errorf("the viewer holds %q", m.fullOutput.Lines)
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if got := updated.(Model).state; got != stateNotes {
		t.Errorf("esc from the note went to state %v, not back to the list", got)
	}
}

// Dropping is the person's only, and the prompt is still a route to it: no
// agent has a tool that reaches Delete, so the screen's key and this command
// are the whole of it.
func TestNotes_DropByNumberStillWorksFromThePrompt(t *testing.T) {
	m := notesModel(t, 100)
	if got := m.dropNoteByName([]string{"n2"}); got != "Dropped note n2." {
		t.Fatalf("drop said %q", got)
	}
	if m.notebook.Len() != 2 {
		t.Fatalf("drop left %d notes", m.notebook.Len())
	}
	if got := m.dropNoteByName([]string{"n2"}); !strings.HasPrefix(got, "Error:") {
		t.Fatalf("dropping a note twice said %q", got)
	}
	if got := m.dropNoteByName(nil); !strings.HasPrefix(got, "Usage:") {
		t.Fatalf("a bare drop said %q", got)
	}
	m = sendText(t, m, "/notes burn")
	if !strings.Contains(strings.Join(m.renderHistoryLines(), "\n"), "Usage:") {
		t.Error("an unknown word said nothing about the usage")
	}
}

// The unread count is what has been written since the screen was last
// opened, and it is stated only where it says something the turn's own count
// does not.
func TestNotes_TheCloseCountsWhatHasNotBeenRead(t *testing.T) {
	m := notesModel(t, 100)
	m.turnCount = 4
	if got := m.turnNotesClause(); got != "2 notes from researcher-1" {
		t.Fatalf("the close said %q before anything was read", got)
	}

	// A later turn's fan-out writes one note, and the two from turn 4 are
	// still waiting on the screen.
	m.turnCount = 5
	m.notebook.SetTurn(5)
	_, _, _ = m.notebook.Write("writer-1", "The patch is in loop.go", "one hunk")
	if got := m.turnNotesClause(); got != "1 note from writer-1 · 3 unread" {
		t.Fatalf("the close said %q", got)
	}

	// Opening the screen reads them, so the next turn reports its own and
	// nothing else.
	opened, _ := m.openNotes(false)
	m = opened.(Model)
	m.turnCount = 6
	m.notebook.SetTurn(6)
	_, _, _ = m.notebook.Write("writer-1", "And the test", "loop_test.go")
	if got := m.turnNotesClause(); got != "1 note from writer-1" {
		t.Fatalf("the close said %q after the notebook had been read", got)
	}
}

// A coding session has the notebook now, so /notes is offered there too.
func TestNotesIsOfferedInBothSessions(t *testing.T) {
	for _, tc := range []struct {
		what string
		m    Model
	}{
		{"a coding session", turnModel(t).WithNotebook(notebook.New(nil))},
		{"a conversation", turnModel(t).WithConversation().WithNotebook(notebook.New(nil))},
	} {
		if tc.m.unavailableCommand("/notes") {
			t.Errorf("%s does not offer /notes", tc.what)
		}
		opened, _ := tc.m.notesCommand(nil)
		shown := opened.(Model)
		if got := strings.Join(shown.renderHistoryLines(), "\n"); strings.Contains(got, "no notebook") {
			t.Errorf("%s has no notebook: %q", tc.what, got)
		}
	}
}

// Binding a slot is also what tells the notebook which turn is open, and it
// happens after the counter has caught up with what the slot has already
// been through — a resumed session that stamped notes with the turn it had
// before the catch-up would file a fan-out under a turn that has closed.
func TestBindSlotSyncsTheNotebooksTurn(t *testing.T) {
	m := turnModel(t)
	m = m.WithNotebook(notebook.New(nil))
	m.turnCount = 7
	m.bindSlot()
	n, _, err := m.notebook.Write("researcher-1", "Found it", "in loop.go")
	if err != nil {
		t.Fatal(err)
	}
	if n.Turn != 7 {
		t.Fatalf("a note written after the bind was stamped turn %d", n.Turn)
	}
}

// notesBackend is a notebook backend a resume can be tested against: notes
// the slot already holds, and what Bind wrote back into it.
type notesBackend struct {
	notes  map[string][]notebook.Note
	nextID int64
}

func (b *notesBackend) SaveNote(session string, n notebook.Note) (int64, error) {
	if b.notes == nil {
		b.notes = map[string][]notebook.Note{}
	}
	b.nextID++
	n.ID = b.nextID
	b.notes[session] = append(b.notes[session], n)
	return n.ID, nil
}

func (b *notesBackend) LoadNotes(session string) ([]notebook.Note, error) {
	return b.notes[session], nil
}

func (b *notesBackend) DeleteNote(string, int64) error { return nil }

// A coding session resumed into a slot comes back with that slot's notebook,
// and the turn a note is stamped with afterwards is the turn the resumed
// session is actually on — not the one the counter held before it caught up
// with what the slot had already been through.
func TestResumeBringsBackACodingSessionsNotebook(t *testing.T) {
	b := &notesBackend{}
	_, _ = b.SaveNote("chat-earlier", notebook.Note{
		Author: "researcher-1", Title: "Where the goldens live", Body: "testdata/golden", Turn: 3})

	m := turnModel(t).WithNotebook(notebook.New(b))
	if m.conversation {
		t.Fatal("this is the coding session's case")
	}
	if m.notebook.Len() != 0 {
		t.Fatalf("a fresh session started with %d notes", m.notebook.Len())
	}

	m.sessionName = "chat-earlier"
	m.turnCount = 9
	m.bindSlot()

	notes := m.notebook.List()
	if len(notes) != 1 || notes[0].Title != "Where the goldens live" {
		t.Fatalf("the resumed slot's notebook came back as %+v", notes)
	}
	if notes[0].Turn != 3 {
		t.Errorf("a resumed note lost its turn: %d", notes[0].Turn)
	}
	n, _, err := m.notebook.Write("reviewer-1", "And the widths", "80, 110, 130")
	if err != nil {
		t.Fatal(err)
	}
	if n.Turn != 9 {
		t.Errorf("a note written after the resume was stamped turn %d", n.Turn)
	}
	if got := b.notes["chat-earlier"]; len(got) != 2 || got[1].Turn != 9 {
		t.Errorf("the note did not reach the slot it belongs to: %+v", got)
	}
}

// The read mark is the session's and is not written to the slot, so a slot
// that comes back full comes back unread: "unread" is a promise that the
// reader has had these notes in front of them, and the only surface that can
// make it is the one this session drew.
func TestNotes_AResumedNotebookIsUnreadUntilTheScreenIsOpened(t *testing.T) {
	b := &notesBackend{}
	_, _ = b.SaveNote("chat-earlier", notebook.Note{
		Author: "researcher-1", Title: "Where the goldens live", Body: "testdata/golden", Turn: 3})
	_, _ = b.SaveNote("chat-earlier", notebook.Note{
		Author: "researcher-1", Title: "And the widths", Body: "80, 110, 130", Turn: 3})

	m := frameModel(t, 100, 30).WithNotebook(notebook.New(b))
	m.sessionName = "chat-earlier"
	m.turnCount = 9
	m.bindSlot()
	_, _, _ = m.notebook.Write("writer-1", "The patch is in loop.go", "one hunk")
	if got := m.turnNotesClause(); got != "1 note from writer-1 · 3 unread" {
		t.Fatalf("the close said %q on a slot nobody has opened the screen on", got)
	}

	opened, _ := m.openNotes(false)
	m = opened.(Model)
	m.turnCount = 10
	m.notebook.SetTurn(10)
	_, _, _ = m.notebook.Write("writer-1", "And the test", "loop_test.go")
	if got := m.turnNotesClause(); got != "1 note from writer-1" {
		t.Fatalf("the close said %q after the resumed notebook had been read", got)
	}
}

// The backlog runner asks the session whether there is a notebook, because a
// finish that writes the run up has nowhere to put it without one. A coding
// session now answers yes, so a profile that ends in a write-up ends the same
// way in both sessions instead of quietly archiving in one of them.
func TestARunsWriteUpIsReadInEitherSession(t *testing.T) {
	_, research, err := run.BuiltinProfile("research")
	if err != nil {
		t.Fatalf("research profile: %v", err)
	}
	if ending, ok := research.Ending(); !ok || ending != run.FinishNote {
		t.Fatalf("this test needs a profile that ends in a write-up, got %v", ending)
	}
	for _, tc := range []struct {
		what string
		m    Model
	}{
		{"a coding session", turnModel(t).WithNotebook(notebook.New(nil))},
		{"a conversation", turnModel(t).WithConversation().WithNotebook(notebook.New(nil))},
	} {
		steps := run.Options{Pipeline: research, Notebook: tc.m.notebook != nil}.Steps()
		if ending, _ := steps.Ending(); ending != run.FinishNote {
			t.Errorf("%s turned the write-up into %v", tc.what, ending)
		}
	}
}
