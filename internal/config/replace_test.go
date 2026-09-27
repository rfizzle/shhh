package config

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// A write that fails after the text is on the disk and before the rename
// leaves the file that was there whole, and no temporary file beside it.
func TestReplaceFile_AFailedRenameLeavesTheOldFileWhole(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "steer.md")
	const old = "the wording as it was\n"
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	failed := errors.New("the power went")
	saved := renameFile
	t.Cleanup(func() { renameFile = saved })
	renameFile = func(from, to string) error {
		written, err := os.ReadFile(from)
		if err != nil || string(written) != "the new wording\n" {
			t.Errorf("the temporary file does not hold the text before the rename: %q, %v", written, err)
		}
		return failed
	}
	if err := ReplaceFile(path, "the new wording\n", 0o600); !errors.Is(err, failed) {
		t.Fatalf("the failed rename was not reported: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != old {
		t.Fatalf("the old file is not whole: %q, %v", got, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("the temporary file was left behind: %v", entries)
	}
}

// The text is synced before the rename and the directory after it, so the
// name never points at a file whose data is not yet on the disk. A file
// already there keeps its mode; a new one takes the one it is given.
func TestReplaceFile_SyncsTheTextBeforeTheRenameAndTheDirectoryAfter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keybindings.toml")
	savedSync, savedRename, savedDir := syncFile, renameFile, syncDir
	t.Cleanup(func() { syncFile, renameFile, syncDir = savedSync, savedRename, savedDir })
	var order []string
	syncFile = func(f *os.File) error { order = append(order, "sync file"); return savedSync(f) }
	renameFile = func(from, to string) error { order = append(order, "rename"); return savedRename(from, to) }
	syncDir = func(d string) error {
		if d != dir {
			t.Errorf("synced %s, not the file's directory", d)
		}
		order = append(order, "sync directory")
		return savedDir(d)
	}
	if err := ReplaceFile(path, "a\n", 0o644); err != nil {
		t.Fatal(err)
	}
	if want := []string{"sync file", "rename", "sync directory"}; !slices.Equal(order, want) {
		t.Fatalf("the steps ran as %v, want %v", order, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Errorf("a new file took %v, not the mode it was given", info.Mode().Perm())
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceFile(path, "b\n", 0o644); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("an existing file lost its mode: %v", info.Mode().Perm())
	}
}
