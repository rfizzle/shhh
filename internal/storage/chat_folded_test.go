package storage

import (
	"path/filepath"
	"testing"

	"github.com/rfizzle/llm"
	"github.com/rfizzle/llm/attachment"
)

func TestChatFolded_StaysBesideAConversationThatIsRewritten(t *testing.T) {
	db, err := OpenPath(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	pic := attachment.Attachment{Kind: attachment.Image, Name: "a.png", MediaType: "image/png", Data: []byte{1, 2, 3}}
	folded := []llm.Message{
		{Role: llm.RoleUser, Content: "first", Attachments: []attachment.Attachment{pic}},
		{Role: llm.RoleAssistant, Content: "answer"},
	}
	compacted := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "summary", Machine: true},
	}
	if err := db.SaveChat("s", compacted); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveChatFolded("s", folded); err != nil {
		t.Fatal(err)
	}
	// A save that rewrites the conversation whole leaves the record alone.
	rewritten := append(append([]llm.Message(nil), compacted...), llm.Message{Role: llm.RoleUser, Content: "next"})
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

func TestSave_ATypedNameReplacesTheFoldedTurns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	first, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.SaveChat("slot", []llm.Message{{Role: llm.RoleUser, Content: "old summary"}}); err != nil {
		t.Fatal(err)
	}
	if err := first.SaveChatFolded("slot", []llm.Message{{Role: llm.RoleUser, Content: "old fold"}}); err != nil {
		t.Fatal(err)
	}
	first.Close()

	second, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { second.Close() })
	if err := second.SaveChat("slot", []llm.Message{{Role: llm.RoleUser, Content: "new"}}); err != nil {
		t.Fatal(err)
	}
	if back, _ := second.LoadChatFolded("slot"); len(back) != 0 {
		t.Fatalf("a typed-name save left %d stale folded turns", len(back))
	}
}

func TestSaveChatBranch_CarriesTheParentsFoldedTurns(t *testing.T) {
	db, err := OpenPath(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	msgs := []llm.Message{{Role: llm.RoleUser, Content: "summary", Machine: true}}
	if err := db.SaveChat("p", msgs); err != nil {
		t.Fatal(err)
	}
	folded := []llm.Message{{Role: llm.RoleUser, Content: "one"}, {Role: llm.RoleAssistant, Content: "two"}}
	if err := db.SaveChatFolded("p", folded); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveChatBranch("p", "p-b", msgs); err != nil {
		t.Fatal(err)
	}
	back, err := db.LoadChatFolded("p-b")
	if err != nil {
		t.Fatal(err)
	}
	if len(back) != 2 || back[0].Content != "one" || back[1].Content != "two" {
		t.Fatalf("branch folded turns = %+v", back)
	}
	if parent, _ := db.LoadChatFolded("p"); len(parent) != 2 {
		t.Fatalf("the parent lost its folded turns: %+v", parent)
	}
}

func TestStoredChatSeq_IgnoresFoldedRows(t *testing.T) {
	db, err := OpenPath(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.SaveChat("s", nil); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveChatFolded("s", []llm.Message{{Role: llm.RoleUser, Content: "f"}}); err != nil {
		t.Fatal(err)
	}
	tx, err := db.sql.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var id int64
	if err := tx.QueryRow(`SELECT id FROM chat_sessions WHERE name = 's'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if got, err := storedChatSeq(tx, id); err != nil || got != -1 {
		t.Fatalf("storedChatSeq = %d, %v; want -1", got, err)
	}
}
