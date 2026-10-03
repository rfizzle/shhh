package worktree

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/rfizzle/shhh/internal/diff"
	"github.com/rfizzle/shhh/internal/hostgit"
)

// WorktreePatch stages everything in the worktree (so new files are included)
// and returns the full change against HEAD as one binary-capable patch. HEAD
// there is the seed, so what comes back is the child's own work and never the
// parent's.
func WorktreePatch(worktree string) (string, error) {
	if err := stageAll(worktree); err != nil {
		return "", err
	}
	return GitOutput(worktree, "diff", "--cached", "--binary")
}

// stageAll is `git add -A` over a copy a child wrote, with every gitlink the
// index holds left out by pathspec. Staging a submodule path asks that
// submodule whether it is dirty by running git inside it — under a store the
// child could have written, clean filter and all — and add takes no
// --ignore-submodules and honours no configuration that would stop it; a
// pathspec that never names the path is the one thing that does. A child's
// copy has no submodule checked out, so a gitlink there with a store behind
// it is one the child made.
// See docs/capabilities/containment.md#the-hosts-own-git-runs-nothing-a-command-wrote.
func stageAll(worktree string) error {
	listed, err := GitOutput(worktree, "ls-files", "--stage", "-z")
	if err != nil {
		return err
	}
	args := []string{"add", "-A", "--", "."}
	for _, entry := range strings.Split(listed, "\x00") {
		meta, path, ok := strings.Cut(entry, "\t")
		if ok && strings.HasPrefix(meta, "160000 ") {
			args = append(args, ":(exclude,literal)"+path)
		}
	}
	_, err = RunGit(worktree, args...)
	return err
}

// ApplyPatch applies a patch to a checkout's working tree, and is both
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
func ApplyPatch(repoTop, patch string) error {
	cmd := hostgit.Command(context.Background(), repoTop, "apply", "--whitespace=nowarn")
	cmd.Stdin = strings.NewReader(patch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s", strings.TrimSpace(string(out)))
	}
	return nil
}

// PatchedFile is one file of an applied patch, read from the real checkout
// either side of `git apply`. Exists distinguishes an empty file from one the
// patch created or removed.
type PatchedFile struct {
	Path                      string
	Before, After             string
	BeforeExists, AfterExists bool
	// BeforeMode is the permission bits the file had when the patch found
	// it, zero where there was no file to have any. It is what puts a script
	// the patch deleted back executable rather than at the default: once the
	// file is gone, nothing else on disk remembers that it was one.
	// See docs/capabilities/coding-agent.md#a-turn-ends-with-what-changed.
	BeforeMode os.FileMode
	// AfterMode is the same reading taken once the patch has landed, and it
	// is the whole of a patch that changed a mode and not a byte: git
	// carries one as an `old mode`/`new mode` header with no hunk, so both
	// sides hold identical content and this pair is the only thing that
	// tells them apart. It is read here because this is the one moment the
	// mode can still be seen at all; the session changeset takes both sides
	// and undo puts the old one back.
	AfterMode os.FileMode
}

// fileSide is a file as it was at one moment: content, whether it was there
// at all, and the permission bits it had.
type fileSide struct {
	Text   string
	Exists bool
	Mode   os.FileMode
}

// ReadSides reads the given repo-relative paths from the real checkout,
// reporting a missing file as absent rather than as an error — a patch that
// creates a file has no before-content, and that is the fact the changeset
// record needs.
//
// The mode is read beside the content, by the same stat the session takes
// around its own edits. A patch is free to delete an executable script, and
// once it has there is nowhere else left to learn that it was one: taking the
// turn back would write the file out at the default mode and the next
// `./script.sh` would fail with permission denied.
func ReadSides(repoTop string, paths []string) map[string]fileSide {
	out := make(map[string]fileSide, len(paths))
	for _, p := range paths {
		full := filepath.Join(repoTop, p)
		data, err := os.ReadFile(full)
		if err != nil {
			out[p] = fileSide{}
			continue
		}
		side := fileSide{Text: string(data), Exists: true}
		if fi, statErr := os.Stat(full); statErr == nil {
			// Permission bits only: applying a patch never changed an owner
			// or a timestamp, so putting one back is not undo's to do.
			side.Mode = fi.Mode().Perm()
		}
		out[p] = side
	}
	return out
}

// PatchedFiles pairs the reads taken either side of `git apply` into one
// record per file; a file the patch created or removed is carried by its
// Exists flags. Patch paths are relative to the repository top, which is not
// where the session is standing when it was started from a subdirectory — so
// each one is re-expressed against the session's own root, the way every
// other path the user sees is.
func PatchedFiles(root, repoTop string, paths []string, before, after map[string]fileSide) []PatchedFile {
	out := make([]PatchedFile, 0, len(paths))
	for _, p := range paths {
		b, a := before[p], after[p]
		out = append(out, PatchedFile{
			Path:         DisplayPath(root, filepath.Join(repoTop, p)),
			Before:       b.Text,
			BeforeExists: b.Exists,
			BeforeMode:   b.Mode,
			After:        a.Text,
			AfterExists:  a.Exists,
			AfterMode:    a.Mode,
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
			curFile = ParseGitDiffPath(line)
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
		if p := ParseGitDiffPath(line); p != "" && !seen[p] {
			seen[p] = true
			files = append(files, p)
		}
	}
	return files
}

// ParseGitDiffPath extracts the b/ path from a "diff --git a/x b/x" line.
func ParseGitDiffPath(line string) string {
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

// maxNotedPatchPaths bounds the file list a patch note carries. The file
// count is stated beside the list either way, so a patch longer than this
// loses the names past it and nothing about its size; what it buys is that a
// mechanical change over two hundred files cannot spend a page of the
// parent's context on a list the parent would then have to summarise.
const maxNotedPatchPaths = 20

// PatchPaths renders a patch's own file list for the note the parent reads.
func PatchPaths(files []string) string {
	if len(files) <= maxNotedPatchPaths {
		return strings.Join(files, ", ")
	}
	return strings.Join(files[:maxNotedPatchPaths], ", ") +
		fmt.Sprintf(" and %d more", len(files)-maxNotedPatchPaths)
}
