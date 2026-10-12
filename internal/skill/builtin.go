package skill

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
)

// The skills the binary carries.
//
// A model asked to set shhh up in a checkout knows nothing of shhh's own
// files, and the answer it reaches for is a search of the disk. These are
// shhh's instructions for its own files, listed and loaded through the same
// three tiers as any skill, so the model learns them the way it learns
// anything else: one list line standing, the body on demand. They are
// several narrow skills rather than one broad one because the list line is
// what every session pays. A file skill of the same name wins, the way a
// wording file replaces its built-in.
// See docs/capabilities/skills.md#shhh-carries-skills-of-its-own.

//go:embed builtin/*/SKILL.md
var builtinFS embed.FS

// BuiltinLocation is where a built-in skill says it lives: in the binary,
// which has no path to read or directory to resolve against.
const BuiltinLocation = "built-in"

// Builtins are the skills the binary carries, in name order. A body that
// does not parse is a fault in the binary rather than in anybody's
// checkout, so it panics; a test loads every one.
func Builtins() []Skill {
	dirs, err := fs.Glob(builtinFS, "builtin/*/SKILL.md")
	if err != nil {
		panic(err)
	}
	sort.Strings(dirs)
	out := make([]Skill, 0, len(dirs))
	for _, p := range dirs {
		data, err := builtinFS.ReadFile(p)
		if err != nil {
			panic(err)
		}
		s, err := parse(data, BuiltinLocation, path.Dir(p))
		if err != nil {
			panic(fmt.Sprintf("built-in skill %s: %v", p, err))
		}
		s.Dir = ""
		s.Scope = ScopeBuiltin
		s.embedded = p
		out = append(out, s)
	}
	return out
}

// Load is Discover over roots with the built-ins after every file skill.
// The built-ins are the binary's own, so they load whether or not the
// checkout is trusted; a file skill of the same name, at any scope,
// shadows one, and the diagnostic names both.
func Load(roots []Root) *Catalog {
	c := Discover(roots)
	for _, b := range Builtins() {
		if prior, ok := c.Find(b.Name); ok {
			c.Diagnostics = append(c.Diagnostics, fmt.Sprintf("built-in skill %s: shadowed by %s", b.Name, prior.Location))
			continue
		}
		c.Skills = append(c.Skills, b)
	}
	return c
}
