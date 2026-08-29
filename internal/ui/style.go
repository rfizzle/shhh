package ui

import (
	"os"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/term"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// Styles is the generate UI's style set, built by newStyles from a token set
// and nothing else. Colors come from the shared components.Palette so
// the generate and chat UIs use identical tokens.
type Styles struct {
	Command      lipgloss.Style
	Error        lipgloss.Style
	Bar          lipgloss.Style
	EditPrompt   lipgloss.Style
	RevisePrompt lipgloss.Style
	ExplainLabel lipgloss.Style
	ExplainBody  lipgloss.Style

	// The result surface: the key row, the containment line, the risk
	// line, and the dimmed command a revise is being compared against.
	Key         lipgloss.Style
	KeyLabel    lipgloss.Style
	PrimaryKey  lipgloss.Style
	DangerKey   lipgloss.Style
	Reach       lipgloss.Style
	Risk        lipgloss.Style
	Dim         lipgloss.Style
	PastCommand lipgloss.Style
}

var (
	sty Styles

	Narrow bool
)

// newStyles builds the whole set from one token set, reading no global.
func newStyles(p components.ColorTokens) Styles {
	return Styles{
		Command: lipgloss.NewStyle().Bold(true).Foreground(p.Add.Color()),
		Error:   lipgloss.NewStyle().Foreground(p.Del.Color()),

		Bar: lipgloss.NewStyle().MarginTop(1),

		EditPrompt:   lipgloss.NewStyle().Foreground(p.Subtle.Color()).MarginTop(1),
		RevisePrompt: lipgloss.NewStyle().Foreground(p.Subtle.Color()).MarginTop(1),
		ExplainLabel: lipgloss.NewStyle().Foreground(p.Subtle.Color()).MarginTop(1).Bold(true),
		ExplainBody:  lipgloss.NewStyle().Foreground(p.Body.Color()),

		// Every key the interface offers is Info; the default and the
		// deliberate one carry their tone as well, and both say it in words
		// too.
		Key:         lipgloss.NewStyle().Foreground(p.Info.Color()),
		KeyLabel:    lipgloss.NewStyle().Foreground(p.Dim.Color()),
		PrimaryKey:  lipgloss.NewStyle().Foreground(p.Add.Color()),
		DangerKey:   lipgloss.NewStyle().Foreground(p.Del.Color()),
		Reach:       lipgloss.NewStyle().Foreground(p.Status.Color()),
		Risk:        lipgloss.NewStyle().Foreground(p.Del.Color()),
		Dim:         lipgloss.NewStyle().Foreground(p.Dim.Color()),
		PastCommand: lipgloss.NewStyle().Foreground(p.Dim.Color()),
	}
}

// applyPalette rebuilds the styles, and settles the width class the surface
// lays out against.
//
// It used to settle the colour profile too, dropping the terminal to Ascii
// under NO_COLOR and TERM=dumb. v2 has no global profile to set — a Style
// carries a resolved colour and nothing degrades it on the way out — so that
// rule moved to where the profile is now decided, beside the palette it
// belongs to (components.detectProfile). It reads the same and it
// reaches every surface rather than only the ones that import this package.
func applyPalette() {
	width, _, err := term.GetSize(os.Stdout.Fd())
	if err != nil {
		width = 80
	}
	Narrow = width < 40

	sty = newStyles(components.Palette)
}

func init() {
	applyPalette()
	// The one-shot generate UI honours the mono swap through the same shared
	// palette the chat TUI uses.
	components.OnPaletteChange(applyPalette)
}
