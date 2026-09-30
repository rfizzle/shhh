package chat

// Session secrets. /secret manages the vault the CLI owns; the chat model's
// part is the scrub on its agent and telling the model when the set
// changes, since the system prompt that named the secrets was written
// before this one existed. See docs/capabilities/secrets.md.

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/rfizzle/shhh/internal/provider"
)

// Secrets wires the session vault into the chat TUI. The zero value
// disables /secret and scrubs nothing.
type Secrets struct {
	// Manage backs the /secret slash command (list | set | forget). It
	// returns the note for the screen and, when the set of secrets changed,
	// what to tell the model.
	Manage func(args []string) (note, announce string)
	// Scrub is the rewrite every message goes through on its way into the
	// conversation and out to a provider.
	Scrub func(provider.Message) provider.Message
}

// WithSecrets enables /secret and installs the scrub on the agent, so the
// conversation this model saves, shows and replays never holds a value.
// See docs/capabilities/secrets.md#the-value-is-scrubbed-at-every-door.
func (m Model) WithSecrets(s Secrets) Model {
	m.secrets = s
	if s.Scrub != nil {
		m.agent.SetScrub(s.Scrub)
	}
	return m
}

// secretCommand is /secret. A change to the set is told to the model as a
// user message — queued as steering while the agent works, appended to
// the conversation when it is idle — because a secret the model cannot
// name is one it cannot use, and it has no other way to learn the name.
func (m Model) secretCommand(args []string) (tea.Model, tea.Cmd) {
	if m.secrets.Manage == nil {
		return m.surfaceNotice("secrets are unavailable in this session")
	}
	note, announce := m.secrets.Manage(args)
	m.announce(announce)
	return m.surfaceNotice(note)
}

// announce tells the model about a change the system prompt was written
// before: queued as machine steering while the agent works, so it lands at
// the next round boundary, and appended to the conversation when it is idle.
// The prompt is not rewritten instead, because that pays for the cached
// prefix again. Empty text says nothing.
func (m *Model) announce(text string) {
	if text == "" {
		return
	}
	if m.working() || m.decisionUngated() {
		m.steering = append(m.steering, steeringItem{text: text, machine: true, id: m.queue.next(), kind: queuedSession})
	} else {
		m.agent.AppendMachine(text)
	}
}

// secretInput reports whether a submitted line carries a secret value —
// `/secret set NAME=value` — and so must stay out of input recall, where
// an up-arrow would put it back on screen.
func secretInput(text string) bool {
	parts := strings.Fields(text)
	return len(parts) >= 3 && (parts[0] == "/secret" || parts[0] == "/secrets") &&
		(parts[1] == "set" || parts[1] == "add") && strings.Contains(parts[2], "=")
}
