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

	if out := manage(nil); !strings.Contains(out, "⊘ nothing remembered yet") {
		t.Fatalf("empty list: %q", out)
	}

	out := manage([]string{"add", "prefers", "short", "answers"})
	if !strings.Contains(out, "✓ remembered m1") || !strings.Contains(out, "project preference") {
		t.Fatalf("default add should be project preference: %q", out)
	}

	out = manage([]string{"add", "global", "convention", "commit subjects are imperative"})
	if !strings.Contains(out, "global convention") {
		t.Fatalf("scope and kind tokens should apply: %q", out)
	}

	out = manage([]string{"list"})
	if !strings.Contains(out, "m1") || !strings.Contains(out, "m2") || !strings.Contains(out, "user") {
		t.Fatalf("list should show both entries with provenance: %q", out)
	}

	if out := manage([]string{"forget", "m1"}); !strings.Contains(out, "✓ forgot m1") {
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
		out := manage(args)
		if !strings.Contains(out, "Usage:") {
			t.Errorf("%v should print usage, got %q", args, out)
		}
		// edit is answered by the chat surface, which opens the reader's
		// editor; the usage line is still where a reader looks for it.
		if !strings.Contains(out, "edit <id>") {
			t.Errorf("%v: usage should name edit, got %q", args, out)
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
	if !strings.Contains(out, "✓ remembered m1") {
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

func TestMemoryTextAndRewriter(t *testing.T) {
	_, store := testMemoryManager(t)
	e, err := store.Add("/proj", memory.KindConvention, "wrap errors", memory.ProvenanceUser)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	read, rewrite := memoryText(store), memoryRewriter(store)

	text, err := read(e.ID)
	if err != nil || text != "wrap errors" {
		t.Fatalf("the editor opens on the stored text, got %q (%v)", text, err)
	}
	if _, err := read(e.ID + 999); err == nil {
		t.Fatal("an id nothing is stored under should surface as an error")
	}

	out, err := rewrite(e.ID, "wrap errors with %w")
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if !strings.Contains(out, "✓ rewrote m1") || !strings.Contains(out, "project convention") {
		t.Fatalf("rewrite result: %q", out)
	}
	entries, err := store.List()
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected one stored entry, got %d (%v)", len(entries), err)
	}
	if entries[0].Text != "wrap errors with %w" {
		t.Fatalf("the edit should be durable, got %q", entries[0].Text)
	}
	if !entries[0].UpdatedAt.After(e.UpdatedAt) {
		t.Fatalf("an edited memory sorts as freshly stated: %s not after %s", entries[0].UpdatedAt, e.UpdatedAt)
	}

	if _, err := rewrite(e.ID, "   "); err == nil {
		t.Fatal("an empty rewrite should surface as an error")
	}
}
