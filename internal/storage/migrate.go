package storage

import (
	"context"
	"fmt"
)

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

	// An offer the person refused in a checkout. The answer lives here
	// rather than in the checkout for the reason the MCP trust above does:
	// an offer to write a file into a checkout cannot be recorded by
	// writing a file into that checkout
	// (docs/interface/surfaces.md#the-start-screen).
	`CREATE TABLE IF NOT EXISTS offers_declined (
		root        TEXT NOT NULL,
		offer       TEXT NOT NULL,
		declined_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
		PRIMARY KEY (root, offer)
	);`,

	// What a session was configured with, beside the prompt hash it already
	// carries. The columns have no default on purpose: a session recorded
	// before they existed reads as NULL — no answer — rather than as
	// whatever this build's defaults happen to be, which would put every
	// old session in today's cohort
	// (docs/capabilities/sessions-and-memory.md#what-a-session-ran-under).
	`ALTER TABLE agent_sessions ADD COLUMN mode TEXT;
	ALTER TABLE agent_sessions ADD COLUMN reasoning TEXT;
	ALTER TABLE agent_sessions ADD COLUMN max_rounds INTEGER;
	ALTER TABLE agent_sessions ADD COLUMN summary_model TEXT;
	ALTER TABLE agent_sessions ADD COLUMN summary_interval INTEGER;
	ALTER TABLE agent_sessions ADD COLUMN summary_enabled INTEGER;
	ALTER TABLE agent_sessions ADD COLUMN classifier_model TEXT;
	ALTER TABLE agent_sessions ADD COLUMN sandbox_profile TEXT;
	ALTER TABLE agent_sessions ADD COLUMN config_hash TEXT;`,
	`ALTER TABLE agent_sessions ADD COLUMN outcome TEXT;`,

	// A person's read on how a session went, beside the outcome the record
	// infers from how it ended. NULL is unrated, and it has to be
	// distinguishable from a thumbs-down: a column defaulted to 0 would
	// report every session anyone never got to as one somebody disliked
	// (docs/capabilities/sessions-and-memory.md#a-rating-is-how-you-check-the-inference).
	`ALTER TABLE agent_sessions ADD COLUMN rating INTEGER;`,

	// Whether the conversation in a slot was saved mid-turn, and where that
	// turn had got to. Nullable, because a slot written before the column
	// existed was not held — it was saved by a build that could not hold a
	// turn at all — and a default would open every one of them parked
	// (docs/capabilities/sessions-and-memory.md#a-held-turn-comes-back-held).
	`ALTER TABLE chat_sessions ADD COLUMN held TEXT;`,

	// What a conversation is opened again on: the summary its last
	// compaction wrote, and the commit the checkout was on when it was last
	// saved. Empty is the honest answer for both — a conversation that never
	// compacted has no summary, and one saved outside a repository was on no
	// commit — so unlike the mid-turn mark above these default rather than
	// being nullable
	// (docs/capabilities/sessions-and-memory.md#a-resumed-session-sees-the-tree-as-it-is).
	`ALTER TABLE chat_sessions ADD COLUMN summary TEXT NOT NULL DEFAULT '';
	ALTER TABLE chat_sessions ADD COLUMN head TEXT NOT NULL DEFAULT '';`,
}

// migrate brings the store up to the current schema, one step per
// transaction. Every step is applied under BEGIN IMMEDIATE with the version
// re-read inside the lock, because two connections can open the same store
// at once — the root command's background history purge and the command's
// own open — and a version read outside the lock lets both apply the same
// ALTER TABLE, which fails the second with a duplicate column. The busy
// timeout makes the loser wait rather than fail, and the re-read makes it
// find the step already recorded and move on.
func (db *DB) migrate() error {
	ctx := context.Background()
	_, err := db.sql.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`)
	if err != nil {
		return fmt.Errorf("create schema_version: %w", err)
	}
	// One conn for the whole run: BEGIN IMMEDIATE is a statement rather than
	// a database/sql option, and it has to run on the same connection the
	// step and its COMMIT do.
	conn, err := db.sql.Conn(ctx)
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	defer conn.Close()

	for {
		if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
			return fmt.Errorf("begin migration: %w", err)
		}
		var current int
		if err := conn.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&current); err != nil {
			_, _ = conn.ExecContext(ctx, `ROLLBACK`)
			return fmt.Errorf("read schema version: %w", err)
		}
		if current >= len(migrations) {
			_, _ = conn.ExecContext(ctx, `ROLLBACK`)
			return nil
		}
		if _, err := conn.ExecContext(ctx, migrations[current]); err != nil {
			_, _ = conn.ExecContext(ctx, `ROLLBACK`)
			return fmt.Errorf("migration %d: %w", current+1, err)
		}
		if _, err := conn.ExecContext(ctx, `INSERT INTO schema_version (version) VALUES (?)`, current+1); err != nil {
			_, _ = conn.ExecContext(ctx, `ROLLBACK`)
			return fmt.Errorf("record migration %d: %w", current+1, err)
		}
		if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
			return fmt.Errorf("commit migration %d: %w", current+1, err)
		}
	}
}
