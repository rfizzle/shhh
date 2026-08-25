package chat

// Persisted model defaults (S-086): `/model <name>` switches the session,
// `/model default <name>` writes provider.model to the config file so new
// sessions start there, and `/model agents <name>` sets the model sub-agents
// run on (agents.model). The chat package never touches the config file
// itself — the CLI installs a writer, and a session without one says so
// rather than pretending the setting stuck.

import "fmt"

// modelUsage is the one-line usage shown by /model and /help.
const modelUsage = "Usage: /model <name> · /model default [name] · /model agents [name|inherit]"

// DefaultsWriter persists one config key/value ("provider.model",
// "agents.model") to the user's config file.
type DefaultsWriter func(key, value string) error

// Defaults describes the session's persisted model defaults and how to change
// them; Write is nil when the session cannot persist config.
type Defaults struct {
	// Model is the configured default session model (provider.model).
	Model string
	// AgentModel is the configured sub-agent model (agents.model); empty or
	// "inherit" means children follow the session model.
	AgentModel string
	Write      DefaultsWriter
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
		switch {
		case current == "":
			return fmt.Sprintf("%s: not set (%s).\n%s", label, m.defaultFallback(which), modelUsage)
		default:
			return fmt.Sprintf("%s: %s\n%s", label, current, modelUsage)
		}
	}
	if len(rest) > 1 {
		return "Model names cannot contain spaces. " + modelUsage
	}
	if m.defaults.Write == nil {
		return "This session cannot write the config file, so the default was not saved."
	}
	name := rest[0]
	if err := m.defaults.Write(key, name); err != nil {
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
	if name == m.modelName {
		return fmt.Sprintf("Default model set to %s (this session already uses it).", name)
	}
	return fmt.Sprintf("Default model set to %s for new sessions; this session stays on %s (/model %s switches it now).", name, m.modelName, name)
}

// defaultFallback explains what an unset default falls back to.
func (m Model) defaultFallback(which string) string {
	if which == "agents" {
		return "sub-agents follow the session model"
	}
	return "new sessions use the provider's built-in default"
}
