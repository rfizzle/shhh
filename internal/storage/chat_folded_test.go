package storage

import (
	"path/filepath"
	"testing"

	"github.com/rfizzle/shhh/internal/provider"
)

func TestChatFolded_StaysBesideAConversationThatIsRewritten(t *testing.T) {
	db, err := OpenPath(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	pic := provider.Attachment{Kind: provider.AttachmentImage, Name: "a.png", MediaType: "image/png", Data: []byte{1, 2, 3}}
	folded := []provider.Message{
		{Role: provider.RoleUser, Content: "first", Attachments: []provider.Attachment{pic}},
		{Role: provider.RoleAssistant, Content: "answer"},
	}
	compacted := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "summary", Machine: true},
	}
	if err := db.SaveChat("s", compacted); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveChatFolded("s", folded); err != nil {
		t.Fatal(err)
	}
	// A save that rewrites the conversation whole leaves the record alone.
	rewritten := append(append([]provider.Message(nil), compacted...), provider.Message{Role: provider.RoleUser, Content: "next"})
	rewritten[1].Content = "another summary"
	if err := db.SaveChat("s", rewritten); err != nil {
		t.Fatal(err)
	}

	got, err := db.LoadChat("s")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("the model's list holds %d messages, want 3", len(got))
	}
	back, err := db.LoadChatFolded("s")
	if err != nil {
		t.Fatal(err)
	}
	if len(back) != 2 || back[0].Content != "first" || back[1].Content != "answer" {
		t.Fatalf("folded turns came back as %+v", back)
	}
	if len(back[0].Attachments) != 1 || len(back[0].Attachments[0].Data) != 3 {
		t.Fatalf("the folded turn lost its attachment: %+v", back[0].Attachments)
	}

	if err := db.SaveChatFolded("s", nil); err != nil {
		t.Fatal(err)
	}
	if back, _ = db.LoadChatFolded("s"); len(back) != 0 {
		t.Fatalf("an empty save left %d folded turns", len(back))
	}
}
