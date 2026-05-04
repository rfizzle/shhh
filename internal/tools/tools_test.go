package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefinitions(t *testing.T) {
	defs := Definitions()
	if len(defs) != 3 {
		t.Fatalf("expected 3 tool definitions, got %d", len(defs))
	}
	names := map[string]bool{}
	for _, d := range defs {
		names[d.Name] = true
	}
	for _, want := range []string{"read_file", "list_directory", "search"} {
		if !names[want] {
			t.Errorf("missing tool definition: %s", want)
		}
	}
}

func TestExecute_UnknownTool(t *testing.T) {
	_, err := Execute("nonexistent", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
	if !strings.Contains(err.Error(), "unknown tool") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestReadFile_Basic(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.txt")
	os.WriteFile(path, []byte("line1\nline2\nline3\n"), 0o644)

	args, _ := json.Marshal(readFileArgs{Path: path})
	result, err := Execute("read_file", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "line1") || !strings.Contains(result, "line3") {
		t.Errorf("unexpected result: %q", result)
	}
}

func TestReadFile_LineRange(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.txt")
	os.WriteFile(path, []byte("a\nb\nc\nd\ne\n"), 0o644)

	args, _ := json.Marshal(readFileArgs{Path: path, StartLine: 2, EndLine: 4})
	result, err := Execute("read_file", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "b\nc\nd" {
		t.Errorf("expected 'b\\nc\\nd', got %q", result)
	}
}

func TestReadFile_StartLineOnly(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.txt")
	os.WriteFile(path, []byte("a\nb\nc\n"), 0o644)

	args, _ := json.Marshal(readFileArgs{Path: path, StartLine: 2})
	result, err := Execute("read_file", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(result, "b\n") {
		t.Errorf("expected to start at line 2, got %q", result)
	}
}

func TestReadFile_StartLineBeyondEnd(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.txt")
	os.WriteFile(path, []byte("only\n"), 0o644)

	args, _ := json.Marshal(readFileArgs{Path: path, StartLine: 100})
	_, err := Execute("read_file", args)
	if err == nil {
		t.Fatal("expected error for start_line beyond file length")
	}
}

func TestReadFile_MissingFile(t *testing.T) {
	args, _ := json.Marshal(readFileArgs{Path: "/nonexistent/file.txt"})
	_, err := Execute("read_file", args)
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestReadFile_MissingPath(t *testing.T) {
	_, err := Execute("read_file", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for missing path")
	}
}

func TestListDirectory_Basic(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "file.txt"), []byte("hi"), 0o644)
	os.Mkdir(filepath.Join(tmp, "subdir"), 0o755)

	args, _ := json.Marshal(listDirectoryArgs{Path: tmp})
	result, err := Execute("list_directory", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "file: file.txt") {
		t.Errorf("expected 'file: file.txt' in result: %q", result)
	}
	if !strings.Contains(result, "dir: subdir") {
		t.Errorf("expected 'dir: subdir' in result: %q", result)
	}
}

func TestListDirectory_Depth(t *testing.T) {
	tmp := t.TempDir()
	os.MkdirAll(filepath.Join(tmp, "a", "b"), 0o755)
	os.WriteFile(filepath.Join(tmp, "a", "b", "deep.txt"), []byte("x"), 0o644)

	args, _ := json.Marshal(listDirectoryArgs{Path: tmp, Depth: 3})
	result, err := Execute("list_directory", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, filepath.Join("a", "b", "deep.txt")) {
		t.Errorf("expected deep file in result: %q", result)
	}
}

func TestListDirectory_DefaultDepth(t *testing.T) {
	tmp := t.TempDir()
	os.MkdirAll(filepath.Join(tmp, "a", "b"), 0o755)
	os.WriteFile(filepath.Join(tmp, "a", "b", "deep.txt"), []byte("x"), 0o644)

	args, _ := json.Marshal(listDirectoryArgs{Path: tmp})
	result, err := Execute("list_directory", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result, "deep.txt") {
		t.Errorf("default depth 1 should not show deep file: %q", result)
	}
}

func TestListDirectory_MissingPath(t *testing.T) {
	_, err := Execute("list_directory", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for missing path")
	}
}

func TestListDirectory_NonexistentPath(t *testing.T) {
	args, _ := json.Marshal(listDirectoryArgs{Path: "/nonexistent/dir"})
	_, err := Execute("list_directory", args)
	if err == nil {
		t.Fatal("expected error for nonexistent path")
	}
}

func TestSearch_Basic(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "hello.go"), []byte("package main\nfunc Hello() {}\n"), 0o644)
	os.WriteFile(filepath.Join(tmp, "other.go"), []byte("package other\n"), 0o644)

	args, _ := json.Marshal(searchArgs{Pattern: "Hello", Path: tmp})
	result, err := Execute("search", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "hello.go:2:") {
		t.Errorf("expected match in hello.go:2, got: %q", result)
	}
	if strings.Contains(result, "other.go") {
		t.Errorf("should not match other.go: %q", result)
	}
}

func TestSearch_CaseInsensitive(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "test.txt"), []byte("FooBar\n"), 0o644)

	args, _ := json.Marshal(searchArgs{Pattern: "foobar", Path: tmp})
	result, err := Execute("search", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "FooBar") {
		t.Errorf("expected case-insensitive match: %q", result)
	}
}

