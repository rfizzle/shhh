package storage

import (
	"database/sql"
	"errors"
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
	defer db1.Close()

	// Verify busy_timeout is set
	var busyTimeout int
	if err := db1.sql.QueryRow("PRAGMA busy_timeout;").Scan(&busyTimeout); err != nil {
		t.Fatalf("read busy_timeout: %v", err)
	}
	if busyTimeout != 5000 {
		t.Fatalf("busy_timeout: want 5000, got %d", busyTimeout)
	}

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

// A resumed session has to keep the screenshot the question was about, not
// just the sentence pointing at nothing.
func TestSaveChat_RoundTripsAttachments(t *testing.T) {
	db := openTestDB(t)

	msgs := []provider.Message{
		{
			Role:    provider.RoleUser,
			Content: "what is wrong here?",
			Attachments: []provider.Attachment{{
				Kind:      provider.AttachmentImage,
				Name:      "shot.png",
				MediaType: "image/png",
				Data:      []byte{0x89, 'P', 'N', 'G', 0x00, 0xff},
			}},
		},
		{Role: provider.RoleAssistant, Content: "the margin is off"},
	}

	if err := db.SaveChat("shots", msgs); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := db.LoadChat("shots")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(loaded[0].Attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(loaded[0].Attachments))
	}
	got := loaded[0].Attachments[0]
	if got.Name != "shot.png" || got.MediaType != "image/png" || got.Kind != provider.AttachmentImage {
		t.Fatalf("attachment metadata lost: %#v", got)
	}
	if string(got.Data) != string(msgs[0].Attachments[0].Data) {
		t.Fatalf("attachment bytes lost: %v", got.Data)
	}
	if loaded[1].Attachments != nil {
		t.Fatal("a message with no attachments should load with none")
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
	if err := db.sql.QueryRow(`SELECT created_at FROM chat_sessions WHERE name = 'update-test'`).Scan(&createdAt); err != nil {
		t.Fatal(err)
	}

	msgs2 := []provider.Message{{Role: provider.RoleUser, Content: "second"}}
	if err := db.SaveChat("update-test", msgs2); err != nil {
		t.Fatalf("save: %v", err)
	}

	var createdAt2 string
	if err := db.sql.QueryRow(`SELECT created_at FROM chat_sessions WHERE name = 'update-test'`).Scan(&createdAt2); err != nil {
		t.Fatal(err)
	}

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
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM requests`).Scan(&count); err != nil {
		t.Fatal(err)
	}
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
	must(t, db.sql.QueryRow(`SELECT exit_code FROM requests WHERE id = ?`, id).Scan(&exitCode))
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
	must(t, db.sql.QueryRow(`SELECT exit_code FROM requests WHERE id = ?`, id2).Scan(&exitCode2))
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

	summary, err := db.MetricsSummary(time.Time{})
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

	// A half-typed word still finds the row, which is what a person typing
	// into a search box has.
	if prefix, _ := db.ListHistory(HistoryFilter{Search: "por", Limit: 10}); len(prefix) != 1 {
		t.Fatalf("a prefix should find the entry, got %d", len(prefix))
	}
	// Each further word narrows: both of these are in the one entry.
	if both, _ := db.ListHistory(HistoryFilter{Search: "find port", Limit: 10}); len(both) != 1 {
		t.Fatalf("two words should still find the one entry, got %d", len(both))
	}
	// And a query with no word in it finds nothing rather than everything: an
	// answer holding the whole table would read as a search that worked.
	if none, err := db.ListHistory(HistoryFilter{Search: "  -  ", Limit: 10}); err != nil || len(none) != 0 {
		t.Fatalf("a query with no word should find nothing, got %d, %v", len(none), err)
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

	summary, err := db.MetricsSummary(time.Time{})
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

// The start screen's resume offer: the newest saved session, its turn
// count, and the price only when a record actually covers it.
func TestMostRecentChat_NewestSessionWithItsTurnCount(t *testing.T) {
	db := openTestDB(t)

	if _, ok, err := db.MostRecentChat(); err != nil || ok {
		t.Fatalf("empty database: ok = %v, err = %v; want false, nil", ok, err)
	}

	older := []provider.Message{
		{Role: provider.RoleUser, Content: "one"},
		{Role: provider.RoleAssistant, Content: "1"},
	}
	newer := []provider.Message{
		{Role: provider.RoleUser, Content: "one"},
		{Role: provider.RoleAssistant, Content: "1"},
		{Role: provider.RoleUser, Content: "two"},
		{Role: provider.RoleAssistant, Content: "2"},
	}
	if err := db.SaveChat("older", older); err != nil {
		t.Fatalf("save older: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	if err := db.SaveChat("newer", newer); err != nil {
		t.Fatalf("save newer: %v", err)
	}

	recent, ok, err := db.MostRecentChat()
	if err != nil || !ok {
		t.Fatalf("ok = %v, err = %v", ok, err)
	}
	if recent.Name != "newer" {
		t.Fatalf("name = %q, want newer", recent.Name)
	}
	if recent.Turns != 2 {
		t.Fatalf("turns = %d, want 2", recent.Turns)
	}
	// Nothing recorded the session, so no price is claimed for it.
	if recent.Priced {
		t.Fatalf("priced = true with no observability record (cost %v)", recent.Cost)
	}
}

func TestMostRecentChat_PricedFromTheSessionThatWroteIt(t *testing.T) {
	db := openTestDB(t)

	id, err := db.StartAgentSession("chat", "openai", "gpt-test")
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	if err := db.UpdateAgentSession(id, 2, 1000, 200, 0.42); err != nil {
		t.Fatalf("update session: %v", err)
	}
	// Saved while that session is still open, which is when a chat is
	// actually autosaved.
	if err := db.SaveChat("live", []provider.Message{{Role: provider.RoleUser, Content: "hi"}}); err != nil {
		t.Fatalf("save: %v", err)
	}

	recent, ok, err := db.MostRecentChat()
	if err != nil || !ok {
		t.Fatalf("ok = %v, err = %v", ok, err)
	}
	if !recent.Priced || recent.Cost != 0.42 {
		t.Fatalf("cost = %v priced = %v, want 0.42 true", recent.Cost, recent.Priced)
	}
}

func TestMostRecentChat_IgnoresASessionThatHadAlreadyEnded(t *testing.T) {
	db := openTestDB(t)

	id, err := db.StartAgentSession("chat", "openai", "gpt-test")
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	if err := db.UpdateAgentSession(id, 2, 1000, 200, 0.99); err != nil {
		t.Fatalf("update session: %v", err)
	}
	if err := db.EndAgentSession(id, ""); err != nil {
		t.Fatalf("end session: %v", err)
	}
	// A full second later, so the ended_at written to millisecond precision
	// is unambiguously before this save.
	time.Sleep(1100 * time.Millisecond)
	if err := db.SaveChat("after", []provider.Message{{Role: provider.RoleUser, Content: "hi"}}); err != nil {
		t.Fatalf("save: %v", err)
	}

	recent, _, err := db.MostRecentChat()
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if recent.Priced {
		t.Fatalf("priced from a session that had already ended (cost %v)", recent.Cost)
	}
}

// The columns the history browser reads: duration, exit code, token
// counts and success. They were in the table before the browser was, and a
// column that was never recorded comes back nil rather than as zero — the
// browser reads the difference as "not known", not as "exit 0".
func TestListHistory_CarriesTheRecordedColumns(t *testing.T) {
	db := openTestDB(t)

	dur := 1400 * time.Millisecond
	in, out := int64(412), int64(38)
	ran, err := db.RecordRequest(RequestRecord{
		Provider: "openai", Model: "gpt-5.2", Prompt: "delete old logs",
		Command: "find . -mtime +7 -delete", Action: "run",
		Duration: &dur, TokensIn: &in, TokensOut: &out, Success: true,
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := db.RecordExitCode(ran, 3); err != nil {
		t.Fatalf("exit code: %v", err)
	}
	if _, err := db.RecordRequest(RequestRecord{
		Provider: "openai", Model: "gpt-5.2", Prompt: "biggest files",
		Command: "du -ah .", Action: "copy", Success: false,
	}); err != nil {
		t.Fatalf("record: %v", err)
	}

	entries, err := db.ListHistory(HistoryFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	var recorded, bare HistoryEntry
	for _, e := range entries {
		if e.ID == ran {
			recorded = e
		} else {
			bare = e
		}
	}
	if recorded.Duration == nil || *recorded.Duration != dur {
		t.Fatalf("duration came back %v", recorded.Duration)
	}
	if recorded.ExitCode == nil || *recorded.ExitCode != 3 {
		t.Fatalf("exit code came back %v", recorded.ExitCode)
	}
	if recorded.TokensIn == nil || *recorded.TokensIn != in ||
		recorded.TokensOut == nil || *recorded.TokensOut != out {
		t.Fatalf("token counts came back %v / %v", recorded.TokensIn, recorded.TokensOut)
	}
	if !recorded.Success {
		t.Fatal("a request that completed came back as a failure")
	}

	if bare.Duration != nil || bare.ExitCode != nil || bare.TokensIn != nil || bare.TokensOut != nil {
		t.Fatalf("an unrecorded column came back as a value, not nil: %+v", bare)
	}
	if bare.Success {
		t.Fatal("a request that did not complete came back as a success")
	}
}

// The metrics window is a cutoff over the same rows, so a cutoff in the
// future leaves nothing and the zero cutoff — what `shhh metrics` reads
// without a --window — leaves everything.
func TestMetricsSummary_WindowIsACutoff(t *testing.T) {
	db := openTestDB(t)
	for _, r := range []RequestRecord{
		{Provider: "openai", Model: "gpt-4o", Prompt: "p1", Command: "c1", Action: "run", Success: true},
		{Provider: "openai", Model: "gpt-4o", Prompt: "p2", Command: "c2", Action: "copy", Success: true},
	} {
		if _, err := db.RecordRequest(r); err != nil {
			t.Fatalf("record: %v", err)
		}
	}

	all, err := db.MetricsSummary(time.Time{})
	if err != nil {
		t.Fatalf("all time: %v", err)
	}
	if len(all) != 1 || all[0].Count != 2 {
		t.Fatalf("all time read %d groups", len(all))
	}
	ahead, err := db.MetricsSummary(time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("future cutoff: %v", err)
	}
	if len(ahead) != 0 {
		t.Fatalf("a cutoff in the future read %d groups", len(ahead))
	}
}

// The per-day token totals are what the sparkline is drawn from, grouped per
// model and per calendar day.
func TestMetricsTokensByDay(t *testing.T) {
	db := openTestDB(t)
	in, out := int64(400), int64(100)
	for range 3 {
		if _, err := db.RecordRequest(RequestRecord{
			Provider: "openai", Model: "gpt-4o", Prompt: "p", Command: "c", Action: "run",
			TokensIn: &in, TokensOut: &out, Success: true,
		}); err != nil {
			t.Fatalf("record: %v", err)
		}
	}

	rows, err := db.MetricsTokensByDay(time.Time{})
	if err != nil {
		t.Fatalf("tokens by day: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("three requests on one day read as %d days", len(rows))
	}
	if rows[0].TokensIn != 1200 || rows[0].TokensOut != 300 {
		t.Fatalf("the day totals read ↑%d ↓%d", rows[0].TokensIn, rows[0].TokensOut)
	}
	if rows[0].Model != "gpt-4o" || len(rows[0].Day) != len("2006-01-02") {
		t.Fatalf("the day row reads %+v", rows[0])
	}
}

// The action split carries success alongside the action, because a request
// that never answered is not a thing that was done with a command.
func TestMetricsByAction(t *testing.T) {
	db := openTestDB(t)
	in, out := int64(100), int64(10)
	for _, r := range []RequestRecord{
		{Provider: "openai", Model: "gpt-4o", Action: "run", Success: true, TokensIn: &in, TokensOut: &out},
		{Provider: "openai", Model: "gpt-4o", Action: "run", Success: true, TokensIn: &in, TokensOut: &out},
		{Provider: "openai", Model: "gpt-4o", Action: "run", Success: false, TokensIn: &in, TokensOut: &out},
		{Provider: "openai", Model: "gpt-4o", Action: "copy", Success: true, TokensIn: &in, TokensOut: &out},
	} {
		r.Prompt, r.Command = "p", "c"
		if _, err := db.RecordRequest(r); err != nil {
			t.Fatalf("record: %v", err)
		}
	}

	rows, err := db.MetricsByAction(time.Time{})
	if err != nil {
		t.Fatalf("by action: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected run/ok, run/failed and copy/ok, got %d groups: %+v", len(rows), rows)
	}
	for _, row := range rows {
		switch {
		case row.Action == "run" && row.Success:
			if row.Count != 2 || row.TokensIn != 200 {
				t.Fatalf("two clean runs read %+v", row)
			}
		case row.Action == "run" && !row.Success:
			if row.Count != 1 {
				t.Fatalf("the run that never answered read %+v", row)
			}
		case row.Action == "copy":
			if row.Count != 1 {
				t.Fatalf("the copy read %+v", row)
			}
		default:
			t.Fatalf("an unexpected group: %+v", row)
		}
	}
}

func TestRenameChat_KeepsMessagesAndBranches(t *testing.T) {
	db := openTestDB(t)

	root := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "one"},
	}
	if err := db.SaveChat("main", root); err != nil {
		t.Fatalf("save root: %v", err)
	}
	if err := db.SaveChatBranch("main", "main@turn2", append(root, provider.Message{Role: provider.RoleUser, Content: "two"})); err != nil {
		t.Fatalf("save branch: %v", err)
	}
	if err := db.SetChatTitle("main", "Renaming things"); err != nil {
		t.Fatalf("title: %v", err)
	}

	if err := db.RenameChat("main", "renamed"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if _, err := db.LoadChat("main"); err == nil {
		t.Fatal("the old name should be gone")
	}
	msgs, err := db.LoadChat("renamed")
	if err != nil {
		t.Fatalf("load renamed: %v", err)
	}
	if len(msgs) != 2 || msgs[1].Content != "one" {
		t.Fatalf("messages should survive the rename, got %+v", msgs)
	}
	branches, err := db.ListChatBranches("renamed")
	if err != nil {
		t.Fatalf("branches: %v", err)
	}
	if len(branches) != 2 || branches[1].Parent != "renamed" {
		t.Fatalf("the branch should still hang off the renamed root, got %+v", branches)
	}
	if title, _ := db.ChatTitle("renamed"); title != "Renaming things" {
		t.Fatalf("the title should travel with the row, got %q", title)
	}
}

func TestRenameChat_RefusesACollision(t *testing.T) {
	db := openTestDB(t)
	msgs := []provider.Message{{Role: provider.RoleUser, Content: "hi"}}
	for _, name := range []string{"a", "b"} {
		if err := db.SaveChat(name, msgs); err != nil {
			t.Fatalf("save %s: %v", name, err)
		}
	}
	err := db.RenameChat("a", "b")
	var exists ChatExistsError
	if !errors.As(err, &exists) || exists.Name != "b" {
		t.Fatalf("expected ChatExistsError for b, got %v", err)
	}
	if _, err := db.LoadChat("a"); err != nil {
		t.Fatalf("a refused rename must leave the source in place: %v", err)
	}
	var missing ChatNotFoundError
	if err := db.RenameChat("nope", "c"); !errors.As(err, &missing) {
		t.Fatalf("renaming a missing chat should say not found, got %v", err)
	}
}

func TestDeleteChat_RemovesBranches(t *testing.T) {
	db := openTestDB(t)
	root := []provider.Message{{Role: provider.RoleUser, Content: "one"}}
	if err := db.SaveChat("main", root); err != nil {
		t.Fatalf("save root: %v", err)
	}
	if err := db.SaveChatBranch("main", "main@turn2", root); err != nil {
		t.Fatalf("save branch: %v", err)
	}
	if err := db.SaveChatBranch("main@turn2", "main@turn2@turn3", root); err != nil {
		t.Fatalf("save grandchild: %v", err)
	}
	if err := db.SaveChat("other", root); err != nil {
		t.Fatalf("save other: %v", err)
	}

	if n, err := db.CountChatBranches("main"); err != nil || n != 2 {
		t.Fatalf("main should count 2 descendants, got %d %v", n, err)
	}
	if n, err := db.CountChatBranches("other"); err != nil || n != 0 {
		t.Fatalf("other should count 0, got %d %v", n, err)
	}

	if err := db.DeleteChat("main"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	entries, err := db.ListChats()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "other" {
		t.Fatalf("only the unrelated chat should remain, got %+v", entries)
	}
	var count int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM chat_messages`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("the family's messages should be gone, %d left (%v)", count, err)
	}
}

