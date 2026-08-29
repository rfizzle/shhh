// Package scope holds a session's working scope: the directories the work is
// allowed to reach (docs/capabilities/containment.md#scope-is-the-set-of-directories-the-work-may-reach).
// A session starts scoped to the directory it was
// opened in, which is the right default and the wrong one the moment the work
// spills over — a config directory the project reads, a sibling checkout, a
// vendored dependency outside the tree. Before this existed the only answers
// were to edit the config file and restart, or to watch contained commands
// fail on paths that were plainly part of the job.
//
// Adding a directory is a permission grant, and it goes through the same
// machinery every other grant does: the user types /add-dir, or answers the
// card that says which directory an action reaches outside the scope. What
// the scope holds is what OS-level containment makes writable (the sandbox's
// write grants follow it), and what file edits may touch without asking
// again.
//
// Two classes of directory never come along for the ride. A path inside the
// sandbox's deny mask — the credential stores and shhh's own state — is
// Refused: it cannot be granted at all, by any key, because the mask it sits
// behind cannot be disabled. A path that is sensitive without being masked —
// a home directory, a system root, another tool's credential store — is
// Sensitive: it can be granted, but only by a person answering for it, never
// by a permissive mode or the classifier.
package scope

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/rfizzle/shhh/internal/sandbox"
)

// Scope is one session's working scope. It is shared by the surfaces that
// read it (the approval card, the slash command) and the runner closures that
// wrap commands off the UI goroutine, so every read and write is guarded.
type Scope struct {
	mu    sync.RWMutex
	root  string
	added []string
}

// New builds the scope for a session rooted at root, with dirs already
// granted (the config list and --add-dir flags). A root that cannot be
// resolved is an error; a listed directory that cannot be is reported and
// skipped, because a stale entry in a config file should not stop a session
// starting.
func New(root string, dirs ...string) (*Scope, []error) {
	var problems []error
	resolved, err := resolveDir(root)
	if err != nil {
		return nil, []error{fmt.Errorf("working scope: cannot resolve %s: %w", root, err)}
	}
	s := &Scope{root: resolved}
	for _, d := range dirs {
		if _, err := s.Add(d); err != nil {
			problems = append(problems, err)
		}
	}
	return s, problems
}

// Root is the directory the session was opened in — the one part of the scope
// that is never granted and never dropped.
func (s *Scope) Root() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.root
}

// Dirs are the granted directories, in the order they were added.
func (s *Scope) Dirs() []string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string(nil), s.added...)
}

// All is the whole scope: the root first, then everything granted since.
func (s *Scope) All() []string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string{s.root}, s.added...)
}

// Contains reports whether path is inside the scope. A path that does not
// exist yet is answered by the nearest directory that does, so a file a
// command is about to create is in scope exactly when its directory is.
func (s *Scope) Contains(path string) bool {
	if s == nil {
		return false
	}
	dir, err := resolveDir(path)
	if err != nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.holds(dir)
}

// holds answers membership for an already-resolved directory; callers hold
// the lock.
func (s *Scope) holds(dir string) bool {
	if within(dir, s.root) {
		return true
	}
	for _, d := range s.added {
		if within(dir, d) {
			return true
		}
	}
	return false
}

// Outside names the directories these paths reach that the scope does not
// hold, deduplicated and in the order the paths were given. A path shhh
// cannot resolve at all contributes nothing: the resolver that produced it
// has already said what it could not account for, and inventing a directory
// here would put a name on the card that nothing verified.
func (s *Scope) Outside(paths ...string) []string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []string
	seen := map[string]bool{}
	for _, p := range paths {
		dir, err := resolveDir(p)
		if err != nil || s.holds(dir) || seen[dir] {
			continue
		}
		seen[dir] = true
		out = append(out, dir)
	}
	return out
}

