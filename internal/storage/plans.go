package storage

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// SavePlanRecord stores one approved plan and returns its opaque handle. The
// content is serialized by the caller, the way a handoff's is, so this layer
// stays content-agnostic.
func (db *DB) SavePlanRecord(chatSession string, content []byte) (string, error) {
	if len(content) == 0 {
		return "", fmt.Errorf("empty plan record")
	}
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	handle := "plan-" + hex.EncodeToString(raw[:])
	if err := db.execRetry(`INSERT INTO plan_records (handle, chat_session, content) VALUES (?, ?, ?)`,
		handle, chatSession, content); err != nil {
		return "", fmt.Errorf("save plan record: %w", err)
	}
	return handle, nil
}

// LoadPlanRecord reads a plan by its opaque handle. A handle the store does
// not have is refused rather than answered with an empty plan, which would be
// a session seeded with nothing and told it was carrying one.
func (db *DB) LoadPlanRecord(handle string) ([]byte, error) {
	var content []byte
	if err := db.sql.QueryRow(`SELECT content FROM plan_records WHERE handle = ?`, handle).Scan(&content); err != nil {
		return nil, fmt.Errorf("load plan record: %w", err)
	}
	return content, nil
}
