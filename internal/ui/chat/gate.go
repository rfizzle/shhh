package chat

import (
	"context"

	"github.com/rfizzle/shhh/internal/quality"
)

// Gate wires the quality gate into the chat TUI. Manage backs the
// /gate slash command: "run [suite]" starts a suite in the background,
// "result" reports the latest verdict (marked stale when the tree changed).
type Gate struct {
	Manage func(args []string) string
	// Run runs a suite to completion and returns its result; the backlog
	// runner's verify stage uses it. Nil when the project has no gate.
	Run func(ctx context.Context, suite string) (*quality.Result, error)
}

// WithGate enables the /gate command.
func (m Model) WithGate(g Gate) Model {
	m.gate = g
	return m
}
