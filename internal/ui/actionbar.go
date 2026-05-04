package ui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type Action int

const (
	ActionNone Action = iota
	ActionRun
	ActionCopy
	ActionRevise
	ActionCancel
	ActionEdit
	ActionExplain
)

type ActionSelectedMsg struct {
	Action Action
}

type actionItem struct {
	label    string
	shortcut string
	action   Action
}

var actions = []actionItem{
	{"Run", "r", ActionRun},
	{"Copy", "c", ActionCopy},
	{"Edit", "e", ActionEdit},
	{"Explain", "x", ActionExplain},
	{"Revise", "v", ActionRevise},
	{"Cancel", "esc", ActionCancel},
}

type ActionBarModel struct {
	cursor   int
	selected Action
}

func NewActionBarModel() ActionBarModel {
	return ActionBarModel{}
}

func (m ActionBarModel) Selected() Action { return m.selected }

func (m ActionBarModel) Reset() ActionBarModel {
	m.selected = ActionNone
	m.cursor = 0
	return m
}

func (m ActionBarModel) Init() tea.Cmd {
	return nil
}

func (m ActionBarModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "left", "shift+tab":
			m.cursor--
			if m.cursor < 0 {
				m.cursor = len(actions) - 1
			}
			return m, nil
		case "right", "tab":
			m.cursor++
			if m.cursor >= len(actions) {
				m.cursor = 0
			}
			return m, nil
		case "enter":
			m.selected = actions[m.cursor].action
			return m, func() tea.Msg { return ActionSelectedMsg{Action: m.selected} }
		case "r":
			m.selected = ActionRun
			return m, func() tea.Msg { return ActionSelectedMsg{Action: ActionRun} }
		case "c":
			m.selected = ActionCopy
			return m, func() tea.Msg { return ActionSelectedMsg{Action: ActionCopy} }
		case "e":
			m.selected = ActionEdit
			return m, func() tea.Msg { return ActionSelectedMsg{Action: ActionEdit} }
		case "v":
			m.selected = ActionRevise
			return m, func() tea.Msg { return ActionSelectedMsg{Action: ActionRevise} }
		case "x":
			m.selected = ActionExplain
			return m, func() tea.Msg { return ActionSelectedMsg{Action: ActionExplain} }
		case "esc", "q":
			m.selected = ActionCancel
			return m, func() tea.Msg { return ActionSelectedMsg{Action: ActionCancel} }
		}
	}
	return m, nil
}

var (
	activeStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15")).Background(lipgloss.Color("62")).Padding(0, 1)
	inactiveStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("250")).Padding(0, 1)
	barStyle      = lipgloss.NewStyle().MarginTop(1)
)

func (m ActionBarModel) View() string {
	var items []string
	for i, a := range actions {
		label := a.label + " (" + a.shortcut + ")"
		if i == m.cursor {
			items = append(items, activeStyle.Render(label))
		} else {
			items = append(items, inactiveStyle.Render(label))
		}
	}
	return barStyle.Render(lipgloss.JoinHorizontal(lipgloss.Center, items...))
}
