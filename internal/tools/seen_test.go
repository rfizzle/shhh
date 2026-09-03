package tools

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readWholeFile puts the file through read_file the way a session would, so a
// test that is not about the guard can get past it.
func readWholeFile(t *testing.T, path string) {
	t.Helper()
	args, _ := json.Marshal(readFileArgs{Path: path})
	if _, err := Execute(ReadFileName, args); err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
}

func writeArgs(t *testing.T, path, content string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(writeFileArgs{Path: path, Content: content})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// seed writes a file directly, standing in for whatever put it there before
// the session started.
func seed(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// The clobber this exists for: a full overwrite of a file the model never
// looked at.
func TestWriteFileRefusesAFileItNeverRead(t *testing.T) {
	path := seed(t, t.TempDir(), "config.yaml", "port: 8080\n")

	_, err := ExecuteMutating(WriteFileName, writeArgs(t, path, "port: 9090\n"))
	if err == nil {
		t.Fatal("overwriting an unread file should be refused")
	}
	if !strings.Contains(err.Error(), "read_file") {
		t.Errorf("the refusal must say what to do about it: %v", err)
	}
	if data, _ := os.ReadFile(path); string(data) != "port: 8080\n" {
		t.Errorf("the file must be untouched, got %q", data)
	}
}

func TestWriteFileAllowsANewFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fresh.txt")
	if _, err := ExecuteMutating(WriteFileName, writeArgs(t, path, "hello\n")); err != nil {
		t.Fatalf("a file that does not exist yet has nothing to clobber: %v", err)
	}
}

func TestWriteFileRefusesAFileReadOnlyInPart(t *testing.T) {
	path := seed(t, t.TempDir(), "long.txt", "a\nb\nc\nd\n")
	args, _ := json.Marshal(readFileArgs{Path: path, StartLine: 1, EndLine: 2})
	if _, err := Execute(ReadFileName, args); err != nil {
		t.Fatal(err)
	}

	_, err := ExecuteMutating(WriteFileName, writeArgs(t, path, "z\n"))
	if err == nil {
		t.Fatal("replacing a file from a windowed read writes over what was never seen")
	}
	if !strings.Contains(err.Error(), "in part") {
		t.Errorf("the refusal should name the reason: %v", err)
	}
}

// The race a sub-agent or a background process creates: the file moved
// between the read and the write.
func TestWriteFileRefusesAFileThatChangedSinceItWasRead(t *testing.T) {
	dir := t.TempDir()
	path := seed(t, dir, "shared.txt", "one\n")
	readWholeFile(t, path)
	seed(t, dir, "shared.txt", "somebody else's work\n")

	_, err := ExecuteMutating(WriteFileName, writeArgs(t, path, "mine\n"))
	if err == nil {
		t.Fatal("a file that moved under the model should be refused")
	}
	if !strings.Contains(err.Error(), "changed since") {
		t.Errorf("the refusal should name the reason: %v", err)
	}
	if data, _ := os.ReadFile(path); string(data) != "somebody else's work\n" {
		t.Errorf("the other writer's work must survive, got %q", data)
	}
}

// edit_file carries its own evidence, so it is not made to read first — a
// snippet that matches exactly and uniquely came from somewhere.
func TestEditFileNeedsNoPriorRead(t *testing.T) {
	path := seed(t, t.TempDir(), "code.go", "alpha\nbeta\n")

	if _, err := ExecuteMutating(EditFileName, editArgs(t, editFileArgs{Path: path, OldText: "beta", NewText: "gamma"})); err != nil {
		t.Fatalf("an exact unique match is evidence enough: %v", err)
	}
	if data, _ := os.ReadFile(path); string(data) != "alpha\ngamma\n" {
		t.Errorf("edit did not apply, got %q", data)
	}
}

func TestEditFileRefusesAFileThatChangedSinceItWasRead(t *testing.T) {
	dir := t.TempDir()
	path := seed(t, dir, "code.go", "alpha\nbeta\n")
	readWholeFile(t, path)
	seed(t, dir, "code.go", "alpha\nbeta\ndelta\n")

	_, err := ExecuteMutating(EditFileName, editArgs(t, editFileArgs{Path: path, OldText: "beta", NewText: "gamma"}))
	if err == nil {
		t.Fatal("the edit was written against a file that has since moved")
	}
	if !strings.Contains(err.Error(), "changed since") {
		t.Errorf("the refusal should name the reason: %v", err)
	}
}

// A session must be able to keep working on a file it just changed without
// being sent back to read it every time.
func TestAMutationRefreshesTheRecordItWroteAgainst(t *testing.T) {
	path := seed(t, t.TempDir(), "code.go", "alpha\nbeta\n")
	readWholeFile(t, path)

	if _, err := ExecuteMutating(EditFileName, editArgs(t, editFileArgs{Path: path, OldText: "beta", NewText: "gamma"})); err != nil {
		t.Fatal(err)
	}
	if _, err := ExecuteMutating(EditFileName, editArgs(t, editFileArgs{Path: path, OldText: "alpha", NewText: "omega"})); err != nil {
		t.Fatalf("a second edit on top of the first should not be stale: %v", err)
	}
	if _, err := ExecuteMutating(WriteFileName, writeArgs(t, path, "rewritten\n")); err != nil {
		t.Fatalf("the file was read in full and then written by us: %v", err)
	}
}

// Validation matches execution, so a call that would be refused is refused
// before a person is asked to approve it.
func TestPreviewRefusesWhatExecutionWould(t *testing.T) {
	dir := t.TempDir()
	path := seed(t, dir, "config.yaml", "port: 8080\n")

	if _, err := PreviewMutation(WriteFileName, writeArgs(t, path, "port: 9090\n")); err == nil {
		t.Fatal("the preview should refuse an overwrite of an unread file")
	}

	readWholeFile(t, path)
	seed(t, dir, "config.yaml", "port: 7070\n")
	if _, err := PreviewMutation(EditFileName, editArgs(t, editFileArgs{Path: path, OldText: "8080", NewText: "9090"})); err == nil {
		t.Fatal("the preview should refuse an edit against a file that moved")
	}
}

// The same file reached by two paths is one file, so a read through one must
// satisfy a write through the other.
func TestTheRecordFollowsTheFileNotThePathSpelling(t *testing.T) {
	dir := t.TempDir()
	path := seed(t, dir, "code.go", "alpha\n")
	readWholeFile(t, path)

	indirect := filepath.Join(dir, ".", "code.go")
	if _, err := ExecuteMutating(WriteFileName, writeArgs(t, indirect, "beta\n")); err != nil {
		t.Fatalf("a differently spelled path is the same file: %v", err)
	}
}

// A staleness refusal is the one a surface has to be able to pick out: the
// person can act on "somebody else changed this file" and cannot act on a
// schema violation, so the two must not arrive as one kind of error.
func TestStalenessIsItsOwnErrorAndTheOtherRefusalsAreNot(t *testing.T) {
	dir := t.TempDir()
	path := seed(t, dir, "shared.txt", "one\n")
	readWholeFile(t, path)
	seed(t, dir, "shared.txt", "somebody else's work\n")

	_, err := PreviewMutation(WriteFileName, writeArgs(t, path, "mine\n"))
	var stale StaleError
	if !errors.As(err, &stale) {
		t.Fatalf("a file that moved should be a StaleError, got %T: %v", err, err)
	}
	if stale.Path != path {
		t.Errorf("the refusal must name the file: got %q, want %q", stale.Path, path)
	}
	if want := path + " has changed since it was read; read_file it again and rebase this change on what it says now"; stale.Error() != want {
		t.Errorf("the sentence the model reads changed:\n got %q\nwant %q", stale.Error(), want)
	}
	if got, want := stale.Skipped("shared.txt"), "skipped · shared.txt changed since it was read"; got != want {
		t.Errorf("the row a person reads:\n got %q\nwant %q", got, want)
	}
	if got := stale.Skipped(""); got != stale.Skipped(path) {
		t.Errorf("without a display path the row falls back to the one recorded, got %q", got)
	}

	// The other two refusals in this file are the model's own to fix, and a
	// surface must not report either of them as somebody else's work.
	unread := seed(t, dir, "unread.txt", "x\n")
	if _, err := PreviewMutation(WriteFileName, writeArgs(t, unread, "y\n")); errors.As(err, &stale) {
		t.Errorf("a file never read is not stale: %v", err)
	}
	partial := seed(t, dir, "partial.txt", "a\nb\nc\n")
	window, _ := json.Marshal(readFileArgs{Path: partial, StartLine: 1, EndLine: 2})
	if _, err := Execute(ReadFileName, window); err != nil {
		t.Fatal(err)
	}
	if _, err := PreviewMutation(WriteFileName, writeArgs(t, partial, "z\n")); errors.As(err, &stale) {
		t.Errorf("a file read only in part is not stale: %v", err)
	}
}
