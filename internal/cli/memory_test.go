package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/memory"
	"github.com/rfizzle/shhh/internal/storage"
)

func testMemoryManager(t *testing.T) (func(args []string) string, *memory.Store) {
	t.Helper()
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	store := memory.NewStore(db, "/proj")
	return memoryManager(store), store
}

func TestMemoryManager_ListAddForget(t *testing.T) {
	manage, _ := testMemoryManager(t)

	if out := manage(nil); !strings.Contains(out, "No memories yet") {
		t.Fatalf("empty list: %q", out)
	}

	out := manage([]string{"add", "prefers", "short", "answers"})
	if !strings.Contains(out, "[m1]") || !strings.Contains(out, "(project preference)") {
		t.Fatalf("default add should be project preference: %q", out)
	}

	out = manage([]string{"add", "global", "convention", "commit subjects are imperative"})
	if !strings.Contains(out, "(global convention)") {
		t.Fatalf("scope and kind tokens should apply: %q", out)
	}

	out = manage([]string{"list"})
	if !strings.Contains(out, "[m1]") || !strings.Contains(out, "[m2]") || !strings.Contains(out, "user") {
		t.Fatalf("list should show both entries with provenance: %q", out)
	}

	if out := manage([]string{"forget", "m1"}); !strings.Contains(out, "Forgot memory [m1]") {
		t.Fatalf("forget: %q", out)
	}
	if out := manage([]string{"forget", "m1"}); !strings.Contains(out, "Error") {
		t.Fatalf("forgetting a missing entry should error: %q", out)
	}
	if out := manage([]string{"forget", "banana"}); !strings.Contains(out, "invalid memory id") {
		t.Fatalf("bad id should error: %q", out)
	}
}

func TestMemoryManager_Usage(t *testing.T) {
	manage, _ := testMemoryManager(t)
	for _, args := range [][]string{{"add"}, {"add", "global"}, {"forget"}, {"bogus"}} {
		if out := manage(args); !strings.Contains(out, "Usage:") {
			t.Errorf("%v should print usage, got %q", args, out)
		}
	}
}

func TestMemorySaver_AgentProvenance(t *testing.T) {
	_, store := testMemoryManager(t)
	save := memorySaver(store)

	out, err := save("/proj", memory.KindLesson, "gofmt runs via make fmt")
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if !strings.Contains(out, "Saved memory [m1]") {
		t.Fatalf("save result: %q", out)
	}
	entries, err := store.List()
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected one stored entry, got %d (%v)", len(entries), err)
	}
	if entries[0].Provenance != memory.ProvenanceAgent {
		t.Fatalf("confirmed proposals record agent provenance, got %q", entries[0].Provenance)
	}

	if _, err := save("/proj", "vibe", "x"); err == nil {
		t.Fatal("invalid kind should surface as an error")
	}
}
