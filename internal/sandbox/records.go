package sandbox

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rfizzle/shhh/internal/storage"
)

// Record is one durable sandbox-ownership entry. Every container shhh creates
// is recorded before use and reconciled against the engine at startup, so a
// crash can never orphan a container silently — the record survives to be
// reaped.
type Record struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Engine    string    `json:"engine"`
	Image     string    `json:"image"`
	Workspace string    `json:"workspace"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Expired reports whether the record's TTL has passed.
func (r Record) Expired(now time.Time) bool {
	return now.After(r.ExpiresAt)
}

// Store persists ownership records as a user-only JSON file under shhh's
// state directory.
type Store struct {
	path string
}

// OpenStore opens the default ownership store, creating the state directory
// (0700) if needed.
func OpenStore() (*Store, error) {
	dir, err := storage.Dir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return NewStoreAt(filepath.Join(dir, "sandboxes.json")), nil
}

// NewStoreAt opens a store at an explicit path (tests).
func NewStoreAt(path string) *Store {
	return &Store{path: path}
}

// List returns all records; a missing file is an empty store.
func (s *Store) List() ([]Record, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var recs []Record
	if err := json.Unmarshal(data, &recs); err != nil {
		return nil, fmt.Errorf("corrupt sandbox records %s: %w", s.path, err)
	}
	return recs, nil
}

// Get returns the record with the given id.
func (s *Store) Get(id string) (Record, error) {
	recs, err := s.List()
	if err != nil {
		return Record{}, err
	}
	for _, r := range recs {
		if r.ID == id {
			return r, nil
		}
	}
	return Record{}, fmt.Errorf("no sandbox %q", id)
}

// Add appends a record.
func (s *Store) Add(rec Record) error {
	recs, err := s.List()
	if err != nil {
		return err
	}
	return s.write(append(recs, rec))
}

// Remove drops the record with the given id; removing an absent id is a
// no-op.
func (s *Store) Remove(id string) error {
	recs, err := s.List()
	if err != nil {
		return err
	}
	kept := recs[:0]
	for _, r := range recs {
		if r.ID != id {
			kept = append(kept, r)
		}
	}
	return s.write(kept)
}

func (s *Store) write(recs []Record) error {
	data, err := json.MarshalIndent(recs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o600)
}
