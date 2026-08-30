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

	`CREATE TABLE IF NOT EXISTS agent_sessions (
		id         INTEGER PRIMARY KEY,
		started_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
		ended_at   TEXT,
		kind       TEXT NOT NULL DEFAULT '',
		provider   TEXT NOT NULL,
		model      TEXT NOT NULL,
		turns      INTEGER NOT NULL DEFAULT 0,
		tokens_in  INTEGER NOT NULL DEFAULT 0,
		tokens_out INTEGER NOT NULL DEFAULT 0,
		est_cost   REAL NOT NULL DEFAULT 0
	);

	CREATE TABLE IF NOT EXISTS agent_events (
		id          INTEGER PRIMARY KEY,
		session_id  INTEGER NOT NULL REFERENCES agent_sessions(id) ON DELETE CASCADE,
		created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
		kind        TEXT NOT NULL,
		tool        TEXT NOT NULL DEFAULT '',
		duration_ms INTEGER,
		outcome     TEXT NOT NULL DEFAULT '',
		reason      TEXT NOT NULL DEFAULT ''
	);

	CREATE INDEX IF NOT EXISTS idx_agent_events_session ON agent_events(session_id);`,

	`ALTER TABLE agent_sessions ADD COLUMN parent_id INTEGER REFERENCES agent_sessions(id);`,

	`ALTER TABLE chat_sessions ADD COLUMN parent_id INTEGER REFERENCES chat_sessions(id) ON DELETE SET NULL;`,

	`CREATE TABLE IF NOT EXISTS memories (
		id         INTEGER PRIMARY KEY,
		scope      TEXT NOT NULL,
		kind       TEXT NOT NULL,
		text       TEXT NOT NULL,
		provenance TEXT NOT NULL,
		created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
		updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
	);

	CREATE INDEX IF NOT EXISTS idx_memories_scope ON memories(scope);`,

	`ALTER TABLE chat_messages ADD COLUMN attachments TEXT;`,

	`ALTER TABLE agent_events ADD COLUMN turn INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE agent_events ADD COLUMN round INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE agent_sessions ADD COLUMN version TEXT NOT NULL DEFAULT '';
	ALTER TABLE agent_sessions ADD COLUMN prompt_hash TEXT NOT NULL DEFAULT '';
	ALTER TABLE agent_sessions ADD COLUMN skills INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE agent_sessions ADD COLUMN project TEXT NOT NULL DEFAULT '';
	ALTER TABLE agent_sessions ADD COLUMN chat_session TEXT NOT NULL DEFAULT '';`,

	`CREATE TABLE IF NOT EXISTS notes (
		id         INTEGER PRIMARY KEY,
		session    TEXT NOT NULL,
		author     TEXT NOT NULL,
		title      TEXT NOT NULL,
		body       TEXT NOT NULL,
		written_at TEXT NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_notes_session ON notes(session);`,

	`CREATE TABLE IF NOT EXISTS mcp_trust (
		root        TEXT NOT NULL,
		name        TEXT NOT NULL,
		fingerprint TEXT NOT NULL,
		trusted_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
		PRIMARY KEY (root, name)
	);`,

	// A session's title is what a cheap model called it after its first
	// turn, kept apart from the name so a name the user typed is never
	// overwritten by one a model wrote
	// (docs/capabilities/sessions-and-memory.md#a-title-you-did-not-write).
	`ALTER TABLE chat_sessions ADD COLUMN title TEXT NOT NULL DEFAULT '';`,
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
