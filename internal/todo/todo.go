// Package todo is the project backlog: one Markdown file per item under the
// checkout's .shhh/todo directory, read into a list that knows which items
// are ready and in what order. The header of each file is what this package
// reads and writes; the sections below it belong to whoever does the work,
// and nothing here reflows them. See docs/capabilities/todo.md.
package todo

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/rfizzle/shhh/internal/project"
)

// StateDir is the checkout's shhh directory, and Subdir the backlog inside
// it. The archive and the per-run scratch state sit under the backlog so
// that one directory holds everything the feature owns.
const (
	StateDir   = ".shhh"
	Subdir     = "todo"
	DoneSubdir = "done"
	RunSubdir  = ".run"
)

// Priority orders ready items. The rank is the reason it is a type: "high"
// sorts before "medium" by what it means, not by its spelling.
type Priority string

const (
	PriorityHigh   Priority = "high"
	PriorityMedium Priority = "medium"
	PriorityLow    Priority = "low"
)

// Status is where an item is in its life. Done items live in the archive
// and are read only so dependencies on them resolve.
type Status string

const (
	StatusOpen       Status = "open"
	StatusInProgress Status = "in-progress"
	StatusBlocked    Status = "blocked"
	StatusDone       Status = "done"
)

// Item is one backlog file as read. Fields the header did not set are
// empty; Extra holds the header fields neither this package nor the profile
// knows, in file order, so a write puts them back where they were.
type Item struct {
	// Slug is the file's base name without .md and the item's identity:
	// what a dependency names, what a command takes.
	Slug string
	// Path is the absolute location of the file.
	Path string
	// Archived reports the item was read from the done archive.
	Archived bool

	Title string
	// Fields are the profile's own header fields by name — what sort of
	// work this is, how big it is — as strings, because what the words may
	// be is the profile's to say and not this package's. Priority is not
	// among them: it is the one field every profile carries and the one
	// this package orders by, so it keeps a type and a rank.
	Fields    map[string]string
	Priority  Priority
	Status    Status
	DependsOn []string
	Created   string
	Session   string
	// Groomed is when the item was last read against the tree, and the
	// commit it was read against where that is the distance the profile
	// measures staleness by. It is written only where the person accepted
	// the reading. Empty is an item nobody has read that way, which is not
	// the same as one whose reading has gone stale.
	Groomed string
	Extra   []Unknown

	// Profile is the vocabulary the file was read under. It is carried so
	// that a reader holding one item can still say what its grade means
	// without being handed the profile a second time.
	Profile Profile

	// Body is everything after the header, exactly as written.
	Body string

	// Warnings are what was odd about the file without stopping it from
	// loading — an unknown priority, a size not on the scale. They are for
	// the listing, so the person can fix the line.
	Warnings []string
}

// Unknown is one header line nothing interpreted: a key the package does
// not own and the profile does not declare. It is carried so a write puts
// it back exactly where it was.
type Unknown struct {
	Key, Value string
}

// Root is the directory the backlog is keyed on: the project the working
// directory is part of — the nearest shhh directory, else the enclosing
// repository — and where nothing in the tree marks one, the root a settings
// file named, else the global backlog every session falls back to. Every
// session under one project shares one backlog, which is what makes it the
// project's rather than a session's.
//
// The last two answers are why a conversation opened in a home directory has
// a backlog at all. A reading list is not kept in a checkout, and the order
// gives one to a session that is standing outside every project without ever
// overruling a project that is right there.
// See docs/capabilities/todo.md#where-the-backlog-lives.
func Root(dir string) string {
	if root, found := project.RootFound(dir); found {
		return root
	}
	elsewhere := heldRoots()
	switch {
	case elsewhere.Setting != "":
		return elsewhere.Setting
	case elsewhere.Global != "":
		return elsewhere.Global
	}
	return project.Root(dir)
}

// Dir is the backlog directory under a root.
//
// The global backlog is its own directory rather than a state directory
// inside one. Nothing but the backlog is kept there, and a `.shhh` under the
// configuration directory would be shhh keeping project state for a project
// that is its own settings.
func Dir(root string) string {
	if global := heldRoots().Global; global != "" && root == global {
		return root
	}
	return filepath.Join(root, StateDir, Subdir)
}

// Elsewhere is where a backlog lives when nothing in the working directory's
// tree marks a project: the root a settings file names, and the global
// backlog under the configuration directory.
type Elsewhere struct {
	// Setting is the directory `todo.root` names, already expanded, and
	// empty where the settings say nothing.
	Setting string
	// Global is the backlog every session outside a project shares, and
	// empty for a caller that never read a settings file — which leaves
	// Root answering with the working directory, the way it always did.
	Global string
}

// held is Elsewhere for this process. It is held rather than passed to Root
// because every surface that opens a backlog asks Root for it, while only
// the command tree's entry point reads a settings file.
var held struct {
	mu sync.Mutex
	is Elsewhere
}

// Hold states the two roots Root falls back to. It is called once, where the
// settings are read for the whole command tree.
func Hold(e Elsewhere) {
	held.mu.Lock()
	defer held.mu.Unlock()
	held.is = e
}

// heldRoots is what this process was told.
func heldRoots() Elsewhere {
	held.mu.Lock()
	defer held.mu.Unlock()
	return held.is
}

// slugPattern is what a slug may look like. Lowercase letters, digits and
// single hyphens, never at an end: a name that is safe as a filename on
// every platform and needs no quoting on a command line.
var slugPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// MaxSlugLen keeps a slug readable in a dependency list and a rail row.
const MaxSlugLen = 48

// ValidSlug reports whether s can name an item, and why not when it cannot.
func ValidSlug(s string) error {
	switch {
	case s == "":
		return fmt.Errorf("a slug cannot be empty")
	case len(s) > MaxSlugLen:
		return fmt.Errorf("slug %q is longer than %d characters", s, MaxSlugLen)
	case !slugPattern.MatchString(s):
		return fmt.Errorf("slug %q must be lowercase letters, digits and single hyphens", s)
	}
	return nil
}

// Slugify derives a slug from free text: a title, a sentence. Anything that
// is not a letter or digit becomes a hyphen, runs collapse, and the result
// is clipped at a word boundary. It never returns an invalid slug; text
// with nothing usable in it becomes "item".
func Slugify(text string) string {
	var b strings.Builder
	lastHyphen := true
	for _, r := range strings.ToLower(text) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastHyphen = false
		default:
			if !lastHyphen {
				b.WriteByte('-')
				lastHyphen = true
			}
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > MaxSlugLen {
		s = s[:MaxSlugLen]
		if i := strings.LastIndexByte(s, '-'); i > 0 {
			s = s[:i]
		}
	}
	if s == "" {
		return "item"
	}
	return s
}
