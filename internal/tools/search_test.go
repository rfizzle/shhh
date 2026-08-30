package tools

import (
	"encoding/json"
	"fmt"
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
	must(t, os.WriteFile(filepath.Join(tmp, "code.go"), []byte("func Hello() {}\nfunc world() {}\n"), 0o644))

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
	must(t, os.WriteFile(filepath.Join(tmp, "test.txt"), []byte("FooBar\n"), 0o644))

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
	must(t, os.WriteFile(filepath.Join(tmp, "test.txt"), []byte("FooBar\nfoobar\n"), 0o644))

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
	must(t, os.WriteFile(filepath.Join(tmp, "big.txt"), big, 0o644))
	must(t, os.WriteFile(filepath.Join(tmp, "small.txt"), []byte("findme\n"), 0o644))

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
	must(t, os.MkdirAll(filepath.Join(tmp, ".git"), 0o755))
	must(t, os.WriteFile(filepath.Join(tmp, ".git", "config"), []byte("findme\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(tmp, "binary.bin"), []byte("findme\x00\x01"), 0o644))
	must(t, os.WriteFile(filepath.Join(tmp, "main.go"), []byte("findme\n"), 0o644))

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
	must(t, os.WriteFile(filepath.Join(tmp, "real.txt"), []byte("findme\n"), 0o644))

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
	must(t, os.WriteFile(filepath.Join(tmp, "hello.go"), []byte("package main\nfunc Hello() {}\n"), 0o644))

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
	must(t, os.WriteFile(path, []byte("alpha\nbeta\n"), 0o644))

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
	must(t, os.WriteFile(filepath.Join(tmp, "test.txt"), []byte("nothing here\n"), 0o644))

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
	must(t, os.MkdirAll(filepath.Join(tmp, "vendor"), 0o755))
	must(t, os.WriteFile(filepath.Join(tmp, "vendor", "dep.go"), []byte("findme\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(tmp, "main.go"), []byte("findme\n"), 0o644))

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

// The options that let one search finish a thought. Each is checked
// on both backends: the walker deterministically, ripgrep when it is present,
// because the two must answer the same question the same way.

func searchOptsFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a.go"), "package a\n\nfunc Target() {\n\treturn\n}\n\nfunc other() {}\n")
	mustWrite(t, filepath.Join(dir, "b.go"), "package b\n\nvar Target = 1\nvar Target2 = 2\n")
	mustWrite(t, filepath.Join(dir, "c.txt"), "Target in a text file\n")
	return dir
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runSearch(t *testing.T, args string) string {
	t.Helper()
	out, err := executeSearch(json.RawMessage(args))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return out
}

func TestSearch_ContextLines(t *testing.T) {
	check := func(t *testing.T) {
		dir := searchOptsFixture(t)
		out := runSearch(t, fmt.Sprintf(`{"pattern":"func Target","path":%q,"context_lines":2}`, dir))
		// The match keeps its ':' separator, context lines take '-'.
		if !strings.Contains(out, "a.go:3: func Target() {") {
			t.Errorf("expected the match line, got:\n%s", out)
		}
		if !strings.Contains(out, "a.go:4- \treturn") {
			t.Errorf("expected trailing context, got:\n%s", out)
		}
		if !strings.Contains(out, "a.go:1- package a") {
			t.Errorf("expected leading context, got:\n%s", out)
		}
	}
	t.Run("walker", func(t *testing.T) { forceWalker(t); check(t) })
	t.Run("ripgrep", func(t *testing.T) { requireRg(t); check(t) })
}

func TestSearch_ContextLinesClamped(t *testing.T) {
	forceWalker(t)
	dir := searchOptsFixture(t)
	out := runSearch(t, fmt.Sprintf(`{"pattern":"Target","path":%q,"context_lines":500}`, dir))
	if strings.Contains(out, "(truncated") {
		t.Errorf("an over-large context should clamp, not truncate:\n%s", out)
	}
}

func TestSearch_FilesOnly(t *testing.T) {
	check := func(t *testing.T) {
		dir := searchOptsFixture(t)
		out := runSearch(t, fmt.Sprintf(`{"pattern":"Target","path":%q,"files_only":true}`, dir))
		if !strings.Contains(out, "b.go: 2 matches") {
			t.Errorf("expected b.go with its count, got:\n%s", out)
		}
		if !strings.Contains(out, "a.go: 1 match") {
			t.Errorf("expected a.go with a singular count, got:\n%s", out)
		}
		if strings.Contains(out, "func Target") {
			t.Errorf("files_only must not quote matching lines, got:\n%s", out)
		}
	}
	t.Run("walker", func(t *testing.T) { forceWalker(t); check(t) })
	t.Run("ripgrep", func(t *testing.T) { requireRg(t); check(t) })
}

func TestSearch_Include(t *testing.T) {
	check := func(t *testing.T) {
		dir := searchOptsFixture(t)
		out := runSearch(t, fmt.Sprintf(`{"pattern":"Target","path":%q,"include":"*.go"}`, dir))
		if strings.Contains(out, "c.txt") {
			t.Errorf("include should have excluded the text file, got:\n%s", out)
		}
		if !strings.Contains(out, "a.go") || !strings.Contains(out, "b.go") {
			t.Errorf("include should have kept the Go files, got:\n%s", out)
		}
	}
	t.Run("walker", func(t *testing.T) { forceWalker(t); check(t) })
	t.Run("ripgrep", func(t *testing.T) { requireRg(t); check(t) })
}

func TestSearch_IncludeInvalid(t *testing.T) {
	forceWalker(t)
	dir := searchOptsFixture(t)
	if _, err := executeSearch(json.RawMessage(fmt.Sprintf(`{"pattern":"Target","path":%q,"include":"[bad"}`, dir))); err == nil {
		t.Error("expected an error for a malformed include pattern")
	}
}

func TestSearch_ContextGroupsSeparated(t *testing.T) {
	forceWalker(t)
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "f.txt"), "hit\n"+strings.Repeat("filler\n", 20)+"hit\n")
	out := runSearch(t, fmt.Sprintf(`{"pattern":"hit","path":%q,"context_lines":1}`, dir))
	if !strings.Contains(out, "\n--\n") {
		t.Errorf("expected a group separator between distant matches, got:\n%s", out)
	}
}

// The rg backend for the search options, against ripgrep's real output
// shapes. ripgrep is not installed everywhere, so the fake stands in for it:
// what is under test is our parsing and the argv we ask for, both of which
// are ours.

func TestSearch_RipgrepContextParsing(t *testing.T) {
	// A match line, its context lines, and the bare "--" rg puts between
	// non-adjacent groups.
	fakeRg(t, `printf 'a.go\0005-package a\na.go\0006:func Target() {\na.go\0007-\treturn\n--\na.go\00020:func Target2() {\n'`)
	out := runSearch(t, fmt.Sprintf(`{"pattern":"func Target","path":%q,"context_lines":2}`, t.TempDir()))

	want := "a.go:5- package a\na.go:6: func Target() {\na.go:7- \treturn\n--\na.go:20: func Target2() {"
	if out != want {
		t.Errorf("unexpected parse:\ngot:\n%s\nwant:\n%s", out, want)
	}
}

func TestSearch_RipgrepFilesOnlyParsing(t *testing.T) {
	fakeRg(t, `printf 'a.go\0001\nb.go\00012\n'`)
	out := runSearch(t, fmt.Sprintf(`{"pattern":"Target","path":%q,"files_only":true}`, t.TempDir()))

	want := "a.go: 1 match\nb.go: 12 matches"
	if out != want {
		t.Errorf("unexpected parse:\ngot:\n%s\nwant:\n%s", out, want)
	}
}

func TestSearch_RipgrepArgvPerMode(t *testing.T) {
	argvFile := filepath.Join(t.TempDir(), "argv")
	fakeRg(t, fmt.Sprintf(`printf '%%s\n' "$@" > %s`, argvFile))

	read := func() string {
		t.Helper()
		b, err := os.ReadFile(argvFile)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	dir := t.TempDir()

	runSearch(t, fmt.Sprintf(`{"pattern":"x","path":%q,"files_only":true}`, dir))
	argv := read()
	if !strings.Contains(argv, "--count-matches\n") {
		t.Errorf("files_only should ask rg to count matches, got:\n%s", argv)
	}
	if strings.Contains(argv, "--line-number\n") {
		t.Errorf("--line-number is meaningless with --count-matches, got:\n%s", argv)
	}

	runSearch(t, fmt.Sprintf(`{"pattern":"x","path":%q,"context_lines":3}`, dir))
	argv = read()
	if !strings.Contains(argv, "--context\n3\n") {
		t.Errorf("expected a context window of 3, got:\n%s", argv)
	}

	runSearch(t, fmt.Sprintf(`{"pattern":"x","path":%q,"include":"*.go","context_lines":9}`, dir))
	argv = read()
	if !strings.Contains(argv, "--glob\n*.go\n") {
		t.Errorf("expected include to reach rg as a glob, got:\n%s", argv)
	}
	if !strings.Contains(argv, fmt.Sprintf("--context\n%d\n", MaxSearchContextLines)) {
		t.Errorf("an over-large context should clamp before reaching rg, got:\n%s", argv)
	}
}
