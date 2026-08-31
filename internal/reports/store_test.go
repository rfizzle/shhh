package reports

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func openTestStore(t *testing.T, base string) *Store {
	t.Helper()
	s, err := Open(base, 90)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

func TestStore_PutListLoad(t *testing.T) {
	s := openTestStore(t, t.TempDir())
	id, err := s.Put(sampleDocument(), Meta{Title: "timing", Project: "/p", Origin: "code"})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if !idRe.MatchString(id) {
		t.Fatalf("id %q does not match the opaque id shape", id)
	}

	entries := s.List()
	if len(entries) != 1 || entries[0].ID != id || entries[0].Title != "timing" {
		t.Fatalf("List = %+v", entries)
	}
	if entries[0].Size <= 0 || entries[0].Created.IsZero() {
		t.Fatalf("meta not filled in: %+v", entries[0].Meta)
	}

	doc, meta, err := s.Load(id)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if doc.Title != "Suite timing breakdown" || meta.Project != "/p" {
		t.Fatalf("Load = %q / %+v", doc.Title, meta)
	}
}

func TestStore_ListNewestFirst(t *testing.T) {
	s := openTestStore(t, t.TempDir())
	old, _ := s.Put(Document{Title: "old", Blocks: []Block{{Type: BlockProse, Text: "x"}}}, Meta{Created: time.Now().Add(-time.Hour)})
	fresh, _ := s.Put(Document{Title: "new", Blocks: []Block{{Type: BlockProse, Text: "x"}}}, Meta{})
	entries := s.List()
	if len(entries) != 2 || entries[0].ID != fresh || entries[1].ID != old {
		t.Fatalf("List order = %+v", entries)
	}
}

func TestStore_AnIdNeverNamesAPath(t *testing.T) {
	s := openTestStore(t, t.TempDir())
	for _, id := range []string{"../secrets", "rp-XYZ", "rp-0123", "", "rp-0123456789abcdef0"} {
		if _, _, err := s.Load(id); err == nil || !strings.Contains(err.Error(), "no report with id") {
			t.Fatalf("Load(%q) = %v, want the not-found wording", id, err)
		}
	}
	// A well-shaped id that was never stored fails with the same wording.
	if _, _, err := s.Load("rp-0123456789abcdef"); err == nil || !strings.Contains(err.Error(), "no report with id") {
		t.Fatalf("unknown id did not fail like a malformed one: %v", err)
	}
}

func TestStore_PruneByAgeAndOrphans(t *testing.T) {
	base := t.TempDir()
	s := openTestStore(t, base)
	keep, _ := s.Put(Document{Title: "keep", Blocks: []Block{{Type: BlockProse, Text: "x"}}}, Meta{})
	drop, _ := s.Put(Document{Title: "drop", Blocks: []Block{{Type: BlockProse, Text: "x"}}}, Meta{Created: time.Now().Add(-91 * 24 * time.Hour)})
	orphan := filepath.Join(base, "rp-aaaaaaaaaaaaaaaa.json")
	if err := os.WriteFile(orphan, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	stray := filepath.Join(base, "notes.txt")
	if err := os.WriteFile(stray, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}

	s = openTestStore(t, base) // reopen prunes
	if _, _, err := s.Load(keep); err != nil {
		t.Fatalf("young report pruned: %v", err)
	}
	if _, _, err := s.Load(drop); err == nil {
		t.Fatal("expired report survived the prune")
	}
	if _, err := os.Stat(filepath.Join(base, drop+".json")); !os.IsNotExist(err) {
		t.Fatal("expired report's file survived the prune")
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatal("orphan report file survived the prune")
	}
	if _, err := os.Stat(stray); err != nil {
		t.Fatal("prune deleted a file that is not a report")
	}
}

func TestStore_SizeCapRefusesWhole(t *testing.T) {
	s := openTestStore(t, t.TempDir())
	big := Document{Title: "big", Blocks: []Block{{Type: BlockProse, Text: strings.Repeat("a", MaxStoredBytes)}}}
	if _, err := s.Put(big, Meta{}); err == nil || !strings.Contains(err.Error(), "trim the largest block") {
		t.Fatalf("Put = %v, want the size cap named", err)
	}
	if len(s.List()) != 0 {
		t.Fatal("an over-cap report was stored anyway")
	}
}

func TestStore_Permissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permissions")
	}
	base := filepath.Join(t.TempDir(), "reports")
	s := openTestStore(t, base)
	id, _ := s.Put(Document{Title: "t", Blocks: []Block{{Type: BlockProse, Text: "x"}}}, Meta{})

	if info, _ := os.Stat(base); info.Mode().Perm() != 0o700 {
		t.Fatalf("store dir mode = %v, want 0700", info.Mode().Perm())
	}
	for _, f := range []string{id + ".json", indexFile} {
		if info, _ := os.Stat(filepath.Join(base, f)); info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %v, want 0600", f, info.Mode().Perm())
		}
	}
}

func TestStore_CorruptIndexStartsEmpty(t *testing.T) {
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, indexFile), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := openTestStore(t, base)
	if len(s.List()) != 0 {
		t.Fatal("corrupt index produced entries")
	}
}

func TestStore_FrozenFreehandRoundTrips(t *testing.T) {
	frozen, err := ValidateFreehand(`<p>drawn <em>once</em></p>`)
	if err != nil {
		t.Fatal(err)
	}
	s := openTestStore(t, t.TempDir())
	id, _ := s.Put(Document{Title: "t", Blocks: []Block{{Type: BlockFreehand, HTML: frozen}}}, Meta{})
	doc, _, err := s.Load(id)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if doc.Blocks[0].HTML != frozen {
		t.Fatalf("frozen markup changed in the store:\nput  %s\ngot  %s", frozen, doc.Blocks[0].HTML)
	}
	// And the bytes on disk are JSON holding that exact markup.
	raw, _ := os.ReadFile(filepath.Join(s.Dir(), id+".json"))
	var back Document
	if err := json.Unmarshal(raw, &back); err != nil || back.Blocks[0].HTML != frozen {
		t.Fatalf("stored bytes do not hold the frozen markup: %v", err)
	}
}
