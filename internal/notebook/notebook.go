// Package notebook is the shared channel between the agents of one session
// — a conversation's or a coding session's: a set of short, titled, signed
// notes that the orchestrator and every delegate can read and write. It is
// working state for one session — kept with the session so a resume brings
// it back, and never proposed to the user as memory.
// See docs/capabilities/subagents.md#what-they-share.
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
	// MaxPromptTitles bounds the titles block a child is spawned with
	// (PromptBlock). A title line is the title plus its id and its author,
	// so a full notebook is around 200 × 100 characters — roughly five
	// thousand tokens a child pays before it has read anything, spent again
	// per child of a fan-out. The cap is set from that arithmetic rather
	// than from any reading of how far back a session refers: forty lines
	// is about a thousand tokens, and what falls outside it is still in the
	// notebook and still reachable by read_note, so the cost of being wrong
	// is one round in the rare case rather than a window in every one.
	MaxPromptTitles = 40
)

// Note is one entry. Author is the agent that wrote it — the orchestrator's
// own name, or a delegate's — so a reader can weigh it, and Turn is the
// turn it was written in, so the turn that spawned a fan-out can say what
// came back from it.
type Note struct {
	ID      int64
	Author  string
	Title   string
	Body    string
	Turn    int64
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
	turn    int64
	scrub   func(string) string
	now     func() time.Time
}

// New opens a store over backend; a nil backend is an in-memory notebook
// that lives as long as the process.
func New(backend Backend) *Store {
	return &Store{backend: backend, now: time.Now, nextID: 1}
}

// SetScrub installs the rewrite a note goes through before it is kept. A
// note outlives the turn that wrote it — a row in the state directory that
// a resume reads back — so a vaulted value reaching it has leaked in the
// way that lasts longest, whatever the agent that wrote it was shown. It is
// installed on the store rather than wrapped around it so the bytes on disk,
// the bytes /notes prints and the bytes read_note returns are one text: a
// wrapper would see the note only after the backend had already written it.
//
// It is a function and not a vault so this package needs to know nothing
// about what a secret is. Nil is a session with no secrets.
// See docs/capabilities/secrets.md#the-value-is-scrubbed-at-every-door.
func (s *Store) SetScrub(scrub func(string) string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.scrub = scrub
	s.mu.Unlock()
}

// SetTurn tells the notebook which turn is open, so a note a delegate
// writes carries the turn its parent spawned it in. The caller is the
// surface that owns the turn counter; a store nobody tells stamps zero,
// which reads as "no turn said so" rather than as turn one.
func (s *Store) SetTurn(turn int64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.turn = turn
	s.mu.Unlock()
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
//
// The scrub runs before the bounds are checked, not after, so the note that
// is measured, refused, stored and echoed is one text. Scrubbing afterwards
// would let a note pass the length check as the model wrote it and then grow
// past it as a placeholder replaced a short value — and the copy on disk
// would no longer be the copy the writer was told about.
func (s *Store) Write(author, title, body string) (Note, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.scrub != nil {
		title, body = s.scrub(title), s.scrub(body)
	}
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
	n := Note{ID: s.nextID, Author: author, Title: title, Body: body, Turn: s.turn, Written: s.now()}
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

// Delete removes a note by id. It is the person's route only: no agent has
// a tool that reaches it (tool.go registers a write and a read and nothing
// else), so a delegate can add to what the session knows and can never take
// something out of it behind the person's back.
// See docs/capabilities/subagents.md#what-they-share.
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
// and id, so a later call can cite it.
func Format(notes []Note) string {
	if len(notes) == 0 {
		return "The notebook is empty."
	}
	return formatNotes(notes, true)
}

// formatNotes is Format's body, with the author dropped where the reader
// already has it from the heading above.
func formatNotes(notes []Note, signed bool) string {
	var b strings.Builder
	for i, n := range notes {
		if i > 0 {
			b.WriteString("\n\n")
		}
		if signed {
			fmt.Fprintf(&b, "## [n%d] %s — %s\n%s", n.ID, n.Title, n.Author, n.Body)
			continue
		}
		fmt.Fprintf(&b, "## [n%d] %s\n%s", n.ID, n.Title, n.Body)
	}
	return b.String()
}

// PromptBlock is the notebook as a system-prompt section for a delegate:
// what the notebook is for, and the titles already in it, so the child
// starts by reading rather than re-finding — without the whole notebook
// riding in its prompt.
//
// The block is emitted here rather than written into the sub-agent prompts
// because a prompt must never name a tool the session might not have: this
// text is appended exactly where the two notebook tools were registered, so
// the child is told about a store it can really reach.
//
// Only the newest MaxPromptTitles titles are listed, with a line saying how
// many are not. The block goes into the child's system prompt, so what it
// costs is counted in the session's system-prompt category like every other
// block — the cap is what keeps that cost bounded as a long session's
// notebook fills.
func PromptBlock(notes []Note) string {
	var b strings.Builder
	b.WriteString("# Notebook\n")
	b.WriteString("This session has a shared notebook that every agent in it — the orchestrator and every other delegate — reads and writes. Read it before you go looking: a sibling may already have found what you are about to re-find. Write a note for what the rest of the session will need, not for what only your report has to say. You can add to the notebook and read it; you cannot remove anything from it.\n")
	if len(notes) == 0 {
		b.WriteString("It is empty so far.")
		return b.String()
	}
	shown := notes
	if len(shown) > MaxPromptTitles {
		shown = shown[len(shown)-MaxPromptTitles:]
		fmt.Fprintf(&b, "It already holds %d notes; the %d most recent are listed below, and read_note returns any note in full, listed here or not:\n",
			len(notes), len(shown))
	} else {
		b.WriteString("It already holds these notes; read_note returns any of them in full:\n")
	}
	for _, n := range shown {
		fmt.Fprintf(&b, "- [n%d] %s (%s)\n", n.ID, n.Title, n.Author)
	}
	return strings.TrimRight(b.String(), "\n")
}

// FormatByAuthor is the notebook as the person reads it: the notes grouped
// under the agent that wrote each one, authors in the order they first
// wrote. Format's flat, oldest-first list is what an agent reads, because an
// agent is looking for a fact and the session's order is the useful one;
// somebody reading their own session is asking who found what, and a
// fan-out's notes arrive interleaved.
// See docs/capabilities/subagents.md#what-they-share.
func FormatByAuthor(notes []Note) string {
	if len(notes) == 0 {
		return "The notebook is empty."
	}
	var authors []string
	byAuthor := map[string][]Note{}
	for _, n := range notes {
		if _, seen := byAuthor[n.Author]; !seen {
			authors = append(authors, n.Author)
		}
		byAuthor[n.Author] = append(byAuthor[n.Author], n)
	}
	var b strings.Builder
	for i, a := range authors {
		if i > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "# %s\n\n%s", a, formatNotes(byAuthor[a], false))
	}
	return b.String()
}

// WrittenIn returns the notes written in one turn by anyone but author —
// what a fan-out left behind, which is the thing the turn that spawned it
// has to be told about. A note stamped with turn zero was written before
// any surface said which turn was open and belongs to no turn.
func WrittenIn(notes []Note, turn int64, author string) []Note {
	if turn <= 0 {
		return nil
	}
	var out []Note
	for _, n := range notes {
		if n.Turn == turn && n.Author != author {
			out = append(out, n)
		}
	}
	return out
}
