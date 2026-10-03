package worktree

import (
	"path/filepath"
	"strings"
)

// DisplayPath renders a child path workspace-relative for approval cards.
//
// Both sides are resolved first because they do not always arrive resolved:
// git answers `rev-parse --show-toplevel` with the symlinks followed, while
// the session's root is whatever the reader's shell was standing in. On
// macOS that is enough on its own — a TMPDIR under /var, which is a link to
// /private/var — and any checkout reached through a link does it anywhere.
// Compared as written, the two look like different places, and a file inside
// the workspace renders as an absolute path to somewhere else.
func DisplayPath(root, p string) string {
	if root == "" {
		return p
	}
	absRoot, absPath := ResolvePath(root), ResolvePath(p)
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return p
	}
	return rel
}

// ResolvePath is p absolute with its symlinks followed as far as the
// filesystem can follow them: the whole path when it is there, its directory
// plus the name when it is not — a file a patch deleted has nothing left to
// resolve, and the directory it was in still answers.
func ResolvePath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return real
	}
	var missing []string
	for dir := abs; ; dir = filepath.Dir(dir) {
		if real, err := filepath.EvalSymlinks(dir); err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				real = filepath.Join(real, missing[i])
			}
			return real
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		missing = append(missing, filepath.Base(dir))
	}
	return abs
}
