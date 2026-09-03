package chat

// Processes wires the long-running process supervisor into the chat
// TUI. Manage backs the /ps slash command (the session's process list); its
// presence also marks the process tool as registered, which routes start
// actions through the approval queue while status/read/input/stop auto-run.
type Processes struct {
	Manage func(args []string) string
	// Contained names the containment mechanism a start actually runs
	// under, empty when nothing contains one. It is the supervisor's own
	// answer and not the runner's: the two command paths are wired from one
	// policy, but a card that read the ordinary path's state would be
	// describing something other than the process about to start. Nil says
	// nothing rather than guessing.
	Contained func() string
}

// WithProcesses enables the /ps command and process-start approval gating.
func (m Model) WithProcesses(p Processes) Model {
	m.processes = p
	return m
}
