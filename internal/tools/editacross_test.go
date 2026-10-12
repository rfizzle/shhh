package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/receipt/describe"
)

// editCall runs edit_file against rec with args marshalled from m.
func editCall(t *testing.T, rec *Recorder, m map[string]any) (string, error) {
	t.Helper()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return rec.ExecuteMutating(context.Background(), EditFileName, raw)
}

// writeFiles puts each name → content under a fresh directory and returns
// the directory.
func writeFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func readBack(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestEditFile_RegexReplacesWithWhatMatched(t *testing.T) {
	for _, tc := range []struct {
		name       string
		content    string
		old, new   string
		replaceAll bool
		want       string
		wantErr    string
	}{
		{name: "a numbered group", content: "call(a)\ncall(b)\n", old: `call\((\w+)\)`, new: "call($1, ctx)", replaceAll: true,
			want: "call(a, ctx)\ncall(b, ctx)\n"},
		{name: "a named group", content: "x := Foo(1)\n", old: `Foo\((?P<arg>\d+)\)`, new: "Bar(${arg})",
			want: "x := Bar(1)\n"},
		{name: "a group followed by a letter, braced", content: "id7\n", old: `id(\d)`, new: "${1}x",
			want: "7x\n"},
		{name: "a literal dollar is $$", content: "price 5\n", old: `price (\d)`, new: "cost $$$1",
			want: "cost $5\n"},
		{name: "a lone dollar is itself", content: "a\n", old: `a`, new: "$ a",
			want: "$ a\n"},
		{name: "line anchors are line ends", content: "one\ntwo\n", old: `^two$`, new: "2",
			want: "one\n2\n"},
		{name: "case is matched exactly", content: "Foo foo\n", old: `foo`, new: "bar",
			want: "Foo bar\n"},
		{name: "a group the pattern does not have is refused", content: "id7\n", old: `id(\d)`, new: "$1x",
			wantErr: "refers to $1x"},
		{name: "a group number past the last is refused", content: "id7\n", old: `id(\d)`, new: "$2",
			wantErr: "refers to $2"},
		{name: "a pattern that can match empty text is refused", content: "aaa\n", old: `a*`, new: "b",
			wantErr: "can match empty text"},
		{name: "a zero-width match in the file is refused", content: "foo bar\n", old: `\b`, new: "|", replaceAll: true,
			wantErr: "matches empty text at line 1"},
		{name: "several matches without replace_all are the literal refusal", content: "a1 a2\n", old: `a\d`, new: "b",
			wantErr: "old_text matches 2 locations in"},
		{name: "no match names the file", content: "abc\n", old: `z+`, new: "y",
			wantErr: "old_text pattern matches nothing in"},
		{name: "a pattern that does not parse", content: "abc\n", old: `(`, new: "y",
			wantErr: "invalid regular expression"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeFiles(t, map[string]string{"f.txt": tc.content})
			p := filepath.Join(dir, "f.txt")
			out, err := editCall(t, NewRecorder(), map[string]any{"path": p, "old_text": tc.old, "new_text": tc.new, "regex": true, "replace_all": tc.replaceAll})
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want it to contain %q", err, tc.wantErr)
				}
				if got := readBack(t, p); got != tc.content {
					t.Fatalf("a refused edit wrote the file: %q", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := readBack(t, p); got != tc.want {
				t.Fatalf("file = %q, want %q", got, tc.want)
			}
			if !strings.Contains(out, "\nReplaced:\n  line ") {
				t.Errorf("the result lists no replacements: %q", out)
			}
		})
	}
}

// The preview the card draws and the write are the same computation, so the
// card never shows a replacement the file does not get.
func TestEditFile_RegexPreviewIsTheWrite(t *testing.T) {
	dir := writeFiles(t, map[string]string{"f.go": "f(a)\nf(b)\n"})
	p := filepath.Join(dir, "f.go")
	args := map[string]any{"path": p, "old_text": `f\((\w)\)`, "new_text": "g($1, $1)", "regex": true, "replace_all": true}
	raw, _ := json.Marshal(args)
	rec := NewRecorder()
	mut, err := rec.PreviewMutation(EditFileName, raw)
	if err != nil {
		t.Fatal(err)
	}
	if !mut.Regex || mut.Pattern != `f\((\w)\)` || mut.Replacement != "g($1, $1)" {
		t.Errorf("preview = %+v", mut)
	}
	if _, err := editCall(t, rec, args); err != nil {
		t.Fatal(err)
	}
	if got := readBack(t, p); got != mut.NewText {
		t.Fatalf("written %q, previewed %q", got, mut.NewText)
	}
}

// The result's list of before/after pairs is bounded, and says how many it
// left out.
func TestEditFile_RegexResultListsBoundedPairs(t *testing.T) {
	var b strings.Builder
	for i := range maxReplacementPairs + 5 {
		fmt.Fprintf(&b, "v%d\n", i)
	}
	dir := writeFiles(t, map[string]string{"f.txt": b.String()})
	out, err := editCall(t, NewRecorder(), map[string]any{"path": filepath.Join(dir, "f.txt"), "old_text": `v(\d+)`, "new_text": "w$1", "regex": true, "replace_all": true})
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(out, " → "); n != maxReplacementPairs {
		t.Errorf("listed %d pairs, want %d", n, maxReplacementPairs)
	}
	if !strings.Contains(out, "… 5 more not listed") {
		t.Errorf("the cut is not counted: %q", out)
	}
	if !strings.Contains(out, `line 1: "v0" → "w0"`) {
		t.Errorf("a pair reads wrong: %q", out)
	}
}

// read files p as the model having read it whole.
func read(t *testing.T, rec *Recorder, p string) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"path": p})
	if _, err := rec.Execute(context.Background(), ReadFileName, raw); err != nil {
		t.Fatal(err)
	}
}

