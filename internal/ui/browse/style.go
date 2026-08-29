package browse

import (
	"charm.land/lipgloss/v2"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// Styles is the saved-chat browser's style set, built by newStyles from a
// token set and nothing else. It draws from the shared palette
// (docs/architecture.md#colour-is-resolved-once-at-the-top) like the other
// surfaces, so the mono swap (/ui mono, NO_COLOR —
// reaches it too.
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
		ListTitle:      lipgloss.NewStyle().Bold(true).Foreground(p.Bright.Color()),
		DetailTitle:    lipgloss.NewStyle().Bold(true).Foreground(p.Bright.Color()),
		DetailBody:     lipgloss.NewStyle().Foreground(p.Body.Color()),
		Cursor:         lipgloss.NewStyle().Foreground(p.Spin.Color()),
		SelectedItem:   lipgloss.NewStyle().Bold(true).Foreground(p.Bright.Color()),
		Item:           lipgloss.NewStyle().Foreground(p.Subtle.Color()),
		Preview:        lipgloss.NewStyle().Foreground(p.Dim.Color()),
		Hint:           lipgloss.NewStyle().Foreground(p.Dim.Color()),
		ActiveAction:   lipgloss.NewStyle().Bold(true).Foreground(p.Bright.Color()).Background(p.FocusBg.Color()).Padding(0, 1),
		InactiveAction: lipgloss.NewStyle().Foreground(p.Subtle.Color()).Padding(0, 1),
		DividerLine:    lipgloss.NewStyle().Foreground(p.Dim.Color()),
	}
}
