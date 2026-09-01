package chat

// Explicit skill activation. The model activates a skill by calling the
// skill tool when a task matches; the user activates one by naming it, and
// what the model receives is the same content either way — as a user
// message here rather than a tool result, because the user said it.
// See docs/capabilities/skills.md#the-user-can-activate-one-too.

import (
	tea "charm.land/bubbletea/v2"

	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/skill"
)

// activateSkill sends a skill's content, with task after it, as the next
// user message; while the agent works it queues as steering instead. The
// transcript shows the command the user typed, not the content: the body
// is the model's to read, and a screen of someone else's instructions in
// the user's own column is not what they said.
func (m Model) activateSkill(name, task string) (tea.Model, tea.Cmd) {
	s, ok := m.skills.Find(name)
	if !ok {
		return m.surfaceNotice("No skill named " + name + ". /skills lists this session's skills.")
	}
	content, err := skill.UserMessage(s, task)
	if err != nil {
		return m.surfaceNotice("Could not read skill " + name + ": " + err.Error())
	}
	m.signal(observe.SignalSkill, s.Name)
	shown := "/skill " + name
	if task != "" {
		shown += " " + task
	}
	if m.working() || m.decisionUngated() {
		m.steering = append(m.steering, content)
		m.syncViewport()
		return m.surfaceNotice("Skill " + name + " queued for the next round.")
	}
	return m.sendUserMessageAs(content, shown)
}

// skillArgs completes /skill's first argument with the catalog.
func skillArgs(m *Model) []argOption {
	if m.skills == nil {
		return nil
	}
	out := make([]argOption, 0, m.skills.Len())
	for _, s := range m.skills.Skills {
		desc := []rune(s.Description)
		if len(desc) > 60 {
			desc = append(desc[:57], []rune("...")...)
		}
		out = append(out, argOption{value: s.Name, desc: string(desc)})
	}
	return out
}
