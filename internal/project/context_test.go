package project

import (
	"os"
	"path/filepath"
	"testing"
)

func writeContext(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".shhh"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".shhh", "project.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFindFrom_InCurrentDir(t *testing.T) {
	dir := t.TempDir()
	content := "This project uses Docker Compose for services."
	writeContext(t, dir, content)

	_, got := FindFrom(dir)
	if got != content {
		t.Errorf("FindFrom() = %q, want %q", got, content)
	}
}

func TestFindFrom_InParentDir(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "src", "pkg")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	content := "Use make test for tests."
	writeContext(t, root, content)

	_, got := FindFrom(sub)
	if got != content {
		t.Errorf("FindFrom() = %q, want %q", got, content)
	}
}

func TestFindFrom_NotPresent(t *testing.T) {
	dir := t.TempDir()

	_, got := FindFrom(dir)
	if got != "" {
		t.Errorf("FindFrom() = %q, want empty string", got)
	}
}

// A caller with no directory to name gets nothing, rather than the process's
// own directory dressed up as an answer to a question about somewhere stated.
func TestFindFrom_NoDirectoryReadsNothing(t *testing.T) {
	if p, c := FindFrom(""); p != "" || c != "" {
		t.Errorf("FindFrom(\"\") = %q, %q, want nothing", p, c)
	}
	if got := FindContextFrom(""); got != "" {
		t.Errorf("FindContextFrom(\"\") = %q, want nothing", got)
	}
}

func TestFindFrom_AgentsMd(t *testing.T) {
	dir := t.TempDir()
	content := "# Agent notes\nRun make ci before committing."
	must(t, os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(content), 0o644))

	_, got := FindFrom(dir)
	if got != content {
		t.Errorf("FindFrom() = %q, want %q", got, content)
	}
}

func TestFindFrom_ShhhBeatsAgentsMd(t *testing.T) {
	dir := t.TempDir()
	writeContext(t, dir, "shhh context")
	must(t, os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("agents context"), 0o644))

	_, got := FindFrom(dir)
	if got != "shhh context" {
		t.Errorf("FindFrom() = %q, want %q", got, "shhh context")
	}
}

func TestFindFrom_AgentsMdInParentDir(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "src")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	content := "Monorepo: services live under src/."
	must(t, os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte(content), 0o644))

	_, got := FindFrom(sub)
	if got != content {
		t.Errorf("FindFrom() = %q, want %q", got, content)
	}
}

func TestFindFrom_NearestWins(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "child")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	writeContext(t, root, "root context")
	writeContext(t, sub, "child context")

	_, got := FindFrom(sub)
	if got != "child context" {
		t.Errorf("FindFrom() = %q, want %q", got, "child context")
	}
}

func TestFindFrom_OldSingleFileIsNotRead(t *testing.T) {
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, ".shhh"), []byte("old layout"), 0o644))

	if _, got := FindFrom(dir); got != "" {
		t.Errorf("FindFrom() = %q, want empty: the old file is the doctor's to move", got)
	}
}

// must fails the test on an error from setting it up.
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// Without a repository the root is the nearest directory that already holds
// a shhh directory, so two terminals opened at different depths of one
// project key their backlog and their refused offers on one directory.
func TestRoot_WithoutARepositoryFindsTheStateDirectory(t *testing.T) {
	base := t.TempDir()
	proj := filepath.Join(base, "proj")
	sub := filepath.Join(proj, "a", "b")
	mustMkdir(t, filepath.Join(proj, StateDir))
	mustMkdir(t, sub)

	if got := Root(sub); got != proj {
		t.Errorf("Root(%s) = %s, want %s", sub, got, proj)
	}
	if got := Root(proj); got != proj {
		t.Errorf("Root at the state directory itself = %s, want %s", got, proj)
	}
	if InRepo(sub) {
		t.Error("a directory with no .git anywhere above it is not in a repository")
	}
}

// A repository still wins: the state directory is the answer only when the
// walk finds no .git at all, so nothing about an existing checkout moves.
func TestRoot_ARepositoryBeatsAStateDirectory(t *testing.T) {
	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	sub := filepath.Join(repo, "pkg")
	mustMkdir(t, filepath.Join(repo, ".git"))
	mustMkdir(t, filepath.Join(sub, StateDir))

	if got := Root(sub); got != repo {
		t.Errorf("Root(%s) = %s, want the repository %s", sub, got, repo)
	}
	if !InRepo(sub) {
		t.Error("a directory under a .git is in a repository")
	}
}

// The old layout's single .shhh file is not a root: a checkout still holding
// one has a doctor migration waiting, and reading it as a root would key the
// project on it for as long as the migration is unmade.
func TestRoot_AStateFileIsNotARoot(t *testing.T) {
	base := t.TempDir()
	proj := filepath.Join(base, "proj")
	sub := filepath.Join(proj, "a")
	mustMkdir(t, sub)
	if err := os.WriteFile(filepath.Join(proj, StateDir), []byte("notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Root(sub); got != sub {
		t.Errorf("Root(%s) = %s, want the directory itself", sub, got)
	}
}

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}
