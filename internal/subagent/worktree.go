package subagent

// Writer isolation: each writer child gets a detached git worktree of the
// parent repository, seeded with whatever the parent has not committed yet
// and stood on that as its base. Its changes are collected as one patch
// (`git add -A` + `git diff --cached --binary` in the worktree) and applied
// to the real checkout only after the user approves.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/rfizzle/shhh/internal/diff"
)

// runGit executes one git command in dir, returning combined output.
func runGit(dir string, args ...string) (string, error) {
	return runGitContext(context.Background(), dir, args...)
}

// runGitContext lets a stopping writer interrupt worktree creation rather than
// waiting for git's repository lock. A writer has not started its turn until
// the copy exists, so a stop that cannot reach this command leaves its slot and
// the parent waiting behind work it no longer wants.
func runGitContext(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// worktreeLocks holds one lock per repository toplevel. Two `git worktree
// add` calls in one repository can each read the other's half-written entry
// under .git/worktrees and fail with "failed to read …/commondir", so every
// add, remove and prune in a repository waits its turn. The lock is the
// package's rather than a supervisor's because the exported NewWorktree and
// Remove make and tear down worktrees too, and a lock only some callers take
// closes nothing. It is a channel so a stopping writer can stop waiting.
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

// worktreeHandle is a writer's isolated checkout: the worktree directory,
// the child's working root inside it (mirroring the session's position in the
// repository), the repository toplevel a patch applies back to, and how many
// of the parent's uncommitted paths the child was started from.
type worktreeHandle struct {
	dir     string
	root    string
	repoTop string
	seeded  int
}

// addWorktree creates a detached worktree of the repository containing root
// at its current HEAD and seeds it with the parent's uncommitted work:
// everything `git diff HEAD` reports, plus the untracked paths the caller
// says the session created.
func addWorktree(root string, untracked []string) (worktreeHandle, error) {
	return addWorktreeContext(context.Background(), root, untracked)
}

// addWorktreeContext builds a writer workspace under the child's lifecycle
// context. The ordinary wrapper keeps callers outside the supervisor working.
func addWorktreeContext(ctx context.Context, root string, untracked []string) (worktreeHandle, error) {
	var h worktreeHandle
	if err := ctx.Err(); err != nil {
		return h, err
	}
	top, err := runGitContext(ctx, root, "rev-parse", "--show-toplevel")
	if err != nil {
		return h, fmt.Errorf("writer agents need a git repository: %w", err)
	}
	h.repoTop = strings.TrimSpace(top)

	h.dir, err = os.MkdirTemp("", "shhh-agent-*")
	if err != nil {
		return worktreeHandle{}, err
	}
	// MkdirTemp created the directory; `git worktree add` wants to create it.
	if err = os.Remove(h.dir); err != nil {
		return worktreeHandle{}, err
	}
	unlock, err := lockWorktrees(ctx, h.repoTop)
	if err != nil {
		return worktreeHandle{}, err
	}
	_, err = runGitContext(ctx, h.repoTop, "worktree", "add", "--detach", h.dir, "HEAD")
	unlock()
	if err != nil {
		// The git error is the one worth reporting; a directory left behind
		// by a failed add is cleaned up as far as it can be.
		_ = os.RemoveAll(h.dir)
		return worktreeHandle{}, err
	}

	// Resolved, because the toplevel git just answered with is: a session
	// standing in a checkout reached through a symlink would otherwise
	// measure its own position against a repository that looks like it is
	// somewhere else, fall back to `.`, and hand a child started in a
	// subdirectory the whole repository instead (rooted.go).
	absRoot := resolvePath(root)
	rel, relErr := filepath.Rel(h.repoTop, absRoot)
	if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		rel = "."
	}
	h.root = filepath.Join(h.dir, rel)
	if mkErr := os.MkdirAll(h.root, 0o755); mkErr != nil {
		removeWorktree(h.repoTop, h.dir)
		return worktreeHandle{}, mkErr
	}

	// A seed that cannot be carried fails the spawn rather than starting the
	// child quietly from the last commit: a writer that thinks it is looking
	// at your tree and is not writes a patch against text you no longer have,
	// and nothing on screen would say which of the two it did.
	if err := ctx.Err(); err != nil {
		removeWorktree(h.repoTop, h.dir)
		return worktreeHandle{}, err
	}
	h.seeded, err = seedWorktree(h.repoTop, h.dir, repoRelative(root, h.repoTop, untracked))
	if err != nil {
		removeWorktree(h.repoTop, h.dir)
		return worktreeHandle{}, err
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
	patch, err := gitOutput(repoTop, "diff", "HEAD", "--binary")
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
		if err := applyPatch(worktree, patch); err != nil {
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
		// displayPath hands back the path it was given when it is outside,
		// and that answer is always absolute.
		rel := displayPath(repoTop, p)
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
	return commitBase(worktree, "uncommitted work carried from the parent session")
}

// landedBaseMessage is the base commit a writer's copy takes once its patch
// has landed in the parent's checkout.
const landedBaseMessage = "patch landed in the parent session"

// commitBase makes whatever the worktree holds now its HEAD, which is the
// base the next patch is measured from: the seed when a writer starts, and a
// patch that has landed when it is asked a follow-up, so the follow-up's patch
// is its own work and not the landed one a second time. The flags are
// commitSeed's, for its reasons.
func commitBase(worktree, message string) error {
	if _, err := runGit(worktree, "add", "-A"); err != nil {
		return err
	}
	_, err := runGit(worktree,
		"-c", "user.name=shhh", "-c", "user.email=shhh@localhost",
		"commit", "--quiet", "--no-verify", "--no-gpg-sign", "--allow-empty",
		"-m", message)
	return err
}

// removeWorktree tears a worktree down, best-effort: a vanished directory or
// repository must never block session teardown.
func removeWorktree(repoTop, worktree string) {
	if worktree == "" {
		return
	}
	if repoTop != "" {
		unlock, _ := lockWorktrees(context.Background(), repoTop)
		_, _ = runGit(repoTop, "worktree", "remove", "--force", worktree)
		_, _ = runGit(repoTop, "worktree", "prune")
		unlock()
	}
	_ = os.RemoveAll(worktree)
}

// gitOutput runs one git command and returns its standard output alone. A
// patch read with the error stream folded into it is a patch that will not
// apply, so the two streams are kept apart wherever the output is content
// rather than a report.
func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var out, errBuf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errBuf
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(errBuf.String()))
	}
	return out.String(), nil
}

