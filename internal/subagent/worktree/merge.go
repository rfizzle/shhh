package worktree

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/rfizzle/shhh/internal/hostgit"
)

// MergeConflict is a patch that would not apply plainly and whose three-way
// merge left a conflict region: Files are where. Nothing was written.
type MergeConflict struct{ Files []string }

func (e *MergeConflict) Error() string {
	return "the patch conflicts with the checkout, which moved since it started, in " + PatchPaths(e.Files)
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

// CheckPatch asks whether a patch applies to the checkout as it stands,
// changing nothing: the question the landing's own all-or-nothing apply
// answers, asked before a card is put up for a patch that could not land.
func CheckPatch(repoTop, patch string) error {
	_, err := hostgit.OutputWith(context.Background(), repoTop, hostgit.Options{Stdin: patch}, "apply", "--check", "--whitespace=nowarn")
	return err
}

// MergeSide is one file at one of a merge's three moments, as git would hold
// it: absent, or content with a mode.
type MergeSide struct {
	Exists bool
	Mode   string
	sha    string // the blob, where git already holds it
	Text   string
}

func (a MergeSide) same(b MergeSide) bool {
	if !a.Exists || !b.Exists {
		return a.Exists == b.Exists
	}
	return a.Mode == b.Mode && a.Text == b.Text
}

// Textual is whether a side can go through a line merge: a regular file with
// no NUL in it. A link, a submodule or a binary has no lines to merge, so a
// change on both sides of one is a conflict rather than a guess.
func (a MergeSide) Textual() bool {
	return (a.Mode == "100644" || a.Mode == "100755") && !strings.Contains(a.Text, "\x00")
}

// MergeWorktree merges a writer's work three ways over the checkout it came
// from, file by file: the base is the file at the copy's HEAD — the seed, or
// the last landing carried in — ours is the file in the checkout now, and
// theirs is the writer's, as its copy's index holds it after WorktreePatch.
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
// is approved. It is not `git apply --3way`, for the reason ApplyPatch gives.
//
// The paths in skip are left out of it altogether — neither merged, nor
// moved, nor in conflict — because they are generated, and a generated file
// is regenerated over the merge rather than merged (RegenerateOver).
// See docs/capabilities/subagents.md#a-writer-starts-from-your-tree.
func MergeWorktree(worktree, repoTop string, skip []string) (patchMerge, error) {
	raw, err := hostgit.Output(context.Background(), worktree, "diff", "--cached", "--raw", "--no-renames", "--no-abbrev", "-z")
	if err != nil {
		return patchMerge{}, err
	}
	return mergeRaw(worktree, repoTop, raw, skip)
}

// MergeKept is MergeWorktree for a patch whose copy may be gone: base is the
// commit the copy stood on, which lives in the object store the checkout and
// every copy share, and the writer's side is that commit with the patch
// applied, built in a scratch index. It is what lets a kept patch reviewed
// from its row be merged the way a finishing writer's is.
func MergeKept(repoTop, base, patch string, skip []string) (patchMerge, error) {
	theirs, err := KeptTree(repoTop, base, patch)
	if err != nil {
		return patchMerge{}, err
	}
	raw, err := hostgit.Output(context.Background(), repoTop, "diff-tree", "-r", "--raw", "--no-renames", "--no-abbrev", "-z", base+"^{tree}", theirs)
	if err != nil {
		return patchMerge{}, err
	}
	return mergeRaw(repoTop, repoTop, raw, skip)
}

// KeptTree is base with patch applied, as a tree in the shared object store,
// built in a scratch index so neither the checkout's index nor a copy's is
// touched.
func KeptTree(repoTop, base, patch string) (string, error) {
	scratch, err := os.MkdirTemp("", "shhh-kept-*")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(scratch) }()
	index := []string{"GIT_INDEX_FILE=" + filepath.Join(scratch, "index")}
	if _, err := hostgit.OutputWith(context.Background(), repoTop, hostgit.Options{Env: index}, "read-tree", base); err != nil {
		return "", err
	}
	if strings.TrimSpace(patch) != "" {
		if _, err := hostgit.OutputWith(context.Background(), repoTop, hostgit.Options{Env: index, Stdin: patch}, "apply", "--cached", "--whitespace=nowarn"); err != nil {
			return "", err
		}
	}
	tree, err := hostgit.OutputWith(context.Background(), repoTop, hostgit.Options{Env: index}, "write-tree")
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
		ours, result MergeSide
	}
	var entries []entry
	fields := strings.Split(raw, "\x00")
	for i := 0; i+1 < len(fields); i += 2 {
		head, path := strings.Fields(strings.TrimPrefix(fields[i], ":")), fields[i+1]
		if len(head) < 5 || skipped[path] {
			continue
		}
		base := MergeSide{Exists: head[0] != "000000", Mode: head[0], sha: head[2]}
		theirs := MergeSide{Exists: head[1] != "000000", Mode: head[1], sha: head[3]}
		for _, side := range []*MergeSide{&base, &theirs} {
			if side.Exists {
				if side.Text, err = hostgit.Output(context.Background(), worktree, "cat-file", "blob", side.sha); err != nil {
					return m, err
				}
			}
		}
		ours, err := CheckoutSide(filepath.Join(repoTop, filepath.FromSlash(path)))
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
	tree := func(pick func(entry) MergeSide) (string, error) {
		var lines strings.Builder
		for _, e := range entries {
			side := pick(e)
			if !side.Exists {
				fmt.Fprintf(&lines, "0 %s\t%s\x00", strings.Repeat("0", 40), e.path)
				continue
			}
			sha := side.sha
			if sha == "" {
				var err error
				if sha, err = hashBlob(worktree, side.Text); err != nil {
					return "", err
				}
			}
			fmt.Fprintf(&lines, "%s %s\t%s\x00", side.Mode, sha, e.path)
		}
		if lines.Len() > 0 {
			if _, err := hostgit.OutputWith(context.Background(), worktree, hostgit.Options{Env: index, Stdin: lines.String()}, "update-index", "-z", "--index-info"); err != nil {
				return "", err
			}
		}
		out, err := hostgit.OutputWith(context.Background(), worktree, hostgit.Options{Env: index}, "write-tree")
		return strings.TrimSpace(out), err
	}
	if _, err := hostgit.OutputWith(context.Background(), worktree, hostgit.Options{Env: index}, "read-tree", "--empty"); err != nil {
		return m, err
	}
	from, err := tree(func(e entry) MergeSide { return e.ours })
	if err != nil {
		return m, err
	}
	to, err := tree(func(e entry) MergeSide { return e.result })
	if err != nil {
		return m, err
	}
	patch, err := hostgit.Output(context.Background(), worktree, "diff-tree", "-p", "--binary", "--no-renames", "--full-index", from, to)
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

// CheckoutSide reads one file of the person's checkout as git would hold it.
// A path that is not there is absent; one that is neither a file nor a link —
// a directory where the writer has a file — is a side no merge can use, and
// reads as a mode nothing else has, so it is never the same as either other
// side and never textual.
func CheckoutSide(full string) (MergeSide, error) {
	info, err := os.Lstat(full)
	if os.IsNotExist(err) {
		return MergeSide{}, nil
	}
	if err != nil {
		return MergeSide{}, err
	}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(full)
		return MergeSide{Exists: true, Mode: "120000", Text: target}, err
	case info.Mode().IsRegular():
		data, err := os.ReadFile(full)
		mode := "100644"
		if info.Mode().Perm()&0o111 != 0 {
			mode = "100755"
		}
		return MergeSide{Exists: true, Mode: mode, Text: string(data)}, err
	}
	return MergeSide{Exists: true, Mode: "?"}, nil
}

