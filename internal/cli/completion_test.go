package cli

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/spf13/cobra"
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

// The backlog's verbs are the command tree's own, so completion knows them
// the moment they are registered rather than from a second list that would
// drift. What is asked here is what the generated scripts ask the binary.
func TestCompletion_TodoVerbs(t *testing.T) {
	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{cobra.ShellCompRequestCmd, "todo", ""})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, verb := range []string{"ready", "next", "block", "open", "done", "drop", "sprint", "run", "show"} {
		if !strings.Contains(out.String(), verb+"\t") {
			t.Errorf("completion does not offer %q:\n%s", verb, out.String())
		}
	}
}
