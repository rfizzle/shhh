package storage

// What a turn changed, written down. The changeset holds a turn's records in
// memory — the whole file on both sides of every edit — so a turn can be
// reviewed and put back; in memory is where they used to end, which made
// closing the terminal the same act as accepting every edit the session had
// made. These rows are those records kept, keyed by the slot the conversation
// is autosaved to, so a resumed conversation can still take one of its turns
// back.
// See docs/capabilities/coding-agent.md#a-turn-ends-with-what-changed.
//
// The bytes are addressed by their digest and kept in a table of their own:
// one file edited ten times in an afternoon is ten records over a handful of
// distinct contents, and the before side of a turn is nearly always the after
// side of the turn before it. The rows carry digests, the blobs carry bytes,
// and a blob nothing names any more is collected when the rows are pruned
// rather than by a foreign key — several rows point at one blob, and there is
// no reference count for a cascade to follow.

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"time"

	"github.com/rfizzle/shhh/internal/changeset"
)

// SaveChange writes one file's record for a turn, replacing whatever that
// path held there before. It is one record and not the whole turn on purpose:
// the changeset writes as each edit lands, so a turn touching a dozen files
// would otherwise rewrite every row a dozen times — quadratic in the size of
// the turn, on a statement the session holds a lock across.
//
// A slot with no row in the store — nothing was ever claimed, or it has been
// pruned — is not an error. There is nothing to key the record to, and a
// session whose slot went away has already lost the conversation it belongs
// to.
func (db *DB) SaveChange(slot string, turn int64, seq int, r changeset.Record) error {
	id, ok, err := db.chatSessionID(slot)
	if err != nil || !ok {
		return err
	}
	tx, err := db.sql.Begin()
	if err != nil {
		return fmt.Errorf("save change: %w", err)
	}
	defer tx.Rollback()

	// The blobs go in first, so a row never names content that is not there
	// yet: the read tolerates a missing blob, but only because it has to
	// tolerate a pruned one.
	before, err := putBlob(tx, r.Before)
	if err != nil {
		return err
	}
	after, err := putBlob(tx, r.After)
	if err != nil {
		return err
	}
	at := r.At
	if at.IsZero() {
		at = time.Now()
	}
	_, err = tx.Exec(
		`INSERT INTO changes (session_id, turn, seq, path, before_hash, after_hash,
		     before_exists, after_exists, before_mode, after_mode, agent, origin, track, at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(session_id, turn, path) DO UPDATE SET
		     seq = excluded.seq,
		     before_hash = excluded.before_hash, after_hash = excluded.after_hash,
		     before_exists = excluded.before_exists, after_exists = excluded.after_exists,
		     before_mode = excluded.before_mode, after_mode = excluded.after_mode,
		     agent = excluded.agent, origin = excluded.origin,
		     track = excluded.track, at = excluded.at`,
		id, turn, seq, r.Path, before, after,
		r.BeforeExists, r.AfterExists, uint32(r.BeforeMode), uint32(r.AfterMode),
		r.Agent, int(r.Origin), int(r.Track), at.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("save change: %w", err)
	}
	return tx.Commit()
}

// DropChange removes one path's record from a turn — the turn edited the file
// back to where it found it, so there is no change of it left to put back.
// Nothing there to remove is the state asked for, not an error.
func (db *DB) DropChange(slot string, turn int64, path string) error {
	_, err := db.sql.Exec(
		`DELETE FROM changes WHERE turn = ? AND path = ? AND session_id IN (
		     SELECT id FROM chat_sessions WHERE name = ?)`, turn, path, slot)
	if err != nil {
		return fmt.Errorf("drop change: %w", err)
	}
	return nil
}

