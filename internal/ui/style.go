package ui

import (
	"os"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"
	"github.com/muesli/termenv"
	"github.com/rfizzle/shhh/internal/ui/components"
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

	// Colors come from the shared components.Palette (S-076) so the generate
	// and chat UIs use identical tokens.
	p := components.Palette
	CommandStyle = lipgloss.NewStyle().Bold(true).Foreground(p.Add)
	ErrorStyle = lipgloss.NewStyle().Foreground(p.Del)
	SpinnerStyle = lipgloss.NewStyle().Foreground(p.Spin)

	ActiveStyle = lipgloss.NewStyle().Bold(true).Foreground(p.Bright).Background(p.FocusBg).Padding(0, padding)
	InactiveStyle = lipgloss.NewStyle().Foreground(p.Subtle).Padding(0, padding)
	BarStyle = lipgloss.NewStyle().MarginTop(1)

	EditPromptStyle = lipgloss.NewStyle().Foreground(p.Subtle).MarginTop(1)
	RevisePromptStyle = lipgloss.NewStyle().Foreground(p.Subtle).MarginTop(1)
	ExplainLabelStyle = lipgloss.NewStyle().Foreground(p.Subtle).MarginTop(1).Bold(true)
	ExplainBodyStyle = lipgloss.NewStyle().Foreground(p.Body)
}

func init() {
	InitStyles()
}
