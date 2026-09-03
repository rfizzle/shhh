package subagent

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

	wt, err := addWorktree(repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	worktree, childRoot, repoTop := wt.dir, wt.root, wt.repoTop
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
	if _, err := addWorktree(t.TempDir(), nil); err == nil {
		t.Fatal("expected an error outside a git repository")
	}
}

// The changeset records a child's patch by reading the real checkout either
// side of the apply, and the paths it reports are relative to the session's
// root — not to the repository top, which is a different place whenever the
// session was started from a subdirectory.
func TestPatchedFiles_SidesAndSessionRelativePaths(t *testing.T) {
	repoTop := t.TempDir()
	root := filepath.Join(repoTop, "sub")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	before := map[string]fileSide{
		"sub/edited.go": {text: "one\n", exists: true},
		"sub/added.go":  {},
		"gone.go":       {text: "bye\n", exists: true},
	}
	after := map[string]fileSide{
		"sub/edited.go": {text: "one\ntwo\n", exists: true},
		"sub/added.go":  {text: "new\n", exists: true},
		"gone.go":       {},
	}
	files := patchedFiles(root, repoTop, []string{"sub/edited.go", "sub/added.go", "gone.go"}, before, after)
	if len(files) != 3 {
		t.Fatalf("expected 3 files, got %d", len(files))
	}
	if files[0].Path != "edited.go" {
		t.Fatalf("a path under the session root should be relative to it, got %q", files[0].Path)
	}
	if files[0].Before != "one\n" || files[0].After != "one\ntwo\n" {
		t.Fatalf("both sides should survive, got %+v", files[0])
	}
	if files[1].BeforeExists || !files[1].AfterExists {
		t.Fatalf("a file the patch created should read as created, got %+v", files[1])
	}
	if !files[2].BeforeExists || files[2].AfterExists {
		t.Fatalf("a file the patch removed should read as removed, got %+v", files[2])
	}
	if files[2].Path != filepath.Join(repoTop, "gone.go") {
		t.Fatalf("a path outside the session root stays absolute, got %q", files[2].Path)
	}
}

