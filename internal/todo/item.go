package todo

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The header keys this package reads whatever the profile is. They are the
// ones the machinery around an item depends on — where it is in its life,
// what it waits on, when it was written and by whom — and no profile may
// rename or restate them. Everything else in the header is either one of the
// profile's own fields or an Unknown, carried and written back unchanged.
const (
	keyTitle     = "title"
	keyPriority  = "priority"
	keyStatus    = "status"
	keyDependsOn = "depends_on"
	keyCreated   = "created"
	keySession   = "session"
	keyGroomed   = "groomed"
)

var ownKeys = map[string]bool{
	keyTitle: true, keyPriority: true, keyStatus: true, keyDependsOn: true,
	keyCreated: true, keySession: true, keyGroomed: true,
}

// LoadFile reads one item file in a profile's vocabulary.
func LoadFile(p Profile, path string) (Item, error) {
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Item{}, err
	}
	return Parse(p, path, string(data))
}

// Parse reads an item from its file content in a profile's vocabulary.
// Validation is lenient where the item is still usable and strict where it
// is not: a value off a field's scale is a warning, but a missing title or
// an unknown status is an error, because an item that cannot be named or
// placed cannot be listed either.
// See docs/capabilities/todo.md#ready-means-the-dependencies-are-done.
func Parse(p Profile, path, content string) (Item, error) {
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

	it := Item{Slug: slug, Path: path, Body: body, Profile: p, Fields: map[string]string{}}
	for _, l := range h.lines {
		if !l.field {
			continue
		}
		switch l.key {
		case keyTitle:
			it.Title = l.value
		case keyPriority:
			it.Priority = Priority(l.value)
		case keyStatus:
			it.Status = Status(l.value)
		case keyDependsOn:
			it.DependsOn = parseList(l.value)
		case keyCreated:
			it.Created = l.value
		case keySession:
			it.Session = l.value
		case keyGroomed:
			it.Groomed = l.value
		default:
			if _, declared := p.Field(l.key); declared {
				it.Fields[l.key] = l.value
				continue
			}
			it.Extra = append(it.Extra, Unknown{Key: l.key, Value: l.value})
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
	it.checkFields(p)
	for _, dep := range it.DependsOn {
		if err := ValidSlug(dep); err != nil {
			it.Warnings = append(it.Warnings, "depends_on: "+err.Error())
		}
	}
	return it, nil
}

// checkFields puts every value against the profile's scale for its field.
// What a word off the scale costs the item differs by what the field is
// for, and the three cases are the three answers:
//
//   - Priority orders the ready list, so an unset or unreadable one has to
//     fall back to something: the item is ordered as the default and the
//     warning says so, because an item that dropped out of the order would
//     read as one that is finished. It is the only field whose default is
//     applied on the way in — a header line the file does not carry is a
//     field the item does not have, and inventing one here would make a
//     listing state something the file does not say. Every other default is
//     a writer's: what a new item takes when nobody chose.
//   - The grade is what a run spends against, and spending against a word
//     the profile cannot rank is worse than not spending at all, so the
//     item reads as ungraded until somebody fixes the line.
//   - Anything else is kept as written. Nothing computes from it, the file
//     says what it says, and the warning names the line to fix.
func (it *Item) checkFields(p Profile) {
	for _, f := range p.Fields {
		if f.Orders() {
			it.checkPriority(f)
			continue
		}
		value := it.Fields[f.Name]
		if value == "" {
			continue
		}
		if canonical, ok := f.Canonical(value); ok {
			it.Fields[f.Name] = canonical
			continue
		}
		if f.Name == p.Grade {
			it.Warnings = append(it.Warnings,
				fmt.Sprintf("unknown %s %q, treated as ungraded (%s)", f.Name, value, f.List()))
			delete(it.Fields, f.Name)
			continue
		}
		it.Warnings = append(it.Warnings, fmt.Sprintf("unknown %s %q (%s)", f.Name, value, f.List()))
	}
}

func (it *Item) checkPriority(f Field) {
	if it.Priority == "" {
		it.Priority = Priority(f.Default)
		return
	}
	if canonical, ok := f.Canonical(string(it.Priority)); ok {
		it.Priority = Priority(canonical)
		return
	}
	it.Warnings = append(it.Warnings,
		fmt.Sprintf("unknown %s %q, ordered as %s (%s)", f.Name, it.Priority, f.Default, f.List()))
	it.Priority = Priority(f.Default)
}

// Grade is the item's word in its profile's grading field — what a run
// spends against — and "" for an item nobody has graded, or for a profile
// that does not grade its work at all.
func (it Item) Grade() string {
	if it.Profile.Grade == "" {
		return ""
	}
	return it.Fields[it.Profile.Grade]
}

// Render writes a new item file: the title, the profile's fields in the
// order it declares them, the keys this package owns, the extras, then the
// body. It is how an item is first written; an existing file is never
// re-rendered, only line-edited.
func Render(p Profile, it Item) string {
	var h header
	h.set(keyTitle, it.Title)
	for _, f := range p.Fields {
		value := it.Fields[f.Name]
		if f.Orders() {
			value = string(it.Priority)
		}
		if value != "" {
			h.set(f.Name, value)
		}
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
	if it.Groomed != "" {
		h.set(keyGroomed, it.Groomed)
	}
	// An extra that names a key this package owns or one the profile
	// declares is dropped rather than written a second time: the lines
	// above have already put that key where it belongs.
	for _, f := range it.Extra {
		if _, declared := p.Field(f.Key); declared || ownKeys[f.Key] {
			continue
		}
		h.set(f.Key, f.Value)
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
