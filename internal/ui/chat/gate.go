package chat

// Gate wires the quality gate (S-067) into the chat TUI. Manage backs the
// /gate slash command: "run [suite]" starts a suite in the background,
// "result" reports the latest verdict (marked stale when the tree changed).
type Gate struct {
	Manage func(args []string) string
}

// WithGate enables the /gate command.
func (m Model) WithGate(g Gate) Model {
	m.gate = g
	return m
}
