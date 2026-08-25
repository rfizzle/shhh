package chat

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// All colors come from the shared components.Palette (DESIGN-TUI.md §10) so
// the chat and generate UIs stay visually consistent; no new colors without
// adding a token there.
var (
	userStyle       = lipgloss.NewStyle().Bold(true).Foreground(components.Palette.Info)
	assistantStyle  = lipgloss.NewStyle().Bold(true).Foreground(components.Palette.Add)
	errorStyle      = lipgloss.NewStyle().Foreground(components.Palette.Del)
	systemMsgStyle  = lipgloss.NewStyle().Foreground(components.Palette.Dim).Italic(true)
	headerStyle     = lipgloss.NewStyle().Bold(true).Foreground(components.Palette.Bright)
	headerHintStyle = lipgloss.NewStyle().Foreground(components.Palette.Dim)
	welcomeStyle    = lipgloss.NewStyle().Foreground(components.Palette.Dim).Italic(true)
	toolStyle       = lipgloss.NewStyle().Foreground(components.Palette.Accent)
	toolArgsStyle   = lipgloss.NewStyle().Foreground(components.Palette.Dim)
	statusBarStyle  = lipgloss.NewStyle().Foreground(components.Palette.Status)
	// Permission-mode segment (DESIGN-TUI.md §8): permissive vs gated modes
	// (the orchestrator's bar renders through components.Cockpit; these back
	// the child-scoped bar, S-077).
	modePermissiveStyle = lipgloss.NewStyle().Foreground(components.Palette.Add)
	modeGatedStyle      = lipgloss.NewStyle().Foreground(components.Palette.Accent)
	ctxAlertStyle       = lipgloss.NewStyle().Bold(true).Foreground(components.Palette.Del)
	updateNoticeStyle   = lipgloss.NewStyle().Foreground(components.Palette.Accent)
	// Focused row of the plan-approval prompt (DESIGN-TUI.md §4a).
	planSelectedStyle = lipgloss.NewStyle().Bold(true).Background(components.Palette.FocusBg)
	// Focus-mode gutter pointer on the selected transcript row (§7).
	focusMarkerStyle = lipgloss.NewStyle().Bold(true).Foreground(components.Palette.Accent)
	// Step outline (S-090, §13): the header's title, ordinal, faint rule and
	// stats, plus one style per state glyph — done, failed, running, queued.
	stepTitleStyle     = lipgloss.NewStyle().Foreground(components.Palette.Body)
	stepLiveTitleStyle = lipgloss.NewStyle().Foreground(components.Palette.Bright)
	stepRuleStyle      = lipgloss.NewStyle().Foreground(components.Palette.Dim)
	stepStatsStyle     = lipgloss.NewStyle().Foreground(components.Palette.Dim)
	stepDimStyle       = lipgloss.NewStyle().Foreground(components.Palette.Dim)
	stepDoneStyle      = lipgloss.NewStyle().Foreground(components.Palette.Add)
	stepFailStyle      = lipgloss.NewStyle().Foreground(components.Palette.Del)
	stepRunStyle       = lipgloss.NewStyle().Foreground(components.Palette.Spin)
)
