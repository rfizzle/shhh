package run

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/todo"
)

// What the run may stage is read out of git's own status, and the backlog is
// never part of it.
func TestPorcelainPaths(t *testing.T) {
	status := " M internal/agent/loop.go\n?? new.go\nR  old.go -> moved.go\n M .shhh/todo/a-one.md\n?? .shhh/run/x/evidence/ev-1.dat\n"
	got := PorcelainPaths(status)
	if strings.Join(got, ",") != "internal/agent/loop.go,moved.go,new.go" {
		t.Fatalf("porcelain paths = %v", got)
	}
	if strings.Contains(strings.Join(got, ","), todo.StateDir) {
		t.Fatalf("neither the backlog nor the run's own spool may be staged: %v", got)
	}
}

// The rule a commit rests on: somebody else's edits stay out, and the run's
// own work on a file they had already touched stays in.
func TestContents_ClaimsTheRunsOwnWorkOnAFileThatWasAlreadyDirty(t *testing.T) {
	got := Contents(
		[]string{"held.go"},
		[]string{"shared.go"},
		[]string{"held.go", "shared.go", "stranger.go", "new.go", ".shhh/todo/x.md"},
		[]string{"shared.go", "stranger.go"},
	)
	if strings.Join(got, ",") != "held.go,shared.go,new.go" {
		t.Fatalf("contents = %v", got)
	}
}

// A run whose stages are separate processes reports what it wrote, and a run
// that writes through a shell command reports nothing — so the tree is read
// as well, and what the tree adds is sorted, so two identical runs produce
// the same list.
func TestContents_TakesTheTreesWordAndSortsIt(t *testing.T) {
	got := Contents(nil, nil, []string{"z.go", "a.go", "m.go"}, nil)
	if strings.Join(got, ",") != "a.go,m.go,z.go" {
		t.Fatalf("contents = %v", got)
	}
}

// Every touched file is represented, however many there are: a reader who is
// handed the last two files whole and never told about the other eighteen
// signs off on a tenth of the change.
func TestBoundDiff_EveryFileIsPresent(t *testing.T) {
	var files []string
	for i := range 20 {
		body := fmt.Sprintf("diff --git a/f%02d.go b/f%02d.go\n", i, i)
		for line := range 100 {
			body += fmt.Sprintf("+line %d of f%02d\n", line, i)
		}
		files = append(files, body)
	}
	out := BoundDiff(files, ReviewDiffLines, ReviewFileFloor)
	for i := range 20 {
		if !strings.Contains(out, fmt.Sprintf("diff --git a/f%02d.go", i)) {
			t.Fatalf("file %d is missing from the bounded diff", i)
		}
	}
	// And a file that was cut says so, rather than reading as a change that
	// simply ended there.
	if n := strings.Count(out, "more lines of this file's diff"); n != 20 {
		t.Fatalf("%d files said they were cut, want 20", n)
	}
	if strings.Contains(out, "line 99 of f00") {
		t.Fatal("a file was handed over whole while others were cut")
	}
}

func TestBoundDiff_SmallChangesArriveWhole(t *testing.T) {
	out := BoundDiff([]string{"diff --git a/a.go b/a.go\n+one\n"}, ReviewDiffLines, ReviewFileFloor)
	if out != "diff --git a/a.go b/a.go\n+one\n" || strings.Contains(out, "not shown") {
		t.Fatalf("bounded diff = %q", out)
	}
	if BoundDiff(nil, ReviewDiffLines, ReviewFileFloor) != "" {
		t.Fatal("no files is no diff")
	}
}

// The spool is under the repository, where a stage's own process can be
// pointed at it — so it has to be invisible to git, or the run would watch
// its own bookkeeping change the tree it is checking.
func TestMakeSpool_IsInvisibleToGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "t@e"},
		{"config", "user.name", "t"}, {"commit", "-q", "--allow-empty", "-m", "seed"}} {
		if out, code := git(root, args...); code != 0 {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	dir, err := MakeSpool(root, "do-it")
	if err != nil {
		t.Fatal(err)
	}
	if dir != EvidenceDir(root, "do-it") {
		t.Fatalf("spool = %q", dir)
	}
	if err := os.WriteFile(filepath.Join(dir, "ev-1.dat"), []byte("output"), 0o600); err != nil {
		t.Fatal(err)
	}
	if paths := DirtyPaths(root); len(paths) != 0 {
		t.Fatalf("the spool must not show as the run's own change: %v", paths)
	}
	ClearSpool(root, "do-it")
	if _, err := os.Stat(RunStateDir(root, "do-it")); !os.IsNotExist(err) {
		t.Fatalf("the spool outlived the run: %v", err)
	}
}
