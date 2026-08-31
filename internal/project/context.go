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
// repository root, else the directory itself. Everything keyed on "this
// project" — the backlog, an offer already refused — is keyed on it, which
// is what makes those the project's rather than a session's.
func Root(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}
	for probe := abs; ; {
		if _, err := os.Stat(filepath.Join(probe, ".git")); err == nil {
			return probe
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return abs
		}
		probe = parent
	}
}

// Find returns the path and contents of the nearest project-context file,
// walking up from the working directory. The path is what the start screen
// names: a session that says what it read is a session whose system
// prompt is not a secret.
func Find() (path, content string) {
	dir, err := os.Getwd()
	if err != nil {
		return "", ""
	}
	return FindFrom(dir)
}

// FindFrom is Find from a stated directory, for the callers that have one
// and for the tests that cannot chdir.
func FindFrom(dir string) (path, content string) {
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

func FindContext() string {
	_, content := Find()
	return content
}
