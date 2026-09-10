package storage

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// SaveChildHandoff stores the sanitized handoff for one failed child attempt
// and returns its opaque handle. Handoff content is deliberately separate from
// agent_sessions: observability rows remain safe to aggregate and export.
func (db *DB) SaveChildHandoff(sessionID int64, content []byte) (string, error) {
	if sessionID <= 0 {
		return "", fmt.Errorf("invalid child session")
	}
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	handle := "handoff-" + hex.EncodeToString(raw[:])
	if err := db.execRetry(`INSERT INTO child_handoffs (handle, child_session_id, content) VALUES (?, ?, ?)`, handle, sessionID, content); err != nil {
		return "", fmt.Errorf("save child handoff: %w", err)
	}
	return handle, nil
}

// LoadChildHandoff reads a handoff by its opaque handle. A missing or expired
// handle is refused instead of being treated as an empty handoff.
func (db *DB) LoadChildHandoff(handle string) ([]byte, error) {
	var content []byte
	if err := db.sql.QueryRow(`SELECT content FROM child_handoffs WHERE handle = ?`, handle).Scan(&content); err != nil {
		return nil, fmt.Errorf("load child handoff: %w", err)
	}
	return content, nil
}
