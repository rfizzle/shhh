package subagent

// Writer isolation: each writer child gets a detached git worktree of the
// parent repository, seeded with whatever the parent has not committed yet
// and stood on that as its base. Its changes are collected as one patch
// (`git add -A` + `git diff --cached --binary` in the worktree) and applied
// to the real checkout only after the user approves.

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/rfizzle/shhh/internal/diff"
)

// runGit executes one git command in dir, returning combined output.
func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
	}
	return string(out), nil
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
	var h worktreeHandle
	top, err := runGit(root, "rev-parse", "--show-toplevel")
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
	if _, err = runGit(h.repoTop, "worktree", "add", "--detach", h.dir, "HEAD"); err != nil {
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
	if _, err := runGit(worktree, "add", "-A"); err != nil {
		return err
	}
	_, err := runGit(worktree,
		"-c", "user.name=shhh", "-c", "user.email=shhh@localhost",
		"commit", "--quiet", "--no-verify", "--no-gpg-sign", "--allow-empty",
		"-m", "uncommitted work carried from the parent session")
	return err
}

// removeWorktree tears a worktree down, best-effort: a vanished directory or
// repository must never block session teardown.
func removeWorktree(repoTop, worktree string) {
	if worktree == "" {
		return
	}
	if repoTop != "" {
		_, _ = runGit(repoTop, "worktree", "remove", "--force", worktree)
		_, _ = runGit(repoTop, "worktree", "prune")
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
