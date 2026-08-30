package todo

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The header keys this package reads. Anything else in the header is an
// Extra field, carried and written back unchanged.
const (
	keyTitle     = "title"
	keyKind      = "kind"
	keyPriority  = "priority"
	keySize      = "size"
	keyStatus    = "status"
	keyDependsOn = "depends_on"
	keyCreated   = "created"
	keySession   = "session"
)

var knownKeys = map[string]bool{
	keyTitle: true, keyKind: true, keyPriority: true, keySize: true,
	keyStatus: true, keyDependsOn: true, keyCreated: true, keySession: true,
}

// LoadFile reads one item file.
func LoadFile(path string) (Item, error) {
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Item{}, err
	}
	return Parse(path, string(data))
}

// Parse reads an item from its file content. Validation is lenient where
// the item is still usable and strict where it is not: a value off the
// scale for priority or size is a warning and the field falls back, but a
// missing title or an unknown status is an error, because an item that
// cannot be named or placed cannot be listed either.
// See docs/capabilities/todo.md#ready-means-the-dependencies-are-done.
func Parse(path, content string) (Item, error) {
	slug := strings.TrimSuffix(filepath.Base(path), ".md")
	if err := ValidSlug(slug); err != nil {
		return Item{}, err
	}
	block, body, err := splitHeader(content)
	if err != nil {
		return Item{}, err
	}
	h, err := parseHeader(block)
	if err != nil {
		return Item{}, err
	}

	it := Item{Slug: slug, Path: path, Body: body}
	for _, l := range h.lines {
		if !l.field {
			continue
		}
		switch l.key {
		case keyTitle:
			it.Title = l.value
		case keyKind:
			it.Kind = Kind(l.value)
		case keyPriority:
			it.Priority = Priority(l.value)
		case keySize:
			it.Size = Size(strings.ToUpper(l.value))
		case keyStatus:
			it.Status = Status(l.value)
		case keyDependsOn:
			it.DependsOn = parseList(l.value)
		case keyCreated:
			it.Created = l.value
		case keySession:
			it.Session = l.value
		default:
			it.Extra = append(it.Extra, Field{Key: l.key, Value: l.value})
		}
	}

	if it.Title == "" {
		return Item{}, fmt.Errorf("no title in the header")
	}
	if it.Status == "" {
		it.Status = StatusOpen
	}
	switch it.Status {
	case StatusOpen, StatusInProgress, StatusBlocked, StatusDone:
	default:
		return Item{}, fmt.Errorf("unknown status %q (open, in-progress, blocked, done)", it.Status)
	}
	switch it.Kind {
	case "", KindStory, KindBug, KindChore:
	default:
		it.Warnings = append(it.Warnings, fmt.Sprintf("unknown kind %q (story, bug, chore)", it.Kind))
	}
	switch it.Priority {
	case PriorityHigh, PriorityMedium, PriorityLow:
	case "":
		it.Priority = PriorityMedium
	default:
		it.Warnings = append(it.Warnings, fmt.Sprintf("unknown priority %q, ordered as medium (high, medium, low)", it.Priority))
		it.Priority = PriorityMedium
	}
	switch it.Size {
	case "", SizeS, SizeM, SizeL:
	default:
		it.Warnings = append(it.Warnings, fmt.Sprintf("unknown size %q, treated as ungraded (S, M, L)", it.Size))
		it.Size = ""
	}
	for _, dep := range it.DependsOn {
		if err := ValidSlug(dep); err != nil {
			it.Warnings = append(it.Warnings, "depends_on: "+err.Error())
		}
	}
	return it, nil
}

// Render writes a new item file from its fields: the known fields in a
// fixed order, then the extras, then the body. It is how an item is first
// written; an existing file is never re-rendered, only line-edited.
func Render(it Item) string {
	var h header
	h.set(keyTitle, it.Title)
	if it.Kind != "" {
		h.set(keyKind, string(it.Kind))
	}
	if it.Priority != "" {
		h.set(keyPriority, string(it.Priority))
	}
	if it.Size != "" {
		h.set(keySize, string(it.Size))
	}
	status := it.Status
	if status == "" {
		status = StatusOpen
	}
	h.set(keyStatus, string(status))
	if len(it.DependsOn) > 0 {
		h.set(keyDependsOn, formatList(it.DependsOn))
	}
	if it.Created != "" {
		h.set(keyCreated, it.Created)
	}
	if it.Session != "" {
		h.set(keySession, it.Session)
	}
	for _, f := range it.Extra {
		if !knownKeys[f.Key] {
			h.set(f.Key, f.Value)
		}
	}
	body := strings.TrimRight(it.Body, "\n")
	if body == "" {
		return h.render()
	}
	return h.render() + "\n" + body + "\n"
}

// Ready reports whether the item is open with every dependency done.
// done is the set of archived slugs.
func (it Item) Ready(done map[string]bool) bool {
	if it.Status != StatusOpen {
		return false
	}
	for _, dep := range it.DependsOn {
		if !done[dep] {
			return false
		}
	}
	return true
}

// Waiting lists the dependencies not yet done, for the listing to name.
func (it Item) Waiting(done map[string]bool) []string {
	var out []string
	for _, dep := range it.DependsOn {
		if !done[dep] {
			out = append(out, dep)
		}
	}
	return out
}

// rank is the priority's sort position.
func (p Priority) rank() int {
	switch p {
	case PriorityHigh:
		return 0
	case PriorityLow:
		return 2
	}
	return 1
}

// Less is the backlog order: priority, then age, then slug. It is total
// and reads off the header alone, so the next item is something a person
// can work out by looking at the files.
// See docs/capabilities/todo.md#ready-means-the-dependencies-are-done.
func Less(a, b Item) bool {
	if ra, rb := a.Priority.rank(), b.Priority.rank(); ra != rb {
		return ra < rb
	}
	if a.Created != b.Created {
		// An item with no date sorts after every dated one: it has no
		// claim to being older.
		if a.Created == "" || b.Created == "" {
			return a.Created != ""
		}
		return a.Created < b.Created
	}
	return a.Slug < b.Slug
}
