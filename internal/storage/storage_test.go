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
	if _, err := db.RecordRequest(RequestRecord{
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

func TestRecordExitCode(t *testing.T) {
	db := openTestDB(t)

	dur := 1 * time.Second
	id, err := db.RecordRequest(RequestRecord{
		Provider: "openai",
		Model:    "gpt-4o",
		Prompt:   "list files",
		Command:  "ls -la",
		Action:   "run",
		Duration: &dur,
		Success:  true,
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}

	if err := db.RecordExitCode(id, 0); err != nil {
		t.Fatalf("record exit code: %v", err)
	}

	var exitCode *int
	db.sql.QueryRow(`SELECT exit_code FROM requests WHERE id = ?`, id).Scan(&exitCode)
	if exitCode == nil || *exitCode != 0 {
		t.Fatalf("expected exit_code 0, got %v", exitCode)
	}

	id2, _ := db.RecordRequest(RequestRecord{
		Provider: "openai",
		Model:    "gpt-4o",
		Prompt:   "bad command",
		Command:  "false",
		Action:   "run",
		Duration: &dur,
		Success:  true,
	})
	if err := db.RecordExitCode(id2, 1); err != nil {
		t.Fatalf("record exit code: %v", err)
	}

	var exitCode2 *int
	db.sql.QueryRow(`SELECT exit_code FROM requests WHERE id = ?`, id2).Scan(&exitCode2)
	if exitCode2 == nil || *exitCode2 != 1 {
		t.Fatalf("expected exit_code 1, got %v", exitCode2)
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
		if _, err := db.RecordRequest(r); err != nil {
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

func TestListHistory(t *testing.T) {
	db := openTestDB(t)

	dur := 1 * time.Second
	for _, r := range []RequestRecord{
		{Provider: "openai", Model: "gpt-4o", Prompt: "list files", Command: "ls -la", Action: "run", Duration: &dur, Success: true},
		{Provider: "openai", Model: "gpt-4o", Prompt: "disk usage", Command: "du -sh *", Action: "copy", Duration: &dur, Success: true},
		{Provider: "gemini", Model: "flash", Prompt: "find port", Command: "lsof -i :8080", Action: "run", Duration: &dur, Success: true},
	} {
		if _, err := db.RecordRequest(r); err != nil {
			t.Fatalf("record: %v", err)
		}
	}

	entries, err := db.ListHistory(HistoryFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3, got %d", len(entries))
	}

	filtered, err := db.ListHistory(HistoryFilter{Search: "port", Limit: 10})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(filtered) != 1 {
		t.Fatalf("expected 1 search result, got %d", len(filtered))
	}
	if filtered[0].Command != "lsof -i :8080" {
		t.Fatalf("unexpected command: %q", filtered[0].Command)
	}
}

func TestSaveAndListSnippets(t *testing.T) {
	db := openTestDB(t)

	if err := db.SaveSnippet("deploy", "kubectl apply -f deploy.yaml"); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := db.SaveSnippet("logs", "kubectl logs -f app"); err != nil {
		t.Fatalf("save: %v", err)
	}

	snippets, err := db.ListSnippets()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(snippets) != 2 {
		t.Fatalf("expected 2 snippets, got %d", len(snippets))
	}
}

func TestGetSnippet(t *testing.T) {
	db := openTestDB(t)

	if err := db.SaveSnippet("test", "echo hello"); err != nil {
		t.Fatalf("save: %v", err)
	}

	s, err := db.GetSnippet("test")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if s.Command != "echo hello" {
		t.Fatalf("expected 'echo hello', got %q", s.Command)
	}
}

func TestGetSnippet_NotFound(t *testing.T) {
	db := openTestDB(t)

	_, err := db.GetSnippet("nope")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestDeleteSnippet(t *testing.T) {
	db := openTestDB(t)

	if err := db.SaveSnippet("rm-me", "echo bye"); err != nil {
		t.Fatalf("save: %v", err)
	}

	if err := db.DeleteSnippet("rm-me"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	_, err := db.GetSnippet("rm-me")
	if err == nil {
		t.Fatal("expected error after delete")
	}
}

func TestSaveSnippet_Overwrite(t *testing.T) {
	db := openTestDB(t)

	if err := db.SaveSnippet("up", "v1"); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := db.SaveSnippet("up", "v2"); err != nil {
		t.Fatalf("overwrite: %v", err)
	}

	s, err := db.GetSnippet("up")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if s.Command != "v2" {
		t.Fatalf("expected 'v2', got %q", s.Command)
	}
}

func TestRating_Flow(t *testing.T) {
	db := openTestDB(t)

	id1, err := db.RecordRequest(RequestRecord{Provider: "openai", Model: "gpt-4o", Prompt: "list files", Command: "ls", Action: "run"})
	if err != nil {
		t.Fatal(err)
	}
	id2, err := db.RecordRequest(RequestRecord{Provider: "openai", Model: "gpt-4o", Prompt: "copy stuff", Command: "cp a b", Action: "copy"})
	if err != nil {
		t.Fatal(err)
	}
	// Cancelled request should never show up for rating.
	if _, err := db.RecordRequest(RequestRecord{Provider: "openai", Model: "gpt-4o", Prompt: "nope", Command: "rm -rf /", Action: "cancel"}); err != nil {
		t.Fatal(err)
	}

	unrated, err := db.ListUnrated(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(unrated) != 2 {
		t.Fatalf("expected 2 unrated, got %d", len(unrated))
	}
	if unrated[0].ID != id2 {
		t.Errorf("expected newest first, got id %d", unrated[0].ID)
	}

	if err := db.RateRequest(id1, true); err != nil {
		t.Fatal(err)
	}
	if err := db.RateRequest(id2, false); err != nil {
		t.Fatal(err)
	}

	unrated, err = db.ListUnrated(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(unrated) != 0 {
		t.Fatalf("expected 0 unrated after rating, got %d", len(unrated))
	}

	summary, err := db.MetricsSummary()
	if err != nil {
		t.Fatal(err)
	}
	if len(summary) != 1 {
		t.Fatalf("expected 1 metrics row, got %d", len(summary))
	}
	m := summary[0]
	if m.RatedCount != 2 {
		t.Errorf("expected 2 rated, got %d", m.RatedCount)
	}
	if m.RatingRate == nil || *m.RatingRate != 0.5 {
		t.Errorf("expected 50%% rating rate, got %v", m.RatingRate)
	}
}

func TestSaveChatBranch_LinksParent(t *testing.T) {
	db := openTestDB(t)

	root := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "one"},
	}
	tail := append(append([]provider.Message{}, root...),
		provider.Message{Role: provider.RoleUser, Content: "two"},
		provider.Message{Role: provider.RoleAssistant, Content: "reply"},
	)

	if err := db.SaveChat("main", root); err != nil {
		t.Fatalf("save root: %v", err)
	}
	if err := db.SaveChatBranch("main", "main@turn2", tail); err != nil {
		t.Fatalf("save branch: %v", err)
	}

	loaded, err := db.LoadChat("main@turn2")
	if err != nil {
		t.Fatalf("load branch: %v", err)
	}
	if len(loaded) != 4 {
		t.Fatalf("expected 4 branch messages, got %d", len(loaded))
	}

	branches, err := db.ListChatBranches("main")
	if err != nil {
		t.Fatalf("list branches: %v", err)
	}
	if len(branches) != 2 {
		t.Fatalf("expected 2 family members, got %d", len(branches))
	}
	if branches[0].Name != "main" || branches[0].Parent != "" {
		t.Fatalf("expected root first with no parent, got %+v", branches[0])
	}
	if branches[1].Name != "main@turn2" || branches[1].Parent != "main" {
		t.Fatalf("expected branch linked to main, got %+v", branches[1])
	}
	if branches[1].Turns != 2 {
		t.Fatalf("expected 2 user turns on the branch, got %d", branches[1].Turns)
	}
}

func TestSaveChatBranch_CreatesMissingParent(t *testing.T) {
	db := openTestDB(t)

	tail := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "hello"},
	}
	if err := db.SaveChatBranch("never-saved", "never-saved@turn1", tail); err != nil {
		t.Fatalf("save branch: %v", err)
	}

	branches, err := db.ListChatBranches("never-saved@turn1")
	if err != nil {
		t.Fatalf("list branches: %v", err)
	}
	if len(branches) != 2 {
		t.Fatalf("expected parent + branch, got %d", len(branches))
	}
	if branches[0].Name != "never-saved" {
		t.Fatalf("expected empty parent session created, got %+v", branches[0])
	}
}

func TestListChatBranches_FromBranchFindsWholeFamily(t *testing.T) {
	db := openTestDB(t)

	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "hi"},
	}
	if err := db.SaveChat("root", msgs); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveChatBranch("root", "root@a", msgs); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveChatBranch("root@a", "root@a@b", msgs); err != nil {
		t.Fatal(err)
	}
	// An unrelated session must not appear in the family.
	if err := db.SaveChat("other", msgs); err != nil {
		t.Fatal(err)
	}

	branches, err := db.ListChatBranches("root@a@b")
	if err != nil {
		t.Fatal(err)
	}
	if len(branches) != 3 {
		t.Fatalf("expected 3 family members from a leaf, got %d: %+v", len(branches), branches)
	}
	for _, b := range branches {
		if b.Name == "other" {
			t.Fatal("unrelated session leaked into the family")
		}
	}
}

func TestListChatBranches_UnknownName(t *testing.T) {
	db := openTestDB(t)

	branches, err := db.ListChatBranches("nope")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(branches) != 0 {
		t.Fatalf("expected empty family, got %d", len(branches))
	}
}

func TestSaveChat_PreservesParentOnOverwrite(t *testing.T) {
	db := openTestDB(t)

	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "hi"},
	}
	if err := db.SaveChat("root", msgs); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveChatBranch("root", "root@a", msgs); err != nil {
		t.Fatal(err)
	}
	// Re-saving the branch (e.g. before a /branches switch) must keep the link.
	if err := db.SaveChat("root@a", append(msgs, provider.Message{Role: provider.RoleUser, Content: "more"})); err != nil {
		t.Fatal(err)
	}

	branches, err := db.ListChatBranches("root@a")
	if err != nil {
		t.Fatal(err)
	}
	if len(branches) != 2 || branches[1].Parent != "root" {
		t.Fatalf("parent link lost on overwrite: %+v", branches)
	}
}
