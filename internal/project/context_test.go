package project

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindContext_InCurrentDir(t *testing.T) {
	dir := t.TempDir()
	content := "This project uses Docker Compose for services."
	os.WriteFile(filepath.Join(dir, ".shhh"), []byte(content), 0o644)

	orig, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(orig)

	got := FindContext()
	if got != content {
		t.Errorf("FindContext() = %q, want %q", got, content)
	}
}

func TestFindContext_InParentDir(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "src", "pkg")
	os.MkdirAll(sub, 0o755)

	content := "Use make test for tests."
	os.WriteFile(filepath.Join(root, ".shhh"), []byte(content), 0o644)

	orig, _ := os.Getwd()
	os.Chdir(sub)
	defer os.Chdir(orig)

	got := FindContext()
	if got != content {
		t.Errorf("FindContext() = %q, want %q", got, content)
	}
}

func TestFindContext_NotPresent(t *testing.T) {
	dir := t.TempDir()

	orig, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(orig)

	got := FindContext()
	if got != "" {
		t.Errorf("FindContext() = %q, want empty string", got)
	}
}

func TestFindContext_AgentsMd(t *testing.T) {
	dir := t.TempDir()
	content := "# Agent notes\nRun make ci before committing."
	os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(content), 0o644)

	orig, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(orig)

	got := FindContext()
	if got != content {
		t.Errorf("FindContext() = %q, want %q", got, content)
	}
}

func TestFindContext_ShhhBeatsAgentsMd(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".shhh"), []byte("shhh context"), 0o644)
	os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("agents context"), 0o644)

	orig, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(orig)

	got := FindContext()
	if got != "shhh context" {
		t.Errorf("FindContext() = %q, want %q", got, "shhh context")
	}
}

func TestFindContext_AgentsMdInParentDir(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "src")
	os.MkdirAll(sub, 0o755)

	content := "Monorepo: services live under src/."
	os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte(content), 0o644)

	orig, _ := os.Getwd()
	os.Chdir(sub)
	defer os.Chdir(orig)

	got := FindContext()
	if got != content {
		t.Errorf("FindContext() = %q, want %q", got, content)
	}
}

func TestFindContext_NearestWins(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "child")
	os.MkdirAll(sub, 0o755)

	os.WriteFile(filepath.Join(root, ".shhh"), []byte("root context"), 0o644)
	os.WriteFile(filepath.Join(sub, ".shhh"), []byte("child context"), 0o644)

	orig, _ := os.Getwd()
	os.Chdir(sub)
	defer os.Chdir(orig)

	got := FindContext()
	if got != "child context" {
		t.Errorf("FindContext() = %q, want %q", got, "child context")
	}
}
