package ui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rfizzle/shhh/internal/provider"
)

type phase int

const (
	phaseStreaming phase = iota
	phaseAction
	phaseDone
)

type GenerateModel struct {
	stream    StreamModel
	actionBar ActionBarModel
	phase     phase
	result    GenerateResult
}

type GenerateResult struct {
	Command   string
	Action    Action
	Cancelled bool
	Err       error
}

func NewGenerateModel(events <-chan provider.StreamEvent, cancel context.CancelFunc) GenerateModel {
	return GenerateModel{
		stream:    NewStreamModel(events, cancel),
		actionBar: NewActionBarModel(),
		phase:     phaseStreaming,
	}
}

func (m GenerateModel) Result() GenerateResult { return m.result }
func (m GenerateModel) Phase() phase           { return m.phase }

func (m GenerateModel) Init() tea.Cmd {
	return m.stream.Init()
}

func (m GenerateModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m.phase {
	case phaseStreaming:
		return m.updateStreaming(msg)
	case phaseAction:
		return m.updateAction(msg)
	}
	return m, nil
}

func (m GenerateModel) updateStreaming(msg tea.Msg) (tea.Model, tea.Cmd) {
	updated, cmd := m.stream.Update(msg)
	m.stream = updated.(StreamModel)

	if m.stream.Done() {
		if m.stream.Cancelled() || m.stream.Err() != nil {
			m.phase = phaseDone
			m.result = GenerateResult{
				Command:   m.stream.Output(),
				Cancelled: m.stream.Cancelled(),
				Err:       m.stream.Err(),
			}
			return m, tea.Quit
		}
		m.phase = phaseAction
		return m, nil
	}
	return m, cmd
}

func (m GenerateModel) updateAction(msg tea.Msg) (tea.Model, tea.Cmd) {
	updated, cmd := m.actionBar.Update(msg)
	m.actionBar = updated.(ActionBarModel)

	if m.actionBar.Selected() != ActionNone {
		m.phase = phaseDone
		m.result = GenerateResult{
			Command: m.stream.Output(),
			Action:  m.actionBar.Selected(),
		}
		return m, tea.Quit
	}
	return m, cmd
}

func (m GenerateModel) View() string {
	switch m.phase {
	case phaseStreaming:
		return m.stream.View()
	case phaseAction:
		return m.stream.View() + "\n" + m.actionBar.View()
	default:
		return m.stream.View()
	}
}
