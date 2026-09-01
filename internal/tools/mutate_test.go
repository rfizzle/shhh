package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMutating_Definitions(t *testing.T) {
	names := map[string]bool{}
	for _, d := range Mutating() {
		names[d.Tool.Name] = true
	}
	for _, want := range []string{WriteFileName, EditFileName} {
		if !names[want] {
			t.Errorf("missing mutating tool definition: %s", want)
		}
		if !IsMutating(want) {
			t.Errorf("IsMutating(%s) should be true", want)
		}
	}
	if IsMutating("read_file") {
		t.Error("read_file must not be mutating")
	}
}

func TestDefinitionsFull_ContainsAllToolsets(t *testing.T) {
	names := map[string]bool{}
	for _, d := range DefinitionsFull() {
		names[d.Name] = true
	}
	want := []string{"read_file", "list_directory", "search", "glob", ExecCommandName, WriteFileName, EditFileName}
	for _, n := range want {
		if !names[n] {
			t.Errorf("DefinitionsFull missing tool: %s", n)
		}
	}
	if len(names) != len(want) {
		t.Errorf("DefinitionsFull has %d unique tools, want %d", len(names), len(want))
	}
}

func TestExecute_RefusesMutatingTools(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "blocked.txt")
	args, _ := json.Marshal(writeFileArgs{Path: path, Content: "x\n"})

	_, err := Execute(WriteFileName, args)
	if err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("auto-run path must not know write_file, got err=%v", err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatal("auto-run path must not have written the file")
	}
	if _, err := Execute(EditFileName, args); err == nil {
		t.Fatal("auto-run path must not know edit_file")
	}
}

func TestExecuteMutating_RefusesOtherTools(t *testing.T) {
	_, err := ExecuteMutating("read_file", json.RawMessage(`{"path":"x"}`))
	if err == nil || !strings.Contains(err.Error(), "unknown mutating tool") {
		t.Fatalf("expected unknown mutating tool error, got %v", err)
	}
}

func TestWriteFile_NewFileCreatesParents(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "sub", "dir", "new.txt")
	args, _ := json.Marshal(writeFileArgs{Path: path, Content: "one\ntwo\n"})

	result, err := ExecuteMutating(WriteFileName, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "one\ntwo\n" {
		t.Fatalf("file content = %q, err = %v", data, err)
	}
	if !strings.Contains(result, path) || !strings.Contains(result, "8 bytes") || !strings.Contains(result, "2 lines") {
		t.Errorf("result should report path, bytes, and lines: %q", result)
	}
	if !strings.Contains(result, "Created") {
		t.Errorf("result should say the file is new: %q", result)
	}
}

