package chat

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/ui/golden"
)

// chatsPicker opens the saved-chat picker over the named chats and puts the
// pointer on the one asked for.
func chatsPicker(t *testing.T, focus string, names ...string) Model {
	t.Helper()
	m := sendText(t, chatPickModel(t, names...), "/chats")
	idx := pickIndex(t, m, focus)
	for m.picker.Focus < idx {
		m = press(t, m, "j")
	}
	for m.picker.Focus > idx {
		m = press(t, m, "k")
	}
	return m
}

func TestChatPick_DeleteArmsConfirmAndEnterIsNo(t *testing.T) {
	m := chatsPicker(t, "alpha", "alpha", "beta")

	m = press(t, m, "x")
	if m.chats.confirm == nil {
		t.Fatal("x should arm the delete confirm")
	}
	if !strings.Contains(m.chats.confirm.Prompt, `"alpha"`) {
		t.Fatalf("the confirm should name the chat, got %q", m.chats.confirm.Prompt)
	}
	if lines := strings.Join(m.pickerLines(), "\n"); !strings.Contains(lines, "[y/N]") {
		t.Fatalf("the confirm should be drawn under the card, got:\n%s", lines)
	}

	m = press(t, m, "enter")
	if m.chats.confirm != nil {
		t.Fatal("enter should answer the confirm")
	}
	if _, err := m.db.LoadChat("alpha"); err != nil {
		t.Fatalf("enter is No: alpha must still exist, got %v", err)
	}
	if m.state != statePick {
		t.Fatal("the picker should stay open")
	}
}

func TestChatPick_DeleteConfirmedRemovesTheChatAndKeepsThePicker(t *testing.T) {
	m := chatsPicker(t, "alpha", "alpha", "beta")
	m = press(t, m, "x")
	m = press(t, m, "y")

	if _, err := m.db.LoadChat("alpha"); err == nil {
		t.Fatal("y should delete alpha")
	}
	if m.state != statePick || m.picker == nil {
		t.Fatal("the picker should stay open with what is left")
	}
	if len(m.picker.Options) != 1 || m.picker.Options[0].Label != "beta" {
		t.Fatalf("the rows should be rebuilt without alpha, got %+v", m.picker.Options)
	}
	if !strings.Contains(lastNote(m), `Deleted chat "alpha"`) {
		t.Fatalf("the transcript should note the delete, got %q", lastNote(m))
	}
}

func TestChatPick_DeleteNamesTheBranches(t *testing.T) {
	m := chatPickModel(t, "alpha")
	tail := []provider.Message{{Role: provider.RoleUser, Content: "q"}}
	if err := m.db.SaveChatBranch("alpha", "alpha@turn2", tail); err != nil {
		t.Fatal(err)
	}
	m = sendText(t, m, "/chats")
	for m.picker.Options[m.picker.Focus].Label != "alpha" {
		m = press(t, m, "j")
	}
	m = press(t, m, "x")
	if !strings.Contains(m.chats.confirm.Prompt, "and its 1 branch?") {
		t.Fatalf("the confirm should count the branches, got %q", m.chats.confirm.Prompt)
	}
	m = press(t, m, "esc")
	if err := m.db.SaveChatBranch("alpha", "alpha@turn3", tail); err != nil {
		t.Fatal(err)
	}
	m = press(t, m, "x")
	if !strings.Contains(m.chats.confirm.Prompt, "and its 2 branches?") {
		t.Fatalf("two branches are plural, got %q", m.chats.confirm.Prompt)
	}
}

func TestChatPick_DeletingTheLastChatClosesThePicker(t *testing.T) {
	m := chatsPicker(t, "alpha", "alpha")
	m = press(t, m, "x")
	m = press(t, m, "y")
	if m.state != stateInput || m.picker != nil || m.chats.active {
		t.Fatal("a picker with no rows left should close")
	}
}

