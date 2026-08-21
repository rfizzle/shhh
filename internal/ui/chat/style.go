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
	statusBarStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("243"))
	// Permission-mode segment (DESIGN-TUI.md §8): permissive vs gated modes,
	// plus the classifier's in-flight indicator (S-060).
	modePermissiveStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	modeGatedStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	modeCheckingStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
	ctxWarnStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	ctxAlertStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)
	updateNoticeStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	// Focused row of the plan-approval prompt (DESIGN-TUI.md §4a).
	planSelectedStyle = lipgloss.NewStyle().Bold(true).Background(lipgloss.Color("62"))
	diffAddStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	diffDelStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	diffHunkStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("14"))
	diffContextStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
)
