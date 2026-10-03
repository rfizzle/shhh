package storage

import (
	"database/sql"
	"encoding/json"
)

// ChatHold is the mark a conversation saved mid-turn carries beside itself:
// the turn was parked at a round boundary rather than finished, and this is
// where it had got to. Both counts belong to the turn and not to the process
// that wrote them — a new session's own round counter starts at nothing, and
// the grant has to come back with the turn or the round it resumes into stops
// again at a ceiling the person had already lifted.
// See docs/capabilities/sessions-and-memory.md#a-held-turn-comes-back-held.
type ChatHold struct {
	Rounds  int `json:"rounds"`
	Granted int `json:"granted"`
}

// setChatHoldTx writes the mark inside tx, or clears it when h is nil. Every
// autosave carries an answer — nil included — so a slot never goes on claiming
// a hold the turn has already been let go of.
func setChatHoldTx(tx *sql.Tx, name string, h *ChatHold) error {
	var held any
	if h != nil {
		b, err := json.Marshal(h)
		if err != nil {
			return err
		}
		held = string(b)
	}
	res, err := tx.Exec(`UPDATE chat_sessions SET held = ? WHERE name = ?`, held, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ChatNotFoundError{Name: name}
	}
	return nil
}

// ChatHold reads the mark and whether there is one. A slot nothing ever held
// — including every slot written before the column existed — reports false,
// which is the answer that opens it the way it always opened.
func (db *DB) ChatHold(name string) (ChatHold, bool, error) {
	var held sql.NullString
	err := db.sql.QueryRow(`SELECT held FROM chat_sessions WHERE name = ?`, name).Scan(&held)
	if err == sql.ErrNoRows {
		return ChatHold{}, false, nil
	}
	if err != nil || !held.Valid || held.String == "" {
		return ChatHold{}, false, err
	}
	var h ChatHold
	if err := json.Unmarshal([]byte(held.String), &h); err != nil {
		// A mark nobody can read is a mark nobody can act on. Opening the
		// conversation idle is the honest answer and costs one keystroke;
		// refusing to open it at all would cost the whole conversation.
		return ChatHold{}, false, nil
	}
	return h, true, nil
}

// SetChatTitle stores the generated title on a session. It is the one write
// a reading makes, and it never touches the name: the name is the user's,
// the title is the model's, and only the listing puts them side by side.
func (db *DB) SetChatTitle(name, title string) error {
	res, err := db.sql.Exec(`UPDATE chat_sessions SET title = ? WHERE name = ?`, title, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ChatNotFoundError{Name: name}
	}
	return nil
}

// HasChat reports whether a session by that name is saved.
func (db *DB) HasChat(name string) (bool, error) {
	var n int
	err := db.sql.QueryRow(`SELECT COUNT(*) FROM chat_sessions WHERE name = ?`, name).Scan(&n)
	return n > 0, err
}

// ChatTitle reads a session's generated title; empty when none was written
// or the session is unknown.
func (db *DB) ChatTitle(name string) (string, error) {
	var title string
	err := db.sql.QueryRow(`SELECT title FROM chat_sessions WHERE name = ?`, name).Scan(&title)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return title, err
}

// ChatResume is what a slot says about the sitting that left it, beside the
// conversation itself: the summary the last compaction wrote, and the commit
// the checkout was on when the slot was last written.
//
// Both are for the conversation's next opening rather than for this one.
// Summary is what a compaction already produced — nothing is summarized to
// fill it — and Head is what makes the difference between the tree the
// transcript describes and the tree in front of the reader something the
// session can state instead of something it has to be told.
// See docs/capabilities/sessions-and-memory.md#a-resumed-session-sees-the-tree-as-it-is.
type ChatResume struct {
	Summary string
	Head    string
	// Root is the checkout the conversation was written down in. It is not
	// read on the way back in; it is what `shhh sessions` names a running
	// session's directory by, since the record beside it stores no paths
	// (docs/capabilities/sessions-and-memory.md#a-session-knows-it-is-not-alone).
	Root string
	// Steps is the session's own working checklist as plan.Checklist encodes
	// it, and empty for a session that declared none. The store keeps the
	// text and never reads it.
	// See docs/capabilities/coding-agent.md#the-session-keeps-its-own-working-steps.
	Steps string
}

// SetChatResume stores what the slot is opened again on. It is the title's
// shape and rides beside it on the same save: one write, both halves, so a
// slot can never carry a summary from one sitting and a commit from another.
func (db *DB) SetChatResume(name string, r ChatResume) error {
	res, err := db.sql.Exec(
		`UPDATE chat_sessions SET summary = ?, head = ?, root = ?, steps = ? WHERE name = ?`, r.Summary, r.Head, r.Root, r.Steps, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ChatNotFoundError{Name: name}
	}
	return nil
}

// ChatResume reads it back. A slot that never wrote one — every slot written
// before the columns existed included — answers with both halves empty, which
// is the answer that opens the conversation the way it always opened.
func (db *DB) ChatResume(name string) (ChatResume, error) {
	var r ChatResume
	err := db.sql.QueryRow(
		`SELECT summary, head, root, steps FROM chat_sessions WHERE name = ?`, name).Scan(&r.Summary, &r.Head, &r.Root, &r.Steps)
	if err == sql.ErrNoRows {
		return ChatResume{}, nil
	}
	return r, err
}
