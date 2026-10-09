package chat

import (
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/storage"
)

// A session that compacted keeps the turns it folded: the reopen hands the
// model the compacted list and draws the folded turns, with the pictures they
// carried, as the live session drew them.
func TestReopen_ACompactedSessionKeepsItsFoldedTurns(t *testing.T) {
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	pic := trayPNG(t, "before.png")
	pic.Handle = "Image#1"
	a := agent.New([]provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "this one", Attachments: []provider.Attachment{pic}},
		{Role: provider.RoleAssistant, Content: "Looked at it."},
		{Role: provider.RoleUser, Content: "and now this"},
		{Role: provider.RoleAssistant, Content: "Done."},
	}, nil)
	a.Compact("they looked at a picture", a.Messages()[3:])
	compacted := len(a.Messages())

	if err := db.SaveChat("tray", a.Messages()); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveChatFolded("tray", a.Folded()); err != nil {
		t.Fatal(err)
	}
	loaded, err := db.LoadChat("tray")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != compacted {
		t.Fatalf("the model is handed %d messages, not the compacted list's %d", len(loaded), compacted)
	}
	m := New(loaded[:1], multiTokenStream("ok"), Wiring{DB: db}).WithResumedMessages("tray", loaded)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = updated.(Model)

	for _, msg := range m.agent.Messages() {
		if msg.Content == "this one" || msg.Content == "Looked at it." {
			t.Fatalf("a folded turn is back in the model's list: %q", msg.Content)
		}
	}
	if got := m.agent.Folded(); len(got) != 2 {
		t.Fatalf("the reopened session carries %d folded messages, want 2", len(got))
	}
	idx := trayAt(t, m, "Image#1")
	if !m.transcript[idx].outOfWindow {
		t.Fatal("the folded turn's picture row is not marked out of the window")
	}
	opened, _ := m.openTrayPicture(idx)
	if card := opened.(Model); card.state != statePreview || card.preview == nil || card.preview.Image == nil {
		t.Fatalf("the folded picture did not open: state %d", card.state)
	}
}
