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
	// Items are the active items — every status but done — in Less order.
	Items []Item
	// Done is the archive, in slug order.
	Done        []Item
	Diagnostics []string
}

// Load reads the backlog under root. A root with no backlog directory is an
// empty store, not an error.
func Load(root string) *Store {
	s := &Store{Root: root, Dir: Dir(root)}
	s.Items = s.readDir(s.Dir, false)
	s.Done = s.readDir(filepath.Join(s.Dir, DoneSubdir), true)
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
		path := filepath.Join(dir, e.Name())
		it, err := LoadFile(path)
		if err != nil {
			s.Diagnostics = append(s.Diagnostics, fmt.Sprintf("%s: skipped: %v", path, err))
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

// Ready is the items that can be started now, in backlog order.
func (s *Store) Ready() []Item {
	if s == nil {
		return nil
	}
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
func Create(root string, it Item) (string, error) {
	if err := ValidSlug(it.Slug); err != nil {
		return "", err
	}
	if strings.TrimSpace(it.Title) == "" {
		return "", fmt.Errorf("an item needs a title")
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
	if err := os.WriteFile(path, []byte(Render(it)), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// SetStatus changes the status line of an item file and nothing else.
// See docs/capabilities/todo.md#an-item-is-a-file-you-can-edit.
func SetStatus(path string, status Status) error {
	return editHeader(path, func(h *header) bool { return h.set(keyStatus, string(status)) })
}

// SetSize changes the size line of an item file and nothing else.
func SetSize(path string, size Size) error {
	return editHeader(path, func(h *header) bool { return h.set(keySize, string(size)) })
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
	if strings.TrimSpace(report) != "" {
		data, err := os.ReadFile(from)
		if err != nil {
			return "", err
		}
		sep := "\n"
		if len(data) > 0 && data[len(data)-1] != '\n' {
			sep = "\n\n"
		}
		data = append(data, []byte(sep+strings.TrimRight(report, "\n")+"\n")...)
		if err := os.WriteFile(from, data, 0o644); err != nil {
			return "", err
		}
	}
	if err := os.Rename(from, to); err != nil {
		return "", err
	}
	return to, nil
}
