package chat

// Housekeeping in the saved-chat picker
// (docs/capabilities/sessions-and-memory.md#housekeeping). The picker behind
// bare /load and /chats is the generic select card with two keys the card
// only offers and this file answers: [x] arms an inline confirm under the
// card, [r] opens a one-line rename row there. Both act on the focused row
// and both leave the picker open, so a reader tidying a dozen sessions does
// not reopen it a dozen times.
//
// The confirm is the product's own inline one
// (docs/interface/surfaces.md#the-inline-confirm): it names the chat and how
// many branches go with it, and enter is No. The rename row keeps the old
// name on esc. The chat the session is in is the one row neither key can
// act on — its slot is the one every autosave is about to write to, and a
// key that deleted it would be racing the save — so the row says so with ⊘
// and a phrase rather than merely dimming
// (docs/interface/surfaces.md#selectors). Because /chats waits for the turn
// to finish, that rule also covers a chat with a turn in flight: the only
// chat that can be running is the one the session is in.

import (
	"errors"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// chatOps is the picker's housekeeping state: the entries the rows were
// built from, and whichever of the confirm, the rename row or a notice is
// open under the card. It is a value on the model, like every other piece
// of surface state, so a model copied before a key still describes the
// picker before it. active is set only while the saved-chat picker is
// showing; entries outlive it by one apply, which reads them after the
// picker has closed.
type chatOps struct {
	active  bool
	entries []storage.ChatListEntry
	// confirm is the armed delete confirm and target the chat it names.
	confirm *components.Confirm
	target  string
	// rename is the open rename row, prefilled with target's current name.
	rename *textinput.Model
	// notice is the one-line answer to a key that could not act — [x] on
	// the chat the session is in — drawn where the confirm would be. It
	// goes on the next key.
	notice string
}

// protectedPhrase is why the session's own row cannot be acted on.
const protectedPhrase = "the chat you are in"

// livePhrase is why a slot another running session holds cannot be opened
// from here: the conversation in it is still being written, and the next
// autosave over there takes the slot back
// (docs/capabilities/sessions-and-memory.md#a-session-knows-it-is-not-alone).
const livePhrase = "open in another session"

// chatPickActions are the housekeeping keys the picker offers.
var chatPickActions = []keys.Binding{keys.Select.Delete, keys.Select.Rename}

// chatDesc is the continuation every saved-chat listing shows: the generated
// title when there is one, then the turn count and when it was last written.
// The title leads because it is the thing that tells two timestamps apart.
func chatDesc(e storage.ChatListEntry) string {
	desc := sessionDesc(e.Turns, e.UpdatedAt)
	if e.Title != "" {
		return e.Title + " · " + desc
	}
	return desc
}

// chatPickOptions builds the picker's rows. Two kinds of row cannot be
// opened, and both are still listed — fold, never hide — with a meta field
// saying why: the session's own slot, and a slot another running session is
// autosaving into, which that session's next save would take straight back.
func (m Model) chatPickOptions(entries []storage.ChatListEntry) ([]components.SelectOption, int) {
	opts := make([]components.SelectOption, len(entries))
	focus := 0
	for i, e := range entries {
		opts[i] = components.SelectOption{Label: e.Name, Desc: chatDesc(e)}
		switch {
		case e.Name == m.sessionName:
			opts[i].Dim = true
			opts[i].Meta = protectedPhrase
			focus = i
		case e.Live:
			opts[i].Dim = true
			opts[i].Meta = livePhrase
		}
	}
	return opts, focus
}

// openChatPick opens the saved-chat picker behind bare /load and /chats:
// enter loads the highlighted chat, esc keeps the current one, and the two
// housekeeping keys act on the focused row. It reports false when there is
// nothing to pick, leaving the caller on the text path.
func (m Model) openChatPick() (tea.Model, tea.Cmd, bool) {
	if m.db == nil {
		return m, nil, false
	}
	entries, err := m.db.ListChats()
	if err != nil || len(entries) == 0 {
		return m, nil, false
	}
	opts, focus := m.chatPickOptions(entries)
	// The one card in the family that opens as a list rather than as a
	// search: its rows carry [x] and [r], and a session's saved chats are
	// short enough to walk. [/] turns it into a search when the list is long.
	model, cmd := m.openPicker("Load a saved chat", opts, focus, func(m *Model, idx int) string {
		// The rows are rebuilt after every delete and rename, so the apply
		// reads the list as it stands rather than the one it was opened
		// over.
		if idx < 0 || idx >= len(m.chats.entries) {
			return ""
		}
		e := m.chats.entries[idx]
		if e.Name == m.sessionName {
			return fmt.Sprintf("%q is %s.", e.Name, protectedPhrase)
		}
		if e.Live {
			return fmt.Sprintf("%q is %s — its conversation is still being written there.", e.Name, livePhrase)
		}
		return m.loadChatByName(e.Name)
	})
	mm := model.(Model)
	mm.picker.Actions = chatPickActions
	mm.chats = chatOps{active: true, entries: entries}
	return mm, cmd, true
}

// chatPickLines is what the picker draws under the card: the armed confirm,
// the open rename row, or the notice a refused key left behind.
func (m Model) chatPickLines() []string {
	ops := m.chats
	if !ops.active {
		return nil
	}
	width := m.contentWidth()
	switch {
	case ops.confirm != nil:
		return []string{ops.confirm.View(width)}
	case ops.rename != nil:
		hint := keys.Shown(keys.Select.Take) + " renames · " + keys.Shown(keys.Select.Cancel) + " keeps the name"
		return []string{ops.rename.View(), sty.Hint.Dim.Render(hint)}
	case ops.notice != "":
		return []string{sty.Hint.Dim.Render(ops.notice)}
	}
	return nil
}

// pickCursor is where the terminal's cursor stands inside the picker panel,
// in the panel's own cells: the rename row when one is open under the card,
// and otherwise the card's own filter row. Only one of the two is ever being
// typed into — opening the rename row is what takes the keyboard off the
// filter.
func (m Model) pickCursor(width int) *tea.Cursor {
	if m.picker == nil {
		return nil
	}
	if row := m.chats.rename; row != nil {
		cur := row.Cursor()
		if cur == nil {
			return nil
		}
		// The rename row is drawn under the card, so it starts where the
		// card's rows end.
		cur.Y += len(strings.Split(m.picker.View(width), "\n"))
		return cur
	}
	return m.picker.Cursor(width)
}

// focusedChat is the entry under the picker's pointer, mapped back through
// the filter to the list it was built from.
func (m Model) focusedChat() (storage.ChatListEntry, bool) {
	if !m.chats.active || m.picker == nil {
		return storage.ChatListEntry{}, false
	}
	row := m.picker.Focus
	if row < 0 || row >= len(m.pickerIndex) {
		return storage.ChatListEntry{}, false
	}
	idx := m.pickerIndex[row]
	if idx < 0 || idx >= len(m.chats.entries) {
		return storage.ChatListEntry{}, false
	}
	return m.chats.entries[idx], true
}

// updateChatOps answers the housekeeping keys while the saved-chat picker is
// showing. It reports false for a key that is the card's own, which the
// caller then hands to the card.
func (m Model) updateChatOps(msg tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	ops := &m.chats
	if !ops.active {
		return m, nil, false
	}
	ops.notice = ""
	switch {
	case ops.confirm != nil:
		done, result := ops.confirm.Update(msg)
		if !done {
			return m, nil, true
		}
		target := ops.target
		ops.confirm, ops.target = nil, ""
		if confirmed, _ := result.(bool); confirmed {
			m.deleteChat(target)
		}
		m.syncViewport()
		return m, nil, true
	case ops.rename != nil:
		return m.updateChatRename(msg)
	}
	// The bare letters are text while the query line is open, the reading
	// every card in the family makes.
	if m.picker.Filtering {
		return m, nil, false
	}
	switch {
	case keys.Match(msg, keys.Select.Delete):
		e, ok := m.focusedChat()
		if !ok {
			return m, nil, true
		}
		if e.Name == m.sessionName {
			ops.notice = fmt.Sprintf("⊘ %q is %s — it cannot be deleted or renamed.", e.Name, protectedPhrase)
			m.syncViewport()
			return m, nil, true
		}
		ops.target = e.Name
		ops.confirm = &components.Confirm{Prompt: m.deleteChatPrompt(e.Name)}
		m.syncViewport()
		return m, nil, true
	case keys.Match(msg, keys.Select.Rename):
		e, ok := m.focusedChat()
		if !ok {
			return m, nil, true
		}
		if e.Name == m.sessionName {
			ops.notice = fmt.Sprintf("⊘ %q is %s — it cannot be deleted or renamed.", e.Name, protectedPhrase)
			m.syncViewport()
			return m, nil, true
		}
		ops.target = e.Name
		ops.rename = newRenameRow(e.Name, m.contentWidth())
		m.syncViewport()
		return m, nil, true
	}
	return m, nil, false
}

// deleteChatPrompt is the confirm's question: the chat by name, the branches
// that go with it, and what is left — nothing on disk changes, because a
// saved chat is only rows in the store.
func (m Model) deleteChatPrompt(name string) string {
	branches := 0
	if m.db != nil {
		branches, _ = m.db.CountChatBranches(name)
	}
	with := ""
	switch {
	case branches == 1:
		with = " and its 1 branch"
	case branches > 1:
		with = fmt.Sprintf(" and its %d branches", branches)
	}
	return fmt.Sprintf("Delete %q%s? Files on disk are untouched.", name, with)
}

// newRenameRow is the one-line rename field, prefilled with the current name
// and the cursor at its end so a suffix is one keystroke away.
func newRenameRow(name string, width int) *textinput.Model {
	ti := textinput.New()
	ti.Prompt = "rename ▸ "
	ti.CharLimit = 0
	ti.SetValue(name)
	ti.CursorEnd()
	ti.SetWidth(max(width-len(ti.Prompt)-1, 8))
	// Like the draft above it, the row hands the terminal its own cursor
	// back (docs/interface/surfaces.md#the-input-frame); the panel reports
	// the coordinate from the row it drew it on.
	ti.SetVirtualCursor(false)
	ti.Focus()
	return &ti
}

// updateChatRename routes keys to the open rename row: enter commits, esc
// keeps the old name, everything else types.
func (m Model) updateChatRename(msg tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	ops := &m.chats
	switch pressed := msg.String(); {
	case keys.Is(pressed, keys.Select.Cancel):
		ops.rename, ops.target = nil, ""
		m.syncViewport()
		return m, nil, true
	case keys.Is(pressed, keys.Select.Take):
		target, next := ops.target, strings.TrimSpace(ops.rename.Value())
		ops.rename, ops.target = nil, ""
		if next != "" && next != target {
			m.renameChat(target, next)
		}
		m.syncViewport()
		return m, nil, true
	}
	updated, cmd := ops.rename.Update(msg)
	ops.rename = &updated
	return m, cmd, true
}

// deleteChat removes a saved chat and rebuilds the rows. The note goes to the
// transcript, which stays visible above the picker.
func (m *Model) deleteChat(name string) {
	if err := m.db.DeleteChat(name); err != nil {
		m.appendEntry(entry{kind: entrySystem, text: "Could not delete: " + err.Error()})
	} else {
		m.appendEntry(entry{kind: entrySystem, text: fmt.Sprintf("Deleted chat %q.", name)})
	}
	m.refreshChatPick()
}

// renameChat gives a saved chat a new name and rebuilds the rows. A
// collision is refused by the store and reported by name, so the reader
// knows which name to pick differently.
func (m *Model) renameChat(oldName, newName string) {
	err := m.db.RenameChat(oldName, newName)
	var exists storage.ChatExistsError
	switch {
	case errors.As(err, &exists):
		m.appendEntry(entry{kind: entrySystem, text: fmt.Sprintf("Could not rename: a chat named %q already exists.", exists.Name)})
	case err != nil:
		m.appendEntry(entry{kind: entrySystem, text: "Could not rename: " + err.Error()})
	default:
		m.appendEntry(entry{kind: entrySystem, text: fmt.Sprintf("Renamed chat %q to %q.", oldName, newName)})
	}
	m.refreshChatPick()
}

// refreshChatPick re-reads the store and rebuilds the picker over it,
// keeping the query and the pointer where they were. A store with nothing
// left closes the picker: a card with no rows has nothing to offer.
func (m *Model) refreshChatPick() {
	m.viewport.SetLines(m.renderHistoryLines())
	m.viewport.GotoBottom()
	entries, err := m.db.ListChats()
	if err != nil || len(entries) == 0 {
		m.closePicker()
		if err == nil {
			m.appendEntry(entry{kind: entrySystem, text: "No saved chats."})
			m.viewport.SetLines(m.renderHistoryLines())
			m.viewport.GotoBottom()
		}
		m.syncViewport()
		return
	}
	opts, _ := m.chatPickOptions(entries)
	m.chats.entries = entries
	m.pickerAll = opts
	m.picker.Total = selectableOptions(opts)
	focus := m.picker.Focus
	m.refilterPicker()
	m.picker.Focus = min(focus, max(len(m.picker.Options)-1, 0))
	m.syncViewport()
}

// closePicker drops the picker and everything hanging off it, handing the
// screen back.
func (m *Model) closePicker() {
	m.picker = nil
	m.pickerApply = nil
	m.pickerAll = nil
	m.pickerIndex = nil
	// The entries stay for the apply that runs after this; everything
	// else about the housekeeping goes with the picker.
	m.chats = chatOps{entries: m.chats.entries}
	m.leaveSurface()
}
