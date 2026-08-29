package ui

import (
	"os"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"
	"github.com/muesli/termenv"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// Styles is the generate UI's style set, built by newStyles from a token set
// and nothing else. Colors come from the shared components.Palette (S-076) so
// the generate and chat UIs use identical tokens.
type Styles struct {
	Command      lipgloss.Style
	Error        lipgloss.Style
	Bar          lipgloss.Style
	EditPrompt   lipgloss.Style
	RevisePrompt lipgloss.Style
	ExplainLabel lipgloss.Style
	ExplainBody  lipgloss.Style

	// The S-113 result surface: the key row, the containment line, the risk
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
		Command: lipgloss.NewStyle().Bold(true).Foreground(p.Add),
		Error:   lipgloss.NewStyle().Foreground(p.Del),

		Bar: lipgloss.NewStyle().MarginTop(1),

		EditPrompt:   lipgloss.NewStyle().Foreground(p.Subtle).MarginTop(1),
		RevisePrompt: lipgloss.NewStyle().Foreground(p.Subtle).MarginTop(1),
		ExplainLabel: lipgloss.NewStyle().Foreground(p.Subtle).MarginTop(1).Bold(true),
		ExplainBody:  lipgloss.NewStyle().Foreground(p.Body),

		// Every key the interface offers is Info (§10a); the default and the
		// deliberate one carry their tone as well, and both say it in words
		// too.
		Key:         lipgloss.NewStyle().Foreground(p.Info),
		KeyLabel:    lipgloss.NewStyle().Foreground(p.Dim),
		PrimaryKey:  lipgloss.NewStyle().Foreground(p.Add),
		DangerKey:   lipgloss.NewStyle().Foreground(p.Del),
		Reach:       lipgloss.NewStyle().Foreground(p.Status),
		Risk:        lipgloss.NewStyle().Foreground(p.Del),
		Dim:         lipgloss.NewStyle().Foreground(p.Dim),
		PastCommand: lipgloss.NewStyle().Foreground(p.Dim),
	}
}

// applyPalette rebuilds the styles, and settles the terminal profile and the
// width class the surface lays out against.
func applyPalette() {
	// components already switched the palette to its two greys for these
	// (S-095); dropping the profile to Ascii on top of that is the stricter
	// reading NO_COLOR asks for — no ANSI colour at all, bold and glyphs
	// intact.
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

	sty = newStyles(components.Palette)
}

func init() {
	applyPalette()
	// The one-shot generate UI honours the mono swap through the same shared
	// palette the chat TUI uses (S-095).
	components.OnPaletteChange(applyPalette)
}
