// Package skill discovers and loads Agent Skills: directories holding a
// SKILL.md whose frontmatter names the skill and says when to use it, and
// whose body is the instructions to follow once it applies. The format is
// the Agent Skills specification (https://agentskills.io/specification), so
// a skill written for any other harness loads here unchanged, and one
// written here loads there.
//
// A catalog is disclosed in three tiers, and the tiers are the point: the
// name and description of every skill go into the system prompt at startup,
// the body of one skill enters the conversation when it is activated, and
// the files beside it are read on demand by the ordinary file tools.
package skill

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Scope is where a skill was found. Project skills come with the checkout;
// user skills live in the home directory and apply everywhere.
type Scope string

const (
	ScopeProject Scope = "project"
	ScopeUser    Scope = "user"
	// ScopeBuiltin is a skill the binary carries: listed after every file
	// skill, and shadowed by any file skill of the same name.
	ScopeBuiltin Scope = "built-in"
)

// Root is one directory to scan for skill subdirectories.
type Root struct {
	Path  string
	Scope Scope
}

// Skill is one loaded skill: its frontmatter, where it lives, and what was
// worth warning about while reading it. The body is not held here — it is
// read from Location at activation, so an edit to a skill between two
// activations is seen without restarting the session.
type Skill struct {
	Name        string
	Description string
	// Location is the absolute path of the SKILL.md; Dir is its parent,
	// the base every relative path in the body resolves against. A
	// built-in's Location is BuiltinLocation and its Dir is empty: it has
	// no directory and bundles no files.
	Location string
	Dir      string
	Scope    Scope

	// embedded is a built-in's path in the binary's own files; empty for a
	// skill read from disk.
	embedded string
	// fsys is the filesystem a built-in was loaded from, which Body reads.
	fsys fs.FS

	License       string
	Compatibility string
	Metadata      map[string]string
	// AllowedTools is the frontmatter's space-separated pre-approval list,
	// carried for display only. The host program grants nothing from it: a file
	// in a checkout is not a place a permission can come from.
	AllowedTools string

	// Warnings are the lenient-validation findings that did not stop the
	// skill from loading — a name that does not match its directory, a
	// description over the limit.
	Warnings []string
}

// Body reads the skill's instructions: the SKILL.md with its frontmatter
// stripped — from the binary for a built-in, from disk for the rest.
func (s Skill) Body() (string, error) {
	var data []byte
	var err error
	if s.embedded != "" {
		data, err = fs.ReadFile(s.fsys, s.embedded)
	} else {
		data, err = os.ReadFile(s.Location)
	}
	if err != nil {
		return "", err
	}
	_, body, err := splitFrontmatter(string(data))
	if err != nil {
		return "", err
	}
	return body, nil
}

// Catalog is every skill a session can activate, in disclosure order, plus
// the diagnostics from loading them: a skill that was skipped, a name that
// was shadowed. Diagnostics are for the user, never for the model.
type Catalog struct {
	Skills      []Skill
	Diagnostics []string
}

// Len reports how many skills loaded.
func (c *Catalog) Len() int {
	if c == nil {
		return 0
	}
	return len(c.Skills)
}

// Find returns the skill with the given name.
func (c *Catalog) Find(name string) (Skill, bool) {
	if c == nil {
		return Skill{}, false
	}
	for _, s := range c.Skills {
		if s.Name == name {
			return s, true
		}
	}
	return Skill{}, false
}

// Names lists the skill names in catalog order.
func (c *Catalog) Names() []string {
	if c == nil {
		return nil
	}
	out := make([]string, len(c.Skills))
	for i, s := range c.Skills {
		out[i] = s.Name
	}
	return out
}

