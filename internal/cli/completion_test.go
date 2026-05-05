package cli

import (
	"bytes"
	"testing"
)

func TestCompletion_Bash(t *testing.T) {
	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"completion", "bash"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if out.Len() == 0 {
		t.Error("expected bash completion output, got empty")
	}
}

func TestCompletion_Zsh(t *testing.T) {
	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"completion", "zsh"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if out.Len() == 0 {
		t.Error("expected zsh completion output, got empty")
	}
}

func TestCompletion_Fish(t *testing.T) {
	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"completion", "fish"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if out.Len() == 0 {
		t.Error("expected fish completion output, got empty")
	}
}

func TestCompletion_Unsupported(t *testing.T) {
	cmd := NewRootCmd()
	cmd.SetArgs([]string{"completion", "powershell"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for unsupported shell")
	}
}

func TestCompletion_NoArgs(t *testing.T) {
	cmd := NewRootCmd()
	cmd.SetArgs([]string{"completion"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when no shell argument provided")
	}
}