func TestEditFile_PathsAppliesOneEditToEachFile(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.go": "old()\n", "b.go": "x\nold()\n", "c.go": "old() old\n"})
	rec := NewRecorder()
	paths := []string{filepath.Join(dir, "a.go"), filepath.Join(dir, "b.go"), filepath.Join(dir, "c.go")}
	for _, p := range paths {
		read(t, rec, p)
	}
	out, err := editCall(t, rec, map[string]any{"paths": paths, "old_text": "old()", "new_text": "renamed()"})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range paths {
		if got := readBack(t, p); !strings.Contains(got, "renamed()") || strings.Contains(got, "old()") {
			t.Errorf("%s = %q", p, got)
		}
	}
	if !strings.HasPrefix(out, "Edited 3 file(s): 3 replacement(s)") {
		t.Errorf("result = %q", out)
	}
	// A literal edit lists no pairs: its replacement is what the call said.
	if strings.Contains(out, "Replaced:") {
		t.Errorf("a literal edit listed pairs: %q", out)
	}
}

func TestEditFile_PathsReportsAFileWithNoMatch(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.go": "old\n", "b.go": "nothing here\n"})
	rec := NewRecorder()
	paths := []string{filepath.Join(dir, "a.go"), filepath.Join(dir, "b.go")}
	for _, p := range paths {
		read(t, rec, p)
	}
	out, err := editCall(t, rec, map[string]any{"paths": paths, "old_text": "old", "new_text": "new"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "Edited 1 of 2 files") || !strings.Contains(out, "b.go: no match, left as it was") {
		t.Errorf("result = %q", out)
	}
	if got := readBack(t, paths[1]); got != "nothing here\n" {
		t.Errorf("the unmatched file changed: %q", got)
	}
	// None matching anywhere is an error: the call describes nothing.
	if _, err := editCall(t, rec, map[string]any{"paths": paths, "old_text": "absent", "new_text": "x"}); err == nil || !strings.Contains(err.Error(), "matches nothing in any of the 2 files") {
		t.Errorf("err = %v", err)
	}
}

// The staleness rule for a list: a file this session has read is admitted, a
// file a search named in this turn is admitted, and any other is refused by
// name — and with it the whole call, before anything is written.
func TestEditFile_PathsAdmitsOnlyReadOrSearchedFiles(t *testing.T) {
	dir := writeFiles(t, map[string]string{"read.go": "old\n", "found.go": "old\n", "unseen.go": "old\n"})
	readPath, foundPath, unseen := filepath.Join(dir, "read.go"), filepath.Join(dir, "found.go"), filepath.Join(dir, "unseen.go")
	rec := NewRecorder()
	read(t, rec, readPath)

	_, err := editCall(t, rec, map[string]any{"paths": []string{readPath, unseen}, "old_text": "old", "new_text": "new"})
	if err == nil || !strings.Contains(err.Error(), unseen+" has not been read in this session and no search in this turn named it") {
		t.Fatalf("err = %v", err)
	}
	if got := readBack(t, readPath); got != "old\n" {
		t.Fatalf("a refused list wrote its first file: %q", got)
	}

	// A search whose answer names found.go admits it; unseen.go is not in
	// that answer, since the search was put to found.go alone.
	raw, _ := json.Marshal(map[string]any{"pattern": "old", "path": foundPath, "case_sensitive": true})
	if _, err := rec.Execute(context.Background(), SearchName, raw); err != nil {
		t.Fatal(err)
	}
	if _, err := editCall(t, rec, map[string]any{"paths": []string{readPath, foundPath}, "old_text": "old", "new_text": "new"}); err != nil {
		t.Fatalf("a read file and a searched one: %v", err)
	}
	if _, err := editCall(t, rec, map[string]any{"paths": []string{unseen, foundPath}, "old_text": "new", "new_text": "newer"}); err == nil {
		t.Fatal("a file nothing read or searched was admitted")
	}

	// A new turn takes the search's word back; the read stays.
	rec.NewTurn()
	if _, err := editCall(t, rec, map[string]any{"paths": []string{readPath, unseen}, "old_text": "new", "new_text": "x"}); err == nil {
		t.Fatal("an unread file was admitted after a new turn")
	}
}

// A search from an earlier turn does not admit a file.
func TestEditFile_PathsForgetsASearchAtTheTurn(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.go": "old\n", "b.go": "old\n"})
	rec := NewRecorder()
	raw, _ := json.Marshal(map[string]any{"pattern": "old", "path": dir, "case_sensitive": true, "files_only": true})
	if _, err := rec.Execute(context.Background(), SearchName, raw); err != nil {
		t.Fatal(err)
	}
	rec.NewTurn()
	paths := []string{filepath.Join(dir, "a.go"), filepath.Join(dir, "b.go")}
	if _, err := editCall(t, rec, map[string]any{"paths": paths, "old_text": "old", "new_text": "new"}); err == nil {
		t.Fatal("a search from the turn before admitted the list")
	}
	if _, err := rec.Execute(context.Background(), SearchName, raw); err != nil {
		t.Fatal(err)
	}
	if _, err := editCall(t, rec, map[string]any{"paths": paths, "old_text": "old", "new_text": "new"}); err != nil {
		t.Fatalf("a files_only search in this turn: %v", err)
	}
}

func TestEditFile_PathsRefusals(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.go": "old\n", "[id].tsx": "old\n"})
	a := filepath.Join(dir, "a.go")
	for _, tc := range []struct {
		name string
		args map[string]any
		want string
	}{
		{"a glob", map[string]any{"paths": []string{filepath.Join(dir, "*.go")}, "old_text": "old", "new_text": "new"}, "is a pattern and not a file"},
		{"a file twice", map[string]any{"paths": []string{a, a}, "old_text": "old", "new_text": "new"}, "twice"},
		{"path and paths", map[string]any{"path": a, "paths": []string{a}, "old_text": "old", "new_text": "new"}, "either path or paths"},
		{"paths with edits", map[string]any{"paths": []string{a}, "edits": []map[string]any{{"old_text": "old", "new_text": "new"}}}, "one old_text/new_text"},
		{"paths with append", map[string]any{"paths": []string{a}, "append": "x"}, "one old_text/new_text"},
		{"neither", map[string]any{"old_text": "old", "new_text": "new"}, "path is required, or paths"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := editCall(t, NewRecorder(), tc.args)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
	// A file whose name holds glob syntax and exists is a file, not a glob.
	rec := NewRecorder()
	br := filepath.Join(dir, "[id].tsx")
	read(t, rec, br)
	if _, err := editCall(t, rec, map[string]any{"paths": []string{br}, "old_text": "old", "new_text": "new"}); err != nil {
		t.Fatalf("an existing bracketed file: %v", err)
	}
	// A list longer than the bound.
	var many []string
	for i := range maxEditPaths + 1 {
		many = append(many, filepath.Join(dir, fmt.Sprintf("f%d.go", i)))
	}
	if _, err := editCall(t, NewRecorder(), map[string]any{"paths": many, "old_text": "a", "new_text": "b"}); err == nil || !strings.Contains(err.Error(), "at most 50") {
		t.Fatalf("err = %v", err)
	}
}

// A write that fails part-way through a list leaves the files before it
// edited, the failed one and the rest untouched, and says which is which —
// as a result rather than an error, because the kept files are changes the
// readers of a run's writes must count; with nothing kept it is an error.
func TestEditFile_PathsAWriteFailingMidListSaysWhatHappened(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes a read-only file")
	}
	dir := writeFiles(t, map[string]string{"a.go": "old\n", "b.go": "old\n", "c.go": "old\n"})
	a, b, c := filepath.Join(dir, "a.go"), filepath.Join(dir, "b.go"), filepath.Join(dir, "c.go")
	rec := NewRecorder()
	for _, p := range []string{a, b, c} {
		read(t, rec, p)
	}
	if err := os.Chmod(b, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(b, 0o644) })
	msg, err := editCall(t, rec, map[string]any{"paths": []string{a, b, c}, "old_text": "old", "new_text": "new"})
	if err != nil {
		t.Fatalf("a list that kept a file answered with an error: %v", err)
	}
	for _, want := range []string{"Edited 1 of 3 files, then the write to " + b + " failed", "Already edited and kept: " + a, "not reached: " + c} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q lacks %q", msg, want)
		}
	}
	if readBack(t, a) != "new\n" || readBack(t, b) != "old\n" || readBack(t, c) != "old\n" {
		t.Errorf("files = %q %q %q", readBack(t, a), readBack(t, b), readBack(t, c))
	}
	// The failed file's record is gone, so it is refused until it is read
	// again; read, a list whose first write fails keeps nothing, and that is
	// an error.
	if _, err := editCall(t, rec, map[string]any{"paths": []string{b, c}, "old_text": "old", "new_text": "new"}); err == nil || !strings.Contains(err.Error(), "has not been read") {
		t.Errorf("the failed file was admitted unread: %v", err)
	}
	read(t, rec, b)
	if _, err := editCall(t, rec, map[string]any{"paths": []string{b, c}, "old_text": "old", "new_text": "new"}); err == nil || !strings.Contains(err.Error(), "the write to "+b+" failed") {
		t.Errorf("err = %v", err)
	}
}

