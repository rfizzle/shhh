package chat

// Evidence wires the tool-output reduction pipeline and evidence store
// into the chat TUI. The auto-run executor is wrapped by the caller,
// so only the approval-gated result paths (exec output, mutating tools) go
// through Reduce here; the transcript records the reduced text the model got,
// keeping the view display-consistent.
type Evidence struct {
	// Reduce runs one tool result through the reduction pipeline; nil leaves
	// results untouched.
	Reduce func(tool, result string) string
	// Manage backs the /evidence slash command (status, purge).
	Manage func(args []string) string
	// Keep stores a result the window trim is about to elide and returns the
	// id that pages it back; false is a store that could not take it, and the
	// trim goes ahead with the bare placeholder. Nil makes elision permanent,
	// which is what a session with no store gets.
	Keep func(tool, content string) (string, bool)
}

// WithEvidence enables tool-output reduction, the /evidence command, and the
// recovery of what a window trim elides.
func (m Model) WithEvidence(e Evidence) Model {
	m.evidence = e
	if m.agent != nil {
		m.agent.StoreElided(e.Keep)
	}
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
