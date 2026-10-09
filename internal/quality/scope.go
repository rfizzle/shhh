package quality

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/hostgit"
)

// WholeModule is the pattern a scoped check takes where the changed files
// name no package to run it over.
const WholeModule = "./..."

// NotRunScoped is why an unscoped check did not run: a scoped check failed
// and ended the run before it.
const NotRunScoped = "a scoped check failed first"

// maxScopedDirs bounds how many directories are handed to the package lister.
// Past it the change is wide enough that the whole module is the honest scope.
const maxScopedDirs = 64

// scope is what a run's scoped checks are filled with: the argument form of
// each package, and the words a result prints for them.
type scope struct {
	pkgs  []string
	label []string
}

// wholeModuleScope is the scope where nothing narrower is known.
func wholeModuleScope() scope {
	return scope{pkgs: []string{WholeModule}, label: []string{WholeModule}}
}

// expand fills the placeholder in args. An argument that is exactly the
// placeholder becomes one argument per package, because there is no shell to
// split a joined one; an argument that holds it among other text takes the
// packages joined by a space.
func (s scope) expand(args []string) []string {
	out := make([]string, 0, len(args)+len(s.pkgs))
	for _, a := range args {
		switch {
		case a == PackagesPlaceholder:
			out = append(out, s.pkgs...)
		case strings.Contains(a, PackagesPlaceholder):
			out = append(out, strings.ReplaceAll(a, PackagesPlaceholder, strings.Join(s.pkgs, " ")))
		default:
			out = append(out, a)
		}
	}
	return out
}

// changedScope is the scope of the workspace's changed files: the Go
// packages they belong to, listed through the same containment a check runs
// in, and the whole module wherever that cannot be said. It never fails; a
// scope that cannot be narrowed is the wider one, which only costs time.
//
// The files are the dirty tree's unless the runner was given a source. A
// deleted package, a lister that refuses and a list too long to be a scope
// all read as the whole module, because a scope that quietly named fewer
// packages than the change touched would be the gate looking away.
// See docs/capabilities/testing.md#how-do-quality-gates-stay-repeatable.
func (r *Runner) changedScope(ctx context.Context, timeout time.Duration) scope {
	files := r.changed()
	dirs := packageDirs(r.Workspace, files)
	if len(dirs) == 0 || len(dirs) > maxScopedDirs {
		return wholeModuleScope()
	}
	args := []string{"list", "-f", "{{.ImportPath}}"}
	for _, d := range dirs {
		args = append(args, "./"+d)
	}
	path, err := exec.LookPath("go")
	if err != nil {
		return wholeModuleScope()
	}
	argv := append([]string{path}, args...)
	if r.Wrap != nil {
		if argv, err = r.Wrap(argv, false); err != nil {
			return wholeModuleScope()
		}
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var out bytes.Buffer
	cmd := exec.CommandContext(cctx, argv[0], argv[1:]...)
	cmd.Dir = r.Workspace
	cmd.Stdout = &out
	if cmd.Run() != nil {
		return wholeModuleScope()
	}
	pkgs := strings.Fields(out.String())
	if len(pkgs) == 0 {
		return wholeModuleScope()
	}
	return scope{pkgs: pkgs, label: dirs}
}

// changed is the paths the run is scoped to, from the root of the repository.
func (r *Runner) changed() []string {
	if r.Changed != nil {
		return r.Changed()
	}
	root, paths := DirtyPaths(r.Workspace)
	if root == "" {
		return nil
	}
	// Porcelain names paths from the repository's top level; the workspace
	// may sit below it.
	rel, err := filepath.Rel(root, r.Workspace)
	if err != nil || rel == "." {
		return paths
	}
	rel = filepath.ToSlash(rel) + "/"
	var out []string
	for _, p := range paths {
		if after, ok := strings.CutPrefix(p, rel); ok {
			out = append(out, after)
		}
	}
	return out
}

// DirtyPaths is the repository's top level and the paths its working tree
// differs from HEAD in, as git names them: the changed files, when nothing
// narrower than the tree is known. It is empty outside a repository.
func DirtyPaths(workspace string) (root string, paths []string) {
	out, err := hostgit.Output(context.Background(), workspace, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", nil
	}
	status, err := hostgit.Output(context.Background(), workspace, "status", "--porcelain", "-z", "-uall")
	if err != nil {
		return "", nil
	}
	return strings.TrimSpace(out), porcelainPaths(status)
}

// packageDirs is the directories, relative to the workspace, of the Go files
// among the paths that still exist and could hold a package. A file that is
// not Go source names no package: the unscoped checks that follow still run
// everything, so a scope only has to be right about what it runs first.
func packageDirs(workspace string, files []string) []string {
	var dirs []string
	for _, f := range files {
		f = filepath.ToSlash(f)
		if !strings.HasSuffix(f, ".go") || slices.Contains(strings.Split(f, "/"), "testdata") {
			continue
		}
		d := filepath.ToSlash(filepath.Dir(f))
		if d == "." || d == "" {
			d = "."
		}
		if info, err := os.Stat(filepath.Join(workspace, filepath.FromSlash(d))); err != nil || !info.IsDir() {
			continue
		}
		if d != "." {
			d = strings.TrimPrefix(d, "./")
		}
		dirs = append(dirs, d)
	}
	slices.Sort(dirs)
	dirs = slices.Compact(dirs)
	return dirs
}
