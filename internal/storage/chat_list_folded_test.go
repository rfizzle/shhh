package storage

import (
	"path/filepath"
	"testing"

	"github.com/rfizzle/shhh/internal/provider"
)

func TestChatList_ACompactedSessionsCountIsTheRuleChosen(t *testing.T) {
	db, err := OpenPath(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	// Two live user turns at seq >= 0, two folded user turns at seq < 0.
	live := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "summary", Machine: true},
		{Role: provider.RoleUser, Content: "zebrafinch live"},
	}
	folded := []provider.Message{
		{Role: provider.RoleUser, Content: "first"},
		{Role: provider.RoleAssistant, Content: "answer"},
		{Role: provider.RoleUser, Content: "second"},
	}
	if err := db.SaveChat("s", live); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveChatFolded("s", folded); err != nil {
		t.Fatal(err)
	}

	const want = 4 // every user turn the record holds, folded ones included
	list, err := db.ListChats()
	if err != nil || len(list) != 1 {
		t.Fatalf("ListChats = %v, %v", list, err)
	}
	if list[0].Turns != want {
		t.Errorf("list counts %d turns, want %d", list[0].Turns, want)
	}
	found, err := db.SearchChats("zebrafinch")
	if err != nil || len(found) != 1 {
		t.Fatalf("SearchChats = %v, %v", found, err)
	}
	if found[0].Turns != want {
		t.Errorf("search counts %d turns, want %d", found[0].Turns, want)
	}
}
