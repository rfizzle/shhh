package tools_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/tools"
)

// shhh's own store is readable while a session writes to it: a read-only
// reader of a WAL database blocks no writer, so an autosave landing while the
// tool holds a statement open still lands. The answer on that file names the
// dashboard that already reads it.
func TestSqlite_ShhhsOwnStoreIsReadWhileTheSessionWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shhh.db")
	db, err := storage.OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	msgs := []provider.Message{{Role: "user", Content: "first"}}
	if _, err := db.AutosaveChat("sitting", "sitting-2", msgs, nil); err != nil {
		t.Fatal(err)
	}

	var saveErr error
	saved := false
	restore := tools.HoldSqliteRead(func() {
		if saved {
			return
		}
		saved = true
		msgs = append(msgs, provider.Message{Role: "assistant", Content: "second"})
		_, saveErr = db.AutosaveChat("sitting", "sitting-2", msgs, nil)
	})
	defer restore()

	raw, _ := json.Marshal(map[string]any{"path": path, "sql": []string{"SELECT name FROM chat_sessions"}})
	out, err := tools.NewRecorder().Execute(tools.SqliteName, raw)
	if err != nil {
		t.Fatalf("sqlite on the store: %v", err)
	}
	if !saved {
		t.Fatal("the read was never held open")
	}
	if saveErr != nil {
		t.Fatalf("the autosave under an open read failed: %v", saveErr)
	}
	loaded, err := db.LoadChat("sitting")
	if err != nil || len(loaded) != 2 {
		t.Fatalf("the save did not land: %d messages, %v", len(loaded), err)
	}
	if first, _, _ := strings.Cut(out, "\n"); !strings.Contains(first, "`shhh observe`") {
		t.Errorf("the answer on shhh's store should lead with the dashboard, got:\n%s", out)
	}
	if !strings.Contains(out, "sitting") {
		t.Errorf("the rows should follow:\n%s", out)
	}

	// The schema answer names it too, and an ordinary database does not.
	raw, _ = json.Marshal(map[string]any{"path": path})
	if out, err := tools.NewRecorder().Execute(tools.SqliteName, raw); err != nil || strings.Count(out, "shhh observe") != 1 {
		t.Errorf("schema on the store: %v\n%s", err, out)
	}
}
