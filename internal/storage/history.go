package storage

import "time"

type HistoryEntry struct {
	ID        int64
	CreatedAt time.Time
	Provider  string
	Model     string
	Prompt    string
	Command   string
	Action    string
}

type HistoryFilter struct {
	Search string
	Limit  int
}

func (db *DB) ListHistory(f HistoryFilter) ([]HistoryEntry, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}

	var rows_result []HistoryEntry
	var err error

	if f.Search != "" {
		query := `SELECT id, created_at, provider, model, prompt, command, action
		          FROM requests
		          WHERE prompt LIKE ? OR command LIKE ?
		          ORDER BY created_at DESC LIMIT ?`
		pattern := "%" + f.Search + "%"
		rows, qErr := db.sql.Query(query, pattern, pattern, limit)
		if qErr != nil {
			return nil, qErr
		}
		defer rows.Close()
		rows_result, err = scanHistory(rows)
	} else {
		query := `SELECT id, created_at, provider, model, prompt, command, action
		          FROM requests
		          ORDER BY created_at DESC LIMIT ?`
		rows, qErr := db.sql.Query(query, limit)
		if qErr != nil {
			return nil, qErr
		}
		defer rows.Close()
		rows_result, err = scanHistory(rows)
	}
	return rows_result, err
}

func (db *DB) DeleteHistoryEntry(id int64) error {
	_, err := db.sql.Exec(`DELETE FROM requests WHERE id = ?`, id)
	return err
}

func scanHistory(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]HistoryEntry, error) {
	var entries []HistoryEntry
	for rows.Next() {
		var (
			e         HistoryEntry
			createdAt string
		)
		if err := rows.Scan(&e.ID, &createdAt, &e.Provider, &e.Model, &e.Prompt, &e.Command, &e.Action); err != nil {
			return nil, err
		}
		e.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		entries = append(entries, e)
	}
	return entries, rows.Err()
}