func TestChatTitle_ListedAndUnknown(t *testing.T) {
	db := openTestDB(t)
	msgs := []provider.Message{{Role: provider.RoleUser, Content: "hi"}}
	if err := db.SaveChat("t", msgs); err != nil {
		t.Fatalf("save: %v", err)
	}
	if title, err := db.ChatTitle("t"); err != nil || title != "" {
		t.Fatalf("a fresh chat has no title, got %q %v", title, err)
	}
	if err := db.SetChatTitle("t", "Greeting"); err != nil {
		t.Fatalf("set title: %v", err)
	}
	entries, _ := db.ListChats()
	if len(entries) != 1 || entries[0].Title != "Greeting" {
		t.Fatalf("listing should carry the title, got %+v", entries)
	}
	if recent, ok, _ := db.MostRecentChat(); !ok || recent.Title != "Greeting" {
		t.Fatalf("the resume suggestion should carry the title, got %+v", recent)
	}
	if err := db.SetChatTitle("missing", "x"); err == nil {
		t.Fatal("titling a missing chat should fail")
	}
	if title, err := db.ChatTitle("missing"); err != nil || title != "" {
		t.Fatalf("an unknown chat has no title and no error, got %q %v", title, err)
	}
}

// The mark a conversation saved mid-turn carries. It rides the autosave
// itself, so the slot and the conversation in it cannot come to disagree; a
// slot nothing ever held reads as unheld, which is what opens every
// conversation written before the column existed the way it always opened.
func TestChatHold_MarkedClearedAndAbsent(t *testing.T) {
	db := openTestDB(t)
	msgs := []provider.Message{{Role: provider.RoleUser, Content: "hi"}}
	if _, err := db.AutosaveChat("h", "fresh", msgs, nil); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, held, err := db.ChatHold("h"); err != nil || held {
		t.Fatalf("a fresh chat is not held, got %v %v", held, err)
	}

	msgs = append(msgs, provider.Message{Role: provider.RoleAssistant, Content: "ok"})
	if _, err := db.AutosaveChat("h", "fresh", msgs, &ChatHold{Rounds: 12, Granted: 100}); err != nil {
		t.Fatalf("save held: %v", err)
	}
	got, held, err := db.ChatHold("h")
	if err != nil || !held {
		t.Fatalf("the mark should be readable back, got %v %v", held, err)
	}
	if got.Rounds != 12 || got.Granted != 100 {
		t.Fatalf("the mark should carry both counts, got %+v", got)
	}

	// A named save is a copy of the conversation, and whether the live
	// session is holding a turn is not a fact about the copy.
	if err := db.SaveChat("h", msgs); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, held, _ := db.ChatHold("h"); !held {
		t.Fatal("a named save should leave the mark alone")
	}

	if _, err := db.AutosaveChat("h", "fresh", msgs, nil); err != nil {
		t.Fatalf("save unheld: %v", err)
	}
	if _, held, _ := db.ChatHold("h"); held {
		t.Fatal("a turn let go of leaves no mark")
	}

	if _, held, err := db.ChatHold("missing"); err != nil || held {
		t.Fatalf("an unknown chat is not held and is not an error, got %v %v", held, err)
	}
}