// The rename row is typed into, so it takes the terminal's cursor off the
// card's filter row above it — one keyboard, one cursor.
func TestChatPick_RenameRowTakesTheCursor(t *testing.T) {
	m := chatsPicker(t, "alpha", "alpha", "beta")
	m = press(t, m, "r")

	var cur cursorSink
	screen := strings.Split(ansi.Strip(m.paint(&cur)), "\n")
	if cur.at == nil {
		t.Fatal("the rename row owns the keyboard, so it owns the cursor")
	}
	row := -1
	for i, line := range screen {
		if strings.Contains(line, "rename ▸") {
			row = i
		}
	}
	if row < 0 {
		t.Fatalf("fixture: the rename row should be on screen:\n%s", strings.Join(screen, "\n"))
	}
	if cur.at.Y != row {
		t.Fatalf("cursor on row %d, want the rename row at %d", cur.at.Y, row)
	}
	// Prefilled with the name and the cursor left at its end, so a suffix is
	// one keystroke away.
	if want := len("rename ▸ ") + len("alpha"); cur.at.X != want {
		t.Fatalf("cursor column %d, want %d — after the prompt and the name", cur.at.X, want)
	}
}

func TestChatPick_RenameCommitsOnEnter(t *testing.T) {
	m := chatsPicker(t, "alpha", "alpha", "beta")

	m = press(t, m, "r")
	if m.chats.rename == nil {
		t.Fatal("r should open the rename row")
	}
	if m.chats.rename.Value() != "alpha" {
		t.Fatalf("the row should be prefilled with the name, got %q", m.chats.rename.Value())
	}
	if lines := strings.Join(m.pickerLines(), "\n"); !strings.Contains(lines, "rename ▸") {
		t.Fatalf("the rename row should be drawn under the card, got:\n%s", lines)
	}
	m = press(t, m, "2")
	m = press(t, m, "enter")

	if m.chats.rename != nil {
		t.Fatal("enter should close the row")
	}
	if _, err := m.db.LoadChat("alpha2"); err != nil {
		t.Fatalf("enter should rename alpha to alpha2: %v", err)
	}
	if _, err := m.db.LoadChat("alpha"); err == nil {
		t.Fatal("the old name should be gone")
	}
	if m.state != statePick || pickIndex(t, m, "alpha2") < 0 {
		t.Fatal("the picker should stay open showing the new name")
	}
}

func TestChatPick_RenameEscKeepsTheName(t *testing.T) {
	m := chatsPicker(t, "alpha", "alpha", "beta")
	m = press(t, m, "r")
	m = press(t, m, "2")
	m = press(t, m, "esc")

	if m.chats.rename != nil {
		t.Fatal("esc should close the row")
	}
	if _, err := m.db.LoadChat("alpha"); err != nil {
		t.Fatalf("esc must keep the old name: %v", err)
	}
	if m.state != statePick {
		t.Fatal("esc on the rename row leaves the picker open")
	}
}

func TestChatPick_RenameCollisionIsRefusedByName(t *testing.T) {
	m := chatsPicker(t, "alpha", "alpha", "beta")
	m = press(t, m, "r")
	for range len("alpha") {
		m = press(t, m, "backspace")
	}
	m.chats.rename.SetValue("beta")
	m = press(t, m, "enter")

	if !strings.Contains(lastNote(m), `a chat named "beta" already exists`) {
		t.Fatalf("a collision should be reported by name, got %q", lastNote(m))
	}
	if _, err := m.db.LoadChat("alpha"); err != nil {
		t.Fatalf("a refused rename keeps alpha: %v", err)
	}
}

func TestChatPick_OwnSlotCannotBeDeletedOrRenamed(t *testing.T) {
	m := chatPickModel(t, "alpha", "beta")
	m.sessionName = "beta"
	m = sendText(t, m, "/chats")
	if m.picker.Options[m.picker.Focus].Label != "beta" {
		t.Fatal("the session's own slot should be focused")
	}

	m = press(t, m, "x")
	if m.chats.confirm != nil {
		t.Fatal("x on the session's own slot must not arm a confirm")
	}
	if !strings.Contains(m.chats.notice, "⊘") || !strings.Contains(m.chats.notice, protectedPhrase) {
		t.Fatalf("the refusal should be a notice with the glyph and the phrase, got %q", m.chats.notice)
	}
	if lines := strings.Join(m.pickerLines(), "\n"); !strings.Contains(lines, protectedPhrase) {
		t.Fatalf("the notice should be drawn under the card, got:\n%s", lines)
	}
	m = press(t, m, "r")
	if m.chats.rename != nil {
		t.Fatal("r on the session's own slot must not open the rename row")
	}
	if _, err := m.db.LoadChat("beta"); err != nil {
		t.Fatalf("the slot must be untouched: %v", err)
	}
}

