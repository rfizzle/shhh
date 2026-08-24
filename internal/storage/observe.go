package storage

// Session observability (S-065): agent sessions record content-free events —
// tokens, cost, model, tool-call counts/durations/outcomes, mode decisions
// with enum-like reason codes, turn counts. Never prompts, outputs, paths, or
// commands: every stored string is either a fixed identifier (provider, model,
// tool name) or a code from a closed set, so the content-free guarantee holds
// structurally.

import (
	"fmt"
	"time"
)

const observeTimeFormat = "2006-01-02T15:04:05.000Z"

// Agent event kinds.
const (
	AgentEventTool     = "tool"
	AgentEventDecision = "decision"
)

// StartAgentSession opens a session row and returns its id. kind is the entry
// point ("chat", "code", "print").
func (db *DB) StartAgentSession(kind, provider, model string) (int64, error) {
	res, err := db.sql.Exec(
		`INSERT INTO agent_sessions (kind, provider, model) VALUES (?, ?, ?)`,
		kind, provider, model,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// StartChildAgentSession opens a session row linked to a parent session, so
// sub-agent spend is attributable (S-068). A non-positive parentID records an
// unlinked session.
func (db *DB) StartChildAgentSession(parentID int64, kind, provider, model string) (int64, error) {
	if parentID <= 0 {
		return db.StartAgentSession(kind, provider, model)
	}
	res, err := db.sql.Exec(
		`INSERT INTO agent_sessions (kind, provider, model, parent_id) VALUES (?, ?, ?, ?)`,
		kind, provider, model, parentID,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateAgentSession sets a session's cumulative totals (idempotent: callers
// pass running totals, not deltas).
func (db *DB) UpdateAgentSession(id, turns, tokensIn, tokensOut int64, estCost float64) error {
	_, err := db.sql.Exec(
		`UPDATE agent_sessions SET turns = ?, tokens_in = ?, tokens_out = ?, est_cost = ? WHERE id = ?`,
		turns, tokensIn, tokensOut, estCost, id,
	)
	return err
}

// EndAgentSession stamps the session's end time.
func (db *DB) EndAgentSession(id int64) error {
	_, err := db.sql.Exec(
		`UPDATE agent_sessions SET ended_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id = ?`, id,
	)
	return err
}

// RecordAgentEvent appends one content-free event to a session. For tool
// events, tool is the tool name, outcome "ok"/"error", and durationMs the
// call's duration when known. For decision events, outcome is
// allow/deny/ask and reason an enum-like code.
func (db *DB) RecordAgentEvent(sessionID int64, kind, tool string, durationMs *int64, outcome, reason string) error {
	_, err := db.sql.Exec(
		`INSERT INTO agent_events (session_id, kind, tool, duration_ms, outcome, reason)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		sessionID, kind, tool, durationMs, outcome, reason,
	)
	return err
}

type AgentDayUsage struct {
	Day       string
	Sessions  int
	TokensIn  int64
	TokensOut int64
	Cost      float64
}

// AgentUsageByDay aggregates session usage per calendar day since the cutoff,
// newest day first.
func (db *DB) AgentUsageByDay(since time.Time) ([]AgentDayUsage, error) {
	rows, err := db.sql.Query(
		`SELECT substr(started_at, 1, 10) AS day, COUNT(*),
		        COALESCE(SUM(tokens_in), 0), COALESCE(SUM(tokens_out), 0), COALESCE(SUM(est_cost), 0)
		 FROM agent_sessions WHERE started_at >= ?
		 GROUP BY day ORDER BY day DESC`, observeCutoff(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AgentDayUsage
	for rows.Next() {
		var u AgentDayUsage
		if err := rows.Scan(&u.Day, &u.Sessions, &u.TokensIn, &u.TokensOut, &u.Cost); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

type AgentModelUsage struct {
	Provider  string
	Model     string
	Sessions  int
	TokensIn  int64
	TokensOut int64
	Cost      float64
}

// AgentUsageByModel aggregates session usage per provider/model since the
// cutoff, most-used first.
func (db *DB) AgentUsageByModel(since time.Time) ([]AgentModelUsage, error) {
	rows, err := db.sql.Query(
		`SELECT provider, model, COUNT(*),
		        COALESCE(SUM(tokens_in), 0), COALESCE(SUM(tokens_out), 0), COALESCE(SUM(est_cost), 0)
		 FROM agent_sessions WHERE started_at >= ?
		 GROUP BY provider, model ORDER BY COUNT(*) DESC`, observeCutoff(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AgentModelUsage
	for rows.Next() {
		var u AgentModelUsage
		if err := rows.Scan(&u.Provider, &u.Model, &u.Sessions, &u.TokensIn, &u.TokensOut, &u.Cost); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

type AgentToolUsage struct {
	Tool          string
	Count         int
	AvgDurationMs *float64
	ErrorRate     float64
}

// AgentToolMix aggregates tool events by tool name since the cutoff,
// most-called first.
func (db *DB) AgentToolMix(since time.Time) ([]AgentToolUsage, error) {
	rows, err := db.sql.Query(
		`SELECT tool, COUNT(*), AVG(duration_ms),
		        AVG(CASE WHEN outcome = 'error' THEN 1.0 ELSE 0.0 END)
		 FROM agent_events WHERE kind = ? AND created_at >= ?
		 GROUP BY tool ORDER BY COUNT(*) DESC`, AgentEventTool, observeCutoff(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AgentToolUsage
	for rows.Next() {
		var u AgentToolUsage
		if err := rows.Scan(&u.Tool, &u.Count, &u.AvgDurationMs, &u.ErrorRate); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

type AgentDecisionCount struct {
	Decision string
	Reason   string
	Count    int
}

// AgentDecisions aggregates mode-decision events (allow/deny/ask + reason
// code) since the cutoff, most-frequent first.
func (db *DB) AgentDecisions(since time.Time) ([]AgentDecisionCount, error) {
	rows, err := db.sql.Query(
		`SELECT outcome, reason, COUNT(*)
		 FROM agent_events WHERE kind = ? AND created_at >= ?
		 GROUP BY outcome, reason ORDER BY COUNT(*) DESC`, AgentEventDecision, observeCutoff(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AgentDecisionCount
	for rows.Next() {
		var d AgentDecisionCount
		if err := rows.Scan(&d.Decision, &d.Reason, &d.Count); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

type AgentSessionSummary struct {
	ID        int64
	StartedAt time.Time
	EndedAt   *time.Time
	Kind      string
	Provider  string
	Model     string
	Turns     int64
	TokensIn  int64
	TokensOut int64
	Cost      float64
}

// AgentSessions lists sessions since the cutoff, newest first.
func (db *DB) AgentSessions(since time.Time, limit int) ([]AgentSessionSummary, error) {
	rows, err := db.sql.Query(
		`SELECT id, started_at, ended_at, kind, provider, model, turns, tokens_in, tokens_out, est_cost
		 FROM agent_sessions WHERE started_at >= ?
		 ORDER BY started_at DESC LIMIT ?`, observeCutoff(since), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AgentSessionSummary
	for rows.Next() {
		var (
			s         AgentSessionSummary
			startedAt string
			endedAt   *string
		)
		if err := rows.Scan(&s.ID, &startedAt, &endedAt, &s.Kind, &s.Provider, &s.Model,
			&s.Turns, &s.TokensIn, &s.TokensOut, &s.Cost); err != nil {
			return nil, err
		}
		s.StartedAt, _ = time.Parse(observeTimeFormat, startedAt)
		if endedAt != nil {
			if t, err := time.Parse(observeTimeFormat, *endedAt); err == nil {
				s.EndedAt = &t
			}
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// AgentExportSession is one session with its events, for JSON export.
type AgentExportSession struct {
	ID        int64              `json:"id"`
	StartedAt string             `json:"started_at"`
	EndedAt   *string            `json:"ended_at,omitempty"`
	Kind      string             `json:"kind"`
	Provider  string             `json:"provider"`
	Model     string             `json:"model"`
	Turns     int64              `json:"turns"`
	TokensIn  int64              `json:"tokens_in"`
	TokensOut int64              `json:"tokens_out"`
	EstCost   float64            `json:"est_cost"`
	ParentID  *int64             `json:"parent_id,omitempty"`
	Events    []AgentExportEvent `json:"events,omitempty"`
}

// AgentExportEvent is one content-free event, for JSON export.
type AgentExportEvent struct {
	CreatedAt  string `json:"created_at"`
	Kind       string `json:"kind"`
	Tool       string `json:"tool,omitempty"`
	DurationMs *int64 `json:"duration_ms,omitempty"`
	Outcome    string `json:"outcome,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

// ExportAgentObservability returns every session (with its events) since the
// cutoff, oldest first, for `shhh observe export`.
func (db *DB) ExportAgentObservability(since time.Time) ([]AgentExportSession, error) {
	rows, err := db.sql.Query(
		`SELECT id, started_at, ended_at, kind, provider, model, turns, tokens_in, tokens_out, est_cost, parent_id
		 FROM agent_sessions WHERE started_at >= ? ORDER BY started_at`, observeCutoff(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []AgentExportSession
	for rows.Next() {
		var s AgentExportSession
		if err := rows.Scan(&s.ID, &s.StartedAt, &s.EndedAt, &s.Kind, &s.Provider, &s.Model,
			&s.Turns, &s.TokensIn, &s.TokensOut, &s.EstCost, &s.ParentID); err != nil {
			return nil, err
		}
		sessions = append(sessions, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range sessions {
		events, err := db.exportAgentEvents(sessions[i].ID)
		if err != nil {
			return nil, err
		}
		sessions[i].Events = events
	}
	return sessions, nil
}

func (db *DB) exportAgentEvents(sessionID int64) ([]AgentExportEvent, error) {
	rows, err := db.sql.Query(
		`SELECT created_at, kind, tool, duration_ms, outcome, reason
		 FROM agent_events WHERE session_id = ? ORDER BY id`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []AgentExportEvent
	for rows.Next() {
		var e AgentExportEvent
		if err := rows.Scan(&e.CreatedAt, &e.Kind, &e.Tool, &e.DurationMs, &e.Outcome, &e.Reason); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// PurgeAgentObservability deletes every recorded session and event, returning
// how many sessions were removed.
func (db *DB) PurgeAgentObservability() (int64, error) {
	if _, err := db.sql.Exec(`DELETE FROM agent_events`); err != nil {
		return 0, err
	}
	res, err := db.sql.Exec(`DELETE FROM agent_sessions`)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count purged sessions: %w", err)
	}
	return n, nil
}

func observeCutoff(since time.Time) string {
	return since.UTC().Format(observeTimeFormat)
}
