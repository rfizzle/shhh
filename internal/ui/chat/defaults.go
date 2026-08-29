package chat

// Persisted model defaults: `/model <name>` switches the session,
// `/model default <name>` writes provider.model to the config file so new
// sessions start there, and `/model agents <name>` sets the model sub-agents
// run on (agents.model). The chat package never touches the config file
// itself — the CLI installs a writer, and a session without one says so
// rather than pretending the setting stuck.

import "fmt"

// modelUsage is the one-line usage shown by /model and /help.
const modelUsage = "Usage: /model <name> · /model default [name] · /model agents [name|inherit]"

// ConfigWriter persists one config key/value to the user's config file. It
// belongs to the session rather than to any one setting: the model defaults
// were the first thing to need it and the mouse toggle is the second,
// and a second copy of the same function would be a second thing to install.
type ConfigWriter func(key, value string) error

// WithConfigWriter installs the writer that makes a setting stick. A session
// without one still applies what it is told; it just says the change is for
// this session only rather than pretending it was saved.
func (m Model) WithConfigWriter(w ConfigWriter) Model {
	m.writeConfig = w
	return m
}

// Defaults describes the session's persisted model defaults.
type Defaults struct {
	// Model is the configured default session model (provider.model).
	Model string
	// AgentModel is the configured sub-agent model (agents.model); empty or
	// "inherit" means children follow the session model.
	AgentModel string
	// Outranked names what beats provider.model when a new session resolves
	// one — an env var, or a flag on the command line. It is empty when
	// nothing does. Writing a default that something else overrules is the
	// one way this surface can succeed and still not work, so the row that
	// writes it has to say so.
	Outranked string
}

// WithDefaults installs the persisted-defaults surface.
func (m Model) WithDefaults(d Defaults) Model {
	m.defaults = d
	return m
}

// setModelDefault handles `/model default [name]` and `/model agents [name]`.
// With no name it reports the current setting; with one it persists it.
func (m *Model) setModelDefault(which string, rest []string) string {
	key, label := "provider.model", "Default model"
	current := m.defaults.Model
	if which == "agents" {
		key, label = "agents.model", "Sub-agent model"
		current = m.defaults.AgentModel
	}
	if len(rest) == 0 {
		var note string
		switch {
		case current == "":
			note = fmt.Sprintf("%s: not set (%s).", label, m.defaultFallback(which))
		default:
			note = fmt.Sprintf("%s: %s", label, current)
		}
		// Reporting a setting that is being overruled without saying so is
		// the same lie as writing one, told more quietly.
		if which == "default" && m.defaults.Outranked != "" {
			note += fmt.Sprintf("\nOverruled: %s, which outranks the config file.", m.defaults.Outranked)
		}
		return note + "\n" + modelUsage
	}
	if len(rest) > 1 {
		return "Model names cannot contain spaces. " + modelUsage
	}
	if m.writeConfig == nil {
		return "This session cannot write the config file, so the default was not saved."
	}
	name := rest[0]
	if err := m.writeConfig(key, name); err != nil {
		return "Error: could not save the default: " + err.Error()
	}
	if which == "agents" {
		m.defaults.AgentModel = name
		if name == "inherit" {
			return "Sub-agents now follow the session model. Agents already running keep the model they started on."
		}
		return fmt.Sprintf("Sub-agents now run on %s. Agents already running keep the model they started on.", name)
	}
	m.defaults.Model = name
	var note string
	switch {
	case name == m.modelName:
		note = fmt.Sprintf("Default model set to %s (this session already uses it).", name)
	default:
		note = fmt.Sprintf("Default model set to %s for new sessions; this session stays on %s (/model %s switches it now).", name, m.modelName, name)
	}
	// A default that something else overrules was written and will still be
	// ignored, which is the one outcome a success message must not claim.
	if m.defaults.Outranked != "" {
		note += fmt.Sprintf("\nIt will not take effect while %s — that outranks the config file.", m.defaults.Outranked)
	}
	return note
}

// defaultFallback explains what an unset default falls back to.
func (m Model) defaultFallback(which string) string {
	if which == "agents" {
		return "sub-agents follow the session model"
	}
	return "new sessions use the provider's built-in default"
}
