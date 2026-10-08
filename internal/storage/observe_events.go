package storage

import (
	"time"

	"github.com/rfizzle/shhh/internal/tools"
)

// RecordAgentEvent appends one content-free event to a session. For tool
// events, tool is the tool name, outcome "ok"/"error", reason the error's
// class, and durationMs the call's duration when known. For decision events,
// outcome is allow/deny/ask and reason an enum-like code. For turn events,
// outcome is how the turn ended and round how many rounds it took. For
// signals, outcome is the signal code and reason its qualifier — and for the
// one signal that names a subject as well, the gate's verdict, tool carries
// the suite that ran. A tool event for a command carries its purpose word
// beside the class. Startup and quiet rows are described at their kinds.
//
// The write is retried while a lock is refused (storage.go): an event is the
// record of something that happened, so dropping one under contention would
// make the dashboard quietest exactly when the checkout is busiest.
func (db *DB) RecordAgentEvent(sessionID int64, e AgentEvent) error {
	return db.execRetry(
		`INSERT INTO agent_events (session_id, kind, tool, duration_ms, outcome, reason, turn, round, purpose,
		   model_first_ms, model_stream_ms, tool_ms, person_ms, delivered)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sessionID, e.Kind, e.Tool, e.DurationMs, e.Outcome, e.Reason, e.Turn, e.Round, e.Purpose,
		e.ModelFirstMs, e.ModelStreamMs, e.ToolMs, e.PersonMs, e.Delivered,
	)
}

// observeEventWindow and observeSessionWindow are the dashboard's scopes:
// every event, or every session, since the cutoff.
//
// Every aggregate below takes its scope rather than writing one, because the
// comparison has to draw the same readings the dashboard does. Two copies of
// a GROUP BY that were meant to agree are two copies that will not: the one
// nobody edited goes on answering the old question, and a comparison that
// measures something the dashboard does not draw is worse than no comparison,
// since nothing on either screen says they disagree.
const (
	observeEventWindow   = `created_at >= ?`
	observeSessionWindow = `started_at >= ?`
)

// observeEventSession narrows an event aggregate to one session, so a
// figure a session's own page prints is drawn by the query the window and
// the cohort are drawn by rather than by a second one beside it.
const observeEventSession = `session_id = ?`

// observeEventsOfWindowSessions is the window's events scoped by the session
// that wrote them rather than by their own timestamps, which a reading of
// whole sessions has to be: a session that was already open when the window
// opened would otherwise be read as half of one, with its looking on one
// side of the cutoff and the write that ended it on the other. It is how the
// cohort scope is built, and for the same reason.
//
// It is deliberately not observeEventWindow, which every other aggregate in
// this file takes. Those count events, so an event either side of the cutoff
// is a whole fact on its own; this one counts what a session did before
// something else it did, and half a session is not a smaller reading of it
// but a wrong one.
const observeEventsOfWindowSessions = `session_id IN (SELECT id FROM agent_sessions WHERE started_at >= ?)`

// observeEventCohort and observeSessionCohort narrow those to the sessions
// that ran under one value of a split column.
//
// The column is written into the SQL rather than bound as a parameter, which
// is not something a placeholder can do — so it comes from agentSplitColumn
// and from nowhere else, and a key that is not on that list never reaches a
// query. The value beside it is bound normally.
//
// Events are scoped by the session that wrote them and not by their own
// timestamp as well: no event predates the session it belongs to, so the
// session's window already bounds them, and a second cutoff would only
// differ by cutting the tail off a session that started inside the window
// and ran past its edge.
func observeEventCohort(column string) string {
	return `session_id IN (SELECT id FROM agent_sessions WHERE started_at >= ? AND ` + column + ` = ?)`
}

func observeSessionCohort(column string) string {
	return `started_at >= ? AND ` + column + ` = ?`
}

// agentSplitColumns are the columns a window's sessions may be split into
// cohorts on: the provenance a session is stamped with, and the tuning
// values stamped beside it. Splitting on anything else is refused rather
// than passed through, because the name is SQL rather than data.
//
// Every key is spelled the way its column is, so the person typing one is
// naming the thing the record actually holds.
var agentSplitColumns = []string{
	"prompt_hash", "config_hash", "version", "model",
	"mode", "reasoning", "max_rounds",
	"summary_model", "summary_interval", "summary_enabled",
	"classifier_model", "sandbox_profile",
	"item", "stage", "check_in_interval", "agents_require_sandbox",
}

// AgentSplitKeys lists what a comparison can split on, for the flag that
// takes one and the error a mistyped one gets.
func AgentSplitKeys() []string {
	return append([]string(nil), agentSplitColumns...)
}

// agentSplitColumn resolves a key to its column, reporting whether it is one
// this store will split on.
func agentSplitColumn(key string) (string, bool) {
	for _, c := range agentSplitColumns {
		if c == key {
			return c, true
		}
	}
	return "", false
}

// AgentCommandEvent is one recorded command, addressed by its row so a
// purpose read back from the conversation can be written onto it.
type AgentCommandEvent struct {
	ID, Turn, Round int64
	Purpose         string
}

// AgentUnclassifiedCommandSessions is every session started since the cutoff
// that recorded a command with no purpose word, oldest first.
func (db *DB) AgentUnclassifiedCommandSessions(since time.Time) ([]int64, error) {
	return queryRows(db, scanFields(func(id *int64) []any { return []any{id} }),
		`SELECT DISTINCT e.session_id FROM agent_events e
		 JOIN agent_sessions a ON a.id = e.session_id
		 WHERE e.kind = ? AND e.tool = ? AND e.purpose = '' AND a.started_at >= ?
		 ORDER BY e.session_id`, AgentEventTool, tools.ExecCommandName, observeCutoff(since))
}

// AgentCommandEvents is every command one session recorded, in the order it
// recorded them, classified or not: a round's commands are paired with the
// conversation's by their order, so a reader that skipped the classified ones
// would hand each of the rest its neighbour's line.
func (db *DB) AgentCommandEvents(sessionID int64) ([]AgentCommandEvent, error) {
	return queryRows(db, scanFields(func(e *AgentCommandEvent) []any {
		return []any{&e.ID, &e.Turn, &e.Round, &e.Purpose}
	}), `SELECT id, turn, round, purpose FROM agent_events
		 WHERE session_id = ? AND kind = ? AND tool = ? ORDER BY id`,
		sessionID, AgentEventTool, tools.ExecCommandName)
}

// SetAgentCommandPurposes writes purpose words onto recorded commands by row,
// in one transaction, and only onto a row that has none: a word the session
// recorded as it ran is the line that ran, and a reading of the conversation
// afterwards is the line the model asked for, which an amended command made
// different. It returns how many rows took a word.
func (db *DB) SetAgentCommandPurposes(purposes map[int64]string) (int, error) {
	if len(purposes) == 0 {
		return 0, nil
	}
	var written int
	err := retryBusy(func() error {
		written = 0
		tx, err := db.sql.Begin()
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		for id, purpose := range purposes {
			res, err := tx.Exec(
				`UPDATE agent_events SET purpose = ? WHERE id = ? AND kind = ? AND tool = ? AND purpose = ''`,
				purpose, id, AgentEventTool, tools.ExecCommandName)
			if err != nil {
				return err
			}
			n, err := res.RowsAffected()
			if err != nil {
				return err
			}
			written += int(n)
		}
		return tx.Commit()
	})
	return written, err
}
