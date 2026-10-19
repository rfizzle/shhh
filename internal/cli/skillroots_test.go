package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rfizzle/agentcore/skill"
)

func TestProjectRootsWalkToRepoRoot(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(repo, "packages", "one")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	roots := projectSkillRoots(pkg)
	want := []string{
		filepath.Join(pkg, ".shhh", "skills"),
		filepath.Join(pkg, ".agents", "skills"),
		filepath.Join(pkg, ".claude", "skills"),
		filepath.Join(repo, "packages", "one", ".shhh", "skills"),
	}
	if len(roots) != 9 {
		t.Fatalf("got %d roots, want 9 (three directories, three names each): %v", len(roots), roots)
	}
	for i := 0; i < 3; i++ {
		if roots[i].Path != want[i] || roots[i].Scope != skill.ScopeProject {
			t.Errorf("roots[%d] = %+v, want %s", i, roots[i], want[i])
		}
	}
	if roots[8].Path != filepath.Join(repo, ".claude", "skills") {
		t.Errorf("last root = %s; the repository root should close the walk", roots[8].Path)
	}

	// Outside a repository, only cwd is scanned.
	loose := t.TempDir()
	if got := projectSkillRoots(loose); len(got) != 3 {
		t.Errorf("outside a repo got %d roots, want 3: %v", len(got), got)
	}
}
