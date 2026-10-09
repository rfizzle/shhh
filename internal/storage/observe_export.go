package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/provider"
)

type AgentSessionSummary struct {
	ID          int64
	StartedAt   time.Time
	EndedAt     *time.Time
	Kind        string
	Provider    string
	Model       string
	Turns       int64
	TokensIn    int64
	TokensOut   int64
	Cost        float64
	Version     string
	PromptHash  string
	Skills      int
	Project     string
	ChatSession string
	// ChatSessionID is the conversation the session wrote, as the reference
	// every join uses. It is nil where there is nothing to reach: a session
	// that never saved a conversation, one whose slot has been renamed or
	// pruned since, and every child — so a reader can say "no words to show"
	// rather than printing a timeline of nameless calls.
	ChatSessionID *int64
	ParentID      *int64
	// Name is the agent's name as its supervisor knew it, and empty on
	// every row that is not a child's.
	Name string
	// Outcome is how the session came out, from the closed set in
	// internal/observe. It is empty for a session that never closed a turn,
	// which the reader shows as unknown rather than filling in.
	Outcome string
	// Rating is what a person made of the session, and nil until one of them
	// says. It is a separate fact from Outcome, not a check on the same one:
	// the outcome is inferred from how the session ended, and this is the
	// only thing that can tell whether that inference is any good.
	Rating *bool
	// Settings is nil for a session recorded before settings were, which
	// is a different answer from a session that ran with every value at
	// its zero.
	Settings *AgentSettings
	// Child is how a sub-agent's attempt ended, and nil on every row that
	// is not one. A child that is still running has none either: the row is
	// closed with it.
	Child *observe.ChildEnd
}

const agentSessionColumns = `id, started_at, ended_at, kind, provider, model, turns, tokens_in, tokens_out, est_cost,
		        version, prompt_hash, skills, project, chat_session, chat_session_id, parent_id,
		        mode, reasoning, max_rounds, summary_model, summary_interval, summary_enabled,
		        classifier_model, sandbox_profile, item, stage, config_hash, outcome, rating,
		        check_in_interval, end_reason, verdict, steers, attempt,
		        child_budget, child_admission_floor, child_tokens_inherited, child_tokens_setup,
		        child_tokens_tools, child_tokens_analysis, child_tokens_handoff, child_tokens_fresh, name,
		        agents_require_sandbox, classifier_backend`

func scanAgentSession(rows rowScanner) (AgentSessionSummary, error) {
	var (
		s         AgentSessionSummary
		startedAt string
		endedAt   *string
		// The settings columns are NULL on a row older than they are; the
		// hash is the one that says whether the set was taken at all.
		mode, reasoning, summaryModel, classifierModel, sandboxProfile, configHash sql.NullString
		item, stage, classifierBackend                                             sql.NullString
		maxRounds, summaryInterval                                                 sql.NullInt64
		summaryEnabled, requireSandbox                                             sql.NullBool
		// The outcome column is NULL on a row older than it is and on a
		// session that never closed a turn; both read as no outcome.
		outcome sql.NullString
		// The rating column is NULL until somebody answers for the session.
		rating sql.NullBool
		// The check-in interval joins the settings above; the four beside it
		// are a child's end, NULL on every row that is not a child's and on
		// a child's row until its attempt closes.
		checkInInterval, steers, attempt           sql.NullInt64
		budget, admissionFloor                     sql.NullInt64
		inherited, setup, tools, analysis, handoff sql.NullInt64
		fresh                                      sql.NullInt64
		endReason, verdict                         sql.NullString
		name                                       sql.NullString
	)
	if err := rows.Scan(&s.ID, &startedAt, &endedAt, &s.Kind, &s.Provider, &s.Model,
		&s.Turns, &s.TokensIn, &s.TokensOut, &s.Cost,
		&s.Version, &s.PromptHash, &s.Skills, &s.Project, &s.ChatSession, &s.ChatSessionID, &s.ParentID,
		&mode, &reasoning, &maxRounds, &summaryModel, &summaryInterval, &summaryEnabled,
		&classifierModel, &sandboxProfile, &item, &stage, &configHash, &outcome, &rating,
		&checkInInterval, &endReason, &verdict, &steers, &attempt,
		&budget, &admissionFloor, &inherited, &setup, &tools, &analysis, &handoff,
		&fresh, &name, &requireSandbox, &classifierBackend); err != nil {
		return s, err
	}
	s.Outcome = outcome.String
	s.Name = name.String
	if rating.Valid {
		s.Rating = &rating.Bool
	}
	if configHash.Valid {
		s.Settings = &AgentSettings{
			Mode: mode.String, Reasoning: reasoning.String, MaxRounds: int(maxRounds.Int64),
			SummaryModel: summaryModel.String, SummaryInterval: int(summaryInterval.Int64),
			SummaryEnabled: summaryEnabled.Bool, ClassifierModel: classifierModel.String,
			SandboxProfile: sandboxProfile.String, Item: item.String, Stage: stage.String,
			ConfigHash:           configHash.String,
			CheckInInterval:      int(checkInInterval.Int64),
			AgentsRequireSandbox: requireSandbox.Bool,
			ClassifierBackend:    classifierBackend.String,
		}
	}
	if endReason.Valid && endReason.String != "" {
		s.Child = &observe.ChildEnd{
			Reason: endReason.String, Verdict: verdict.String,
			Steers: int(steers.Int64), Attempt: int(attempt.Int64),
			Budget: budget.Int64, AdmissionFloor: admissionFloor.Int64,
			Tokens: observe.ChildTokens{Inherited: inherited.Int64, Setup: setup.Int64,
				Tools: tools.Int64, Analysis: analysis.Int64, Handoff: handoff.Int64,
				Fresh: fresh.Int64},
		}
	}
	s.StartedAt, _ = time.Parse(observeTimeFormat, startedAt)
	if endedAt != nil {
		if t, err := time.Parse(observeTimeFormat, *endedAt); err == nil {
			s.EndedAt = &t
		}
	}
	return s, nil
}

