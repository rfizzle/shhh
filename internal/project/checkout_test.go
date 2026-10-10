package project

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// checkoutRepo is a repository with one commit, and a git runner bound to it
// whose configuration is pinned so a developer's own never reaches the test.
func checkoutRepo(t *testing.T) (string, func(dir string, args ...string)) {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not on PATH")
	}
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command(git, append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	run(dir, "init", "-q", "--initial-branch=trunk")
	writeFiles(t, dir, map[string]string{"a.txt": "one\n", "sub/b.txt": "two\n"})
	run(dir, "add", ".")
	run(dir, "commit", "-q", "-m", "first")
	return dir, run
}

// Read from a directory below the root, the reading is the whole work tree's:
// its root, and every changed path keyed from that root rather than from
// where it was asked.
func TestReadCheckout_FromASubdirectoryKeysFromTheRoot(t *testing.T) {
	dir, _ := checkoutRepo(t)
	writeFiles(t, dir, map[string]string{"a.txt": "edited\n", "new.txt": "x\n"})

	co := ReadCheckout(filepath.Join(dir, "sub"))
	if !co.Read() || co.Top != dir {
		t.Fatalf("top = %q read = %v (err %v), want %q", co.Top, co.Read(), co.Err, dir)
	}
	if co.Branch != "trunk" || co.Detached || len(co.Head) != 40 {
		t.Fatalf("header = branch %q detached %v head %q", co.Branch, co.Detached, co.Head)
	}
	if len(co.Status) != 2 || co.Status["a.txt"] != ".M" || co.Status["new.txt"] != "??" {
		t.Fatalf("status = %v, want a.txt modified and new.txt untracked", co.Status)
	}
}

// A linked worktree's .git is a file, and its reading is its own: its own
// root, its own branch, and none of the main checkout's changes.
func TestReadCheckout_ALinkedWorktreeIsItsOwnCheckout(t *testing.T) {
	dir, run := checkoutRepo(t)
	linked := filepath.Join(filepath.Dir(dir), filepath.Base(dir)+"-linked")
	run(dir, "worktree", "add", "-q", "-b", "side", linked)
	t.Cleanup(func() { _ = os.RemoveAll(linked) })
	writeFiles(t, dir, map[string]string{"main-only.txt": "x\n"})

	co := ReadCheckout(linked)
	if !co.Read() || co.Top != linked || co.Branch != "side" {
		t.Fatalf("top = %q branch = %q (err %v), want %q on side", co.Top, co.Branch, co.Err, linked)
	}
	if len(co.Status) != 0 {
		t.Fatalf("the main checkout's change reached the linked one: %v", co.Status)
	}
}

// A repository with no commit yet is a work tree to every reader that asks
// whether there is one, and still no repository to the survey, which has no
// branch to name and no commit a resumed conversation could be compared with.
func TestSurvey_ARepositoryWithNoCommitIsNotStatedAsOne(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not on PATH")
	}
	dir := t.TempDir()
	if out, err := exec.Command(git, "-C", dir, "init", "-q", "--initial-branch=trunk").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	writeFiles(t, dir, map[string]string{"a.txt": "one\n"})

	info := Survey(dir)
	if !info.Checkout.Repo() {
		t.Fatal("the reading found no work tree in an initialised repository")
	}
	if info.Repo || info.Branch != "" || info.Dirty != 0 || info.Head != "" {
		t.Fatalf("survey = repo %v branch %q dirty %d head %q, want no repository stated",
			info.Repo, info.Branch, info.Dirty, info.Head)
	}
}

// Outside a work tree the reading is empty and says so by its root.
func TestReadCheckout_OutsideAWorkTreeIsEmpty(t *testing.T) {
	co := ReadCheckout(t.TempDir())
	if co.Repo() || co.Read() || co.Status != nil {
		t.Fatalf("outside a work tree: %+v", co)
	}
}