// Discover loads every skill under roots, in root order. The first root
// holding a name wins and later ones are reported as shadowed, so project
// roots listed first override user roots of the same name — the precedence
// every other harness applies, and the one a user expects when they copy a
// shared skill into a checkout to change it.
//
// A root that does not exist is nothing. A SKILL.md that cannot be used is a
// diagnostic naming the file and the reason, never an error: one broken
// skill in a directory of good ones must not cost the session all of them.
func Discover(roots []Root) *Catalog {
	c := &Catalog{}
	seen := map[string]string{}
	for _, root := range roots {
		entries, err := os.ReadDir(root.Path)
		if err != nil {
			continue
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, e := range entries {
			if !e.IsDir() && e.Type()&os.ModeSymlink == 0 {
				continue
			}
			dir := filepath.Join(root.Path, e.Name())
			location := filepath.Join(dir, "SKILL.md")
			if st, err := os.Stat(location); err != nil || st.IsDir() {
				continue
			}
			s, err := LoadFile(location)
			if err != nil {
				c.Diagnostics = append(c.Diagnostics, fmt.Sprintf("%s: skipped: %v", location, err))
				continue
			}
			s.Scope = root.Scope
			if prior, dup := seen[s.Name]; dup {
				c.Diagnostics = append(c.Diagnostics, fmt.Sprintf("%s: shadowed by %s", location, prior))
				continue
			}
			seen[s.Name] = location
			c.Skills = append(c.Skills, s)
		}
	}
	return c
}

// Limits from the specification's frontmatter table.
const (
	maxNameLen          = 64
	maxDescriptionLen   = 1024
	maxCompatibilityLen = 500
)

// LoadFile reads one SKILL.md. Validation is lenient the way the client
// guide asks: a missing description or unparseable frontmatter is an error,
// because a skill nobody can be told about cannot be activated; everything
// else — a name that does not match the directory, a field over its limit —
// is a warning on the loaded skill.
func LoadFile(location string) (Skill, error) {
	abs, err := filepath.Abs(location)
	if err == nil {
		location = abs
	}
	data, err := os.ReadFile(location)
	if err != nil {
		return Skill{}, err
	}
	return parse(data, location, filepath.Dir(location))
}

// parse reads a SKILL.md's frontmatter under LoadFile's rules; dir is the
// directory whose name the skill's name is checked against.
func parse(data []byte, location, dir string) (Skill, error) {
	fm, _, err := splitFrontmatter(string(data))
	if err != nil {
		return Skill{}, err
	}
	fields, err := parseFrontmatter(fm)
	if err != nil {
		return Skill{}, err
	}

	s := Skill{
		Location:      location,
		Dir:           dir,
		Description:   strings.TrimSpace(fields.scalar("description")),
		License:       strings.TrimSpace(fields.scalar("license")),
		Compatibility: strings.TrimSpace(fields.scalar("compatibility")),
		AllowedTools:  strings.TrimSpace(fields.scalar("allowed-tools")),
		Metadata:      fields.mapping("metadata"),
	}
	if s.Description == "" {
		return Skill{}, fmt.Errorf("frontmatter has no description")
	}
	if len(s.Description) > maxDescriptionLen {
		s.Warnings = append(s.Warnings, fmt.Sprintf("description is %d characters; the limit is %d", len(s.Description), maxDescriptionLen))
	}
	if len(s.Compatibility) > maxCompatibilityLen {
		s.Warnings = append(s.Warnings, fmt.Sprintf("compatibility is %d characters; the limit is %d", len(s.Compatibility), maxCompatibilityLen))
	}

	dirName := filepath.Base(s.Dir)
	s.Name = strings.TrimSpace(fields.scalar("name"))
	switch {
	case s.Name == "":
		s.Name = dirName
		s.Warnings = append(s.Warnings, "frontmatter has no name; using the directory name")
	case s.Name != dirName:
		s.Warnings = append(s.Warnings, fmt.Sprintf("name %q does not match the directory %q", s.Name, dirName))
	}
	if msg := checkName(s.Name); msg != "" {
		s.Warnings = append(s.Warnings, msg)
	}
	return s, nil
}

// checkName applies the specification's name rules and returns the first
// violation, or "" for a valid name. Lowercase letters, digits and single
// hyphens, not at either end, at most 64 characters.
func checkName(name string) string {
	if len(name) > maxNameLen {
		return fmt.Sprintf("name is %d characters; the limit is %d", len(name), maxNameLen)
	}
	if strings.HasPrefix(name, "-") || strings.HasSuffix(name, "-") {
		return "name must not start or end with a hyphen"
	}
	if strings.Contains(name, "--") {
		return "name must not contain consecutive hyphens"
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
		default:
			return fmt.Sprintf("name contains %q; only lowercase letters, digits and hyphens are allowed", r)
		}
	}
	return ""
}
