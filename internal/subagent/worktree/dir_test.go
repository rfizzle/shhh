package worktree

import (
	"path/filepath"
	"strings"
	"testing"
)

// A copy is made under agents.worktree_dir when it is set, so a host whose
// temporary directory is a small tmpfs can move them, and under the
// temporary directory again once it is not.
func TestWorktree_TheDirectoryIsConfigurable(t *testing.T) {
	repo := initTestRepo(t)
	base := filepath.Join(t.TempDir(), "copies")
	SetDir(base)
	t.Cleanup(func() { SetDir("") })

	wt, err := addWorktree(repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer RemoveWorktree(wt.RepoTop, wt.Dir)
	if filepath.Dir(wt.Dir) != base || !strings.HasPrefix(filepath.Base(wt.Dir), "shhh-agent-") {
		t.Fatalf("the copy is at %s, want a shhh-agent-* directory under %s", wt.Dir, base)
	}
	SetDir("")
	if got := Dir(); got != "" {
		t.Fatalf("an unset directory should be the system's: %q", got)
	}
}
