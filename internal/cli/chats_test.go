package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/storage"
)

// chatsDB points the data directory at a temp store holding two chats and
// a branch, and returns nothing: the commands open the store themselves.
func chatsDB(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	db, err := storage.Open()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "why does it flake"},
		{Role: provider.RoleAssistant, Content: "it races the timer", ToolCalls: []provider.ToolCall{{ID: "c1", Name: "read_file", Arguments: `{"path":"a.go"}`}}},
	}
	if err := db.SaveChat("alpha", msgs); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveChatBranch("alpha", "alpha@turn2", msgs); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveChat("beta", msgs[:2]); err != nil {
		t.Fatal(err)
	}
	if err := db.SetChatTitle("alpha", "Flaky retry test"); err != nil {
		t.Fatal(err)
	}
}

// runChats runs `shhh chats <args>` and returns what it printed and the
// error it returned. The command is executed on its own rather than under
// the root, whose pre-run opens the store in a goroutine that outlives the
// test and would race the next test's fresh store.
func runChats(t *testing.T, in string, args ...string) (string, error) {
	t.Helper()
	cmd := newChatsCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetIn(strings.NewReader(in))
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestChats_HelpSucceeds(t *testing.T) {
	out, err := runChats(t, "", "--help")
	if err != nil {
		t.Fatalf("shhh chats --help: %v", err)
	}
	for _, want := range []string{"list", "show", "delete", "rename", "browser"} {
		if !strings.Contains(out, want) {
			t.Errorf("help should mention %q, got:\n%s", want, out)
		}
	}
}

func TestChats_ListTextAndJSON(t *testing.T) {
	chatsDB(t)
	out, err := runChats(t, "", "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out, "NAME") || !strings.Contains(out, "Flaky retry test") || !strings.Contains(out, "beta") {
		t.Fatalf("the listing should carry names and titles, got:\n%s", out)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 4 {
		t.Fatalf("a header and three rows, got %d:\n%s", len(lines), out)
	}
	if !strings.Contains(out, "—") {
		t.Fatal("an untitled chat shows a dash in the title column")
	}

	out, err = runChats(t, "", "list", "--json")
	if err != nil {
		t.Fatalf("list --json: %v", err)
	}
	var rows []chatRow
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("the JSON should round-trip: %v\n%s", err, out)
	}
	if len(rows) != 3 {
		t.Fatalf("expected three rows, got %+v", rows)
	}
	var alpha chatRow
	for _, r := range rows {
		if r.Name == "alpha" {
			alpha = r
		}
	}
	if alpha.Title != "Flaky retry test" || alpha.Turns != 1 || alpha.UpdatedAt.IsZero() {
		t.Fatalf("alpha should carry its title, turns and time, got %+v", alpha)
	}
}

func TestChats_ShowTextAndJSON(t *testing.T) {
	chatsDB(t)
	out, err := runChats(t, "", "show", "alpha")
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	if strings.Contains(out, "sys") {
		t.Fatal("the system prompt is not part of the transcript")
	}
	for _, want := range []string{"user: why does it flake", "assistant: it races the timer", "→ read_file"} {
		if !strings.Contains(out, want) {
			t.Errorf("transcript should carry %q, got:\n%s", want, out)
		}
	}
	out, err = runChats(t, "", "show", "alpha", "--json")
	if err != nil {
		t.Fatalf("show --json: %v", err)
	}
	var msgs []chatMessage
	if err := json.Unmarshal([]byte(out), &msgs); err != nil {
		t.Fatalf("the JSON should round-trip: %v\n%s", err, out)
	}
	if len(msgs) != 3 || msgs[2].ToolCalls[0] != (chatToolCall{Name: "read_file", Arguments: `{"path":"a.go"}`}) {
		t.Fatalf("messages should carry their tool calls, got %+v", msgs)
	}
	if _, err := runChats(t, "", "show", "nope"); err == nil || !strings.Contains(err.Error(), "shhh chats list") {
		t.Fatalf("a missing chat names the way out, got %v", err)
	}
}

func TestChats_DeleteAsksThenRemovesBranches(t *testing.T) {
	chatsDB(t)
	out, err := runChats(t, "n\n", "delete", "alpha")
	if err != nil || !strings.Contains(out, "Kept.") {
		t.Fatalf("n keeps the chat, got %q %v", out, err)
	}
	if !strings.Contains(out, `Delete "alpha" and its 1 branch?`) {
		t.Fatalf("the question names the chat and its branches, got %q", out)
	}
	out, err = runChats(t, "", "delete", "alpha", "--yes")
	if err != nil || !strings.Contains(out, `Deleted "alpha" and its 1 branch.`) {
		t.Fatalf("--yes deletes without asking, got %q %v", out, err)
	}
	out, err = runChats(t, "", "list", "--json")
	if err != nil || strings.Contains(out, "alpha") {
		t.Fatalf("alpha and its branch should be gone, got %s %v", out, err)
	}
	if _, err := runChats(t, "", "delete", "alpha", "--yes"); err == nil {
		t.Fatal("deleting a missing chat is an error")
	}
}

func TestChats_BareWithNothingSavedNeedsNoProvider(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if _, err := runChats(t, ""); err != nil {
		t.Fatalf("an empty store is a note on stderr and a clean exit, not a session, got %v", err)
	}
}

func TestChats_Rename(t *testing.T) {
	chatsDB(t)
	out, err := runChats(t, "", "rename", "alpha", "retry flake")
	if err != nil || !strings.Contains(out, `Renamed "alpha" to "retry flake".`) {
		t.Fatalf("rename: %q %v", out, err)
	}
	if _, err := runChats(t, "", "rename", "beta", "retry flake"); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("a collision is refused by name, got %v", err)
	}
	if _, err := runChats(t, "", "rename", "nope", "x"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("a missing chat is reported, got %v", err)
	}
}