// An edit is not a read: a file an edit changed without anybody reading it
// is still not admitted to a later list on that alone.
func TestEditFile_AnEditIsNotAReadForALaterList(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.go": "old one\n", "b.go": "old\n"})
	a, b := filepath.Join(dir, "a.go"), filepath.Join(dir, "b.go")
	rec := NewRecorder()
	read(t, rec, b)
	if _, err := editCall(t, rec, map[string]any{"path": a, "old_text": "one", "new_text": "two"}); err != nil {
		t.Fatal(err)
	}
	if _, err := editCall(t, rec, map[string]any{"paths": []string{a, b}, "old_text": "old", "new_text": "new"}); err == nil {
		t.Fatal("a file only edited was admitted to a list")
	}
	read(t, rec, a)
	if _, err := editCall(t, rec, map[string]any{"paths": []string{a, b}, "old_text": "old", "new_text": "new"}); err != nil {
		t.Fatalf("after a read: %v", err)
	}
}

// A file the model never read and only saw named by a search is recorded as
// read in part once the list edits it: a later overwrite still has to read it.
func TestEditFile_PathsDoesNotVouchForASearchedFileWhole(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.go": "old\nrest\n"})
	a := filepath.Join(dir, "a.go")
	rec := NewRecorder()
	raw, _ := json.Marshal(map[string]any{"pattern": "old", "path": dir, "case_sensitive": true})
	if _, err := rec.Execute(context.Background(), SearchName, raw); err != nil {
		t.Fatal(err)
	}
	if _, err := editCall(t, rec, map[string]any{"paths": []string{a}, "old_text": "old", "new_text": "new"}); err != nil {
		t.Fatal(err)
	}
	w, _ := json.Marshal(map[string]any{"path": a, "content": "x"})
	if _, err := rec.ExecuteMutating(context.Background(), WriteFileName, w); err == nil || !strings.Contains(err.Error(), "only read in part") {
		t.Fatalf("an overwrite of a file only searched: %v", err)
	}
}

