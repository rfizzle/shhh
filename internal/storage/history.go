// History is what you asked and what happened: every generated command with
// the prompt behind it, what became of it, and what it cost. Nothing here
// re-runs anything — the browser that reads these rows requires a deliberate
// key
// (docs/capabilities/sessions-and-memory.md#history-is-what-you-asked-and-what-happened).
package storage

import (
	"fmt"
	"time"
)

type HistoryEntry struct {
	ID        int64
	CreatedAt time.Time
	Provider  string
	Model     string
	Prompt    string
	Command   string
	Action    string
	// Duration is how long the generation took, and ExitCode what the command
	// exited with when it was run. Both are nil for an entry recorded before the
	// columns existed, which the browser reads as "not known" rather than as
	// zero (docs/interface/surfaces.md#the-supporting-screens).
	Duration  *time.Duration
	ExitCode  *int64
	TokensIn  *int64
	TokensOut *int64
	// Success is whether the request itself completed — a stream that broke
	// is a different thing from a command that exited non-zero.
	Success bool
}

type HistoryFilter struct {
	Search string
	Limit  int
}

// ListHistory is the newest entries, filtered by the words in Search.
//
// The filter is put to the full-text index rather than to every row: a
// wildcarded LIKE reads the whole table, so the search a person runs to find
// the command they typed last month got slower every month they kept using
// the tool (docs/capabilities/sessions-and-memory.md#finding-a-conversation-again).
// What that costs is the match in the middle of a word — the index knows
// words, and matchQuery asks for each one as a prefix.
func (db *DB) ListHistory(f HistoryFilter) ([]HistoryEntry, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}

	var rows_result []HistoryEntry
	var err error

	if f.Search != "" {
		match, ok := matchQuery(f.Search)
		if !ok {
			return nil, nil
		}
		query := `SELECT r.id, r.created_at, r.provider, r.model, r.prompt, r.command, r.action,
		                 r.duration_ms, r.exit_code, r.tokens_in, r.tokens_out, r.success
		          FROM requests r JOIN request_search ON request_search.rowid = r.id
		          WHERE request_search MATCH ?
		          ORDER BY r.created_at DESC LIMIT ?`
		rows, qErr := db.sql.Query(query, match, limit)
		if qErr != nil {
			return nil, qErr
		}
		defer rows.Close()
		rows_result, err = scanHistory(rows)
	} else {
		query := `SELECT id, created_at, provider, model, prompt, command, action,
		                 duration_ms, exit_code, tokens_in, tokens_out, success
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

func (db *DB) PurgeOldHistory(retentionDays int) (int64, error) {
	res, err := db.sql.Exec(`DELETE FROM requests WHERE created_at < ?`,
		retentionCutoff(time.Now(), retentionDays))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (db *DB) ClearAllHistory() (int64, error) {
	res, err := db.sql.Exec(`DELETE FROM requests`)
	if err != nil {
		return 0, fmt.Errorf("clear history: %w", err)
	}
	return res.RowsAffected()
}

func scanHistory(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]HistoryEntry, error) {
	var entries []HistoryEntry
	for rows.Next() {
		var (
			e          HistoryEntry
			createdAt  string
			durationMs *int64
			success    int64
		)
		if err := rows.Scan(&e.ID, &createdAt, &e.Provider, &e.Model, &e.Prompt, &e.Command,
			&e.Action, &durationMs, &e.ExitCode, &e.TokensIn, &e.TokensOut, &success); err != nil {
			return nil, err
		}
		e.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		if durationMs != nil {
			d := time.Duration(*durationMs) * time.Millisecond
			e.Duration = &d
		}
		e.Success = success != 0
		entries = append(entries, e)
	}
	return entries, rows.Err()
}
