package cli

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/cli/report"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// livePhrase is why a slot another running session is autosaving into
// cannot be opened: the conversation in it is still being written, and that
// session's next save takes the slot straight back from whoever loaded it.
// One mark, one sentence, and every door that draws the mark says it. A
// picker that marks a row and opens it anyway makes the mark mean two
// things in two places, and the reader who trusted it is the one who loses
// the conversation.
// See docs/capabilities/sessions-and-memory.md#a-session-knows-it-is-not-alone.
const livePhrase = "open in another session"

// chatBrowseRows is the saved chats as the browser lists them: the name, what
// the conversation was about and where it left off, how big it is, and what
// deleting it would take along.
func chatBrowseRows(db *storage.DB, entries []storage.ChatListEntry) []components.ChatRow {
	rows := make([]components.ChatRow, len(entries))
	for i, e := range entries {
		rows[i] = components.ChatRow{
			ID:      e.Name,
			Name:    e.Name,
			Title:   e.Title,
			Summary: e.Summary,
			Turns:   countOf(e.Turns, "turn", "turns"),
			When:    e.UpdatedAt.Local().Format("Jan 2 15:04"),
			Updated: e.UpdatedAt.Local().Format("2006-01-02 15:04:05"),
		}
		if e.Live {
			// The row keeps its place. Reading it, renaming it and
			// deleting it are all still the reader's to do; the one thing
			// it will not do is open, because the other session's next
			// autosave takes the slot back and the conversation loaded
			// here goes with it. Naming the slot is still the way in, and
			// that is the flag's business rather than this list's.
			rows[i].Mark = livePhrase
			rows[i].Refused = fmt.Sprintf(
				"%q is %s — its conversation is still being written there.", e.Name, livePhrase)
		}
		if n, err := db.CountChatBranches(e.Name); err == nil && n > 0 {
			rows[i].Deleting = "and its " + branchCount(n)
		}
	}
	return rows
}

// branchCount is n branches, in words.
func branchCount(n int) string {
	if n == 1 {
		return "1 branch"
	}
	return fmt.Sprintf("%d branches", n)
}

// defaultChatsWidth is what the browser is drawn at before the terminal has
// said how wide it is — the working width the artboard is drawn at.
const defaultChatsWidth = 130

// chatsModel hosts the saved-chat browser
// (docs/interface/surfaces.md#the-supporting-screens). It owns everything the
// screen deliberately does not: what a conversation is, what holds a slot,
// how many branches go with one, and when any of it reaches the store.
//
// The screen resolves a key to a components.ChatCommand; the host carries it
// out, says so in the notice line, and hands back fresh rows. `[enter]` is the
// exception and closes the screen, because opening a conversation hands the
// terminal to a session.
type chatsModel struct {
	db      *storage.DB
	entries []storage.ChatListEntry
	result  components.ChatResult

	screen components.ChatScreen
}

func newChatsModel(db *storage.DB, entries []storage.ChatListEntry) *chatsModel {
	m := &chatsModel{db: db, entries: entries}
	m.refresh()
	return m
}

// answer carries out the housekeeping a key asked for and keeps what the
// screen closed with, which is read once the terminal has been given back.
// The notice is not cleared here, unlike the other screens' hosts: this
// screen writes its own on the key it refused, and clearing after its Update
// has run would wipe the sentence that key just produced. It clears it
// itself, on the next keystroke.
func (m *chatsModel) answer(done bool, result components.ChatResult) tea.Cmd {
	if result.Do != nil {
		m.apply(*result.Do)
	}
	if !done {
		return nil
	}
	m.result = result
	return tea.Quit
}

// apply carries out one command against the store and re-reads the listing,
// so the screen redraws from the store rather than from what it thinks
// changed.
func (m *chatsModel) apply(command components.ChatCommand) {
	switch command.Act {
	case components.ChatRename:
		if err := m.db.RenameChat(command.ID, command.Name); err != nil {
			m.screen.Notice = "rename: " + err.Error()
			return
		}
		m.screen.Notice = fmt.Sprintf("renamed %q to %q", command.ID, command.Name)
	case components.ChatDelete:
		if err := m.db.DeleteChat(command.ID); err != nil {
			m.screen.Notice = "delete: " + err.Error()
			return
		}
		m.screen.Notice = fmt.Sprintf("deleted %q", command.ID)
	}
	entries, err := m.db.ListChats()
	if err != nil {
		// The rows it already has stay: a browser that emptied itself on a
		// failed read would look like a store that had lost everything.
		m.screen.Notice = "list: " + err.Error()
		return
	}
	m.entries = entries
	if m.screen.Focus >= len(m.entries) {
		m.screen.Focus = max(len(m.entries)-1, 0)
	}
	m.refresh()
}

// refresh rebuilds every row and the header subject from the listing the host
// is holding.
func (m *chatsModel) refresh() {
	m.screen.Rows = chatBrowseRows(m.db, m.entries)
	m.screen.Subject = countOf(len(m.entries), "conversation", "conversations")
}

// pickSavedChat shows the saved-chat browser and returns the chosen session
// name, or "" if the user backed out.
func pickSavedChat(db *storage.DB) (string, error) {
	entries, err := db.ListChats()
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		_ = report.Fprintln(os.Stderr, report.Empty("nothing saved yet", "shhh chat"))
		return "", nil
	}

	m := newChatsModel(db, entries)
	if _, err := newProgram(newScreenModel(&m.screen, defaultChatsWidth, m.answer)).Run(); err != nil {
		return "", err
	}
	if !m.result.Open {
		return "", nil
	}
	return m.result.ID, nil
}
