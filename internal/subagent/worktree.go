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
		os.RemoveAll(worktree)
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
