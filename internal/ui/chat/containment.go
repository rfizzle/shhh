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
	// Required says the session was told to contain the assistant's
	// commands rather than to prefer it, which is what the chip reports: a
	// mechanism that is in force and a mechanism that had to be are
	// different facts about the same session.
	Required bool
	// Refusal is why no assistant command may run at all — a session that
	// requires containment on a host with none. Non-empty is answered
	// before the card is drawn: there is nothing to decide, so the model
	// gets this as the call's result and the reader is not asked to approve
	// something that cannot happen.
	// See docs/capabilities/containment.md#containment-can-be-required.
	Refusal string
	// Manage handles the /sandbox subcommands (doctor, list, status,
	// destroy, prune) for container sandboxes and returns the text to
	// show. Nil means container sandbox management is not wired up.
	Manage func(args []string) string
	// Wrap is the argv one command line runs as under this session's
	// containment, for the callers that have to build the process
	// themselves — a hook, which is a command with a payload on its stdin
	// and no way through the runner that captures one. Nil is a session
	// running its commands bare, and such a caller then runs bare too: it is
	// contained exactly as much as the assistant's own commands are, which
	// is the whole rule.
	// See docs/capabilities/hooks.md#a-hook-is-a-command-like-any-other.
	Wrap func(command string) ([]string, error)
}

// containmentRefusal is the refusal an action gets before it is drawn, or ""
// when this session runs it. It is asked of the actions that run a command —
// execute_command and a process start, which is the whole of what the
// requirement is about; /run carries no request here and is never refused.
func (m Model) containmentRefusal(req *approvalRequest) string {
	if m.containment.Refusal == "" || req == nil || req.command == "" {
		return ""
	}
	return m.containment.Refusal
}

// containmentStatus is the containment line `/status` prints: what is
// containing the assistant's commands, in the words the card's chip uses, or
// that nothing is and why. A session with no containment wiring says nothing
// rather than claiming either state.
func (m Model) containmentStatus() string {
	if m.containment.Status == "" {
		return ""
	}
	if m.containment.Mechanism == "" {
		if m.containment.Refusal != "" {
			// The session was told to require one, so "unconfined" is not
			// the whole answer: nothing of the assistant's is going to run.
			return "Containment\nrequired, and none is in force — the assistant's commands are refused\n" +
				uncontainedDetail(m.containment.Detail)
		}
		return "Containment\nunconfined — " + uncontainedDetail(m.containment.Detail)
	}
	return "Containment\n" + m.containmentWords(m.containment.Mechanism)
}

// containmentWords is the mechanism and the profile in one clause, with the
// requirement in front of it where there is one. It is one function so the
// chip on a card and the line `/status` prints cannot come to disagree — and
// it takes the mechanism rather than reading it, because a card names the
// path that will run its own action rather than the one beside it.
func (m Model) containmentWords(mechanism string) string {
	words := mechanism
	if m.containment.Required {
		words = "required · " + words
	}
	if m.containment.Profile != "" {
		words += " · " + m.containment.Profile
	}
	return words
}

// WithContainment wires the containment setup into the session.
func (m Model) WithContainment(c Containment) Model {
	m.containment = c
	return m
}
