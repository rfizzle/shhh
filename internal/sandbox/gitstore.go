package sandbox

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// gitStore is what a contained command must not be able to change about the
// repository a directory is in: the paths git reads a program to run from.
// A git write, the tree reading between rounds and the read-only git verbs
// all run git on the host, outside containment, and each of them runs
// whatever these paths name — a hook on a commit, an fsmonitor or a clean
// filter on a status. A command the mechanism bounded that could write one
// of them would have written a program the next host-side git runs as the
// person, so they are read-only to a contained command while the rest of the
// store stays writable: staging, a branch, a fetch are ordinary work.
// See docs/capabilities/containment.md#the-repositorys-own-programs-are-read-only.
type gitStore struct {
	// stores are the repository's store — the directory that holds its
	// config, its objects and its hooks — as git named it and as the .git
	// directory found by walking up names it, which differ only where
	// something redirected the store. A write grant at or inside one is what
	// unmasks every entry: the grant is the store, not the file in it, which
	// is the shape the credential stores have too.
	stores []string
	// entries are the paths git reads a program from, symlinks resolved as
	// far as they exist. A path that is not there is still an entry: git
	// consults several that are normally absent, and one of them —
	// commondir — redirects the whole store.
	entries []string
	// links are entries that are themselves symbolic links, spelled as the
	// link rather than as its target. Seatbelt judges the link by that name
	// when it is removed; a mount cannot hold one at all.
	links []string
}

// gitStoreTimeout bounds the one git call a store reading costs. It runs on
// every contained command, so a git that hangs must cost a command its mask
// check rather than its start.
const gitStoreTimeout = 2 * time.Second

// readGitStore finds the paths the repository dir is in runs programs from.
// Git is asked, because core.hooksPath can move the hooks anywhere and a
// linked worktree's store is somewhere else entirely; and the .git found by
// walking up is read as well, whatever git said, because git's answer is
// only as good as the store it read. A contained command that broke the
// store for one command — a HEAD that is not a ref, a HEAD that is a pipe
// git waits on — would otherwise have git answer nothing, nothing masked,
// and the config written by the next command and read once HEAD is put
// back. So a failed answer falls back to the store's ordinary layout rather
// than to no mask, and so does a git too old to answer in absolute paths.
//
// The entries:
//
//   - config, and config.worktree where a worktree has one: every program
//     git names in configuration — core.hooksPath, core.fsmonitor,
//     gpg.program, the filter and diff drivers — is read from these.
//   - the hooks directory, where core.hooksPath puts it.
//   - info, which holds info/attributes: the attribute file inside the store
//     that selects a filter or a diff driver for a path.
//   - commondir, in the git directory: a file there redirects the
//     store, config and hooks included, to wherever it names, and git reads
//     it in an ordinary checkout as readily as in a linked worktree.
//   - the commondir and config.worktree of every linked worktree the store
//     keeps, which redirect those checkouts the same way.
//   - the .git file of a checkout whose store is elsewhere, which names the
//     store; and .git in dir itself when dir is below the checkout's top,
//     since git finds that one first and would take it as the repository.
func readGitStore(dir string) (gitStore, bool) {
	d, ok := existingDir(dir)
	if !ok {
		return gitStore{}, false
	}
	var g gitStore
	var candidates []string
	store := func(gitDir, common, hooks string) {
		if !slices.Contains(g.stores, common) {
			g.stores = append(g.stores, common)
		}
		candidates = append(candidates,
			filepath.Join(common, "config"),
			hooks,
			filepath.Join(common, "info"),
			filepath.Join(gitDir, "commondir"),
			filepath.Join(gitDir, "config.worktree"),
		)
		if linked, err := filepath.Glob(filepath.Join(common, "worktrees", "*")); err == nil {
			for _, w := range linked {
				candidates = append(candidates, filepath.Join(w, "commondir"), filepath.Join(w, "config.worktree"))
			}
		}
	}
	gitDir, common, hooks, asked := askGit(d)
	if asked {
		store(gitDir, common, hooks)
	}
	top, dotgit, found := findDotGit(d)
	if found {
		if info, err := os.Lstat(dotgit); err == nil {
			switch {
			case info.IsDir():
				if resolved, err := filepath.EvalSymlinks(dotgit); err == nil {
					store(resolved, resolved, filepath.Join(resolved, "hooks"))
				}
			case info.Mode().IsRegular():
				candidates = append(candidates, dotgit)
			}
		}
		inStore := within(d, dotgit) || (asked && within(d, gitDir)) ||
			slices.ContainsFunc(g.stores, func(s string) bool { return within(d, s) })
		if top != d && !inStore {
			candidates = append(candidates, filepath.Join(d, ".git"))
		}
	}
	if !asked && !found {
		return gitStore{}, false
	}
	for _, c := range candidates {
		g.add(c)
	}
	return g, true
}