func TestChatPick_KeysAreTextWhileFiltering(t *testing.T) {
	m := chatsPicker(t, "alpha", "alpha", "beta")
	m = press(t, m, "/")
	m = press(t, m, "x")
	if m.chats.confirm != nil {
		t.Fatal("x typed into the filter row is text")
	}
	if m.picker.Query != "x" {
		t.Fatalf("the query should have taken the x, got %q", m.picker.Query)
	}
}

func TestChatPick_TitleLeadsTheDescription(t *testing.T) {
	m := chatPickModel(t, "alpha", "beta")
	if err := m.db.SetChatTitle("alpha", "Greeting the tests"); err != nil {
		t.Fatal(err)
	}
	m = sendText(t, m, "/chats")
	desc := m.picker.Options[pickIndex(t, m, "alpha")].Desc
	if !strings.HasPrefix(desc, "Greeting the tests · 1 turns, ") {
		t.Fatalf("the title should lead the row, got %q", desc)
	}
	if desc := m.picker.Options[pickIndex(t, m, "beta")].Desc; !strings.HasPrefix(desc, "1 turns, ") {
		t.Fatalf("an untitled row keeps the plain description, got %q", desc)
	}
}

// TestGolden_ChatPicker captures the saved-chat picker: a titled row, an
// untitled one, the session's own unavailable slot, then the delete confirm
// and the rename row under the card.
func TestGolden_ChatPicker(t *testing.T) {
	captureGolden(t, "chat-picker", "the saved-chat picker with housekeeping", goldenWidths, func(width int) []golden.Panel {
		db := rewindTestDB(t)
		msgs := []provider.Message{
			{Role: provider.RoleUser, Content: "q"},
			{Role: provider.RoleAssistant, Content: "a"},
		}
		for _, name := range []string{"2026-08-30 09:12:04", "release notes", "2026-08-31 14:02:11"} {
			if err := db.SaveChat(name, msgs); err != nil {
				t.Fatal(err)
			}
		}
		if err := db.SetChatTitle("2026-08-30 09:12:04", "Flaky retry test"); err != nil {
			t.Fatal(err)
		}
		m := frameModel(t, width, 40).WithDB(db)
		m.sessionName = "2026-08-31 14:02:11"
		// The listing is ordered by write time, which a test cannot hold
		// still; the rows are pinned so the capture is the same every run.
		at := time.Date(2026, 8, 31, 9, 4, 0, 0, time.Local)
		fixed := []storage.ChatListEntry{
			{Name: "2026-08-30 09:12:04", Title: "Flaky retry test", Turns: 1, UpdatedAt: at},
			{Name: "release notes", Turns: 4, UpdatedAt: at},
			{Name: "2026-08-31 14:02:11", Turns: 1, UpdatedAt: at},
		}
		opened := sendText(t, m, "/chats")
		opts, focus := opened.chatPickOptions(fixed)
		opened.chats.entries = fixed
		opened.pickerAll, opened.picker.Options, opened.picker.Total = opts, opts, len(opts)
		opened.picker.Focus = focus
		opened.pickerIndex = identityIndex(len(opts))

		// The card is a pointer on the model, so each panel takes a copy of
		// it before pressing anything.
		branch := func(m Model) Model {
			card := *m.picker
			m.picker = &card
			return m
		}
		listed := opened
		armed := press(t, branch(listed), "k")
		armed = press(t, armed, "x")
		renaming := press(t, branch(listed), "k")
		renaming = press(t, renaming, "r")

		return []golden.Panel{
			{Label: "titled, untitled, and the session's own slot", View: strings.Join(listed.pickerLines(), "\n")},
			{Label: "[x] armed the confirm", View: strings.Join(armed.pickerLines(), "\n")},
			{Label: "[r] opened the rename row", View: strings.Join(renaming.pickerLines(), "\n")},
		}
	})
}

