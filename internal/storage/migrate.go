package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	sqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
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

	// What tells a session that is still running from one that crashed with
	// its end time never written. An open row — ended_at null — has always
	// meant both, so nothing could say whether another session had this
	// checkout open right now
	// (docs/capabilities/sessions-and-memory.md#a-session-knows-it-is-not-alone).
	//
	// The process id answers it and the heartbeat guards the id: ids are
	// reused, so a row whose process died without closing it would otherwise
	// be vouched for by whatever inherited its number. The checkout is
	// matched on the project fingerprint the row already carries, because
	// this table is content-free by construction and a workspace path is a
	// path.
	//
	// Both default to the values that mean "nothing was recorded", so a row
	// written before this reads as a session nobody can vouch for rather
	// than as a live one: an unknown pid never makes a sibling and is never
	// reconciled away.
	`ALTER TABLE agent_sessions ADD COLUMN pid INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE agent_sessions ADD COLUMN heartbeat TEXT NOT NULL DEFAULT '';`,

	// Trust is the checkout's, not one server's. A repository names skills,
	// agent profiles, quality suites, hooks and servers, and all of them run
	// as whoever cloned it, so one answer per root replaces the per-server
	// one (docs/capabilities/approvals-and-safety.md#a-checkout-declares-what-it-runs).
	//
	// The old rows are carried so a checkout somebody already answered for
	// is still on record. Their fingerprints are not the new one — that
	// covered a single definition and this covers everything the checkout
	// declares — so the first session in such a checkout reads it as edited
	// and asks once. Widening a person's answer without asking would grant
	// trust over four kinds of thing they were never shown.
	`CREATE TABLE IF NOT EXISTS project_trust (
		root        TEXT NOT NULL PRIMARY KEY,
		fingerprint TEXT NOT NULL,
		trusted_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
	);
	INSERT OR IGNORE INTO project_trust (root, fingerprint, trusted_at)
		SELECT root, MIN(fingerprint), MIN(trusted_at) FROM mcp_trust GROUP BY root;
	DROP TABLE mcp_trust;`,

	// A full-text index over everything anything is ever searched for: what
	// was said in a conversation, what a conversation was called, and the
	// prompt and the command a request was recorded under. Searching
	// used to be a wildcarded LIKE, which reads every row in the table and
	// gets slower in exactly the store a long-running user has
	// (docs/capabilities/sessions-and-memory.md#finding-a-conversation-again).
	//
	// Each index is external-content — the terms live here and the text
	// stays in the table it came from — so nothing is stored twice, and
	// triggers keep the two in step. A message appended to a conversation
	// costs the one index row it earns; a message deleted takes its row with
	// it, which is why the delete trigger names the old text: an
	// external-content index cannot look up a row that has already gone.
	//
	// The rebuild at the end of each is the backfill for a store that
	// already holds conversations, and it is the only time the whole table
	// is read.
	//
	// Two of the update triggers name the columns they watch, because the
	// rows they sit on are updated for other reasons: every autosave stamps
	// a session's time, and a command's exit code and rating are written
	// after the fact. Re-indexing a title or a prompt that did not change
	// would be work done on the busiest path in the store for no answer.
	`CREATE VIRTUAL TABLE chat_message_search USING fts5(
		content, content='chat_messages', content_rowid='id', tokenize='unicode61'
	);
	CREATE TRIGGER chat_messages_search_insert AFTER INSERT ON chat_messages BEGIN
		INSERT INTO chat_message_search (rowid, content) VALUES (new.id, new.content);
	END;
	CREATE TRIGGER chat_messages_search_delete AFTER DELETE ON chat_messages BEGIN
		INSERT INTO chat_message_search (chat_message_search, rowid, content)
			VALUES ('delete', old.id, old.content);
	END;
	CREATE TRIGGER chat_messages_search_update AFTER UPDATE ON chat_messages BEGIN
		INSERT INTO chat_message_search (chat_message_search, rowid, content)
			VALUES ('delete', old.id, old.content);
		INSERT INTO chat_message_search (rowid, content) VALUES (new.id, new.content);
	END;
	INSERT INTO chat_message_search (chat_message_search) VALUES ('rebuild');

	CREATE VIRTUAL TABLE chat_title_search USING fts5(
		title, content='chat_sessions', content_rowid='id', tokenize='unicode61'
	);
	CREATE TRIGGER chat_sessions_search_insert AFTER INSERT ON chat_sessions BEGIN
		INSERT INTO chat_title_search (rowid, title) VALUES (new.id, new.title);
	END;
	CREATE TRIGGER chat_sessions_search_delete AFTER DELETE ON chat_sessions BEGIN
		INSERT INTO chat_title_search (chat_title_search, rowid, title)
			VALUES ('delete', old.id, old.title);
	END;
	CREATE TRIGGER chat_sessions_search_update AFTER UPDATE OF title ON chat_sessions BEGIN
		INSERT INTO chat_title_search (chat_title_search, rowid, title)
			VALUES ('delete', old.id, old.title);
		INSERT INTO chat_title_search (rowid, title) VALUES (new.id, new.title);
	END;
	INSERT INTO chat_title_search (chat_title_search) VALUES ('rebuild');

	CREATE VIRTUAL TABLE request_search USING fts5(
		prompt, command, content='requests', content_rowid='id', tokenize='unicode61'
	);
	CREATE TRIGGER requests_search_insert AFTER INSERT ON requests BEGIN
		INSERT INTO request_search (rowid, prompt, command) VALUES (new.id, new.prompt, new.command);
	END;
	CREATE TRIGGER requests_search_delete AFTER DELETE ON requests BEGIN
		INSERT INTO request_search (request_search, rowid, prompt, command)
			VALUES ('delete', old.id, old.prompt, old.command);
	END;
	CREATE TRIGGER requests_search_update AFTER UPDATE OF prompt, command ON requests BEGIN
		INSERT INTO request_search (request_search, rowid, prompt, command)
			VALUES ('delete', old.id, old.prompt, old.command);
		INSERT INTO request_search (rowid, prompt, command) VALUES (new.id, new.prompt, new.command);
	END;
	INSERT INTO request_search (request_search) VALUES ('rebuild');`,

	// What a turn changed, kept past the sitting that changed it. The
	// changeset holds a turn's records in memory so the turn can be reviewed
	// and put back, and in memory is where they ended: closing the terminal
	// was the same act as accepting every edit the session had made
	// (docs/capabilities/coding-agent.md#a-turn-ends-with-what-changed).
	//
	// The rows hang off the slot the conversation is autosaved to, so a
	// conversation deleted or pruned takes its records with it and a resumed
	// one finds its own. The turn number is the one on the close row, which is
	// what a person types at /undo, so the pair is unique per slot.
	//
	// The bytes live apart from the rows that name them, addressed by their
	// digest: one file edited ten times in an afternoon is ten records over a
	// handful of distinct contents, and the before side of a turn is nearly
	// always the after side of the turn before it. A blob is left out of the
	// cascade deliberately — several rows point at one, and there is no
	// reference count for a foreign key to follow, so unreachable content is
	// collected when rows are deleted instead.
	`CREATE TABLE IF NOT EXISTS change_blobs (
		hash    TEXT PRIMARY KEY,
		content BLOB NOT NULL
	);

	CREATE TABLE IF NOT EXISTS changes (
		id            INTEGER PRIMARY KEY,
		session_id    INTEGER NOT NULL REFERENCES chat_sessions(id) ON DELETE CASCADE,
		turn          INTEGER NOT NULL,
		seq           INTEGER NOT NULL,
		path          TEXT NOT NULL,
		before_hash   TEXT NOT NULL DEFAULT '',
		after_hash    TEXT NOT NULL DEFAULT '',
		before_exists INTEGER NOT NULL DEFAULT 0,
		after_exists  INTEGER NOT NULL DEFAULT 0,
		before_mode   INTEGER NOT NULL DEFAULT 0,
		after_mode    INTEGER NOT NULL DEFAULT 0,
		agent         TEXT NOT NULL DEFAULT '',
		origin        INTEGER NOT NULL DEFAULT 0,
		track         INTEGER NOT NULL DEFAULT 0,
		at            TEXT NOT NULL,
		UNIQUE(session_id, turn, path)
	);

	CREATE INDEX IF NOT EXISTS idx_changes_session_turn ON changes(session_id, turn);`,
}

