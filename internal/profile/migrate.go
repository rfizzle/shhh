package profile

// Consolidating a providers/ directory into one providers.toml.
//
// The directory form — a file per provider — was the only form there was, and
// it duplicates whatever several gateways share: the same key variable, the
// same headers, the same house rules. Endpoints removed most of the reason to
// keep them apart, so this folds the lot into one file, in the order they
// load, and leaves the originals alone until asked.

import (
	"fmt"
	"os"
	"path/filepath"
)

// Consolidation is a planned migration: what would be written, from where,
// and what would be dropped on the way.
type Consolidation struct {
	// Target is the providers.toml that would be written — the one beside
	// the highest-priority config directory.
	Target string
	// Sources are the files being folded in, in load order. The target
	// itself appears here when it already exists: its providers are kept,
	// and kept first.
	Sources []string
	// Dead are files that contribute nothing because every provider they
	// declare is already answered for by an earlier file. Load ignores them
	// today; they are redundant in the same way a folded-in file is, and
	// --prune removes them for the same reason.
	Dead []string
	// Profiles are what the target would hold, in load order.
	Profiles []Profile
	// Shadowed are providers a later source declared under a name an earlier
	// source already used. Load ignores them today, and the migration does
	// too — writing them would change which one answers.
	Shadowed []Shadowed
	// Errors are the files that would not load. They are left where they
	// are: a file shhh cannot parse is one it must not rewrite.
	Errors []error
}

// Shadowed is one provider that lost a name collision.
type Shadowed struct {
	Name string
	Path string
	Kept string
}

// Plan works out what consolidating these directories would do, without
// touching anything.
func Plan(dirs []string) Consolidation {
	c := Consolidation{}
	if len(dirs) > 0 {
		c.Target = dirs[0] + ".toml"
	}
	paths, errs := Sources(dirs)
	c.Errors = errs

	owner := map[string]string{}
	for _, path := range paths {
		found, err := LoadFile(path)
		if err != nil {
			c.Errors = append(c.Errors, err)
			continue
		}
		kept, shadowed := 0, 0
		for _, p := range found {
			if winner, ok := owner[p.Name]; ok {
				c.Shadowed = append(c.Shadowed, Shadowed{Name: p.Name, Path: path, Kept: winner})
				shadowed++
				continue
			}
			owner[p.Name] = path
			c.Profiles = append(c.Profiles, p)
			kept++
		}
		switch {
		case kept > 0:
			c.Sources = append(c.Sources, path)
		case shadowed > 0:
			c.Dead = append(c.Dead, path)
		}
	}
	return c
}

// Redundant are the files the target stands in for once it is written:
// everything folded into it, and everything already dead, but never the
// target itself. They are what `--prune` removes.
//
// The dead files are why this is not simply the source list. Running the
// migration and then pruning is two commands, and between them every
// directory file is shadowed by the providers.toml that was just written —
// so a plan that only counted contributors would find nothing to prune and
// leave the originals on disk forever.
func (c Consolidation) Redundant() []string {
	var out []string
	for _, path := range append(append([]string(nil), c.Sources...), c.Dead...) {
		if path != c.Target {
			out = append(out, path)
		}
	}
	return out
}

// NeedsWork reports whether there is anything to do: a plan with providers
// and no redundant file beside the target already is one file.
func (c Consolidation) NeedsWork() bool {
	return len(c.Profiles) > 0 && len(c.Redundant()) > 0
}

// Write renders the plan to its target. It refuses to run against a plan that
// could not read everything it found, because a provider that failed to parse
// is a provider that would silently vanish from the consolidated file.
func (c Consolidation) Write() error {
	if len(c.Errors) > 0 {
		return fmt.Errorf("%d profile file(s) could not be read; fix or move them first", len(c.Errors))
	}
	if c.Target == "" {
		return fmt.Errorf("no config directory to write to")
	}
	if len(c.Profiles) == 0 {
		return fmt.Errorf("no providers to write")
	}
	if err := os.MkdirAll(filepath.Dir(c.Target), 0o755); err != nil {
		return err
	}
	return os.WriteFile(c.Target, []byte(Encode(c.Profiles)), 0o600)
}

// Prune removes the files the target now stands in for, and the providers
// directory itself once it is empty. It reports what it removed.
func (c Consolidation) Prune() ([]string, error) {
	var removed []string
	for _, path := range c.Redundant() {
		if err := os.Remove(path); err != nil {
			return removed, err
		}
		removed = append(removed, path)
	}
	for _, dir := range dirsOf(c.Redundant()) {
		// Only an empty directory goes; anything else in there is not ours.
		if err := os.Remove(dir); err == nil {
			removed = append(removed, dir)
		}
	}
	return removed, nil
}

// dirsOf is the distinct parent directories of a set of paths, deepest first
// so a nested directory is removed before its parent is tried.
func dirsOf(paths []string) []string {
	seen := map[string]bool{}
	var out []string
	for i := len(paths) - 1; i >= 0; i-- {
		dir := filepath.Dir(paths[i])
		if seen[dir] {
			continue
		}
		seen[dir] = true
		out = append(out, dir)
	}
	return out
}
