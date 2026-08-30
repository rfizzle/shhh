package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGlob_TopLevel(t *testing.T) {
	tmp := t.TempDir()
	must(t, os.WriteFile(filepath.Join(tmp, "main.go"), []byte("x"), 0o644))
	must(t, os.WriteFile(filepath.Join(tmp, "readme.md"), []byte("x"), 0o644))
	must(t, os.MkdirAll(filepath.Join(tmp, "sub"), 0o755))
	must(t, os.WriteFile(filepath.Join(tmp, "sub", "deep.go"), []byte("x"), 0o644))

	args, _ := json.Marshal(globArgs{Pattern: "*.go", Path: tmp})
	result, err := Execute("glob", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "main.go" {
		t.Errorf("*.go should match only top-level main.go, got: %q", result)
	}
}

func TestGlob_DoubleStar(t *testing.T) {
	tmp := t.TempDir()
	must(t, os.WriteFile(filepath.Join(tmp, "main.go"), []byte("x"), 0o644))
	must(t, os.MkdirAll(filepath.Join(tmp, "a", "b"), 0o755))
	must(t, os.WriteFile(filepath.Join(tmp, "a", "b", "deep.go"), []byte("x"), 0o644))
	must(t, os.WriteFile(filepath.Join(tmp, "a", "note.txt"), []byte("x"), 0o644))

	args, _ := json.Marshal(globArgs{Pattern: "**/*.go", Path: tmp})
	result, err := Execute("glob", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "main.go") {
		t.Errorf("**/*.go should match top-level main.go: %q", result)
	}
	if !strings.Contains(result, "a/b/deep.go") {
		t.Errorf("**/*.go should match nested deep.go: %q", result)
	}
	if strings.Contains(result, "note.txt") {
		t.Errorf("**/*.go should not match note.txt: %q", result)
	}
}

func TestGlob_MidPatternDoubleStar(t *testing.T) {
	tmp := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(tmp, "cmd", "shhh"), 0o755))
	must(t, os.WriteFile(filepath.Join(tmp, "cmd", "shhh", "main.go"), []byte("x"), 0o644))
	must(t, os.WriteFile(filepath.Join(tmp, "cmd", "other.go"), []byte("x"), 0o644))

	args, _ := json.Marshal(globArgs{Pattern: "cmd/**/main.go", Path: tmp})
	result, err := Execute("glob", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "cmd/shhh/main.go") {
		t.Errorf("expected cmd/shhh/main.go: %q", result)
	}
	if strings.Contains(result, "other.go") {
		t.Errorf("should not match other.go: %q", result)
	}
}

func TestGlob_SkipList(t *testing.T) {
	tmp := t.TempDir()
	for _, dir := range []string{".git", "node_modules", "vendor"} {
		must(t, os.MkdirAll(filepath.Join(tmp, dir), 0o755))
		must(t, os.WriteFile(filepath.Join(tmp, dir, "skipped.go"), []byte("x"), 0o644))
	}
	must(t, os.WriteFile(filepath.Join(tmp, "kept.go"), []byte("x"), 0o644))

	args, _ := json.Marshal(globArgs{Pattern: "**/*.go", Path: tmp})
	result, err := Execute("glob", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result, "skipped.go") {
		t.Errorf("skip-list directories should be excluded: %q", result)
	}
	if !strings.Contains(result, "kept.go") {
		t.Errorf("expected kept.go: %q", result)
	}
}

func TestGlob_MatchesFilesOnly(t *testing.T) {
	tmp := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(tmp, "somedir"), 0o755))
	must(t, os.WriteFile(filepath.Join(tmp, "somefile"), []byte("x"), 0o644))

	args, _ := json.Marshal(globArgs{Pattern: "*", Path: tmp})
	result, err := Execute("glob", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result, "somedir") {
		t.Errorf("glob should return files only: %q", result)
	}
	if !strings.Contains(result, "somefile") {
		t.Errorf("expected somefile: %q", result)
	}
}

func TestGlob_NoMatches(t *testing.T) {
	tmp := t.TempDir()
	must(t, os.WriteFile(filepath.Join(tmp, "main.go"), []byte("x"), 0o644))

	args, _ := json.Marshal(globArgs{Pattern: "*.rs", Path: tmp})
	result, err := Execute("glob", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "No files matched." {
		t.Errorf("expected 'No files matched.', got: %q", result)
	}
}

func TestGlob_MissingPattern(t *testing.T) {
	_, err := Execute("glob", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for missing pattern")
	}
}

func TestGlob_InvalidPattern(t *testing.T) {
	tmp := t.TempDir()
	args, _ := json.Marshal(globArgs{Pattern: "[", Path: tmp})
	_, err := Execute("glob", args)
	if err == nil {
		t.Fatal("expected error for invalid pattern")
	}
	if !strings.Contains(err.Error(), "invalid pattern") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestGlob_NonexistentPath(t *testing.T) {
	args, _ := json.Marshal(globArgs{Pattern: "*.go", Path: "/nonexistent/dir"})
	_, err := Execute("glob", args)
	if err == nil {
		t.Fatal("expected error for nonexistent path")
	}
}

func TestMatchGlob(t *testing.T) {
	cases := []struct {
		pattern, name string
		want          bool
	}{
		{"*.go", "main.go", true},
		{"*.go", "sub/main.go", false},
		{"**/*.go", "main.go", true},
		{"**/*.go", "a/b/c/main.go", true},
		{"a/**/z.go", "a/z.go", true},
		{"a/**/z.go", "a/b/z.go", true},
		{"a/**/z.go", "b/z.go", false},
		{"a/*/z.go", "a/b/z.go", true},
		{"a/*/z.go", "a/b/c/z.go", false},
		{"**", "anything/at/all.txt", true},
	}
	for _, c := range cases {
		got, err := matchGlob(strings.Split(c.pattern, "/"), strings.Split(c.name, "/"))
		if err != nil {
			t.Fatalf("matchGlob(%q, %q) error: %v", c.pattern, c.name, err)
		}
		if got != c.want {
			t.Errorf("matchGlob(%q, %q) = %v, want %v", c.pattern, c.name, got, c.want)
		}
	}
}
