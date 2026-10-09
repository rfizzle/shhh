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
	// Read returns the opening bytes of a stored entry, and false for a
	// session with no store or an entry it no longer holds. It is what the
	// sources screen previews a page with and what [enter] opens whole —
	// the ledger outlives a purge, so a row whose entry has gone still
	// draws, without its page.
	Read func(id string, limit int) (string, bool)
	// Keep stores a result the window trim is about to elide and returns the
	// id that pages it back; false is a store that could not take it, and the
	// trim goes ahead with the bare placeholder. Nil makes elision permanent,
	// which is what a session with no store gets.
	Keep func(tool, content string) (string, bool)
}

// reduceResult applies the reduction pipeline to a tool result, or returns it
// unchanged when no pipeline is wired.
func (m Model) reduceResult(tool, result string) string {
	if m.wiring.Evidence.Reduce == nil {
		return result
	}
	return m.wiring.Evidence.Reduce(tool, result)
}
