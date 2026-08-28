package chat

// Reasoning effort in the session (S-139): `ctrl+r` walks the four levels,
// `/reasoning [level]` says or sets one, `/reasoning default [level]` writes
// it to the config file, and the cockpit states which one is live beside the
// model.
//
// It is the model's sibling and behaves like one. Both are things a session
// picks and keeps, both are read by the stream closure rather than by this
// package, and both are written back through a hook the CLI installs — a
// session without one still applies what it is told and says the change is
// for this session only, rather than pretending it was saved.
//
// The key is a chord for the reason every live key here is: no sentence can
// produce it, so it can be pressed with a half-typed draft in the box and the
// draft is not touched. `r` alone would be a letter.

import (
	"fmt"

	"github.com/rfizzle/shhh/internal/provider"
)

// reasoningKey cycles the level. Ctrl+R is free of the textarea's own
// bindings and free of the terminal's, and it is the mnemonic.
const reasoningKey = "ctrl+r"

// reasoningUsage is the one-line usage shown by /reasoning and /help.
const reasoningUsage = "Usage: /reasoning <off|low|medium|high> · /reasoning default [level] (Ctrl+R cycles)"

// WithReasoning installs the session's reasoning level and the hook that
// makes a change reach the next request. fn may be nil — the level is then
// display-only, which is what a session with no switchable provider has.
func (m Model) WithReasoning(effort provider.Effort, fn func(provider.Effort)) Model {
	m.effort = effort
	m.effortFn = fn
	return m
}

// WithReasoningDefault records the persisted level and whatever outranks it,
// so `/reasoning default` can report both (S-136's rule, applied to the
// second setting that has the same problem).
func (m Model) WithReasoningDefault(level, outranked string) Model {
	m.effortDefault = level
	m.effortOutranked = outranked
	return m
}

// cycleReasoning is ctrl+r: the next level, applied and stated. The statement
// is a notice rather than a transcript entry — it is a setting, not something
// that happened in the conversation, and the cockpit is already showing it.
func (m Model) cycleReasoning() (Model, string) {
	if m.effortFn == nil {
		return m, "This session cannot change the reasoning level."
	}
	next := provider.NextEffort(m.effort)
	m.applyEffort(next)
	return m, m.reasoningNote(next)
}

// applyEffort sets the level and pushes it at the session.
func (m *Model) applyEffort(e provider.Effort) {
	m.effort = e
	if m.effortFn != nil {
		m.effortFn(e)
	}
}

// reasoningNote is what a change says: the level, what it means, and — while
// a turn is running — when it starts applying. A setting that takes effect
// one request later has to say so, or the next answer looks like it ignored
// the key.
func (m Model) reasoningNote(e provider.Effort) string {
	note := fmt.Sprintf("Reasoning: %s — %s.", e, e.Describe())
	if m.turnState() != stateInput {
		note += " It applies from the next model request; the one in flight keeps the level it opened on."
	}
	return note
}

// reasoningCommand handles /reasoning and its arguments.
func (m *Model) reasoningCommand(parts []string) string {
	if len(parts) == 0 {
		return fmt.Sprintf("Reasoning: %s — %s.\n%s", m.effort, m.effort.Describe(), reasoningUsage)
	}
	if parts[0] == "default" {
		return m.setReasoningDefault(parts[1:])
	}
	if len(parts) > 1 {
		return "One level at a time. " + reasoningUsage
	}
	e, err := provider.ParseEffort(parts[0])
	if err != nil {
		return "Error: " + err.Error()
	}
	if m.effortFn == nil {
		return "This session cannot change the reasoning level."
	}
	if e == m.effort {
		return fmt.Sprintf("Already reasoning %s.", e)
	}
	m.applyEffort(e)
	return m.reasoningNote(e)
}

// setReasoningDefault handles `/reasoning default [level]`: with no level it
// reports what is persisted, with one it writes provider.reasoning.
func (m *Model) setReasoningDefault(rest []string) string {
	if len(rest) == 0 {
		current := m.effortDefault
		if current == "" {
			current = "not set (new sessions start with reasoning off)"
		}
		note := "Default reasoning: " + current
		if m.effortOutranked != "" {
			note += fmt.Sprintf("\nOverruled: %s, which outranks the config file.", m.effortOutranked)
		}
		return note + "\n" + reasoningUsage
	}
	if len(rest) > 1 {
		return "One level at a time. " + reasoningUsage
	}
	e, err := provider.ParseEffort(rest[0])
	if err != nil {
		return "Error: " + err.Error()
	}
	if m.writeConfig == nil {
		return "This session cannot write the config file, so the default was not saved."
	}
	if err := m.writeConfig("provider.reasoning", e.String()); err != nil {
		return "Error: could not save the default: " + err.Error()
	}
	m.effortDefault = e.String()
	note := fmt.Sprintf("Default reasoning set to %s for new sessions", e)
	if e == m.effort {
		note += " (this session already reasons " + e.String() + ")."
	} else {
		note += fmt.Sprintf("; this session stays on %s (/reasoning %s changes it now).", m.effort, e)
	}
	if m.effortOutranked != "" {
		note += fmt.Sprintf("\nIt will not take effect while %s — that outranks the config file.", m.effortOutranked)
	}
	return note
}

// reasoningSegment is what the cockpit shows (§8a): the level, named, or ""
// when nothing is being asked for. Off is not drawn — a rail states what the
// session is doing, and a session doing nothing extra has nothing to say.
func (m Model) reasoningSegment() string {
	if !m.effort.On() {
		return ""
	}
	return "think " + m.effort.String()
}

// reasoningArgs are the levels /reasoning offers, plus the sub-command that
// persists one.
func reasoningArgs(m *Model) []argOption {
	out := make([]argOption, 0, len(provider.EffortCycle())+1)
	for _, e := range provider.EffortCycle() {
		desc := e.Describe()
		if e == m.effort {
			desc = "current — " + desc
		}
		out = append(out, argOption{value: e.String(), desc: desc})
	}
	return append(out, argOption{value: "default", desc: "Show or persist the level new sessions start on"})
}

// reasoningLevelArgs are the levels alone, for the position after "default".
func reasoningLevelArgs() []argOption {
	out := make([]argOption, 0, len(provider.EffortCycle()))
	for _, e := range provider.EffortCycle() {
		out = append(out, argOption{value: e.String(), desc: e.Describe()})
	}
	return out
}