// The list's preview is per file, in the order listed, with the counts the
// card draws.
func TestEditFile_PathsPreviewIsPerFile(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.go": "f(1) f(2)\n", "b.go": "none\n"})
	rec := NewRecorder()
	paths := []string{filepath.Join(dir, "a.go"), filepath.Join(dir, "b.go")}
	for _, p := range paths {
		read(t, rec, p)
	}
	raw, _ := json.Marshal(map[string]any{"paths": paths, "old_text": `f\((\d)\)`, "new_text": "g($1)", "regex": true, "replace_all": true})
	mut, err := rec.PreviewMutation(EditFileName, raw)
	if err != nil {
		t.Fatal(err)
	}
	if mut.Path != "" || len(mut.Files) != 2 || mut.Files[0].Replacements != 2 || mut.Files[1].Replacements != 0 {
		t.Fatalf("preview = %+v", mut)
	}
	if mut.Files[0].NewText != "g(1) g(2)\n" || !mut.Regex || mut.Pattern != `f\((\d)\)` {
		t.Errorf("preview = %+v", mut)
	}
	if got := WrittenPaths(EditFileName, string(raw)); len(got) != 2 || got[1] != paths[1] {
		t.Errorf("WrittenPaths = %q", got)
	}
}

// A path git runs anywhere in a list refuses the call.
func TestEditFile_PathsRefusesAGitProgramPathAnywhere(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.go": "old\n"})
	_, err := editCall(t, NewRecorder(), map[string]any{"paths": []string{filepath.Join(dir, "a.go"), filepath.Join(dir, ".git", "hooks", "pre-commit")}, "old_text": "old", "new_text": "new"})
	if !errors.Is(err, ErrGitProgramPath) {
		t.Fatalf("err = %v", err)
	}
}

