package run

// The profiles shipped in the binary.
//
// They are files rather than Go values for the reason the grammar exists at
// all: a built-in written in Go and a person's written in TOML would be two
// descriptions of one thing, and only the first would ever be exercised. A
// test holds the `code` directory to the Go pipeline it replaces, so what a
// project copies to start from is the thing the runner actually runs.
// See docs/capabilities/todo.md#a-profile-says-what-the-work-is.

import (
	"embed"
	"fmt"
	"io/fs"

	"github.com/rfizzle/shhh/internal/todo"
)

//go:embed builtin
var builtins embed.FS

// BuiltinProfiles are the profiles shipped, in the order they are documented:
// the one a checkout of code has always run first, then the four furthest
// from it — a backlog of readings, one of operational tasks, one of notes,
// and a list with no run at all.
func BuiltinProfiles() []string {
	return []string{"code", "research", "ops", "notes", "checklist"}
}

// IsBuiltinProfile reports the name being one of them.
func IsBuiltinProfile(name string) bool {
	for _, b := range BuiltinProfiles() {
		if b == name {
			return true
		}
	}
	return false
}

// BuiltinProfile is the shipped profile of that name, read through the same
// loader a directory on disk goes through.
func BuiltinProfile(name string) (todo.Profile, Pipeline, error) {
	if !IsBuiltinProfile(name) {
		return todo.Profile{}, Pipeline{}, fmt.Errorf("no profile is built in under the name %q", name)
	}
	dir, err := fs.Sub(builtins, "builtin/"+name)
	if err != nil {
		return todo.Profile{}, Pipeline{}, err
	}
	return readProfile(dir, func(rel string) string {
		return "the built-in " + name + " profile's " + rel
	})
}
