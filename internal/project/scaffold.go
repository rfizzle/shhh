package project

// Scaffolding the state directory. A checkout with no `.shhh` of its own
// tells the model nothing about itself, and the fix is one small file — but
// it is a file, so it is written when someone asks for it and never on the
// way past
// (docs/capabilities/configuration.md#project-context-is-opt-in-and-lives-with-the-project).
//
// The write lives here rather than beside the command that used to own it,
// because two surfaces ask for it now: `shhh init --project` and the start
// screen's offer.

import (
	"fmt"
	"os"
	"path/filepath"
)

// contextTemplate is what a scaffolded context file says: what the file is
// for, and examples of the kind of fact worth putting in it. It is comments
// all the way down, so a file nobody edits adds nothing to the prompt.
const contextTemplate = `# .shhh/project.md — project-local context for shhh
# This file is appended to the LLM system prompt when running shhh
# from this directory (or any subdirectory). Use it to describe your
# project's tooling, conventions, and common workflows.

# Examples:
# - This project uses Docker Compose for services (docker compose up -d)
# - Run tests with: make test
# - Deployed via Terraform in infra/
# - Prefer ripgrep (rg) over grep
# - Database migrations: goose -dir migrations up
`

// ScaffoldPaths are what scaffolding root creates, in the order it creates
// them, written the way a reader standing in from would write them — what a
// card lists before it asks. The two directories are usually the same one;
// where they are not, a bare `.shhh/` would name a directory the write is
// not going to touch.
func ScaffoldPaths(root, from string) []string {
	return []string{
		relativeTo(from, filepath.Join(root, StateDir)) + "/",
		relativeTo(from, filepath.Join(root, filepath.FromSlash(ContextFile))),
	}
}

// NeedsScaffold reports a directory the offer can be made in: one where the
// model has been told nothing about the project at all — no context file of
// any recognised name, here or up the tree — and where no file is sitting
// in the state directory's place.
//
// It is deliberately the whole question rather than "is there a project.md":
// a checkout carrying an AGENTS.md or a CLAUDE.md has already said what it
// is, and shhh's own file wins inside a directory, so offering there would
// replace what the model is reading with a template that says nothing.
func NeedsScaffold(dir string) bool {
	if st, err := os.Stat(filepath.Join(dir, StateDir)); err == nil && !st.IsDir() {
		return false
	}
	// A context file that exists and says nothing is not read into the
	// prompt, but it is still a file, and Scaffold will not write over one.
	// Without this the offer appears and then refuses itself with "already
	// exists", which reads as a bug in the offer rather than as the file
	// nobody filled in.
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(ContextFile))); err == nil {
		return false
	}
	path, _ := FindFrom(dir)
	return path == ""
}

// Scaffold writes the context file under dir and returns its path. A
// checkout where .shhh is still the old single file is left alone and
// pointed at the doctor: writing a directory over it is impossible, and
// replacing it would lose what it says.
func Scaffold(dir string) (string, error) {
	state := filepath.Join(dir, StateDir)
	context := filepath.Join(dir, ContextFile)
	if st, err := os.Stat(state); err == nil && !st.IsDir() {
		return "", fmt.Errorf("%s is a file from an older layout; run `shhh doctor` to move it to %s", StateDir, ContextFile)
	}
	if _, err := os.Stat(context); err == nil {
		return "", fmt.Errorf("%s already exists", ContextFile)
	}
	if err := os.MkdirAll(state, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(context, []byte(contextTemplate), 0o644); err != nil {
		return "", err
	}
	return context, nil
}
