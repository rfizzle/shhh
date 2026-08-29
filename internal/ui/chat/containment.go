package chat

import "context"

// Containment is the process-containment setup for assistant commands.
// When Run is set, approved and waved-through execute_command calls run
// through it (the sandbox-wrapped runner) instead of the plain runner; /run —
// the user's own command — always stays on the plain runner. Status is the
// one-line state shown on the exec confirm prompt, and Report is the full
// doctor text behind /sandbox.
type Containment struct {
	Run func(context.Context, string) (string, int)
	// TailRun is Run with live per-line output reporting for the activity
	// feed's running row; nil runs contained commands with no tail.
	TailRun func(ctx context.Context, command string, onLine func(string)) (string, int)
	Status  string
	Report  string
	// Mechanism, Profile and Network are the same state in the pieces the
	// approval card's blast-radius block needs: the chip on the title
	// rail, and the honest answer to "is the network open". An empty
	// Mechanism means nothing is containing assistant commands, and Detail is
	// then why — the text /sandbox doctor expands on.
	Mechanism string
	Profile   string
	Network   bool
	Detail    string
	// Manage handles the /sandbox subcommands (doctor, list, status,
	// destroy, prune) for container sandboxes and returns the text to
	// show. Nil means container sandbox management is not wired up.
	Manage func(args []string) string
}

// WithContainment wires the containment setup into the session.
func (m Model) WithContainment(c Containment) Model {
	m.containment = c
	return m
}
