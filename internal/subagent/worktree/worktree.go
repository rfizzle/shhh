// Package worktree is writer isolation: each writer child gets a detached
// git worktree of the parent repository, seeded with whatever the parent has
// not committed yet and stood on that as its base. Its changes are collected
// as one patch (`git add -A` + `git diff --cached --binary` in the worktree)
// and applied to the real checkout only after the user approves.
package worktree

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/rfizzle/shhh/internal/hostgit"
)

// worktreeLocks holds one lock per repository toplevel. Two `git worktree
// add` calls in one repository can each read the other's half-written entry
// under .git/worktrees and fail with "failed to read …/commondir", so every
// add, remove and prune in a repository waits its turn. Its one owner is
// this package: AddWorktreeContext and RemoveWorktree take it through
// lockWorktrees. The lock is the package's rather than a supervisor's because
// the exported NewWorktree and Remove make and tear down worktrees too, and a
// lock only some callers take closes nothing. It is a channel so a stopping
// writer can stop waiting.
var worktreeLocks sync.Map // repoTop → chan struct{}

// lockWorktrees waits for the repository's worktree administration, or for
// ctx to end; the function it returns releases the turn.
func lockWorktrees(ctx context.Context, repoTop string) (func(), error) {
	v, _ := worktreeLocks.LoadOrStore(repoTop, make(chan struct{}, 1))
	turn := v.(chan struct{})
	select {
	case turn <- struct{}{}:
		return func() { <-turn }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

var baseDir atomic.Pointer[string]

// SetDir moves the copies this package makes under dir, which is
// agents.worktree_dir; empty is the system's temporary directory again. It is
// set when a process reads its configuration, before any copy is made.
func SetDir(dir string) {
	if dir != "" {
		if abs, err := filepath.Abs(dir); err == nil {
			dir = abs
		}
	}
	baseDir.Store(&dir)
}

// Dir is the directory copies are made under; empty is the system's
// temporary directory.
func Dir() string {
	if d := baseDir.Load(); d != nil {
		return *d
	}
	return ""
}

// WorktreeHandle is a writer's isolated checkout: the worktree directory,
// the child's working root inside it (mirroring the session's position in the
// repository), the repository toplevel a patch applies back to, and how many
// of the parent's uncommitted paths the child was started from.
type WorktreeHandle struct {
	Dir     string
	Root    string
	RepoTop string
	Seeded  int
}

// addWorktree creates a detached worktree of the repository containing root
// at its current HEAD and seeds it with the parent's uncommitted work:
// everything `git diff HEAD` reports, plus the untracked paths the caller
// says the session created.
func addWorktree(root string, untracked []string) (WorktreeHandle, error) {
	return AddWorktreeContext(context.Background(), root, untracked)
}

// AddWorktreeContext builds a writer workspace under the child's lifecycle
// context. The ordinary wrapper keeps callers outside the supervisor working.
// Its git runs under that context so a stopping writer interrupts the copy
// rather than waiting for git's repository lock: a writer has not started its
// turn until the copy exists, so a stop that cannot reach this command leaves
// its slot and the parent waiting behind work it no longer wants.
func AddWorktreeContext(ctx context.Context, root string, untracked []string) (WorktreeHandle, error) {
	var h WorktreeHandle
	if err := ctx.Err(); err != nil {
		return h, err
	}
	top, err := hostgit.Output(ctx, root, "rev-parse", "--show-toplevel")
	if err != nil {
		return h, fmt.Errorf("writer agents need a git repository: %w", err)
	}
	h.RepoTop = strings.TrimSpace(top)

	base := Dir()
	if base != "" {
		if err = os.MkdirAll(base, 0o755); err != nil {
			return WorktreeHandle{}, err
		}
	}
	h.Dir, err = os.MkdirTemp(base, "shhh-agent-*")
	if err != nil {
		return WorktreeHandle{}, err
	}
	// MkdirTemp created the directory; `git worktree add` wants to create it.
	if err = os.Remove(h.Dir); err != nil {
		return WorktreeHandle{}, err
	}
	unlock, err := lockWorktrees(ctx, h.RepoTop)
	if err != nil {
		return WorktreeHandle{}, err
	}
	_, err = hostgit.Output(ctx, h.RepoTop, "worktree", "add", "--detach", h.Dir, "HEAD")
	unlock()
	if err != nil {
		// The git error is the one worth reporting; a directory left behind
		// by a failed add is cleaned up as far as it can be.
		_ = os.RemoveAll(h.Dir)
		return WorktreeHandle{}, err
	}

	// Resolved, because the toplevel git just answered with is: a session
	// standing in a checkout reached through a symlink would otherwise
	// measure its own position against a repository that looks like it is
	// somewhere else, fall back to `.`, and hand a child started in a
	// subdirectory the whole repository instead (rooted.go).
	absRoot := ResolvePath(root)
	rel, relErr := filepath.Rel(h.RepoTop, absRoot)
	if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		rel = "."
	}
	h.Root = filepath.Join(h.Dir, rel)
	if mkErr := os.MkdirAll(h.Root, 0o755); mkErr != nil {
		RemoveWorktree(h.RepoTop, h.Dir)
		return WorktreeHandle{}, mkErr
	}

	// A seed that cannot be carried fails the spawn rather than starting the
	// child quietly from the last commit: a writer that thinks it is looking
	// at your tree and is not writes a patch against text you no longer have,
	// and nothing on screen would say which of the two it did.
	if err := ctx.Err(); err != nil {
		RemoveWorktree(h.RepoTop, h.Dir)
		return WorktreeHandle{}, err
	}
	h.Seeded, err = seedWorktree(h.RepoTop, h.Dir, repoRelative(root, h.RepoTop, untracked))
	if err != nil {
		RemoveWorktree(h.RepoTop, h.Dir)
		return WorktreeHandle{}, err
	}
	return h, nil
}

// seedWorktree carries the parent's uncommitted work into a fresh worktree
// and makes the result the base the child's patch will be measured against.
// It returns how many of the parent's paths the child started from.
//
// A child branched from HEAD alone works on code a session that has been
// going for an hour no longer has: every hunk it writes over a file the
// parent already edited clashes when the patch lands, and the person is asked
// to reconcile a conflict between their own work and work they asked for.
// See docs/capabilities/subagents.md#a-writer-starts-from-your-tree.
func seedWorktree(repoTop, worktree string, untracked []string) (int, error) {
	patch, err := hostgit.Output(context.Background(), repoTop, "diff", "HEAD", "--binary")
	if err != nil {
		return 0, err
	}
	inPatch := map[string]bool{}
	for _, p := range PatchFiles(patch) {
		inPatch[p] = true
	}
	carried := len(inPatch)
	if carried == 0 && len(untracked) == 0 {
		// A clean parent is seeded by doing nothing at all: no apply, no
		// commit, and a worktree still standing exactly where it was added.
		return 0, nil
	}
	if strings.TrimSpace(patch) != "" {
		if err := ApplyPatch(worktree, patch); err != nil {
			return 0, fmt.Errorf("carrying the session's uncommitted changes into the agent worktree: %w", err)
		}
	}
	for _, p := range untracked {
		// A file the session created and the person has since added is in
		// both lists: git knows it now, the diff has already carried it, and
		// counting it twice would tell the lane a number nobody can check.
		if inPatch[p] {
			continue
		}
		copied, err := copyIntoWorktree(repoTop, worktree, p)
		if err != nil {
			return 0, err
		}
		if copied {
			carried++
		}
	}
	if carried == 0 {
		return 0, nil
	}
	if err := commitSeed(worktree); err != nil {
		return 0, err
	}
	return carried, nil
}

// repoRelative re-expresses paths the session named — relative to where it is
// standing, or absolute — as the repository-relative ones a copy of the
// repository can hold. Duplicates and anything outside the repository are
// dropped: a session can name a file anywhere on the disk, and only what is
// under the toplevel has a place in a worktree. The separator is git's, so
// these paths and the ones read out of a patch are the same strings.
func repoRelative(root, repoTop string, paths []string) []string {
	out := make([]string, 0, len(paths))
	seen := map[string]bool{}
	for _, p := range paths {
		if p == "" {
			continue
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(root, p)
		}
		// DisplayPath hands back the path it was given when it is outside,
		// and that answer is always absolute.
		rel := DisplayPath(repoTop, p)
		if filepath.IsAbs(rel) {
			continue
		}
		rel = filepath.ToSlash(rel)
		if seen[rel] {
			continue
		}
		seen[rel] = true
		out = append(out, rel)
	}
	return out
}

// copyIntoWorktree copies one of the parent's untracked files into the
// worktree at the same relative place, permissions included. A path that has
// since gone, or that is not a regular file, is not an error: the session
// recorded a file it wrote, the person may have deleted it or replaced it
// with a link, and there is simply nothing to copy. The stat does not follow
// links, so a symlink is left behind rather than silently flattened into a
// copy of whatever it pointed at — which for a link out of the repository
// would be carrying in something nobody named.
func copyIntoWorktree(repoTop, worktree, rel string) (bool, error) {
	info, err := os.Lstat(filepath.Join(repoTop, rel))
	if err != nil || !info.Mode().IsRegular() {
		return false, nil
	}
	data, err := os.ReadFile(filepath.Join(repoTop, rel))
	if err != nil {
		return false, nil
	}
	dst := filepath.Join(worktree, rel)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return false, err
	}
	if err := os.WriteFile(dst, data, info.Mode().Perm()); err != nil {
		return false, err
	}
	return true, nil
}

