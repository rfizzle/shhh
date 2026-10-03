package worktree

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// reseededBaseMessage is the base commit a live writer's copy takes when a
// patch another writer landed is carried into it.
const reseededBaseMessage = "patch landed in the parent session by another writer"

// ReseedCollision is a landed patch that would not carry into a writer's copy
// over the work the writer has there. Landed is every path the patch touched
// and Files the ones it collided on: the landed paths the writer has itself
// changed, or all of them where the patch would not apply to the copy's base
// at all, which is a tree the parent moved some other way since the copy was
// taken. The copy is left exactly as it was.
type ReseedCollision struct {
	Landed []string
	Files  []string
	Reason string
}

func (e *ReseedCollision) Error() string {
	return "the landed patch does not carry into the copy over " + strings.Join(e.Files, ", ") + ": " + e.Reason
}

// ReseedWorktree carries a patch that has landed in the parent's checkout into
// a live writer's copy and makes it part of the copy's base, so the tree the
// writer is working in is the parent's tree again and the patch it hands back
// is still its own work alone.
//
// It is a reseed and not a rebase: the writer's own work is never committed,
// moved or replayed. The landed patch is applied twice — once to a scratch
// index read from the base, which is what is committed on top of it, and once
// to the working tree, over whatever the writer has there — so HEAD moves by
// exactly the landed change and `git diff --cached` after `add -A` (what
// WorktreePatch returns) still answers with the writer's own work. Both
// applies are checked before either touches a file, and the working-tree apply
// is the plain all-or-nothing one (ApplyPatch), so a patch that meets the
// writer's work leaves every file in the copy as it was and comes back as a
// *ReseedCollision naming where.
//
// A landed path the project declares generated (gen) is never applied to the
// working tree as hunks: the base takes the landed bytes, which are what the
// checkout now holds, and the working tree's copy of the file is put back to
// the base and its generator run in the copy, so what the writer sees there
// is what its own source change generates over the landed one. A generator
// that fails leaves those files at the landed text and is reported, not
// raised: the landing itself has carried.
// See docs/capabilities/subagents.md#a-writer-starts-from-your-tree.
func ReseedWorktree(ctx context.Context, worktree, patch string, gen Regenerator) (reseedRegen, error) {
	var regen reseedRegen
	landed := PatchFiles(patch)
	generated := GeneratedPaths(gen, landed)
	carry := WithoutFiles(patch, generated)
	collide := func(reason string) error {
		return &ReseedCollision{Landed: landed, Files: collidedPaths(worktree, PatchFiles(carry)), Reason: firstReseedLine(reason)}
	}
	scratch, err := os.MkdirTemp("", "shhh-reseed-*")
	if err != nil {
		return regen, err
	}
	defer func() { _ = os.RemoveAll(scratch) }()
	// A scratch index, so the writer's own index — which it may have staged
	// into with a command of its own — is not what the base is built from.
	index := []string{"GIT_INDEX_FILE=" + filepath.Join(scratch, "index")}
	if _, err := gitWithEnv(worktree, index, "", "read-tree", "HEAD"); err != nil {
		return regen, err
	}
	if _, err := gitWithEnv(worktree, index, patch, "apply", "--cached", "--whitespace=nowarn"); err != nil {
		return regen, collide(err.Error())
	}
	if carry != "" {
		if _, err := gitWithEnv(worktree, nil, carry, "apply", "--check", "--whitespace=nowarn"); err != nil {
			return regen, collide(err.Error())
		}
		if err := ApplyPatch(worktree, carry); err != nil {
			return regen, collide(err.Error())
		}
	}
	// From here the files have moved and the base has not. A step that fails
	// takes the files back, because a copy whose tree holds the landed change
	// over a base that does not would hand it back as the writer's own work.
	undo := func(err error) (reseedRegen, error) {
		if carry != "" {
			_, _ = gitWithEnv(worktree, nil, carry, "apply", "-R", "--whitespace=nowarn")
		}
		return regen, err
	}
	tree, err := gitWithEnv(worktree, index, "", "write-tree")
	if err != nil {
		return undo(err)
	}
	// The identity and the signing flag are CommitBase's, for its reasons;
	// commit-tree runs no hooks.
	commit, err := gitWithEnv(worktree, nil, "",
		"-c", "user.name=shhh", "-c", "user.email=shhh@localhost",
		"commit-tree", strings.TrimSpace(tree), "-p", "HEAD", "--no-gpg-sign", "-m", reseededBaseMessage)
	if err != nil {
		return undo(err)
	}
	if _, err := RunGit(worktree, "update-ref", "--no-deref", "HEAD", strings.TrimSpace(commit)); err != nil {
		return undo(err)
	}
	// The writer's index is put back on the new base and the working tree
	// left alone: an index still on the old base would read the landed change
	// as the writer's own to anything that asked it.
	if _, err := RunGit(worktree, "reset", "--quiet"); err != nil {
		return regen, err
	}
	if len(generated) == 0 {
		return regen, nil
	}
	if err := restoreFromBase(worktree, generated); err != nil {
		regen.Failed = err
		return regen, nil
	}
	regen.Ran, regen.Failed = runGenerators(ctx, gen, worktree, generated)
	return regen, nil
}

// reseedRegen is what a reseed did about the landed patch's generated paths:
// the generator commands that ran in the copy, and the failure of the one
// that did not finish, which leaves those paths at the landed text.
type reseedRegen struct {
	Ran    []string
	Failed error
}

// collidedPaths is which of the landed paths the writer has changed in its
// copy — the files a landing met the writer's own work on. Where it has
// changed none of them the whole landing is named, because then it was the
// copy's base the patch would not apply to, and every landed path is as
// likely a place to look as any other.
func collidedPaths(worktree string, landed []string) []string {
	changed := map[string]bool{}
	for _, args := range [][]string{{"diff", "HEAD", "--name-only"}, {"ls-files", "--others", "--exclude-standard"}} {
		out, err := GitOutput(worktree, args...)
		if err != nil {
			continue
		}
		for _, p := range strings.Split(out, "\n") {
			changed[strings.TrimSpace(p)] = true
		}
	}
	var out []string
	for _, p := range landed {
		if changed[p] {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return landed
	}
	return out
}

// firstReseedLine is the part of git's refusal worth carrying: its first
// line, which names the file and the line the patch failed at.
func firstReseedLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}
