package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// touch writes a file and stamps it, so the ordering under test is the
// ordering the test asked for rather than the one the filesystem produced.
func touch(t *testing.T, dir, name string, age time.Duration) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-age)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
}

func TestRecentFiles_NewestFirst(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "old.go", 3*time.Hour)
	touch(t, dir, "internal/agent/loop.go", 2*time.Minute)
	touch(t, dir, "README.md", time.Hour)

	got := RecentFiles(dir, 10)
	want := []string{"internal/agent/loop.go", "README.md", "old.go"}
	if len(got) != len(want) {
		t.Fatalf("got %d files, want %d: %v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].Path != w {
			t.Fatalf("position %d is %q, want %q (all: %v)", i, got[i].Path, w, got)
		}
	}
}

func TestRecentFiles_SkipsTheDirectoriesTheSurveySkips(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "main.go", time.Minute)
	touch(t, dir, ".git/HEAD", time.Second)
	touch(t, dir, "node_modules/pkg/index.js", time.Second)
	touch(t, dir, "vendor/x/y.go", time.Second)

	for _, f := range RecentFiles(dir, 10) {
		if strings.HasPrefix(f.Path, ".git/") || strings.Contains(f.Path, "node_modules") ||
			strings.HasPrefix(f.Path, "vendor/") {
			t.Fatalf("the walk should skip %q", f.Path)
		}
	}
}

func TestRecentFiles_HonoursTheLimit(t *testing.T) {
	dir := t.TempDir()
	for i := range 5 {
		touch(t, dir, string(rune('a'+i))+".go", time.Duration(i)*time.Minute)
	}
	if got := RecentFiles(dir, 2); len(got) != 2 {
		t.Fatalf("expected the limit to bound the list, got %d", len(got))
	}
	if got := RecentFiles(dir, 0); got != nil {
		t.Fatalf("a zero limit asks for nothing, got %v", got)
	}
}

func TestRecentFiles_MissingDirectoryReportsNothing(t *testing.T) {
	if got := RecentFiles(filepath.Join(t.TempDir(), "gone"), 5); len(got) != 0 {
		t.Fatalf("an unreadable root should report nothing, got %v", got)
	}
}
