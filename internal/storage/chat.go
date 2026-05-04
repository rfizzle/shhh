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

type ChatListEntry struct {
	Name      string
	UpdatedAt time.Time
	Turns     int
}

func (db *DB) SaveChat(name string, messages []provider.Message) error {
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var sessionID int64
	now := time.Now().UTC().Format(time.RFC3339Nano)

	err = tx.QueryRow(`SELECT id FROM chat_sessions WHERE name = ?`, name).Scan(&sessionID)
	if err == sql.ErrNoRows {
		res, err := tx.Exec(
			`INSERT INTO chat_sessions (name, created_at, updated_at) VALUES (?, ?, ?)`,
			name, now, now,
		)
		if err != nil {
			return fmt.Errorf("insert session: %w", err)
		}
		sessionID, _ = res.LastInsertId()
	} else if err != nil {
		return fmt.Errorf("lookup session: %w", err)
	} else {
		if _, err := tx.Exec(`UPDATE chat_sessions SET updated_at = ? WHERE id = ?`, now, sessionID); err != nil {
			return fmt.Errorf("update session: %w", err)
		}
		if _, err := tx.Exec(`DELETE FROM chat_messages WHERE session_id = ?`, sessionID); err != nil {
			return fmt.Errorf("clear messages: %w", err)
		}
	}

	for i, msg := range messages {
		var toolCallsJSON *string
		if len(msg.ToolCalls) > 0 {
			b, err := json.Marshal(msg.ToolCalls)
			if err != nil {
				return fmt.Errorf("marshal tool calls: %w", err)
			}
			s := string(b)
			toolCallsJSON = &s
		}
		_, err := tx.Exec(
			`INSERT INTO chat_messages (session_id, seq, role, content, tool_calls, tool_call_id)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			sessionID, i, string(msg.Role), msg.Content, toolCallsJSON, msg.ToolCallID,
		)
		if err != nil {
			return fmt.Errorf("insert message %d: %w", i, err)
		}
	}

	return tx.Commit()
}

func (db *DB) LoadChat(name string) ([]provider.Message, error) {
	var sessionID int64
	err := db.sql.QueryRow(`SELECT id FROM chat_sessions WHERE name = ?`, name).Scan(&sessionID)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("chat %q not found", name)
	}
	if err != nil {
		return nil, err
	}

	rows, err := db.sql.Query(
		`SELECT role, content, tool_calls, tool_call_id
		 FROM chat_messages WHERE session_id = ? ORDER BY seq`, sessionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []provider.Message
	for rows.Next() {
		var (
			role, content, toolCallID string
			toolCallsJSON             *string
		)
		if err := rows.Scan(&role, &content, &toolCallsJSON, &toolCallID); err != nil {
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
		messages = append(messages, msg)
	}
	return messages, rows.Err()
}

func (db *DB) ListChats() ([]ChatListEntry, error) {
	rows, err := db.sql.Query(
		`SELECT s.name, s.updated_at,
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
		if err := rows.Scan(&e.Name, &updatedAt, &e.Turns); err != nil {
			return nil, err
		}
		e.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

func (db *DB) DeleteChat(name string) error {
	res, err := db.sql.Exec(`DELETE FROM chat_sessions WHERE name = ?`, name)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("chat %q not found", name)
	}
	return nil
}
