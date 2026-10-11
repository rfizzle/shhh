package storage

import (
	"time"
)

// AgentWriter is one child's attempt as the budget saw it: the new tokens it
// took in, the budget it was admitted with and the word it ended on.
type AgentWriter struct {
	ID int64
	// Name is the child's name as the supervisor knew it; empty on a row
	// recorded before names were.
	Name string
	// NewTokens is the sum of the five fields the child's end row prints
	// (inherited, setup, tools, analysis, handoff).
	NewTokens int64
	// Budget is the effective fresh-token cap the child was admitted with.
	Budget int64
	// Reason is the child's end_reason as recorded.
	Reason string
}

// AgentWriters reads the closed child attempts since the cutoff that carry a
// budget, oldest first. A child recorded before the budget was, or one still
// running, has nothing to be set against and is left out. Sessions are scoped
// whole, by their start, as a cohort is.
func (db *DB) AgentWriters(since time.Time) ([]AgentWriter, error) {
	return queryRows(db, scanFields(func(w *AgentWriter) []any {
		return []any{&w.ID, &w.Name, &w.NewTokens, &w.Budget, &w.Reason}
	}), `SELECT id, COALESCE(name, ''),
		        COALESCE(child_tokens_inherited, 0) + COALESCE(child_tokens_setup, 0) + COALESCE(child_tokens_tools, 0)
		          + COALESCE(child_tokens_analysis, 0) + COALESCE(child_tokens_handoff, 0),
		        child_budget, end_reason
		 FROM agent_sessions
		 WHERE started_at >= ? AND parent_id IS NOT NULL AND end_reason IS NOT NULL AND end_reason != ''
		   AND child_budget > 0
		 ORDER BY id`, observeCutoff(since))
}
