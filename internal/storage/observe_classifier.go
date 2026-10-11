package storage

import (
	"database/sql"
	"fmt"
	"slices"
	"time"
)

// AgentClassifierBackend is one classifier backend's verdicts over a window,
// read from the decision rows the classifier wrote and the session rows that
// name the backend it ran on.
type AgentClassifierBackend struct {
	// Backend is the session's classifier_backend as stamped; empty for a
	// session recorded before the backend was.
	Backend string
	// Verdicts counts the classifier's allows and denies. A classifier that
	// failed closed reached no verdict and is not among them.
	Verdicts, Allows, Denies int
	// Timed is how many verdicts carry a duration; MedianMs and WorstMs are
	// over those alone.
	Timed             int
	MedianMs, WorstMs int64
	// Cost is the classifier origin's spend over the backend's sessions, as
	// the ledger billed it.
	Cost float64
	// Overturns pairs its denials with the person's answer at the card.
	Overturns AgentOverturns
	// Stopped is how many of its allows the person stopped while the call
	// ran: a decision row under reason user-stop right after the allow.
	Stopped int
}

// agentStoppedQuery pairs a classifier allow with the next decision row of
// the same session, turn and round, the way agentOverturnsQuery pairs a deny.
// The code is spelled here rather than imported for the same reason.
const agentStoppedQuery = `WITH allowed AS (
		   SELECT id, session_id, kind, turn, round FROM agent_events
		   WHERE kind = ? AND outcome = 'allow' AND reason = 'classifier' AND %s
		 )
		 SELECT COUNT(*)
		 FROM allowed j
		 JOIN agent_events a ON a.id = (
		   SELECT MIN(x.id) FROM agent_events x
		   WHERE x.session_id = j.session_id AND x.kind = j.kind
		     AND x.turn = j.turn AND x.round = j.round AND x.id > j.id)
		 WHERE a.outcome = 'deny' AND a.reason = 'user-stop'`

// SetAgentClassifierCost records what the session's classifier has spent so
// far (idempotent: a running total, not a delta).
func (db *DB) SetAgentClassifierCost(id int64, cost float64) error {
	return db.execRetry(`UPDATE agent_sessions SET classifier_cost = ? WHERE id = ?`, cost, id)
}

// AgentClassifierBackends reads the classifier's verdicts since the cutoff,
// one entry per backend that reached any, the most verdicts first. Sessions
// are scoped whole, by their start, as a cohort is.
func (db *DB) AgentClassifierBackends(since time.Time) ([]AgentClassifierBackend, error) {
	cutoff := observeCutoff(since)
	type verdict struct {
		backend, outcome string
		ms               sql.NullInt64
	}
	rows, err := queryRows(db, scanFields(func(v *verdict) []any {
		return []any{&v.backend, &v.outcome, &v.ms}
	}), `SELECT COALESCE(s.classifier_backend, ''), e.outcome, e.duration_ms
		 FROM agent_events e JOIN agent_sessions s ON s.id = e.session_id
		 WHERE e.kind = ? AND e.reason = 'classifier' AND e.outcome IN ('allow', 'deny') AND s.started_at >= ?
		 ORDER BY e.id`, AgentEventDecision, cutoff)
	if err != nil {
		return nil, err
	}
	byBackend := map[string]*AgentClassifierBackend{}
	durations := map[string][]int64{}
	var order []string
	for _, v := range rows {
		b := byBackend[v.backend]
		if b == nil {
			b = &AgentClassifierBackend{Backend: v.backend}
			byBackend[v.backend] = b
			order = append(order, v.backend)
		}
		b.Verdicts++
		if v.outcome == "allow" {
			b.Allows++
		} else {
			b.Denies++
		}
		if v.ms.Valid {
			durations[v.backend] = append(durations[v.backend], v.ms.Int64)
		}
	}
	type spend struct {
		backend string
		cost    float64
	}
	spends, err := queryRows(db, scanFields(func(s *spend) []any { return []any{&s.backend, &s.cost} }),
		`SELECT COALESCE(classifier_backend, ''), COALESCE(SUM(classifier_cost), 0)
		 FROM agent_sessions WHERE started_at >= ? GROUP BY 1`, cutoff)
	if err != nil {
		return nil, err
	}
	for _, s := range spends {
		if b := byBackend[s.backend]; b != nil {
			b.Cost = s.cost
		}
	}
	out := make([]AgentClassifierBackend, 0, len(order))
	for _, name := range order {
		b := byBackend[name]
		d := durations[name]
		slices.Sort(d)
		if n := len(d); n > 0 {
			b.Timed, b.WorstMs = n, d[n-1]
			b.MedianMs = d[n/2]
			if n%2 == 0 {
				b.MedianMs = (d[n/2-1] + d[n/2]) / 2
			}
		}
		scope := `session_id IN (SELECT id FROM agent_sessions WHERE started_at >= ? AND COALESCE(classifier_backend, '') = ?)`
		if b.Overturns, err = db.agentOverturns(scope, cutoff, name); err != nil {
			return nil, err
		}
		if err := db.sql.QueryRow(fmt.Sprintf(agentStoppedQuery, scope), AgentEventDecision, cutoff, name).Scan(&b.Stopped); err != nil {
			return nil, err
		}
		out = append(out, *b)
	}
	slices.SortStableFunc(out, func(a, b AgentClassifierBackend) int { return b.Verdicts - a.Verdicts })
	return out, nil
}
