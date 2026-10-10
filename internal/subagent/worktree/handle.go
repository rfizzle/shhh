package worktree

import (
	"context"
	"fmt"
	"strings"
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
	h WorktreeHandle
	// root and untracked are what the copy was made from, kept for the
	// copy a landing regenerates in, which is made the same way.
	root      string
	untracked []string
	gen       Regenerator
}

// NewWorktree makes one, seeded with the caller's uncommitted work: what `git
// diff HEAD` reports, plus the untracked paths the caller says are its own.
func NewWorktree(root string, untracked []string) (*Worktree, error) {
	h, err := addWorktree(root, untracked)
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
// The apply is all-or-nothing (ApplyPatch), so a lane whose work overlaps
// what another lane has already landed leaves the checkout exactly as it was
// and says so with an error, rather than half-applying and leaving conflict
// markers in files nobody has read.
//
// A patch the checkout has moved under since the copy was taken — an earlier
// lane landed in the same files, somewhere else in them — is merged three
// ways against the copy's base (MergeWorktree) and the merge is what lands,
// by the same plain apply. A merge that leaves a conflict region lands
// nothing and names the files.
//
// A path the project declares generated (UseGenerators) is neither applied
// nor merged: the rest of the patch is, in a copy of the checkout, its
// generators are run there, and what lands is that copy's difference
// (RegenerateOver). A generator that fails lands nothing.
func (w *Worktree) Land() ([]string, error) {
	landed, err := w.LandPatch()
	return PatchFiles(landed), err
}

// LandPatch is Land answering with the patch it applied rather than its file
// list: the writer's own where it applied plainly, the merge where the
// checkout had moved, and with the regenerated files where there were any —
// what the checkout moved by, which is what every other copy is owed.
func (w *Worktree) LandPatch() (string, error) { return w.land(false) }

// CheckoutMoved is a landing refused because the checkout holds a file the
// patch touches otherwise than the copy's base says: the checkout moved by a
// hand the copy never carried. A merge would land a tree nobody verified.
type CheckoutMoved struct{ Files []string }

func (e *CheckoutMoved) Error() string {
	return "the checkout moved since the copy's base, in " + PatchPaths(e.Files)
}

// LandPlain is LandPatch for a copy whose tree was verified and must land as
// verified: it lands only when the checkout still holds, for every file the
// patch touches, what the copy's base holds, so the landing is the plain
// apply of the copy's own patch and never a merge. A checkout that moved in
// such a file lands nothing and comes back as a *CheckoutMoved.
func (w *Worktree) LandPlain() (string, error) { return w.land(true) }

func (w *Worktree) land(plain bool) (string, error) {
	patch, err := WorktreePatch(w.h.Dir)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(patch) == "" {
		return "", nil
	}
	generated := GeneratedPaths(w.gen, PatchFiles(patch))
	offer := WithoutFiles(patch, generated)
	if offer != "" && plain {
		m, err := MergeWorktree(w.h.Dir, w.h.RepoTop, generated)
		switch {
		case err != nil:
			return "", err
		case len(m.Moved) > 0:
			return "", &CheckoutMoved{Files: m.Moved}
		}
		if err := CheckPatch(w.h.RepoTop, offer); err != nil {
			return "", err
		}
	} else if offer != "" {
		if applyErr := CheckPatch(w.h.RepoTop, offer); applyErr != nil {
			m, err := MergeWorktree(w.h.Dir, w.h.RepoTop, generated)
			switch {
			case err != nil:
				return "", fmt.Errorf("%w; merging it over the checkout: %v", applyErr, err)
			case len(m.Conflicts) > 0:
				return "", &MergeConflict{Files: m.Conflicts}
			}
			offer = m.Patch
		}
	}
	if len(generated) > 0 {
		if offer, _, err = RegenerateOver(context.Background(), w.gen, w.root, w.untracked, offer, generated); err != nil {
			return "", err
		}
	}
	if offer == "" {
		// The checkout already says everything the copy does.
		return "", nil
	}
	if err := ApplyPatch(w.h.RepoTop, offer); err != nil {
		return "", err
	}
	return offer, nil
}

// Remove tears the copy down. Best-effort, like every other teardown of one:
// a directory that is already gone must not stop a run from ending.
func (w *Worktree) Remove() { RemoveWorktree(w.h.RepoTop, w.h.Dir) }

// Reseed carries a patch that has landed in the checkout this copy was taken
// from into the copy, under whatever is being built here, and makes it part
// of the copy's base — so the next Land hands back only this copy's own work,
// measured against the checkout as it now stands. A patch that meets the work
// here is refused as a *ReseedCollision and the copy is left exactly as it
// was (ReseedWorktree). A generated path's generator that fails in the copy
// is the error too, with the landing itself carried and that path at the
// landed text.
func (w *Worktree) Reseed(patch string) error {
	if strings.TrimSpace(patch) == "" {
		return nil
	}
	regen, err := ReseedWorktree(context.Background(), w.h.Dir, patch, w.gen)
	if err != nil {
		return err
	}
	return regen.Failed
}

// ReseedMerging is Reseed for a patch that meets the work here, merged into
// it three ways (ReseedMerging). The caller holds the lock the copy is written
// under; landed and lane label the marks of a region no rule settles.
func (w *Worktree) ReseedMerging(patch, landed, lane string) (*Reconciliation, error) {
	return ReseedMerging(context.Background(), w.h.Dir, patch, w.gen, landed, lane)
}
