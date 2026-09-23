package subagent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// Regenerator is the project's declaration of which files it generates, and
// the way to generate them: the quality gate's runner, which reads the
// declaration from the trusted quality config and runs each generator the way
// it runs a check. Nil — no trusted config, or a surface with no gate —
// declares nothing, and every file is text.
type Regenerator interface {
	// Generated is the subset of these repository-relative paths some
	// generator claims.
	Generated(paths []string) []string
	// Regenerate runs, in dir, the generators these paths belong to and
	// answers with the command lines that ran.
	Regenerate(ctx context.Context, dir string, paths []string) ([]string, error)
}

// generatedPaths is which of a patch's paths are generated.
func generatedPaths(gen Regenerator, paths []string) []string {
	if gen == nil || len(paths) == 0 {
		return nil
	}
	return gen.Generated(paths)
}

// runGenerators is the one step every landing and every reseed takes for a
// generated path: run its generator in the tree it is landing in, and never
// apply or merge its bytes. It is one function so the rule has one place to
// be right in.
// See docs/capabilities/subagents.md#a-writer-starts-from-your-tree.
func runGenerators(ctx context.Context, gen Regenerator, dir string, paths []string) ([]string, error) {
	if gen == nil || len(paths) == 0 {
		return nil, nil
	}
	return gen.Regenerate(ctx, dir, paths)
}

// withoutFiles is a patch with the sections for these paths taken out.
func withoutFiles(patch string, drop []string) string {
	if len(drop) == 0 {
		return patch
	}
	skip := map[string]bool{}
	for _, p := range drop {
		skip[p] = true
	}
	var out strings.Builder
	keep := true
	for _, line := range strings.SplitAfter(patch, "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			keep = !skip[parseGitDiffPath(strings.TrimRight(line, "\n"))]
		}
		if keep {
			out.WriteString(line)
		}
	}
	if strings.TrimSpace(out.String()) == "" {
		return ""
	}
	return out.String()
}

// regenerateOver is what a patch that touches generated paths lands as: its
// other changes applied to a copy of the checkout as it stands, the
// generators for those paths run there, and the copy's whole difference from
// the checkout taken as one patch — the source change and every file its
// generators rewrote. The copy is seeded the way a writer's is, so it is the
// checkout's own text; the checkout itself is only read, and a generator that
// fails leaves it exactly as it was.
//
// root and untracked are what a writer's copy is made from (addWorktree).
// The copy is made and removed under the repository's worktree lock, and the
// generators run with the lock released.
func regenerateOver(ctx context.Context, gen Regenerator, root string, untracked []string, patch string, paths []string) (string, []string, error) {
	h, err := addWorktreeContext(ctx, root, untracked)
	if err != nil {
		return "", nil, err
	}
	defer removeWorktree(h.repoTop, h.dir)
	if strings.TrimSpace(patch) != "" {
		if err := applyPatch(h.dir, patch); err != nil {
			return "", nil, err
		}
	}
	ran, err := runGenerators(ctx, gen, h.dir, paths)
	if err != nil {
		return "", ran, err
	}
	out, err := worktreePatch(h.dir)
	if strings.TrimSpace(out) == "" {
		out = ""
	}
	return out, ran, err
}

// restoreFromBase puts these paths back to what the copy's base holds —
// removing one the base does not have — so a generator run over them starts
// from the checkout's text and not from a hand-merge of the writer's.
func restoreFromBase(worktree string, paths []string) error {
	var present []string
	for _, p := range paths {
		if _, err := gitOutput(worktree, "cat-file", "-e", "HEAD:"+p); err == nil {
			present = append(present, p)
			continue
		}
		if err := os.Remove(filepath.Join(worktree, filepath.FromSlash(p))); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if len(present) == 0 {
		return nil
	}
	_, err := runGit(worktree, append([]string{"checkout", "HEAD", "--"}, present...)...)
	return err
}
