package todo

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Store is the backlog as read: the active items in backlog order, the
// archive, and what could not be read. Diagnostics are for the person,
// never for the model — an item that failed to load is named with the
// reason, because one that silently vanished would look finished.
// See docs/capabilities/todo.md#ready-means-the-dependencies-are-done.
type Store struct {
	Root string
	Dir  string
	// Profile is the vocabulary every item in it was read under, kept so a
	// surface handed the store does not have to be handed the profile
	// beside it.
	Profile Profile
	// Items are the active items — every status but done — in Less order.
	Items []Item
	// Done is the archive, in slug order.
	Done []Item
	// Sprint is the set being worked, when the backlog holds a sprint
	// file. An open one scopes Ready and Next to its slugs, in its order.
	Sprint *Sprint
	// Unreadable are the files in the backlog that would not parse as
	// items. They are kept as entries rather than counted because the
	// alternative is a file that is on disk and on no list: a surface that
	// drops the row says the item is gone, and the person goes looking for
	// a deletion that never happened.
	Unreadable []Unreadable
	// Diagnostics are the same failures as sentences, plus anything wrong
	// with the sprint file, for a listing that prints rather than draws.
	Diagnostics []string
}

// Unreadable is one file in the backlog directory that could not be read as
// an item.
type Unreadable struct {
	// Slug is the file's base name without .md — the name the item would
	// have had, and the name another item's depends_on would use for it.
	Slug string
	// Path is where the file is, so the reason can be acted on.
	Path string
	// Reason is why it would not load, in the parser's own words.
	Reason string
	// Archived reports it was found in the done archive rather than in the
	// active backlog.
	Archived bool
}

// Load reads the backlog under root. A root with no backlog directory is an
// empty store, not an error.
func Load(p Profile, root string) *Store {
	s := &Store{Root: root, Dir: Dir(root), Profile: p}
	s.Items = s.readDir(s.Dir, false)
	s.Done = s.readDir(filepath.Join(s.Dir, DoneSubdir), true)
	for _, u := range s.Unreadable {
		s.Diagnostics = append(s.Diagnostics, fmt.Sprintf("%s: skipped: %s", u.Path, u.Reason))
	}
	// A sprint file that will not parse is a diagnostic and no sprint, so
	// the ready list falls back to the whole backlog rather than to an
	// empty one: a broken file must not look like a finished sprint.
	if sp, err := LoadSprint(root); err != nil {
		s.Diagnostics = append(s.Diagnostics, fmt.Sprintf("%s: skipped: %v", SprintPath(root), err))
	} else {
		s.Sprint = sp
	}
	for i := range s.Items {
		it := &s.Items[i]
		for _, dep := range it.DependsOn {
			if _, ok := s.Find(dep); !ok {
				it.Warnings = append(it.Warnings, fmt.Sprintf("depends_on names %q, which is not in the backlog or its archive", dep))
			}
		}
	}
	sort.SliceStable(s.Items, func(i, j int) bool { return Less(s.Items[i], s.Items[j]) })
	sort.SliceStable(s.Done, func(i, j int) bool { return s.Done[i].Slug < s.Done[j].Slug })
	return s
}

func (s *Store) readDir(dir string, archived bool) []Item {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []Item
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		// The sprint sits in the same directory and is not an item; read
		// as one it would fail for want of a title and show up as a file
		// that could not be loaded.
		if !archived && e.Name() == SprintFile {
			continue
		}
		path := filepath.Join(dir, e.Name())
		it, err := LoadFile(s.Profile, path)
		if err != nil {
			s.Unreadable = append(s.Unreadable, Unreadable{
				Slug:     strings.TrimSuffix(e.Name(), ".md"),
				Path:     path,
				Reason:   err.Error(),
				Archived: archived,
			})
			continue
		}
		it.Archived = archived
		if archived && it.Status != StatusDone {
			it.Warnings = append(it.Warnings, fmt.Sprintf("in the archive but status is %q", it.Status))
		}
		if !archived && it.Status == StatusDone {
			it.Warnings = append(it.Warnings, "status is done but the file is not in the archive")
		}
		out = append(out, it)
	}
	return out
}

// Len reports how many active items there are.
func (s *Store) Len() int {
	if s == nil {
		return 0
	}
	return len(s.Items)
}

// Find returns the item with the slug, active or archived.
func (s *Store) Find(slug string) (Item, bool) {
	if s == nil {
		return Item{}, false
	}
	for _, it := range s.Items {
		if it.Slug == slug {
			return it, true
		}
	}
	for _, it := range s.Done {
		if it.Slug == slug {
			return it, true
		}
	}
	return Item{}, false
}

