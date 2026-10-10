package worktree

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rfizzle/shhh/internal/hostgit"
)

// evidenceBytes bounds the regions a reconciliation quotes.
const evidenceBytes = 60000

// quotedContext is how many lines either side of a region its quote shows.
const quotedContext = 3

// Reconciliation is what carrying a landed patch into a copy over the work
// the copy has did, file by file.
type Reconciliation struct {
	// Settled is the files the merge wrote with no region left: git settled
	// them, or a rule settled every region.
	Settled []string
	// Unsettled is the files that still hold a marked region, or are not a
	// line merge at all (added or removed on a side, binary, a link). The
	// first are written marked; the second are left as the copy had them.
	Unsettled []string
	// Evidence says, for each unsettled file, what could not be settled: its
	// regions at their lines, or that it is not a line merge. Bounded.
	Evidence string
	// Seeded is each unsettled file as the copy was left holding it — marked,
	// or as the lane had it where it is not a line merge — and Sides the
	// landed file and the lane's it was merged from: what a turn that
	// reconciles them is judged against (Unreconciled, PickedSide).
	Seeded map[string]SeededFile
	Sides  map[string][2]SeededFile
	// Regen is what the generators did, as a reseed reports it. It is empty
	// where files are unsettled until a turn has reconciled them
	// (Regenerate): the sources the generators read held marks.
	Regen reseedRegen

	dir       string
	oldBase   string
	lanePatch string
	gen       Regenerator
	generated []string
}

// Dir is the copy the reconciliation was made in, where a turn that
// reconciles it is judged.
func (r *Reconciliation) Dir() string { return r.dir }

// Regenerate runs the generators for the landed patch's generated paths over
// the copy as a turn reconciled it, which a carry that left regions unsettled
// did not do: the sources they read held marks. A failure leaves those paths
// at the landed text and is answered, as a reseed's is.
func (r *Reconciliation) Regenerate(ctx context.Context) error {
	r.Regen = regenerateReseeded(ctx, r.gen, r.dir, r.generated)
	return r.Regen.Failed
}

// RegenFailed is the generator failure of a carry that settled, which leaves
// those paths at the landed text.
func (r *Reconciliation) RegenFailed() error { return r.Regen.Failed }

// PutBack puts the copy back to the lane's own patch on the base it had
// before the merge, so a copy that is kept holds the lane's work and no
// marker. The caller holds whatever lock the copy is written under.
func (r *Reconciliation) PutBack() error {
	if _, err := hostgit.Output(context.Background(), r.dir, "reset", "--hard", "--quiet", r.oldBase); err != nil {
		return err
	}
	if _, err := hostgit.Output(context.Background(), r.dir, "clean", "-fdq"); err != nil {
		return err
	}
	if strings.TrimSpace(r.lanePatch) != "" {
		if err := ApplyPatch(r.dir, r.lanePatch); err != nil {
			return err
		}
	}
	_, err := hostgit.Output(context.Background(), r.dir, "reset", "--quiet")
	return err
}

