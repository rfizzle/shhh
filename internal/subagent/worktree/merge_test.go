package worktree

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mergeBase is the committed file the merge tests work on: a line the
// checkout moves ("// c") one unchanged line away from the line a writer
// changes ("var y = 0"). Close enough that the writer's hunk carries the
// moved line as context, so its patch no longer applies as written; far
// enough apart that a line merge has no conflict to report.
const mergeBase = "package main\n\nvar x = 0\n\n// a\n// b\n// c\n\nvar y = 0\n"

func mergeRepo(t *testing.T) string {
	t.Helper()
	repo := initTestRepo(t)
	writeInto(t, repo, "main.go", mergeBase)
	if _, err := RunGit(repo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-am", "base"); err != nil {
		t.Fatal(err)
	}
	return repo
}

// moved is the checkout's change to mergeBase, and ours the writer's.
func moved(text string) string { return strings.Replace(text, "// c", "// C", 1) }
func ours(text string) string  { return strings.Replace(text, "var y = 0", "var y = 2", 1) }

// A patch whose context the checkout moved is merged over the checkout, and
// the merge is a patch against the checkout as it now stands: it applies
// plainly there and carries the writer's change and nothing of the
// checkout's.
func TestMergeWorktree_APatchOverAMovedFileMergesAgainstTheCheckout(t *testing.T) {
	repo := mergeRepo(t)
	h, err := addWorktree(repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer RemoveWorktree(h.RepoTop, h.Dir)
	writeInto(t, h.Root, "main.go", ours(mergeBase))
	patch, err := WorktreePatch(h.Dir)
	if err != nil {
		t.Fatal(err)
	}
	writeInto(t, repo, "main.go", moved(mergeBase))
	if CheckPatch(repo, patch) == nil {
		t.Fatal("the fixture should move the checkout under the writer's hunk")
	}

	m, err := MergeWorktree(h.Dir, repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Conflicts) != 0 || len(m.Moved) != 1 || m.Moved[0] != "main.go" {
		t.Fatalf("one moved file and no conflict, got moved %v conflicts %v", m.Moved, m.Conflicts)
	}
	if !strings.Contains(m.Patch, "+var y = 2") || !strings.Contains(m.Patch, " // C") || strings.Contains(m.Patch, "-// c") {
		t.Fatalf("the merge should be the writer's change over the checkout's text:\n%s", m.Patch)
	}
	if got := readFrom(t, repo, "main.go"); got != moved(mergeBase) {
		t.Fatalf("merging must not touch the checkout:\n%s", got)
	}
	if err := ApplyPatch(repo, m.Patch); err != nil {
		t.Fatalf("the merge should apply plainly to the checkout: %v", err)
	}
	if got := readFrom(t, repo, "main.go"); got != ours(moved(mergeBase)) {
		t.Fatalf("both changes should stand:\n%s", got)
	}
}

// The files the checkout did not move come through the merge as the writer
// left them, whatever the change was: a new file in a new directory, a
// deletion, and a mode flipped with no byte changed.
func TestMergeWorktree_UnmovedFilesAreTheWritersOutright(t *testing.T) {
	repo := mergeRepo(t)
	writeInto(t, repo, "gone.txt", "bye\n")
	writeInto(t, repo, "run.sh", "#!/bin/sh\n")
	if _, err := RunGit(repo, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := RunGit(repo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "more"); err != nil {
		t.Fatal(err)
	}
	h, err := addWorktree(repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer RemoveWorktree(h.RepoTop, h.Dir)
	writeInto(t, h.Root, "main.go", ours(mergeBase))
	writeInto(t, h.Root, "pkg/deep/new.go", "package deep\n")
	if err := os.Remove(filepath.Join(h.Root, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(h.Root, "run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := WorktreePatch(h.Dir); err != nil {
		t.Fatal(err)
	}
	writeInto(t, repo, "main.go", moved(mergeBase))

	m, err := MergeWorktree(h.Dir, repo, nil)
	if err != nil || len(m.Conflicts) != 0 || len(m.Moved) != 1 {
		t.Fatalf("one moved file and no conflict, got moved %v conflicts %v err %v", m.Moved, m.Conflicts, err)
	}
	if err := ApplyPatch(repo, m.Patch); err != nil {
		t.Fatalf("the merge should apply plainly:\n%s\n%v", m.Patch, err)
	}
	if got := readFrom(t, repo, "main.go"); got != ours(moved(mergeBase)) {
		t.Fatalf("main.go should hold both changes:\n%s", got)
	}
	if got := readFrom(t, repo, "pkg/deep/new.go"); got != "package deep\n" {
		t.Fatalf("the new file should land: %q", got)
	}
	if _, err := os.Stat(filepath.Join(repo, "gone.txt")); !os.IsNotExist(err) {
		t.Fatalf("the deleted file should be gone: %v", err)
	}
	if info, err := os.Stat(filepath.Join(repo, "run.sh")); err != nil || info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("run.sh should be executable: %v %v", info, err)
	}
}

// Two changes to the same line are a conflict region, and a conflict is not
// merged: no patch, the file named, the checkout untouched.
func TestMergeWorktree_TheSameLinesAreAConflictAndNothingIsMerged(t *testing.T) {
	repo := mergeRepo(t)
	h, err := addWorktree(repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer RemoveWorktree(h.RepoTop, h.Dir)
	writeInto(t, h.Root, "main.go", ours(mergeBase))
	writeInto(t, h.Root, "other.go", "package main\n")
	if _, err := WorktreePatch(h.Dir); err != nil {
		t.Fatal(err)
	}
	theirs := strings.Replace(mergeBase, "var y = 0", "var y = 1", 1)
	writeInto(t, repo, "main.go", theirs)

	m, err := MergeWorktree(h.Dir, repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	if m.Patch != "" || len(m.Conflicts) != 1 || m.Conflicts[0] != "main.go" {
		t.Fatalf("a conflict should name main.go and merge nothing, got conflicts %v patch %q", m.Conflicts, m.Patch)
	}
	if got := readFrom(t, repo, "main.go"); got != theirs {
		t.Fatalf("a conflict must leave the checkout as it was:\n%s", got)
	}
}
