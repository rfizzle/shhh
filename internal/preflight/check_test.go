package preflight

import (
	"testing"
)

func TestCheck_ValidCommand(t *testing.T) {
	result := Check("ls -la", "bash")
	if !result.OK {
		t.Errorf("expected OK for 'ls -la', got errors: %v", result.Errors)
	}
}

func TestCheck_UnknownBinary(t *testing.T) {
	result := Check("nonexistentcommand123 --foo", "bash")
	if result.OK {
		t.Error("expected failure for unknown binary")
	}
	found := false
	for _, e := range result.Errors {
		if e == "command not found: nonexistentcommand123" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'command not found' error, got: %v", result.Errors)
	}
}

func TestCheck_SyntaxError(t *testing.T) {
	result := Check("if true; then", "bash")
	if result.OK {
		t.Error("expected failure for incomplete if statement")
	}
}

func TestCheck_ShellBuiltin(t *testing.T) {
	result := Check("cd /tmp", "bash")
	if !result.OK {
		t.Errorf("expected OK for shell builtin, got errors: %v", result.Errors)
	}
}

func TestCheck_SudoPrefix(t *testing.T) {
	result := Check("sudo ls /root", "bash")
	if !result.OK {
		t.Errorf("expected OK for 'sudo ls', got errors: %v", result.Errors)
	}
}

func TestCheck_EnvVarPrefix(t *testing.T) {
	result := Check("FOO=bar ls", "bash")
	if !result.OK {
		t.Errorf("expected OK for env-prefixed command, got errors: %v", result.Errors)
	}
}

func TestCheck_MultiLine(t *testing.T) {
	result := Check("ls\nnonexistentcommand123", "bash")
	if result.OK {
		t.Error("expected failure when one command has unknown binary")
	}
}

func TestCheck_EmptyShellSkips(t *testing.T) {
	result := Check("nonexistentcommand123", "")
	if result.OK {
		t.Error("expected failure for unknown binary even with empty shell")
	}
}

func TestExtractBinary_EnvVars(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"ls -la", "ls"},
		{"FOO=bar ls", "ls"},
		{"A=1 B=2 grep foo", "grep"},
		{"sudo vim /etc/hosts", "vim"},
		{"FOO=bar sudo cat /etc/passwd", "cat"},
		{"", ""},
	}
	for _, tt := range tests {
		got := extractBinary(tt.input)
		if got != tt.expected {
			t.Errorf("extractBinary(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}
