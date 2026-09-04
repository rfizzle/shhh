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
	"os"
	"time"

	"github.com/rfizzle/shhh/internal/provider"
)

const observeTimeFormat = "2006-01-02T15:04:05.000Z"

// agentHeartbeatWindow is how long a session's row may go unrefreshed before
// its process id stops being taken as evidence that the session is running.
//
// The id is the liveness check; the window only bounds the one way that
// check can lie, which is a dead session's id being reused by something
// else. It is deliberately generous, because the opposite failure is the one
// that matters here: a second session sitting at its start screen while
// somebody works in the first refreshes nothing for as long as it is idle,
// and a short window would hide exactly the session this reading exists to
// reveal. Nothing stays stale for long either way — the next session's start
// closes every row whose process is gone.
const agentHeartbeatWindow = 12 * time.Hour

// pidRunning answers whether a process is still there. It is a variable
// because "a process that is definitely gone" has no portable spelling a
// test can write down — an id that is free on this machine is in use on the
// next, and each platform disagrees about which ids are even possible.
var pidRunning = pidAlive

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
//
// The row is stamped with this process's id and a first heartbeat, which is
// what later lets another session tell a sitting that is still going from
// one that was killed with its end time never written
// (docs/capabilities/sessions-and-memory.md#a-session-knows-it-is-not-alone).
func (db *DB) StartAgentSession(kind, provider, model string) (int64, error) {
	res, err := db.sql.Exec(
		`INSERT INTO agent_sessions (kind, provider, model, pid, heartbeat)
		 VALUES (?, ?, ?, ?, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`,
		kind, provider, model, os.Getpid(),
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
		`INSERT INTO agent_sessions (kind, provider, model, parent_id, pid, heartbeat)
		 VALUES (?, ?, ?, ?, ?, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`,
		kind, provider, model, parentID, os.Getpid(),
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

// SetAgentSessionOutcome records how the session came out so far. It is
// written at every turn close and overwritten by the next one, because the
// session the record most needs an outcome for is the one whose exit path
// never runs: a run the user gave up on and killed writes nothing on the way
// out, and an outcome stamped only at the end would describe the sessions
// that ended well and say nothing about the rest. The optimistic write
// leaves the last turn's reading standing instead.
// See docs/capabilities/sessions-and-memory.md#whether-it-worked.
func (db *DB) SetAgentSessionOutcome(id int64, outcome string) error {
	_, err := db.sql.Exec(`UPDATE agent_sessions SET outcome = ? WHERE id = ?`, outcome, id)
	return err
}

// EndAgentSession stamps the session's end time, and its outcome when the
// caller has one to correct the standing reading with. An empty outcome
// leaves whatever the turns wrote alone, which is the ordinary case: a
// session that finished a turn has already said how it came out.
func (db *DB) EndAgentSession(id int64, outcome string) error {
	if outcome == "" {
		_, err := db.sql.Exec(
			`UPDATE agent_sessions SET ended_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id = ?`, id,
		)
		return err
	}
	_, err := db.sql.Exec(
		`UPDATE agent_sessions SET ended_at = strftime('%Y-%m-%dT%H:%M:%fZ','now'), outcome = ? WHERE id = ?`,
		outcome, id,
	)
	return err
}

// BeatAgentSession says the session is still there. It is called at every
// turn boundary, which is the coarsest beat that still tracks a person
// working: a session between turns is a session somebody is reading.
//
// It writes to whichever row the recorder holds now, so a conversation that
// ended and opened another inside one process keeps the beat on the row that
// is actually open rather than on the one it left behind.
func (db *DB) BeatAgentSession(id int64) error {
	_, err := db.sql.Exec(
		`UPDATE agent_sessions SET heartbeat = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id = ?`, id,
	)
	return err
}

// LiveSession is another sitting open in this checkout right now, described
// by the only thing a notice needs: when it started.
type LiveSession struct {
	Since time.Time
}

// LiveSibling reports another session open in the checkout that project
// fingerprints, and when it started. The oldest one wins, because the fact
// being reported is that somebody else is here and not how many.
//
// A sibling is another *process*: a row this process opened — the one it is
// in, or the one a new conversation left behind — is never its own sibling,
// and neither is a sub-agent, whose row hangs off a parent and shares its
// id. What is left is a row with the same fingerprint, no end time, a
// heartbeat inside the window and a process that answers.
// See docs/capabilities/sessions-and-memory.md#a-session-knows-it-is-not-alone.
func (db *DB) LiveSibling(project string, now time.Time) (LiveSession, bool, error) {
	if project == "" {
		// No fingerprint is not "every checkout" — it is "we do not know
		// which checkout", and matching on it would report a session in
		// somebody else's directory as a sibling here.
		return LiveSession{}, false, nil
	}
	rows, err := db.sql.Query(
		`SELECT started_at, pid FROM agent_sessions
		 WHERE project = ? AND ended_at IS NULL AND parent_id IS NULL
		   AND pid > 0 AND pid != ? AND heartbeat >= ?
		 ORDER BY started_at`,
		project, os.Getpid(), heartbeatCutoff(now),
	)
	if err != nil {
		return LiveSession{}, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			startedAt string
			pid       int
		)
		if err := rows.Scan(&startedAt, &pid); err != nil {
			return LiveSession{}, false, err
		}
		if !pidRunning(pid) {
			continue
		}
		since, _ := time.Parse(observeTimeFormat, startedAt)
		return LiveSession{Since: since}, true, rows.Err()
	}
	return LiveSession{}, false, rows.Err()
}

// CloseCrashedAgentSessions ends every open row whose process is gone and
// returns how many it closed, the way sandbox ownership records are
// reconciled against the engine at startup: the record outlives the thing it
// describes, so something has to bring the two back in line.
//
// It leaves the outcome alone. A killed session's last turn already said how
// the work was going, and a row that never closed a turn reads as unknown,
// which is the honest answer about a process nobody heard from again.
//
// Rows with no recorded id are left open. They were written by a build that
// recorded none, and closing a row because it cannot vouch for itself would
// rewrite history the reader can still see.
func (db *DB) CloseCrashedAgentSessions() (int, error) {
	rows, err := db.sql.Query(
		`SELECT id, pid FROM agent_sessions WHERE ended_at IS NULL AND pid > 0 AND pid != ?`,
		os.Getpid(),
	)
	if err != nil {
		return 0, err
	}
	var dead []int64
	for rows.Next() {
		var (
			id  int64
			pid int
		)
		if err := rows.Scan(&id, &pid); err != nil {
			rows.Close()
			return 0, err
		}
		if !pidRunning(pid) {
			dead = append(dead, id)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	// The rows are read out before anything is written: the store runs on
	// one connection, so an update issued while the cursor is open waits on
	// a cursor that is waiting on it.
	rows.Close()
	closed := 0
	for _, id := range dead {
		if err := db.EndAgentSession(id, ""); err != nil {
			return closed, err
		}
		closed++
	}
	return closed, nil
}

// liveChatSlots is the set of saved-conversation names another running
// session is writing to. A slot in it is one an autosave in another process
// is about to overwrite, which is why nothing offers to open it.
func (db *DB) liveChatSlots(now time.Time) (map[string]bool, error) {
	rows, err := db.sql.Query(
		`SELECT chat_session, pid FROM agent_sessions
		 WHERE chat_session != '' AND ended_at IS NULL
		   AND pid > 0 AND pid != ? AND heartbeat >= ?`,
		os.Getpid(), heartbeatCutoff(now),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	live := map[string]bool{}
	for rows.Next() {
		var (
			name string
			pid  int
		)
		if err := rows.Scan(&name, &pid); err != nil {
			return nil, err
		}
		if pidRunning(pid) {
			live[name] = true
		}
	}
	return live, rows.Err()
}

// heartbeatCutoff is the oldest beat still trusted, in the column's own
// layout so the comparison is the string one SQLite makes. now is the
// caller's clock and has to be a real one: a zero time would put the cutoff
// two thousand years before any row was written, and every stale row in the
// store would pass the check the window exists to apply.
func heartbeatCutoff(now time.Time) string {
	return now.UTC().Add(-agentHeartbeatWindow).Format(observeTimeFormat)
}

// RecordAgentEvent appends one content-free event to a session. For tool
// events, tool is the tool name, outcome "ok"/"error", reason the error's
// class, and durationMs the call's duration when known. For decision events,
// outcome is allow/deny/ask and reason an enum-like code. For turn events,
// outcome is how the turn ended and round how many rounds it took. For
// signals, outcome is the signal code and reason its qualifier — and for the
// one signal that names a subject as well, the gate's verdict, tool carries
// the suite that ran.
func (db *DB) RecordAgentEvent(sessionID int64, e AgentEvent) error {
	_, err := db.sql.Exec(
		`INSERT INTO agent_events (session_id, kind, tool, duration_ms, outcome, reason, turn, round)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		sessionID, e.Kind, e.Tool, e.DurationMs, e.Outcome, e.Reason, e.Turn, e.Round,
	)
	return err
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

// AgentCohort is one group of the window's sessions: those that ran under
// one value of the split column, with the session-level totals over them.
//
// Sessions is the denominator every rate drawn from this cohort is over, and
// it is why a cohort carries its own count rather than being handed one: two
// cohorts either side of a change are never the same size, and a count read
// off the wrong side turns a difference in how much work went through into a
// difference in how the work went.
type AgentCohort struct {
	// Value is the column's own text. A numeric or boolean setting reads as
	// the number SQLite stores it as, since the cohort is named by what the
	// record holds rather than by how a screen would word it.
	Value     string
	Sessions  int
	TokensIn  int64
	TokensOut int64
	Cost      float64
	// First and Last are when the cohort's earliest and latest sessions
	// started, which is what says which side of a change it is on.
	First time.Time
	Last  time.Time
}

// AgentCohorts groups the window's sessions by one stamped column, largest
// cohort first. Sessions the column is empty or NULL for are left out: a row
// written before the column existed ran under a value nobody recorded, and
// putting them all in one bucket would compare a cohort against the history
// of the store.
func (db *DB) AgentCohorts(since time.Time, key string) ([]AgentCohort, error) {
	column, ok := agentSplitColumn(key)
	if !ok {
		return nil, fmt.Errorf("cannot split sessions on %q", key)
	}
	rows, err := db.sql.Query(fmt.Sprintf(
		`SELECT CAST(%[1]s AS TEXT), COUNT(*),
		        COALESCE(SUM(tokens_in), 0), COALESCE(SUM(tokens_out), 0), COALESCE(SUM(est_cost), 0),
		        MIN(started_at), MAX(started_at)
		 FROM agent_sessions
		 WHERE started_at >= ? AND %[1]s IS NOT NULL AND %[1]s != ''
		 GROUP BY %[1]s ORDER BY COUNT(*) DESC, MIN(started_at)`, column), observeCutoff(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AgentCohort
	for rows.Next() {
		var (
			c           AgentCohort
			first, last string
		)
		if err := rows.Scan(&c.Value, &c.Sessions, &c.TokensIn, &c.TokensOut, &c.Cost, &first, &last); err != nil {
			return nil, err
		}
		c.First, _ = time.Parse(observeTimeFormat, first)
		c.Last, _ = time.Parse(observeTimeFormat, last)
		out = append(out, c)
	}
	return out, rows.Err()
}

// AgentCohortReading is every aggregate the dashboard draws, taken over one
// cohort instead of over the whole window. It is the same set of readings in
// the same shapes, so a figure on the comparison and the same figure on the
// dashboard are the same query with a narrower scope.
type AgentCohortReading struct {
	Turns      []AgentTurnOutcome
	Tools      []AgentToolUsage
	ToolErrors []AgentToolErrorCount
	Decisions  []AgentDecisionCount
	Signals    []AgentSignalCount
	Gates      []AgentGateVerdict
	Outcomes   []AgentSessionOutcome
}

// ReadAgentCohort runs those aggregates for one value of the split column.
func (db *DB) ReadAgentCohort(since time.Time, key, value string) (AgentCohortReading, error) {
	column, ok := agentSplitColumn(key)
	if !ok {
		return AgentCohortReading{}, fmt.Errorf("cannot split sessions on %q", key)
	}
	var (
		r        AgentCohortReading
		err      error
		cutoff   = observeCutoff(since)
		events   = observeEventCohort(column)
		sessions = observeSessionCohort(column)
	)
	if r.Turns, err = db.agentTurns(events, cutoff, value); err != nil {
		return AgentCohortReading{}, fmt.Errorf("query cohort turns: %w", err)
	}
	if r.Tools, err = db.agentToolMix(events, cutoff, value); err != nil {
		return AgentCohortReading{}, fmt.Errorf("query cohort tool mix: %w", err)
	}
	if r.ToolErrors, err = db.agentToolErrors(events, cutoff, value); err != nil {
		return AgentCohortReading{}, fmt.Errorf("query cohort tool errors: %w", err)
	}
	if r.Decisions, err = db.agentDecisions(events, cutoff, value); err != nil {
		return AgentCohortReading{}, fmt.Errorf("query cohort decisions: %w", err)
	}
	if r.Signals, err = db.agentSignals(events, cutoff, value); err != nil {
		return AgentCohortReading{}, fmt.Errorf("query cohort signals: %w", err)
	}
	if r.Gates, err = db.agentGateVerdicts(events, cutoff, value); err != nil {
		return AgentCohortReading{}, fmt.Errorf("query cohort gate verdicts: %w", err)
	}
	if r.Outcomes, err = db.agentSessionOutcomes(sessions, cutoff, value); err != nil {
		return AgentCohortReading{}, fmt.Errorf("query cohort outcomes: %w", err)
	}
	return r, nil
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

const agentToolMixQuery = `SELECT tool, COUNT(*), AVG(duration_ms),
		        AVG(CASE WHEN outcome = 'error' THEN 1.0 ELSE 0.0 END)
		 FROM agent_events WHERE kind = ? AND %s
		 GROUP BY tool ORDER BY COUNT(*) DESC`

// AgentToolMix aggregates tool events by tool name since the cutoff,
// most-called first.
func (db *DB) AgentToolMix(since time.Time) ([]AgentToolUsage, error) {
	return db.agentToolMix(observeEventWindow, observeCutoff(since))
}

func (db *DB) agentToolMix(scope string, args ...any) ([]AgentToolUsage, error) {
	rows, err := db.sql.Query(fmt.Sprintf(agentToolMixQuery, scope), append([]any{AgentEventTool}, args...)...)
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
	return db.agentToolErrors(observeEventWindow, observeCutoff(since))
}

const agentToolErrorsQuery = `SELECT tool, reason, COUNT(*)
		 FROM agent_events WHERE kind = ? AND outcome = 'error' AND %s
		 GROUP BY tool, reason ORDER BY COUNT(*) DESC`

func (db *DB) agentToolErrors(scope string, args ...any) ([]AgentToolErrorCount, error) {
	rows, err := db.sql.Query(fmt.Sprintf(agentToolErrorsQuery, scope), append([]any{AgentEventTool}, args...)...)
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
	return db.agentDecisions(observeEventWindow, observeCutoff(since))
}

const agentDecisionsQuery = `SELECT outcome, reason, COUNT(*)
		 FROM agent_events WHERE kind = ? AND %s
		 GROUP BY outcome, reason ORDER BY COUNT(*) DESC`

func (db *DB) agentDecisions(scope string, args ...any) ([]AgentDecisionCount, error) {
	rows, err := db.sql.Query(fmt.Sprintf(agentDecisionsQuery, scope), append([]any{AgentEventDecision}, args...)...)
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
	return db.agentTurns(observeEventWindow, observeCutoff(since))
}

const agentTurnsQuery = `SELECT outcome, COUNT(*), AVG(round), MAX(round), AVG(duration_ms)
		 FROM agent_events WHERE kind = ? AND %s
		 GROUP BY outcome ORDER BY COUNT(*) DESC`

func (db *DB) agentTurns(scope string, args ...any) ([]AgentTurnOutcome, error) {
	rows, err := db.sql.Query(fmt.Sprintf(agentTurnsQuery, scope), append([]any{AgentEventTurn}, args...)...)
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
	return db.agentSignals(observeEventWindow, observeCutoff(since))
}

const agentSignalsQuery = `SELECT outcome, reason, COUNT(*)
		 FROM agent_events WHERE kind = ? AND %s
		 GROUP BY outcome, reason ORDER BY COUNT(*) DESC`

func (db *DB) agentSignals(scope string, args ...any) ([]AgentSignalCount, error) {
	rows, err := db.sql.Query(fmt.Sprintf(agentSignalsQuery, scope), append([]any{AgentEventSignal}, args...)...)
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

// AgentGateVerdict is how often one quality-gate suite came out one way.
type AgentGateVerdict struct {
	Suite   string
	Verdict string
	Count   int
}

// AgentGateVerdicts aggregates gate runs by suite and verdict since the
// cutoff. It is the one reading in the record that judges the work rather
// than describing it: the checks are the project's own, and they were run
// against a fingerprint of the tree, so a pass cannot vouch for code it did
// not see.
//
// The signal code is spelled here rather than imported, the way the tool
// events' 'error' outcome is: this reads rows written by every build that
// ever wrote one, and their spelling is fixed by history rather than by what
// this build happens to write.
func (db *DB) AgentGateVerdicts(since time.Time) ([]AgentGateVerdict, error) {
	return db.agentGateVerdicts(observeEventWindow, observeCutoff(since))
}

const agentGateVerdictsQuery = `SELECT tool, reason, COUNT(*)
		 FROM agent_events WHERE kind = ? AND outcome = 'gate' AND %s
		 GROUP BY tool, reason ORDER BY tool, COUNT(*) DESC`

func (db *DB) agentGateVerdicts(scope string, args ...any) ([]AgentGateVerdict, error) {
	rows, err := db.sql.Query(fmt.Sprintf(agentGateVerdictsQuery, scope), append([]any{AgentEventSignal}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AgentGateVerdict
	for rows.Next() {
		var g AgentGateVerdict
		if err := rows.Scan(&g.Suite, &g.Verdict, &g.Count); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// AgentSessionOutcome is how many sessions came out one way.
type AgentSessionOutcome struct {
	Outcome string
	Count   int
}

// AgentSessionOutcomes counts sessions by outcome since the cutoff,
// most-frequent first. A session with no outcome recorded counts as
// unknown — it was killed before its first turn closed, or it is still
// running — and unknown is a bucket of its own rather than folded into
// abandoned, because "the record cannot say" and "nothing was finished" are
// different answers and only one of them is about the work.
func (db *DB) AgentSessionOutcomes(since time.Time) ([]AgentSessionOutcome, error) {
	return db.agentSessionOutcomes(observeSessionWindow, observeCutoff(since))
}

const agentSessionOutcomesQuery = `SELECT COALESCE(NULLIF(outcome, ''), 'unknown'), COUNT(*)
		 FROM agent_sessions WHERE %s
		 GROUP BY 1 ORDER BY COUNT(*) DESC`

func (db *DB) agentSessionOutcomes(scope string, args ...any) ([]AgentSessionOutcome, error) {
	rows, err := db.sql.Query(fmt.Sprintf(agentSessionOutcomesQuery, scope), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AgentSessionOutcome
	for rows.Next() {
		var o AgentSessionOutcome
		if err := rows.Scan(&o.Outcome, &o.Count); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// UnratedSession is one session waiting for a person's read on it, together
// with the reminder of what it was.
//
// The reminder is the one field here that is content: it is the title a model
// wrote for the conversation, or failing that the first thing the person said
// in it. That is a deliberate crossing of the line the rest of this file
// holds — the record is content-free, and the question "was this session any
// good" cannot be answered by anything that is. It is read for the walk and
// never written back: what the store keeps is the answer, which is one bit.
// See docs/capabilities/sessions-and-memory.md#a-rating-is-how-you-check-the-inference.
type UnratedSession struct {
	ID        int64
	StartedAt time.Time
	Kind      string
	Model     string
	Turns     int64
	// Outcome is how the record read the session's ending, empty where it
	// could not say. It is what the rating is there to check.
	Outcome string
	// Chat is the saved conversation's name — the handle `shhh chats show`
	// takes, so a reader who wants more than the reminder can go and get it.
	Chat string
	// Title is what a model called the conversation, empty when none did;
	// Opening is the first thing the person said in it.
	Title   string
	Opening string
}

// ListUnratedSessions returns recent sessions nobody has rated yet, newest
// first, and only those whose linked conversation still holds something the
// reader can be reminded by.
//
// The join is what enforces that, and it is the point rather than a
// convenience: a session is a fortnight of nothing to look at without the
// conversation beside it, and asking someone to judge a row of token counts
// produces an answer about nothing. A sub-agent's session is left out by the
// same clause — no child is linked to a conversation — which is right for a
// different reason: a child is judged by the parent that spawned it.
//
// The mapping it joins on is not one to one, and the reminder is the half
// that suffers. Resuming a conversation opens a second session row against
// the same name, so both are reminded by the first sitting's title and
// opening line — the later one is described by work it did not do. What
// keeps the two apart on the card is everything else the row carries: the
// name, the turn count and when it started.
func (db *DB) ListUnratedSessions(limit int) ([]UnratedSession, error) {
	// Whitespace is not a reminder: a message of three spaces and a newline
	// satisfies a bare `!= ''` and reaches the card as an empty body, and the
	// walk would then be asking about a session it is showing nothing of.
	// The trim names its characters because SQLite's one-argument trim takes
	// only spaces, which would leave exactly that message in.
	const opening = `(SELECT m.content FROM chat_messages m
		           WHERE m.session_id = c.id AND m.role = 'user'
		             AND trim(m.content, char(32,9,10,13)) != ''
		           ORDER BY m.seq LIMIT 1)`
	rows, err := db.sql.Query(
		`SELECT a.id, a.started_at, a.kind, a.model, a.turns, COALESCE(a.outcome, ''),
		        c.name, c.title, `+opening+`
		 FROM agent_sessions a
		 JOIN chat_sessions c ON c.name = a.chat_session
		 WHERE a.rating IS NULL AND a.chat_session != '' AND `+opening+` IS NOT NULL
		 ORDER BY a.id DESC
		 LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []UnratedSession
	for rows.Next() {
		var (
			u         UnratedSession
			startedAt string
		)
		if err := rows.Scan(&u.ID, &startedAt, &u.Kind, &u.Model, &u.Turns, &u.Outcome,
			&u.Chat, &u.Title, &u.Opening); err != nil {
			return nil, err
		}
		u.StartedAt, _ = time.Parse(observeTimeFormat, startedAt)
		out = append(out, u)
	}
	return out, rows.Err()
}

// RateAgentSession records a thumbs-up (true) or thumbs-down (false) for a
// session, the way RateRequest does for a command.
func (db *DB) RateAgentSession(id int64, up bool) error {
	rating := 0
	if up {
		rating = 1
	}
	_, err := db.sql.Exec(`UPDATE agent_sessions SET rating = ? WHERE id = ?`, rating, id)
	return err
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
}

const agentSessionColumns = `id, started_at, ended_at, kind, provider, model, turns, tokens_in, tokens_out, est_cost,
		        version, prompt_hash, skills, project, chat_session, parent_id,
		        mode, reasoning, max_rounds, summary_model, summary_interval, summary_enabled,
		        classifier_model, sandbox_profile, config_hash, outcome, rating`

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
		// The outcome column is NULL on a row older than it is and on a
		// session that never closed a turn; both read as no outcome.
		outcome sql.NullString
		// The rating column is NULL until somebody answers for the session.
		rating sql.NullBool
	)
	if err := rows.Scan(&s.ID, &startedAt, &endedAt, &s.Kind, &s.Provider, &s.Model,
		&s.Turns, &s.TokensIn, &s.TokensOut, &s.Cost,
		&s.Version, &s.PromptHash, &s.Skills, &s.Project, &s.ChatSession, &s.ParentID,
		&mode, &reasoning, &maxRounds, &summaryModel, &summaryInterval, &summaryEnabled,
		&classifierModel, &sandboxProfile, &configHash, &outcome, &rating); err != nil {
		return s, err
	}
	s.Outcome = outcome.String
	if rating.Valid {
		s.Rating = &rating.Bool
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
		Skills: s.Skills, Project: s.Project, ChatSession: s.ChatSession,
		Outcome: s.Outcome, Rating: s.Rating, Settings: s.Settings,
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
