package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestInitZsh_OutputsSnippet(t *testing.T) {
	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"init", "zsh"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := out.String()
	if !strings.Contains(output, "bindkey '^K' _shhh_raw") {
		t.Error("expected zsh snippet to contain bindkey for Ctrl+K")
	}
	if !strings.Contains(output, "shhh --raw") {
		t.Error("expected zsh snippet to call shhh --raw")
	}
	if !strings.Contains(output, "zle -N _shhh_raw") {
		t.Error("expected zsh snippet to register ZLE widget")
	}
	if !strings.Contains(output, "BUFFER=") {
		t.Error("expected zsh snippet to set BUFFER")
	}
}

func TestInitZsh_Idempotent(t *testing.T) {
	cmd1 := NewRootCmd()
	cmd2 := NewRootCmd()
	var out1, out2 bytes.Buffer
	cmd1.SetOut(&out1)
	cmd2.SetOut(&out2)
	cmd1.SetArgs([]string{"init", "zsh"})
	cmd2.SetArgs([]string{"init", "zsh"})

	cmd1.Execute()
	cmd2.Execute()

	if out1.String() != out2.String() {
		t.Error("expected identical output on repeated calls (idempotent)")
	}
}

func TestInit_UnsupportedShell(t *testing.T) {
	cmd := NewRootCmd()
	cmd.SetArgs([]string{"init", "powershell"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for unsupported shell")
	}
}

func TestInit_NoArgs(t *testing.T) {
	cmd := NewRootCmd()
	cmd.SetArgs([]string{"init"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when no shell argument provided")
	}
}
