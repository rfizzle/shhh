package chat

// /notes: the notebook as the person reads and corrects it.

import (
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/notebook"
	"github.com/rfizzle/shhh/internal/todo/run"
)

// The listing groups by the agent that wrote each entry, because a fan-out's
// notes arrive interleaved and the question somebody asks their own session
// is who found what. Dropping is the person's only: no agent has a tool that
// reaches it, so this is the whole route.
func TestNotesCommandListsByAuthorAndIsTheOnlyRouteToDropping(t *testing.T) {
	m := turnModel(t)
	m = m.WithNotebook(notebook.New(nil))
	if got := m.notesCommand(nil); !strings.Contains(got, "The notebook is empty.") {
		t.Fatalf("an empty notebook said %q", got)
	}

	_, _, _ = m.notebook.Write("researcher-1", "Where the goldens live", "testdata/golden")
	_, _, _ = m.notebook.Write(notebook.Orchestrator, "The freeze is the target", "better, not wider")
	_, _, _ = m.notebook.Write("researcher-1", "And the widths", "80, 110, 130")

	listed := m.notesCommand(nil)
	if strings.Index(listed, "# researcher-1") > strings.Index(listed, "# "+notebook.Orchestrator) {
		t.Fatalf("authors are not in first-write order:\n%s", listed)
	}
	if strings.Count(listed, "# researcher-1") != 1 {
		t.Fatalf("an author was listed twice:\n%s", listed)
	}
	if strings.Index(listed, "And the widths") > strings.Index(listed, "The freeze is the target") {
		t.Fatalf("a note was not filed under its author:\n%s", listed)
	}

	if got := m.notesCommand([]string{"drop", "n2"}); got != "Dropped note n2." {
		t.Fatalf("drop said %q", got)
	}
	if m.notebook.Len() != 2 {
		t.Fatalf("drop left %d notes", m.notebook.Len())
	}
	if got := m.notesCommand([]string{"drop", "n2"}); !strings.HasPrefix(got, "Error:") {
		t.Fatalf("dropping a note twice said %q", got)
	}
	if got := m.notesCommand([]string{"clear"}); got != "Dropped 2 notes." {
		t.Fatalf("clear said %q", got)
	}
	if m.notebook.Len() != 0 {
		t.Fatalf("clear left %d notes", m.notebook.Len())
	}
	if got := m.notesCommand([]string{"burn"}); !strings.HasPrefix(got, "Usage:") {
		t.Fatalf("an unknown word said %q", got)
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
		if got := tc.m.notesCommand(nil); strings.Contains(got, "no notebook") {
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
