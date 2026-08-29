package browse

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// Styles is the saved-chat browser's style set, built by newStyles from a
// token set and nothing else. It draws from the shared palette (DESIGN-TUI.md
// §10a) like the other surfaces, so the mono swap (/ui mono, NO_COLOR —
// S-095) reaches it too.
type Styles struct {
	ListTitle      lipgloss.Style
	DetailTitle    lipgloss.Style
	DetailBody     lipgloss.Style
	Cursor         lipgloss.Style
	SelectedItem   lipgloss.Style
	Item           lipgloss.Style
	Preview        lipgloss.Style
	Hint           lipgloss.Style
	ActiveAction   lipgloss.Style
	InactiveAction lipgloss.Style
	DividerLine    lipgloss.Style
}

// sty is the live style set, rebuilt on a palette change rather than
// initialized in place.
var sty Styles

func init() {
	applyPalette()
	components.OnPaletteChange(applyPalette)
}

func applyPalette() { sty = newStyles(components.Palette) }

func newStyles(p components.ColorTokens) Styles {
	return Styles{
		ListTitle:      lipgloss.NewStyle().Bold(true).Foreground(p.Bright),
		DetailTitle:    lipgloss.NewStyle().Bold(true).Foreground(p.Bright),
		DetailBody:     lipgloss.NewStyle().Foreground(p.Body),
		Cursor:         lipgloss.NewStyle().Foreground(p.Spin),
		SelectedItem:   lipgloss.NewStyle().Bold(true).Foreground(p.Bright),
		Item:           lipgloss.NewStyle().Foreground(p.Subtle),
		Preview:        lipgloss.NewStyle().Foreground(p.Dim),
		Hint:           lipgloss.NewStyle().Foreground(p.Dim),
		ActiveAction:   lipgloss.NewStyle().Bold(true).Foreground(p.Bright).Background(p.FocusBg).Padding(0, 1),
		InactiveAction: lipgloss.NewStyle().Foreground(p.Subtle).Padding(0, 1),
		DividerLine:    lipgloss.NewStyle().Foreground(p.Dim),
	}
}
