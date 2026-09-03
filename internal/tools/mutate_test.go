package tools

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
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

// A rewrite is a rewrite, not a re-creation: the mode argument the write
// carries applies only to a file it brings into existence, so overwriting a
// script leaves it executable.
func TestWriteFile_OverwriteKeepsTheFileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permissions")
	}
	tmp := t.TempDir()
	path := filepath.Join(tmp, "script.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho one\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The mode WriteFile is given is masked by the umask; the fixture has to
	// be exactly 0755 whatever the umask running the suite is.
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	readWholeFile(t, path)
	args, _ := json.Marshal(writeFileArgs{Path: path, Content: "#!/bin/sh\necho two\n"})

	if _, err := ExecuteMutating(WriteFileName, args); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("mode after the overwrite = %v, want 0755", info.Mode().Perm())
	}
}

// A file the tool creates gets the default mode. The exact bits are the
// process umask's business; what matters is that nothing here grants execute
// permission on its own.
func TestWriteFile_NewFileIsNotExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permissions")
	}
	path := filepath.Join(t.TempDir(), "new.sh")
	args, _ := json.Marshal(writeFileArgs{Path: path, Content: "#!/bin/sh\n"})

	if _, err := ExecuteMutating(WriteFileName, args); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 != 0 {
		t.Fatalf("a created file should not be executable, got %v", info.Mode().Perm())
	}
}

// An edit writes the file in place, which is what keeps its mode. Asserted so
// a future rewrite of the write path does not quietly take it away.
func TestEditFile_KeepsTheFileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permissions")
	}
	path := filepath.Join(t.TempDir(), "script.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho one\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The mode WriteFile is given is masked by the umask; the fixture has to
	// be exactly 0755 whatever the umask running the suite is.
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(editFileArgs{Path: path, OldText: "one", NewText: "two"})

	if _, err := ExecuteMutating(EditFileName, args); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("mode after the edit = %v, want 0755", info.Mode().Perm())
	}
}

