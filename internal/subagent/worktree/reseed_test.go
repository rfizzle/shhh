package worktree

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// reseedBase is the committed file both writers work on: two lines far enough
// apart that a change to one is not context for a change to the other.
const reseedBase = "package main\n\nvar x = 0\n\n// one\n// two\n// three\n// four\n// five\n// six\n\nvar y = 0\n"

// reseedRepo is a repository whose one committed file is reseedBase.
func reseedRepo(t *testing.T) string {
	t.Helper()
	repo := initTestRepo(t)
	writeInto(t, repo, "main.go", reseedBase)
	if _, err := RunGit(repo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-am", "base"); err != nil {
		t.Fatal(err)
	}
	return repo
}

// A landed patch carried into a copy moves the copy's base by exactly that
// patch: the file has the landed text, the writer's own work is still there
// on top of it, and the patch the copy hands back is the writer's alone.
func TestReseedWorktree_TheLandedChangeJoinsTheBase(t *testing.T) {
	repo := reseedRepo(t)
	other, err := addWorktree(repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer RemoveWorktree(other.RepoTop, other.Dir)
	mine, err := addWorktree(repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer RemoveWorktree(mine.RepoTop, mine.Dir)

	writeInto(t, other.Root, "main.go", strings.Replace(reseedBase, "var x = 0", "var x = 1", 1))
	landed, err := WorktreePatch(other.Dir)
	if err != nil {
		t.Fatal(err)
	}
	writeInto(t, mine.Root, "two.go", "package main\n\nvar two = 2\n")

	if _, err := ReseedWorktree(context.Background(), mine.Dir, landed, nil); err != nil {
		t.Fatalf("a patch over a file the writer has not touched should carry: %v", err)
	}
	if got := readFrom(t, mine.Root, "main.go"); !strings.Contains(got, "var x = 1") {
		t.Fatalf("the landed change is not in the copy:\n%s", got)
	}
	if got := readFrom(t, mine.Root, "two.go"); got != "package main\n\nvar two = 2\n" {
		t.Fatalf("the writer's own work did not survive the reseed: %q", got)
	}
	patch, err := WorktreePatch(mine.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(patch, "two.go") || strings.Contains(patch, "var x = 1") {
		t.Fatalf("the copy's patch should be its own work alone:\n%s", patch)
	}
}

// A landed patch that meets the writer's own work is not forced: every file
// in the copy and its base are exactly as they were, and the refusal names
// the file the two met on.
func TestReseedWorktree_ACollisionLeavesTheCopyAsItWas(t *testing.T) {
	repo := reseedRepo(t)
	other, err := addWorktree(repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer RemoveWorktree(other.RepoTop, other.Dir)
	mine, err := addWorktree(repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer RemoveWorktree(mine.RepoTop, mine.Dir)

	writeInto(t, other.Root, "main.go", strings.Replace(reseedBase, "var x = 0", "var x = 1", 1))
	landed, err := WorktreePatch(other.Dir)
	if err != nil {
		t.Fatal(err)
	}
	mineText := strings.Replace(reseedBase, "var x = 0", "var x = 2", 1)
	writeInto(t, mine.Root, "main.go", mineText)
	head, _ := GitOutput(mine.Dir, "rev-parse", "HEAD")

	_, err = ReseedWorktree(context.Background(), mine.Dir, landed, nil)
	var clash *ReseedCollision
	if !errors.As(err, &clash) {
		t.Fatalf("a patch over the writer's own line should be refused as a collision, got %v", err)
	}
	if len(clash.Files) != 1 || clash.Files[0] != "main.go" {
		t.Fatalf("the collision should name main.go, got %v", clash.Files)
	}
	if got := readFrom(t, mine.Root, "main.go"); got != mineText {
		t.Fatalf("the copy was changed by a refused reseed:\n%s", got)
	}
	if after, _ := GitOutput(mine.Dir, "rev-parse", "HEAD"); after != head {
		t.Fatalf("the copy's base moved under a refused reseed: %s → %s", head, after)
	}
}
