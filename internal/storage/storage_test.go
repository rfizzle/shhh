package storage

import (
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
	moved, err := first.AutosaveChat(slot, "2026-09-02 10:04:00", mine)
	if err != nil {
		t.Fatalf("autosave: %v", err)
	}
	if moved == slot {
		t.Fatalf("the autosave should have left the taken slot %q", slot)
	}
	again, err := first.AutosaveChat(slot, "2026-09-02 10:04:01", mine)
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
