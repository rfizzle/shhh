package chat

import (
	"time"

	"github.com/rfizzle/shhh/internal/observe"
)

// injectProgressCheckpoint asks for public status only between complete tool
// rounds. An overlay belongs to the reader, so it defers the request rather
// than sending an unseen update behind a card or supporting screen.
func (m *Model) injectProgressCheckpoint() {
	if m.state.isSurface() {
		return
	}
	prompt, ok := m.agent.TakeProgressCheckpoint()
	if !ok {
		return
	}
	m.agent.AppendMachine(prompt)
}

// noteProgressProse records the assistant prose that a tool round led with.
// A checkpoint response is ordinary assistant text — its existing one-line
// step title therefore remains the title for the calls following it — while
// the record keeps only that public status happened, never its content.
func (m *Model) noteProgressProse(text string) {
	if m.agent.NoteProgressProse(text) {
		m.signal(observe.SignalProgress, observe.ProgressCheckpoint)
	}
}

// WithProgressIntervals sets the two public-status clocks for this session.
func (m Model) WithProgressIntervals(calls int, elapsed time.Duration) Model {
	m.agent.SetProgressIntervals(calls, elapsed)
	return m
}