// worktreePatch stages everything in the worktree (so new files are included)
// and returns the full change against HEAD as one binary-capable patch. HEAD
// there is the seed, so what comes back is the child's own work and never the
// parent's.
func worktreePatch(worktree string) (string, error) {
	if _, err := runGit(worktree, "add", "-A"); err != nil {
		return "", err
	}
	return gitOutput(worktree, "diff", "--cached", "--binary")
}

// applyPatch applies a patch to a checkout's working tree, and is both
// directions of a writer's isolation: the parent's uncommitted work going
// into a fresh worktree, and the child's reviewed patch coming back.
//
// Plainly, and deliberately not with `--3way`. Three-way merge implies
// `--index`, which refuses any file whose working copy differs from the
// index — which is every file the parent has edited and not staged, and so
// precisely the tree this seeding exists to support. The seed is what makes
// the patch apply: its context is the parent's own text rather than the last
// commit's, so an ordinary apply matches. An ordinary apply is also all-or-
// nothing, where a three-way merge would leave conflict markers in the
// person's files for them to find.
func applyPatch(repoTop, patch string) error {
	cmd := exec.Command("git", "-C", repoTop, "apply", "--whitespace=nowarn")
	cmd.Stdin = strings.NewReader(patch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s", strings.TrimSpace(string(out)))
	}
	return nil
}