// doneSet is the slugs a dependency counts as satisfied by: everything in
// the archive with status done.
func (s *Store) doneSet() map[string]bool {
	done := map[string]bool{}
	for _, it := range s.Done {
		if it.Status == StatusDone {
			done[it.Slug] = true
		}
	}
	return done
}

// Ready is the items that can be started now: the open sprint's slugs in
// the file's order when there is one, else the whole backlog in its own
// order. A sprint states which items and in what sequence; it never states
// that one of them is ready, so a slug whose dependencies are outstanding
// is left out here exactly as it would be without a sprint.
// See docs/capabilities/todo.md#a-sprint-is-a-file-that-names-its-items.
func (s *Store) Ready() []Item {
	if s == nil {
		return nil
	}
	if !s.Sprint.Open() {
		return s.readyAll()
	}
	done := s.doneSet()
	var out []Item
	for _, slug := range s.Sprint.Slugs {
		it, ok := s.Find(slug)
		if ok && !it.Archived && it.Ready(done) {
			out = append(out, it)
		}
	}
	return out
}

// readyAll is the whole backlog's ready set, in backlog order. It is what
// Ready answers with no sprint, and what a sprint is proposed from.
func (s *Store) readyAll() []Item {
	done := s.doneSet()
	var out []Item
	for _, it := range s.Items {
		if it.Ready(done) {
			out = append(out, it)
		}
	}
	return out
}

// Waiting names the dependencies holding an item back.
func (s *Store) Waiting(it Item) []string { return it.Waiting(s.doneSet()) }

// Next is the first ready item.
func (s *Store) Next() (Item, bool) {
	ready := s.Ready()
	if len(ready) == 0 {
		return Item{}, false
	}
	return ready[0], true
}

// Count is the active items by status.
func (s *Store) Count(status Status) int {
	n := 0
	for _, it := range s.Items {
		if it.Status == status {
			n++
		}
	}
	return n
}

// ignoreFile is written beside the items the first time the directory is
// created. Whether the backlog itself is committed is the project's call;
// the per-run scratch state under it never is.
// See docs/capabilities/todo.md#where-the-backlog-lives.
const ignoreFile = RunSubdir + "/\n"

