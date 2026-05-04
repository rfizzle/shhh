package storage

import (
	"database/sql"
	"fmt"
	"time"
)

type Snippet struct {
	ID        int64
	Name      string
	Command   string
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (db *DB) SaveSnippet(name, command string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)

	var id int64
	err := db.sql.QueryRow(`SELECT id FROM snippets WHERE name = ?`, name).Scan(&id)
	if err == sql.ErrNoRows {
		_, err = db.sql.Exec(
			`INSERT INTO snippets (name, command, created_at, updated_at) VALUES (?, ?, ?, ?)`,
			name, command, now, now,
		)
		return err
	}
	if err != nil {
		return err
	}
	_, err = db.sql.Exec(`UPDATE snippets SET command = ?, updated_at = ? WHERE id = ?`, command, now, id)
	return err
}

func (db *DB) ListSnippets() ([]Snippet, error) {
	rows, err := db.sql.Query(
		`SELECT id, name, command, created_at, updated_at FROM snippets ORDER BY updated_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var snippets []Snippet
	for rows.Next() {
		var (
			s                    Snippet
			createdAt, updatedAt string
		)
		if err := rows.Scan(&s.ID, &s.Name, &s.Command, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		s.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		s.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
		snippets = append(snippets, s)
	}
	return snippets, rows.Err()
}

func (db *DB) GetSnippet(name string) (Snippet, error) {
	var (
		s                    Snippet
		createdAt, updatedAt string
	)
	err := db.sql.QueryRow(
		`SELECT id, name, command, created_at, updated_at FROM snippets WHERE name = ?`, name,
	).Scan(&s.ID, &s.Name, &s.Command, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return Snippet{}, fmt.Errorf("snippet %q not found", name)
	}
	if err != nil {
		return Snippet{}, err
	}
	s.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	s.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	return s, nil
}

func (db *DB) DeleteSnippet(name string) error {
	res, err := db.sql.Exec(`DELETE FROM snippets WHERE name = ?`, name)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("snippet %q not found", name)
	}
	return nil
}
