// Package components is the reusable TUI interaction catalog (S-076,
// DESIGN-TUI.md): approval card, diff viewer, selectors, inline confirm,
// activity rows, cockpit rail, and agent list. Components are plain state with
// two methods — Update(tea.KeyMsg) (done, result) and View(width) string —
// owned by a host Bubble Tea model via states; they are never nested programs
// and never start goroutines. Esc always dismisses or declines, never
// destroys.
package components

import "github.com/charmbracelet/lipgloss"

// ColorTokens is the shared 256-color palette (DESIGN-TUI.md §10) promoted
// from the chat and generate style files, so every TUI surface uses identical
// tokens. No new colors without adding them here.
type ColorTokens struct {
	Add     lipgloss.Color // diff additions, [x], permissive mode, success
	Del     lipgloss.Color // diff deletions, errors, required-note, ctx ≥90%
	AddBg   lipgloss.Color // intraline emphasis background for additions
	DelBg   lipgloss.Color // intraline emphasis background for deletions
	Hunk    lipgloss.Color // @@ hunk headers
	Accent  lipgloss.Color // tool glyphs, warnings, gated modes, ctx ≥70%
	Info    lipgloss.Color // sub-agents, user accents
	FocusBg lipgloss.Color // selected option/row background
	Dim     lipgloss.Color // chrome, counts, hints
	Dimmer  lipgloss.Color // tool results, live tail
	Spin    lipgloss.Color // spinners, ✦ checking
	Status  lipgloss.Color // status bar text, containment line
	Bright  lipgloss.Color // headers, focused-row foreground
	Subtle  lipgloss.Color // inactive labels (generate UI)
	Body    lipgloss.Color // explanation body text (generate UI)
}

// Palette is the one shared token set.
var Palette = ColorTokens{
	Add:     lipgloss.Color("10"),
	Del:     lipgloss.Color("9"),
	AddBg:   lipgloss.Color("22"),
	DelBg:   lipgloss.Color("52"),
	Hunk:    lipgloss.Color("14"),
	Accent:  lipgloss.Color("214"),
	Info:    lipgloss.Color("12"),
	FocusBg: lipgloss.Color("62"),
	Dim:     lipgloss.Color("241"),
	Dimmer:  lipgloss.Color("245"),
	Spin:    lipgloss.Color("205"),
	Status:  lipgloss.Color("243"),
	Bright:  lipgloss.Color("15"),
	Subtle:  lipgloss.Color("250"),
	Body:    lipgloss.Color("252"),
}

// Styles shared by the components in this package.
var (
	borderStyle   = lipgloss.NewStyle().Foreground(Palette.Dim)
	headlineStyle = lipgloss.NewStyle().Bold(true).Foreground(Palette.Info)
	hintStyle     = lipgloss.NewStyle().Foreground(Palette.Dim).Italic(true)
	warnStyle     = lipgloss.NewStyle().Foreground(Palette.Del)
	shieldStyle   = lipgloss.NewStyle().Foreground(Palette.Status)
	dimStyle      = lipgloss.NewStyle().Foreground(Palette.Dim)
	dimmerStyle   = lipgloss.NewStyle().Foreground(Palette.Dimmer)
	focusRowStyle = lipgloss.NewStyle().Bold(true).Background(Palette.FocusBg)
	addStyle      = lipgloss.NewStyle().Foreground(Palette.Add)
	delStyle      = lipgloss.NewStyle().Foreground(Palette.Del)
	addEmphStyle  = lipgloss.NewStyle().Foreground(Palette.Add).Background(Palette.AddBg)
	delEmphStyle  = lipgloss.NewStyle().Foreground(Palette.Del).Background(Palette.DelBg)
	hunkStyle     = lipgloss.NewStyle().Foreground(Palette.Hunk)
	contextStyle  = lipgloss.NewStyle().Foreground(Palette.Dimmer)
	accentStyle   = lipgloss.NewStyle().Foreground(Palette.Accent)
	infoStyle     = lipgloss.NewStyle().Foreground(Palette.Info)
	errStyle      = lipgloss.NewStyle().Foreground(Palette.Del)
	spinTextStyle = lipgloss.NewStyle().Foreground(Palette.Spin)
	statusStyle   = lipgloss.NewStyle().Foreground(Palette.Status)
)