func TestChatResume_RoundTripsAndFollowsTheSlot(t *testing.T) {
	db := openTestDB(t)
	msgs := []provider.Message{{Role: provider.RoleUser, Content: "hi"}}
	if err := db.SaveChat("r", msgs); err != nil {
		t.Fatalf("save: %v", err)
	}

	// A slot nothing has written one to answers with both halves empty,
	// which is every slot written before the columns existed.
	if got, err := db.ChatResume("r"); err != nil || got != (ChatResume{}) {
		t.Fatalf("a fresh slot has nothing to resume on, got %+v %v", got, err)
	}

	want := ChatResume{Summary: "what was decided and what is open", Head: "0123456789abcdef0123456789abcdef01234567"}
	if err := db.SetChatResume("r", want); err != nil {
		t.Fatalf("set: %v", err)
	}
	if got, err := db.ChatResume("r"); err != nil || got != want {
		t.Fatalf("resume state = %+v %v, want %+v", got, err, want)
	}

	// A rename is the same row under another name, so what it is opened on
	// goes with it.
	if err := db.RenameChat("r", "renamed"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if got, err := db.ChatResume("renamed"); err != nil || got != want {
		t.Fatalf("a rename should carry the resume state, got %+v %v", got, err)
	}

	// And a branch is a tail of the conversation it forked from, so the
	// summary and the commit are its own past too.
	if err := db.SaveChatBranch("renamed", "renamed (branch 1)", msgs); err != nil {
		t.Fatalf("branch: %v", err)
	}
	if got, err := db.ChatResume("renamed (branch 1)"); err != nil || got != want {
		t.Fatalf("a branch should carry the resume state, got %+v %v", got, err)
	}

	if err := db.DeleteChat("renamed"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if got, err := db.ChatResume("renamed"); err != nil || got != (ChatResume{}) {
		t.Fatalf("a deleted chat has no resume state, got %+v %v", got, err)
	}
	if got, err := db.ChatResume("renamed (branch 1)"); err != nil || got != (ChatResume{}) {
		t.Fatalf("the branch goes with it, got %+v %v", got, err)
	}

	var missing ChatNotFoundError
	if err := db.SetChatResume("nobody", want); !errors.As(err, &missing) {
		t.Fatalf("writing to an unknown slot should say so, got %v", err)
	}
}

// Two openers on a store neither has seen — the root's background history
// purge beside the command's own open — must both come up: the loser waits
// on the lock and finds the step already recorded rather than applying it
// again.
func TestOpenPath_ConcurrentOpenersMigrateOnce(t *testing.T) {
	for round := 0; round < 20; round++ {
		path := filepath.Join(t.TempDir(), "test.db")
		errs := make(chan error, 2)
		for i := 0; i < 2; i++ {
			go func() {
				db, err := OpenPath(path)
				if err == nil {
					db.Close()
				}
				errs <- err
			}()
		}
		for i := 0; i < 2; i++ {
			if err := <-errs; err != nil {
				t.Fatalf("round %d: concurrent open failed: %v", round, err)
			}
		}
		check, err := OpenPath(path)
		if err != nil {
			t.Fatal(err)
		}
		var versions int
		if err := check.sql.QueryRow(`SELECT COUNT(*) FROM schema_version`).Scan(&versions); err != nil {
			t.Fatal(err)
		}
		check.Close()
		if versions != len(migrations) {
			t.Fatalf("round %d: every step recorded exactly once, got %d rows for %d steps", round, versions, len(migrations))
		}
	}
}

// TestRefusedLock_IsSQLiteBusyAndNothingElse pins the predicate the opener
// retries on to the code SQLite hands back when it will not wait for a lock:
// a connection with no busy timeout asking to write while another holds the
// store exclusively.
func TestRefusedLock_IsSQLiteBusyAndNothingElse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	holder, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	holder.SetMaxOpenConns(1)
	if _, err := holder.Exec(`BEGIN EXCLUSIVE`); err != nil {
		t.Fatal(err)
	}
	other, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(0)")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	_, err = other.Exec(`CREATE TABLE t (x)`)
	if err == nil {
		t.Fatal("a write under another connection's exclusive lock should be refused")
	}
	if !refusedLock(err) {
		t.Fatalf("SQLITE_BUSY should read as a refused lock, got %v", err)
	}
	if refusedLock(errors.New("database is locked")) || refusedLock(nil) {
		t.Fatal("only the driver's own busy code is a refused lock")
	}
}

// TestOfferDeclined_IsPerRepositoryAndPerOffer holds the offer flag to what
// it is for: one checkout's answer to one offer, and answering twice is the
// same answer.
func TestOfferDeclined_IsPerRepositoryAndPerOffer(t *testing.T) {
	db := openTestDB(t)
	if db.OfferDeclined("/src/shhh", "scaffold") {
		t.Fatal("an offer was refused before it was made")
	}
	if err := db.DeclineOffer("/src/shhh", "scaffold"); err != nil {
		t.Fatalf("decline: %v", err)
	}
	if !db.OfferDeclined("/src/shhh", "scaffold") {
		t.Fatal("the refusal was not remembered")
	}
	if db.OfferDeclined("/src/other", "scaffold") {
		t.Fatal("one repository's refusal answered for another")
	}
	if db.OfferDeclined("/src/shhh", "something-else") {
		t.Fatal("one offer's refusal answered for another")
	}
	if err := db.DeclineOffer("/src/shhh", "scaffold"); err != nil {
		t.Fatalf("declining twice: %v", err)
	}
}

// twoStores opens the same database twice, the way two `shhh code` processes
// on one machine hold it: separate connections, separate memories of what
// each has written.
func twoStores(t *testing.T) (*DB, *DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "shared.db")
	first, err := OpenPath(path)
	if err != nil {
		t.Fatalf("open first store: %v", err)
	}
	t.Cleanup(func() { first.Close() })
	second, err := OpenPath(path)
	if err != nil {
		t.Fatalf("open second store: %v", err)
	}
	t.Cleanup(func() { second.Close() })
	return first, second
}