func TestWriteFile_Overwrite(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "old.txt")
	if err := os.WriteFile(path, []byte("previous content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	readWholeFile(t, path)
	args, _ := json.Marshal(writeFileArgs{Path: path, Content: "new\n"})

	result, err := ExecuteMutating(WriteFileName, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "new\n" {
		t.Fatalf("file content = %q", data)
	}
	if !strings.Contains(result, "Overwrote") || !strings.Contains(result, path) {
		t.Errorf("result should report the overwrite: %q", result)
	}
}

func TestWriteFile_MissingPath(t *testing.T) {
	_, err := ExecuteMutating(WriteFileName, json.RawMessage(`{"content":"x"}`))
	if err == nil || !strings.Contains(err.Error(), "path is required") {
		t.Fatalf("expected path error, got %v", err)
	}
}

func editArgs(t *testing.T, args editFileArgs) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestEditFile_SingleReplace(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "code.go")
	if err := os.WriteFile(path, []byte("alpha\nbeta\ngamma\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := ExecuteMutating(EditFileName, editArgs(t, editFileArgs{Path: path, OldText: "beta", NewText: "delta"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "alpha\ndelta\ngamma\n" {
		t.Fatalf("file content = %q", data)
	}
	if !strings.Contains(result, "1 replacement") || !strings.Contains(result, path) {
		t.Errorf("result should report the replacement and path: %q", result)
	}
}

func TestEditFile_NoMatch(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "code.go")
	if err := os.WriteFile(path, []byte("alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := ExecuteMutating(EditFileName, editArgs(t, editFileArgs{Path: path, OldText: "missing", NewText: "x"}))
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected no-match error, got %v", err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "alpha\n" {
		t.Fatal("file must be untouched on no-match")
	}
}

func TestEditFile_MultiMatchWithoutReplaceAll(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "code.go")
	if err := os.WriteFile(path, []byte("x\nx\nx\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := ExecuteMutating(EditFileName, editArgs(t, editFileArgs{Path: path, OldText: "x", NewText: "y"}))
	if err == nil || !strings.Contains(err.Error(), "3 locations") || !strings.Contains(err.Error(), "replace_all") {
		t.Fatalf("expected multi-match error naming the count and replace_all, got %v", err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "x\nx\nx\n" {
		t.Fatal("file must be untouched on ambiguous match")
	}
}

func TestEditFile_ReplaceAll(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "code.go")
	if err := os.WriteFile(path, []byte("x\nx\nx\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := ExecuteMutating(EditFileName, editArgs(t, editFileArgs{Path: path, OldText: "x", NewText: "y", ReplaceAll: true}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "y\ny\ny\n" {
		t.Fatalf("file content = %q", data)
	}
	if !strings.Contains(result, "3 replacement") {
		t.Errorf("result should report all replacements: %q", result)
	}
}

func TestEditFile_MissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.txt")
	_, err := ExecuteMutating(EditFileName, editArgs(t, editFileArgs{Path: path, OldText: "a", NewText: "b"}))
	if err == nil || !strings.Contains(err.Error(), "cannot read file") {
		t.Fatalf("expected read error for missing file, got %v", err)
	}
}

func TestEditFile_IdenticalTexts(t *testing.T) {
	_, err := ExecuteMutating(EditFileName, editArgs(t, editFileArgs{Path: "x", OldText: "same", NewText: "same"}))
	if err == nil || !strings.Contains(err.Error(), "identical") {
		t.Fatalf("expected identical-text error, got %v", err)
	}
}

func TestPreviewMutation_WriteNewAndExisting(t *testing.T) {
	tmp := t.TempDir()
	newPath := filepath.Join(tmp, "new.txt")
	args, _ := json.Marshal(writeFileArgs{Path: newPath, Content: "hello\n"})
	mut, err := PreviewMutation(WriteFileName, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mut.Action != "write" || mut.Path != newPath || mut.OldText != "" || mut.NewText != "hello\n" {
		t.Errorf("unexpected preview: %+v", mut)
	}
	if _, statErr := os.Stat(newPath); !os.IsNotExist(statErr) {
		t.Fatal("preview must not create the file")
	}

	existing := filepath.Join(tmp, "old.txt")
	if err := os.WriteFile(existing, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	readWholeFile(t, existing)
	args, _ = json.Marshal(writeFileArgs{Path: existing, Content: "after\n"})
	mut, err = PreviewMutation(WriteFileName, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mut.OldText != "before\n" || mut.NewText != "after\n" {
		t.Errorf("preview should diff current against proposed content: %+v", mut)
	}
}

func TestPreviewMutation_EditValidatesLikeExecution(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "code.go")
	if err := os.WriteFile(path, []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	mut, err := PreviewMutation(EditFileName, editArgs(t, editFileArgs{Path: path, OldText: "beta", NewText: "delta"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mut.Action != "edit" || mut.OldText != "alpha\nbeta\n" || mut.NewText != "alpha\ndelta\n" {
		t.Errorf("unexpected preview: %+v", mut)
	}

	if _, err := PreviewMutation(EditFileName, editArgs(t, editFileArgs{Path: path, OldText: "missing", NewText: "x"})); err == nil {
		t.Fatal("preview should surface no-match errors")
	}
	if _, err := PreviewMutation("unknown_tool", json.RawMessage(`{}`)); err == nil {
		t.Fatal("preview should refuse unknown tools")
	}
}

func TestCountLines(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"a", 1},
		{"a\n", 1},
		{"a\nb", 2},
		{"a\nb\n", 2},
	}
	for _, c := range cases {
		if got := countLines(c.in); got != c.want {
			t.Errorf("countLines(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

// The line-number prefix read_file adds is the mistake it invites: a reader
// quotes a numbered line straight back into old_text. The error has to name
// that, or "not found" against text visibly present in the file is a puzzle.
func TestEditFile_NamesTheLineNumberPrefixWhenTheMatchFails(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "a.go")
	must(t, os.WriteFile(path, []byte("package a\n\nfunc F() {}\n"), 0o644))

	args, _ := json.Marshal(editFileArgs{Path: path, OldText: "3\tfunc F() {}", NewText: "func G() {}"})
	_, err := ExecuteMutating("edit_file", args)
	if err == nil {
		t.Fatal("expected the numbered snippet not to match")
	}
	if !strings.Contains(err.Error(), "line-number prefixes") {
		t.Errorf("error should name the prefix, got: %v", err)
	}

	// The same edit without the prefix is the one that works.
	args, _ = json.Marshal(editFileArgs{Path: path, OldText: "func F() {}", NewText: "func G() {}"})
	if _, err := ExecuteMutating("edit_file", args); err != nil {
		t.Fatalf("unprefixed snippet should apply: %v", err)
	}
}

// Ordinary misses keep the ordinary message: the hint is for text that
// actually looks numbered.
func TestEditFile_KeepsThePlainMissMessage(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "a.go")
	must(t, os.WriteFile(path, []byte("package a\n"), 0o644))

	args, _ := json.Marshal(editFileArgs{Path: path, OldText: "func Missing()", NewText: "x"})
	_, err := ExecuteMutating("edit_file", args)
	if err == nil {
		t.Fatal("expected a miss")
	}
	if strings.Contains(err.Error(), "line-number prefixes") {
		t.Errorf("plain miss should not blame the prefix, got: %v", err)
	}
}

func TestLooksLineNumbered(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"3\tfunc F() {}", true},
		{"10\ta\n11\tb", true},
		{"func F() {}", false},
		{"3\ta\nplain", false},
		{"\tindented", false},
		{"", false},
	} {
		if got := looksLineNumbered(tc.in); got != tc.want {
			t.Errorf("looksLineNumbered(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
