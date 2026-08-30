package storage

import (
	"time"

	"github.com/rfizzle/shhh/internal/notebook"
)

// The notebook's persistence: notes keyed by the chat session slot they
// belong to, so a resumed conversation resumes its notebook. The store owns
// the bounds and the in-memory copy; this layer only persists.

// SaveNote inserts one note under a session and returns its id.
func (db *DB) SaveNote(session string, n notebook.Note) (int64, error) {
	res, err := db.sql.Exec(
		`INSERT INTO notes (session, author, title, body, written_at) VALUES (?, ?, ?, ?, ?)`,
		session, n.Author, n.Title, n.Body, n.Written.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// LoadNotes returns a session's notes, oldest first.
func (db *DB) LoadNotes(session string) ([]notebook.Note, error) {
	rows, err := db.sql.Query(
		`SELECT id, author, title, body, written_at FROM notes WHERE session = ? ORDER BY id`, session)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var notes []notebook.Note
	for rows.Next() {
		var (
			n       notebook.Note
			written string
		)
		if err := rows.Scan(&n.ID, &n.Author, &n.Title, &n.Body, &written); err != nil {
			return nil, err
		}
		n.Written, _ = time.Parse(time.RFC3339Nano, written)
		notes = append(notes, n)
	}
	return notes, rows.Err()
}

// DeleteNote removes one note.
func (db *DB) DeleteNote(session string, id int64) error {
	_, err := db.sql.Exec(`DELETE FROM notes WHERE session = ? AND id = ?`, session, id)
	return err
}
