package storage

import (
	"time"

	"github.com/rfizzle/shhh/internal/web"
)

// The sources ledger's persistence: one row per fetch and per search, keyed
// to the chat slot that made it, so a resumed session can still say what it
// read. The ledger owns the in-memory copy and the ordering; this layer only
// persists.
//
// The rows hang off the slot's row id rather than off its name, which is
// what makes them die with the conversation they belong to: a ledger that
// outlived a deleted chat would be a list of URLs with nothing left to
// explain why they were read.

// SaveSource writes one row under a slot and returns its id. A slot with no
// row in the store — a headless run, or one already pruned — is not an
// error and returns a zero id: the ledger keeps the row for the session
// either way, and only the resume loses it.
func (db *DB) SaveSource(slot string, s web.Source) (int64, error) {
	id, ok, err := db.chatSessionID(slot)
	if err != nil || !ok {
		return 0, err
	}
	at := s.At
	if at.IsZero() {
		at = time.Now()
	}
	res, err := db.sql.Exec(
		`INSERT INTO sources (session_id, turn, agent, kind, query, requested_url, final_url,
		     title, status, bytes, results, cached, evidence, at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, s.Turn, s.Agent, s.Kind, s.Query, s.Requested, s.FinalURL,
		s.Title, s.Status, s.Bytes, s.Results, s.Cached, s.Evidence, at.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// LoadSources returns a slot's rows, oldest first.
func (db *DB) LoadSources(slot string) ([]web.Source, error) {
	id, ok, err := db.chatSessionID(slot)
	if err != nil || !ok {
		return nil, err
	}
	rows, err := db.sql.Query(
		`SELECT id, turn, agent, kind, query, requested_url, final_url, title,
		     status, bytes, results, cached, evidence, at
		 FROM sources WHERE session_id = ? ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []web.Source
	for rows.Next() {
		var (
			s  web.Source
			at string
		)
		if err := rows.Scan(&s.ID, &s.Turn, &s.Agent, &s.Kind, &s.Query, &s.Requested,
			&s.FinalURL, &s.Title, &s.Status, &s.Bytes, &s.Results, &s.Cached,
			&s.Evidence, &at); err != nil {
			return nil, err
		}
		s.At, _ = time.Parse(time.RFC3339Nano, at)
		out = append(out, s)
	}
	return out, rows.Err()
}
