package cli

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestKeymapNoticeIsNotShownOnAFirstRun(t *testing.T) {
	dir := t.TempDir()

	got := keymapLaunchIn(dir, false)
	if got.noticeDue || !got.firstRun {
		t.Fatalf("a fresh data directory = %+v, want a first run with no notice", got)
	}
	if got := keymapLaunchIn(dir, true); got.noticeDue || got.firstRun {
		t.Fatalf("the launch after a first run = %+v, want nothing", got)
	}
}

func TestKeymapNoticeShowsOnceBesideAStoreWithNoMarker(t *testing.T) {
	dir := t.TempDir()

	// An install from before the marker existed: the store is there, the
	// marker is not, and the hand learned the old keys.
	got := keymapLaunchIn(dir, true)
	if !got.noticeDue || got.firstRun {
		t.Fatalf("a store with no marker = %+v, want the notice", got)
	}
	if keymapLaunchIn(dir, true).noticeDue {
		t.Fatal("the notice showed twice; the marker did not take")
	}
}

func TestKeymapNoticeShowsOnceForAMarkerOneGenerationBehind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, keymapMarkerFile)
	if err := os.WriteFile(path, []byte(strconv.Itoa(keymapGeneration-1)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A marker behind is an upgrade across a rebind, whatever the store says.
	got := keymapLaunchIn(dir, false)
	if !got.noticeDue || got.firstRun {
		t.Fatalf("a marker one generation behind = %+v, want the notice", got)
	}
	if keymapLaunchIn(dir, true).noticeDue {
		t.Fatal("the notice showed twice; the marker did not take")
	}
}

func TestKeymapNoticeStaysQuietOnAFutureGeneration(t *testing.T) {
	dir := t.TempDir()
	// A marker ahead of this binary — a newer shhh ran here — is not a
	// reason to re-announce an older rebind.
	if err := os.WriteFile(filepath.Join(dir, keymapMarkerFile), []byte("99\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if keymapLaunchIn(dir, true).noticeDue {
		t.Fatal("an already-seen generation was announced again")
	}
}
