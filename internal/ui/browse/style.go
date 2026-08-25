package browse

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// The saved-chat browser draws from the shared palette (DESIGN-TUI.md §10a)
// like the other surfaces, so the mono swap (/ui mono, NO_COLOR — S-095)
// reaches it too. Styles are rebuilt on a palette change rather than
// initialized in place.
var (
	listTitleStyle      lipgloss.Style
	detailTitleStyle    lipgloss.Style
	detailBodyStyle     lipgloss.Style
	cursorStyle         lipgloss.Style
	selectedItemStyle   lipgloss.Style
	itemStyle           lipgloss.Style
	previewStyle        lipgloss.Style
	hintStyle           lipgloss.Style
	activeActionStyle   lipgloss.Style
	inactiveActionStyle lipgloss.Style
	dividerLineStyle    lipgloss.Style
)

func init() {
	applyPalette()
	components.OnPaletteChange(applyPalette)
}

func applyPalette() {
	p := components.Palette
	listTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(p.Bright)
	detailTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(p.Bright)
	detailBodyStyle = lipgloss.NewStyle().Foreground(p.Body)
	cursorStyle = lipgloss.NewStyle().Foreground(p.Spin)
	selectedItemStyle = lipgloss.NewStyle().Bold(true).Foreground(p.Bright)
	itemStyle = lipgloss.NewStyle().Foreground(p.Subtle)
	previewStyle = lipgloss.NewStyle().Foreground(p.Dim)
	hintStyle = lipgloss.NewStyle().Foreground(p.Dim)
	activeActionStyle = lipgloss.NewStyle().Bold(true).Foreground(p.Bright).Background(p.FocusBg).Padding(0, 1)
	inactiveActionStyle = lipgloss.NewStyle().Foreground(p.Subtle).Padding(0, 1)
	dividerLineStyle = lipgloss.NewStyle().Foreground(p.Dim)
}
