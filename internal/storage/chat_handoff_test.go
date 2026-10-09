package storage

import (
	"errors"
	"testing"

	"github.com/rfizzle/shhh/internal/provider"
)

// A handoff is kept on its slot, survives the saves after it and the resume
// columns being rewritten, and a slot that never had one answers empty.
func TestChatHandoff_IsKeptBesideTheConversation(t *testing.T) {
	db := openTestDB(t)
	msgs := []provider.Message{{Role: provider.RoleUser, Content: "hi"}}
	if err := db.SaveChat("h", msgs); err != nil {
		t.Fatalf("save: %v", err)
	}
	if got, err := db.ChatHandoff("h"); err != nil || got != "" {
		t.Fatalf("a slot with no handoff answers empty, got %q %v", got, err)
	}
	if err := db.SetChatHandoff("h", "Retry backoff half done\nopen: the timer test"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := db.SaveChat("h", append(msgs, provider.Message{Role: provider.RoleAssistant, Content: "ok"})); err != nil {
		t.Fatalf("save again: %v", err)
	}
	if err := db.SetChatResume("h", ChatResume{Summary: "s"}); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if got, _ := db.ChatHandoff("h"); got != "Retry backoff half done\nopen: the timer test" {
		t.Fatalf("the handoff should outlive the saves after it, got %q", got)
	}
	var missing ChatNotFoundError
	if err := db.SetChatHandoff("nobody", "x"); !errors.As(err, &missing) {
		t.Fatalf("a slot that does not exist should say so, got %v", err)
	}
	if got, err := db.ChatHandoff("nobody"); err != nil || got != "" {
		t.Fatalf("an unknown slot answers empty, got %q %v", got, err)
	}
}
