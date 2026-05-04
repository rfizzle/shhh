package ui

import (
	"context"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/rfizzle/shhh/internal/provider"
)

type phase int

const (
	phaseStreaming phase = iota
	phaseAction
	phaseRevise
	phaseEdit
	phaseExplain
	phaseDone
)

type NewStreamFunc func(messages []provider.Message) (<-chan provider.StreamEvent, context.CancelFunc, error)

type ExplainStreamFunc func(command string) (<-chan provider.StreamEvent, context.CancelFunc, error)

type GenerateModel struct {
	stream        StreamModel
	actionBar     ActionBarModel
	reviseInput   textinput.Model
	editInput     textinput.Model
	explainStream StreamModel
	messages      []provider.Message
	newStream     NewStreamFunc
	newExplain    ExplainStreamFunc
	phase         phase
	result        GenerateResult
}

type GenerateResult struct {
	Command   string
	Action    Action
	Feedback  string
	Cancelled bool
	Err       error
}

func NewGenerateModel(events <-chan provider.StreamEvent, cancel context.CancelFunc, messages []provider.Message, newStream NewStreamFunc, newExplain ExplainStreamFunc) GenerateModel {
	ti := textinput.New()
	ti.Placeholder = "Describe what to change…"
	ti.CharLimit = 500
	ei := textinput.New()
	ei.CharLimit = 1000
	msgs := make([]provider.Message, len(messages))
	copy(msgs, messages)
	return GenerateModel{
		stream:      NewStreamModel(events, cancel),
		actionBar:   NewActionBarModel(),
		reviseInput: ti,
		editInput:   ei,
		messages:    msgs,
		newStream:   newStream,
		newExplain:  newExplain,
		phase:       phaseStreaming,
	}
}

func (m GenerateModel) Result() GenerateResult      { return m.result }
func (m GenerateModel) Phase() phase                { return m.phase }
func (m GenerateModel) Messages() []provider.Message { return m.messages }

func (m GenerateModel) Init() tea.Cmd {
	return m.stream.Init()
}

func (m GenerateModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m.phase {
	case phaseStreaming:
		return m.updateStreaming(msg)
	case phaseAction:
		return m.updateAction(msg)
	case phaseRevise:
		return m.updateRevise(msg)
	case phaseEdit:
		return m.updateEdit(msg)
	case phaseExplain:
		return m.updateExplain(msg)
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
		m.messages = append(m.messages, provider.Message{
			Role:    provider.RoleAssistant,
			Content: m.stream.Output(),
		})
		m.phase = phaseAction
		return m, nil
	}
	return m, cmd
}

func (m GenerateModel) updateAction(msg tea.Msg) (tea.Model, tea.Cmd) {
	updated, cmd := m.actionBar.Update(msg)
	m.actionBar = updated.(ActionBarModel)

	if m.actionBar.Selected() == ActionEdit {
		m.phase = phaseEdit
		m.editInput.SetValue(m.stream.Output())
		m.editInput.CursorEnd()
		m.editInput.Focus()
		m.actionBar = m.actionBar.Reset()
		return m, m.editInput.Cursor.BlinkCmd()
	}

	if m.actionBar.Selected() == ActionExplain {
		m.actionBar = m.actionBar.Reset()
		if m.newExplain == nil {
			return m, nil
		}
		events, cancel, err := m.newExplain(m.stream.Output())
		if err != nil {
			return m, func() tea.Msg { return explainErrMsg{err: err} }
		}
		m.explainStream = NewStreamModel(events, cancel)
		m.phase = phaseExplain
		return m, m.explainStream.Init()
	}

	if m.actionBar.Selected() == ActionRevise {
		m.phase = phaseRevise
		m.reviseInput.Reset()
		m.reviseInput.Focus()
		m.actionBar = m.actionBar.Reset()
		return m, m.reviseInput.Cursor.BlinkCmd()
	}

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

type reviseErrMsg struct{ err error }
type explainErrMsg struct{ err error }

func (m GenerateModel) updateRevise(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyEnter:
			feedback := m.reviseInput.Value()
			if feedback == "" {
				return m, nil
			}
			m.messages = append(m.messages, provider.Message{
				Role:    provider.RoleUser,
				Content: feedback,
			})
			if m.newStream == nil {
				m.phase = phaseDone
				m.result = GenerateResult{
					Command:  m.stream.Output(),
					Action:   ActionRevise,
					Feedback: feedback,
				}
				return m, tea.Quit
			}
			events, cancel, err := m.newStream(m.messages)
			if err != nil {
				return m, func() tea.Msg { return reviseErrMsg{err: err} }
			}
			m.stream = NewStreamModel(events, cancel)
			m.phase = phaseStreaming
			return m, m.stream.Init()
		case tea.KeyEscape:
			m.phase = phaseAction
			m.reviseInput.Blur()
			return m, nil
		}
	case reviseErrMsg:
		m.phase = phaseDone
		m.result = GenerateResult{Err: msg.err}
		return m, tea.Quit
	}
	var cmd tea.Cmd
	m.reviseInput, cmd = m.reviseInput.Update(msg)
	return m, cmd
}

func (m GenerateModel) updateEdit(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyEnter:
			edited := m.editInput.Value()
			if edited == "" {
				return m, nil
			}
			m.stream = m.stream.WithOutput(edited)
			if len(m.messages) > 0 && m.messages[len(m.messages)-1].Role == provider.RoleAssistant {
				m.messages[len(m.messages)-1].Content = edited
			}
			m.editInput.Blur()
			m.phase = phaseAction
			return m, nil
		case tea.KeyEscape:
			m.editInput.Blur()
			m.phase = phaseAction
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.editInput, cmd = m.editInput.Update(msg)
	return m, cmd
}

func (m GenerateModel) updateExplain(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.explainStream.Done() {
			m.phase = phaseAction
			return m, nil
		}
		switch msg.String() {
		case "q", "esc":
			m.explainStream.cancel()
			m.phase = phaseAction
			return m, nil
		}
		return m, nil
	case explainErrMsg:
		m.explainStream = m.explainStream.WithOutput("Error: " + msg.err.Error())
		m.explainStream.done = true
		m.phase = phaseAction
		return m, nil
	}
	updated, cmd := m.explainStream.Update(msg)
	m.explainStream = updated.(StreamModel)
	if m.explainStream.Done() {
		m.phase = phaseAction
		return m, nil
	}
	return m, cmd
}


func (m GenerateModel) viewExplanation() string {
	if m.explainStream.output == "" && !m.explainStream.done {
		return ""
	}
	return "\n" + ExplainLabelStyle.Render("Explanation:") + "\n" + ExplainBodyStyle.Render(m.explainStream.output)
}

func (m GenerateModel) View() string {
	explanation := m.viewExplanation()
	switch m.phase {
	case phaseStreaming:
		return m.stream.View()
	case phaseAction:
		return m.stream.View() + explanation + "\n" + m.actionBar.View()
	case phaseEdit:
		return EditPromptStyle.Render("Edit: ") + m.editInput.View()
	case phaseRevise:
		return m.stream.View() + "\n" + RevisePromptStyle.Render("Feedback: ") + m.reviseInput.View()
	case phaseExplain:
		view := m.stream.View() + "\n" + ExplainLabelStyle.Render("Explanation:")
		if m.explainStream.output == "" && !m.explainStream.done {
			view += " " + m.explainStream.spinner.View()
		} else {
			view += "\n" + ExplainBodyStyle.Render(m.explainStream.output)
		}
		return view
	default:
		return m.stream.View()
	}
}
