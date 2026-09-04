package cli

// Which profile this project's backlog is written in and worked under,
// resolved once per process.
//
// A profile is a directory: the table that says what an item is called and
// which fields it carries, and the wordings each step of its run is
// instructed with. Three places can hold one, and they are read most
// specific first — the checkout's own, then the person's by name, then the
// ones built into the binary — for the reason the wordings are: what is
// true of a repository travels with the repository, and "where did this word
// come from" has to have one answer for both.
//
// It is held rather than resolved per reader because the two halves have to
// agree. A screen drawing an item's fields from one profile while the runner
// spends turns against another would be showing a backlog nobody has.
// See docs/capabilities/todo.md#a-profile-says-what-the-work-is.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/todo"
	"github.com/rfizzle/shhh/internal/todo/run"
)

// todoProfileDirName is what a person's profiles are kept under, beside their
// settings: one directory per profile, named for it, the way the wordings sit
// in a directory beside the same file.
const todoProfileDirName = "todo"

// backlogProfile is the answer: the vocabulary, the run, where they were read
// from, and the refusal where the name resolved to nothing. A failure is
// carried rather than returned because every reader asks for the profile and
// only the process's entry point can decide what a bad one costs — the
// session refuses to start, and the doctor reports it as a row.
type backlogProfile struct {
	words    todo.Profile
	pipeline run.Pipeline
	// from is where it came from, as a surface prints it: a path for a
	// directory somebody wrote, and the word for one out of the binary.
	from string
	// dir is the directory it was read from, and empty for one out of the
	// binary — which is the difference between a profile whose files a
	// reader can open and one whose files are not on the machine.
	dir string
	err error
}

// name is what the profile is called, which is what a surface names beside
// the root and what a refusal made in its terms says it came from.
func (b backlogProfile) name() string { return b.words.Name }

// builtinBacklogProfile is the answer before anything has resolved one: the
// profile a checkout of code has always been written in, out of the values
// this binary holds rather than the copy of them it ships as files. It is
// what a command that never read a settings file gets.
func builtinBacklogProfile() backlogProfile {
	return backlogProfile{words: todo.BuiltinCode(), pipeline: run.BuiltinCode(), from: builtinProfileFrom}
}

// profileHeld is what this process resolved, and the built-in answer until
// something states one.
var profileHeld struct {
	mu   sync.Mutex
	read *backlogProfile
}

// backlogProfileFor resolves the profile these settings name and holds it, so
// that every reader after it gets the same answer. It is called once, where
// the settings are read for the whole command tree.
func backlogProfileFor(cfg config.Config) backlogProfile {
	p := resolveBacklogProfile(cfg.Todo.Profile)
	profileHeld.mu.Lock()
	profileHeld.read = &p
	profileHeld.mu.Unlock()
	return p
}

// heldBacklogProfile is what was resolved, or the built-in `code` profile
// where nothing has resolved one — a test with no settings file, and any
// caller reached without going through the command tree.
func heldBacklogProfile() backlogProfile {
	profileHeld.mu.Lock()
	defer profileHeld.mu.Unlock()
	if profileHeld.read == nil {
		p := builtinBacklogProfile()
		profileHeld.read = &p
	}
	return *profileHeld.read
}

// backlogProfileIs is the answer this process holds. It is a variable so a
// test can state one rather than writing a directory to imply it; nothing but
// a test ever assigns it.
var backlogProfileIs = heldBacklogProfile

// todoProfile is the vocabulary a checkout's backlog is written in, handed to
// every reader of it rather than reached for, because there is no default:
// which words an item carries is the project's answer and not the tool's.
func todoProfile() todo.Profile { return backlogProfileIs().words }

// todoPipeline is the steps a run of that backlog takes. It is asked for
// beside the vocabulary and for the same reason: the two are halves of one
// answer about what the work is, and a reader that took one from the profile
// and the other from a constant would be working an item under a run its own
// backlog does not have.
func todoPipeline() run.Pipeline { return backlogProfileIs().pipeline }

// resolveBacklogProfile reads the profile of that name out of the first of
// the three places that holds one.
//
// The checkout's own comes first and needs no name: a repository carries at
// most one profile, at a path every clone of it has, and the settings key is
// how a project that keeps its profile somewhere else names which of the
// others it means. It is a trust resource like the wordings, so an untrusted
// checkout's profile is not read at all and the person's or the built-in one
// stands — the session says what it withheld on its way in.
func resolveBacklogProfile(name string) backlogProfile {
	if name == "" {
		name = defaultProfileName
	}
	if dir := projectTodoProfile(); dir != "" {
		return loadedBacklogProfile(dir, project.TodoProfileDir+"/")
	}
	mine := userProfileDir(name)
	if info, err := os.Stat(mine); err == nil && info.IsDir() {
		got := loadedBacklogProfile(mine, shortPath(mine)+string(filepath.Separator))
		// A profile of the person's own is named by its directory, because
		// that is the name the settings key holds. A table that calls itself
		// something else would leave the key naming one thing and every
		// sentence about the profile naming another.
		if got.err == nil && got.name() != name {
			return backlogProfile{err: fmt.Errorf("todo.profile names %q and the profile at %s calls itself %q; rename the directory or the name in its %s",
				name, shortPath(mine), got.name(), run.ProfileFile)}
		}
		return got
	}
	if !run.IsBuiltinProfile(name) {
		return backlogProfile{err: unknownProfile(name, mine)}
	}
	words, pipeline, err := run.BuiltinProfile(name)
	return backlogProfile{words: words, pipeline: pipeline, from: builtinProfileFrom, err: err}
}

// defaultProfileName is the profile a project that says nothing runs: the one
// a checkout of code has always been written in, and builtinProfileFrom is
// where a surface says a shipped profile came from.
const (
	defaultProfileName = "code"
	builtinProfileFrom = "built in"
)

// loadedBacklogProfile is one directory read, kept whole or refused whole. A
// profile that will not load is not half applied: a run built on the steps of
// one profile and the fields of another is the failure the validator exists
// to stop, and it would be this loader that introduced it.
func loadedBacklogProfile(dir, from string) backlogProfile {
	words, pipeline, err := run.LoadProfile(dir)
	if err != nil {
		return backlogProfile{err: err}
	}
	return backlogProfile{words: words, pipeline: pipeline, from: from, dir: dir}
}

// unknownProfile is the refusal for a name nothing answers to. It names all
// three places rather than the last one it looked in, because the person who
// typed the name is deciding where to put the directory.
func unknownProfile(name, mine string) error {
	return fmt.Errorf("todo.profile names %q, and there is no profile by that name: this checkout has no %s directory, there is no %s, and the profiles built in are %s",
		name, project.TodoProfileDir, shortPath(mine), strings.Join(run.BuiltinProfiles(), ", "))
}

// userProfileDir is where a person keeps a profile of their own: a directory
// beside the settings file, named for the profile, so everything they wrote
// for shhh sits in one place and a profile travels with the settings rather
// than with whichever directory a session was opened in.
func userProfileDir(name string) string {
	return filepath.Join(filepath.Dir(config.WritePath()), todoProfileDirName, name)
}

// projectTodoProfile is this checkout's own profile directory, and "" where
// there is none this session may read. The root and the answer about it come
// from one reading, so a session cannot load a checkout's profile under one
// answer and report the other.
func projectTodoProfile() string {
	t := projectTrust()
	if t.Root == "" || !t.Allows() {
		return ""
	}
	dir := filepath.Join(t.Root, filepath.FromSlash(project.TodoProfileDir))
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return ""
	}
	return dir
}