// askGit is git's own answer about the repository d is in, in absolute
// paths with their directories resolved; asked is false wherever git could
// not give one.
func askGit(d string) (gitDir, common, hooks string, asked bool) {
	ctx, cancel := context.WithTimeout(context.Background(), gitStoreTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", d, "rev-parse", "--path-format=absolute",
		"--git-dir", "--git-common-dir", "--git-path", "hooks").Output()
	if err != nil {
		return "", "", "", false
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) != 3 {
		return "", "", "", false
	}
	gitDir, common, hooks = lexical(lines[0]), lexical(lines[1]), lexical(lines[2])
	if gitDir == "" || common == "" || hooks == "" {
		return "", "", "", false
	}
	return gitDir, common, hooks, true
}

// add records one candidate: the path as far as it resolves, and the link
// spelling beside it where the path is a symbolic link.
func (g *gitStore) add(path string) {
	resolved := path
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 && !slices.Contains(g.links, path) {
			g.links = append(g.links, path)
		}
		if r, err := filepath.EvalSymlinks(path); err == nil {
			resolved = r
		}
	}
	if !slices.Contains(g.entries, resolved) {
		g.entries = append(g.entries, resolved)
	}
}

// lexical is an absolute path from git with its existing directories'
// symlinks resolved, so it compares with the spec's other resolved paths;
// the last element is left as git spelled it, because it may not exist.
func lexical(path string) string {
	if !filepath.IsAbs(path) {
		return ""
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return ""
	}
	return filepath.Join(parent, filepath.Base(path))
}

// existingDir is the nearest existing directory at or above path, resolved.
func existingDir(path string) (string, bool) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	for p := abs; ; p = filepath.Dir(p) {
		if info, err := os.Stat(p); err == nil && info.IsDir() {
			r, err := filepath.EvalSymlinks(p)
			return r, err == nil
		}
		if filepath.Dir(p) == p {
			return "", false
		}
	}
}

// findDotGit walks up from dir to the first directory holding a .git entry,
// the way git's own discovery does.
func findDotGit(dir string) (top, dotgit string, found bool) {
	for p := dir; ; p = filepath.Dir(p) {
		candidate := filepath.Join(p, ".git")
		if _, err := os.Lstat(candidate); err == nil {
			return p, candidate, true
		}
		if filepath.Dir(p) == p {
			return "", "", false
		}
	}
}

// granted reports whether a write grant covers the store: a grant at or
// inside the store itself, or at or inside the entry, is the person having
// asked for a contained command to write there. The workspace counts for the
// reason it does beside the credential stores — a session opened inside the
// store is working in it.
func (g gitStore) granted(entry string, write []string, workspace string) bool {
	covers := func(path string) bool {
		return within(path, entry) || slices.ContainsFunc(g.stores, func(s string) bool { return within(path, s) })
	}
	if workspace != "" && covers(workspace) {
		return true
	}
	return slices.ContainsFunc(write, covers)
}