// AgentSessions lists sessions since the cutoff, newest first.
func (db *DB) AgentSessions(since time.Time, limit int) ([]AgentSessionSummary, error) {
	return queryRows(db, scanAgentSession,
		`SELECT `+agentSessionColumns+`
		 FROM agent_sessions WHERE started_at >= ?
		 ORDER BY started_at DESC LIMIT ?`, observeCutoff(since), limit)
}

// AgentSession reads one session by id. The second result is false when
// there is no such session.
func (db *DB) AgentSession(id int64) (AgentSessionSummary, bool, error) {
	row := db.sql.QueryRow(`SELECT `+agentSessionColumns+` FROM agent_sessions WHERE id = ?`, id)
	s, err := scanAgentSession(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return s, false, nil
		}
		return s, false, err
	}
	return s, true, nil
}

// AgentSessionEvents lists one session's events in order, for the timeline.
func (db *DB) AgentSessionEvents(id int64) ([]AgentExportEvent, error) {
	return db.exportAgentEvents(id)
}

// AgentSessionCall is one tool call as the conversation recorded it: what was
// called, the arguments it was called with, and the text it came back with.
//
// It is content, and it is deliberately not on the event. The record stays
// content-free by construction — every string in agent_events is an
// identifier or a code from a closed set — so the call a row is about is read
// back out of the conversation the session already saved, on the machine that
// saved it, and never copied into the table the export walks.
// See docs/capabilities/sessions-and-memory.md#a-round-can-be-read-back.
type AgentSessionCall struct {
	// Turn and Round are where the call was asked, and what an event is
	// matched to it by.
	Turn, Round int64
	Tool        string
	// Args is the call's arguments as the model sent them, raw JSON.
	Args string
	// Result is what the call came back with, empty for a call whose result
	// never reached the conversation — one cancelled mid-round, or one the
	// slot was saved before.
	Result string
}

// AgentSessionCalls is every tool call in the conversation a session wrote,
// in the order the conversation holds them, each placed at the turn and the
// round it was asked in.
//
// The join is the reference on the session's row and the position on the
// message, which is why both exist: agent_events has always known which round
// a call was made in and chat_messages has always held what the call asked
// for, and until they could be joined "what did round 47 search for" was
// answered by counting rows in one table and hoping they lined up with the
// other.
//
// A session with no conversation to reach — a child, a slot pruned since —
// answers with nothing rather than an error: there is no failure in a session
// that saved no words.
func (db *DB) AgentSessionCalls(id int64) ([]AgentSessionCall, error) {
	type message struct {
		turn, round           int64
		role, content, callID string
		toolCallsJSON         *string
	}
	msgs, err := queryRows(db, scanFields(func(m *message) []any {
		return []any{&m.turn, &m.round, &m.role, &m.content, &m.toolCallsJSON, &m.callID}
	}), `SELECT m.turn, m.round, m.role, m.content, m.tool_calls, m.tool_call_id
		 FROM chat_messages m
		 JOIN agent_sessions a ON a.chat_session_id = m.session_id
		 WHERE a.id = ? ORDER BY m.seq`, id)
	if err != nil {
		return nil, err
	}

	var (
		calls []AgentSessionCall
		// Where each call landed, by the id the conversation gives it, so the
		// result message that follows can be put back beside the call it
		// answers. A round's results usually arrive in call order, but an
		// approval answered out of turn does not, and pairing by position
		// would then file one call's output under another's name.
		at = map[string]int{}
	)
	for _, m := range msgs {
		if m.toolCallsJSON != nil {
			var tcs []provider.ToolCall
			if err := json.Unmarshal([]byte(*m.toolCallsJSON), &tcs); err != nil {
				return nil, fmt.Errorf("unmarshal tool calls: %w", err)
			}
			for _, tc := range tcs {
				at[tc.ID] = len(calls)
				calls = append(calls, AgentSessionCall{
					Turn: m.turn, Round: m.round, Tool: tc.Name, Args: tc.Arguments,
				})
			}
			continue
		}
		if m.role == string(provider.RoleTool) && m.callID != "" {
			if i, ok := at[m.callID]; ok {
				calls[i].Result = m.content
			}
		}
	}
	return calls, nil
}

