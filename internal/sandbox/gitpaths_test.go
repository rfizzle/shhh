package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/tools"
)

func containerArgvFor(t *testing.T, ws string) []string {
	t.Helper()
	eng := Engine{Name: "docker", Path: "/usr/bin/docker", OK: true}
	s, err := ContainerSpec{Image: "wolfi-base" + testDigest, Workspace: ws}.withDefaults()
	if err != nil {
		t.Fatal(err)
	}
	s.git = containerGit(ws)
	return createArgv(eng, "shhh-sbx-test", s)
}

// The file tools refuse a fixed list of entries under .git, and the container
// holds each of them read-only under its workspace mount, so a contained
// command and an in-process write meet the same list. A hooks directory that
// core.hooksPath puts outside the workspace is refused to the file tools and
// is not in the container at all: the one writable mount is the workspace, so
// nothing there can write it and nothing is bound for it.
func TestTheContainerHoldsEveryEntryTheFileToolsRefuse(t *testing.T) {
	_, ws := gitWorkspace(t)
	dotgit := filepath.Join(ws, ".git")
	outside := t.TempDir()
	gitRun(t, ws, "config", "core.hooksPath", outside)
	if err := os.WriteFile(filepath.Join(dotgit, "config.worktree"), []byte("[core]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	argv := containerArgvFor(t, ws)
	for _, entry := range tools.GitProgramEntries {
		if !tools.GitProgramPath(ws, filepath.Join(".git", entry)) {
			t.Errorf("the file tools do not refuse .git/%s", entry)
		}
		if entry != "config" && entry != "config.worktree" && !tools.GitProgramPath(ws, filepath.Join(".git", entry, "x")) {
			t.Errorf("the file tools do not refuse under .git/%s", entry)
		}
		if argvIndex(argv, "--volume", filepath.Join(dotgit, entry)+":"+workspaceMount+"/.git/"+entry+":ro") < 0 {
			t.Errorf(".git/%s is refused to the file tools but not read-only in the container:\n%v", entry, argv)
		}
	}
	if got := (Container{gitHeld: true}).GitStoreWords(); got != "read-only git hooks and config" {
		t.Errorf("GitStoreWords = %q", got)
	}
	if !tools.GitProgramPath(ws, filepath.Join(outside, "pre-commit")) {
		t.Error("the file tools do not refuse the hooks directory core.hooksPath names")
	}
	if strings.Contains(strings.Join(argv, " "), outside) {
		t.Errorf("a path outside the workspace is not in the container, yet the argv names it:\n%v", argv)
	}
}

// A linked worktree's .git file, which names the store, is refused to the
// file tools and bound read-only in the container; its store is outside the
// workspace and so is not in the container.
func TestTheContainerHoldsALinkedWorktreesDotGitFile(t *testing.T) {
	_, ws := gitWorkspace(t)
	gitRun(t, ws, "commit", "--allow-empty", "-q", "-m", "init")
	linked := filepath.Join(t.TempDir(), "linked")
	gitRun(t, ws, "worktree", "add", "-q", linked, "-b", "linked")
	linked = resolvedPath(t, linked)
	if !tools.GitProgramPath(linked, ".git") {
		t.Fatal("the file tools do not refuse a worktree's .git file")
	}
	argv := containerArgvFor(t, linked)
	if argvIndex(argv, "--volume", filepath.Join(linked, ".git")+":"+workspaceMount+"/.git:ro") < 0 {
		t.Errorf("the worktree's .git file is not bound read-only:\n%v", argv)
	}
	if strings.Contains(strings.Join(argv, " "), filepath.Join(ws, ".git")) {
		t.Errorf("the store outside the workspace is not in the container:\n%v", argv)
	}
}