// maskGitStore adds the workspace's repository to the spec: its program
// paths read-only to a contained command, where a write grant would
// otherwise reach them. It runs after the grants, because what it masks is
// decided by them — an entry outside every grant is already read-only, and
// one the person granted is theirs to write — the way the credential stores'
// mask is.
//
// The two mechanisms hold it differently, and not equally.
//
// Both have to stop the directories above an entry being renamed: .git moved
// aside, a hook written into it under a name no rule covers, and moved back.
// So every directory between the outermost grant and an entry is pinned —
// bound over itself under bubblewrap, where a mount point cannot be renamed
// or removed, and denied by its literal name under Seatbelt, which refuses
// the rename and leaves creating files inside it alone.
//
// Seatbelt judges a write by the path it names, so a deny is a rule about a
// name: it holds whether the path exists or not. Every entry goes in, and
// every link beside its target.
//
// Bubblewrap can only mount over a path that exists, and a bind over a link
// lands on its target. So the existing entries are bound read-only, and an
// entry that does not exist, or a link, cannot be held; those are the part
// of the boundary only Seatbelt draws, and the report names them.
// See docs/capabilities/containment.md#the-repositorys-own-programs-are-read-only.
func (s *spec) maskGitStore(mechanism string) {
	switch mechanism {
	case "bwrap", "sandbox-exec":
	default:
		return // nothing is wrapping the command; nothing is masked
	}
	g, ok := readGitStore(s.workspace)
	if !ok {
		return
	}
	enclosing := func(path string) string {
		best := ""
		for _, w := range s.write {
			if within(path, w) && len(w) > len(best) {
				best = w
			}
		}
		return best
	}
	// The pins run up to the outermost grant, not the nearest: a directory
	// between the workspace and a wider grant above it (an /add-dir of the
	// parent) can be renamed as readily as .git can, and the workspace moved
	// aside is a checkout rebuilt under its name.
	outermost := func(path string) string {
		best := ""
		for _, w := range s.write {
			if within(path, w) && (best == "" || len(w) < len(best)) {
				best = w
			}
		}
		return best
	}
	masked := func(path string) bool {
		return enclosing(path) != "" && !g.granted(path, s.write, s.workspace)
	}
	hold := func(path string) {
		if !slices.Contains(s.gitReadOnly, path) {
			s.gitReadOnly = append(s.gitReadOnly, path)
		}
		grant := outermost(path)
		for p := filepath.Dir(path); p != grant && within(p, grant); p = filepath.Dir(p) {
			if !slices.Contains(s.gitPinned, p) {
				s.gitPinned = append(s.gitPinned, p)
			}
		}
	}
	for _, e := range g.entries {
		if !masked(e) {
			continue
		}
		// Stat rather than Lstat: a bind needs a source, and a dangling link
		// at an entry would fail every wrap rather than hold anything.
		if _, err := os.Stat(e); err == nil || mechanism == "sandbox-exec" {
			hold(e)
		} else {
			s.gitUnheld = append(s.gitUnheld, e)
		}
	}
	for _, l := range g.links {
		if !masked(l) {
			continue
		}
		if mechanism == "sandbox-exec" {
			hold(l)
		} else if !slices.Contains(s.gitUnheld, l) {
			s.gitUnheld = append(s.gitUnheld, l)
		}
	}
	// A bind over a directory has to come before the binds inside it, or it
	// would cover them.
	slices.SortFunc(s.gitPinned, func(a, b string) int { return len(a) - len(b) })
}

// GitStoreWords is the one phrasing every report uses for the repository's
// program paths: the words when a contained command has them read-only, and
// "" when nothing is masked — no mechanism, no repository, or the person
// granted the store. It resolves the policy the way a wrap does, so a report
// says what the next command will run under.
// See docs/capabilities/containment.md#what-is-reported-is-what-is-in-force.
func GitStoreWords(avail Availability, p Policy) string {
	if !avail.OK {
		return ""
	}
	s, err := resolvePolicy(p, avail.Mechanism)
	if err != nil || len(s.gitReadOnly) == 0 {
		return ""
	}
	return gitStoreWords
}

// gitStoreWords is what the card, /status and the doctor row say.
const gitStoreWords = "read-only git hooks and config"

// GitStoreOf reports the repository store dir would unmask if it were
// granted: dir is the store itself or inside it, or it is one of the paths
// git reads a program from, wherever core.hooksPath put them. The working
// scope classifies such a directory as sensitive for the reason it
// classifies a credential store so — a grant is what makes it writable to a
// contained command, and only a person answers for that.
func GitStoreOf(dir string) (string, bool) {
	d, ok := existingDir(dir)
	if !ok {
		return "", false
	}
	g, ok := readGitStore(d)
	if !ok {
		return "", false
	}
	for _, s := range g.stores {
		if within(d, s) {
			return s, true
		}
	}
	if len(g.stores) == 0 {
		return "", false
	}
	for _, e := range g.entries {
		if within(d, e) {
			return g.stores[0], true
		}
	}
	return "", false
}