// commitSeed makes the seeded state the worktree's own HEAD, which is what
// turns the seed into a base: the returned patch is `git diff --cached`, that
// answers against HEAD, and a base left at the last real commit would hand
// the parent every one of its own uncommitted changes back as if a child had
// written them.
//
// Three flags earn their place. The identity is forced because this commit is
// dangling in a directory that is about to be thrown away, and a machine with
// no `user.email` configured would otherwise fail here for a commit nobody
// will ever read. Hooks are skipped because a checkout whose pre-commit hook
// runs the test suite would run it once per writer, for a commit that is not
// the person's. And an empty commit is allowed because a seed can be entirely
// files git is told to ignore, which `git add` will not stage and which the
// child still has on disk.
func commitSeed(worktree string) error {
	return CommitBase(worktree, "uncommitted work carried from the parent session")
}

// LandedBaseMessage is the base commit a writer's copy takes once its patch
// has landed in the parent's checkout.
const LandedBaseMessage = "patch landed in the parent session"

// CommitBase makes whatever the worktree holds now its HEAD, which is the
// base the next patch is measured from: the seed when a writer starts, and a
// patch that has landed when it is asked a follow-up, so the follow-up's patch
// is its own work and not the landed one a second time. The flags are
// commitSeed's, for its reasons.
func CommitBase(worktree, message string) error {
	if err := stageAll(worktree); err != nil {
		return err
	}
	_, err := hostgit.Output(context.Background(), worktree,
		"-c", "user.name=shhh", "-c", "user.email=shhh@localhost",
		"commit", "--quiet", "--no-verify", "--no-gpg-sign", "--allow-empty",
		"-m", message)
	return err
}

// RemoveWorktree tears a worktree down, best-effort: a vanished directory or
// repository must never block session teardown.
func RemoveWorktree(repoTop, worktree string) {
	if worktree == "" {
		return
	}
	if repoTop != "" {
		unlock, _ := lockWorktrees(context.Background(), repoTop)
		_, _ = hostgit.Output(context.Background(), repoTop, "worktree", "remove", "--force", worktree)
		_, _ = hostgit.Output(context.Background(), repoTop, "worktree", "prune")
		unlock()
	}
	_ = os.RemoveAll(worktree)
}