// fileSide is a file as it was at one moment: content, whether it was there
// at all, and the permission bits it had.
type fileSide struct {
	text   string
	exists bool
	mode   os.FileMode
}

// readSides reads the given repo-relative paths from the real checkout,
// reporting a missing file as absent rather than as an error — a patch that
// creates a file has no before-content, and that is the fact the changeset
// record needs.
//
// The mode is read beside the content, by the same stat the session takes
// around its own edits. A patch is free to delete an executable script, and
// once it has there is nowhere else left to learn that it was one: taking the
// turn back would write the file out at the default mode and the next
// `./script.sh` would fail with permission denied.
func readSides(repoTop string, paths []string) map[string]fileSide {
	out := make(map[string]fileSide, len(paths))
	for _, p := range paths {
		full := filepath.Join(repoTop, p)
		data, err := os.ReadFile(full)
		if err != nil {
			out[p] = fileSide{}
			continue
		}
		side := fileSide{text: string(data), exists: true}
		if fi, statErr := os.Stat(full); statErr == nil {
			// Permission bits only: applying a patch never changed an owner
			// or a timestamp, so putting one back is not undo's to do.
			side.mode = fi.Mode().Perm()
		}
		out[p] = side
	}
	return out
}

// patchedFiles pairs the reads taken either side of `git apply` into one
// record per file; a file the patch created or removed is carried by its
// Exists flags. Patch paths are relative to the repository top, which is not
// where the session is standing when it was started from a subdirectory — so
// each one is re-expressed against the session's own root, the way every
// other path the user sees is.
func patchedFiles(root, repoTop string, paths []string, before, after map[string]fileSide) []PatchedFile {
	out := make([]PatchedFile, 0, len(paths))
	for _, p := range paths {
		b, a := before[p], after[p]
		out = append(out, PatchedFile{
			Path:         displayPath(root, filepath.Join(repoTop, p)),
			Before:       b.text,
			BeforeExists: b.exists,
			BeforeMode:   b.mode,
			After:        a.text,
			AfterExists:  a.exists,
			AfterMode:    a.mode,
		})
	}
	return out
}

var hunkHeaderRe = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// PatchHunks parses a unified git patch into diff.Hunk values for the
// approval card's diff preview, and counts the files it touches. Each file's
// first hunk opens with a context line naming the file; binary changes render
// as a one-line note.
func PatchHunks(patch string) (hunks []diff.Hunk, files int) {
	var cur *diff.Hunk
	var curFile string
	fileLabelPending := false
	oldNo, newNo := 0, 0

	flush := func() {
		if cur != nil {
			hunks = append(hunks, *cur)
			cur = nil
		}
	}

	for _, line := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			flush()
			files++
			curFile = parseGitDiffPath(line)
			fileLabelPending = true
		case strings.HasPrefix(line, "@@"):
			m := hunkHeaderRe.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			flush()
			h := diff.Hunk{
				OldStart: atoiDefault(m[1], 0),
				OldCount: atoiDefault(m[2], 1),
				NewStart: atoiDefault(m[3], 0),
				NewCount: atoiDefault(m[4], 1),
			}
			oldNo, newNo = h.OldStart, h.NewStart
			if fileLabelPending {
				h.Lines = append(h.Lines, diff.Line{Kind: diff.Context, Text: "─ " + curFile})
				fileLabelPending = false
			}
			cur = &h
		case strings.HasPrefix(line, "Binary files ") || strings.HasPrefix(line, "GIT binary patch"):
			flush()
			hunks = append(hunks, diff.Hunk{Lines: []diff.Line{{Kind: diff.Context, Text: "─ " + curFile + " (binary change)"}}})
			fileLabelPending = false
		case cur == nil:
			// File headers (---/+++/index/mode/rename) between hunks.
		case strings.HasPrefix(line, "+"):
			cur.Lines = append(cur.Lines, diff.Line{Kind: diff.Add, Text: line[1:], NewNo: newNo})
			newNo++
		case strings.HasPrefix(line, "-"):
			cur.Lines = append(cur.Lines, diff.Line{Kind: diff.Del, Text: line[1:], OldNo: oldNo})
			oldNo++
		case strings.HasPrefix(line, " "):
			cur.Lines = append(cur.Lines, diff.Line{Kind: diff.Context, Text: line[1:], OldNo: oldNo, NewNo: newNo})
			oldNo++
			newNo++
		}
		// Anything else (e.g. "\ No newline at end of file") is ignored.
	}
	flush()
	return hunks, files
}