// AgentExportSession is one session with its events, for JSON export.
type AgentExportSession struct {
	ID          int64   `json:"id"`
	StartedAt   string  `json:"started_at"`
	EndedAt     *string `json:"ended_at,omitempty"`
	Kind        string  `json:"kind"`
	Provider    string  `json:"provider"`
	Model       string  `json:"model"`
	Turns       int64   `json:"turns"`
	TokensIn    int64   `json:"tokens_in"`
	TokensOut   int64   `json:"tokens_out"`
	EstCost     float64 `json:"est_cost"`
	ParentID    *int64  `json:"parent_id,omitempty"`
	Name        string  `json:"name,omitempty"`
	Version     string  `json:"version,omitempty"`
	PromptHash  string  `json:"prompt_hash,omitempty"`
	Skills      int     `json:"skills,omitempty"`
	Project     string  `json:"project,omitempty"`
	ChatSession string  `json:"chat_session,omitempty"`
	// Outcome is how the session came out; absent on a session that never
	// closed a turn.
	Outcome string `json:"outcome,omitempty"`
	// Rating is what a person made of the session; absent until one of them
	// says, which is a different fact from a thumbs-down.
	Rating *bool `json:"rating,omitempty"`
	// Settings is what the session ran under; absent on a session recorded
	// before settings were.
	Settings *AgentSettings     `json:"settings,omitempty"`
	Events   []AgentExportEvent `json:"events,omitempty"`
	// Transcript is the saved conversation, present only when the export
	// asked for it and the session was linked to one.
	Transcript []AgentExportMessage `json:"transcript,omitempty"`
}

// AgentExportEvent is one content-free event, for JSON export.
type AgentExportEvent struct {
	CreatedAt  string `json:"created_at"`
	Kind       string `json:"kind"`
	Turn       int64  `json:"turn,omitempty"`
	Round      int64  `json:"round,omitempty"`
	Tool       string `json:"tool,omitempty"`
	DurationMs *int64 `json:"duration_ms,omitempty"`
	Outcome    string `json:"outcome,omitempty"`
	Reason     string `json:"reason,omitempty"`
	Purpose    string `json:"purpose,omitempty"`
}

// AgentExportMessage is one conversation message in the transcript join.
type AgentExportMessage struct {
	Role       string              `json:"role"`
	Content    string              `json:"content,omitempty"`
	ToolCalls  []provider.ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string              `json:"tool_call_id,omitempty"`
}

// ExportAgentObservability returns every session (with its events) since the
// cutoff, oldest first, for `shhh observe export`. With transcript set, each
// session linked to a saved conversation carries it too — this is the one
// path that puts content beside the metrics, and it runs only when asked.
func (db *DB) ExportAgentObservability(since time.Time, transcript bool) ([]AgentExportSession, error) {
	summaries, err := queryRows(db, scanAgentSession,
		`SELECT `+agentSessionColumns+`
		 FROM agent_sessions WHERE started_at >= ? ORDER BY started_at`, observeCutoff(since))
	if err != nil {
		return nil, err
	}

	var (
		sessions []AgentExportSession
		// The conversation each row wrote, as the reference rather than the
		// name it was saved under: the name on the row is the one the
		// session used, and a conversation renamed since would either hand
		// the export nothing or hand it whatever holds that name now.
		slots []*int64
	)
	for _, s := range summaries {
		sessions = append(sessions, exportSession(s))
		slots = append(slots, s.ChatSessionID)
	}

	for i := range sessions {
		events, err := db.exportAgentEvents(sessions[i].ID)
		if err != nil {
			return nil, err
		}
		sessions[i].Events = events
		if !transcript || slots[i] == nil {
			continue
		}
		msgs, err := db.chatMessages(*slots[i])
		if err != nil {
			// A conversation deleted since is a gap, not a failure of the
			// export; the metrics stand on their own.
			continue
		}
		for _, msg := range msgs {
			sessions[i].Transcript = append(sessions[i].Transcript, AgentExportMessage{
				Role: string(msg.Role), Content: msg.Content, ToolCalls: msg.ToolCalls, ToolCallID: msg.ToolCallID,
			})
		}
	}
	return sessions, nil
}

