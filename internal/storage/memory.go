package storage

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// memoryStampLayout writes a memory's timestamp with every fractional digit
// in place. SQLite compares these columns as text, and time.RFC3339Nano drops
// trailing zeros, so it renders 10:00:00.500000000 as "10:00:00.5Z" and the
// later 10:00:00.512000000 as "10:00:00.512Z" — text that sorts the two the
// wrong way round, because 'Z' outranks '1'. A fixed width makes the text
// order the instant order. Reads still parse with RFC3339Nano, which accepts
// either width, so entries written before this are read back unchanged.
const memoryStampLayout = "2006-01-02T15:04:05.000000000Z07:00"

// memoryStamp renders an instant for storage in UTC.
func memoryStamp(t time.Time) string { return t.UTC().Format(memoryStampLayout) }

// Memory is one durable memory entry: a short text with a scope ("global" or
// a per-project key), a kind (preference, convention, correction, lesson),
// and its provenance (user-stated vs agent-proposed).
type Memory struct {
	ID         int64
	Scope      string
	Kind       string
	Text       string
	Provenance string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// AddMemory inserts a memory entry and returns it with its assigned id.
// Validation (kind, provenance, text bounds) belongs to internal/memory; the
// storage layer only persists.
func (db *DB) AddMemory(scope, kind, text, provenance string) (Memory, error) {
	now := time.Now().UTC()
	stamp := memoryStamp(now)
	res, err := db.sql.Exec(
		`INSERT INTO memories (scope, kind, text, provenance, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		scope, kind, text, provenance, stamp, stamp,
	)
	if err != nil {
		return Memory{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Memory{}, err
	}
	return Memory{ID: id, Scope: scope, Kind: kind, Text: text, Provenance: provenance, CreatedAt: now, UpdatedAt: now}, nil
}

// ListMemories returns the entries in any of the given scopes, most recently
// updated first, and breaks a tie on that column by id so that entries saved
// in the same instant still come back in one order — the caller's screen and
// a test have to agree. No scopes means no entries.
func (db *DB) ListMemories(scopes ...string) ([]Memory, error) {
	if len(scopes) == 0 {
		return nil, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?, ", len(scopes)), ", ")
	args := make([]any, len(scopes))
	for i, s := range scopes {
		args[i] = s
	}
	rows, err := db.sql.Query(
		`SELECT id, scope, kind, text, provenance, created_at, updated_at FROM memories
		 WHERE scope IN (`+placeholders+`) ORDER BY updated_at DESC, id DESC`, args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var memories []Memory
	for rows.Next() {
		var (
			m                    Memory
			createdAt, updatedAt string
		)
		if err := rows.Scan(&m.ID, &m.Scope, &m.Kind, &m.Text, &m.Provenance, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		m.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		m.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
		memories = append(memories, m)
	}
	return memories, rows.Err()
}

// GetMemory returns one entry by id.
func (db *DB) GetMemory(id int64) (Memory, error) {
	var (
		m                    Memory
		createdAt, updatedAt string
	)
	err := db.sql.QueryRow(
		`SELECT id, scope, kind, text, provenance, created_at, updated_at FROM memories WHERE id = ?`, id,
	).Scan(&m.ID, &m.Scope, &m.Kind, &m.Text, &m.Provenance, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return Memory{}, fmt.Errorf("memory %d not found", id)
	}
	if err != nil {
		return Memory{}, err
	}
	m.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	m.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	return m, nil
}

// UpdateMemory replaces one entry's text and stamps updated_at, so an edited
// memory is as recent as one just stated: the listing and recall are both
// ordered by that column, and a rewrite that left it alone would sink the
// entry the user just fixed below the ones they did not. Validation (text
// bounds) belongs to internal/memory; the storage layer only persists.
func (db *DB) UpdateMemory(id int64, text string) (Memory, error) {
	res, err := db.sql.Exec(
		`UPDATE memories SET text = ?, updated_at = ? WHERE id = ?`,
		text, memoryStamp(time.Now()), id,
	)
	if err != nil {
		return Memory{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Memory{}, fmt.Errorf("memory %d not found", id)
	}
	return db.GetMemory(id)
}

// DeleteMemory removes one entry by id.
func (db *DB) DeleteMemory(id int64) error {
	res, err := db.sql.Exec(`DELETE FROM memories WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("memory %d not found", id)
	}
	return nil
}