// PatchFiles lists the workspace-relative paths a unified git patch touches.
func PatchFiles(patch string) []string {
	var files []string
	seen := map[string]bool{}
	for _, line := range strings.Split(patch, "\n") {
		if !strings.HasPrefix(line, "diff --git ") {
			continue
		}
		if p := parseGitDiffPath(line); p != "" && !seen[p] {
			seen[p] = true
			files = append(files, p)
		}
	}
	return files
}

// parseGitDiffPath extracts the b/ path from a "diff --git a/x b/x" line.
func parseGitDiffPath(line string) string {
	rest := strings.TrimPrefix(line, "diff --git ")
	if i := strings.Index(rest, " b/"); i >= 0 {
		return rest[i+3:]
	}
	return rest
}

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

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
	h worktreeHandle
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
func (w *Worktree) Root() string { return w.h.root }

// Land applies what was built here to the checkout it was copied from and
// answers with the repository-relative paths the patch touched. A patch with
// nothing in it lands nothing and names nothing, which is the answer for a
// lane that changed no files.
//
// The apply is all-or-nothing (applyPatch), so a lane whose work overlaps
// what another lane has already landed leaves the checkout exactly as it was
// and says so with an error, rather than half-applying and leaving conflict
// markers in files nobody has read.
//
// A patch the checkout has moved under since the copy was taken — an earlier
// lane landed in the same files, somewhere else in them — is merged three
// ways against the copy's base (mergeWorktree) and the merge is what lands,
// by the same plain apply. A merge that leaves a conflict region lands
// nothing and names the files.
//
// A path the project declares generated (UseGenerators) is neither applied
// nor merged: the rest of the patch is, in a copy of the checkout, its
// generators are run there, and what lands is that copy's difference
// (regenerateOver). A generator that fails lands nothing.
func (w *Worktree) Land() ([]string, error) {
	landed, err := w.LandPatch()
	return PatchFiles(landed), err
}