func exportSession(s AgentSessionSummary) AgentExportSession {
	out := AgentExportSession{
		ID: s.ID, StartedAt: s.StartedAt.UTC().Format(observeTimeFormat),
		Kind: s.Kind, Provider: s.Provider, Model: s.Model,
		Turns: s.Turns, TokensIn: s.TokensIn, TokensOut: s.TokensOut, EstCost: s.Cost,
		ParentID: s.ParentID, Name: s.Name, Version: s.Version, PromptHash: s.PromptHash,
		Skills: s.Skills, Project: s.Project, ChatSession: s.ChatSession,
		Outcome: s.Outcome, Rating: s.Rating, Settings: s.Settings,
	}
	if s.EndedAt != nil {
		e := s.EndedAt.UTC().Format(observeTimeFormat)
		out.EndedAt = &e
	}
	return out
}

// exportAgentEvents is the events the timeline and the export carry. The
// timing rows are left out of both: they are read by AgentTimings, whose
// shape is theirs, and a startup row drawn as a timeline event would be a
// phase with no turn read as something the session did.
func (db *DB) exportAgentEvents(sessionID int64) ([]AgentExportEvent, error) {
	return queryRows(db, scanFields(func(e *AgentExportEvent) []any {
		return []any{&e.CreatedAt, &e.Kind, &e.Turn, &e.Round, &e.Tool, &e.DurationMs, &e.Outcome, &e.Reason, &e.Purpose}
	}), `SELECT created_at, kind, turn, round, tool, duration_ms, outcome, reason, purpose
		 FROM agent_events WHERE session_id = ? AND kind NOT IN (?, ?) ORDER BY id`,
		sessionID, AgentEventStartup, AgentEventQuiet)
}

// PruneAgentObservability deletes the sessions that ended before the window
// and every event they recorded, and returns how many sessions it removed.
// It is the record's window, the way history and the report store have one;
// PurgeAgentObservability below is the switch, and the two stay apart because
// a reader who wants the last six months and a reader who wants none of it
// are asking different questions.
//
// A session is pruned with its whole family. agent_sessions.parent_id has no
// cascade of its own, so a parent deleted while one of its children survives
// would leave a row pointing at nothing — and a child outlives its parent
// often enough to matter, since a killed child's row is closed at the next
// session's start rather than when its parent ended. Taking the descendants
// with it is also the honest reading: a sub-agent's spend is only meaningful
// against the session that spawned it.
//
// The events go first and both statements share one transaction, so there is
// no instant at which an event's session is gone. Nothing here relies on the
// cascade the schema declares: a prune that quietly did nothing because a
// pragma was off is exactly the failure a window cannot afford, since nobody
// looks at a table that is supposed to shrink by itself.
func (db *DB) PruneAgentObservability(retentionDays int) (int64, error) {
	if retentionDays <= 0 {
		return 0, nil
	}
	cutoff := observeCutoff(time.Now().AddDate(0, 0, -retentionDays))
	tx, err := db.sql.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	// Only an ended row starts a family off. An open one is either a session
	// still running — a sitting older than the window is implausible, but
	// deleting the row out from under one would leave it recording into
	// nothing — or one whose ending was never written, and the next session's
	// start closes that, which brings it into the prune's reach then. A
	// descendant goes with its parent whether or not it ended, because the
	// alternative is the dangling row this exists to avoid.
	const doomed = `WITH RECURSIVE doomed(id) AS (
		    SELECT id FROM agent_sessions WHERE ended_at IS NOT NULL AND ended_at < ?
		    UNION
		    SELECT s.id FROM agent_sessions s JOIN doomed d ON s.parent_id = d.id
		)`
	if _, err := tx.Exec(doomed+`
		 DELETE FROM agent_events WHERE session_id IN (SELECT id FROM doomed)`, cutoff); err != nil {
		return 0, err
	}
	res, err := tx.Exec(doomed+`
		 DELETE FROM agent_sessions WHERE id IN (SELECT id FROM doomed)`, cutoff)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count pruned sessions: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return n, nil
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
