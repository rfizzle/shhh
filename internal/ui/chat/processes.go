package chat

// Processes wires the long-running process supervisor (S-073) into the chat
// TUI. Manage backs the /ps slash command (the session's process list); its
// presence also marks the process tool as registered, which routes start
// actions through the approval queue while status/read/input/stop auto-run.
type Processes struct {
	Manage func(args []string) string
}

// WithProcesses enables the /ps command and process-start approval gating.
func (m Model) WithProcesses(p Processes) Model {
	m.processes = p
	return m
}