// LandPatch is Land answering with the patch it applied rather than its file
// list: the writer's own where it applied plainly, the merge where the
// checkout had moved, and with the regenerated files where there were any —
// what the checkout moved by, which is what every other copy is owed.
func (w *Worktree) LandPatch() (string, error) {
	patch, err := worktreePatch(w.h.dir)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(patch) == "" {
		return "", nil
	}
	generated := generatedPaths(w.gen, PatchFiles(patch))
	offer := withoutFiles(patch, generated)
	if offer != "" {
		if applyErr := checkPatch(w.h.repoTop, offer); applyErr != nil {
			m, err := mergeWorktree(w.h.dir, w.h.repoTop, generated)
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
		if offer, _, err = regenerateOver(context.Background(), w.gen, w.root, w.untracked, offer, generated); err != nil {
			return "", err
		}
	}
	if offer == "" {
		// The checkout already says everything the copy does.
		return "", nil
	}
	if err := applyPatch(w.h.repoTop, offer); err != nil {
		return "", err
	}
	return offer, nil
}

// Remove tears the copy down. Best-effort, like every other teardown of one:
// a directory that is already gone must not stop a run from ending.
func (w *Worktree) Remove() { removeWorktree(w.h.repoTop, w.h.dir) }

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

// reseedWorktree carries a patch that has landed in the parent's checkout into
// a live writer's copy and makes it part of the copy's base, so the tree the
// writer is working in is the parent's tree again and the patch it hands back
// is still its own work alone.
//
// It is a reseed and not a rebase: the writer's own work is never committed,
// moved or replayed. The landed patch is applied twice — once to a scratch
// index read from the base, which is what is committed on top of it, and once
// to the working tree, over whatever the writer has there — so HEAD moves by
// exactly the landed change and `git diff --cached` after `add -A` (what
// worktreePatch returns) still answers with the writer's own work. Both
// applies are checked before either touches a file, and the working-tree apply
// is the plain all-or-nothing one (applyPatch), so a patch that meets the
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
func reseedWorktree(ctx context.Context, worktree, patch string, gen Regenerator) (reseedRegen, error) {
	var regen reseedRegen
	landed := PatchFiles(patch)
	generated := generatedPaths(gen, landed)
	carry := withoutFiles(patch, generated)
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
		if err := applyPatch(worktree, carry); err != nil {
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
	// The identity and the signing flag are commitBase's, for its reasons;
	// commit-tree runs no hooks.
	commit, err := gitWithEnv(worktree, nil, "",
		"-c", "user.name=shhh", "-c", "user.email=shhh@localhost",
		"commit-tree", strings.TrimSpace(tree), "-p", "HEAD", "--no-gpg-sign", "-m", reseededBaseMessage)
	if err != nil {
		return undo(err)
	}
	if _, err := runGit(worktree, "update-ref", "--no-deref", "HEAD", strings.TrimSpace(commit)); err != nil {
		return undo(err)
	}
	// The writer's index is put back on the new base and the working tree
	// left alone: an index still on the old base would read the landed change
	// as the writer's own to anything that asked it.
	if _, err := runGit(worktree, "reset", "--quiet"); err != nil {
		return regen, err
	}
	if len(generated) == 0 {
		return regen, nil
	}
	if err := restoreFromBase(worktree, generated); err != nil {
		regen.failed = err
		return regen, nil
	}
	regen.ran, regen.failed = runGenerators(ctx, gen, worktree, generated)
	return regen, nil
}

// reseedRegen is what a reseed did about the landed patch's generated paths:
// the generator commands that ran in the copy, and the failure of the one
// that did not finish, which leaves those paths at the landed text.
type reseedRegen struct {
	ran    []string
	failed error
}

// collidedPaths is which of the landed paths the writer has changed in its
// copy — the files a landing met the writer's own work on. Where it has
// changed none of them the whole landing is named, because then it was the
// copy's base the patch would not apply to, and every landed path is as
// likely a place to look as any other.
func collidedPaths(worktree string, landed []string) []string {
	changed := map[string]bool{}
	for _, args := range [][]string{{"diff", "HEAD", "--name-only"}, {"ls-files", "--others", "--exclude-standard"}} {
		out, err := gitOutput(worktree, args...)
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

// gitWithEnv runs one git command with extra environment and, where it is not
// empty, the given text on its standard input, answering with its standard
// output. It exists for the scratch index a reseed builds its base in.
func gitWithEnv(dir string, env []string, stdin string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out, errBuf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errBuf
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(errBuf.String()))
	}
	return out.String(), nil
}

// Reseed carries a patch that has landed in the checkout this copy was taken
// from into the copy, under whatever is being built here, and makes it part
// of the copy's base — so the next Land hands back only this copy's own work,
// measured against the checkout as it now stands. A patch that meets the work
// here is refused as a *ReseedCollision and the copy is left exactly as it
// was (reseedWorktree). A generated path's generator that fails in the copy
// is the error too, with the landing itself carried and that path at the
// landed text.
func (w *Worktree) Reseed(patch string) error {
	if strings.TrimSpace(patch) == "" {
		return nil
	}
	regen, err := reseedWorktree(context.Background(), w.h.dir, patch, w.gen)
	if err != nil {
		return err
	}
	return regen.failed
}

// MergeConflict is a patch that would not apply plainly and whose three-way
// merge left a conflict region: Files are where. Nothing was written.
type MergeConflict struct{ Files []string }

func (e *MergeConflict) Error() string {
	return "the patch conflicts with the checkout, which moved since it started, in " + patchPaths(e.Files)
}

// patchMerge is a writer's patch that no longer applies plainly, merged three
// ways over the checkout as it stands.
type patchMerge struct {
	// Patch is the merge as a patch against the checkout now, which is what
	// is shown and what lands. Empty where the checkout already holds every
	// change the writer made, and wherever there are conflicts.
	Patch string
	// Moved is the writer's files the checkout changed since the copy's base:
	// the ones the merge went over.
	Moved []string
	// Conflicts is the files whose merge left a conflict region.
	Conflicts []string
	// Resolved is, where there are conflicts, the merge of every other file
	// as a patch against the checkout now: what an integration writer's
	// copy starts from, so the files it is not asked to reconcile arrive
	// already merged and cannot be left out of the one result.
	Resolved string
}

// checkPatch asks whether a patch applies to the checkout as it stands,
// changing nothing: the question the landing's own all-or-nothing apply
// answers, asked before a card is put up for a patch that could not land.
func checkPatch(repoTop, patch string) error {
	_, err := gitWithEnv(repoTop, nil, patch, "apply", "--check", "--whitespace=nowarn")
	return err
}

// mergeSide is one file at one of a merge's three moments, as git would hold
// it: absent, or content with a mode.
type mergeSide struct {
	exists bool
	mode   string
	sha    string // the blob, where git already holds it
	text   string
}

func (a mergeSide) same(b mergeSide) bool {
	if !a.exists || !b.exists {
		return a.exists == b.exists
	}
	return a.mode == b.mode && a.text == b.text
}

// textual is whether a side can go through a line merge: a regular file with
// no NUL in it. A link, a submodule or a binary has no lines to merge, so a
// change on both sides of one is a conflict rather than a guess.
func (a mergeSide) textual() bool {
	return (a.mode == "100644" || a.mode == "100755") && !strings.Contains(a.text, "\x00")
}

// mergeWorktree merges a writer's work three ways over the checkout it came
// from, file by file: the base is the file at the copy's HEAD — the seed, or
// the last landing carried in — ours is the file in the checkout now, and
// theirs is the writer's, as its copy's index holds it after worktreePatch.
// A file the checkout has not moved is the writer's outright; one it has moved
// goes through `git merge-file`.
//
// `git merge-file`, and not a three-way of shhh's own over internal/diff:
// git is already what every writer's copy is built from, and its line merge
// is the one the person would get resolving the same two changes by hand, so
// a region it calls clean is one they would call clean. A second merge
// algorithm would be a second answer to what "overlaps" means.
//
// It runs in a scratch directory with a scratch index — never in a worktree
// of its own — and the person's checkout is only read: what comes back is a
// patch against it, written only by the ordinary all-or-nothing apply once it
// is approved. It is not `git apply --3way`, for the reason applyPatch gives.
//
// The paths in skip are left out of it altogether — neither merged, nor
// moved, nor in conflict — because they are generated, and a generated file
// is regenerated over the merge rather than merged (regenerateOver).
// See docs/capabilities/subagents.md#a-writer-starts-from-your-tree.
func mergeWorktree(worktree, repoTop string, skip []string) (patchMerge, error) {
	raw, err := gitOutput(worktree, "diff", "--cached", "--raw", "--no-renames", "--no-abbrev", "-z")
	if err != nil {
		return patchMerge{}, err
	}
	return mergeRaw(worktree, repoTop, raw, skip)
}

// mergeKept is mergeWorktree for a patch whose copy may be gone: base is the
// commit the copy stood on, which lives in the object store the checkout and
// every copy share, and the writer's side is that commit with the patch
// applied, built in a scratch index. It is what lets a kept patch reviewed
// from its row be merged the way a finishing writer's is.
func mergeKept(repoTop, base, patch string, skip []string) (patchMerge, error) {
	theirs, err := keptTree(repoTop, base, patch)
	if err != nil {
		return patchMerge{}, err
	}
	raw, err := gitOutput(repoTop, "diff-tree", "-r", "--raw", "--no-renames", "--no-abbrev", "-z", base+"^{tree}", theirs)
	if err != nil {
		return patchMerge{}, err
	}
	return mergeRaw(repoTop, repoTop, raw, skip)
}

// keptTree is base with patch applied, as a tree in the shared object store,
// built in a scratch index so neither the checkout's index nor a copy's is
// touched.
func keptTree(repoTop, base, patch string) (string, error) {
	scratch, err := os.MkdirTemp("", "shhh-kept-*")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(scratch) }()
	index := []string{"GIT_INDEX_FILE=" + filepath.Join(scratch, "index")}
	if _, err := gitWithEnv(repoTop, index, "", "read-tree", base); err != nil {
		return "", err
	}
	if strings.TrimSpace(patch) != "" {
		if _, err := gitWithEnv(repoTop, index, patch, "apply", "--cached", "--whitespace=nowarn"); err != nil {
			return "", err
		}
	}
	tree, err := gitWithEnv(repoTop, index, "", "write-tree")
	return strings.TrimSpace(tree), err
}

// mergeRaw is the merge over a raw diff from the base to the writer's side:
// worktree is where the sides' blobs are read and the scratch trees built,
// repoTop the checkout whose files are ours.
func mergeRaw(worktree, repoTop, raw string, skip []string) (patchMerge, error) {
	var m patchMerge
	skipped := map[string]bool{}
	for _, p := range skip {
		skipped[p] = true
	}
	scratch, err := os.MkdirTemp("", "shhh-merge-*")
	if err != nil {
		return m, err
	}
	defer func() { _ = os.RemoveAll(scratch) }()

	type entry struct {
		path         string
		ours, result mergeSide
	}
	var entries []entry
	fields := strings.Split(raw, "\x00")
	for i := 0; i+1 < len(fields); i += 2 {
		head, path := strings.Fields(strings.TrimPrefix(fields[i], ":")), fields[i+1]
		if len(head) < 5 || skipped[path] {
			continue
		}
		base := mergeSide{exists: head[0] != "000000", mode: head[0], sha: head[2]}
		theirs := mergeSide{exists: head[1] != "000000", mode: head[1], sha: head[3]}
		for _, side := range []*mergeSide{&base, &theirs} {
			if side.exists {
				if side.text, err = gitOutput(worktree, "cat-file", "blob", side.sha); err != nil {
					return m, err
				}
			}
		}
		ours, err := checkoutSide(filepath.Join(repoTop, filepath.FromSlash(path)))
		if err != nil {
			return m, err
		}
		if ours.same(base) {
			entries = append(entries, entry{path, ours, theirs})
			continue
		}
		m.Moved = append(m.Moved, path)
		if ours.same(theirs) {
			entries = append(entries, entry{path, ours, ours})
			continue
		}
		merged, ok, err := mergeFile(scratch, base, ours, theirs)
		if err != nil {
			return m, err
		}
		if !ok {
			m.Conflicts = append(m.Conflicts, path)
			continue
		}
		entries = append(entries, entry{path, ours, merged})
	}

	// The patch is the difference between two trees holding only these
	// files: the checkout's side, then the merge. Both are built in a scratch
	// index, so neither what the writer staged nor the checkout's own index
	// is touched.
	index := []string{"GIT_INDEX_FILE=" + filepath.Join(scratch, "index")}
	tree := func(pick func(entry) mergeSide) (string, error) {
		var lines strings.Builder
		for _, e := range entries {
			side := pick(e)
			if !side.exists {
				fmt.Fprintf(&lines, "0 %s\t%s\x00", strings.Repeat("0", 40), e.path)
				continue
			}
			sha := side.sha
			if sha == "" {
				var err error
				if sha, err = hashBlob(worktree, side.text); err != nil {
					return "", err
				}
			}
			fmt.Fprintf(&lines, "%s %s\t%s\x00", side.mode, sha, e.path)
		}
		if lines.Len() > 0 {
			if _, err := gitWithEnv(worktree, index, lines.String(), "update-index", "-z", "--index-info"); err != nil {
				return "", err
			}
		}
		out, err := gitWithEnv(worktree, index, "", "write-tree")
		return strings.TrimSpace(out), err
	}
	if _, err := gitWithEnv(worktree, index, "", "read-tree", "--empty"); err != nil {
		return m, err
	}
	from, err := tree(func(e entry) mergeSide { return e.ours })
	if err != nil {
		return m, err
	}
	to, err := tree(func(e entry) mergeSide { return e.result })
	if err != nil {
		return m, err
	}
	patch, err := gitOutput(worktree, "diff-tree", "-p", "--binary", "--no-renames", "--full-index", from, to)
	if strings.TrimSpace(patch) == "" {
		patch = ""
	}
	if len(m.Conflicts) > 0 {
		m.Resolved = patch
	} else {
		m.Patch = patch
	}
	return m, err
}

// checkoutSide reads one file of the person's checkout as git would hold it.
// A path that is not there is absent; one that is neither a file nor a link —
// a directory where the writer has a file — is a side no merge can use, and
// reads as a mode nothing else has, so it is never the same as either other
// side and never textual.
func checkoutSide(full string) (mergeSide, error) {
	info, err := os.Lstat(full)
	if os.IsNotExist(err) {
		return mergeSide{}, nil
	}
	if err != nil {
		return mergeSide{}, err
	}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(full)
		return mergeSide{exists: true, mode: "120000", text: target}, err
	case info.Mode().IsRegular():
		data, err := os.ReadFile(full)
		mode := "100644"
		if info.Mode().Perm()&0o111 != 0 {
			mode = "100755"
		}
		return mergeSide{exists: true, mode: mode, text: string(data)}, err
	}
	return mergeSide{exists: true, mode: "?"}, nil
}