// Add grants a directory to the scope and returns it as resolved. A path
// inside the deny mask is refused outright; a path already in scope is
// reported as such rather than added twice.
func (s *Scope) Add(path string) (string, error) {
	if s == nil {
		return "", fmt.Errorf("this session has no working scope")
	}
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("no directory given")
	}
	// An explicit grant names the directory itself: a path that is a file, or
	// one that is not there at all, is a typo rather than an instruction to
	// grant whatever encloses it.
	dir, err := resolveExact(expandHome(path))
	if err != nil {
		return "", fmt.Errorf("cannot add %s: no such directory", path)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return "", fmt.Errorf("cannot add %s: it is not a directory", path)
	}
	if class, reason := Classify(dir); class == Refused {
		return "", fmt.Errorf("cannot add %s: %s", dir, reason)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.holds(dir) {
		return dir, ErrAlreadyInScope
	}
	s.added = append(s.added, dir)
	return dir, nil
}

// ErrAlreadyInScope is returned by Add for a directory the scope already
// holds. It is a state, not a failure: the caller says so and carries on.
var ErrAlreadyInScope = fmt.Errorf("already in the working scope")

// Drop takes a granted directory back out. The root cannot be dropped: a
// session with no directory to work in is not a state worth reaching.
func (s *Scope) Drop(path string) (string, bool) {
	if s == nil {
		return "", false
	}
	dir, err := resolveExact(expandHome(path))
	if err != nil {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, d := range s.added {
		if d == dir {
			s.added = append(s.added[:i:i], s.added[i+1:]...)
			return d, true
		}
	}
	return "", false
}

// Clear drops every granted directory and reports what went.
func (s *Scope) Clear() []string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	gone := s.added
	s.added = nil
	return gone
}

// Class is how much answering for a directory is worth asking about.
type Class int

const (
	// Ordinary is a directory a permissive mode may grant on the user's
	// behalf, like any other auto-approved action.
	Ordinary Class = iota
	// Sensitive is a directory only a person may grant: no mode, session
	// grant, or classifier ever adds one automatically.
	Sensitive
	// Refused is a directory the scope will not hold at all — it sits behind
	// the containment deny mask, which cannot be disabled.
	Refused
)

func (c Class) String() string {
	switch c {
	case Sensitive:
		return "sensitive"
	case Refused:
		return "refused"
	}
	return "ordinary"
}

// Classify says what kind of directory this is, and why in the words a card
// or a command prints after a dash.
func Classify(dir string) (Class, string) {
	resolved, err := resolveExact(expandHome(dir))
	if err != nil {
		// A directory that is not there yet is classified by the name it
		// would have; nothing under it can be resolved, so the comparison is
		// with the path as written.
		if abs, aerr := filepath.Abs(expandHome(dir)); aerr == nil {
			resolved = abs
		} else {
			resolved = filepath.Clean(dir)
		}
	}
	// The mask's own entries are resolved strictly: a deny path that does not
	// exist masks nothing, and resolving it loosely would hand back its
	// parent — which is how "no ~/.ssh here" turns into "the home directory
	// is masked".
	for _, denied := range sandbox.DenyPaths() {
		d, err := resolveExact(denied)
		if err != nil {
			continue
		}
		if within(resolved, d) {
			return Refused, "contained commands mask " + d + ", and the mask cannot be disabled"
		}
	}
	for _, s := range sensitivePaths() {
		if resolved == s {
			return Sensitive, "granting " + s + " puts everything under it in scope"
		}
	}
	for _, s := range credentialPaths() {
		c, err := resolveExact(s)
		if err != nil {
			continue
		}
		if within(resolved, c) {
			return Sensitive, c + " holds credentials"
		}
	}
	return Ordinary, ""
}

