package history

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppend_Bash(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	if err := Append("bash", "echo hello"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := readFile(t, filepath.Join(tmp, ".bash_history"))
	if content != "echo hello\n" {
		t.Errorf("expected 'echo hello\\n', got %q", content)
	}
}

func TestAppend_Zsh(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	if err := Append("zsh", "ls -la"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := readFile(t, filepath.Join(tmp, ".zsh_history"))
	if !strings.HasPrefix(content, ": ") || !strings.HasSuffix(content, ";ls -la\n") {
		t.Errorf("unexpected zsh history format: %q", content)
	}
}

func TestAppend_Fish(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	os.MkdirAll(filepath.Join(tmp, ".local", "share", "fish"), 0755)

	if err := Append("fish", "git status"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := readFile(t, filepath.Join(tmp, ".local", "share", "fish", "fish_history"))
	if !strings.Contains(content, "- cmd: git status\n") || !strings.Contains(content, "  when: ") {
		t.Errorf("unexpected fish history format: %q", content)
	}
}

func TestAppend_EmptyCommand(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	if err := Append("bash", "  "); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	path := filepath.Join(tmp, ".bash_history")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("expected no history file for empty command")
	}
}

func TestAppend_DeduplicatesBash(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	Append("bash", "echo hello")
	Append("bash", "echo hello")

	content := readFile(t, filepath.Join(tmp, ".bash_history"))
	if strings.Count(content, "echo hello") != 1 {
		t.Errorf("expected one entry, got: %q", content)
	}
}

func TestAppend_DeduplicatesZsh(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	Append("zsh", "ls -la")
	Append("zsh", "ls -la")

	content := readFile(t, filepath.Join(tmp, ".zsh_history"))
	if strings.Count(content, ";ls -la") != 1 {
		t.Errorf("expected one entry, got: %q", content)
	}
}

func TestAppend_DeduplicatesFish(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	os.MkdirAll(filepath.Join(tmp, ".local", "share", "fish"), 0755)

	Append("fish", "git status")
	Append("fish", "git status")

	content := readFile(t, filepath.Join(tmp, ".local", "share", "fish", "fish_history"))
	if strings.Count(content, "- cmd: git status") != 1 {
		t.Errorf("expected one entry, got: %q", content)
	}
}

func TestAppend_DifferentCommandsNotDeduplicated(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	Append("bash", "echo hello")
	Append("bash", "echo world")

	content := readFile(t, filepath.Join(tmp, ".bash_history"))
	if content != "echo hello\necho world\n" {
		t.Errorf("expected both entries, got: %q", content)
	}
}

func TestAppend_UnknownShellFallsToBash(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	if err := Append("csh", "pwd"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := readFile(t, filepath.Join(tmp, ".bash_history"))
	if content != "pwd\n" {
		t.Errorf("expected fallback to bash format, got %q", content)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read %s: %v", path, err)
	}
	return string(data)
}
