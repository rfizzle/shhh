package subagent

// Writer isolation: each writer child gets a detached git worktree of the
// parent repository at HEAD. Its changes are collected as one patch
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

// addWorktree creates a detached worktree of the repository containing root
// at its current HEAD. It returns the worktree directory, the child's working
// root inside it (mirroring root's position in the repo), and the repository
// toplevel (where patches apply).
func addWorktree(root string) (worktree, childRoot, repoTop string, err error) {
	top, err := runGit(root, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", "", "", fmt.Errorf("writer agents need a git repository: %w", err)
	}
	repoTop = strings.TrimSpace(top)

	worktree, err = os.MkdirTemp("", "shhh-agent-*")
	if err != nil {
		return "", "", "", err
	}
	// MkdirTemp created the directory; `git worktree add` wants to create it.
	if err = os.Remove(worktree); err != nil {
		return "", "", "", err
	}
	if _, err = runGit(repoTop, "worktree", "add", "--detach", worktree, "HEAD"); err != nil {
		// The git error is the one worth reporting; a directory left behind
		// by a failed add is cleaned up as far as it can be.
		_ = os.RemoveAll(worktree)
		return "", "", "", err
	}

	absRoot, absErr := filepath.Abs(root)
	if absErr != nil {
		removeWorktree(repoTop, worktree)
		return "", "", "", absErr
	}
	rel, relErr := filepath.Rel(repoTop, absRoot)
	if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		rel = "."
	}
	childRoot = filepath.Join(worktree, rel)
	if mkErr := os.MkdirAll(childRoot, 0o755); mkErr != nil {
		removeWorktree(repoTop, worktree)
		return "", "", "", mkErr
	}
	return worktree, childRoot, repoTop, nil
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

// worktreePatch stages everything in the worktree (so new files are included)
// and returns the full change against HEAD as one binary-capable patch.
func worktreePatch(worktree string) (string, error) {
	if _, err := runGit(worktree, "add", "-A"); err != nil {
		return "", err
	}
	cmd := exec.Command("git", "-C", worktree, "diff", "--cached", "--binary")
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git diff --cached: %s", strings.TrimSpace(errBuf.String()))
	}
	return out.String(), nil
}

// applyPatch applies a worktree patch to the real checkout's working tree.
func applyPatch(repoTop, patch string) error {
	cmd := exec.Command("git", "-C", repoTop, "apply", "--whitespace=nowarn")
	cmd.Stdin = strings.NewReader(patch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s", strings.TrimSpace(string(out)))
	}
	return nil
}

// fileSide is a file as it was at one moment: content, and whether it was
// there at all.
type fileSide struct {
	text   string
	exists bool
}

// readSides reads the given repo-relative paths from the real checkout,
// reporting a missing file as absent rather than as an error — a patch that
// creates a file has no before-content, and that is the fact the changeset
// record needs.
func readSides(repoTop string, paths []string) map[string]fileSide {
	out := make(map[string]fileSide, len(paths))
	for _, p := range paths {
		if data, err := os.ReadFile(filepath.Join(repoTop, p)); err == nil {
			out[p] = fileSide{text: string(data), exists: true}
		} else {
			out[p] = fileSide{}
		}
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
			After:        a.text,
			AfterExists:  a.exists,
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
