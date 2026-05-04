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
	ActionRunAll
	ActionRunStep
)

type ActionSelectedMsg struct {
	Action Action
}

type actionItem struct {
	label    string
	shortcut string
	action   Action
}

var singleActions = []actionItem{
	{"Run", "r", ActionRun},
	{"Copy", "c", ActionCopy},
	{"Edit", "e", ActionEdit},
	{"Explain", "x", ActionExplain},
	{"Revise", "v", ActionRevise},
	{"Cancel", "esc", ActionCancel},
}

var multiActions = []actionItem{
	{"Run all", "r", ActionRunAll},
	{"Step-by-step", "s", ActionRunStep},
	{"Copy", "c", ActionCopy},
	{"Edit", "e", ActionEdit},
	{"Revise", "v", ActionRevise},
	{"Cancel", "esc", ActionCancel},
}

type ActionBarModel struct {
	cursor   int
	selected Action
	multi    bool
}

func NewActionBarModel() ActionBarModel {
	return ActionBarModel{}
}

func (m ActionBarModel) Selected() Action { return m.selected }

func (m ActionBarModel) SetMulti(multi bool) ActionBarModel {
	m.multi = multi
	return m
}

func (m ActionBarModel) actions() []actionItem {
	if m.multi {
		return multiActions
	}
	return singleActions
}

func (m ActionBarModel) Reset() ActionBarModel {
	m.selected = ActionNone
	m.cursor = 0
	return m
}

func (m ActionBarModel) Init() tea.Cmd {
	return nil
}

func (m ActionBarModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	items := m.actions()
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "left", "shift+tab":
			m.cursor--
			if m.cursor < 0 {
				m.cursor = len(items) - 1
			}
			return m, nil
		case "right", "tab":
			m.cursor++
			if m.cursor >= len(items) {
				m.cursor = 0
			}
			return m, nil
		case "enter":
			m.selected = items[m.cursor].action
			return m, func() tea.Msg { return ActionSelectedMsg{Action: m.selected} }
		}
		for _, a := range items {
			if msg.String() == a.shortcut {
				m.selected = a.action
				action := a.action
				return m, func() tea.Msg { return ActionSelectedMsg{Action: action} }
			}
		}
		if msg.String() == "esc" || msg.String() == "q" {
			m.selected = ActionCancel
			return m, func() tea.Msg { return ActionSelectedMsg{Action: ActionCancel} }
		}
	}
	return m, nil
}

func (m ActionBarModel) View() string {
	items := m.actions()
	var rendered []string
	for i, a := range items {
		label := a.label + " (" + a.shortcut + ")"
		if i == m.cursor {
			rendered = append(rendered, ActiveStyle.Render(label))
		} else {
			rendered = append(rendered, InactiveStyle.Render(label))
		}
	}
	return BarStyle.Render(lipgloss.JoinHorizontal(lipgloss.Center, rendered...))
}