// migrate brings the store up to the current schema, one step per
// transaction. Every step is applied under BEGIN IMMEDIATE with the version
// re-read inside the lock, because two connections can open the same store
// at once — the root command's background history purge and the command's
// own open — and a version read outside the lock lets both apply the same
// ALTER TABLE, which fails the second with a duplicate column. The busy
// timeout makes the loser wait rather than fail, and the re-read makes it
// find the step already recorded and move on.
//
// The version table itself is created under the same lock. As a bare
// statement it starts a read transaction to look the table up and only then
// asks to write, and in WAL mode a writer whose snapshot has gone stale is
// refused outright (SQLITE_BUSY_SNAPSHOT) rather than made to wait — so two
// openers of a brand-new store could race, and the busy timeout never got a
// say. BEGIN IMMEDIATE takes the write lock before reading anything, and
// that wait is the one the timeout covers.
func (db *DB) migrate() error {
	ctx := context.Background()
	// One conn for the whole run: BEGIN IMMEDIATE is a statement rather than
	// a database/sql option, and it has to run on the same connection the
	// step and its COMMIT do.
	conn, err := db.sql.Conn(ctx)
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return fmt.Errorf("begin migration: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		_, _ = conn.ExecContext(ctx, `ROLLBACK`)
		return fmt.Errorf("create schema_version: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return fmt.Errorf("create schema_version: %w", err)
	}

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

// openRetries bounds how many times a fresh opener tries the migration again
// after SQLite refused its lock outright, and openRetryWait is the pause
// between tries — together about a second, which is longer than any first
// migration step takes.
const (
	openRetries   = 20
	openRetryWait = 50 * time.Millisecond
)

// migrateWithRetry is migrate, tried again when SQLite refused a lock rather
// than waiting for it. The busy timeout covers a wait, but there is one
// SQLite will not make: two openers of a brand-new store, one holding a
// shared lock while it switches the journal mode and the other holding a
// reserved lock while it commits the first step, would each be waiting on
// the other, so the switcher is handed SQLITE_BUSY at once instead of the
// busy handler (sqlite.org/c3ref/busy_handler.html). Nothing is half-done on
// that connection — the pragma is the first thing it ran — so it starts over,
// and the second time the store is already in WAL and the wait is one the
// timeout covers. It showed up as a one-in-hundreds failure of the concurrent
// opener test on the CI runner and never on a workstation.
func (db *DB) migrateWithRetry() error {
	var err error
	for attempt := 0; attempt <= openRetries; attempt++ {
		if err = db.migrate(); err == nil || !refusedLock(err) {
			return err
		}
		time.Sleep(openRetryWait)
	}
	return err
}

// refusedLock reports whether err is SQLITE_BUSY: a lock SQLite handed back
// rather than waited for. A busy timeout that ran out arrives as the same
// code, and after openRetries the difference no longer matters.
func refusedLock(err error) bool {
	var e *sqlite.Error
	return errors.As(err, &e) && e.Code() == sqlite3.SQLITE_BUSY
}