// mergeFile is one file's three-way line merge, answering with the merged
// side and whether it came out clean. A file added or removed on either side,
// or one that is not text, has no merge short of choosing a winner, so it is
// not clean. The mode is whichever side changed it.
func mergeFile(scratch string, base, ours, theirs mergeSide) (mergeSide, bool, error) {
	if !base.exists || !ours.exists || !theirs.exists || !base.textual() || !ours.textual() || !theirs.textual() {
		return mergeSide{}, false, nil
	}
	mode := ours.mode
	switch {
	case theirs.mode == base.mode:
	case ours.mode == base.mode:
		mode = theirs.mode
	case ours.mode != theirs.mode:
		return mergeSide{}, false, nil
	}
	names := make([]string, 3)
	for i, side := range []mergeSide{ours, base, theirs} {
		f, err := os.CreateTemp(scratch, "side-*")
		if err != nil {
			return mergeSide{}, false, err
		}
		_, werr := f.WriteString(side.text)
		cerr := f.Close()
		if werr != nil || cerr != nil {
			return mergeSide{}, false, errors.Join(werr, cerr)
		}
		names[i] = f.Name()
	}
	cmd := exec.Command("git", append([]string{"merge-file", "-p"}, names...)...)
	var out, errBuf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errBuf
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return mergeSide{exists: true, mode: mode, text: out.String()}, true, nil
	case errors.As(err, &exit) && exit.ExitCode() > 0 && exit.ExitCode() < 128:
		// The exit status is the number of conflict regions.
		return mergeSide{}, false, nil
	}
	return mergeSide{}, false, fmt.Errorf("git merge-file: %s", strings.TrimSpace(errBuf.String()))
}

// hashBlob writes text into the repository's object store as a blob and
// answers with its id. The store is the one the checkout and every copy of it
// share, and nothing points at the blob until a landing commits it, so an
// unlanded merge leaves only a dangling object git collects on its own.
func hashBlob(dir, text string) (string, error) {
	out, err := gitWithEnv(dir, nil, text, "hash-object", "-w", "--no-filters", "--stdin")
	return strings.TrimSpace(out), err
}
