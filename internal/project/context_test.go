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
