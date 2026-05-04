package storage

import (
	"path/filepath"
	"testing"

	"github.com/rfizzle/shhh/internal/provider"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := OpenPath(path)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestMigrate_Idempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	db1, err := OpenPath(path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	db1.Close()

	db2, err := OpenPath(path)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	db2.Close()
}

func TestSaveAndLoadChat(t *testing.T) {
	db := openTestDB(t)

	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "hello"},
		{Role: provider.RoleAssistant, Content: "hi there"},
	}

	if err := db.SaveChat("test-chat", msgs); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := db.LoadChat("test-chat")
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if len(loaded) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(loaded))
	}
	if loaded[1].Content != "hello" {
		t.Fatalf("expected 'hello', got %q", loaded[1].Content)
	}
	if loaded[2].Content != "hi there" {
		t.Fatalf("expected 'hi there', got %q", loaded[2].Content)
	}
}

func TestSaveChat_WithToolCalls(t *testing.T) {
	db := openTestDB(t)

	msgs := []provider.Message{
		{Role: provider.RoleUser, Content: "list files"},
		{
			Role:    provider.RoleAssistant,
			Content: "",
			ToolCalls: []provider.ToolCall{
				{ID: "tc1", Name: "list_dir", Arguments: `{"path":"."}`},
			},
		},
		{Role: provider.RoleTool, Content: "file1.go\nfile2.go", ToolCallID: "tc1"},
		{Role: provider.RoleAssistant, Content: "Here are the files."},
	}

	if err := db.SaveChat("tools-chat", msgs); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := db.LoadChat("tools-chat")
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if len(loaded) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(loaded))
	}
	if len(loaded[1].ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(loaded[1].ToolCalls))
	}
	if loaded[1].ToolCalls[0].Name != "list_dir" {
		t.Fatalf("expected tool call 'list_dir', got %q", loaded[1].ToolCalls[0].Name)
	}
	if loaded[2].ToolCallID != "tc1" {
		t.Fatalf("expected tool_call_id 'tc1', got %q", loaded[2].ToolCallID)
	}
}

func TestLoadChat_NotFound(t *testing.T) {
	db := openTestDB(t)

	_, err := db.LoadChat("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent chat")
	}
}

func TestListChats_Empty(t *testing.T) {
	db := openTestDB(t)

	entries, err := db.ListChats()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(entries))
	}
}

func TestListChats_WithSessions(t *testing.T) {
	db := openTestDB(t)

	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "q1"},
		{Role: provider.RoleAssistant, Content: "a1"},
		{Role: provider.RoleUser, Content: "q2"},
		{Role: provider.RoleAssistant, Content: "a2"},
	}

	if err := db.SaveChat("chat-one", msgs); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := db.SaveChat("chat-two", msgs[:3]); err != nil {
		t.Fatalf("save: %v", err)
	}

	entries, err := db.ListChats()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	found := map[string]int{}
	for _, e := range entries {
		found[e.Name] = e.Turns
	}
	if found["chat-one"] != 2 {
		t.Fatalf("chat-one should have 2 turns, got %d", found["chat-one"])
	}
	if found["chat-two"] != 1 {
		t.Fatalf("chat-two should have 1 turn, got %d", found["chat-two"])
	}
}

func TestSaveChat_OverwritePreservesCreatedAt(t *testing.T) {
	db := openTestDB(t)

	msgs1 := []provider.Message{{Role: provider.RoleUser, Content: "first"}}
	if err := db.SaveChat("update-test", msgs1); err != nil {
		t.Fatalf("save: %v", err)
	}

	var createdAt string
	db.sql.QueryRow(`SELECT created_at FROM chat_sessions WHERE name = 'update-test'`).Scan(&createdAt)

	msgs2 := []provider.Message{{Role: provider.RoleUser, Content: "second"}}
	if err := db.SaveChat("update-test", msgs2); err != nil {
		t.Fatalf("save: %v", err)
	}

	var createdAt2 string
	db.sql.QueryRow(`SELECT created_at FROM chat_sessions WHERE name = 'update-test'`).Scan(&createdAt2)

	if createdAt != createdAt2 {
		t.Fatalf("created_at changed on overwrite: %q -> %q", createdAt, createdAt2)
	}

	loaded, err := db.LoadChat("update-test")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded[0].Content != "second" {
		t.Fatalf("expected 'second', got %q", loaded[0].Content)
	}
}

func TestDeleteChat(t *testing.T) {
	db := openTestDB(t)

	msgs := []provider.Message{{Role: provider.RoleUser, Content: "hi"}}
	if err := db.SaveChat("del-test", msgs); err != nil {
		t.Fatalf("save: %v", err)
	}

	if err := db.DeleteChat("del-test"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	_, err := db.LoadChat("del-test")
	if err == nil {
		t.Fatal("expected error after delete")
	}
}

func TestDeleteChat_NotFound(t *testing.T) {
	db := openTestDB(t)

	err := db.DeleteChat("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent chat")
	}
}