// sensitivePaths are the directories that are sensitive because of how much
// they hold rather than what: a home directory, a filesystem root, a system
// tree. Granting one is granting everything beneath it, so it is a decision
// for a person and never for a mode.
func sensitivePaths() []string {
	out := []string{string(filepath.Separator)}
	if home, err := os.UserHomeDir(); err == nil {
		out = append(out, home,
			filepath.Join(home, ".config"),
			filepath.Join(home, ".local"),
		)
	}
	if runtime.GOOS == "darwin" {
		out = append(out, "/System", "/Library", "/Applications", "/Users", "/Volumes")
	}
	out = append(out, "/etc", "/usr", "/bin", "/sbin", "/lib", "/var", "/opt", "/boot", "/root", "/home")
	seen := map[string]bool{}
	var uniq []string
	for _, p := range out {
		c := filepath.Clean(p)
		if !seen[c] {
			seen[c] = true
			uniq = append(uniq, c)
		}
	}
	return uniq
}

// credentialPaths are the credential stores the containment deny mask does
// not already cover. The mask hides ~/.ssh, ~/.aws and ~/.config/gh outright;
// these are the ones a working session might genuinely need — a kubeconfig, a
// registry login — so they are grantable, but only by the person whose
// credentials they are.
func credentialPaths() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return []string{
		filepath.Join(home, ".gnupg"),
		filepath.Join(home, ".kube"),
		filepath.Join(home, ".docker"),
		filepath.Join(home, ".azure"),
		filepath.Join(home, ".config", "gcloud"),
		filepath.Join(home, ".password-store"),
		filepath.Join(home, ".gem"),
		filepath.Join(home, ".netrc"),
		filepath.Join(home, ".secrets"),
	}
}

// Describe is how a scope reads in a status line: the root, and what has been
// added to it.
func (s *Scope) Describe() string {
	if s == nil {
		return "no working scope"
	}
	dirs := s.Dirs()
	if len(dirs) == 0 {
		return s.Root()
	}
	return fmt.Sprintf("%s and %d added %s", s.Root(), len(dirs), plural(len(dirs), "directory", "directories"))
}

// Sorted is the whole scope in path order, for the reports that list it
// rather than replaying how it was granted.
func (s *Scope) Sorted() []string {
	all := s.All()
	sort.Strings(all)
	return all
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// resolveDir turns a path into the existing directory that answers for it:
// the path itself when it is a directory, its parent when it is a file, and
// the nearest existing ancestor when nothing is there yet. Symlinks are
// resolved, so a link cannot smuggle a path into the scope under another
// name — the same rule containment applies to its own grants.
func resolveDir(path string) (string, error) {
	abs, err := filepath.Abs(expandHome(path))
	if err != nil {
		return "", err
	}
	for dir := abs; ; dir = filepath.Dir(dir) {
		info, err := os.Lstat(dir)
		switch {
		case err == nil && info.IsDir():
			return filepath.EvalSymlinks(dir)
		case err == nil && info.Mode()&os.ModeSymlink != 0:
			// A symlink to a directory answers as its target; one to a file
			// answers as the target's directory.
			target, terr := filepath.EvalSymlinks(dir)
			if terr != nil {
				return "", terr
			}
			if info, err := os.Stat(target); err == nil && info.IsDir() {
				return target, nil
			}
			return filepath.Dir(target), nil
		case err == nil:
			return filepath.EvalSymlinks(filepath.Dir(dir))
		}
		if parent := filepath.Dir(dir); parent != dir {
			continue
		}
		return "", fmt.Errorf("no existing directory in %s", abs)
	}
}

// resolveExact resolves a path that must already exist, following symlinks
// and never standing in a parent for it. It is what a grant and a mask entry
// are read with; resolveDir's walk up the tree is for the paths an action
// names, which may not be there yet.
func resolveExact(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

// expandHome resolves a leading ~ so a typed path means what it looks like.
func expandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~"+string(filepath.Separator)) {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~"))
}

// within reports whether path is dir or inside it; both must already be
// absolute and symlink-resolved.
func within(path, dir string) bool {
	return path == dir || strings.HasPrefix(path, dir+string(filepath.Separator))
}