// mergeFile is one file's three-way line merge, answering with the merged
// side and whether it came out clean. A file added or removed on either side,
// or one that is not text, has no merge short of choosing a winner, so it is
// not clean. The mode is whichever side changed it.
func mergeFile(scratch string, base, ours, theirs MergeSide) (MergeSide, bool, error) {
	merged, conflicts, textual, err := mergeLines(scratch, base, ours, theirs, nil)
	if err != nil || !textual || conflicts > 0 {
		return MergeSide{}, false, err
	}
	return merged, true, nil
}

// mergeLines is `git merge-file -p` over three sides, with args added to the
// command (the diff3 marking and the labels of a merge that keeps its
// conflict regions). It answers with the merge as git wrote it, how many
// regions it left in conflict, and whether the sides could be line-merged at
// all; where they could not the merge is empty.
func mergeLines(scratch string, base, ours, theirs MergeSide, args []string) (MergeSide, int, bool, error) {
	if !base.Exists || !ours.Exists || !theirs.Exists || !base.Textual() || !ours.Textual() || !theirs.Textual() {
		return MergeSide{}, 0, false, nil
	}
	mode := ours.Mode
	switch {
	case theirs.Mode == base.Mode:
	case ours.Mode == base.Mode:
		mode = theirs.Mode
	case ours.Mode != theirs.Mode:
		return MergeSide{}, 0, false, nil
	}
	names := make([]string, 3)
	for i, side := range []MergeSide{ours, base, theirs} {
		f, err := os.CreateTemp(scratch, "side-*")
		if err != nil {
			return MergeSide{}, 0, false, err
		}
		_, werr := f.WriteString(side.Text)
		cerr := f.Close()
		if werr != nil || cerr != nil {
			return MergeSide{}, 0, false, errors.Join(werr, cerr)
		}
		names[i] = f.Name()
	}
	cmd := hostgit.Command(context.Background(), "", append(append([]string{"merge-file", "-p"}, args...), names...)...)
	var out, errBuf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errBuf
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return MergeSide{Exists: true, Mode: mode, Text: out.String()}, 0, true, nil
	case errors.As(err, &exit) && exit.ExitCode() > 0 && exit.ExitCode() < 128:
		// The exit status is the number of conflict regions.
		return MergeSide{Exists: true, Mode: mode, Text: out.String()}, exit.ExitCode(), true, nil
	}
	return MergeSide{}, 0, false, fmt.Errorf("git merge-file: %s", strings.TrimSpace(errBuf.String()))
}

// hashBlob writes text into the repository's object store as a blob and
// answers with its id. The store is the one the checkout and every copy of it
// share, and nothing points at the blob until a landing commits it, so an
// unlanded merge leaves only a dangling object git collects on its own.
func hashBlob(dir, text string) (string, error) {
	out, err := hostgit.OutputWith(context.Background(), dir, hostgit.Options{Stdin: text}, "hash-object", "-w", "--no-filters", "--stdin")
	return strings.TrimSpace(out), err
}
