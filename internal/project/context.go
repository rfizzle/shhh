package project

import (
	"os"
	"path/filepath"
)

// contextFilenames are the recognized project-context files, in precedence
// order within a directory: shhh's own file inside its state directory wins
// over the generic AGENTS.md convention. The state directory is a directory
// now — the backlog and the skills live in it — so the context file moved
// inside it; a checkout still holding the old single file is reported by
// the doctor rather than read here
// (docs/capabilities/configuration.md#a-migration-is-a-doctor-check).
var contextFilenames = []string{filepath.Join(".shhh", "project.md"), "AGENTS.md"}

// StateDir is the checkout's shhh directory and ContextFile the context
// file inside it — where `shhh init --project` writes and what a session
// reads first, relative to the checkout.
const (
	StateDir    = ".shhh"
	ContextFile = ".shhh/project.md"
)

// Root is the directory a checkout's shhh state belongs to: the enclosing
// repository root; without one, the nearest ancestor that already holds a
// shhh directory; and the directory itself when there is neither. Everything
// keyed on "this project" — the backlog, an offer already refused — is
// keyed on it, which is what makes those the project's rather than a
// session's.
//
// The middle answer is what a project with no repository needs. Falling
// straight to the directory means two terminals opened two levels apart in
// one project key their state on two different directories, and the symptom
// is a backlog that is empty in one of them and full in the other, with
// nothing on screen to say why.
func Root(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}
	repo, state := nearest(abs)
	switch {
	case repo != "":
		return repo
	case state != "":
		return state
	}
	return abs
}

// InRepo reports whether dir is inside a git working tree. It is the same
// walk Root makes, asked for the half of the answer a caller that must have
// a repository needs — a run that ends in a commit, and nothing else.
func InRepo(dir string) bool {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	repo, _ := nearest(abs)
	return repo != ""
}

// nearest walks up from abs, itself included, for the closest ancestor
// holding .git and the closest holding the shhh state directory. A .git
// entry ends the walk, so a repository is never overruled by a state
// directory nearer the leaf: the two are found together only because the
// second answer is worth nothing until the first has come back empty.
//
// The state directory must be a directory. A checkout still holding the old
// single-file .shhh is a doctor migration, and reading it as a root would
// key a project on a file's parent for as long as the migration is unmade
// (docs/capabilities/configuration.md#a-migration-is-a-doctor-check).
func nearest(abs string) (repo, state string) {
	for probe := abs; ; {
		if _, err := os.Stat(filepath.Join(probe, ".git")); err == nil {
			return probe, state
		}
		if state == "" {
			if info, err := os.Stat(filepath.Join(probe, StateDir)); err == nil && info.IsDir() {
				state = probe
			}
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return "", state
		}
		probe = parent
	}
}

// FindFrom returns the path and contents of the nearest project-context
// file, walking up from dir. The path is what the start screen names: a
// session that says what it read is a session whose system prompt is not a
// secret.
//
// The directory is the caller's to state. Every caller has one — a session
// its working directory, a sub-agent its worktree — and reading the process
// here instead would make the answer depend on where the binary was started
// rather than on what it was asked about.
func FindFrom(dir string) (path, content string) {
	// A caller that could not name its directory has not named the root of
	// the walk either, and walking up from "" would read the process's
	// directory while claiming to have read somewhere stated.
	if dir == "" {
		return "", ""
	}
	for {
		for _, name := range contextFilenames {
			p := filepath.Join(dir, name)
			data, err := os.ReadFile(p)
			if err == nil {
				return p, string(data)
			}
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", ""
}

// FindContextFrom is FindFrom for the callers that want only the text.
func FindContextFrom(dir string) string {
	_, content := FindFrom(dir)
	return content
}
