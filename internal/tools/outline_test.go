package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readWith(t *testing.T, r *Recorder, args string) string {
	t.Helper()
	got, err := r.Execute(ReadFileName, json.RawMessage(args))
	if err != nil {
		t.Fatalf("read_file %s: %v", args, err)
	}
	return got
}

// tail_lines is `tail -n`: the last N lines, numbered where they stand in the
// file, under the line giving the whole file's size. A final newline ends the
// last line rather than starting an empty one, with or without it the file
// has the same lines.
func TestReadFile_TailLinesReadsTheEndNumberedAsInTheFile(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		name, content, want string
	}{
		{"trailing newline", "a\nb\nc\nd\n", "4 lines, 8 bytes; showing lines 3-4\n3\tc\n4\td"},
		{"no trailing newline", "a\nb\nc\nd", "4 lines, 7 bytes; showing lines 3-4\n3\tc\n4\td"},
		{"more asked than there are", "a\nb\n", "2 lines, 4 bytes; showing lines 1-2\n1\ta\n2\tb"},
		{"one line", "only", "1 line, 4 bytes; showing lines 1-1\n1\tonly"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, strings.ReplaceAll(tc.name, " ", "-")+".log")
			must(t, os.WriteFile(path, []byte(tc.content), 0o644))
			got := readWith(t, NewRecorder(), fmt.Sprintf(`{"path":%q,"tail_lines":2}`, path))
			if want := path + ": " + tc.want; got != want {
				t.Errorf("got:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}

func TestReadFile_TailLinesIsRefusedBesideARange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.log")
	must(t, os.WriteFile(path, []byte("a\nb\n"), 0o644))
	for _, args := range []string{
		fmt.Sprintf(`{"path":%q,"tail_lines":1,"start_line":1}`, path),
		fmt.Sprintf(`{"path":%q,"tail_lines":1,"end_line":2}`, path),
	} {
		if _, err := NewRecorder().Execute(ReadFileName, json.RawMessage(args)); err == nil || !strings.Contains(err.Error(), "without start_line/end_line") {
			t.Errorf("%s: want the refusal naming the range, got %v", args, err)
		}
	}
	if _, err := NewRecorder().Execute(ReadFileName, json.RawMessage(fmt.Sprintf(`{"path":%q,"tail_lines":0}`, path))); err == nil {
		t.Error("tail_lines 0 should be refused, not read as absent")
	}
}

// A tail read is a window, even one that covered the whole file: a
// write_file built on it is refused the way one built on any window is, and
// an edit inside what it showed is not.
func TestReadFile_ATailReadIsNotAWholeReading(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.log")
	must(t, os.WriteFile(path, []byte("a\nb\n"), 0o644))
	r := NewRecorder()
	readWith(t, r, fmt.Sprintf(`{"path":%q,"tail_lines":5}`, path))
	rec, ok := r.lookupSeen(path)
	if !ok {
		t.Fatal("a tail read should record what it showed")
	}
	if rec.whole {
		t.Error("a tail read was recorded as a reading of the whole file")
	}
}

// Over either cap, a tail keeps the end — the part it was asked for — and
// says which lines were dropped from the front.
func TestReadFile_ATailOverTheCapKeepsTheEnd(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big.log")
	var b strings.Builder
	n := MaxReadFileLines + 50
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	must(t, os.WriteFile(path, []byte(b.String()), 0o644))
	got := readWith(t, NewRecorder(), fmt.Sprintf(`{"path":%q,"tail_lines":%d}`, path, n))
	first, _, _ := strings.Cut(got, "\n")
	if want := fmt.Sprintf("showing lines 51-%d", n); !strings.HasSuffix(first, want) {
		t.Errorf("first line %q should end %q", first, want)
	}
	if !strings.Contains(got, fmt.Sprintf("\n%d\tline %d\n", n, n)) || !strings.Contains(got, "\n51\tline 51\n") {
		t.Errorf("the kept lines should be the last %d, numbered as in the file", MaxReadFileLines)
	}
	if !strings.HasSuffix(got, fmt.Sprintf("the last %d lines, 51-%d of %d; read the lines before with start_line/end_line)", MaxReadFileLines, n, n)) {
		t.Errorf("the notice should name what was dropped, got tail %q", got[len(got)-120:])
	}
}

// Every read that shows part of a file opens with its size; a whole read does
// not, because the model has the file and can count.
func TestReadFile_APartialReadOpensWithTheFilesSize(t *testing.T) {
	dir := t.TempDir()
	small := filepath.Join(dir, "small.txt")
	must(t, os.WriteFile(small, []byte("a\nb"), 0o644))
	if got := readWith(t, NewRecorder(), fmt.Sprintf(`{"path":%q}`, small)); got != "1\ta\n2\tb" {
		t.Errorf("a whole read carries no size line: %q", got)
	}
	if got := readWith(t, NewRecorder(), fmt.Sprintf(`{"path":%q,"start_line":2}`, small)); got != small+": 2 lines, 3 bytes; showing lines 2-2\n2\tb" {
		t.Errorf("windowed: %q", got)
	}

	cut := filepath.Join(dir, "cut.txt")
	must(t, os.WriteFile(cut, []byte(strings.Repeat("x\n", MaxReadFileLines+1)), 0o644))
	got := readWith(t, NewRecorder(), fmt.Sprintf(`{"path":%q}`, cut))
	first, _, _ := strings.Cut(got, "\n")
	if want := fmt.Sprintf("%s: 2,001 lines, 4,002 bytes; showing lines 1-%d", cut, MaxReadFileLines); first != want {
		t.Errorf("a cut read's first line = %q, want %q", first, want)
	}
}

// A listing sizes each file from the stat the walk already holds, after a
// tab, and a directory carries none.
func TestListingsCarryEachFilesSize(t *testing.T) {
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "a.go"), make([]byte, 3000), 0o644))
	must(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o755))

	list, err := executeListDirectory(json.RawMessage(fmt.Sprintf(`{"path":%q}`, dir)))
	if err != nil {
		t.Fatal(err)
	}
	if list != "file: a.go\t3 KB\ndir: sub" {
		t.Errorf("list_directory = %q", list)
	}
	glob, err := executeGlob(json.RawMessage(fmt.Sprintf(`{"pattern":"*.go","path":%q}`, dir)))
	if err != nil {
		t.Fatal(err)
	}
	if glob != "a.go\t3 KB" {
		t.Errorf("glob = %q", glob)
	}
}

