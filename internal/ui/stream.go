package ui

import (
	"context"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/rfizzle/shhh/internal/provider"
)

type tokenMsg string
type doneMsg struct{}
type streamErrMsg struct{ err error }

type StreamModel struct {
	events    <-chan provider.StreamEvent
	cancel    context.CancelFunc
	spinner   spinner.Model
	output    string
	done      bool
	cancelled bool
	err       error
}

func NewStreamModel(events <-chan provider.StreamEvent, cancel context.CancelFunc) StreamModel {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
	return StreamModel{
		events:  events,
		cancel:  cancel,
		spinner: s,
	}
}

func (m StreamModel) Output() string    { return m.output }
func (m StreamModel) Done() bool        { return m.done }
func (m StreamModel) Cancelled() bool   { return m.cancelled }
func (m StreamModel) Err() error        { return m.err }

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
		switch msg.String() {
		case "q", "esc":
			if !m.done {
				m.cancel()
				m.cancelled = true
				m.done = true
				m.output = StripFences(m.output)
				return m, nil
			}
		}
		return m, nil
	case tokenMsg:
		m.output += string(msg)
		return m, m.waitForEvent()
	case doneMsg:
		m.done = true
		m.output = StripFences(m.output)
		return m, nil
	case streamErrMsg:
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

var commandStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("10"))

func (m StreamModel) View() string {
	if m.err != nil {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render("Error: " + m.err.Error())
	}
	if m.output == "" && !m.done {
		return m.spinner.View() + " Thinking…"
	}
	return commandStyle.Render(m.output)
}

func (m StreamModel) waitForEvent() tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-m.events
		if !ok {
			return doneMsg{}
		}
		if ev.Err != nil {
			return streamErrMsg{err: ev.Err}
		}
		if ev.Done {
			return doneMsg{}
		}
		return tokenMsg(ev.Token)
	}
}
