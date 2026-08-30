// Package notebook is the shared channel between the agents of one chat
// session: a set of short, titled, signed notes that the orchestrator and
// every delegate can read and write. It is working state for one
// conversation — kept with the session so a resume brings it back, and
// never proposed to the user as memory.
// See docs/capabilities/chat.md#what-they-share.
package notebook

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Bounds. A note is a paragraph, not a document: the model that reads the
// notebook gets every note in one block, and a store that could hold a
// crawled page would spend a context window on a single delegate's leavings.
const (
	MaxTitleLen = 80
	MaxBodyLen  = 2000
	// MaxNotes bounds a session's notebook; the oldest is dropped when a new
	// one would exceed it, and the drop is reported to the writer.
	MaxNotes = 200
)

// Note is one entry. Author is the agent that wrote it — "you" for the
// orchestrator, a delegate's name otherwise — so a reader can weigh it.
type Note struct {
	ID      int64
	Author  string
	Title   string
	Body    string
	Written time.Time
}

// Backend is where notes persist. The store keeps its own copy in memory
// and writes through, so a backend that fails leaves the session's notebook
// intact for the session; only the resume loses it.
type Backend interface {
	// SaveNote persists one note under the session key and returns its id.
	SaveNote(session string, n Note) (int64, error)
	// LoadNotes returns a session's notes, oldest first.
	LoadNotes(session string) ([]Note, error)
	// DeleteNote removes one.
	DeleteNote(session string, id int64) error
}

// Store is a session's notebook.
type Store struct {
	mu      sync.Mutex
	backend Backend
	session string
	notes   []Note
	nextID  int64
	now     func() time.Time
}

// New opens a store over backend; a nil backend is an in-memory notebook
// that lives as long as the process.
func New(backend Backend) *Store {
	return &Store{backend: backend, now: time.Now, nextID: 1}
}

// Bind names the session the notebook belongs to and loads what that
// session left. It is called when the session's slot is known — at start for
// a fresh one, at resume for a loaded one, and again when the session
// rebinds (a rewind that branches, a /clear). Notes written before the first
// Bind stay in memory and are written through under the new key.
func (s *Store) Bind(session string) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if session == s.session {
		return nil
	}
	s.session = session
	if s.backend == nil || session == "" {
		return nil
	}
	loaded, err := s.backend.LoadNotes(session)
	if err != nil {
		return err
	}
	// Anything already in memory was written before the bind; it goes to
	// the backend now, after what the slot already had.
	pending := s.notes
	s.notes = loaded
	for _, n := range loaded {
		if n.ID >= s.nextID {
			s.nextID = n.ID + 1
		}
	}
	for _, n := range pending {
		id, err := s.backend.SaveNote(session, n)
		if err != nil {
			return err
		}
		n.ID = id
		s.notes = append(s.notes, n)
		if id >= s.nextID {
			s.nextID = id + 1
		}
	}
	return nil
}

// Write adds a note. It returns the note as stored and the title of any
// note dropped to stay under MaxNotes.
func (s *Store) Write(author, title, body string) (Note, string, error) {
	title = strings.TrimSpace(title)
	body = strings.TrimSpace(body)
	switch {
	case title == "":
		return Note{}, "", errors.New("title is required")
	case len(title) > MaxTitleLen:
		return Note{}, "", fmt.Errorf("title is too long (%d chars, max %d)", len(title), MaxTitleLen)
	case body == "":
		return Note{}, "", errors.New("body is required")
	case len(body) > MaxBodyLen:
		return Note{}, "", fmt.Errorf("body is too long (%d chars, max %d) — a note is a paragraph; put the source in the evidence store or cite it", len(body), MaxBodyLen)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n := Note{ID: s.nextID, Author: author, Title: title, Body: body, Written: s.now()}
	if s.backend != nil && s.session != "" {
		id, err := s.backend.SaveNote(s.session, n)
		if err != nil {
			return Note{}, "", err
		}
		n.ID = id
	}
	if n.ID >= s.nextID {
		s.nextID = n.ID + 1
	}
	s.notes = append(s.notes, n)
	dropped := ""
	if len(s.notes) > MaxNotes {
		old := s.notes[0]
		s.notes = s.notes[1:]
		dropped = old.Title
		if s.backend != nil && s.session != "" {
			_ = s.backend.DeleteNote(s.session, old.ID)
		}
	}
	return n, dropped, nil
}

// List returns every note, oldest first.
func (s *Store) List() []Note {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Note, len(s.notes))
	copy(out, s.notes)
	return out
}

// Find returns the notes whose title or body contains every word of query,
// case-insensitively; an empty query is List.
func (s *Store) Find(query string) []Note {
	words := strings.Fields(strings.ToLower(query))
	all := s.List()
	if len(words) == 0 {
		return all
	}
	var out []Note
	for _, n := range all {
		hay := strings.ToLower(n.Title + "\n" + n.Body + "\n" + n.Author)
		hit := true
		for _, w := range words {
			if !strings.Contains(hay, w) {
				hit = false
				break
			}
		}
		if hit {
			out = append(out, n)
		}
	}
	return out
}

// Delete removes a note by id.
func (s *Store) Delete(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := sort.Search(len(s.notes), func(i int) bool { return s.notes[i].ID >= id })
	if i >= len(s.notes) || s.notes[i].ID != id {
		return fmt.Errorf("no note %d", id)
	}
	s.notes = append(s.notes[:i], s.notes[i+1:]...)
	if s.backend != nil && s.session != "" {
		return s.backend.DeleteNote(s.session, id)
	}
	return nil
}

// Len is the number of notes.
func (s *Store) Len() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.notes)
}

// Format renders notes for a model: one heading per note with its author
// and id, so a later call can cite or delete it.
func Format(notes []Note) string {
	if len(notes) == 0 {
		return "The notebook is empty."
	}
	var b strings.Builder
	for i, n := range notes {
		if i > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "## [n%d] %s — %s\n%s", n.ID, n.Title, n.Author, n.Body)
	}
	return b.String()
}

// PromptBlock is the notebook as a system-prompt section for an agent that
// starts after notes exist: the titles only, so a delegate knows what it can
// read without the whole notebook riding in its prompt.
func PromptBlock(notes []Note) string {
	if len(notes) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Notebook\nThe session's shared notebook already holds these notes; read_note returns any of them in full:\n")
	for _, n := range notes {
		fmt.Fprintf(&b, "- [n%d] %s (%s)\n", n.ID, n.Title, n.Author)
	}
	return strings.TrimRight(b.String(), "\n")
}
