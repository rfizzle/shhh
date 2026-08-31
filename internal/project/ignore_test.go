package project

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func paths(files []RecentFile) map[string]bool {
	out := map[string]bool{}
	for _, f := range files {
		out[f.Path] = true
	}
	return out
}

func TestRecentFiles_HonoursGitignore(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		".gitignore":     "*.log\ndist/\n/secret.txt\n!keep.log\n",
		"main.go":        "package main",
		"trace.log":      "x",
		"keep.log":       "x",
		"secret.txt":     "x",
		"sub/secret.txt": "x",
		"sub/deep.log":   "x",
		"dist/out.bin":   "x",
	})

	got := paths(RecentFiles(root, 50))
	for _, want := range []string{"main.go", "keep.log", "sub/secret.txt", ".gitignore"} {
		if !got[want] {
			t.Errorf("expected %s in the walk, got %v", want, got)
		}
	}
	for _, banned := range []string{"trace.log", "secret.txt", "sub/deep.log", "dist/out.bin"} {
		if got[banned] {
			t.Errorf("%s is gitignored and must not be offered, got %v", banned, got)
		}
	}
}

func TestRecentFiles_NestedGitignoreScopesToItsDirectory(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"sub/.gitignore": "gen.go\n",
		"sub/gen.go":     "x",
		"gen.go":         "x",
	})

	got := paths(RecentFiles(root, 50))
	if !got["gen.go"] {
		t.Errorf("the root's gen.go is not ignored, got %v", got)
	}
	if got["sub/gen.go"] {
		t.Errorf("sub/.gitignore should hide sub/gen.go, got %v", got)
	}
}

func TestRecentFiles_DoubleStarMatchesAcrossSegments(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		".gitignore":       "docs/**/draft.md\n",
		"docs/a/draft.md":  "x",
		"docs/draft.md":    "x",
		"docs/a/final.md":  "x",
		"other/draft.md":   "x",
		"docs/b/c/keep.md": "x",
	})

	got := paths(RecentFiles(root, 50))
	if got["docs/a/draft.md"] || got["docs/draft.md"] {
		t.Errorf("** should swallow any run of directories, got %v", got)
	}
	if !got["docs/a/final.md"] || !got["other/draft.md"] || !got["docs/b/c/keep.md"] {
		t.Errorf("only the pattern's own matches go, got %v", got)
	}
}

func TestRecentFilesIn_WalksAddedRootsRelativeToTheFirst(t *testing.T) {
	base := t.TempDir()
	other := t.TempDir()
	writeTree(t, base, map[string]string{"here.go": "x"})
	writeTree(t, other, map[string]string{"there.go": "x", ".gitignore": "*.log\n", "skip.log": "x"})

	got := paths(RecentFilesIn([]string{base, other}, 50))
	if !got["here.go"] {
		t.Errorf("expected the base root's file, got %v", got)
	}
	rel, err := filepath.Rel(base, filepath.Join(other, "there.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !got[filepath.ToSlash(rel)] {
		t.Errorf("expected the added root's file as %s, got %v", filepath.ToSlash(rel), got)
	}
	for p := range got {
		if filepath.Base(p) == "skip.log" {
			t.Errorf("an added root's .gitignore holds there too, got %v", got)
		}
	}
}
