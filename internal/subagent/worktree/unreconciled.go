package worktree

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// SeededFile is one file as a copy held it when it was seeded for a
// reconciliation: its text, and whether it was there at all.
type SeededFile struct {
	Text   string
	Exists bool
}

// ReadSeeded is a copy's file as it holds it now, in the shape it was seeded
// in.
func ReadSeeded(dir, path string) SeededFile {
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(path)))
	return SeededFile{Text: string(data), Exists: err == nil}
}

// Unreconciled is the conflicting files a copy does not hold a
// reconciliation of: any still exactly as it was seeded, and any file the
// copy's patch writes a conflict marker into. Either is a patch that must not
// land as the one result. Both a session's integration writer and a sprint's
// lane are judged by it.
func Unreconciled(dir, patch string, conflicts []string, seeded map[string]SeededFile) []string {
	var out []string
	seen := map[string]bool{}
	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, p := range conflicts {
		if ReadSeeded(dir, p) == seeded[p] {
			add(p)
		}
	}
	file := ""
	for _, line := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			file = ParseGitDiffPath(line)
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			if conflictMark(line[1:]) {
				add(file)
			}
		}
	}
	return out
}

// conflictMark is whether a line opens or closes a conflict region, as git
// marks one by default or as a lane's merge marks one (MarkerSize).
func conflictMark(line string) bool {
	for _, c := range []string{"<", ">"} {
		git := strings.Repeat(c, 7)
		if line == git || strings.HasPrefix(line, git+" ") || strings.HasPrefix(line, strings.Repeat(c, MarkerSize)) {
			return true
		}
	}
	return false
}

// PickedSide is the conflicting files a copy now holds exactly one side of:
// the landed file whole, or the lane's whole. A file kept as either side has
// dropped the other's change, which is a side picked and not a
// reconciliation. sides holds each file's landed side, then the lane's.
func PickedSide(dir string, sides map[string][2]SeededFile) []string {
	var out []string
	for p, two := range sides {
		now := ReadSeeded(dir, p)
		if now == two[0] || now == two[1] {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return out
}
