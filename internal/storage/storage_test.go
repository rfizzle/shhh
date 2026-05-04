package storage

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

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

func TestRecordRequest(t *testing.T) {
	db := openTestDB(t)

	ttft := 150 * time.Millisecond
	dur := 2 * time.Second
	if err := db.RecordRequest(RequestRecord{
		Provider: "openai",
		Model:    "gpt-4o",
		Prompt:   "list files",
		Command:  "ls -la",
		Action:   "run",
		TTFT:     &ttft,
		Duration: &dur,
		Success:  true,
	}); err != nil {
		t.Fatalf("record: %v", err)
	}

	var count int
	db.sql.QueryRow(`SELECT COUNT(*) FROM requests`).Scan(&count)
	if count != 1 {
		t.Fatalf("expected 1 request, got %d", count)
	}
}

func TestMetricsSummary(t *testing.T) {
	db := openTestDB(t)

	ttft1 := 100 * time.Millisecond
	dur1 := 1 * time.Second
	ttft2 := 200 * time.Millisecond
	dur2 := 3 * time.Second

	for _, r := range []RequestRecord{
		{Provider: "openai", Model: "gpt-4o", Prompt: "p1", Command: "c1", Action: "run", TTFT: &ttft1, Duration: &dur1, Success: true},
		{Provider: "openai", Model: "gpt-4o", Prompt: "p2", Command: "c2", Action: "copy", TTFT: &ttft2, Duration: &dur2, Success: true},
		{Provider: "gemini", Model: "gemini-2.5-flash", Prompt: "p3", Command: "c3", Action: "run", TTFT: &ttft1, Duration: &dur1, Success: false},
	} {
		if err := db.RecordRequest(r); err != nil {
			t.Fatalf("record: %v", err)
		}
	}

	summary, err := db.MetricsSummary()
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if len(summary) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(summary))
	}

	for _, m := range summary {
		if m.Provider == "openai" {
			if m.Count != 2 {
				t.Fatalf("openai count: want 2, got %d", m.Count)
			}
			if m.SuccessRate != 1.0 {
				t.Fatalf("openai success rate: want 1.0, got %f", m.SuccessRate)
			}
		}
		if m.Provider == "gemini" {
			if m.Count != 1 {
				t.Fatalf("gemini count: want 1, got %d", m.Count)
			}
			if m.SuccessRate != 0.0 {
				t.Fatalf("gemini success rate: want 0.0, got %f", m.SuccessRate)
			}
		}
	}
}

func TestInstrumentStream(t *testing.T) {
	events := make(chan provider.StreamEvent, 3)
	events <- provider.StreamEvent{Token: "hello"}
	events <- provider.StreamEvent{Token: " world"}
	events <- provider.StreamEvent{Done: true}
	close(events)

	out, metrics := InstrumentStream(events)

	var tokens []string
	for ev := range out {
		if ev.Token != "" {
			tokens = append(tokens, ev.Token)
		}
	}

	if len(tokens) != 2 {
		t.Fatalf("expected 2 tokens, got %d", len(tokens))
	}
	if metrics.TTFT == nil {
		t.Fatal("expected TTFT to be set")
	}
	if metrics.Duration == nil {
		t.Fatal("expected Duration to be set")
	}
	if !metrics.Success {
		t.Fatal("expected success=true")
	}
}

func TestInstrumentStream_Error(t *testing.T) {
	events := make(chan provider.StreamEvent, 1)
	events <- provider.StreamEvent{Err: fmt.Errorf("api error"), Done: true}
	close(events)

	out, metrics := InstrumentStream(events)

	for range out {
	}

	if metrics.Success {
		t.Fatal("expected success=false on error")
	}
}
