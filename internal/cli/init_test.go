package cli

import (
	"bytes"
	"os"
	"path/filepath"
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
	if !strings.Contains(output, "shhh cmd --raw") {
		t.Error("expected zsh snippet to call shhh cmd --raw")
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

	if err := cmd1.Execute(); err != nil {
		t.Fatal(err)
	}
	if err := cmd2.Execute(); err != nil {
		t.Fatal(err)
	}

	if out1.String() != out2.String() {
		t.Error("expected identical output on repeated calls (idempotent)")
	}
}

func TestInitBash_OutputsSnippet(t *testing.T) {
	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"init", "bash"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := out.String()
	if !strings.Contains(output, `bind -x '"\C-k": _shhh_raw'`) {
		t.Error("expected bash snippet to contain bind -x for Ctrl+K")
	}
	if !strings.Contains(output, "shhh cmd --raw") {
		t.Error("expected bash snippet to call shhh cmd --raw")
	}
	if !strings.Contains(output, "READLINE_LINE") {
		t.Error("expected bash snippet to use READLINE_LINE")
	}
	if !strings.Contains(output, "READLINE_POINT") {
		t.Error("expected bash snippet to use READLINE_POINT")
	}
}

func TestInitBash_Idempotent(t *testing.T) {
	cmd1 := NewRootCmd()
	cmd2 := NewRootCmd()
	var out1, out2 bytes.Buffer
	cmd1.SetOut(&out1)
	cmd2.SetOut(&out2)
	cmd1.SetArgs([]string{"init", "bash"})
	cmd2.SetArgs([]string{"init", "bash"})

	if err := cmd1.Execute(); err != nil {
		t.Fatal(err)
	}
	if err := cmd2.Execute(); err != nil {
		t.Fatal(err)
	}

	if out1.String() != out2.String() {
		t.Error("expected identical output on repeated calls (idempotent)")
	}
}

func TestInitFish_OutputsSnippet(t *testing.T) {
	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"init", "fish"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := out.String()
	if !strings.Contains(output, `bind \ck _shhh_raw`) {
		t.Error("expected fish snippet to contain bind \\ck for Ctrl+K")
	}
	if !strings.Contains(output, "shhh cmd --raw") {
		t.Error("expected fish snippet to call shhh cmd --raw")
	}
	if !strings.Contains(output, "commandline") {
		t.Error("expected fish snippet to use commandline builtin")
	}
}

func TestInitFish_Idempotent(t *testing.T) {
	cmd1 := NewRootCmd()
	cmd2 := NewRootCmd()
	var out1, out2 bytes.Buffer
	cmd1.SetOut(&out1)
	cmd2.SetOut(&out2)
	cmd1.SetArgs([]string{"init", "fish"})
	cmd2.SetArgs([]string{"init", "fish"})

	if err := cmd1.Execute(); err != nil {
		t.Fatal(err)
	}
	if err := cmd2.Execute(); err != nil {
		t.Fatal(err)
	}

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

// `--project` is declared, and declared as a switch. What it writes is the
// initProject tests below; this is only the name a reader types.
func TestInitProject_FlagIsDeclared(t *testing.T) {
	for _, c := range NewRootCmd().Commands() {
		if c.Name() != "init" {
			continue
		}
		if f := c.Flags().Lookup("project"); f == nil || f.Value.Type() != "bool" {
			t.Fatalf("init has no --project flag: %v", f)
		}
		return
	}
	t.Fatal("no init command is registered")
}

func TestInitProject_CreatesFile(t *testing.T) {
	dir := t.TempDir()

	if err := initProject(dir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".shhh", "project.md"))
	if err != nil {
		t.Fatalf("expected .shhh/project.md to exist: %v", err)
	}
	if !strings.Contains(string(data), "project-local context") {
		t.Error("expected .shhh/project.md to contain template content")
	}
}

func TestInitProject_OldLayoutPointsAtDoctor(t *testing.T) {
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, ".shhh"), []byte("existing"), 0o644))

	err := initProject(dir)
	if err == nil || !strings.Contains(err.Error(), "shhh doctor") {
		t.Fatalf("err = %v, want a pointer at the doctor", err)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, ".shhh")); string(data) != "existing" {
		t.Error("the old file was touched")
	}
}

func TestInitProject_AlreadyExists(t *testing.T) {
	dir := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(dir, ".shhh"), 0o755))
	must(t, os.WriteFile(filepath.Join(dir, ".shhh", "project.md"), []byte("existing"), 0o644))

	if err := initProject(dir); err == nil {
		t.Fatal("expected error when .shhh already exists")
	}
}
