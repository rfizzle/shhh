package prompt

import (
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/shell"
)

func TestBuild_ContainsShell(t *testing.T) {
	info := shell.Info{Shell: "fish", OS: "darwin", Cwd: "/home/user"}
	got := Build(info)

	if !strings.Contains(got, "Shell: fish") {
		t.Errorf("expected prompt to contain shell name, got:\n%s", got)
	}
}

func TestBuild_ContainsOS(t *testing.T) {
	info := shell.Info{Shell: "zsh", OS: "darwin", Cwd: "/tmp"}
	got := Build(info)

	if !strings.Contains(got, "OS: macOS") {
		t.Errorf("expected 'OS: macOS' for darwin, got:\n%s", got)
	}
}

func TestBuild_LinuxOS(t *testing.T) {
	info := shell.Info{Shell: "bash", OS: "linux", Cwd: "/tmp"}
	got := Build(info)

	if !strings.Contains(got, "OS: Linux") {
		t.Errorf("expected 'OS: Linux' for linux, got:\n%s", got)
	}
}

func TestBuild_ContainsCwd(t *testing.T) {
	info := shell.Info{Shell: "zsh", OS: "darwin", Cwd: "/Users/me/projects"}
	got := Build(info)

	if !strings.Contains(got, "Cwd: /Users/me/projects") {
		t.Errorf("expected prompt to contain cwd, got:\n%s", got)
	}
}

func TestBuild_InstructsCommandOnly(t *testing.T) {
	info := shell.Info{Shell: "zsh", OS: "darwin", Cwd: "/tmp"}
	got := Build(info)

	if !strings.Contains(got, "ONLY the command") {
		t.Errorf("expected prompt to instruct command-only output, got:\n%s", got)
	}
}

func TestBuild_NoMarkdown(t *testing.T) {
	info := shell.Info{Shell: "zsh", OS: "darwin", Cwd: "/tmp"}
	got := Build(info)

	if !strings.Contains(got, "no markdown") {
		t.Errorf("expected prompt to forbid markdown, got:\n%s", got)
	}
}

func TestBuild_Terse(t *testing.T) {
	info := shell.Info{Shell: "zsh", OS: "darwin", Cwd: "/tmp"}
	got := Build(info)

	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) > 10 {
		t.Errorf("prompt should be terse, got %d lines:\n%s", len(lines), got)
	}
}

func TestBuild_UnknownOS(t *testing.T) {
	info := shell.Info{Shell: "sh", OS: "freebsd", Cwd: "/tmp"}
	got := Build(info)

	if !strings.Contains(got, "OS: freebsd") {
		t.Errorf("expected unknown OS to pass through, got:\n%s", got)
	}
}

func TestFriendlyOS(t *testing.T) {
	tests := []struct {
		goos string
		want string
	}{
		{"darwin", "macOS"},
		{"linux", "Linux"},
		{"windows", "Windows"},
		{"freebsd", "freebsd"},
	}
	for _, tt := range tests {
		if got := friendlyOS(tt.goos); got != tt.want {
			t.Errorf("friendlyOS(%q) = %q, want %q", tt.goos, got, tt.want)
		}
	}
}
