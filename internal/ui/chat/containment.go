package chat

import "context"

// Containment is the S-062 process-containment setup for assistant commands.
// When Run is set, approved and waved-through execute_command calls run
// through it (the sandbox-wrapped runner) instead of the plain runner; /run —
// the user's own command — always stays on the plain runner. Status is the
// one-line state shown on the exec confirm prompt, and Report is the full
// doctor text behind /sandbox.
type Containment struct {
	Run    func(context.Context, string) (string, int)
	Status string
	Report string
}

// WithContainment wires the containment setup into the session.
func (m Model) WithContainment(c Containment) Model {
	m.containment = c
	return m
}
