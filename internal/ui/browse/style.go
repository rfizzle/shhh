package browse

import "github.com/charmbracelet/lipgloss"

var (
	listTitleStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15"))
	detailTitleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15"))
	detailBodyStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	cursorStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
	selectedItemStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15"))
	itemStyle           = lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
	previewStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	hintStyle           = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	activeActionStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15")).Background(lipgloss.Color("62")).Padding(0, 1)
	inactiveActionStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("250")).Padding(0, 1)
	dividerLineStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
)