// The activity row reads an edit across files as the files it names and is
// counted in the files it changed; an edit of one file reads as it did.
func TestEditFile_DescriberCountsFiles(t *testing.T) {
	d := Describers()[EditFileName]
	call := describe.Call{Name: EditFileName, Args: `{"paths":["a.go","b.go","c.go"],"old_text":"x","new_text":"y"}`,
		Result: "Edited 2 of 3 files: 4 replacement(s)\n  a.go: 3 replacement(s), file now 9 bytes (1 lines)\n  b.go: 1 replacement(s), file now 9 bytes (1 lines)\n  c.go: no match, left as it was"}
	if got := d.Subject(call); got != "a.go, b.go" {
		t.Errorf("subject = %q", got)
	}
	// Before the result, the row names every file the call listed.
	pending := call
	pending.Result = ""
	if got := d.Subject(pending); got != "a.go, b.go, c.go" {
		t.Errorf("pending subject = %q", got)
	}
	if n, noun, _ := d.Count(call); n != 2 || noun != "files" {
		t.Errorf("count = %d %s", n, noun)
	}
	one := describe.Call{Name: EditFileName, Args: `{"path":"a.go","old_text":"x","new_text":"y"}`, Arg: "a.go", Result: "Edited a.go: 1 replacement(s), file now 3 bytes (1 lines)"}
	if got := d.Subject(one); got != "a.go" {
		t.Errorf("one file's subject = %q", got)
	}
	if n, noun, _ := d.Count(one); n != 1 || noun != "line" {
		t.Errorf("one file's count = %d %s", n, noun)
	}
}
