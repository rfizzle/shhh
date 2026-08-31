package reports

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"time"
)

const (
	// MaxStoredBytes caps one report document; a report is a page, not an
	// archive, and anything past this is refused rather than truncated —
	// half a page is a different page.
	MaxStoredBytes = 2 << 20

	indexFile = "index.json"
)

// idRe is the shape of a report id. Ids are opaque tokens: every lookup
// resolves through the index, so an id can never name a path — and 64
// random bits is also what makes the serving URL unguessable.
var idRe = regexp.MustCompile(`^rp-[0-9a-f]{16}$`)

// Meta describes one stored report.
type Meta struct {
	Title   string    `json:"title"`
	Project string    `json:"project"` // absolute project root, the same key mcp trust uses
	Origin  string    `json:"origin"`  // the command the session ran under: chat, code, print
	Created time.Time `json:"created"`
	Size    int64     `json:"size"`
}

// Entry is one listing row: an id and its metadata.
type Entry struct {
	ID string
	Meta
}

// Store holds every project's reports under one base directory, 0700, each
// document a 0600 file named by id with the metadata in one index.
type Store struct {
	dir string

	mu    sync.Mutex
	index map[string]Meta
}

// Open creates (or reopens) the report store and prunes reports older than
// retentionDays. Retention is best-effort hygiene: prune failures are
// ignored, a corrupt index starts empty rather than failing the session.
func Open(base string, retentionDays int) (*Store, error) {
	if err := os.MkdirAll(base, 0o700); err != nil {
		return nil, err
	}
	s := &Store{dir: base, index: map[string]Meta{}}
	if data, err := os.ReadFile(filepath.Join(base, indexFile)); err == nil {
		_ = json.Unmarshal(data, &s.index)
	}
	s.prune(retentionDays)
	return s, nil
}

// Dir is the directory this store holds its reports in.
func (s *Store) Dir() string { return s.dir }

// Put stores one validated document and returns its opaque id. Freehand
// blocks must already hold their frozen ValidateFreehand serialization.
func (s *Store) Put(doc Document, meta Meta) (string, error) {
	data, err := json.Marshal(doc)
	if err != nil {
		return "", err
	}
	if len(data) > MaxStoredBytes {
		return "", fmt.Errorf("report is %d bytes; the store holds %d per report — trim the largest block", len(data), MaxStoredBytes)
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	id := "rp-" + hex.EncodeToString(b[:])
	meta.Size = int64(len(data))
	if meta.Created.IsZero() {
		meta.Created = time.Now().UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.WriteFile(s.path(id), data, 0o600); err != nil {
		return "", err
	}
	s.index[id] = meta
	if err := s.writeIndexLocked(); err != nil {
		return "", err
	}
	return id, nil
}

// Load resolves an id through the index and returns the stored document.
func (s *Store) Load(id string) (Document, Meta, error) {
	meta, err := s.lookup(id)
	if err != nil {
		return Document{}, Meta{}, err
	}
	data, err := os.ReadFile(s.path(id))
	if err != nil {
		return Document{}, Meta{}, fmt.Errorf("report unreadable: %w", err)
	}
	var doc Document
	if err := json.Unmarshal(data, &doc); err != nil {
		return Document{}, Meta{}, fmt.Errorf("report unreadable: %w", err)
	}
	return doc, meta, nil
}

// List returns every stored report, newest first.
func (s *Store) List() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Entry, 0, len(s.index))
	for id, m := range s.index {
		out = append(out, Entry{ID: id, Meta: m})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out
}

// lookup validates an id and resolves it through the index; a malformed id
// and an unknown one fail with the same wording.
func (s *Store) lookup(id string) (Meta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !idRe.MatchString(id) {
		return Meta{}, fmt.Errorf("no report with id %q", id)
	}
	meta, ok := s.index[id]
	if !ok {
		return Meta{}, fmt.Errorf("no report with id %q", id)
	}
	return meta, nil
}

func (s *Store) path(id string) string { return filepath.Join(s.dir, id+".json") }

func (s *Store) writeIndexLocked() error {
	data, err := json.Marshal(s.index)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.dir, indexFile), data, 0o600)
}

// Census reads how many reports the store holds and how much disk they use,
// without opening it: a diagnostic looks, it never prunes. A directory that
// does not exist yet is a fresh install — zero of each, not an error.
func Census(base string) (count int, bytes int64, err error) {
	data, err := os.ReadFile(filepath.Join(base, indexFile))
	if os.IsNotExist(err) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	index := map[string]Meta{}
	if err := json.Unmarshal(data, &index); err != nil {
		return 0, 0, fmt.Errorf("index unreadable: %w", err)
	}
	for _, m := range index {
		count++
		bytes += m.Size
	}
	return count, bytes, nil
}

// prune drops index entries older than retentionDays and deletes their
// files, plus any orphaned report file the index no longer names.
func (s *Store) prune(retentionDays int) {
	if retentionDays <= 0 {
		return
	}
	cutoff := time.Now().Add(-time.Duration(retentionDays) * 24 * time.Hour)
	s.mu.Lock()
	changed := false
	for id, m := range s.index {
		if m.Created.Before(cutoff) {
			_ = os.Remove(s.path(id))
			delete(s.index, id)
			changed = true
		}
	}
	if changed {
		_ = s.writeIndexLocked()
	}
	s.mu.Unlock()

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range entries {
		name := e.Name()
		if name == indexFile || filepath.Ext(name) != ".json" {
			continue
		}
		id := name[:len(name)-len(".json")]
		if _, ok := s.index[id]; !ok && idRe.MatchString(id) {
			_ = os.Remove(filepath.Join(s.dir, name))
		}
	}
}
