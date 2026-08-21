package evidence

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func openTestStore(t *testing.T, base, session string) *Store {
	t.Helper()
	s, err := Open(base, session)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

func TestStore_PutInfoRead(t *testing.T) {
	s := openTestStore(t, t.TempDir(), "sess-a")
	id, err := s.Put("read_file", []byte("hello evidence world"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if !idRe.MatchString(id) {
		t.Fatalf("id %q does not match the opaque id shape", id)
	}

	meta, err := s.Info(id)
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if meta.Tool != "read_file" || meta.Size != 20 || meta.Stored != 20 || meta.Truncated {
		t.Fatalf("unexpected meta: %+v", meta)
	}

	data, _, err := s.Read(id, 0, 100)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(data) != "hello evidence world" {
		t.Fatalf("Read = %q", data)
	}
}

func TestStore_ReadPaging(t *testing.T) {
	s := openTestStore(t, t.TempDir(), "sess-a")
	id, _ := s.Put("exec", []byte("0123456789"))

	data, _, err := s.Read(id, 4, 3)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(data) != "456" {
		t.Fatalf("paged read = %q, want 456", data)
	}

	// Clamped past the end and at negative offsets.
	if data, _, _ := s.Read(id, 8, 100); string(data) != "89" {
		t.Fatalf("end-clamped read = %q", data)
	}
	if data, _, _ := s.Read(id, -5, 2); string(data) != "01" {
		t.Fatalf("negative-offset read = %q", data)
	}
	if data, _, _ := s.Read(id, 100, 10); len(data) != 0 {
		t.Fatalf("past-end read = %q", data)
	}
	if _, _, err := s.Read(id, 0, 0); err == nil {
		t.Fatal("non-positive limit must be rejected")
	}
}

func TestStore_Search(t *testing.T) {
	s := openTestStore(t, t.TempDir(), "sess-a")
	id, _ := s.Put("exec", []byte("alpha\nFAIL: beta\ngamma\nanother fail here\n"))

	matches, total, err := s.Search(id, "fail", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if total != 2 || len(matches) != 2 {
		t.Fatalf("expected 2 case-insensitive matches, got %d/%d", len(matches), total)
	}
	if matches[0].Line != 2 || matches[0].Text != "FAIL: beta" {
		t.Fatalf("unexpected first match: %+v", matches[0])
	}

	// The match cap bounds what is returned but not the total count.
	matches, total, _ = s.Search(id, "a", 1)
	if len(matches) != 1 || total < 3 {
		t.Fatalf("cap should limit matches (%d) but count the total (%d)", len(matches), total)
	}
}

func TestStore_RejectsInvalidAndUnknownIDs(t *testing.T) {
	s := openTestStore(t, t.TempDir(), "sess-a")
	for _, id := range []string{"", "ev-XYZ", "../../etc/passwd", "ev-0123456789abcdef0", "index"} {
		if _, err := s.Info(id); err == nil {
			t.Fatalf("id %q must be rejected", id)
		}
	}
	// Well-formed but unknown.
	if _, err := s.Info("ev-0123456789abcdef"); err == nil {
		t.Fatal("unknown id must be rejected")
	}
}

func TestStore_PerSessionScoping(t *testing.T) {
	base := t.TempDir()
	a := openTestStore(t, base, "sess-a")
	b := openTestStore(t, base, "sess-b")
	id, _ := a.Put("exec", []byte("secret from session a"))
	if _, err := b.Info(id); err == nil {
		t.Fatal("an id from another session must not resolve")
	}
	if _, _, err := b.Read(id, 0, 100); err == nil {
		t.Fatal("another session's evidence must not be readable")
	}
}

func TestStore_Permissions(t *testing.T) {
	base := t.TempDir()
	s := openTestStore(t, base, "sess-a")
	id, _ := s.Put("exec", []byte("data"))

	dirInfo, err := os.Stat(filepath.Join(base, "sess-a"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Fatalf("session dir permissions = %o, want 0700", perm)
	}
	fileInfo, err := os.Stat(filepath.Join(base, "sess-a", id+".dat"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := fileInfo.Mode().Perm(); perm != 0o600 {
		t.Fatalf("evidence file permissions = %o, want 0600", perm)
	}
}

func TestStore_StatsAndPurge(t *testing.T) {
	s := openTestStore(t, t.TempDir(), "sess-a")
	id1, _ := s.Put("exec", []byte(strings.Repeat("a", 100)))
	_, _ = s.Put("exec", []byte(strings.Repeat("b", 50)))

	st := s.Stats()
	if st.Entries != 2 || st.StoredBytes != 150 {
		t.Fatalf("Stats = %+v", st)
	}

	if err := s.Purge(); err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if st := s.Stats(); st.Entries != 0 || st.StoredBytes != 0 {
		t.Fatalf("Stats after purge = %+v", st)
	}
	if _, err := s.Info(id1); err == nil {
		t.Fatal("purged entries must be gone")
	}
}

func TestStore_TruncatesOversizedOriginals(t *testing.T) {
	s := openTestStore(t, t.TempDir(), "sess-a")
	id, err := s.Put("exec", make([]byte, MaxStoredBytes+10))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	meta, _ := s.Info(id)
	if !meta.Truncated || meta.Stored != MaxStoredBytes || meta.Size != MaxStoredBytes+10 {
		t.Fatalf("oversized meta = %+v", meta)
	}
}

func TestStore_PrunesOldSessions(t *testing.T) {
	base := t.TempDir()
	old := openTestStore(t, base, "sess-old")
	_, _ = old.Put("exec", []byte("stale"))
	oldDir := filepath.Join(base, "sess-old")
	stale := time.Now().Add(-RetentionAge - time.Hour)
	if err := os.Chtimes(oldDir, stale, stale); err != nil {
		t.Fatal(err)
	}

	openTestStore(t, base, "sess-new")
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Fatalf("session past retention should have been pruned, stat err = %v", err)
	}
}

func TestStore_ReloadsIndex(t *testing.T) {
	base := t.TempDir()
	s := openTestStore(t, base, "sess-a")
	id, _ := s.Put("read_file", []byte("persisted"))

	reopened := openTestStore(t, base, "sess-a")
	data, meta, err := reopened.Read(id, 0, 100)
	if err != nil {
		t.Fatalf("Read after reopen: %v", err)
	}
	if string(data) != "persisted" || meta.Tool != "read_file" {
		t.Fatalf("reopened entry = %q / %+v", data, meta)
	}
}
