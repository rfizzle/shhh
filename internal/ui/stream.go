package ui

import (
	"context"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// Stream messages carry the id of the stream that produced them. The result
// surface can have a command stream and an explanation stream in the same
// session, and a cancelled stream's last message can land after the next one
// has started — without the id it would be read as the new stream's own
// (S-113).
type tokenMsg struct {
	id   int
	text string
}
type doneMsg struct{ id int }
type streamErrMsg struct {
	id  int
	err error
}

// streamSeq hands out stream ids. Streams are created on the UI goroutine,
// one at a time, so a plain counter is enough. It starts above zero so the
// zero-valued StreamModel — the one a restored revision carries — matches
// nothing.
var streamSeq int

type StreamModel struct {
	id        int
	events    <-chan provider.StreamEvent
	cancel    context.CancelFunc
	spinner   spinner.Model
	output    string
	done      bool
	cancelled bool
	err       error
}

func NewStreamModel(events <-chan provider.StreamEvent, cancel context.CancelFunc) StreamModel {
	// The frame set and its cadence belong to components (S-094), so the
	// one-shot UI spins exactly like the chat surface does.
	s := components.NewSpinnerModel()
	streamSeq++
	return StreamModel{
		id:      streamSeq,
		events:  events,
		cancel:  cancel,
		spinner: s,
	}
}

func (m StreamModel) Output() string  { return m.output }
func (m StreamModel) Done() bool      { return m.done }
func (m StreamModel) Cancelled() bool { return m.cancelled }
func (m StreamModel) Err() error      { return m.err }

func (m StreamModel) WithOutput(s string) StreamModel {
	m.output = s
	return m
}

func (m StreamModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.waitForEvent())
}

func (m StreamModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch pressed := msg.String(); {
		case keys.Is(pressed, keys.Screen.Quit):
			if !m.done {
				// A stream still being opened has no cancel yet (S-132); it
				// is the surface's gen counter that discards its answer.
				if m.cancel != nil {
					m.cancel()
				}
				m.cancelled = true
				m.done = true
				m.output = StripFences(m.output)
				return m, nil
			}
		}
		return m, nil
	case tokenMsg:
		if msg.id != m.id {
			return m, nil
		}
		m.output += msg.text
		return m, m.waitForEvent()
	case doneMsg:
		if msg.id != m.id {
			return m, nil
		}
		m.done = true
		m.output = StripFences(m.output)
		return m, nil
	case streamErrMsg:
		if msg.id != m.id {
			return m, nil
		}
		m.err = msg.err
		m.done = true
		return m, nil
	case spinner.TickMsg:
		if m.output == "" && !m.done {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		return m, nil
	}
	return m, nil
}

func (m StreamModel) View() string {
	if m.err != nil {
		return sty.Error.Render("Error: " + m.err.Error())
	}
	if m.output == "" && !m.done {
		return m.spinner.View() + " Thinking…"
	}
	return sty.Command.Render(m.output)
}

func (m StreamModel) waitForEvent() tea.Cmd {
	id, events := m.id, m.events
	return func() tea.Msg {
		ev, ok := <-events
		if !ok {
			return doneMsg{id: id}
		}
		if ev.Err != nil {
			return streamErrMsg{id: id, err: ev.Err}
		}
		if ev.Done {
			return doneMsg{id: id}
		}
		return tokenMsg{id: id, text: ev.Token}
	}
}
