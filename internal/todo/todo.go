// Package todo is the project backlog: one Markdown file per item under the
// checkout's .shhh/todo directory, read into a list that knows which items
// are ready and in what order. The header of each file is what this package
// reads and writes; the sections below it belong to whoever does the work,
// and nothing here reflows them. See docs/capabilities/todo.md.
package todo

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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

// Kind is what sort of work an item is.
type Kind string

const (
	KindStory Kind = "story"
	KindBug   Kind = "bug"
	KindChore Kind = "chore"
)

// Priority orders ready items. The rank is the reason it is a type: "high"
// sorts before "medium" by what it means, not by its spelling.
type Priority string

const (
	PriorityHigh   Priority = "high"
	PriorityMedium Priority = "medium"
	PriorityLow    Priority = "low"
)

// Size is the grade a runner reads to decide how much ceremony an item
// gets. An empty size is "not graded yet", never a default.
type Size string

const (
	SizeS Size = "S"
	SizeM Size = "M"
	SizeL Size = "L"
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
// empty; Extra holds the header fields this package does not know, in file
// order, so a write puts them back where they were.
type Item struct {
	// Slug is the file's base name without .md and the item's identity:
	// what a dependency names, what a command takes.
	Slug string
	// Path is the absolute location of the file.
	Path string
	// Archived reports the item was read from the done archive.
	Archived bool

	Title     string
	Kind      Kind
	Priority  Priority
	Size      Size
	Status    Status
	DependsOn []string
	Created   string
	Session   string
	Extra     []Field

	// Body is everything after the header, exactly as written.
	Body string

	// Warnings are what was odd about the file without stopping it from
	// loading — an unknown priority, a size not on the scale. They are for
	// the listing, so the person can fix the line.
	Warnings []string
}

// Field is one header line this package does not interpret.
type Field struct {
	Key, Value string
}

// Root is the directory the backlog is keyed on: the enclosing repository
// root, else the directory itself. Every session under one checkout shares
// one backlog, which is what makes it the project's rather than a
// session's. See docs/capabilities/todo.md#where-the-backlog-lives.
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

// Dir is the backlog directory under a root.
func Dir(root string) string { return filepath.Join(root, StateDir, Subdir) }

// slugPattern is what a slug may look like. Lowercase letters, digits and
// single hyphens, never at an end: a name that is safe as a filename on
// every platform and needs no quoting on a command line.
var slugPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// storyPattern is the shape a planning identifier takes elsewhere — a
// letter, a hyphen, three digits. A slug must never look like one: a
// backlog item named that way reads, to a person, as a reference to a plan
// the code must not make, and the point of a slug is to name the work.
var storyPattern = regexp.MustCompile(`^[a-z]-\d{3}$`)

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
	case storyPattern.MatchString(s):
		return fmt.Errorf("slug %q looks like a planning identifier; name the work instead", s)
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
	if s == "" || storyPattern.MatchString(s) {
		return "item"
	}
	return s
}
