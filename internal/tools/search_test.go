package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// forceWalker disables ripgrep discovery for one test so the pure-Go
// fallback path is exercised deterministically.
func forceWalker(t *testing.T) {
	t.Helper()
	orig := lookupRg
	lookupRg = func() (string, bool) { return "", false }
	t.Cleanup(func() { lookupRg = orig })
}

// requireRg skips the test unless ripgrep is actually on PATH.
func requireRg(t *testing.T) {
	t.Helper()
	if _, ok := lookupRg(); !ok {
		t.Skip("rg not on PATH")
	}
}

func TestSearch_RegexWalker(t *testing.T) {
	forceWalker(t)
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "code.go"), []byte("func Hello() {}\nfunc world() {}\n"), 0o644)

	args, _ := json.Marshal(searchArgs{Pattern: `func H\w+`, Path: tmp})
	result, err := Execute("search", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "code.go:1:") {
		t.Errorf("expected regex match at line 1: %q", result)
	}
	if strings.Contains(result, "code.go:2:") {
		t.Errorf("regex should not match line 2: %q", result)
	}
}

func TestSearch_RegexCaseInsensitiveDefault(t *testing.T) {
	forceWalker(t)
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "test.txt"), []byte("FooBar\n"), 0o644)

	args, _ := json.Marshal(searchArgs{Pattern: `FOO\w+`, Path: tmp})
	result, err := Execute("search", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "FooBar") {
		t.Errorf("expected case-insensitive regex match: %q", result)
	}
}

func TestSearch_CaseSensitiveFlag(t *testing.T) {
	forceWalker(t)
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "test.txt"), []byte("FooBar\nfoobar\n"), 0o644)

	args, _ := json.Marshal(searchArgs{Pattern: "foobar", Path: tmp, CaseSensitive: true})
	result, err := Execute("search", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "test.txt:2:") {
		t.Errorf("expected case-sensitive match on line 2: %q", result)
	}
	if strings.Contains(result, "test.txt:1:") {
		t.Errorf("case-sensitive search should not match FooBar: %q", result)
	}
}

func TestSearch_InvalidRegex(t *testing.T) {
	tmp := t.TempDir()
	args, _ := json.Marshal(searchArgs{Pattern: "[", Path: tmp})
	_, err := Execute("search", args)
	if err == nil {
		t.Fatal("expected error for invalid regex")
	}
	if !strings.Contains(err.Error(), "invalid regular expression") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSearch_WalkerSkipsOversizedFiles(t *testing.T) {
	forceWalker(t)
	tmp := t.TempDir()
	big := append([]byte("findme\n"), make([]byte, MaxSearchFileBytes)...)
	for i := range big[7:] {
		big[7+i] = 'a'
	}
	os.WriteFile(filepath.Join(tmp, "big.txt"), big, 0o644)
	os.WriteFile(filepath.Join(tmp, "small.txt"), []byte("findme\n"), 0o644)

	args, _ := json.Marshal(searchArgs{Pattern: "findme", Path: tmp})
	result, err := Execute("search", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result, "big.txt") {
		t.Errorf("oversized file should be skipped: %q", result)
	}
	if !strings.Contains(result, "small.txt") {
		t.Errorf("small file should match: %q", result)
	}
}

func TestSearch_WalkerSkipsBinaryAndGit(t *testing.T) {
	forceWalker(t)
	tmp := t.TempDir()
	os.MkdirAll(filepath.Join(tmp, ".git"), 0o755)
	os.WriteFile(filepath.Join(tmp, ".git", "config"), []byte("findme\n"), 0o644)
	os.WriteFile(filepath.Join(tmp, "binary.bin"), []byte("findme\x00\x01"), 0o644)
	os.WriteFile(filepath.Join(tmp, "main.go"), []byte("findme\n"), 0o644)

	args, _ := json.Marshal(searchArgs{Pattern: "findme", Path: tmp})
	result, err := Execute("search", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result, ".git") || strings.Contains(result, "binary.bin") {
		t.Errorf("walker should skip .git and binary files: %q", result)
	}
	if !strings.Contains(result, "main.go") {
		t.Errorf("walker should match main.go: %q", result)
	}
}

// fakeRg points lookupRg at a shell script standing in for ripgrep, so the
// rg output parsing and exit-code handling are testable without ripgrep
// installed.
func fakeRg(t *testing.T, script string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake rg script requires a POSIX shell")
	}
	path := filepath.Join(t.TempDir(), "rg")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	orig := lookupRg
	lookupRg = func() (string, bool) { return path, true }
	t.Cleanup(func() { lookupRg = orig })
}

func TestSearch_RipgrepOutputParsing(t *testing.T) {
	fakeRg(t, `printf 'some/file.go\0007:match text\n'`)
	tmp := t.TempDir()

	args, _ := json.Marshal(searchArgs{Pattern: "match", Path: tmp})
	result, err := Execute("search", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "some/file.go:7: match text" {
		t.Errorf("unexpected parsed result: %q", result)
	}
}

func TestSearch_RipgrepNoMatchExitCode(t *testing.T) {
	fakeRg(t, "exit 1")
	tmp := t.TempDir()

	args, _ := json.Marshal(searchArgs{Pattern: "zzzzz", Path: tmp})
	result, err := Execute("search", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "No matches found." {
		t.Errorf("rg exit 1 should mean no matches: %q", result)
	}
}

func TestSearch_RipgrepFailureFallsBackToWalker(t *testing.T) {
	fakeRg(t, "exit 2")
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "real.txt"), []byte("findme\n"), 0o644)

	args, _ := json.Marshal(searchArgs{Pattern: "findme", Path: tmp})
	result, err := Execute("search", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "real.txt:1:") {
		t.Errorf("rg failure should fall back to the walker: %q", result)
	}
}

func TestSearch_Ripgrep(t *testing.T) {
	requireRg(t)
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "hello.go"), []byte("package main\nfunc Hello() {}\n"), 0o644)

	args, _ := json.Marshal(searchArgs{Pattern: `func h\w+`, Path: tmp})
	result, err := Execute("search", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "hello.go:2: func Hello() {}") {
		t.Errorf("expected rg match in path:line: text format: %q", result)
	}
}

func TestSearch_RipgrepSingleFileKeepsFilename(t *testing.T) {
	requireRg(t)
	tmp := t.TempDir()
	path := filepath.Join(tmp, "target.txt")
	os.WriteFile(path, []byte("alpha\nbeta\n"), 0o644)

	args, _ := json.Marshal(searchArgs{Pattern: "beta", Path: path})
	result, err := Execute("search", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "target.txt:2:") {
		t.Errorf("single-file rg search should keep the filename: %q", result)
	}
}

func TestSearch_RipgrepNoMatches(t *testing.T) {
	requireRg(t)
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

func TestSearch_RipgrepSkipsVendor(t *testing.T) {
	requireRg(t)
	tmp := t.TempDir()
	os.MkdirAll(filepath.Join(tmp, "vendor"), 0o755)
	os.WriteFile(filepath.Join(tmp, "vendor", "dep.go"), []byte("findme\n"), 0o644)
	os.WriteFile(filepath.Join(tmp, "main.go"), []byte("findme\n"), 0o644)

	args, _ := json.Marshal(searchArgs{Pattern: "findme", Path: tmp})
	result, err := Execute("search", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result, "vendor") {
		t.Errorf("rg path should skip vendor: %q", result)
	}
	if !strings.Contains(result, "main.go") {
		t.Errorf("rg path should match main.go: %q", result)
	}
}
