package subagent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// initTestRepo builds a git repository with one committed file, skipping the
// test when git is unavailable.
func initTestRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-q", "-m", "init")
	return dir
}

// writeInto writes one file under dir, creating the directories above it.
func writeInto(t *testing.T, dir, rel, body string) {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// readFrom reads one file under dir, failing the test when it is not there.
func readFrom(t *testing.T, dir, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		t.Fatalf("reading %s: %v", rel, err)
	}
	return string(data)
}

// linkedWorktrees counts the copies of a checkout that exist beside it. The
// main checkout is one of the entries `git worktree list` prints, and is not
// one of the copies.
func linkedWorktrees(t *testing.T, repo string) int {
	t.Helper()
	// Listing while a writer's copy is being removed reads a half-removed
	// entry and fails. The worktree package's lock is its own, so the
	// listing asks again until the removal has finished rather than waiting
	// its turn.
	var out []byte
	var err error
	for try := 0; try < 100; try++ {
		out, err = exec.Command("git", "-C", repo, "worktree", "list", "--porcelain").CombinedOutput()
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("git worktree list: %v\n%s", err, out)
	}
	n := 0
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "worktree ") {
			n++
		}
	}
	return n - 1
}

// A writer whose copy of the repository cannot be made fails as a child, and
// its lane says why. The workspace is opened when the lane starts, which is
// long after the spawn that asked for it answered: a failure handed back as
// the spawn's return value would have nowhere left to go, and the fan-out
// would see a writer that never wrote anything and never said why.
func TestWriterWorkspaceFailureLandsOnTheChild(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	// Not a git repository, so there is nothing to make a worktree of and
	// nothing to seed one from.
	sup := New(context.Background(), Options{Root: t.TempDir(), NewEnv: hangingEnv()})
	t.Cleanup(sup.Close)

	if _, err := spawnRaw(sup, `{"role":"writer","task":"carry on"}`); err != nil {
		t.Fatalf("the spawn itself should answer: %v", err)
	}
	waitState(t, sup, "writer-1", StateFailed)
	st, _ := sup.Get("writer-1")
	if !strings.Contains(st.Detail, "isolated worktree") || !strings.Contains(st.Detail, "git repository") {
		t.Fatalf("the lane does not say why the writer never started: %q", st.Detail)
	}
	if st.Seeded != 0 {
		t.Fatalf("a writer that never got a worktree reports %d seeded paths", st.Seeded)
	}
}
