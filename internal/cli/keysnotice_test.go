package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestKeymapNoticeShowsOnceAndMarksItself(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	if !keymapNoticeDue() {
		t.Fatal("the first launch after a rebind must carry the notice")
	}
	if keymapNoticeDue() {
		t.Fatal("the notice showed twice; the marker did not take")
	}
}

func TestKeymapNoticeStaysQuietOnAFutureGeneration(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)
	path := filepath.Join(dir, "shhh", keymapMarkerFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	// A marker ahead of this binary — a newer shhh ran here — is not a
	// reason to re-announce an older rebind.
	if err := os.WriteFile(path, []byte("99\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if keymapNoticeDue() {
		t.Fatal("an already-seen generation was announced again")
	}
}