// A slot another running session is autosaving into is still listed — fold,
// never hide — and enter on it says why rather than loading a conversation
// the other session's next save would take straight back.
func TestChatPick_ASlotAnotherSessionHoldsIsNotOffered(t *testing.T) {
	m := chatPickModel(t, "alpha", "beta")
	m = sendText(t, m, "/chats")
	at := time.Date(2026, 9, 4, 9, 4, 0, 0, time.Local)
	entries := []storage.ChatListEntry{
		{Name: "alpha", Turns: 1, UpdatedAt: at, Live: true},
		{Name: "beta", Turns: 1, UpdatedAt: at},
	}
	opts, focus := m.chatPickOptions(entries)
	if !opts[0].Dim || opts[0].Meta != livePhrase {
		t.Fatalf("the live row should be folded and say why, got %+v", opts[0])
	}
	if opts[1].Dim || opts[1].Meta != "" {
		t.Fatalf("a slot nobody holds should be an ordinary row, got %+v", opts[1])
	}

	m.chats.entries = entries
	m.pickerAll, m.picker.Options, m.picker.Total = opts, opts, len(opts)
	m.picker.Focus = focus
	m.pickerIndex = identityIndex(len(opts))
	m = press(t, m, "enter")
	view := ansi.Strip(m.renderHistory())
	if !strings.Contains(view, livePhrase) {
		t.Fatalf("choosing a live slot should say why it was not opened:\n%s", view)
	}
	if strings.Contains(view, "Loaded") {
		t.Fatalf("the live slot was loaded anyway:\n%s", view)
	}
}

// What the model was shown belongs to the conversation it was shown in.
// Opening somebody else's leaves a record of readings this conversation never
// made, which is exactly the evidence a full overwrite is let through on.
func TestChatLoad_ADifferentConversationEmptiesWhatWasShown(t *testing.T) {
	tools.ForgetAll()
	t.Cleanup(tools.ForgetAll)
	m := chatPickModel(t, "alpha")
	changed := shownFile(t)
	if changed() == nil {
		t.Fatal("the record should stand before anything is loaded")
	}

	// Loading the slot this session is already writing is not another
	// conversation: it read exactly what the record says.
	m.sessionName = "alpha"
	m.loadChatByName("alpha")
	if changed() == nil {
		t.Fatal("reopening this session's own slot must keep what it was shown")
	}

	m.sessionName = "2026-01-01 00:00:00"
	m.loadChatByName("alpha")
	if err := changed(); err != nil {
		t.Fatalf("another conversation's readings are not this one's: %v", err)
	}
}

