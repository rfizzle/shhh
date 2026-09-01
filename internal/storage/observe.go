package storage

// Session observability: agent sessions record content-free events — tokens,
// cost, model, tool-call counts/durations/outcomes, mode decisions with
// enum-like reason codes, turn ends, and the signals the loop's own
// safeguards raise. Never prompts, outputs, paths, or commands: every stored
// string is either a fixed identifier (provider, model, tool name, skill
// name) or a code from a closed set, so the content-free guarantee holds
// structurally. The one deliberate exception is the export's transcript
// join, which the caller has to ask for.
// See docs/capabilities/sessions-and-memory.md#observations-are-what-the-session-did.

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/rfizzle/shhh/internal/provider"
)

const observeTimeFormat = "2006-01-02T15:04:05.000Z"

// Agent event kinds.
const (
	AgentEventTool     = "tool"
	AgentEventDecision = "decision"
	// AgentEventTurn closes one turn: outcome is how it ended, round is how
	// many tool rounds it took, duration_ms its wall time.
	AgentEventTurn = "turn"
	// AgentEventSignal is one of the loop's own safeguards or a workflow
	// transition firing: outcome is the signal code, reason its qualifier.
	AgentEventSignal = "signal"
)

// AgentEvent is one content-free event. Turn and Round place it in the
// session — a tool call in round 40 of turn 3 is a different fact from the
// same call in round 2 — and are zero only where the recorder that wrote the
// row kept no such accounting, which every surface shipping today does.
type AgentEvent struct {
	Kind       string
	Tool       string
	DurationMs *int64
	Outcome    string
	Reason     string
	Turn       int64
	Round      int64
}

// AgentProvenance is what a session ran under, stamped once it is known.
// It is what makes a before/after comparison of a prompt or a workflow
// change possible: without it two weeks of sessions are one population.
type AgentProvenance struct {
	// Version is the shhh build.
	Version string
	// PromptHash fingerprints the system prompt as sent, so an edit to it
	// splits the sessions on either side.
	PromptHash string
	// Skills is how many skills the catalog loaded.
	Skills int
	// Project fingerprints the checkout the session ran in.
	Project string
	// Settings is what the session was configured with. It is written only
	// when its ConfigHash is set: a hash is what says the set was taken, so
	// a stamp that carries none leaves the columns NULL rather than writing
	// a row of zero values that would read as "manual, off, uncapped".
	Settings AgentSettings
}

// AgentSettings is what a session was configured with: the tuning values a
// comparison can group and filter by, and one hash over the whole effective
// config for everything it cannot.
//
// The two halves answer different questions and neither replaces the other.
// A hash tells "before I changed something" from "after", for a setting
// nobody thought to enumerate — but it has no order and no meaning, so it
// cannot answer "interval 10 against interval 20", and that is the question
// a tuning loop actually asks. The scalars can; the hash catches what they
// miss.
//
// The scalars are an allowlist, and that is what keeps the record
// content-free: every value here is a mode name, a level, a model name, a
// count or a profile name — a fixed identifier or a code from a closed set,
// never a path, a command or a secret. A config field is stamped by being
// named here and nowhere else, so a new field is excluded until someone
// decides otherwise; it still reaches the hash, so a change to it is never
// invisible.
// See docs/capabilities/sessions-and-memory.md#what-a-session-ran-under.
type AgentSettings struct {
	// Mode is the permission mode the session started in, empty on a
	// surface with no permission mode (a one-shot, a headless run).
	Mode string `json:"mode,omitempty"`
	// Reasoning is the level the session started thinking at.
	Reasoning string `json:"reasoning"`
	// MaxRounds is the per-turn tool-round cap in force; 0 is no cap.
	MaxRounds int `json:"max_rounds"`
	// SummaryModel and SummaryInterval are the summariser's model and
	// reading interval when SummaryEnabled, and empty otherwise.
	SummaryModel    string `json:"summary_model,omitempty"`
	SummaryInterval int    `json:"summary_interval,omitempty"`
	SummaryEnabled  bool   `json:"summary_enabled"`
	// ClassifierModel is the model auto mode's classifier asks, empty on a
	// surface that has none.
	ClassifierModel string `json:"classifier_model,omitempty"`
	// SandboxProfile is the containment profile in force, empty when
	// nothing contains the session's commands.
	SandboxProfile string `json:"sandbox_profile,omitempty"`
	// ConfigHash fingerprints the whole effective config.
	ConfigHash string `json:"config_hash"`
}

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
// sub-agent spend is attributable. A non-positive parentID records an
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