func TestSearch_NoMatches(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "test.txt"), []byte("nothing here\n"), 0o644)

	args, _ := json.Marshal(searchArgs{Pattern: "zzzzz", Path: tmp})
	result, err := Execute("search", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "No matches found." {
		t.Errorf("expected 'No matches found.', got: %q", result)
	}
}

func TestSearch_DefaultPath(t *testing.T) {
	args, _ := json.Marshal(searchArgs{Pattern: "package"})
	result, err := Execute("search", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "tools.go") {
		t.Errorf("searching cwd should find tools.go: %q", result)
	}
}

func TestSearch_MissingPattern(t *testing.T) {
	_, err := Execute("search", json.RawMessage(`{"path":"."}`))
	if err == nil {
		t.Fatal("expected error for missing pattern")
	}
}

func TestSearch_SkipsGitDir(t *testing.T) {
	tmp := t.TempDir()
	os.MkdirAll(filepath.Join(tmp, ".git"), 0o755)
	os.WriteFile(filepath.Join(tmp, ".git", "config"), []byte("findme\n"), 0o644)
	os.WriteFile(filepath.Join(tmp, "main.go"), []byte("findme\n"), 0o644)

	args, _ := json.Marshal(searchArgs{Pattern: "findme", Path: tmp})
	result, err := Execute("search", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result, ".git") {
		t.Errorf("should skip .git directory: %q", result)
	}
	if !strings.Contains(result, "main.go") {
		t.Errorf("should find match in main.go: %q", result)
	}
}

func TestSearch_SingleFile(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "target.txt")
	os.WriteFile(path, []byte("alpha\nbeta\ngamma\n"), 0o644)

	args, _ := json.Marshal(searchArgs{Pattern: "beta", Path: path})
	result, err := Execute("search", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "target.txt:2:") {
		t.Errorf("expected match at line 2: %q", result)
	}
}

func TestSearch_SkipsBinaryFiles(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "binary.bin"), []byte("findme\x00\x01\x02"), 0o644)
	os.WriteFile(filepath.Join(tmp, "text.txt"), []byte("findme\n"), 0o644)

	args, _ := json.Marshal(searchArgs{Pattern: "findme", Path: tmp})
	result, err := Execute("search", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result, "binary.bin") {
		t.Errorf("should skip binary files: %q", result)
	}
	if !strings.Contains(result, "text.txt") {
		t.Errorf("should find text file: %q", result)
	}
}
