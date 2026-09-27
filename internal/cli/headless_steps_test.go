package cli

import (
	"path/filepath"
	"testing"

	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/storage"
)

// A headless run carrying a conversation on reads no working list of its own,
// so its save puts back the one the session left rather than writing none
// over it (docs/capabilities/coding-agent.md#the-session-keeps-its-own-working-steps).
func TestHeadlessSave_KeepsTheSessionsWorkingList(t *testing.T) {
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	const steps = `{"steps":[{"n":1,"title":"Read"},{"n":2,"title":"Patch"}],"done":[1]}`
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "carry on"},
	}
	if err := db.SaveChat("left", msgs); err != nil {
		t.Fatal(err)
	}
	if err := db.SetChatResume("left", storage.ChatResume{Steps: steps}); err != nil {
		t.Fatal(err)
	}

	c := &headlessChat{db: db, slot: "left", kind: "code", steps: steps}
	c.save(append(msgs, provider.Message{Role: provider.RoleAssistant, Content: "done"}))
	got, err := db.ChatResume(c.slot)
	if err != nil {
		t.Fatal(err)
	}
	if got.Steps != steps {
		t.Fatalf("the run's save wrote the list as %q, want the session's kept", got.Steps)
	}
}
