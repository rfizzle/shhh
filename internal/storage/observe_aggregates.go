package storage

import (
	"fmt"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/tools"
)

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
	return queryRows(db, func(r rowScanner) (AgentCohort, error) {
		var (
			c           AgentCohort
			first, last string
		)
		if err := r.Scan(&c.Value, &c.Sessions, &c.TokensIn, &c.TokensOut, &c.Cost, &first, &last); err != nil {
			return c, err
		}
		c.First, _ = time.Parse(observeTimeFormat, first)
		c.Last, _ = time.Parse(observeTimeFormat, last)
		return c, nil
	}, fmt.Sprintf(
		`SELECT CAST(%[1]s AS TEXT), COUNT(*),
		        COALESCE(SUM(tokens_in), 0), COALESCE(SUM(tokens_out), 0), COALESCE(SUM(est_cost), 0),
		        MIN(started_at), MAX(started_at)
		 FROM agent_sessions
		 WHERE started_at >= ? AND %[1]s IS NOT NULL AND %[1]s != ''
		 GROUP BY %[1]s ORDER BY COUNT(*) DESC, MIN(started_at)`, column), observeCutoff(since))
}

// AgentCohortReading is every aggregate the dashboard draws, taken over one
// cohort instead of over the whole window. It is the same set of readings in
// the same shapes, so a figure on the comparison and the same figure on the
// dashboard are the same query with a narrower scope.
type AgentCohortReading struct {
	Turns      []AgentTurnOutcome
	Tools      []AgentToolUsage
	ToolErrors []AgentToolErrorCount
	// Commands is the dashboard's COMMANDS split over this cohort: the share
	// of shell calls that were a read a built-in tool answers is the figure a
	// change to those tools is made to move.
	Commands    []AgentCommandPurpose
	FirstWrites []AgentFirstWrite
	Decisions   []AgentDecisionCount
	// Overturns is the one decision figure that is a pairing rather than a
	// count: how often the person said yes to the classifier's no.
	Overturns AgentOverturns
	Signals   []AgentSignalCount
	// Interventions is the same join the dashboard draws, over this cohort:
	// the reading that follows an interruption is the one figure a change to
	// the thresholds is meant to move, so a comparison without it cannot
	// answer the question it was run for.
	Interventions []AgentInterventionOutcome
	Gates         []AgentGateVerdict
	Outcomes      []AgentSessionOutcome
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
	if r.Commands, err = db.agentCommandPurposes(events, cutoff, value); err != nil {
		return AgentCohortReading{}, fmt.Errorf("query cohort command purposes: %w", err)
	}
	if r.FirstWrites, err = db.agentFirstWrites(events, cutoff, value); err != nil {
		return AgentCohortReading{}, fmt.Errorf("query cohort first writes: %w", err)
	}
	if r.Decisions, err = db.agentDecisions(events, cutoff, value); err != nil {
		return AgentCohortReading{}, fmt.Errorf("query cohort decisions: %w", err)
	}
	if r.Overturns, err = db.agentOverturns(events, cutoff, value); err != nil {
		return AgentCohortReading{}, fmt.Errorf("query cohort overturns: %w", err)
	}
	if r.Signals, err = db.agentSignals(events, cutoff, value); err != nil {
		return AgentCohortReading{}, fmt.Errorf("query cohort signals: %w", err)
	}
	if r.Interventions, err = db.agentInterventionOutcomes(events, cutoff, value); err != nil {
		return AgentCohortReading{}, fmt.Errorf("query cohort intervention outcomes: %w", err)
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
	return queryRows(db, scanFields(func(u *AgentDayUsage) []any {
		return []any{&u.Day, &u.Sessions, &u.TokensIn, &u.TokensOut, &u.Cost}
	}), `SELECT substr(started_at, 1, 10) AS day, COUNT(*),
		        COALESCE(SUM(tokens_in), 0), COALESCE(SUM(tokens_out), 0), COALESCE(SUM(est_cost), 0)
		 FROM agent_sessions WHERE started_at >= ?
		 GROUP BY day ORDER BY day DESC`, observeCutoff(since))
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
	return queryRows(db, scanFields(func(u *AgentModelUsage) []any {
		return []any{&u.Provider, &u.Model, &u.Sessions, &u.TokensIn, &u.TokensOut, &u.Cost}
	}), `SELECT provider, model, COUNT(*),
		        COALESCE(SUM(tokens_in), 0), COALESCE(SUM(tokens_out), 0), COALESCE(SUM(est_cost), 0)
		 FROM agent_sessions WHERE started_at >= ?
		 GROUP BY provider, model ORDER BY COUNT(*) DESC`, observeCutoff(since))
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
	return queryScoped(db, scanFields(func(u *AgentToolUsage) []any {
		return []any{&u.Tool, &u.Count, &u.AvgDurationMs, &u.ErrorRate}
	}), agentToolMixQuery, scope, []any{AgentEventTool}, args)
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
	return queryScoped(db, scanFields(func(c *AgentToolErrorCount) []any {
		return []any{&c.Tool, &c.Class, &c.Count}
	}), agentToolErrorsQuery, scope, []any{AgentEventTool}, args)
}

// AgentCommandPurpose is how many of the commands the model ran did one kind
// of thing. Purpose is empty for a command recorded before the record said.
type AgentCommandPurpose struct {
	Purpose string
	Count   int
}

const agentCommandPurposesQuery = `SELECT purpose, COUNT(*)
		 FROM agent_events WHERE kind = ? AND tool = ? AND %s
		 GROUP BY purpose ORDER BY COUNT(*) DESC, purpose`

// AgentCommandPurposes splits the window's execute_command events by what
// each was for, most frequent first
// (docs/capabilities/sessions-and-memory.md#a-command-is-recorded-by-what-it-was-for).
func (db *DB) AgentCommandPurposes(since time.Time) ([]AgentCommandPurpose, error) {
	return db.agentCommandPurposes(observeEventWindow, observeCutoff(since))
}

func (db *DB) agentCommandPurposes(scope string, args ...any) ([]AgentCommandPurpose, error) {
	return queryScoped(db, scanFields(func(p *AgentCommandPurpose) []any {
		return []any{&p.Purpose, &p.Count}
	}), agentCommandPurposesQuery, scope, []any{AgentEventTool, tools.ExecCommandName}, args)
}

// AgentFirstWrite is how much looking one session did before it changed
// anything: the calls it spent on reads, listings, searches, globs and the
// language server before its first write, and whether it ever wrote at all.
//
// Wrote is the qualification the count cannot be read without. A session
// that never wrote has no such figure — nothing bounds the looking — so it
// is counted beside the rest rather than folded in at zero, which would read
// as a session that found its place immediately.
//
// Calls and not rounds, which is the one place this differs from every other
// per-turn figure in the file. The round a call was made in is optional in
// the record: a surface that keeps no accounting writes the zero position,
// and every row written before the headless run started counting its rounds
// holds one. A round count over those rows is 1 for a session that read
// forty files, which understates exactly the sessions this reading exists to
// find; the tool name is on every row the record has ever held.
// See docs/capabilities/sessions-and-memory.md#how-much-looking-comes-before-the-first-write.
type AgentFirstWrite struct {
	SessionID int64
	Searches  int
	Wrote     bool
}

// The first write is found by row id rather than by time or position: ids
// are the order the events were appended in, and two calls in one round
// share a timestamp to the millisecond often enough to matter.
const agentFirstWritesQuery = `WITH calls AS (
		   SELECT session_id, id, tool FROM agent_events WHERE kind = ? AND %s
		 ), first_write AS (
		   SELECT session_id, MIN(id) AS id FROM calls WHERE tool IN (%s) GROUP BY session_id
		 )
		 SELECT c.session_id, MAX(w.id) IS NOT NULL,
		        COUNT(CASE WHEN c.id < w.id AND c.tool IN (%s) THEN 1 END)
		 FROM calls c LEFT JOIN first_write w ON w.session_id = c.session_id
		 GROUP BY c.session_id ORDER BY c.session_id`

// AgentFirstWrites reads that for every session in the window that called a
// tool at all. A session that called none is absent rather than present at
// zero: it is a session with nothing to say about looking, not one that did
// none.
func (db *DB) AgentFirstWrites(since time.Time) ([]AgentFirstWrite, error) {
	return db.agentFirstWrites(observeEventsOfWindowSessions, observeCutoff(since))
}

// AgentSessionFirstWrite is the same reading for one session, reported false
// when that session called no tool. It goes through the same query as the
// window and the cohort do, so the figure on a session's own page and the
// figure it contributes to a rate cannot come to be counted differently.
func (db *DB) AgentSessionFirstWrite(id int64) (AgentFirstWrite, bool, error) {
	rows, err := db.agentFirstWrites(observeEventSession, id)
	if err != nil || len(rows) == 0 {
		return AgentFirstWrite{}, false, err
	}
	return rows[0], true, nil
}

func (db *DB) agentFirstWrites(scope string, args ...any) ([]AgentFirstWrite, error) {
	writes, searches := observe.WriteToolNames(), observe.SearchToolNames()
	query := fmt.Sprintf(agentFirstWritesQuery, scope, sqlPlaceholders(len(writes)), sqlPlaceholders(len(searches)))
	// The order the placeholders are bound in is the order they appear in
	// the text, which is the scope's, then the writes in the second common
	// table expression, then the searches in the SELECT.
	all := append([]any{AgentEventTool}, args...)
	all = append(all, sqlStrings(writes)...)
	all = append(all, sqlStrings(searches)...)

	return queryRows(db, scanFields(func(f *AgentFirstWrite) []any {
		return []any{&f.SessionID, &f.Wrote, &f.Searches}
	}), query, all...)
}

// sqlPlaceholders is a bind list of n parameters, for an IN whose length is
// decided by a list in the code rather than by the caller.
func sqlPlaceholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

// sqlStrings widens a list of names into query arguments, which the driver
// takes one at a time rather than as a slice.
func sqlStrings(names []string) []any {
	args := make([]any, len(names))
	for i, n := range names {
		args[i] = n
	}
	return args
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
	return queryScoped(db, scanFields(func(d *AgentDecisionCount) []any {
		return []any{&d.Decision, &d.Reason, &d.Count}
	}), agentDecisionsQuery, scope, []any{AgentEventDecision}, args)
}

// AgentOverturns is how often a person answering the classifier's no said
// yes: the judged denials a person answered, and how many of those answers
// allowed the call.
//
// It is the figure a change to the classifier is made to move. Both halves
// were already decision rows — the classifier's verdict filed as the verdict
// it was, and the person's answer filed as a row of its own when the card is
// answered — and nothing joined them, so the rate was something a person
// read off two adjacent rows one session at a time.
type AgentOverturns struct {
	// Judged is the classifier's denials a person answered. A denial nobody
	// was asked about — an unattended run's, which stands as a refusal — has
	// no answer and is left out: counted, it would read as a person agreeing
	// with a verdict nobody put to them.
	Judged int
	// Overturned is how many of those answers were an allow.
	Overturned int
}

// AgentOverturns pairs every judged denial in the window with the person's
// answer to it.
func (db *DB) AgentOverturns(since time.Time) (AgentOverturns, error) {
	return db.agentOverturns(observeEventWindow, observeCutoff(since))
}

// The answer is the next decision row of the same session, turn and round,
// found by row id the way agentInterventionOutcomesQuery finds its reading.
// A decision row names no call, and it needs none: the card carrying the
// verdict holds the approval queue until it is answered, so no other call's
// decision is written between the two. The next row is taken as the answer
// only where it is a person's allow or deny — an amended line put back on
// the card is not an answer yet, and a hard refusal followed by the next
// call's policy decision is no pair at all.
//
// The person's codes are `user` and every `user-` code beside it, since a
// grant taken from the card and a batch answer are the person saying yes as
// well. They are spelled here rather than imported, as the signal codes are:
// this reads rows written by every build that ever wrote one.
const agentOverturnsQuery = `WITH judged AS (
		   SELECT id, session_id, kind, turn, round FROM agent_events
		   WHERE kind = ? AND outcome = 'deny' AND reason = 'classifier' AND %s
		 )
		 SELECT COUNT(*), COALESCE(SUM(a.outcome = 'allow'), 0)
		 FROM judged j
		 JOIN agent_events a ON a.id = (
		   SELECT MIN(x.id) FROM agent_events x
		   WHERE x.session_id = j.session_id AND x.kind = j.kind
		     AND x.turn = j.turn AND x.round = j.round AND x.id > j.id)
		 WHERE a.outcome IN ('allow', 'deny') AND (a.reason = 'user' OR a.reason LIKE 'user-%%')`

func (db *DB) agentOverturns(scope string, args ...any) (AgentOverturns, error) {
	var o AgentOverturns
	err := db.sql.QueryRow(fmt.Sprintf(agentOverturnsQuery, scope),
		append([]any{AgentEventDecision}, args...)...).Scan(&o.Judged, &o.Overturned)
	return o, err
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
	return queryScoped(db, scanFields(func(t *AgentTurnOutcome) []any {
		return []any{&t.Outcome, &t.Count, &t.AvgRounds, &t.MaxRounds, &t.AvgDurationMs}
	}), agentTurnsQuery, scope, []any{AgentEventTurn}, args)
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
	return queryScoped(db, scanFields(func(s *AgentSignalCount) []any {
		return []any{&s.Signal, &s.Reason, &s.Count}
	}), agentSignalsQuery, scope, []any{AgentEventSignal}, args)
}

// AgentInterventionOutcome is one kind of interruption and what the next
// reading of that session made of the run afterwards: how many times the
// pair happened, and how many rounds apart the two were.
//
// It is the one reading in the record that says whether the interruption
// machinery works. Every intervention is already recorded with its kind and
// every reading with its state, and nothing joined the two — so a threshold
// could be changed and the record would say the same number of steers went
// out either way, with no way to ask whether any of them landed.
type AgentInterventionOutcome struct {
	// Kind is the intervention's own qualifier: "steer", "check-in",
	// "enough" or "stale".
	Kind string
	// Reading is the state the next reading came back with, or "none" where
	// the session took no further reading — a turn that ended on the
	// interruption, or one whose summariser was off.
	Reading string
	Count   int
	// AvgRounds is how far past the interruption that reading was taken. A
	// steer answered two rounds later and one answered fifteen rounds later
	// are not the same evidence about the interval.
	AvgRounds float64
}

// AgentInterventionOutcomes pairs every interruption in the window with the
// reading that followed it, most frequent first.
//
// The denominator is the interruptions themselves: the rows for one kind add
// up to every interruption of that kind, so "steer → on-target" against the
// steers in total is the rate a person changing the drift thresholds is
// asking for. That is why a reading that never came is kept as its own row
// rather than dropped — dropped, it would quietly shrink the denominator and
// make the machinery look better the more often it interrupted a turn that
// ended before anything could read it again.
func (db *DB) AgentInterventionOutcomes(since time.Time) ([]AgentInterventionOutcome, error) {
	return db.agentInterventionOutcomes(observeEventWindow, observeCutoff(since))
}

// The next reading is the next one written in the same turn of the same
// session. The turn is the bound because that is what a reading is about: a
// turn interrupted near its end and never read again was not answered by the
// first reading of the next turn, which is judging different work against a
// different instruction, and the rounds between them are not a distance at
// all — the counter goes back to zero at every turn start, so the
// subtraction would be a small or negative number standing where a wait
// should be. An interruption with no reading after it in its own turn is
// reported as one, which is the fact.
//
// Within the turn the next row is found by row id rather than by round: the
// rows are written in the order they happened, which is what the id says and
// what the timestamp — a millisecond stamp two events can share — does not.
//
// The signal codes are spelled here rather than imported, the way the gate
// verdicts' are: this reads rows written by every build that ever wrote one,
// and their spelling is fixed by history rather than by what this build
// happens to write.
const agentInterventionOutcomesQuery = `WITH intervened AS (
		   SELECT id, session_id, kind, turn, round, reason FROM agent_events
		   WHERE kind = ? AND outcome = 'intervened' AND %s
		 )
		 SELECT i.reason, COALESCE(s.reason, 'none'), COUNT(*), COALESCE(AVG(s.round - i.round), 0)
		 FROM intervened i
		 LEFT JOIN agent_events s ON s.id = (
		   SELECT MIN(x.id) FROM agent_events x
		   WHERE x.session_id = i.session_id AND x.kind = i.kind
		     AND x.turn = i.turn AND x.outcome = 'summary' AND x.id > i.id)
		 GROUP BY i.reason, s.reason ORDER BY COUNT(*) DESC, i.reason`

func (db *DB) agentInterventionOutcomes(scope string, args ...any) ([]AgentInterventionOutcome, error) {
	return queryScoped(db, scanFields(func(o *AgentInterventionOutcome) []any {
		return []any{&o.Kind, &o.Reading, &o.Count, &o.AvgRounds}
	}), agentInterventionOutcomesQuery, scope, []any{AgentEventSignal}, args)
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
	return queryScoped(db, scanFields(func(g *AgentGateVerdict) []any {
		return []any{&g.Suite, &g.Verdict, &g.Count}
	}), agentGateVerdictsQuery, scope, []any{AgentEventSignal}, args)
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
	return queryScoped(db, scanFields(func(o *AgentSessionOutcome) []any {
		return []any{&o.Outcome, &o.Count}
	}), agentSessionOutcomesQuery, scope, nil, args)
}