// A conversation loaded over this one names the files it read and holds none
// of them. They come back as readings nobody can vouch for rather than as
// nothing at all, or the first edit to one lands on a picture that may be
// days old on the strength of its old_text alone.
func TestChatLoad_TheLoadedConversationsReadingsComeBackUnknown(t *testing.T) {
	tools.ForgetAll()
	t.Cleanup(tools.ForgetAll)
	dir := t.TempDir()
	read := filepath.Join(dir, "read.go")
	never := filepath.Join(dir, "never.go")
	for _, path := range []string{read, never} {
		if err := os.WriteFile(path, []byte("alpha\nbeta\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	db := rewindTestDB(t)
	if err := db.SaveChat("alpha", []provider.Message{
		{Role: provider.RoleUser, Content: "have a look"},
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{
			{ID: "1", Name: tools.ReadFileName, Arguments: `{"path":"` + read + `"}`},
		}},
		{Role: provider.RoleTool, ToolCallID: "1", Content: "1\talpha"},
	}); err != nil {
		t.Fatal(err)
	}

	m := readyModel(t).WithDB(db)
	m.sessionName = "2026-01-01 00:00:00"
	m.loadChatByName("alpha")

	edit := func(path string) error {
		_, err := tools.PreviewMutation(tools.EditFileName,
			[]byte(`{"path":"`+path+`","old_text":"beta","new_text":"gamma"}`))
		return err
	}
	var stale tools.StaleError
	if err := edit(read); !errors.As(err, &stale) {
		t.Fatalf("a file the loaded chat read is refused until it is read again, got %T: %v", err, err)
	}
	// A path it never read keeps the ordinary rule: the quoted text is its
	// own evidence.
	if err := edit(never); err != nil {
		t.Errorf("a file the loaded chat never read is not stale: %v", err)
	}
}

// A new conversation has been shown nothing, whatever the one before it read.
func TestChatNew_TheRecordOfWhatWasShownIsEmptied(t *testing.T) {
	tools.ForgetAll()
	t.Cleanup(tools.ForgetAll)
	m := chatPickModel(t, "alpha")
	changed := shownFile(t)

	m.startNewSession()

	if err := changed(); err != nil {
		t.Fatalf("the new conversation has been shown nothing: %v", err)
	}
}

// shownFile records a file as one this conversation has been shown and hands
// back the question that says whether that record still stands: a change to
// it is refused while it does, and allowed once nothing claims to have read
// it.
func shownFile(t *testing.T) func() error {
	t.Helper()
	path := filepath.Join(t.TempDir(), "code.go")
	if err := os.WriteFile(path, []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tools.NoteUnknown(path)
	return func() error {
		_, err := tools.PreviewMutation(tools.EditFileName,
			[]byte(`{"path":"`+path+`","old_text":"beta","new_text":"gamma"}`))
		return err
	}
}

// searchablePicker opens the saved-chat picker over two conversations whose
// names give nothing away — the shape a session nobody named has, where the
// name is the moment it began — and whose bodies do.
func searchablePicker(t *testing.T) Model {
	t.Helper()
	db := rewindTestDB(t)
	for name, said := range map[string]string{
		"2026-09-04 09:15": "why does the retry flake",
		"2026-09-04 11:02": "how do I bump the version",
	} {
		if err := db.SaveChat(name, []provider.Message{
			{Role: provider.RoleUser, Content: said},
			{Role: provider.RoleAssistant, Content: "one moment"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.SetChatTitle("2026-09-04 11:02", "Release chore"); err != nil {
		t.Fatal(err)
	}
	return sendText(t, readyModel(t).WithDB(db), "/chats")
}

func TestChatPick_FindsAChatByAWordSaidInIt(t *testing.T) {
	m := searchablePicker(t)
	if len(m.picker.Options) != 2 {
		t.Fatalf("both chats should be listed, got %v", m.picker.Options)
	}

	m = press(t, m, "/")
	m = runes(t, m, "retry")

	if len(m.picker.Options) != 1 {
		t.Fatalf("one conversation says \"retry\", the card is showing %v", m.picker.Options)
	}
	if m.picker.Options[0].Label != "2026-09-04 09:15" {
		t.Fatalf("the wrong conversation matched: %+v", m.picker.Options[0])
	}
	// A row no name matched says what did, since the card has nothing in the
	// label to bold.
	if m.picker.Options[0].Meta != bodyMatchPhrase {
		t.Fatalf("the row should say why it is here, got %q", m.picker.Options[0].Meta)
	}
	// And the choice still reaches the apply as the chat it names: the row
	// the card answers with is one of the matches, and the apply was written
	// against the list the picker opened over.
	if idx := m.pickerIndex[0]; m.chats.entries[idx].Name != "2026-09-04 09:15" {
		t.Fatalf("the filtered row should map back to its chat, got %q", m.chats.entries[idx].Name)
	}
	if m = press(t, m, "enter"); m.sessionName != "2026-09-04 09:15" {
		t.Fatalf("taking the row should open that conversation, the session is %q", m.sessionName)
	}
}

func TestChatPick_FindsAChatByItsTitle(t *testing.T) {
	m := press(t, searchablePicker(t), "/")
	m = runes(t, m, "chore")

	if len(m.picker.Options) != 1 || m.picker.Options[0].Label != "2026-09-04 11:02" {
		t.Fatalf("the title should have found the chat, got %v", m.picker.Options)
	}
}

func TestChatPick_KeepsTheNameFilterWhenItMatches(t *testing.T) {
	m := press(t, searchablePicker(t), "/")
	m = runes(t, m, "09:15")

	if len(m.picker.Options) != 1 || m.picker.Options[0].Label != "2026-09-04 09:15" {
		t.Fatalf("a name should still filter by name, got %v", m.picker.Options)
	}
	if m.picker.Options[0].Meta != "" {
		t.Fatalf("a row the name matched needs no explanation, got %q", m.picker.Options[0].Meta)
	}
}
