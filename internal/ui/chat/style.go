package chat

import "github.com/charmbracelet/lipgloss"

var (
	userStyle       = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	assistantStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("10"))
	errorStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	systemMsgStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Italic(true)
	headerStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15"))
	headerHintStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	welcomeStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Italic(true)
)
