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
	if len(lines) > 20 {
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

func TestBuild_WithExtra(t *testing.T) {
	info := shell.Info{Shell: "zsh", OS: "darwin", Cwd: "/tmp"}
	got := Build(info, "Always use ripgrep instead of grep")

	if !strings.Contains(got, "Always use ripgrep instead of grep") {
		t.Error("expected extra prompt to be appended")
	}
	if !strings.Contains(got, "Shell: zsh") {
		t.Error("expected base prompt to still be present")
	}
}

func TestBuild_WithEmptyExtra(t *testing.T) {
	info := shell.Info{Shell: "zsh", OS: "darwin", Cwd: "/tmp"}
	withEmpty := Build(info, "")
	without := Build(info)

	if withEmpty != without {
		t.Error("empty extra should produce same result as no extra")
	}
}

func TestBuildChat_WithExtra(t *testing.T) {
	info := shell.Info{Shell: "bash", OS: "linux", Cwd: "/home/user"}
	got := BuildChat(info, "Prefer explaining with examples")

	if !strings.Contains(got, "Prefer explaining with examples") {
		t.Error("expected extra prompt to be appended to chat prompt")
	}
	if !strings.Contains(got, "technical assistant") {
		t.Error("expected base chat prompt to still be present")
	}
}

func TestBuildAgent_AgentInstructions(t *testing.T) {
	info := shell.Info{Shell: "bash", OS: "linux", Cwd: "/home/user/project"}
	got := BuildAgent(info)

	for _, want := range []string{
		"coding agent",
		"use them proactively",
		"Read a file before editing",
		"verify your changes",
		"Keep going",
		"write_file and edit_file rather than pasting code blocks",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected agent prompt to contain %q, got:\n%s", want, got)
		}
	}
}

func TestBuildAgent_ContainsEnvironment(t *testing.T) {
	info := shell.Info{Shell: "zsh", OS: "darwin", Cwd: "/Users/me/proj"}
	got := BuildAgent(info)

	if !strings.Contains(got, "Shell: zsh") {
		t.Errorf("expected agent prompt to contain shell name, got:\n%s", got)
	}
	if !strings.Contains(got, "OS: macOS") {
		t.Errorf("expected agent prompt to contain OS, got:\n%s", got)
	}
	if !strings.Contains(got, "Cwd: /Users/me/proj") {
		t.Errorf("expected agent prompt to contain cwd, got:\n%s", got)
	}
}

func TestBuildAgent_WithExtra(t *testing.T) {
	info := shell.Info{Shell: "bash", OS: "linux", Cwd: "/home/user"}
	got := BuildAgent(info, "This repo uses make test.")

	if !strings.Contains(got, "This repo uses make test.") {
		t.Error("expected extra prompt to be appended to agent prompt")
	}

	if withEmpty := BuildAgent(info, ""); withEmpty != BuildAgent(info) {
		t.Error("empty extra should produce same result as no extra")
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
