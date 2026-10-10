package changeset

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/rfizzle/shhh/internal/project"
)

func TestTracker_TrackedUntrackedAndOutsideARepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "test"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("tracked.go", "package main\n")
	write("scratch.txt", "notes\n")
	if out, err := exec.Command("git", "-C", dir, "add", "tracked.go").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v: %s", err, out)
	}

	tr := NewTracker(dir)
	if !tr.Repo() {
		t.Fatal("the tracker should have found the work tree")
	}
	if got := tr.Track("tracked.go"); got != TrackTracked {
		t.Fatalf("expected tracked, got %v", got)
	}
	if got := tr.Track("scratch.txt"); got != TrackUntracked {
		t.Fatalf("expected untracked, got %v", got)
	}

	// A directory that was never a repository answers unknown, and the
	// session keeps recording everything else.
	plain := NewTracker(t.TempDir())
	if plain.Repo() {
		t.Fatal("a bare temp dir is not a work tree")
	}
	if got := plain.Track("anything.go"); got != TrackUnknown {
		t.Fatalf("outside a repository the answer is unknown, got %v", got)
	}
	var nilTracker *Tracker
	if got := nilTracker.Track("anything.go"); got != TrackUnknown || nilTracker.Repo() {
		t.Fatalf("a nil tracker answers unknown, got %v", got)
	}
}

// A tracker built from the session's reading of its checkout answers what
// that reading found, and asks git nothing to find it.
func TestTracker_FromTheCheckoutTheSessionRead(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if !NewTrackerFrom(dir, project.ReadCheckout(dir)).Repo() {
		t.Fatal("the reading found a work tree and the tracker did not")
	}
	if NewTrackerFrom(t.TempDir(), project.Checkout{}).Repo() {
		t.Fatal("a reading outside a work tree made a tracker that found one")
	}
}
