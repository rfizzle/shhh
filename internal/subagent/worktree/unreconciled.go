package worktree

import (
	"os"
	"path/filepath"
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

// conflictMark is whether a line opens or closes a conflict region as git
// marks one.
func conflictMark(line string) bool {
	for _, git := range []string{"<<<<<<<", ">>>>>>>"} {
		if line == git || strings.HasPrefix(line, git+" ") {
			return true
		}
	}
	return false
}
