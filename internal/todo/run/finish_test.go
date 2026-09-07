package run

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/todo"
)

// gitRepo is a repository with one commit already in it, so the index this
// tests against is the one a run would find.
func gitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on the path")
	}
	root := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "T"},
		{"commit", "--allow-empty", "-q", "-m", "root"},
	} {
		if out, code := git(root, args...); code != 0 {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	return root
}

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The commit is the run package's, so both drivers make exactly the same
// one: the run's paths staged by name, the message written as it stands, and
// nothing else in the tree carried along.
func TestCommit_StagesTheRunsPathsAndNothingElse(t *testing.T) {
	root := gitRepo(t)
	write(t, root, "a.go", "package a\n")
	write(t, root, "b.go", "package b\n")
	write(t, root, "stranger.go", "package stranger\n")

	files, err := Commit(root, []string{"a.go", "b.go"}, "feat(a): do the thing\n\nBecause.", "ask for it without one", true)
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if strings.Join(files, ",") != "a.go,b.go" {
		t.Fatalf("committed %v", files)
	}
	out, code := git(root, "show", "--name-only", "--format=%s%n%n%b", "HEAD")
	if code != 0 {
		t.Fatalf("git show: %s", out)
	}
	if !strings.Contains(out, "feat(a): do the thing") || !strings.Contains(out, "Because.") {
		t.Errorf("the message was not written as it stands:\n%s", out)
	}
	if !strings.Contains(out, "a.go") || !strings.Contains(out, "b.go") {
		t.Errorf("the run's paths are not in the commit:\n%s", out)
	}
	if strings.Contains(out, "stranger.go") {
		t.Errorf("a file the run did not change rode along:\n%s", out)
	}
}

// A commit that would carry a stranger is refused instead: one that cannot
// be reverted, cited or read as a unit is worse than none.
func TestCommit_RefusesAnIndexItDidNotFill(t *testing.T) {
	root := gitRepo(t)
	write(t, root, "a.go", "package a\n")
	write(t, root, "theirs.go", "package theirs\n")
	if out, code := git(root, "add", "--", "theirs.go"); code != 0 {
		t.Fatalf("git add: %s", out)
	}
	_, err := Commit(root, []string{"a.go"}, "subject", "ask for it without one", true)
	if err == nil || !strings.Contains(err.Error(), "already holds staged changes") {
		t.Fatalf("err = %v", err)
	}
}

// Outside a repository the refusal says so and offers the way through,
// which is the archive finish under whatever name the surface gives it.
func TestCommit_OutsideARepositorySaysSoAndOffersTheWayThrough(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.go", "package a\n")
	_, err := Commit(root, []string{"a.go"}, "subject", "--no-commit runs it without one", true)
	if err == nil || !strings.Contains(err.Error(), "not a git repository") ||
		!strings.Contains(err.Error(), "--no-commit runs it without one") {
		t.Fatalf("err = %v", err)
	}
}

// A run that changed nothing has nothing to commit, and says that rather
// than making an empty one.
func TestCommit_RefusesAnEmptyRun(t *testing.T) {
	if _, err := Commit(t.TempDir(), nil, "subject", "ask", true); err == nil ||
		!strings.Contains(err.Error(), "changed no files") {
		t.Fatalf("err = %v", err)
	}
}

func TestSourcesSection_TheReadAndTheOnlyCited(t *testing.T) {
	block := SourcesSection([]Source{
		{URL: "https://go.dev/doc/go1.24", Title: "Go 1.24 release notes", Read: true},
		{URL: "https://docs.rs/tokio/", Read: true},
		{URL: "https://example.com/invented", Title: "Nobody opened this"},
	})
	for _, want := range []string{
		"## Sources",
		"- https://go.dev/doc/go1.24 — Go 1.24 release notes",
		"- https://docs.rs/tokio/\n",
		"Cited, not read:",
		"- https://example.com/invented — Nobody opened this",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("the block is missing %q:\n%s", want, block)
		}
	}
	if strings.Index(block, "Cited, not read:") < strings.Index(block, "https://docs.rs/tokio/") {
		t.Error("what was cited and never read is listed above what was read")
	}
	if SourcesSection(nil) != "" {
		t.Error("a run that read nothing still wrote a sources block")
	}
}

// The block is built from what the session fetched and never from the
// write-up's own prose, so it is there whether or not the turn wrote one.
func TestFileNote_TheWriteUpCarriesTheSourcesItWasHanded(t *testing.T) {
	root := t.TempDir()
	write(t, root, ".shhh/todo/q-1.md", "---\nstatus: doing\n---\n\n# A question\n")
	it := todo.Item{Path: filepath.Join(root, ".shhh/todo/q-1.md"), Slug: "q-1", Title: "A question"}
	s := &State{Slug: "q-1", Report: "## Report\nThe answer is yes.", NoCommit: true,
		Sources: []Source{{URL: "https://go.dev/doc", Title: "The docs", Read: true}}}

	var noted string
	if _, err := FileNote(root, s, it, func(author, title, body string) (string, error) {
		noted = body
		return "n1", nil
	}); err != nil {
		t.Fatalf("FileNote: %v", err)
	}
	if !strings.Contains(noted, "## Sources") || !strings.Contains(noted, "https://go.dev/doc") {
		t.Errorf("the note carries no sources block:\n%s", noted)
	}
	archived, err := os.ReadFile(filepath.Join(root, ".shhh/todo/done/q-1.md"))
	if err != nil {
		t.Fatalf("read the archived item: %v", err)
	}
	if !strings.Contains(string(archived), "https://go.dev/doc") {
		t.Errorf("the archived item carries no sources block:\n%s", archived)
	}
}