// The outline is the headings a reader of the document sees, which is not
// every line that starts with a #: a fenced block, an indented code block and
// the front matter each hold #-lines that are not headings.
func TestDocumentSymbol_OutlinesMarkdownHeadings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "README.md")
	doc := strings.Join([]string{
		"---",
		"# front matter is YAML",
		"---",
		"# Title #",
		"",
		"````bash",
		"# a comment in a fence",
		"```",
		"## still in the fence: a shorter run does not close it",
		"````",
		"~~~",
		"# a tilde fence",
		"~~~",
		"    # indented code",
		"#hashtag is not a heading",
		"   ### Three spaces is ###",
		"####### seven is too many",
		"## Last",
	}, "\n")
	must(t, os.WriteFile(path, []byte(doc), 0o644))
	got, err := executeDocumentSymbol(json.RawMessage(fmt.Sprintf(`{"path":%q}`, path)))
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("Outline of %s (3 headings, 18 lines):\n4 # Title\n16 ### Three spaces is\n18 ## Last", path)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestDocumentSymbol_WithoutAServerOutlinesMarkdownOnly(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.go")
	must(t, os.WriteFile(src, []byte("package a\n"), 0o644))
	if _, err := executeDocumentSymbol(json.RawMessage(fmt.Sprintf(`{"path":%q}`, src))); err == nil || !strings.Contains(err.Error(), "no language server answers for .go files") {
		t.Errorf("a source file with no server should be refused by name, got %v", err)
	}
	plain := filepath.Join(dir, "notes.md")
	must(t, os.WriteFile(plain, []byte("just text\n"), 0o644))
	got, err := executeDocumentSymbol(json.RawMessage(fmt.Sprintf(`{"path":%q}`, plain)))
	if err != nil || got != fmt.Sprintf("No headings in %s (1 line).", plain) {
		t.Errorf("a document with no headings: %q, %v", got, err)
	}
}

func TestLineCount(t *testing.T) {
	for in, want := range map[string]int{"": 0, "a": 1, "a\n": 1, "a\nb": 2, "a\nb\n": 2, "\n": 1, "\n\n": 2} {
		if got := lineCount([]byte(in)); got != want {
			t.Errorf("lineCount(%q) = %d, want %d", in, got, want)
		}
		o, err := scanFile(strings.NewReader(in), false)
		if err != nil || o.lines != want {
			t.Errorf("scanFile(%q) counted %d, want %d", in, o.lines, want)
		}
	}
}