// StampAgentSession records a session's provenance and its settings in one
// statement, so a row is never left with one half and not the other. A stamp
// that carries no settings writes NULL to every settings column, which is
// what an unstamped row holds and what the reader takes for "none".
func (db *DB) StampAgentSession(id int64, p AgentProvenance) error {
	c := p.Settings
	settings := []any{nil, nil, nil, nil, nil, nil, nil, nil, nil}
	if c.ConfigHash != "" {
		settings = []any{c.Mode, c.Reasoning, c.MaxRounds, c.SummaryModel, c.SummaryInterval, c.SummaryEnabled,
			c.ClassifierModel, c.SandboxProfile, c.ConfigHash}
	}
	args := append([]any{p.Version, p.PromptHash, p.Skills, p.Project}, settings...)
	_, err := db.sql.Exec(
		`UPDATE agent_sessions SET version = ?, prompt_hash = ?, skills = ?, project = ?,
		        mode = ?, reasoning = ?, max_rounds = ?,
		        summary_model = ?, summary_interval = ?, summary_enabled = ?,
		        classifier_model = ?, sandbox_profile = ?, config_hash = ?
		 WHERE id = ?`,
		append(args, id)...,
	)
	return err
}

// LinkAgentSession names the saved conversation this session is the metadata
// of, so the two can be joined when someone deliberately wants to read what
// a session said. The name is a timestamp or a name the user chose, not
// content.
func (db *DB) LinkAgentSession(id int64, chatSession string) error {
	_, err := db.sql.Exec(`UPDATE agent_sessions SET chat_session = ? WHERE id = ?`, chatSession, id)
	return err
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
// events, tool is the tool name, outcome "ok"/"error", reason the error's
// class, and durationMs the call's duration when known. For decision events,
// outcome is allow/deny/ask and reason an enum-like code. For turn events,
// outcome is how the turn ended and round how many rounds it took. For
// signals, outcome is the signal code and reason its qualifier.
func (db *DB) RecordAgentEvent(sessionID int64, e AgentEvent) error {
	_, err := db.sql.Exec(
		`INSERT INTO agent_events (session_id, kind, tool, duration_ms, outcome, reason, turn, round)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		sessionID, e.Kind, e.Tool, e.DurationMs, e.Outcome, e.Reason, e.Turn, e.Round,
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

// AgentToolErrorCount is how often one tool failed one way.
type AgentToolErrorCount struct {
	Tool  string
	Class string
	Count int
}

// AgentToolErrors aggregates failed tool events by tool and error class
// since the cutoff, most-frequent first. The class is what makes the number
// actionable: a tool that fails on arguments is a prompt problem, one that
// fails on scope is a policy problem, and the error rate alone cannot say
// which.
func (db *DB) AgentToolErrors(since time.Time) ([]AgentToolErrorCount, error) {
	rows, err := db.sql.Query(
		`SELECT tool, reason, COUNT(*)
		 FROM agent_events WHERE kind = ? AND outcome = 'error' AND created_at >= ?
		 GROUP BY tool, reason ORDER BY COUNT(*) DESC`, AgentEventTool, observeCutoff(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AgentToolErrorCount
	for rows.Next() {
		var c AgentToolErrorCount
		if err := rows.Scan(&c.Tool, &c.Class, &c.Count); err != nil {
			return nil, err
		}
		out = append(out, c)
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

// AgentTurnOutcome is how many turns ended one way, and what they took.
type AgentTurnOutcome struct {
	Outcome       string
	Count         int
	AvgRounds     float64
	MaxRounds     int64
	AvgDurationMs *float64
}

// AgentTurns aggregates turn events by how they ended since the cutoff,
// most-frequent first. Rounds per turn is the efficiency number: a prompt
// change that helps shows up here as fewer rounds for the same outcome.
func (db *DB) AgentTurns(since time.Time) ([]AgentTurnOutcome, error) {
	rows, err := db.sql.Query(
		`SELECT outcome, COUNT(*), AVG(round), MAX(round), AVG(duration_ms)
		 FROM agent_events WHERE kind = ? AND created_at >= ?
		 GROUP BY outcome ORDER BY COUNT(*) DESC`, AgentEventTurn, observeCutoff(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AgentTurnOutcome
	for rows.Next() {
		var t AgentTurnOutcome
		if err := rows.Scan(&t.Outcome, &t.Count, &t.AvgRounds, &t.MaxRounds, &t.AvgDurationMs); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// AgentSignalCount is how often one signal fired with one qualifier.
type AgentSignalCount struct {
	Signal string
	Reason string
	Count  int
}

// AgentSignals aggregates signal events since the cutoff, most-frequent
// first. These are the base rates a guard is designed against: how often
// the summarizer reads the session as off target, how often the repeat
// detector fires, how often a turn hits its round cap.
func (db *DB) AgentSignals(since time.Time) ([]AgentSignalCount, error) {
	rows, err := db.sql.Query(
		`SELECT outcome, reason, COUNT(*)
		 FROM agent_events WHERE kind = ? AND created_at >= ?
		 GROUP BY outcome, reason ORDER BY COUNT(*) DESC`, AgentEventSignal, observeCutoff(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AgentSignalCount
	for rows.Next() {
		var s AgentSignalCount
		if err := rows.Scan(&s.Signal, &s.Reason, &s.Count); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

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
	ParentID    *int64
	// Settings is nil for a session recorded before settings were, which
	// is a different answer from a session that ran with every value at
	// its zero.
	Settings *AgentSettings
}

const agentSessionColumns = `id, started_at, ended_at, kind, provider, model, turns, tokens_in, tokens_out, est_cost,
		        version, prompt_hash, skills, project, chat_session, parent_id,
		        mode, reasoning, max_rounds, summary_model, summary_interval, summary_enabled,
		        classifier_model, sandbox_profile, config_hash`

func scanAgentSession(rows interface{ Scan(...any) error }) (AgentSessionSummary, error) {
	var (
		s         AgentSessionSummary
		startedAt string
		endedAt   *string
		// The settings columns are NULL on a row older than they are; the
		// hash is the one that says whether the set was taken at all.
		mode, reasoning, summaryModel, classifierModel, sandboxProfile, configHash sql.NullString
		maxRounds, summaryInterval                                                 sql.NullInt64
		summaryEnabled                                                             sql.NullBool
	)
	if err := rows.Scan(&s.ID, &startedAt, &endedAt, &s.Kind, &s.Provider, &s.Model,
		&s.Turns, &s.TokensIn, &s.TokensOut, &s.Cost,
		&s.Version, &s.PromptHash, &s.Skills, &s.Project, &s.ChatSession, &s.ParentID,
		&mode, &reasoning, &maxRounds, &summaryModel, &summaryInterval, &summaryEnabled,
		&classifierModel, &sandboxProfile, &configHash); err != nil {
		return s, err
	}
	if configHash.Valid {
		s.Settings = &AgentSettings{
			Mode: mode.String, Reasoning: reasoning.String, MaxRounds: int(maxRounds.Int64),
			SummaryModel: summaryModel.String, SummaryInterval: int(summaryInterval.Int64),
			SummaryEnabled: summaryEnabled.Bool, ClassifierModel: classifierModel.String,
			SandboxProfile: sandboxProfile.String, ConfigHash: configHash.String,
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
	rows, err := db.sql.Query(
		`SELECT `+agentSessionColumns+`
		 FROM agent_sessions WHERE started_at >= ?
		 ORDER BY started_at DESC LIMIT ?`, observeCutoff(since), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AgentSessionSummary
	for rows.Next() {
		s, err := scanAgentSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
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
	Version     string  `json:"version,omitempty"`
	PromptHash  string  `json:"prompt_hash,omitempty"`
	Skills      int     `json:"skills,omitempty"`
	Project     string  `json:"project,omitempty"`
	ChatSession string  `json:"chat_session,omitempty"`
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
	rows, err := db.sql.Query(
		`SELECT `+agentSessionColumns+`
		 FROM agent_sessions WHERE started_at >= ? ORDER BY started_at`, observeCutoff(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []AgentExportSession
	for rows.Next() {
		s, err := scanAgentSession(rows)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, exportSession(s))
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
		if !transcript || sessions[i].ChatSession == "" {
			continue
		}
		msgs, err := db.LoadChat(sessions[i].ChatSession)
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
		ParentID: s.ParentID, Version: s.Version, PromptHash: s.PromptHash,
		Skills: s.Skills, Project: s.Project, ChatSession: s.ChatSession, Settings: s.Settings,
	}
	if s.EndedAt != nil {
		e := s.EndedAt.UTC().Format(observeTimeFormat)
		out.EndedAt = &e
	}
	return out
}

func (db *DB) exportAgentEvents(sessionID int64) ([]AgentExportEvent, error) {
	rows, err := db.sql.Query(
		`SELECT created_at, kind, turn, round, tool, duration_ms, outcome, reason
		 FROM agent_events WHERE session_id = ? ORDER BY id`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []AgentExportEvent
	for rows.Next() {
		var e AgentExportEvent
		if err := rows.Scan(&e.CreatedAt, &e.Kind, &e.Turn, &e.Round, &e.Tool, &e.DurationMs, &e.Outcome, &e.Reason); err != nil {
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
