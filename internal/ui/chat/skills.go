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
	s, ok := m.wiring.Skills.Find(name)
	if !ok {
		return m.surfaceNotice("no skill named " + name + ". /skills lists this session's skills")
	}
	content, err := skill.UserMessage(s, task)
	if err != nil {
		return m.surfaceNotice("could not read skill " + name + ": " + err.Error())
	}
	m.signal(observe.SignalSkill, s.Name)
	shown := "/skill " + name
	if task != "" {
		shown += " " + task
	}
	if m.working() || m.decisionUngated() {
		m.steering = append(m.steering, steeringItem{text: content, id: m.queue.next(), kind: queuedSkill})
		m.syncViewport()
		return m.surfaceNotice("skill " + name + " queued for the next round")
	}
	return m.sendUserMessageAs(content, shown)
}

// skillArgs completes /skill's first argument with the catalog.
func skillArgs(m *Model) []argOption {
	if m.wiring.Skills == nil {
		return nil
	}
	out := make([]argOption, 0, m.wiring.Skills.Len())
	for _, s := range m.wiring.Skills.Skills {
		desc := []rune(s.Description)
		if len(desc) > 60 {
			desc = append(desc[:57], []rune("...")...)
		}
		out = append(out, argOption{value: s.Name, desc: string(desc)})
	}
	return out
}