// ReseedMerging is ReseedWorktree for a carry that meets the work the copy
// has: where the landed patch changes files the copy changed too, it merges
// the landed text, the base and the copy's own three ways file by file and
// writes the result, settling a region by rule where a rule fits (settleRegions)
// and marking it as `git merge-file --diff3` does where none does. The copy's
// base then moves by the landed patch exactly as a reseed moves it, so the
// patch the copy hands back is still its own work.
//
// A landed patch the copy's base will not take, or one that meets none of the
// copy's changes, is not a collision of work and comes back as the
// *ReseedCollision a reseed raises, with the copy as it was. Landed paths the
// project generates are never merged: they are regenerated, as in a reseed.
//
// The caller holds the land lock; landed and lane are the labels the marks
// carry. When a path is unsettled the copy is left holding the marked merge,
// and the caller decides: PutBack, or reconcile it.
// See docs/capabilities/todo.md#a-sprint-can-work-several-items-at-once.
func ReseedMerging(ctx context.Context, worktree, patch string, gen Regenerator, landed, lane string) (*Reconciliation, error) {
	files := PatchFiles(patch)
	generated := GeneratedPaths(gen, files)
	carry := WithoutFiles(patch, generated)
	collide := func(reason string) error {
		return &ReseedCollision{Landed: files, Files: collidedPaths(worktree, PatchFiles(carry)), Reason: firstReseedLine(reason)}
	}
	scratch, err := os.MkdirTemp("", "shhh-reconcile-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(scratch) }()
	index := []string{"GIT_INDEX_FILE=" + filepath.Join(scratch, "index")}
	if _, err := hostgit.OutputWith(context.Background(), worktree, hostgit.Options{Env: index}, "read-tree", "HEAD"); err != nil {
		return nil, err
	}
	if _, err := hostgit.OutputWith(context.Background(), worktree, hostgit.Options{Env: index, Stdin: patch}, "apply", "--cached", "--whitespace=nowarn"); err != nil {
		return nil, collide(err.Error())
	}
	changed := changedAmong(worktree, PatchFiles(carry))
	if len(changed) == 0 {
		return nil, collide("the landed patch meets none of the copy's changes")
	}
	landedTree, err := hostgit.OutputWith(context.Background(), worktree, hostgit.Options{Env: index}, "write-tree")
	if err != nil {
		return nil, err
	}
	landedTree = strings.TrimSpace(landedTree)
	oldBase, err := hostgit.Output(context.Background(), worktree, "rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}
	lanePatch, err := WorktreePatch(worktree)
	if err != nil {
		return nil, err
	}
	rec := &Reconciliation{
		Seeded: map[string]SeededFile{}, Sides: map[string][2]SeededFile{},
		dir: worktree, oldBase: strings.TrimSpace(oldBase), lanePatch: lanePatch,
		gen: gen, generated: generated,
	}

	// Every merge is made before a file is written, so a failure while
	// merging leaves the copy as it was.
	type write struct {
		path string
		side MergeSide
	}
	var writes []write
	var evidence strings.Builder
	marks := []string{"--diff3", "--marker-size=" + strconv.Itoa(MarkerSize), "-L", landed, "-L", baseLabel, "-L", lane}
	for _, p := range changed {
		base, ours := treeSide(worktree, "HEAD", p), treeSide(worktree, landedTree, p)
		theirs, err := CheckoutSide(filepath.Join(worktree, filepath.FromSlash(p)))
		if err != nil {
			return nil, err
		}
		merged, conflicts, textual, err := mergeLines(scratch, base, ours, theirs, marks)
		if err != nil {
			return nil, err
		}
		if !textual {
			rec.Unsettled = append(rec.Unsettled, p)
			rec.Seeded[p], rec.Sides[p] = ReadSeeded(worktree, p), [2]SeededFile{seededSide(ours), seededSide(theirs)}
			fmt.Fprintf(&evidence, "## %s\n\nNot a line merge: %s landed a change to it and this lane changed it too, and it is not a text file both can be merged in line by line.\n\n", p, landed)
			continue
		}
		if conflicts > 0 {
			text, left := settleRegions(merged.Text, landed, lane)
			// A mark the parser did not recognise is still a mark: a file
			// that holds one is not settled, whatever was counted.
			if stillMarked(text) {
				left = max(left, 1)
			}
			if left == 0 && !strings.HasSuffix(ours.Text, "\n") && !strings.HasSuffix(theirs.Text, "\n") {
				// git ends a marked region with a newline; a file that ended
				// without one on both sides still does.
				text = strings.TrimSuffix(text, "\n")
			}
			merged.Text = text
			if left > 0 {
				rec.Unsettled = append(rec.Unsettled, p)
				rec.Seeded[p], rec.Sides[p] = SeededFile{Text: text, Exists: true}, [2]SeededFile{seededSide(ours), seededSide(theirs)}
				quote := markedRegions(text, landed, lane, quotedContext)
				if quote == "" {
					quote = "The merge left marks that could not be read as regions; read the file."
				}
				fmt.Fprintf(&evidence, "## %s\n\n%s\n", p, quote)
				writes = append(writes, write{p, merged})
				continue
			}
		}
		rec.Settled = append(rec.Settled, p)
		writes = append(writes, write{p, merged})
	}
	rec.Evidence = boundEvidence(evidence.String())

	if rest := WithoutFiles(carry, changed); strings.TrimSpace(rest) != "" {
		if err := ApplyPatch(worktree, rest); err != nil {
			return nil, collide(err.Error())
		}
	}
	fail := func(err error) (*Reconciliation, error) {
		_ = rec.PutBack()
		return nil, err
	}
	for _, w := range writes {
		full := filepath.Join(worktree, filepath.FromSlash(w.path))
		perm := os.FileMode(0o644)
		if w.side.Mode == "100755" {
			perm = 0o755
		}
		if err := os.WriteFile(full, []byte(w.side.Text), perm); err != nil {
			return fail(err)
		}
		if err := os.Chmod(full, perm); err != nil {
			return fail(err)
		}
	}
	if _, err := moveBase(worktree, index); err != nil {
		return fail(err)
	}
	if len(rec.Unsettled) == 0 {
		rec.Regen = regenerateReseeded(ctx, gen, worktree, generated)
	}
	return rec, nil
}

// seededSide is one side of a merge as a copy would hold it.
func seededSide(side MergeSide) SeededFile {
	return SeededFile{Text: side.Text, Exists: side.Exists}
}

// treeSide is one file in a tree or commit, absent where it does not hold it.
func treeSide(dir, treeish, path string) MergeSide {
	entry, err := hostgit.Output(context.Background(), dir, "ls-tree", treeish, "--", path)
	mode, _, _ := strings.Cut(entry, " ")
	if err != nil || mode == "" {
		return MergeSide{}
	}
	text, err := hostgit.Output(context.Background(), dir, "cat-file", "blob", treeish+":"+path)
	if err != nil {
		return MergeSide{}
	}
	return MergeSide{Exists: true, Mode: mode, Text: text}
}

// boundEvidence cuts the quoted regions at their allowance and says so.
func boundEvidence(s string) string {
	if len(s) <= evidenceBytes {
		return s
	}
	cut := strings.LastIndex(s[:evidenceBytes], "\n")
	if cut <= 0 {
		cut = evidenceBytes
	}
	return s[:cut] + fmt.Sprintf("\n[evidence truncated: %d of %d bytes shown; read the files for the rest]\n", cut, len(s))
}
