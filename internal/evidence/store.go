// Package evidence implements tool-output reduction and the evidence store
// behind it (S-064): bulky tool results are reduced deterministically before
// the model sees them, the full originals are kept under shhh's state dir,
// and the model can retrieve an original through the evidence tool using an
// opaque id — never a path.
package evidence

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	// MaxStoredBytes caps one stored original; anything past it is dropped
	// and the entry marked truncated.
	MaxStoredBytes = 4 << 20

	// RetentionAge is how long a session's evidence survives: stores from
	// sessions older than this are pruned whenever a new store opens.
	RetentionAge = 7 * 24 * time.Hour

	indexFile = "index.json"
)

// idRe is the shape of an evidence id. Ids are opaque tokens: every lookup
// resolves through the session index, so an id can never name a path.
var idRe = regexp.MustCompile(`^ev-[0-9a-f]{16}$`)

// Meta describes one stored original.
type Meta struct {
	Tool      string    `json:"tool"`
	Size      int64     `json:"size"`
	Stored    int64     `json:"stored"`
	Truncated bool      `json:"truncated,omitempty"`
	Created   time.Time `json:"created"`
}

// Store holds one session's full tool-result originals under
// <base>/<session>/, with 0700 directories and 0600 files.
type Store struct {
	dir     string
	session string

	mu    sync.Mutex
	index map[string]Meta
}

// Open creates (or reopens) the evidence store for one session and prunes
// stores left by sessions older than RetentionAge.
func Open(base, session string) (*Store, error) {
	if err := os.MkdirAll(base, 0o700); err != nil {
		return nil, err
	}
	pruneSessions(base)
	dir := filepath.Join(base, session)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s := &Store{dir: dir, session: session, index: map[string]Meta{}}
	if data, err := os.ReadFile(filepath.Join(dir, indexFile)); err == nil {
		_ = json.Unmarshal(data, &s.index)
	}
	return s, nil
}

// NewSessionID returns a fresh id scoping one session's evidence.
func NewSessionID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return time.Now().UTC().Format("20060102-150405") + "-" + hex.EncodeToString(b[:])
}

// Session is the id scoping this store.
func (s *Store) Session() string { return s.session }

// Put stores content as a new evidence entry and returns its opaque id.
func (s *Store) Put(tool string, content []byte) (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	id := "ev-" + hex.EncodeToString(b[:])
	meta := Meta{Tool: tool, Size: int64(len(content)), Stored: int64(len(content)), Created: time.Now().UTC()}
	if len(content) > MaxStoredBytes {
		content = content[:MaxStoredBytes]
		meta.Stored = MaxStoredBytes
		meta.Truncated = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.WriteFile(s.path(id), content, 0o600); err != nil {
		return "", err
	}
	s.index[id] = meta
	if err := s.writeIndexLocked(); err != nil {
		return "", err
	}
	return id, nil
}

// Info returns the metadata for one entry.
func (s *Store) Info(id string) (Meta, error) { return s.lookup(id) }

// Read returns stored bytes [offset, offset+limit) of an entry, clamped to
// what exists; limit must be positive.
func (s *Store) Read(id string, offset, limit int) ([]byte, Meta, error) {
	meta, err := s.lookup(id)
	if err != nil {
		return nil, Meta{}, err
	}
	if limit <= 0 {
		return nil, Meta{}, fmt.Errorf("limit must be positive")
	}
	data, err := os.ReadFile(s.path(id))
	if err != nil {
		return nil, Meta{}, fmt.Errorf("evidence unreadable: %w", err)
	}
	if offset < 0 {
		offset = 0
	}
	if offset > len(data) {
		offset = len(data)
	}
	end := offset + limit
	if end > len(data) {
		end = len(data)
	}
	return data[offset:end], meta, nil
}

// SearchMatch is one matching line from a stored original.
type SearchMatch struct {
	Line int
	Text string
}

// Search scans an entry for a literal substring (case-insensitive) and
// returns matching lines with their line numbers, at most max of them, plus
// the total match count.
func (s *Store) Search(id, query string, max int) ([]SearchMatch, int, error) {
	if _, err := s.lookup(id); err != nil {
		return nil, 0, err
	}
	data, err := os.ReadFile(s.path(id))
	if err != nil {
		return nil, 0, fmt.Errorf("evidence unreadable: %w", err)
	}
	needle := strings.ToLower(query)
	var out []SearchMatch
	total := 0
	for i, line := range strings.Split(string(data), "\n") {
		if !strings.Contains(strings.ToLower(line), needle) {
			continue
		}
		total++
		if len(out) < max {
			out = append(out, SearchMatch{Line: i + 1, Text: line})
		}
	}
	return out, total, nil
}

// StoreStats summarizes the store for the /evidence view.
type StoreStats struct {
	Entries     int
	StoredBytes int64
}

func (s *Store) Stats() StoreStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := StoreStats{Entries: len(s.index)}
	for _, m := range s.index {
		st.StoredBytes += m.Stored
	}
	return st
}

// Purge deletes every stored original in this session's store.
func (s *Store) Purge() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.RemoveAll(s.dir); err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	s.index = map[string]Meta{}
	return s.writeIndexLocked()
}

// lookup validates an id and resolves it through the session index; a
// malformed id and an id from another session fail the same way.
func (s *Store) lookup(id string) (Meta, error) {
	if !idRe.MatchString(id) {
		return Meta{}, fmt.Errorf("invalid evidence id %q", id)
	}
	s.mu.Lock()
	meta, ok := s.index[id]
	s.mu.Unlock()
	if !ok {
		return Meta{}, fmt.Errorf("no evidence with id %s in this session", id)
	}
	return meta, nil
}

func (s *Store) path(id string) string { return filepath.Join(s.dir, id+".dat") }

func (s *Store) writeIndexLocked() error {
	data, err := json.Marshal(s.index)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.dir, indexFile), data, 0o600)
}

// pruneSessions removes evidence directories older than RetentionAge;
// retention is best-effort hygiene, so failures are ignored.
func pruneSessions(base string) {
	entries, err := os.ReadDir(base)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-RetentionAge)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			_ = os.RemoveAll(filepath.Join(base, e.Name()))
		}
	}
}
