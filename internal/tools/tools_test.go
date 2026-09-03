package tools

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/attachment"
	"github.com/rfizzle/shhh/internal/provider"
)

func TestDefinitions(t *testing.T) {
	defs := Definitions()
	if len(defs) != 4 {
		t.Fatalf("expected 4 tool definitions, got %d", len(defs))
	}
	names := map[string]bool{}
	for _, d := range defs {
		names[d.Name] = true
	}
	for _, want := range []string{"read_file", "list_directory", "search", "glob"} {
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
	must(t, os.WriteFile(path, []byte("line1\nline2\nline3\n"), 0o644))

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
	must(t, os.WriteFile(path, []byte("a\nb\nc\nd\ne\n"), 0o644))

	args, _ := json.Marshal(readFileArgs{Path: path, StartLine: 2, EndLine: 4})
	result, err := Execute("read_file", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "2\tb\n3\tc\n4\td" {
		t.Errorf("expected numbered 'b\\nc\\nd', got %q", result)
	}
}

func TestReadFile_StartLineOnly(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.txt")
	must(t, os.WriteFile(path, []byte("a\nb\nc\n"), 0o644))

	args, _ := json.Marshal(readFileArgs{Path: path, StartLine: 2})
	result, err := Execute("read_file", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(result, "2\tb\n") {
		t.Errorf("expected to start at line 2, got %q", result)
	}
}

// Line numbers are what let a reader cite file:line without counting, so they
// carry the file's own numbering — not the window's.
func TestReadFile_NumbersLinesFromTheirPlaceInTheFile(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.txt")
	must(t, os.WriteFile(path, []byte("a\nb\nc\nd\n"), 0o644))

	args, _ := json.Marshal(readFileArgs{Path: path})
	whole, err := Execute("read_file", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if whole != "1\ta\n2\tb\n3\tc\n4\td\n5\t" {
		t.Errorf("whole-file numbering: %q", whole)
	}

	args, _ = json.Marshal(readFileArgs{Path: path, StartLine: 3, EndLine: 3})
	window, err := Execute("read_file", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if window != "3\tc" {
		t.Errorf("a window keeps the file's numbering, got %q", window)
	}
}

// .git under a listed directory is named but never descended into: it used to
// spend most of the entry budget on object shards.
func TestListDirectory_NamesButDoesNotEnterSkippedDirs(t *testing.T) {
	tmp := t.TempDir()
	for _, skipped := range []string{".git", "node_modules", "vendor"} {
		must(t, os.MkdirAll(filepath.Join(tmp, skipped, "inner"), 0o755))
		must(t, os.WriteFile(filepath.Join(tmp, skipped, "inner", "buried.txt"), []byte("x"), 0o644))
	}
	must(t, os.MkdirAll(filepath.Join(tmp, "src"), 0o755))
	must(t, os.WriteFile(filepath.Join(tmp, "src", "main.go"), []byte("x"), 0o644))

	args, _ := json.Marshal(listDirectoryArgs{Path: tmp, Depth: 3})
	out, err := Execute("list_directory", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, skipped := range []string{".git", "node_modules", "vendor"} {
		if !strings.Contains(out, "dir: "+skipped) {
			t.Errorf("expected %s to be named, got %q", skipped, out)
		}
	}
	if strings.Contains(out, "buried.txt") {
		t.Errorf("descended into a skipped directory: %q", out)
	}
	if !strings.Contains(out, "src/main.go") {
		t.Errorf("expected the real tree to still be walked, got %q", out)
	}
}

// A directory the caller names is the one they asked for, skip list or not.
func TestListDirectory_ListsASkippedDirWhenAskedForDirectly(t *testing.T) {
	tmp := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(tmp, ".git"), 0o755))
	must(t, os.WriteFile(filepath.Join(tmp, ".git", "HEAD"), []byte("ref"), 0o644))

	args, _ := json.Marshal(listDirectoryArgs{Path: filepath.Join(tmp, ".git")})
	out, err := Execute("list_directory", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "HEAD") {
		t.Errorf("expected the named directory to be listed, got %q", out)
	}
}

// The reduction pipeline must leave results these tools already sized alone.
func TestSelfBounding(t *testing.T) {
	for _, name := range []string{ReadFileName, ListDirectoryName, SearchName, GlobName} {
		if !SelfBounding(name) {
			t.Errorf("%s bounds its own output and should be exempt from reduction", name)
		}
	}
	for _, name := range []string{ExecCommandName, WriteFileName, "web_fetch", "docs__search"} {
		if SelfBounding(name) {
			t.Errorf("%s does not bound its own output", name)
		}
	}
}

func TestReadFile_StartLineBeyondEnd(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.txt")
	must(t, os.WriteFile(path, []byte("only\n"), 0o644))

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
	must(t, os.WriteFile(filepath.Join(tmp, "file.txt"), []byte("hi"), 0o644))
	must(t, os.Mkdir(filepath.Join(tmp, "subdir"), 0o755))

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
	must(t, os.MkdirAll(filepath.Join(tmp, "a", "b"), 0o755))
	must(t, os.WriteFile(filepath.Join(tmp, "a", "b", "deep.txt"), []byte("x"), 0o644))

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
	must(t, os.MkdirAll(filepath.Join(tmp, "a", "b"), 0o755))
	must(t, os.WriteFile(filepath.Join(tmp, "a", "b", "deep.txt"), []byte("x"), 0o644))

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
	must(t, os.WriteFile(filepath.Join(tmp, "hello.go"), []byte("package main\nfunc Hello() {}\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(tmp, "other.go"), []byte("package other\n"), 0o644))

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
	must(t, os.WriteFile(filepath.Join(tmp, "test.txt"), []byte("FooBar\n"), 0o644))

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
	must(t, os.MkdirAll(filepath.Join(tmp, ".git"), 0o755))
	must(t, os.WriteFile(filepath.Join(tmp, ".git", "config"), []byte("findme\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(tmp, "main.go"), []byte("findme\n"), 0o644))

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
	must(t, os.WriteFile(path, []byte("alpha\nbeta\ngamma\n"), 0o644))

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
	must(t, os.WriteFile(filepath.Join(tmp, "binary.bin"), []byte("findme\x00\x01\x02"), 0o644))
	must(t, os.WriteFile(filepath.Join(tmp, "text.txt"), []byte("findme\n"), 0o644))

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

// must fails the test on an error from setting up its files.
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// tinyPNG is a real, valid PNG: the sniff is a content sniff, so a file
// named .png that is not one must not pass it.
func tinyPNG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestReadFile_RefusesOverTheCeilingBeforeOpeningTheFile(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "huge.log")
	f, err := os.Create(path)
	must(t, err)
	// Sparse: the point is the size in the stat, not 200 MiB of bytes.
	must(t, f.Truncate(200<<20))
	must(t, f.Close())
	// Unreadable, so a refusal that named permission rather than size would
	// mean the file had been opened before the ceiling was consulted.
	must(t, os.Chmod(path, 0))

	args, _ := json.Marshal(readFileArgs{Path: path})
	_, err = Execute("read_file", args)
	if err == nil {
		t.Fatal("expected a file over the ceiling to be refused")
	}
	if !strings.Contains(err.Error(), "200.0 MB") || !strings.Contains(err.Error(), "10.0 MB") {
		t.Errorf("the refusal should name both sizes, got: %v", err)
	}
}

func TestReadFile_BinaryIsANoticeNamingWhatItIs(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "index.db")
	must(t, os.WriteFile(path, append([]byte("SQLite format 3\x00"), make([]byte, 300)...), 0o644))

	args, _ := json.Marshal(readFileArgs{Path: path})
	result, err := Execute("read_file", args)
	if err != nil {
		t.Fatalf("a binary file is an answer, not an error: %v", err)
	}
	if strings.Contains(strings.TrimSuffix(result, "\n"), "\n") {
		t.Errorf("the notice should be one line, got: %q", result)
	}
	if !strings.Contains(result, "application/octet-stream") {
		t.Errorf("the notice should name the detected type, got: %q", result)
	}
	if _, ok := lookupSeen(path); ok {
		t.Error("a file that was never shown must not be recorded as read")
	}
}

func TestReadFile_TextWithNULPastTheSnifferIsStillBinary(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "mixed.bin")
	body := append([]byte(strings.Repeat("prose and more prose\n", 40)), 0x00, 0x01)
	must(t, os.WriteFile(path, body, 0o644))

	args, _ := json.Marshal(readFileArgs{Path: path})
	result, err := Execute("read_file", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "binary file") {
		t.Errorf("a NUL inside the sniffed window makes a file binary, got: %q", result)
	}
}

func TestReadFile_ImageComesBackAsANoticeAndAnAttachment(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "logo.png")
	want := tinyPNG(t)
	must(t, os.WriteFile(path, want, 0o644))

	args, _ := json.Marshal(readFileArgs{Path: path})
	result, err := Execute("read_file", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "image/png") {
		t.Errorf("the notice should name the detected type, got: %q", result)
	}
	atts := attachment.TakeResult(result)
	if len(atts) != 1 {
		t.Fatalf("expected the image to ride on the result, got %d attachments", len(atts))
	}
	if atts[0].Kind != provider.AttachmentImage || atts[0].MediaType != "image/png" {
		t.Errorf("unexpected attachment: %+v", provider.Attachment{Kind: atts[0].Kind, Name: atts[0].Name, MediaType: atts[0].MediaType})
	}
	if !bytes.Equal(atts[0].Data, want) {
		t.Errorf("the attachment should carry the whole file: %d bytes of %d", len(atts[0].Data), len(want))
	}
	if attachment.TakeResult(result) != nil {
		t.Error("a collected result should not be collectable twice")
	}
}

func TestReadFile_ImageTooLargeToAttachIsStillANotice(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "big.png")
	// A real PNG header, padded past the attachment ceiling.
	must(t, os.WriteFile(path, append(tinyPNG(t), make([]byte, attachment.MaxBytes)...), 0o644))

	args, _ := json.Marshal(readFileArgs{Path: path})
	result, err := Execute("read_file", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "binary file") {
		t.Errorf("an image nothing can carry is described as bytes, got: %q", result)
	}
	if atts := attachment.TakeResult(result); atts != nil {
		t.Errorf("expected no attachment, got %d", len(atts))
	}
}

func TestListDirectory_LeavesOutWhatGitignoreNames(t *testing.T) {
	tmp := t.TempDir()
	must(t, os.WriteFile(filepath.Join(tmp, ".gitignore"), []byte("dist/\n*.log\n"), 0o644))
	must(t, os.MkdirAll(filepath.Join(tmp, "dist"), 0o755))
	must(t, os.WriteFile(filepath.Join(tmp, "dist", "app.js"), []byte("x"), 0o644))
	must(t, os.WriteFile(filepath.Join(tmp, "main.go"), []byte("x"), 0o644))
	must(t, os.WriteFile(filepath.Join(tmp, "trace.log"), []byte("x"), 0o644))

	args, _ := json.Marshal(listDirectoryArgs{Path: tmp, Depth: 3})
	result, err := Execute("list_directory", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "main.go") {
		t.Errorf("expected main.go in the listing, got: %q", result)
	}
	for _, banned := range []string{"dist", "trace.log"} {
		if strings.Contains(result, banned) {
			t.Errorf("%s is gitignored and must not be listed, got: %q", banned, result)
		}
	}
}

func TestListDirectory_ListsAnIgnoredDirectoryWhenAskedForItByName(t *testing.T) {
	tmp := t.TempDir()
	must(t, os.WriteFile(filepath.Join(tmp, ".gitignore"), []byte("dist/\n"), 0o644))
	must(t, os.MkdirAll(filepath.Join(tmp, "dist"), 0o755))
	must(t, os.WriteFile(filepath.Join(tmp, "dist", "app.js"), []byte("x"), 0o644))

	args, _ := json.Marshal(listDirectoryArgs{Path: filepath.Join(tmp, "dist")})
	result, err := Execute("list_directory", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "app.js") {
		t.Errorf("a directory named directly is one the caller chose to look in, got: %q", result)
	}
}

func TestSniffText(t *testing.T) {
	cases := []struct {
		name      string
		head      []byte
		mediaType string
		text      bool
	}{
		{"source", []byte("package main\n\nfunc main() {}\n"), "text/plain", true},
		{"empty", nil, "text/plain", true},
		{"utf8 prose", []byte("héllo — em dash and all\n"), "text/plain", true},
		{"html", []byte("<!DOCTYPE html><html></html>"), "text/html", true},
		{"nul early", []byte("SQLite format 3\x00"), "application/octet-stream", false},
		{"nul late", append([]byte(strings.Repeat("x", 1000)), 0x00), "text/plain", false},
		{"png", tinyPNG(t), "image/png", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mediaType, text := sniffText(tc.head)
			if mediaType != tc.mediaType || text != tc.text {
				t.Errorf("got (%q, %v), want (%q, %v)", mediaType, text, tc.mediaType, tc.text)
			}
		})
	}
}
