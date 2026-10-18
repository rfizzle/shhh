package cli

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/rfizzle/shhh/internal/quality"
)

func TestQuality_TheGateScopesToWhatTheTurnWrote(t *testing.T) {
	ws := t.TempDir()
	wrote := []string{"a/x.go", filepath.Join(ws, "b", "y.go"), "./c/z.go", "../elsewhere.go", filepath.Join(filepath.Dir(ws), "other", "w.go")}
	g := &quality.Runner{Workspace: ws}
	scopeGateToWrites(g, func() []string { return wrote })

	want := []string{"a/x.go", "b/y.go", "c/z.go"}
	if got := g.Changed(); !slices.Equal(got, want) {
		t.Errorf("Changed() = %q, want %q: paths are the workspace's, outside ones dropped", got, want)
	}

	// A turn that wrote nothing is scoped to the dirty tree. The workspace
	// here is no repository, so the tree names nothing; the point is that
	// the answer is the runner's default and not the written list.
	wrote = nil
	if got, dirty := g.Changed(), quality.DirtyChanged(ws); !slices.Equal(got, dirty) {
		t.Errorf("Changed() = %q for a turn that wrote nothing, want the dirty tree's %q", got, dirty)
	}

	scopeGateToWrites(nil, func() []string { return wrote })
	empty := &quality.Runner{Workspace: ws}
	scopeGateToWrites(empty, nil)
	if empty.Changed != nil {
		t.Error("a nil source must leave the dirty tree as the scope")
	}
}

func TestQuality_AWrittenPathIsComparedWithTheWorkspaceResolved(t *testing.T) {
	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	real, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	// One path as git names it (resolved) under a workspace given through a
	// link, and one as the link spells it under the resolved workspace.
	for _, tc := range []struct{ name, ws, path string }{
		{"resolved path, linked workspace", link, filepath.Join(real, "b", "y.go")},
		{"linked path, resolved workspace", real, filepath.Join(link, "b", "y.go")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := &quality.Runner{Workspace: tc.ws}
			scopeGateToWrites(g, func() []string { return []string{tc.path} })
			if got, want := g.Changed(), []string{"b/y.go"}; !slices.Equal(got, want) {
				t.Errorf("Changed() = %q, want %q", got, want)
			}
		})
	}
}
