package ui

import (
	"os"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"
	"github.com/muesli/termenv"
)

var (
	CommandStyle      lipgloss.Style
	ErrorStyle        lipgloss.Style
	SpinnerStyle      lipgloss.Style
	ActiveStyle       lipgloss.Style
	InactiveStyle     lipgloss.Style
	BarStyle          lipgloss.Style
	EditPromptStyle   lipgloss.Style
	RevisePromptStyle lipgloss.Style
	ExplainLabelStyle lipgloss.Style
	ExplainBodyStyle  lipgloss.Style

	Narrow bool
)

func InitStyles() {
	noColor := os.Getenv("NO_COLOR") != ""
	dumbTerm := os.Getenv("TERM") == "dumb"

	if noColor || dumbTerm {
		lipgloss.SetColorProfile(termenv.Ascii)
	}

	width, _, err := term.GetSize(os.Stdout.Fd())
	if err != nil {
		width = 80
	}
	Narrow = width < 40

	padding := 1
	if Narrow {
		padding = 0
	}

	CommandStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("10"))
	ErrorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	SpinnerStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))

	ActiveStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15")).Background(lipgloss.Color("62")).Padding(0, padding)
	InactiveStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("250")).Padding(0, padding)
	BarStyle = lipgloss.NewStyle().MarginTop(1)

	EditPromptStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("250")).MarginTop(1)
	RevisePromptStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("250")).MarginTop(1)
	ExplainLabelStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("250")).MarginTop(1).Bold(true)
	ExplainBodyStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
}

func init() {
	InitStyles()
}