// LoadChanges reads back a slot's records, turn by turn, from turn `from`
// onwards. A `to` of zero or less has no upper bound, which is what reading
// everything a rewind would put back asks for.
//
// The records come back without their hunks: the counts and the diff are
// computed from the two sides, and recomputing them is cheaper than storing a
// derivation that could disagree with the content beside it.
func (db *DB) LoadChanges(slot string, from, to int64) ([]changeset.TurnRecords, error) {
	rows, err := db.sql.Query(
		`SELECT c.turn, c.path, b.content, a.content, c.before_exists, c.after_exists,
		        c.before_mode, c.after_mode, c.agent, c.origin, c.track, c.at
		   FROM changes c
		   JOIN chat_sessions s ON s.id = c.session_id
		   LEFT JOIN change_blobs b ON b.hash = c.before_hash
		   LEFT JOIN change_blobs a ON a.hash = c.after_hash
		  WHERE s.name = ? AND c.turn >= ? AND (? <= 0 OR c.turn <= ?)
		  ORDER BY c.turn, c.seq`, slot, from, to, to)
	if err != nil {
		return nil, fmt.Errorf("load changes: %w", err)
	}
	defer rows.Close()

	var out []changeset.TurnRecords
	for rows.Next() {
		var (
			turn                  int64
			before, after         sql.NullString
			beforeMode, afterMode uint32
			origin, track         int
			at                    string
			r                     changeset.Record
		)
		if err := rows.Scan(&turn, &r.Path, &before, &after, &r.BeforeExists, &r.AfterExists,
			&beforeMode, &afterMode, &r.Agent, &origin, &track, &at); err != nil {
			return nil, fmt.Errorf("load changes: %w", err)
		}
		r.Before, r.After = before.String, after.String
		r.BeforeMode, r.AfterMode = os.FileMode(beforeMode), os.FileMode(afterMode)
		r.Origin, r.Track = changeset.Origin(origin), changeset.Tracking(track)
		r.At, _ = time.Parse(time.RFC3339Nano, at)
		if n := len(out); n > 0 && out[n-1].Turn == turn {
			out[n-1].Records = append(out[n-1].Records, r)
			continue
		}
		out = append(out, changeset.TurnRecords{Turn: turn, Records: []changeset.Record{r}})
	}
	return out, rows.Err()
}

// LastChangeTurn is the highest turn number a slot holds records for, or zero
// for a slot that holds none. A resumed conversation numbers its next turn
// past it, so the number on a close row addresses the same turn in the sitting
// that wrote it and in every one after.
func (db *DB) LastChangeTurn(slot string) (int64, error) {
	var last int64
	err := db.sql.QueryRow(
		`SELECT COALESCE(MAX(c.turn), 0) FROM changes c
		   JOIN chat_sessions s ON s.id = c.session_id WHERE s.name = ?`, slot).Scan(&last)
	if err != nil {
		return 0, fmt.Errorf("last change turn: %w", err)
	}
	return last, nil
}

// PruneOldChanges deletes recorded changes past the window and reports how
// many rows went, then collects the blobs nothing names any more. The window
// is the saved conversations' own, because a record of what a turn changed is
// part of that conversation and outliving it would leave the store holding
// edits nobody can find the turn for; a conversation deleted or pruned takes
// its records with it through the foreign key without coming here at all.
// See docs/capabilities/sessions-and-memory.md#a-conversation-is-kept-for-a-window.
//
// Zero days is off, the way it is for the conversations themselves, and it
// leaves the table alone.
func (db *DB) PruneOldChanges(retentionDays int) (int64, error) {
	if retentionDays <= 0 {
		return 0, nil
	}
	res, err := db.sql.Exec(`DELETE FROM changes WHERE at < ?`,
		retentionCutoff(time.Now(), retentionDays))
	if err != nil {
		return 0, fmt.Errorf("prune changes: %w", err)
	}
	n, _ := res.RowsAffected()
	if err := db.collectChangeBlobs(); err != nil {
		return n, err
	}
	return n, nil
}

// collectChangeBlobs drops the content nothing points at. It runs after any
// delete rather than on a schedule: a blob is shared by every record holding
// the same bytes, so the only moment one can be known to be unreachable is
// after the rows that might have named it have gone.
func (db *DB) collectChangeBlobs() error {
	_, err := db.sql.Exec(
		`DELETE FROM change_blobs WHERE hash NOT IN (
		     SELECT before_hash FROM changes UNION SELECT after_hash FROM changes)`)
	if err != nil {
		return fmt.Errorf("collect change blobs: %w", err)
	}
	return nil
}

// chatSessionID resolves a slot name to its row id.
func (db *DB) chatSessionID(slot string) (int64, bool, error) {
	var id int64
	err := db.sql.QueryRow(`SELECT id FROM chat_sessions WHERE name = ?`, slot).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("chat session id: %w", err)
	}
	return id, true, nil
}

// putBlob stores content under its digest and answers with the digest. The
// insert ignores a collision because a collision is the point: two records
// holding the same bytes are one blob, which is what keeps a file edited all
// afternoon from costing its whole size on every turn.
func putBlob(tx *sql.Tx, content string) (string, error) {
	sum := sha256.Sum256([]byte(content))
	hash := hex.EncodeToString(sum[:])
	if _, err := tx.Exec(
		`INSERT OR IGNORE INTO change_blobs (hash, content) VALUES (?, ?)`, hash, content); err != nil {
		return "", fmt.Errorf("save change content: %w", err)
	}
	return hash, nil
}
