package chat

// Evidence wires the tool-output reduction pipeline and evidence store
// (S-064) into the chat TUI. The auto-run executor is wrapped by the caller,
// so only the approval-gated result paths (exec output, mutating tools) go
// through Reduce here; the transcript records the reduced text the model got,
// keeping the view display-consistent.
type Evidence struct {
	// Reduce runs one tool result through the reduction pipeline; nil leaves
	// results untouched.
	Reduce func(tool, result string) string
	// Manage backs the /evidence slash command (status, purge).
	Manage func(args []string) string
}

// WithEvidence enables tool-output reduction and the /evidence command.
func (m Model) WithEvidence(e Evidence) Model {
	m.evidence = e
	return m
}

// reduceResult applies the reduction pipeline to a tool result, or returns it
// unchanged when no pipeline is wired.
func (m Model) reduceResult(tool, result string) string {
	if m.evidence.Reduce == nil {
		return result
	}
	return m.evidence.Reduce(tool, result)
}
