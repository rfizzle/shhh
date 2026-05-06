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
	toolStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	toolArgsStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	toolResultStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	toolBorderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	statusBarStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("243"))
	updateNoticeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
)