// ensureDir creates the backlog directory and its ignore file. It refuses
// to proceed when the state directory exists as a file — the old layout —
// because creating a directory beside it is impossible and replacing it
// would lose the context it holds.
func ensureDir(root string) (string, error) {
	state := filepath.Join(root, StateDir)
	if st, err := os.Stat(state); err == nil && !st.IsDir() {
		return "", fmt.Errorf("%s is a file from an older layout; run `shhh doctor` to move it", state)
	}
	dir := Dir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	ignore := filepath.Join(dir, ".gitignore")
	if _, err := os.Stat(ignore); os.IsNotExist(err) {
		if err := os.WriteFile(ignore, []byte(ignoreFile), 0o644); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// Create writes a new item and returns its path. The slug must be valid
// and unused, active or archived: an archived slug is still what other
// items' dependencies name.
func Create(p Profile, root string, it Item) (string, error) {
	if err := ValidSlug(it.Slug); err != nil {
		return "", err
	}
	// A name the project reserves is refused here and nowhere else: this is
	// the one place an item's slug is chosen, and every other verb takes a
	// slug a file already has. A sprint's name borrows the same grammar and
	// is not put to the rule — it names a set rather than a piece of work,
	// and it is the profile's items the profile speaks for.
	if err := p.RefuseSlug(it.Slug); err != nil {
		return "", err
	}
	if strings.TrimSpace(it.Title) == "" {
		return "", fmt.Errorf("an item needs a title")
	}
	// The sprint's file name is taken. An item written there would be
	// invisible — the loader skips it — and would break the sprint reader
	// as well, since it has a title where a name should be.
	if it.Slug+".md" == SprintFile {
		return "", fmt.Errorf("%q names the sprint file; an item cannot be called that", it.Slug)
	}
	dir, err := ensureDir(root)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, it.Slug+".md")
	for _, p := range []string{path, filepath.Join(dir, DoneSubdir, it.Slug+".md")} {
		if _, err := os.Stat(p); err == nil {
			return "", fmt.Errorf("%s already exists", p)
		}
	}
	if err := os.WriteFile(path, []byte(Render(p, it)), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// SetStatus changes the status line of an item file and nothing else.
// See docs/capabilities/todo.md#an-item-is-a-file-you-can-edit.
func SetStatus(path string, status Status) error {
	return editHeader(path, func(h *header) bool { return h.set(keyStatus, string(status)) })
}

// SetField changes one of the profile's header fields in an item file and
// nothing else.
func SetField(path, name, value string) error {
	return editHeader(path, func(h *header) bool { return h.set(name, value) })
}

// editHeader rewrites the header block of a file through edit, leaving the
// body as it was — its line endings and byte-order mark included, since a
// file a Windows editor wrote must come back as one. A no-op edit does not
// touch the file.
func editHeader(path string, edit func(*header) bool) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	raw := string(data)
	bom := strings.HasPrefix(raw, "\uFEFF")
	crlf := strings.Contains(raw, "\r\n")
	block, body, err := splitHeader(raw)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	h, err := parseHeader(block)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if !edit(&h) {
		return nil
	}
	out := h.render() + body
	if crlf {
		out = strings.ReplaceAll(out, "\n", "\r\n")
	}
	if bom {
		out = "\uFEFF" + out
	}
	return os.WriteFile(path, []byte(out), 0o644)
}

// Archive marks an item done, appends the report if there is one, and
// moves the file into the archive. The move is a plain rename; nothing here
// stages it. See docs/capabilities/todo.md#done-is-archived-not-deleted.
func Archive(root, slug, report string) (string, error) {
	if err := ValidSlug(slug); err != nil {
		return "", err
	}
	dir := Dir(root)
	from := filepath.Join(dir, slug+".md")
	if _, err := os.Stat(from); err != nil {
		return "", fmt.Errorf("no active item %q", slug)
	}
	// Everything that could refuse the move is checked before the file is
	// touched, so a refusal leaves an item that can be archived again
	// rather than one already marked done in the wrong place.
	doneDir := filepath.Join(dir, DoneSubdir)
	if err := os.MkdirAll(doneDir, 0o755); err != nil {
		return "", err
	}
	to := filepath.Join(doneDir, slug+".md")
	if _, err := os.Stat(to); err == nil {
		return "", fmt.Errorf("%s already exists", to)
	}
	if err := SetStatus(from, StatusDone); err != nil {
		return "", err
	}
	if err := Append(from, report); err != nil {
		return "", err
	}
	if err := os.Rename(from, to); err != nil {
		return "", err
	}
	// The item is done, so the reading of it against the tree is scratch
	// about work that no longer exists (groom.go).
	DiscardReading(root, slug)
	return to, nil
}

// Reopen takes an archived item back into the active backlog and marks it
// open. It is the way back from the archive for work that turned out not to
// be finished.
//
// The body is left exactly as it was, report and all. What a run wrote about
// the work is a record of what happened, and an item coming back is a
// statement about what is still to do rather than a correction of that: a
// reopen that deleted the report would lose the one account of why the item
// was thought done. See docs/capabilities/todo.md#done-is-archived-not-deleted.
func Reopen(root, slug string) (string, error) {
	if err := ValidSlug(slug); err != nil {
		return "", err
	}
	dir := Dir(root)
	from := filepath.Join(dir, DoneSubdir, slug+".md")
	if _, err := os.Stat(from); err != nil {
		return "", fmt.Errorf("no archived item %q", slug)
	}
	// Everything that could refuse the move is checked before the file is
	// touched, the way archiving checks: a refusal must leave an archived
	// item that is still archived rather than one marked open in the
	// archive, where nothing lists it and nothing can act on it.
	to := filepath.Join(dir, slug+".md")
	if _, err := os.Stat(to); err == nil {
		return "", fmt.Errorf("%s already exists", to)
	}
	if err := SetStatus(from, StatusOpen); err != nil {
		return "", err
	}
	if err := os.Rename(from, to); err != nil {
		return "", err
	}
	return to, nil
}

// Append adds a block of Markdown to the end of an item file — a report, a
// note on why it is blocked — after a blank line. Empty text appends
// nothing.
func Append(path, text string) error {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	sep := "\n"
	if len(data) > 0 && data[len(data)-1] != '\n' {
		sep = "\n\n"
	}
	data = append(data, []byte(sep+strings.TrimRight(text, "\n")+"\n")...)
	return os.WriteFile(path, data, 0o644)
}

// Remove deletes an active item outright. It is the one operation here
// that loses information, which is why it is a separate verb from archiving
// and takes an active item only: an archived item is a record.
func Remove(root, slug string) error {
	if err := ValidSlug(slug); err != nil {
		return err
	}
	path := filepath.Join(Dir(root), slug+".md")
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("no active item %q", slug)
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	// The file is gone and so is anything read about it (groom.go).
	DiscardReading(root, slug)
	return nil
}
