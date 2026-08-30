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

func TestFindContext_InCurrentDir(t *testing.T) {
	dir := t.TempDir()
	content := "This project uses Docker Compose for services."
	writeContext(t, dir, content)

	chdir(t, dir)

	got := FindContext()
	if got != content {
		t.Errorf("FindContext() = %q, want %q", got, content)
	}
}

func TestFindContext_InParentDir(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "src", "pkg")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	content := "Use make test for tests."
	writeContext(t, root, content)

	chdir(t, sub)

	got := FindContext()
	if got != content {
		t.Errorf("FindContext() = %q, want %q", got, content)
	}
}

func TestFindContext_NotPresent(t *testing.T) {
	dir := t.TempDir()

	chdir(t, dir)

	got := FindContext()
	if got != "" {
		t.Errorf("FindContext() = %q, want empty string", got)
	}
}

func TestFindContext_AgentsMd(t *testing.T) {
	dir := t.TempDir()
	content := "# Agent notes\nRun make ci before committing."
	must(t, os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(content), 0o644))

	chdir(t, dir)

	got := FindContext()
	if got != content {
		t.Errorf("FindContext() = %q, want %q", got, content)
	}
}

func TestFindContext_ShhhBeatsAgentsMd(t *testing.T) {
	dir := t.TempDir()
	writeContext(t, dir, "shhh context")
	must(t, os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("agents context"), 0o644))

	chdir(t, dir)

	got := FindContext()
	if got != "shhh context" {
		t.Errorf("FindContext() = %q, want %q", got, "shhh context")
	}
}

func TestFindContext_AgentsMdInParentDir(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "src")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	content := "Monorepo: services live under src/."
	must(t, os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte(content), 0o644))

	chdir(t, sub)

	got := FindContext()
	if got != content {
		t.Errorf("FindContext() = %q, want %q", got, content)
	}
}

func TestFindContext_NearestWins(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "child")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	writeContext(t, root, "root context")
	writeContext(t, sub, "child context")

	chdir(t, sub)

	got := FindContext()
	if got != "child context" {
		t.Errorf("FindContext() = %q, want %q", got, "child context")
	}
}

func TestFindContext_OldSingleFileIsNotRead(t *testing.T) {
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, ".shhh"), []byte("old layout"), 0o644))

	chdir(t, dir)

	if got := FindContext(); got != "" {
		t.Errorf("FindContext() = %q, want empty: the old file is the doctor's to move", got)
	}
}

// must fails the test on an error from setting it up.
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
