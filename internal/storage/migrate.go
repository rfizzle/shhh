package storage

import "fmt"

var migrations = []string{
	`CREATE TABLE IF NOT EXISTS chat_sessions (
		id         INTEGER PRIMARY KEY,
		name       TEXT NOT NULL UNIQUE,
		created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
		updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
	);

	CREATE TABLE IF NOT EXISTS chat_messages (
		id         INTEGER PRIMARY KEY,
		session_id INTEGER NOT NULL REFERENCES chat_sessions(id) ON DELETE CASCADE,
		seq        INTEGER NOT NULL,
		role       TEXT NOT NULL,
		content    TEXT NOT NULL DEFAULT '',
		tool_calls TEXT,
		tool_call_id TEXT NOT NULL DEFAULT '',
		UNIQUE(session_id, seq)
	);

	CREATE TABLE IF NOT EXISTS requests (
		id          INTEGER PRIMARY KEY,
		created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
		provider    TEXT NOT NULL,
		model       TEXT NOT NULL,
		prompt      TEXT NOT NULL DEFAULT '',
		command     TEXT NOT NULL DEFAULT '',
		action      TEXT NOT NULL DEFAULT '',
		ttft_ms     INTEGER,
		duration_ms INTEGER,
		tokens_in   INTEGER,
		tokens_out  INTEGER,
		success     INTEGER NOT NULL DEFAULT 1
	);

	CREATE TABLE IF NOT EXISTS snippets (
		id         INTEGER PRIMARY KEY,
		name       TEXT NOT NULL UNIQUE,
		command    TEXT NOT NULL,
		created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
		updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
	);`,

	`ALTER TABLE requests ADD COLUMN exit_code INTEGER;`,

	`ALTER TABLE snippets ADD COLUMN description TEXT NOT NULL DEFAULT '';`,

	`ALTER TABLE requests ADD COLUMN rating INTEGER;`,
}

func (db *DB) migrate() error {
	_, err := db.sql.Exec(`CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`)
	if err != nil {
		return fmt.Errorf("create schema_version: %w", err)
	}

	var current int
	row := db.sql.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_version`)
	if err := row.Scan(&current); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}

	for i := current; i < len(migrations); i++ {
		tx, err := db.sql.Begin()
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", i+1, err)
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_version (version) VALUES (?)`, i+1); err != nil {
			tx.Rollback()
			return fmt.Errorf("record migration %d: %w", i+1, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", i+1, err)
		}
	}
	return nil
}
