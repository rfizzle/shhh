package subagent

import (
	"context"
	"fmt"
	"strings"

	wtree "github.com/rfizzle/shhh/internal/subagent/worktree"
)

// Worktree is one isolated copy of a checkout for a caller outside this
// package: the backlog runner's fan-out, which has no supervisor to spawn
// writers from and the same need for a lane that cannot see, or clash with,
// what its neighbours are writing.
//
// It is this package's own worktree under an exported name rather than a
// second implementation of one. The delicate half is the seeding — a lane
// started from HEAD alone writes its patch against text the checkout no
// longer has, and every hunk over a file the caller had already edited
// clashes when it lands — and two copies of that reasoning come apart at the
// first fix to either.
// See docs/capabilities/subagents.md#a-writer-starts-from-your-tree.
type Worktree struct {
	h wtree.WorktreeHandle
	// root and untracked are what the copy was made from, kept for the
	// copy a landing regenerates in, which is made the same way.
	root      string
	untracked []string
	gen       Regenerator
}

// NewWorktree makes one, seeded with the caller's uncommitted work: what `git
// diff HEAD` reports, plus the untracked paths the caller says are its own.
func NewWorktree(root string, untracked []string) (*Worktree, error) {
	h, err := wtree.AddWorktree(root, untracked)
	if err != nil {
		return nil, err
	}
	return &Worktree{h: h, root: root, untracked: untracked}, nil
}

// UseGenerators gives the copy the project's generated paths: Land and
// Reseed then regenerate those rather than merge or apply their bytes. A
// copy given none treats every file as text.
func (w *Worktree) UseGenerators(gen Regenerator) { w.gen = gen }

// Root is where the work happens: the copy's own version of the directory
// the caller was standing in, which is what keeps a lane's relative paths
// meaning what they mean in the checkout.
func (w *Worktree) Root() string { return w.h.Root }

// Land applies what was built here to the checkout it was copied from and
// answers with the repository-relative paths the patch touched. A patch with
// nothing in it lands nothing and names nothing, which is the answer for a
// lane that changed no files.
//
// The apply is all-or-nothing (worktree.ApplyPatch), so a lane whose work overlaps
// what another lane has already landed leaves the checkout exactly as it was
// and says so with an error, rather than half-applying and leaving conflict
// markers in files nobody has read.
//
// A patch the checkout has moved under since the copy was taken — an earlier
// lane landed in the same files, somewhere else in them — is merged three
// ways against the copy's base (worktree.MergeWorktree) and the merge is what lands,
// by the same plain apply. A merge that leaves a conflict region lands
// nothing and names the files.
//
// A path the project declares generated (UseGenerators) is neither applied
// nor merged: the rest of the patch is, in a copy of the checkout, its
// generators are run there, and what lands is that copy's difference
// (worktree.RegenerateOver). A generator that fails lands nothing.
func (w *Worktree) Land() ([]string, error) {
	landed, err := w.LandPatch()
	return wtree.PatchFiles(landed), err
}

// LandPatch is Land answering with the patch it applied rather than its file
// list: the writer's own where it applied plainly, the merge where the
// checkout had moved, and with the regenerated files where there were any —
// what the checkout moved by, which is what every other copy is owed.
func (w *Worktree) LandPatch() (string, error) {
	patch, err := wtree.WorktreePatch(w.h.Dir)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(patch) == "" {
		return "", nil
	}
	generated := wtree.GeneratedPaths(w.gen, wtree.PatchFiles(patch))
	offer := wtree.WithoutFiles(patch, generated)
	if offer != "" {
		if applyErr := wtree.CheckPatch(w.h.RepoTop, offer); applyErr != nil {
			m, err := wtree.MergeWorktree(w.h.Dir, w.h.RepoTop, generated)
			switch {
			case err != nil:
				return "", fmt.Errorf("%w; merging it over the checkout: %v", applyErr, err)
			case len(m.Conflicts) > 0:
				return "", &wtree.MergeConflict{Files: m.Conflicts}
			}
			offer = m.Patch
		}
	}
	if len(generated) > 0 {
		if offer, _, err = wtree.RegenerateOver(context.Background(), w.gen, w.root, w.untracked, offer, generated); err != nil {
			return "", err
		}
	}
	if offer == "" {
		// The checkout already says everything the copy does.
		return "", nil
	}
	if err := wtree.ApplyPatch(w.h.RepoTop, offer); err != nil {
		return "", err
	}
	return offer, nil
}

// Remove tears the copy down. Best-effort, like every other teardown of one:
// a directory that is already gone must not stop a run from ending.
func (w *Worktree) Remove() { wtree.RemoveWorktree(w.h.RepoTop, w.h.Dir) }

// Reseed carries a patch that has landed in the checkout this copy was taken
// from into the copy, under whatever is being built here, and makes it part
// of the copy's base — so the next Land hands back only this copy's own work,
// measured against the checkout as it now stands. A patch that meets the work
// here is refused as a *ReseedCollision and the copy is left exactly as it
// was (worktree.ReseedWorktree). A generated path's generator that fails in the copy
// is the error too, with the landing itself carried and that path at the
// landed text.
func (w *Worktree) Reseed(patch string) error {
	if strings.TrimSpace(patch) == "" {
		return nil
	}
	regen, err := wtree.ReseedWorktree(context.Background(), w.h.Dir, patch, w.gen)
	if err != nil {
		return err
	}
	return regen.Failed
}
