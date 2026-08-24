package subagent

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/diff"
)

const samplePatch = `diff --git a/main.go b/main.go
index 1111111..2222222 100644
--- a/main.go
+++ b/main.go
@@ -1,3 +1,4 @@
 package main
-func old() {}
+func new() {}
+func extra() {}
diff --git a/util.go b/util.go
new file mode 100644
index 0000000..3333333
--- /dev/null
+++ b/util.go
@@ -0,0 +1,2 @@
+package main
+var x = 1
`

func TestPatchHunks(t *testing.T) {
	hunks, files := PatchHunks(samplePatch)
	if files != 2 {
		t.Fatalf("files = %d, want 2", files)
	}
	adds, dels := diff.Stats(hunks)
	if adds != 4 || dels != 1 {
		t.Fatalf("stats = +%d −%d, want +4 −1", adds, dels)
	}
	var labels []string
	for _, h := range hunks {
		for _, l := range h.Lines {
			if strings.HasPrefix(l.Text, "─ ") {
				labels = append(labels, l.Text)
			}
		}
	}
	if len(labels) != 2 || !strings.Contains(labels[0], "main.go") || !strings.Contains(labels[1], "util.go") {
		t.Fatalf("file labels = %v", labels)
	}
	if hunks[0].OldStart != 1 || hunks[0].NewStart != 1 {
		t.Fatalf("hunk numbers not parsed: %+v", hunks[0])
	}
}

func TestPatchHunks_Binary(t *testing.T) {
	patch := "diff --git a/img.png b/img.png\nBinary files a/img.png and b/img.png differ\n"
	hunks, files := PatchHunks(patch)
	if files != 1 || len(hunks) != 1 {
		t.Fatalf("hunks=%d files=%d", len(hunks), files)
	}
	if !strings.Contains(hunks[0].Lines[0].Text, "binary change") {
		t.Fatalf("missing binary note: %+v", hunks[0].Lines)
	}
}

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

func TestWorktreeLifecycle(t *testing.T) {
	repo := initTestRepo(t)

	worktree, childRoot, repoTop, err := addWorktree(repo)
	if err != nil {
		t.Fatal(err)
	}
	defer removeWorktree(repoTop, worktree)

	// Edit a tracked file and add a new one inside the worktree.
	if err := os.WriteFile(filepath.Join(childRoot, "main.go"), []byte("package main\n\nvar changed = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(childRoot, "new.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	patch, err := worktreePatch(worktree)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(patch, "changed = true") || !strings.Contains(patch, "new.go") {
		t.Fatalf("patch missing changes:\n%s", patch)
	}

	// The real checkout is untouched until the patch applies.
	before, _ := os.ReadFile(filepath.Join(repo, "main.go"))
	if strings.Contains(string(before), "changed") {
		t.Fatal("worktree edit leaked into the real checkout")
	}
	if err := applyPatch(repoTop, patch); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(repo, "main.go"))
	if !strings.Contains(string(after), "changed = true") {
		t.Fatal("patch did not apply to the real checkout")
	}
	if _, err := os.Stat(filepath.Join(repo, "new.go")); err != nil {
		t.Fatal("new file missing after apply")
	}

	removeWorktree(repoTop, worktree)
	if _, err := os.Stat(worktree); !os.IsNotExist(err) {
		t.Fatal("worktree not removed")
	}
}

func TestAddWorktreeNeedsGitRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	if _, _, _, err := addWorktree(t.TempDir()); err == nil {
		t.Fatal("expected an error outside a git repository")
	}
}