// Two sessions started in the same second mint the same timestamp, so the
// name cannot be what tells them apart — the insert has to be.
func TestClaimChatSlot_SameSecondGetsItsOwnSlot(t *testing.T) {
	first, second := twoStores(t)

	stamp := "2026-09-02 10:00:00"
	a, err := first.ClaimChatSlot(stamp)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	b, err := second.ClaimChatSlot(stamp)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if a != stamp {
		t.Fatalf("the first session should get the name it asked for, got %q", a)
	}
	if b == a {
		t.Fatalf("two sessions were given the same slot %q", a)
	}

	if err := first.SaveChat(a, []provider.Message{{Role: provider.RoleUser, Content: "one"}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := second.SaveChat(b, []provider.Message{{Role: provider.RoleUser, Content: "two"}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	kept, err := first.LoadChat(a)
	if err != nil || len(kept) != 1 || kept[0].Content != "one" {
		t.Fatalf("the first conversation should be whole, got %v (err=%v)", kept, err)
	}
}

// A claim nobody wrote to is not a conversation: while a session sits idle,
// its slot must not be what --continue offers, and giving it back must not
// take another session's live claim with it.
func TestClaimChatSlot_UnwrittenSlotIsInvisible(t *testing.T) {
	first, second := twoStores(t)

	if err := first.SaveChat("yesterday", []provider.Message{{Role: provider.RoleUser, Content: "old"}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	idle, err := second.ClaimChatSlot("2026-09-02 10:00:00")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}

	recent, ok, err := first.MostRecentChat()
	if err != nil || !ok || recent.Name != "yesterday" {
		t.Fatalf("--continue should still find %q, got %q (ok=%v, err=%v)", "yesterday", recent.Name, ok, err)
	}

	// The claim is another store's; releasing it here must be a no-op.
	if err := first.ReleaseChatSlot(idle); err != nil {
		t.Fatalf("release: %v", err)
	}
	again, err := first.ClaimChatSlot(idle)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if again == idle {
		t.Fatalf("a live claim was handed to a second session as %q", again)
	}
	if err := second.ReleaseChatSlot(idle); err != nil {
		t.Fatalf("release: %v", err)
	}
	var rows int
	if err := first.sql.QueryRow(`SELECT COUNT(*) FROM chat_sessions WHERE name = ?`, idle).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("the session that claimed %q should have given it back", idle)
	}
}

// A save replaces what the slot holds, so a slot that grew under a session
// is refused rather than emptied: those messages are somebody else's.
func TestSaveChat_RefusesASlotAnotherSessionWrote(t *testing.T) {
	first, second := twoStores(t)

	slot, err := first.ClaimChatSlot("2026-09-02 10:00:00")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := first.SaveChat(slot, []provider.Message{
		{Role: provider.RoleUser, Content: "mine"},
		{Role: provider.RoleAssistant, Content: "ok"},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	// The second store resumes the slot and carries it on past where the
	// first one left it.
	if _, err := second.LoadChat(slot); err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := second.SaveChat(slot, []provider.Message{
		{Role: provider.RoleUser, Content: "mine"},
		{Role: provider.RoleAssistant, Content: "ok"},
		{Role: provider.RoleUser, Content: "and then"},
		{Role: provider.RoleAssistant, Content: "more"},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	err = first.SaveChat(slot, []provider.Message{{Role: provider.RoleUser, Content: "clobber"}})
	var taken ChatSlotConflictError
	if !errors.As(err, &taken) {
		t.Fatalf("expected the save to be refused, got %v", err)
	}
	if taken.Name != slot {
		t.Fatalf("the refusal should name the slot %q, got %q", slot, taken.Name)
	}
	kept, err := second.LoadChat(slot)
	if err != nil || len(kept) != 4 {
		t.Fatalf("the other session's conversation should be intact, got %d messages (err=%v)", len(kept), err)
	}

	// The conversation still has to go somewhere: the autosave moves it to
	// a slot of its own, and a second one refused at the same moment lands
	// in that same slot rather than leaving a copy behind.
	mine := []provider.Message{{Role: provider.RoleUser, Content: "clobber"}}
	moved, err := first.AutosaveChat(slot, "2026-09-02 10:04:00", mine, nil)
	if err != nil {
		t.Fatalf("autosave: %v", err)
	}
	if moved == slot {
		t.Fatalf("the autosave should have left the taken slot %q", slot)
	}
	again, err := first.AutosaveChat(slot, "2026-09-02 10:04:01", mine, nil)
	if err != nil {
		t.Fatalf("autosave: %v", err)
	}
	if again != moved {
		t.Fatalf("the second refusal should follow the first to %q, got %q", moved, again)
	}
	if held, err := first.LoadChat(moved); err != nil || len(held) != 1 {
		t.Fatalf("the moved conversation should be in %q, got %d messages (err=%v)", moved, len(held), err)
	}
	if theirs, err := second.LoadChat(slot); err != nil || len(theirs) != 4 {
		t.Fatalf("the other session's conversation should still be intact, got %d (err=%v)", len(theirs), err)
	}

	// A name this store has never touched is still its own to overwrite:
	// /save <name> is a decision the person made.
	if err := first.SaveChat("notes", []provider.Message{{Role: provider.RoleUser, Content: "first"}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := second.SaveChat("notes", []provider.Message{{Role: provider.RoleUser, Content: "second"}}); err != nil {
		t.Fatalf("a name nothing here wrote should not be refused: %v", err)
	}
}

// One process saves the same conversation from several goroutines at once —
// a turn ending while a cancel or a follow-up is still writing. None of
// those is another session, so none of them may be refused: a refusal here
// would move the conversation to a second slot and tell the reader that
// somebody else had taken the first.
func TestSaveChat_ConcurrentSavesFromOneProcessAreItsOwn(t *testing.T) {
	db := openTestDB(t)

	slot, err := db.ClaimChatSlot("2026-09-02 10:00:00")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "one"},
		{Role: provider.RoleAssistant, Content: "two"},
	}

	errs := make(chan error, 8)
	for range cap(errs) {
		go func() { errs <- db.SaveChat(slot, msgs) }()
	}
	for range cap(errs) {
		if err := <-errs; err != nil {
			t.Fatalf("a save from this process was refused: %v", err)
		}
	}
	if held, err := db.LoadChat(slot); err != nil || len(held) != len(msgs) {
		t.Fatalf("the slot should hold the conversation, got %d messages (err=%v)", len(held), err)
	}
}

// A conversation that was rewound or compacted is shorter than what the
// session last wrote, so a slot judged only by its length would let a save
// replace the shorter conversation another session had just put there.
func TestSaveChat_RefusesASlotThatShrankUnderIt(t *testing.T) {
	first, second := twoStores(t)

	slot, err := first.ClaimChatSlot("2026-09-02 10:00:00")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	long := []provider.Message{
		{Role: provider.RoleUser, Content: "one"},
		{Role: provider.RoleAssistant, Content: "two"},
		{Role: provider.RoleUser, Content: "three"},
	}
	if err := first.SaveChat(slot, long); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := second.LoadChat(slot); err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := second.SaveChat(slot, long[:1]); err != nil {
		t.Fatalf("save: %v", err)
	}

	err = first.SaveChat(slot, long)
	var taken ChatSlotConflictError
	if !errors.As(err, &taken) {
		t.Fatalf("expected the save to be refused, got %v", err)
	}
	if kept, err := second.LoadChat(slot); err != nil || len(kept) != 1 {
		t.Fatalf("the other session's conversation should be intact, got %d (err=%v)", len(kept), err)
	}
}

func TestListChats_MarksASlotARunningSessionStillHolds(t *testing.T) {
	db := openTestDB(t)
	liveOnly(t, 4242)

	for _, name := range []string{"busy", "finished"} {
		if _, err := db.ClaimChatSlot(name); err != nil {
			t.Fatalf("claim %s: %v", name, err)
		}
		if err := db.SaveChat(name, []provider.Message{{Role: provider.RoleUser, Content: "hello"}}); err != nil {
			t.Fatalf("save %s: %v", name, err)
		}
	}

	// One session in another process is writing "busy"; another wrote
	// "finished" and has since ended.
	busy := openSessionIn(t, db, "checkout-a", 4242, time.Now())
	ended := openSessionIn(t, db, "checkout-a", 4343, time.Now())
	if err := db.LinkAgentSession(busy, "busy"); err != nil {
		t.Fatalf("link busy: %v", err)
	}
	if err := db.LinkAgentSession(ended, "finished"); err != nil {
		t.Fatalf("link finished: %v", err)
	}
	if err := db.EndAgentSession(ended, ""); err != nil {
		t.Fatalf("end: %v", err)
	}

	entries, err := db.ListChats()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	live := map[string]bool{}
	for _, e := range entries {
		live[e.Name] = e.Live
	}
	if !live["busy"] {
		t.Error("the slot a running session holds is not marked live")
	}
	if live["finished"] {
		t.Error("a slot whose session ended is marked live")
	}
}

// liveChatFixture saves name and hands it to a session running under pid, the
// way another process's autosave leaves it.
func liveChatFixture(t *testing.T, db *DB, name string, pid int) {
	t.Helper()
	if err := db.SaveChat(name, []provider.Message{
		{Role: provider.RoleUser, Content: "hello"}}); err != nil {
		t.Fatalf("save %s: %v", name, err)
	}
	id := openSessionIn(t, db, "checkout-a", pid, time.Now())
	if err := db.LinkAgentSession(id, name); err != nil {
		t.Fatalf("link %s: %v", name, err)
	}
}

// Coming back to the last conversation steps past a slot another running
// session is autosaving into: what is in it is half of somebody else's turn,
// and their next save takes the slot back.
func TestMostRecentChat_StepsPastASlotARunningSessionHolds(t *testing.T) {
	db := openTestDB(t)
	liveOnly(t, 4242)

	if err := db.SaveChat("older", []provider.Message{
		{Role: provider.RoleUser, Content: "then"}}); err != nil {
		t.Fatalf("save older: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	liveChatFixture(t, db, "newest", 4242)

	recent, ok, err := db.MostRecentChat()
	if err != nil || !ok {
		t.Fatalf("ok = %v, err = %v; want the slot before the busy one", ok, err)
	}
	if recent.Name != "older" {
		t.Fatalf("name = %q, want the newest slot nobody else holds", recent.Name)
	}
	if recent.Held != "newest" {
		t.Fatalf("held = %q, want the slot that was stepped past", recent.Held)
	}
}

// Where the only slot is one somebody else holds there is nothing to come
// back to — and still something to say about why.
func TestMostRecentChat_NamesTheHeldSlotWithNothingBehindIt(t *testing.T) {
	db := openTestDB(t)
	liveOnly(t, 4242)
	liveChatFixture(t, db, "newest", 4242)

	recent, ok, err := db.MostRecentChat()
	if err != nil || ok {
		t.Fatalf("ok = %v, err = %v; want nothing to offer", ok, err)
	}
	if recent.Held != "newest" {
		t.Fatalf("held = %q, want the slot that was stepped past", recent.Held)
	}
}

// A slot whose session is gone is offered like any other. Nothing has to be
// forgiven for it: the row that session left open is closed at the next
// session's start, and a process that does not answer holds nothing.
func TestMostRecentChat_OffersASlotWhoseSessionIsGone(t *testing.T) {
	db := openTestDB(t)
	liveOnly(t)
	liveChatFixture(t, db, "newest", 4242)

	recent, ok, err := db.MostRecentChat()
	if err != nil || !ok {
		t.Fatalf("ok = %v, err = %v; want the slot offered", ok, err)
	}
	if recent.Name != "newest" || recent.Held != "" {
		t.Fatalf("got %q (held %q), want the slot a dead session left", recent.Name, recent.Held)
	}
}

// chatMessageIDs is the row ids of a slot's messages in order. It is what
// tells an append from a rewrite: a rewrite gives every message a new row.
func chatMessageIDs(t *testing.T, db *DB, name string) []int64 {
	t.Helper()
	rows, err := db.sql.Query(
		`SELECT m.id FROM chat_messages m
		   JOIN chat_sessions s ON s.id = m.session_id
		  WHERE s.name = ? ORDER BY m.seq`, name)
	if err != nil {
		t.Fatalf("read message ids: %v", err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan id: %v", err)
		}
		ids = append(ids, id)
	}
	return ids
}

func TestAutosaveChat_WritesOnlyTheMessagesTheSlotDoesNotHave(t *testing.T) {
	db := openTestDB(t)

	msgs := []provider.Message{
		{Role: provider.RoleUser, Content: "why does it flake"},
		{Role: provider.RoleAssistant, Content: "it races the timer"},
	}
	if _, err := db.AutosaveChat("slot", "fresh", msgs, nil); err != nil {
		t.Fatalf("first save: %v", err)
	}
	before := chatMessageIDs(t, db, "slot")

	msgs = append(msgs, provider.Message{Role: provider.RoleUser, Content: "so what fixes it"})
	if _, err := db.AutosaveChat("slot", "fresh", msgs, nil); err != nil {
		t.Fatalf("second save: %v", err)
	}
	after := chatMessageIDs(t, db, "slot")

	if len(after) != len(before)+1 {
		t.Fatalf("the slot should hold one more message, got %d after %d", len(after), len(before))
	}
	for i, id := range before {
		if after[i] != id {
			t.Fatalf("message %d was rewritten: row %d became row %d", i, id, after[i])
		}
	}
	if after[len(before)] <= before[len(before)-1] {
		t.Fatalf("the new message should be a new row, got %v after %v", after, before)
	}
}

func TestAutosaveChat_WritesNothingWhenTheConversationHasNotMoved(t *testing.T) {
	db := openTestDB(t)

	msgs := []provider.Message{{Role: provider.RoleUser, Content: "still thinking"}}
	if _, err := db.AutosaveChat("slot", "fresh", msgs, nil); err != nil {
		t.Fatalf("first save: %v", err)
	}
	before := chatMessageIDs(t, db, "slot")
	if _, err := db.AutosaveChat("slot", "fresh", msgs, nil); err != nil {
		t.Fatalf("second save: %v", err)
	}
	if after := chatMessageIDs(t, db, "slot"); len(after) != len(before) || after[0] != before[0] {
		t.Fatalf("a save with nothing new should write nothing, got %v after %v", after, before)
	}
}

func TestAutosaveChat_RewritesWhenTheConversationChangedBehindItself(t *testing.T) {
	db := openTestDB(t)

	msgs := []provider.Message{
		{Role: provider.RoleUser, Content: "first ask"},
		{Role: provider.RoleAssistant, Content: "first answer"},
		{Role: provider.RoleUser, Content: "second ask"},
	}
	if _, err := db.AutosaveChat("slot", "fresh", msgs, nil); err != nil {
		t.Fatalf("first save: %v", err)
	}
	// A second conversation saved after it, so the store's next row id is
	// past the slot's own: without it a rewrite would hand the same ids back
	// and there would be nothing here to read.
	if err := db.SaveChat("other", msgs); err != nil {
		t.Fatalf("save other: %v", err)
	}
	before := chatMessageIDs(t, db, "slot")

	// A compaction: the opening is replaced by a summary and the tail kept,
	// so the slot ends up exactly as long as it was with a different
	// conversation in it. Judged by its length alone this would be taken for
	// a save with nothing new.
	compacted := []provider.Message{
		{Role: provider.RoleUser, Content: "summary of the opening"},
		{Role: provider.RoleAssistant, Content: "first answer"},
		{Role: provider.RoleUser, Content: "second ask"},
	}
	if _, err := db.AutosaveChat("slot", "fresh", compacted, nil); err != nil {
		t.Fatalf("second save: %v", err)
	}
	if after := chatMessageIDs(t, db, "slot"); after[0] == before[0] {
		t.Fatal("a conversation rewritten behind itself should be written whole")
	}
	got, err := db.LoadChat("slot")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 3 || got[0].Content != "summary of the opening" {
		t.Fatalf("the slot should hold the compacted conversation, got %+v", got)
	}
}

func TestAutosaveChat_RewritesAfterARewind(t *testing.T) {
	db := openTestDB(t)

	msgs := []provider.Message{
		{Role: provider.RoleUser, Content: "first ask"},
		{Role: provider.RoleAssistant, Content: "first answer"},
		{Role: provider.RoleUser, Content: "second ask"},
	}
	if _, err := db.AutosaveChat("slot", "fresh", msgs, nil); err != nil {
		t.Fatalf("first save: %v", err)
	}
	if _, err := db.AutosaveChat("slot", "fresh", msgs[:1], nil); err != nil {
		t.Fatalf("rewound save: %v", err)
	}
	got, err := db.LoadChat("slot")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 1 || got[0].Content != "first ask" {
		t.Fatalf("the rewind should have left one message, got %+v", got)
	}
	// And the slot goes on appending from where the rewind left it.
	if _, err := db.AutosaveChat("slot", "fresh",
		append(msgs[:1:1], provider.Message{Role: provider.RoleAssistant, Content: "another answer"}), nil); err != nil {
		t.Fatalf("save after rewind: %v", err)
	}
	if got, _ := db.LoadChat("slot"); len(got) != 2 || got[1].Content != "another answer" {
		t.Fatalf("the conversation should carry on from the rewind, got %+v", got)
	}
}

func TestSearchChats_FindsAWordSaidInTheConversation(t *testing.T) {
	db := openTestDB(t)

	if err := db.SaveChat("alpha", []provider.Message{
		{Role: provider.RoleUser, Content: "why does the retry flake"},
		{Role: provider.RoleAssistant, Content: "it races the timer"},
	}); err != nil {
		t.Fatalf("save alpha: %v", err)
	}
	if err := db.SaveChat("beta", []provider.Message{
		{Role: provider.RoleUser, Content: "how do I bump the version"},
	}); err != nil {
		t.Fatalf("save beta: %v", err)
	}

	found, err := db.SearchChats("timer")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(found) != 1 || found[0].Name != "alpha" {
		t.Fatalf("a word said in alpha should find alpha, got %+v", found)
	}
	// The words narrow rather than widen: both of these are in alpha alone.
	if found, _ := db.SearchChats("retry timer"); len(found) != 1 || found[0].Name != "alpha" {
		t.Fatalf("two words should still find alpha, got %+v", found)
	}
	// A half-typed word finds it too, which is what a picker needs.
	if found, _ := db.SearchChats("tim"); len(found) != 1 || found[0].Name != "alpha" {
		t.Fatalf("a prefix should find alpha, got %+v", found)
	}
	if found, _ := db.SearchChats("nothing said here"); len(found) != 0 {
		t.Fatalf("a query nothing carries should find nothing, got %+v", found)
	}
	if found, _ := db.SearchChats("   "); len(found) != 0 {
		t.Fatalf("a query with no word in it should find nothing, got %+v", found)
	}
}

func TestSearchChats_FindsAChatWithNoMessagesByItsTitle(t *testing.T) {
	db := openTestDB(t)

	if _, err := db.ClaimChatSlot("2026-09-04 11:02"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := db.SetChatTitle("2026-09-04 11:02", "The flaky retry test"); err != nil {
		t.Fatalf("title: %v", err)
	}

	found, err := db.SearchChats("flaky")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(found) != 1 || found[0].Name != "2026-09-04 11:02" {
		t.Fatalf("a title should find a chat holding no messages, got %+v", found)
	}
	if found[0].Turns != 0 {
		t.Fatalf("it holds no turns, got %d", found[0].Turns)
	}
}

func TestSearchChats_ForgetsAConversationThatWasDeleted(t *testing.T) {
	db := openTestDB(t)

	if err := db.SaveChat("alpha", []provider.Message{
		{Role: provider.RoleUser, Content: "why does the retry flake"},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := db.DeleteChat("alpha"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if found, _ := db.SearchChats("retry"); len(found) != 0 {
		t.Fatalf("a deleted conversation should not be findable, got %+v", found)
	}
	// And its words leave the index with it, rather than sitting there
	// pointing at rows that have gone.
	var left int
	if err := db.sql.QueryRow(
		`SELECT COUNT(*) FROM chat_message_search WHERE chat_message_search MATCH 'retry'`).Scan(&left); err != nil {
		t.Fatalf("count index rows: %v", err)
	}
	if left != 0 {
		t.Fatalf("the index should have let the words go, %d left", left)
	}
}

func TestPruneOldChats_TakesAFamilyPastTheWindow(t *testing.T) {
	db := openTestDB(t)

	msgs := []provider.Message{{Role: provider.RoleUser, Content: "an old question"}}
	for _, name := range []string{"old", "old@turn1", "recent"} {
		if err := db.SaveChat(name, msgs); err != nil {
			t.Fatalf("save %s: %v", name, err)
		}
	}
	if err := db.SaveChatBranch("old", "old@turn1", msgs); err != nil {
		t.Fatalf("branch: %v", err)
	}
	long := time.Now().UTC().AddDate(0, 0, -400).Format(time.RFC3339Nano)
	if _, err := db.sql.Exec(
		`UPDATE chat_sessions SET updated_at = ? WHERE name IN ('old', 'old@turn1')`, long); err != nil {
		t.Fatalf("age the family: %v", err)
	}

	gone, err := db.PruneOldChats(90)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if gone != 2 {
		t.Fatalf("the whole family should go, got %d", gone)
	}
	if _, err := db.LoadChat("old"); err == nil {
		t.Fatal("the old chat should be gone")
	}
	if _, err := db.LoadChat("old@turn1"); err == nil {
		t.Fatal("its branch should be gone with it")
	}
	if _, err := db.LoadChat("recent"); err != nil {
		t.Fatalf("the recent chat should be untouched, got %v", err)
	}
}

func TestPruneOldChats_KeepsAFamilyWithABranchInsideTheWindow(t *testing.T) {
	db := openTestDB(t)

	msgs := []provider.Message{{Role: provider.RoleUser, Content: "an old question"}}
	if err := db.SaveChat("root", msgs); err != nil {
		t.Fatalf("save root: %v", err)
	}
	if err := db.SaveChatBranch("root", "root@turn1", msgs); err != nil {
		t.Fatalf("branch: %v", err)
	}
	long := time.Now().UTC().AddDate(0, 0, -400).Format(time.RFC3339Nano)
	if _, err := db.sql.Exec(`UPDATE chat_sessions SET updated_at = ? WHERE name = 'root'`, long); err != nil {
		t.Fatalf("age the root: %v", err)
	}

	gone, err := db.PruneOldChats(90)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if gone != 0 {
		t.Fatalf("a family somebody is still working in should stay, %d went", gone)
	}
	if _, err := db.LoadChat("root"); err != nil {
		t.Fatalf("the root should stay with its branch, got %v", err)
	}
}

func TestPruneOldChats_OffUntilAWindowIsSet(t *testing.T) {
	db := openTestDB(t)

	if err := db.SaveChat("ancient", []provider.Message{{Role: provider.RoleUser, Content: "hello"}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	long := time.Now().UTC().AddDate(-5, 0, 0).Format(time.RFC3339Nano)
	if _, err := db.sql.Exec(`UPDATE chat_sessions SET updated_at = ?`, long); err != nil {
		t.Fatalf("age it: %v", err)
	}
	if gone, err := db.PruneOldChats(0); err != nil || gone != 0 {
		t.Fatalf("no window means nothing goes, got %d, %v", gone, err)
	}
	if _, err := db.LoadChat("ancient"); err != nil {
		t.Fatalf("the conversation should still be there, got %v", err)
	}
}

// The full-text indexes write bookkeeping rows of their own the moment a
// store is created, and a store that has never been used has to keep reading
// as one: what turns on this is whether the migration off an older data
// directory offers to replace the file at the destination or refuses to touch
// it as a conflict.
func TestRecorded_AStoreNobodyHasUsedHoldsNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fresh.db")
	db, err := OpenPath(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.Close()

	held, err := Recorded(path)
	if err != nil {
		t.Fatalf("recorded: %v", err)
	}
	if held {
		t.Fatal("a store nobody has written to should hold nothing")
	}

	db, err = OpenPath(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if err := db.SaveChat("something", []provider.Message{{Role: provider.RoleUser, Content: "hello"}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	db.Close()
	if held, err = Recorded(path); err != nil || !held {
		t.Fatalf("a store with a conversation in it holds something, got %v, %v", held, err)
	}
}

// A name the person typed is theirs to overwrite, and the index has to follow
// the overwrite: a slot this process has never seen is written whole even
// when the messages would have extended it, and the words it replaced go.
func TestSaveChat_OverwritesASlotThisProcessHasNeverSeen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	first, err := OpenPath(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := first.SaveChat("notes", []provider.Message{
		{Role: provider.RoleUser, Content: "kubernetes"},
		{Role: provider.RoleUser, Content: "shared"},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	first.Close()

	second, err := OpenPath(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer second.Close()
	if err := second.SaveChat("notes", []provider.Message{
		{Role: provider.RoleUser, Content: "postgres"},
		{Role: provider.RoleUser, Content: "shared"},
		{Role: provider.RoleUser, Content: "and one more"},
	}); err != nil {
		t.Fatalf("overwrite: %v", err)
	}

	got, err := second.LoadChat("notes")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 3 || got[0].Content != "postgres" {
		t.Fatalf("the slot should hold what was written over it, got %+v", got)
	}
	if found, _ := second.SearchChats("kubernetes"); len(found) != 0 {
		t.Fatalf("a message written over should have left the index, got %+v", found)
	}
	if found, _ := second.SearchChats("postgres"); len(found) != 1 {
		t.Fatalf("what replaced it should be findable, got %+v", found)
	}
}

// The family is the whole tree and not one generation of it, and what a prune
// takes it takes completely: the messages go, and their words leave the index
// rather than sitting in it pointing at rows that have gone.
func TestPruneOldChats_TakesTheWholeTreeAndItsWords(t *testing.T) {
	db := openTestDB(t)

	msgs := []provider.Message{{Role: provider.RoleUser, Content: "kubernetes"}}
	for _, name := range []string{"root", "root@turn1", "root@turn1@turn3"} {
		if err := db.SaveChat(name, msgs); err != nil {
			t.Fatalf("save %s: %v", name, err)
		}
	}
	if err := db.SaveChatBranch("root", "root@turn1", msgs); err != nil {
		t.Fatalf("branch: %v", err)
	}
	if err := db.SaveChatBranch("root@turn1", "root@turn1@turn3", msgs); err != nil {
		t.Fatalf("branch of a branch: %v", err)
	}
	long := time.Now().UTC().AddDate(0, 0, -400).Format(time.RFC3339Nano)
	if _, err := db.sql.Exec(`UPDATE chat_sessions SET updated_at = ?`, long); err != nil {
		t.Fatalf("age the tree: %v", err)
	}

	gone, err := db.PruneOldChats(90)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if gone != 3 {
		t.Fatalf("the branch of a branch should go too, %d went", gone)
	}
	var messages, indexed int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM chat_messages`).Scan(&messages); err != nil {
		t.Fatalf("count messages: %v", err)
	}
	if err := db.sql.QueryRow(
		`SELECT COUNT(*) FROM chat_message_search WHERE chat_message_search MATCH 'kubernetes'`).Scan(&indexed); err != nil {
		t.Fatalf("count index rows: %v", err)
	}
	if messages != 0 || indexed != 0 {
		t.Fatalf("%d messages and %d index rows outlived the conversations they belong to", messages, indexed)
	}
}
