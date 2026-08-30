package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rfizzle/shhh/internal/provider"
)

type ChatSession struct {
	ID        int64
	Name      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ChatListEntry is one saved session as a listing shows it. Title is the
// generated one, empty until a reading produced it; the name is always the
// slot's own and is what every command addresses the session by.
type ChatListEntry struct {
	Name      string
	Title     string
	UpdatedAt time.Time
	Turns     int
}

// ChatExistsError is what a rename into a name already in use returns. It is
// its own type so a caller can tell a collision — which the person can fix by
// choosing another name — from a store that failed.
type ChatExistsError struct{ Name string }

func (e ChatExistsError) Error() string { return fmt.Sprintf("a chat named %q already exists", e.Name) }

// ChatNotFoundError is the error for a name no saved session carries.
type ChatNotFoundError struct{ Name string }

func (e ChatNotFoundError) Error() string { return fmt.Sprintf("chat %q not found", e.Name) }

func (db *DB) SaveChat(name string, messages []provider.Message) error {
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := saveChatTx(tx, name, messages); err != nil {
		return err
	}
	return tx.Commit()
}

// SaveChatBranch stores messages as a branch session of parentName: the
// branch gets its own session row with parent_id pointing at the parent
// (created as an empty session if it doesn't exist yet, so a never-saved live
// session can still grow branches).
func (db *DB) SaveChatBranch(parentName, branchName string, messages []provider.Message) error {
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.Exec(
		`INSERT OR IGNORE INTO chat_sessions (name, created_at, updated_at) VALUES (?, ?, ?)`,
		parentName, now, now,
	); err != nil {
		return fmt.Errorf("ensure parent session: %w", err)
	}

	branchID, err := saveChatTx(tx, branchName, messages)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(
		`UPDATE chat_sessions SET parent_id = (SELECT id FROM chat_sessions WHERE name = ?) WHERE id = ?`,
		parentName, branchID,
	); err != nil {
		return fmt.Errorf("link branch to parent: %w", err)
	}
	return tx.Commit()
}

// saveChatTx upserts one session's messages inside tx, preserving any
// existing parent link, and returns the session id.
func saveChatTx(tx *sql.Tx, name string, messages []provider.Message) (int64, error) {
	var sessionID int64
	now := time.Now().UTC().Format(time.RFC3339Nano)

	err := tx.QueryRow(`SELECT id FROM chat_sessions WHERE name = ?`, name).Scan(&sessionID)
	if err == sql.ErrNoRows {
		res, err := tx.Exec(
			`INSERT INTO chat_sessions (name, created_at, updated_at) VALUES (?, ?, ?)`,
			name, now, now,
		)
		if err != nil {
			return 0, fmt.Errorf("insert session: %w", err)
		}
		sessionID, _ = res.LastInsertId()
	} else if err != nil {
		return 0, fmt.Errorf("lookup session: %w", err)
	} else {
		if _, err := tx.Exec(`UPDATE chat_sessions SET updated_at = ? WHERE id = ?`, now, sessionID); err != nil {
			return 0, fmt.Errorf("update session: %w", err)
		}
		if _, err := tx.Exec(`DELETE FROM chat_messages WHERE session_id = ?`, sessionID); err != nil {
			return 0, fmt.Errorf("clear messages: %w", err)
		}
	}

	for i, msg := range messages {
		var toolCallsJSON *string
		if len(msg.ToolCalls) > 0 {
			b, err := json.Marshal(msg.ToolCalls)
			if err != nil {
				return 0, fmt.Errorf("marshal tool calls: %w", err)
			}
			s := string(b)
			toolCallsJSON = &s
		}
		// Attachment bytes are saved with the turn that carried them
		//, so resuming a session keeps the screenshot the question
		// was about rather than a sentence pointing at nothing.
		var attachmentsJSON *string
		if len(msg.Attachments) > 0 {
			b, err := json.Marshal(msg.Attachments)
			if err != nil {
				return 0, fmt.Errorf("marshal attachments: %w", err)
			}
			s := string(b)
			attachmentsJSON = &s
		}
		_, err := tx.Exec(
			`INSERT INTO chat_messages (session_id, seq, role, content, tool_calls, tool_call_id, attachments)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			sessionID, i, string(msg.Role), msg.Content, toolCallsJSON, msg.ToolCallID, attachmentsJSON,
		)
		if err != nil {
			return 0, fmt.Errorf("insert message %d: %w", i, err)
		}
	}

	return sessionID, nil
}

func (db *DB) LoadChat(name string) ([]provider.Message, error) {
	var sessionID int64
	err := db.sql.QueryRow(`SELECT id FROM chat_sessions WHERE name = ?`, name).Scan(&sessionID)
	if err == sql.ErrNoRows {
		return nil, ChatNotFoundError{Name: name}
	}
	if err != nil {
		return nil, err
	}

	rows, err := db.sql.Query(
		`SELECT role, content, tool_calls, tool_call_id, attachments
		 FROM chat_messages WHERE session_id = ? ORDER BY seq`, sessionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []provider.Message
	for rows.Next() {
		var (
			role, content, toolCallID      string
			toolCallsJSON, attachmentsJSON *string
		)
		if err := rows.Scan(&role, &content, &toolCallsJSON, &toolCallID, &attachmentsJSON); err != nil {
			return nil, err
		}
		msg := provider.Message{
			Role:       provider.Role(role),
			Content:    content,
			ToolCallID: toolCallID,
		}
		if toolCallsJSON != nil {
			if err := json.Unmarshal([]byte(*toolCallsJSON), &msg.ToolCalls); err != nil {
				return nil, fmt.Errorf("unmarshal tool calls: %w", err)
			}
		}
		if attachmentsJSON != nil {
			if err := json.Unmarshal([]byte(*attachmentsJSON), &msg.Attachments); err != nil {
				return nil, fmt.Errorf("unmarshal attachments: %w", err)
			}
		}
		messages = append(messages, msg)
	}
	return messages, rows.Err()
}

func (db *DB) ListChats() ([]ChatListEntry, error) {
	rows, err := db.sql.Query(
		`SELECT s.name, s.title, s.updated_at,
		        COUNT(CASE WHEN m.role = 'user' THEN 1 END) as turns
		 FROM chat_sessions s
		 LEFT JOIN chat_messages m ON m.session_id = s.id
		 GROUP BY s.id
		 ORDER BY s.updated_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []ChatListEntry
	for rows.Next() {
		var (
			e         ChatListEntry
			updatedAt string
		)
		if err := rows.Scan(&e.Name, &e.Title, &updatedAt, &e.Turns); err != nil {
			return nil, err
		}
		e.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// ChatBranch is one session in a branch family: the root plus every branch
// hanging off it. Parent is empty for the root.
type ChatBranch struct {
	Name      string
	Parent    string
	UpdatedAt time.Time
	Turns     int
}

// RecentChat is the most recently saved session, for the start screen's
// resume suggestion: what it was called, how many turns it holds, and
// what it cost. Cost is only present when an observability record covers the
// moment the session was saved — the two tables are joined by that window
// rather than by name, because a chat session row has never carried a price
// and inventing one is worse than leaving the clause off.
type RecentChat struct {
	Name      string
	Title     string
	UpdatedAt time.Time
	Turns     int
	Cost      float64
	Priced    bool
}

// MostRecentChat returns the newest saved session, or ok=false when nothing
// has been saved yet. A missing observability record costs the price clause,
// never the suggestion.
func (db *DB) MostRecentChat() (RecentChat, bool, error) {
	entries, err := db.ListChats()
	if err != nil {
		return RecentChat{}, false, err
	}
	if len(entries) == 0 {
		return RecentChat{}, false, nil
	}
	e := entries[0]
	out := RecentChat{Name: e.Name, Title: e.Title, UpdatedAt: e.UpdatedAt, Turns: e.Turns}

	// The session that was running when the chat was last written is the one
	// whose lifetime contains that write: started before it, and either still
	// open or ended after it. Children are excluded — a sub-agent's spend is
	// part of its parent's, not a session of its own to resume. Both columns
	// are written in the same UTC layout, so the comparison is exact.
	at := e.UpdatedAt.UTC().Format(observeTimeFormat)
	row := db.sql.QueryRow(
		`SELECT est_cost FROM agent_sessions
		 WHERE parent_id IS NULL AND started_at <= ?
		   AND (ended_at IS NULL OR ended_at >= ?)
		 ORDER BY started_at DESC LIMIT 1`, at, at)
	var cost float64
	if err := row.Scan(&cost); err == nil {
		out.Cost, out.Priced = cost, true
	}
	return out, true, nil
}

// ListChatBranches returns the branch family of name — the root reached by
// walking name's parent chain, plus every descendant — ordered oldest-first.
// An unknown name yields an empty list, not an error.
func (db *DB) ListChatBranches(name string) ([]ChatBranch, error) {
	rows, err := db.sql.Query(
		`WITH RECURSIVE up(id, parent_id) AS (
		     SELECT id, parent_id FROM chat_sessions WHERE name = ?
		     UNION ALL
		     SELECT s.id, s.parent_id FROM chat_sessions s JOIN up ON s.id = up.parent_id
		 ),
		 family(id) AS (
		     SELECT id FROM up WHERE parent_id IS NULL
		     UNION ALL
		     SELECT s.id FROM chat_sessions s JOIN family f ON s.parent_id = f.id
		 )
		 SELECT s.name, COALESCE(p.name, ''), s.updated_at,
		        COUNT(CASE WHEN m.role = 'user' THEN 1 END) AS turns
		 FROM chat_sessions s
		 JOIN family ON family.id = s.id
		 LEFT JOIN chat_sessions p ON p.id = s.parent_id
		 LEFT JOIN chat_messages m ON m.session_id = s.id
		 GROUP BY s.id
		 ORDER BY s.created_at, s.id`, name,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var branches []ChatBranch
	for rows.Next() {
		var (
			b         ChatBranch
			updatedAt string
		)
		if err := rows.Scan(&b.Name, &b.Parent, &updatedAt, &b.Turns); err != nil {
			return nil, err
		}
		b.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
		branches = append(branches, b)
	}
	return branches, rows.Err()
}

// DeleteChat removes a session and every branch hanging off it. The
// branches go with it because a branch is a tail of the conversation it
// forked from: left behind, it would be a session nobody named, holding a
// copy of messages that were just deleted. The confirm that precedes this
// names the count (CountChatBranches), so nothing is removed unannounced
// (docs/interface/surfaces.md#the-inline-confirm).
func (db *DB) DeleteChat(name string) error {
	res, err := db.sql.Exec(
		`WITH RECURSIVE family(id) AS (
		     SELECT id FROM chat_sessions WHERE name = ?
		     UNION ALL
		     SELECT s.id FROM chat_sessions s JOIN family f ON s.parent_id = f.id
		 )
		 DELETE FROM chat_sessions WHERE id IN (SELECT id FROM family)`, name)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ChatNotFoundError{Name: name}
	}
	return nil
}

// CountChatBranches is how many sessions hang off name — its descendants,
// not the family it belongs to — which is what deleting it would take with
// it. An unknown name counts zero.
func (db *DB) CountChatBranches(name string) (int, error) {
	var n int
	err := db.sql.QueryRow(
		`WITH RECURSIVE below(id) AS (
		     SELECT id FROM chat_sessions WHERE name = ?
		     UNION ALL
		     SELECT s.id FROM chat_sessions s JOIN below b ON s.parent_id = b.id
		 )
		 SELECT COUNT(*) - 1 FROM below`, name).Scan(&n)
	if err != nil {
		return 0, err
	}
	return max(n, 0), nil
}

// RenameChat gives a saved session a new name. Branches are linked by id,
// so a renamed root keeps every branch and a renamed branch keeps its
// parent. A name already in use is refused with ChatExistsError rather than
// merged: two conversations under one name is what SaveChat's overwrite
// would make of it, and a rename that silently discards a session is not a
// rename (docs/capabilities/sessions-and-memory.md#housekeeping).
func (db *DB) RenameChat(oldName, newName string) error {
	if oldName == newName {
		return nil
	}
	var taken int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM chat_sessions WHERE name = ?`, newName).Scan(&taken); err != nil {
		return err
	}
	if taken > 0 {
		return ChatExistsError{Name: newName}
	}
	res, err := db.sql.Exec(`UPDATE chat_sessions SET name = ? WHERE name = ?`, newName, oldName)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ChatNotFoundError{Name: oldName}
	}
	return nil
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