// A session standing in a checkout it reached through a symlink is the
// ordinary case on macOS, where a TMPDIR lives under /var and /var is a link
// to /private/var. git answers `rev-parse --show-toplevel` with the link
// followed and the session's root keeps the name it was given, so the two
// have to be resolved before they can be compared: unresolved, the child is
// handed the repository top instead of its own subdirectory, and every file
// its patch touches is recorded as an absolute path to a repository that
// looks like somewhere else — which the run then cannot name among its own
// changes.
func TestWorktree_RootReachedThroughASymlink(t *testing.T) {
	repo := initTestRepo(t)
	link := filepath.Join(t.TempDir(), "workspace")
	if err := os.Symlink(repo, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}

	wt, err := addWorktree(filepath.Join(link, "sub"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer removeWorktree(wt.repoTop, wt.dir)
	if want := filepath.Join(wt.dir, "sub"); wt.root != want {
		t.Fatalf("the child should mirror the session's place in the repo: %q, want %q", wt.root, want)
	}

	files := patchedFiles(link, wt.repoTop, []string{"main.go"},
		map[string]fileSide{"main.go": {text: "one\n", exists: true}},
		map[string]fileSide{"main.go": {text: "two\n", exists: true}})
	if files[0].Path != "main.go" {
		t.Fatalf("a patched file should be named relative to the session root, got %q", files[0].Path)
	}
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

// A session that has been working for an hour has an hour of changes that no
// commit holds. A child branched from HEAD alone would read the code as it
// was before any of them, and every hunk it wrote over a file the session had
// already edited would clash on the way back. So the worktree starts from the
// parent's tree: the tracked edit is there to read, the untracked file the
// session created is there, and the patch that comes back is the child's own
// work applying cleanly on top of the parent's.
func TestWorktree_SeededFromTheParentsUncommittedWork(t *testing.T) {
	repo := initTestRepo(t)
	writeInto(t, repo, "main.go", "package main\n\nvar edited = true\n")
	writeInto(t, repo, "docs/notes.md", "the session wrote this\n")

	wt, err := addWorktree(repo, []string{"docs/notes.md"})
	if err != nil {
		t.Fatal(err)
	}
	defer removeWorktree(wt.repoTop, wt.dir)

	if wt.seeded != 2 {
		t.Fatalf("the child started from %d parent paths, want 2", wt.seeded)
	}
	if got := readFrom(t, wt.root, "main.go"); !strings.Contains(got, "var edited = true") {
		t.Fatalf("the parent's uncommitted edit is not in the worktree:\n%s", got)
	}
	if got := readFrom(t, wt.root, "docs/notes.md"); got != "the session wrote this\n" {
		t.Fatalf("the parent's untracked file is not in the worktree: %q", got)
	}

	writeInto(t, wt.root, "main.go", "package main\n\nvar edited = true\nvar byTheChild = true\n")
	patch, err := worktreePatch(wt.dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(patch, "byTheChild") {
		t.Fatalf("the child's own work is missing from its patch:\n%s", patch)
	}
	// The seed is the base, so nothing the parent had already done comes
	// back as if the child had done it.
	for _, unwanted := range []string{"docs/notes.md", "+var edited = true"} {
		if strings.Contains(patch, unwanted) {
			t.Fatalf("the patch hands back the parent's own work (%q):\n%s", unwanted, patch)
		}
	}

	if err := applyPatch(wt.repoTop, patch); err != nil {
		t.Fatalf("the child's patch should apply to the tree it was written against: %v", err)
	}
	main := readFrom(t, repo, "main.go")
	if !strings.Contains(main, "var edited = true") || !strings.Contains(main, "var byTheChild = true") {
		t.Fatalf("the apply should leave both the parent's edit and the child's:\n%s", main)
	}
}

// A parent with nothing uncommitted is seeded by doing nothing at all: the
// child stands on the parent's own commit, with a clean tree behind it and
// the same empty patch it would have produced before any of this existed.
func TestWorktree_CleanParentIsUntouched(t *testing.T) {
	repo := initTestRepo(t)
	head, err := gitOutput(repo, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}

	wt, err := addWorktree(repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer removeWorktree(wt.repoTop, wt.dir)

	if wt.seeded != 0 {
		t.Fatalf("a clean parent seeded %d paths, want none", wt.seeded)
	}
	childHead, err := gitOutput(wt.dir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if childHead != head {
		t.Fatalf("the child should stand on the parent's commit: %q, want %q", childHead, head)
	}
	status, err := gitOutput(wt.dir, "status", "--porcelain")
	if err != nil {
		t.Fatal(err)
	}
	if status != "" {
		t.Fatalf("a clean parent should leave a clean worktree, got:\n%s", status)
	}
	patch, err := worktreePatch(wt.dir)
	if err != nil {
		t.Fatal(err)
	}
	if patch != "" {
		t.Fatalf("a child that changed nothing should produce no patch:\n%s", patch)
	}
}

// The falsifiable half of the same claim: with nothing to carry, the seed
// runs no git in the worktree — so it can be pointed at a directory that does
// not exist and still succeed. Anything it ran there would fail instead.
func TestSeedWorktree_CleanParentRunsNothing(t *testing.T) {
	repo := initTestRepo(t)
	n, err := seedWorktree(repo, filepath.Join(t.TempDir(), "not-a-worktree"), nil)
	if err != nil {
		t.Fatalf("a clean parent should seed without touching the worktree: %v", err)
	}
	if n != 0 {
		t.Fatalf("seeded %d paths from a clean parent, want none", n)
	}
}

// An untracked path the session recorded and the person then deleted is not
// an error: there is simply nothing left to carry, and a spawn that failed
// over it would be a writer lost to a file nobody wanted.
func TestSeedWorktree_UntrackedFileThatHasSinceGone(t *testing.T) {
	repo := initTestRepo(t)
	wt, err := addWorktree(repo, []string{"deleted-since.md"})
	if err != nil {
		t.Fatal(err)
	}
	defer removeWorktree(wt.repoTop, wt.dir)
	if wt.seeded != 0 {
		t.Fatalf("a path that is not there seeded %d, want none", wt.seeded)
	}
}

// The session names its files from where it is standing; a worktree is a copy
// of the repository and can only hold what is under the top.
func TestRepoRelative(t *testing.T) {
	repoTop := t.TempDir()
	root := filepath.Join(repoTop, "sub")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	got := repoRelative(root, repoTop, []string{
		"notes.md",
		"notes.md",
		filepath.Join(root, "notes.md"),
		filepath.Join(repoTop, "top.md"),
		filepath.Join(t.TempDir(), "elsewhere.md"),
		"",
	})
	want := []string{"sub/notes.md", "top.md"}
	if len(got) != len(want) {
		t.Fatalf("repoRelative = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("repoRelative = %v, want %v", got, want)
		}
	}
}

// A file the session created and the person has since staged is in both
// halves of the seed: git reports it in the diff, and the session's record
// still remembers it as one git had never heard of. It is carried once and
// counted once, because the count is a number on screen that somebody may
// check against their own tree.
func TestSeedWorktree_AFileAddedSinceIsCountedOnce(t *testing.T) {
	repo := initTestRepo(t)
	writeInto(t, repo, "added.go", "package main\n")
	cmd := exec.Command("git", "-C", repo, "add", "added.go")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}

	wt, err := addWorktree(repo, []string{"added.go"})
	if err != nil {
		t.Fatal(err)
	}
	defer removeWorktree(wt.repoTop, wt.dir)
	if wt.seeded != 1 {
		t.Fatalf("a file in both halves of the seed counted %d, want 1", wt.seeded)
	}
	if got := readFrom(t, wt.root, "added.go"); got != "package main\n" {
		t.Fatalf("the file should still be carried once, got %q", got)
	}
}

// A symlink is left where it is rather than flattened into a copy of what it
// points at: following one out of the repository would carry in a file nobody
// named, and a dangling one has nothing to carry at all.
func TestSeedWorktree_SymlinkIsNotFollowed(t *testing.T) {
	repo := initTestRepo(t)
	target := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(target, []byte("not yours\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(repo, "link.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	wt, err := addWorktree(repo, []string{"link.txt"})
	if err != nil {
		t.Fatal(err)
	}
	defer removeWorktree(wt.repoTop, wt.dir)
	if wt.seeded != 0 {
		t.Fatalf("a symlink was carried: seeded %d, want 0", wt.seeded)
	}
	if _, err := os.Lstat(filepath.Join(wt.root, "link.txt")); !os.IsNotExist(err) {
		t.Fatalf("the worktree should not have the link: %v", err)
	}
}

// The parent's changeset is the only thing that will remember what a file
// was once a child's patch has deleted it, so the read taken either side of
// the apply carries the mode along with the content. Without it, taking the
// turn back writes the script out at the default mode and the next
// `./script.sh` fails with permission denied.
func TestPatchedFiles_ADeletedScriptKeepsItsExecuteBit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permissions")
	}
	repo := initTestRepo(t)
	writeInto(t, repo, "script.sh", "#!/bin/sh\necho hi\n")
	script := filepath.Join(repo, "script.sh")
	if !chmodCarried(t, script, 0o755) {
		t.Skip("this filesystem does not carry the execute bit")
	}

	wt, err := addWorktree(repo, []string{"script.sh"})
	if err != nil {
		t.Fatal(err)
	}
	defer removeWorktree(wt.repoTop, wt.dir)
	if err := os.Remove(filepath.Join(wt.root, "script.sh")); err != nil {
		t.Fatal(err)
	}
	patch, err := worktreePatch(wt.dir)
	if err != nil {
		t.Fatal(err)
	}

	touched := PatchFiles(patch)
	before := readSides(repo, touched)
	if err := applyPatch(repo, patch); err != nil {
		t.Fatalf("the child's deletion should apply to the parent: %v", err)
	}
	files := patchedFiles(repo, repo, touched, before, readSides(repo, touched))
	if len(files) != 1 {
		t.Fatalf("expected one patched file, got %+v", files)
	}
	if files[0].BeforeMode != 0o755 {
		t.Fatalf("the deleted script's mode is the record's only copy, got %o", files[0].BeforeMode)
	}
	if !files[0].BeforeExists || files[0].AfterExists {
		t.Fatalf("the file should read as deleted, got %+v", files[0])
	}
	if files[0].AfterMode != 0 {
		t.Fatalf("a file that is gone has no mode, got %o", files[0].AfterMode)
	}
}

// git carries a mode change as a header and no hunk at all, so a child that
// only made a script executable produces a patch whose two sides hold the
// same bytes. Reading the after side's mode is what keeps that from being
// indistinguishable from a file nothing touched.
func TestPatchedFiles_AModeOnlyPatchIsStillRead(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permissions")
	}
	repo := initTestRepo(t)
	writeInto(t, repo, "script.sh", "#!/bin/sh\necho hi\n")
	if !chmodCarried(t, filepath.Join(repo, "script.sh"), 0o644) {
		t.Skip("this filesystem does not carry the execute bit")
	}

	wt, err := addWorktree(repo, []string{"script.sh"})
	if err != nil {
		t.Fatal(err)
	}
	defer removeWorktree(wt.repoTop, wt.dir)
	if !chmodCarried(t, filepath.Join(wt.root, "script.sh"), 0o755) {
		t.Skip("this filesystem does not carry the execute bit")
	}
	patch, err := worktreePatch(wt.dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(patch, "new mode 100755") {
		t.Fatalf("git should carry the mode change:\n%s", patch)
	}

	touched := PatchFiles(patch)
	before := readSides(repo, touched)
	if err := applyPatch(repo, patch); err != nil {
		t.Fatalf("a mode-only patch should apply: %v", err)
	}
	files := patchedFiles(repo, repo, touched, before, readSides(repo, touched))
	if len(files) != 1 {
		t.Fatalf("a mode-only patch should still produce a record, got %+v", files)
	}
	if files[0].Before != files[0].After {
		t.Fatalf("a mode-only patch changed the content: %+v", files[0])
	}
	if files[0].BeforeMode != 0o644 || files[0].AfterMode != 0o755 {
		t.Fatalf("the mode change is the whole of the patch, got %o -> %o",
			files[0].BeforeMode, files[0].AfterMode)
	}
}

// chmodCarried sets a file's permissions and reports whether the filesystem
// kept them: a checkout on a mount that reports one mode for everything can
// say nothing about execute bits, and a test that asserted them there would
// fail for a reason that is not the code's.
func chmodCarried(t *testing.T, path string, mode os.FileMode) bool {
	t.Helper()
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm() == mode
}
