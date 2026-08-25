package chat

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// All colors come from the shared components.Palette (DESIGN-TUI.md §10) so
// the chat and generate UIs stay visually consistent; no new colors without
// adding a token there.
// The style vars are rebuilt by applyPalette rather than initialized in
// place, so the mono swap (/ui mono, NO_COLOR — S-095) reaches every surface
// in this package.
var (
	userStyle       lipgloss.Style
	assistantStyle  lipgloss.Style
	errorStyle      lipgloss.Style
	systemMsgStyle  lipgloss.Style
	headerStyle     lipgloss.Style
	headerHintStyle lipgloss.Style
	welcomeStyle    lipgloss.Style
	toolStyle       lipgloss.Style
	toolArgsStyle   lipgloss.Style
	statusBarStyle  lipgloss.Style
	// Permission-mode segment (DESIGN-TUI.md §8): permissive vs gated modes
	// (the orchestrator's bar renders through components.Cockpit; these back
	// the child-scoped bar, S-077).
	modePermissiveStyle lipgloss.Style
	modeGatedStyle      lipgloss.Style
	ctxAlertStyle       lipgloss.Style
	updateNoticeStyle   lipgloss.Style
	// Focused row of the plan-approval prompt (DESIGN-TUI.md §4a).
	planSelectedStyle lipgloss.Style
	// Focus-mode gutter pointer on the selected transcript row (§7).
	focusMarkerStyle lipgloss.Style
	// Step outline (S-090, §13): the header's title, ordinal, faint rule and
	// stats, plus one style per state glyph — done, failed, running, queued.
	stepTitleStyle     lipgloss.Style
	stepLiveTitleStyle lipgloss.Style
	stepRuleStyle      lipgloss.Style
	stepStatsStyle     lipgloss.Style
	stepDimStyle       lipgloss.Style
	stepDoneStyle      lipgloss.Style
	stepFailStyle      lipgloss.Style
	stepRunStyle       lipgloss.Style
)

// init builds this package's styles and keeps them current across a palette
// swap. It runs after internal/ui/components is fully initialized, so the
// environment's mono decision is already settled.
func init() {
	applyPalette()
	components.OnPaletteChange(applyPalette)
}

// applyPalette rebuilds every style this package owns — here, in frame.go,
// complete.go and inspector.go — from the current components.Palette.
func applyPalette() {
	p := components.Palette
	userStyle = lipgloss.NewStyle().Bold(true).Foreground(p.Info)
	assistantStyle = lipgloss.NewStyle().Bold(true).Foreground(p.Add)
	errorStyle = lipgloss.NewStyle().Foreground(p.Del)
	systemMsgStyle = lipgloss.NewStyle().Foreground(p.Dim).Italic(true)
	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(p.Bright)
	headerHintStyle = lipgloss.NewStyle().Foreground(p.Dim)
	welcomeStyle = lipgloss.NewStyle().Foreground(p.Dim).Italic(true)
	toolStyle = lipgloss.NewStyle().Foreground(p.Accent)
	toolArgsStyle = lipgloss.NewStyle().Foreground(p.Dim)
	statusBarStyle = lipgloss.NewStyle().Foreground(p.Status)
	modePermissiveStyle = lipgloss.NewStyle().Foreground(p.Add)
	modeGatedStyle = lipgloss.NewStyle().Foreground(p.Accent)
	ctxAlertStyle = lipgloss.NewStyle().Bold(true).Foreground(p.Del)
	updateNoticeStyle = lipgloss.NewStyle().Foreground(p.Accent)
	planSelectedStyle = lipgloss.NewStyle().Bold(true).Background(p.FocusBg)
	focusMarkerStyle = lipgloss.NewStyle().Bold(true).Foreground(p.Accent)
	stepTitleStyle = lipgloss.NewStyle().Foreground(p.Body)
	stepLiveTitleStyle = lipgloss.NewStyle().Foreground(p.Bright)
	stepRuleStyle = lipgloss.NewStyle().Foreground(p.Dim)
	stepStatsStyle = lipgloss.NewStyle().Foreground(p.Dim)
	stepDimStyle = lipgloss.NewStyle().Foreground(p.Dim)
	stepDoneStyle = lipgloss.NewStyle().Foreground(p.Add)
	stepFailStyle = lipgloss.NewStyle().Foreground(p.Del)
	stepRunStyle = lipgloss.NewStyle().Foreground(p.Spin)

	applyFrameStyles(p)
	applyCompleteStyles(p)
	applyInspectorStyles(p)
}