// Three changes to one file are one call, one read and one write. The point
// of the array is that the model stops paying a round and an approval for
// each pair, so the assertion is that all three land together.
func TestEditFile_SeveralEditsInOneCall(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "code.go")
	must(t, os.WriteFile(path, []byte("alpha\nbeta\ngamma\n"), 0o644))

	result, err := ExecuteMutating(EditFileName, editArgs(t, editFileArgs{Path: path, Edits: []fileEdit{
		{OldText: "alpha", NewText: "one"},
		{OldText: "beta", NewText: "two"},
		{OldText: "gamma", NewText: "three"},
	}}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "one\ntwo\nthree\n" {
		t.Fatalf("file content = %q", data)
	}
	if !strings.Contains(result, "3 replacement") || !strings.Contains(result, "3 edits") {
		t.Errorf("result should report the replacements and the edits behind them: %q", result)
	}
}

// The list describes places, not steps. Every quote is matched against the
// file as it was read, so listing the last change first produces the same
// file — an incremental apply would make the second quote's meaning depend on
// what the first one wrote.
func TestEditFile_OrderOfTheEditsDoesNotMatter(t *testing.T) {
	tmp := t.TempDir()
	forward := filepath.Join(tmp, "forward.go")
	backward := filepath.Join(tmp, "backward.go")
	const before = "func A() {}\nfunc B() {}\nfunc C() {}\n"
	must(t, os.WriteFile(forward, []byte(before), 0o644))
	must(t, os.WriteFile(backward, []byte(before), 0o644))

	edits := []fileEdit{
		{OldText: "func A() {}", NewText: "func X() {}"},
		{OldText: "func B() {}", NewText: "func Y() {}"},
		{OldText: "func C() {}", NewText: "func Z() {}"},
	}
	if _, err := ExecuteMutating(EditFileName, editArgs(t, editFileArgs{Path: forward, Edits: edits})); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	reversed := []fileEdit{edits[2], edits[1], edits[0]}
	if _, err := ExecuteMutating(EditFileName, editArgs(t, editFileArgs{Path: backward, Edits: reversed})); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	a, _ := os.ReadFile(forward)
	b, _ := os.ReadFile(backward)
	if string(a) != string(b) {
		t.Fatalf("order changed the result:\n%q\n%q", a, b)
	}
	if string(a) != "func X() {}\nfunc Y() {}\nfunc Z() {}\n" {
		t.Fatalf("file content = %q", a)
	}
}

// Two edits claiming the same text have no order to be resolved by, so the
// call is refused — and the refusal names both, because the model cannot fix
// what it is not told.
func TestEditFile_OverlappingEditsAreRefusedNamingBoth(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "code.go")
	must(t, os.WriteFile(path, []byte("func Handle(w, r) {}\n"), 0o644))

	_, err := ExecuteMutating(EditFileName, editArgs(t, editFileArgs{Path: path, Edits: []fileEdit{
		{OldText: "func Handle(w, r) {}", NewText: "func Serve(w, r) {}"},
		{OldText: "Handle", NewText: "Serve"},
	}}))
	if err == nil {
		t.Fatal("expected the overlapping pair to be refused")
	}
	for _, want := range []string{"overlap", "edits 1 and 2", "func Handle(w, r) {}", `"Handle"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal should name both edits, missing %q: %v", want, err)
		}
	}
	data, _ := os.ReadFile(path)
	if string(data) != "func Handle(w, r) {}\n" {
		t.Fatal("an overlapping call must leave the file untouched")
	}
}

// All-or-nothing: one quote that does not match refuses the call, and the two
// that would have matched are not written. A file half changed is worse than
// a file not changed, because nothing on screen says which half.
func TestEditFile_OneBadQuoteWritesNothing(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "code.go")
	must(t, os.WriteFile(path, []byte("alpha\nbeta\ngamma\n"), 0o644))

	_, err := ExecuteMutating(EditFileName, editArgs(t, editFileArgs{Path: path, Edits: []fileEdit{
		{OldText: "alpha", NewText: "one"},
		{OldText: "delta", NewText: "four"},
		{OldText: "gamma", NewText: "three"},
	}}))
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected the unmatched quote to refuse the call, got %v", err)
	}
	// The refusal says which of the three it is about; "not found" alone
	// leaves the model re-reading a call it can already see.
	if !strings.Contains(err.Error(), "edit 2") {
		t.Errorf("refusal should name the edit it is about: %v", err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "alpha\nbeta\ngamma\n" {
		t.Fatalf("nothing may be written when one edit fails, got %q", data)
	}
}

// The line-number diagnosis is per edit, not per call: the model quoted one
// numbered line out of three and the message has to say which.
func TestEditFile_NamesTheLineNumberPrefixInsideAnArray(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "a.go")
	must(t, os.WriteFile(path, []byte("package a\n\nfunc F() {}\n"), 0o644))

	_, err := ExecuteMutating(EditFileName, editArgs(t, editFileArgs{Path: path, Edits: []fileEdit{
		{OldText: "package a", NewText: "package b"},
		{OldText: "3\tfunc F() {}", NewText: "func G() {}"},
	}}))
	if err == nil {
		t.Fatal("expected the numbered snippet not to match")
	}
	if !strings.Contains(err.Error(), "line-number prefixes") || !strings.Contains(err.Error(), "edit 2") {
		t.Errorf("error should name the prefix and the edit, got: %v", err)
	}
}

// The staleness fingerprint is one question about the file, so it covers
// every edit in the call: a file that moved refuses the whole array.
func TestEditFile_StaleFileRefusesTheWholeCall(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "code.go")
	must(t, os.WriteFile(path, []byte("alpha\nbeta\n"), 0o644))
	readWholeFile(t, path)
	// Something else writes the file between the read and the call.
	must(t, os.WriteFile(path, []byte("alpha\nbeta\ngamma\n"), 0o644))

	args := editArgs(t, editFileArgs{Path: path, Edits: []fileEdit{
		{OldText: "alpha", NewText: "one"},
		{OldText: "beta", NewText: "two"},
	}})
	var stale StaleError
	if _, err := ExecuteMutating(EditFileName, args); !errors.As(err, &stale) {
		t.Fatalf("expected a staleness refusal, got %v", err)
	}
	if _, err := PreviewMutation(EditFileName, args); !errors.As(err, &stale) {
		t.Fatalf("the preview refuses it too, got %v", err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "alpha\nbeta\ngamma\n" {
		t.Fatal("a stale call must leave the file untouched")
	}
}

// The preview and the write share one validator, so a card can never offer a
// change the write would then refuse.
func TestPreviewMutation_MultiEditMatchesTheWrite(t *testing.T) {
	tmp := t.TempDir()
	preview := filepath.Join(tmp, "preview.go")
	applied := filepath.Join(tmp, "applied.go")
	const before = "alpha\nbeta\ngamma\n"
	must(t, os.WriteFile(preview, []byte(before), 0o644))
	must(t, os.WriteFile(applied, []byte(before), 0o644))

	edits := []fileEdit{{OldText: "alpha", NewText: "one"}, {OldText: "gamma", NewText: "three"}}
	mut, err := PreviewMutation(EditFileName, editArgs(t, editFileArgs{Path: preview, Edits: edits}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mut.OldText != before {
		t.Errorf("preview should diff against the file as read: %q", mut.OldText)
	}
	if _, err := ExecuteMutating(EditFileName, editArgs(t, editFileArgs{Path: applied, Edits: edits})); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, _ := os.ReadFile(applied)
	if string(data) != mut.NewText {
		t.Fatalf("the preview promised %q, the write produced %q", mut.NewText, data)
	}
	if _, err := os.Stat(filepath.Join(tmp, "preview.go")); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(preview); string(data) != before {
		t.Fatal("a preview must not write anything")
	}
}

// A call carrying both spellings has two possible meanings and the tool picks
// neither: merging them would pick an order on the model's behalf.
func TestEditFile_RefusesBothSpellingsAtOnce(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "code.go")
	must(t, os.WriteFile(path, []byte("alpha\nbeta\n"), 0o644))

	_, err := ExecuteMutating(EditFileName, editArgs(t, editFileArgs{
		Path:    path,
		OldText: "alpha", NewText: "one",
		Edits: []fileEdit{{OldText: "beta", NewText: "two"}},
	}))
	if err == nil || !strings.Contains(err.Error(), "not both") {
		t.Fatalf("expected the mixed call to be refused, got %v", err)
	}
	if data, _ := os.ReadFile(path); string(data) != "alpha\nbeta\n" {
		t.Fatal("a refused call must leave the file untouched")
	}
}

// Neither spelling is a call with nothing to do, and the message has to name
// the array as well as the pair or the model only ever learns half the tool.
func TestEditFile_RefusesACallWithNoEdits(t *testing.T) {
	_, err := ExecuteMutating(EditFileName, json.RawMessage(`{"path":"x"}`))
	if err == nil || !strings.Contains(err.Error(), "old_text is required") || !strings.Contains(err.Error(), "edits") {
		t.Fatalf("expected an error naming both spellings, got %v", err)
	}
}

// replace_all is per edit, and its occurrences take part in the overlap check
// like any other match.
func TestEditFile_ReplaceAllInsideAnArray(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "code.go")
	must(t, os.WriteFile(path, []byte("x\ny\nx\n"), 0o644))

	result, err := ExecuteMutating(EditFileName, editArgs(t, editFileArgs{Path: path, Edits: []fileEdit{
		{OldText: "x", NewText: "a", ReplaceAll: true},
		{OldText: "y", NewText: "b"},
	}}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if data, _ := os.ReadFile(path); string(data) != "a\nb\na\n" {
		t.Fatalf("file content = %q", data)
	}
	if !strings.Contains(result, "3 replacement") {
		t.Errorf("result should count every occurrence: %q", result)
	}
}

// The array reaches the model as an array. A schema flattened to its
// top-level properties describes edits as a free-form value, and the model
// sends whatever it guesses.
func TestEditFile_SchemaDescribesTheEditsArray(t *testing.T) {
	var schema struct {
		Properties struct {
			Edits struct {
				Type  string `json:"type"`
				Items struct {
					Type       string                     `json:"type"`
					Properties map[string]json.RawMessage `json:"properties"`
					Required   []string                   `json:"required"`
				} `json:"items"`
			} `json:"edits"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(editFile.Tool.Parameters, &schema); err != nil {
		t.Fatalf("the edit_file schema must be valid JSON: %v", err)
	}
	edits := schema.Properties.Edits
	if edits.Type != "array" || edits.Items.Type != "object" {
		t.Fatalf("edits should be an array of objects, got %q of %q", edits.Type, edits.Items.Type)
	}
	for _, field := range []string{"old_text", "new_text", "replace_all"} {
		if _, ok := edits.Items.Properties[field]; !ok {
			t.Errorf("an edits entry should describe %s", field)
		}
	}
	if !slices.Equal(edits.Items.Required, []string{"old_text", "new_text"}) {
		t.Errorf("an edits entry requires both texts, got %v", edits.Items.Required)
	}
	// Only the path is required now: either spelling of the change is valid,
	// so requiring old_text would refuse every array call before it is read.
	if !slices.Equal(schema.Required, []string{"path"}) {
		t.Errorf("edit_file should require the path alone, got %v", schema.Required)
	}
}

func TestSnippet(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"beta", `"beta"`},
		{"\tindented", `"\tindented"`},
		{"first\nsecond", `"first"…`},
		{strings.Repeat("a", 41), `"` + strings.Repeat("a", 40) + `"…`},
	} {
		if got := snippet(tc.in); got != tc.want {
			t.Errorf("snippet(%q) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

// The overlap is found by walking the file, so the pair can be met in the
// opposite order to the one the model wrote. The refusal still names them the
// way the call listed them: "edits 2 and 1" reads as though the order meant
// something.
func TestEditFile_OverlapNamesTheEditsInTheOrderTheyWereGiven(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "code.go")
	must(t, os.WriteFile(path, []byte("alpha\nbeta\n"), 0o644))

	// The second edit's text starts earlier in the file than the first's.
	_, err := ExecuteMutating(EditFileName, editArgs(t, editFileArgs{Path: path, Edits: []fileEdit{
		{OldText: "eta", NewText: "reak"},
		{OldText: "beta", NewText: "delta"},
	}}))
	if err == nil {
		t.Fatal("expected the overlapping pair to be refused")
	}
	if !strings.Contains(err.Error(), "edits 1 and 2") {
		t.Errorf("the refusal should name them in call order: %v", err)
	}
}
