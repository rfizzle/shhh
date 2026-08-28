// Package components is the reusable TUI interaction catalog (S-076,
// DESIGN-TUI.md): approval card, diff viewer, selectors, inline confirm,
// activity rows, cockpit rail, and agent list. Components are plain state with
// two methods — Update(tea.KeyMsg) (done, result) and View(width) string —
// owned by a host Bubble Tea model via states; they are never nested programs
// and never start goroutines. Esc always dismisses or declines, never
// destroys.
package components

import "github.com/charmbracelet/lipgloss"

// ColorTokens is the shared 256-color palette (DESIGN-TUI.md §10a) promoted
// from the chat and generate style files, so every TUI surface uses identical
// tokens. No new colors without adding them here.
//
// The assignments below are reconciled with tokens/colors.css in the shhh
// Design System project (S-088): same token set, one documented job each.
// Three of them carry the redesign — Spin means anything in motion and only
// that, Accent additionally means the mutation rail, and Info marks every key
// the interface offers, so a key written in any other color is not an offer.
//
// colors.css also defines canvas-only shades (--screen, --page, --rule-faint,
// --meter-empty, --win-*) that let the artboards be drawn in a browser. They
// have no ANSI counterpart: in the terminal the screen is the terminal's own
// background, and faint rules and empty meter cells (▱) are Dim.
type ColorTokens struct {
	Add     lipgloss.Color // 10  diff additions, ✓, [x], permissive mode, staged hunks, healthy context
	Del     lipgloss.Color // 9   diff deletions, ✗, failures, blocked agents, a rule's denial, ctx ≥90%
	AddBg   lipgloss.Color // 22  intraline emphasis background for additions
	DelBg   lipgloss.Color // 52  intraline emphasis background for deletions
	Hunk    lipgloss.Color // 14  @@ hunk headers and nothing else
	Accent  lipgloss.Color // 214 tool glyphs, ⚠ warnings, gated modes, ctx ≥70%, and the mutation rail (§14)
	Info    lipgloss.Color // 12  sub-agents, block headings, and every key the interface offers
	FocusBg lipgloss.Color // 62  selected option/row background, the cursor block
	Dim     lipgloss.Color // 241 chrome, counts, hints, faint rules, empty meter cells, the scroll gutter's track
	Dimmer  lipgloss.Color // 245 tool output, live tails, detail bodies, sparklines, the scroll gutter's thumb
	Spin    lipgloss.Color // 205 anything in motion — spinner frames, ▸ running…, ✦ checking
	Status  lipgloss.Color // 243 status text, the ⛨ containment line
	Bright  lipgloss.Color // 15  headings, the focused row's text
	Subtle  lipgloss.Color // 250 inactive labels (generate UI); no design-system counterpart
	Body    lipgloss.Color // 252 ordinary body text
}

// Palette is the live token set: the full palette above, or the two-grey
// mono palette while mono conformance is on (S-095, mono.go). Every style in
// the product reads it through the vars below, which applyPalette rebuilds
// whenever the palette is swapped.
var Palette = FullPalette

// FullPalette is the 256-color token set — the palette unless mono is on.
var FullPalette = ColorTokens{
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

// Styles shared by the components in this package. They are rebuilt by
// applyPalette rather than initialized in place, so swapping the palette at
// runtime (/ui mono) reaches every surface.
var (
	borderStyle   lipgloss.Style
	headlineStyle lipgloss.Style
	hintStyle     lipgloss.Style
	warnStyle     lipgloss.Style
	shieldStyle   lipgloss.Style
	dimStyle      lipgloss.Style
	dimmerStyle   lipgloss.Style
	focusRowStyle lipgloss.Style
	// The reading cursor (§7a): the row it sits on is lit, and the pointer
	// that names it stays outside the highlight.
	litTextStyle      lipgloss.Style
	focusPointerStyle lipgloss.Style
	addStyle          lipgloss.Style
	delStyle          lipgloss.Style
	addEmphStyle      lipgloss.Style
	delEmphStyle      lipgloss.Style
	hunkStyle         lipgloss.Style
	contextStyle      lipgloss.Style
	accentStyle       lipgloss.Style
	infoStyle         lipgloss.Style
	errStyle          lipgloss.Style
	spinTextStyle     lipgloss.Style
	statusStyle       lipgloss.Style
	bodyStyle         lipgloss.Style
	// The filter row (§4a): what has been typed reads bright against the
	// card, and the run of an option the query named is bold — the one
	// emphasis that costs no colour and survives mono.
	queryTextStyle lipgloss.Style
	matchStyle     lipgloss.Style
	// The scroll gutter (§10g): the track is chrome like every other faint
	// rule, the thumb a step brighter — the same step a sparkline stands off
	// the chrome by, and for the same reason. It is a shape, not a
	// measurement.
	scrollTrackStyle lipgloss.Style
	scrollThumbStyle lipgloss.Style
)

// applyPalette rebuilds every style in this package from the current Palette.
func applyPalette() {
	borderStyle = lipgloss.NewStyle().Foreground(Palette.Dim)
	headlineStyle = lipgloss.NewStyle().Bold(true).Foreground(Palette.Info)
	hintStyle = lipgloss.NewStyle().Foreground(Palette.Dim).Italic(true)
	warnStyle = lipgloss.NewStyle().Foreground(Palette.Del)
	shieldStyle = lipgloss.NewStyle().Foreground(Palette.Status)
	dimStyle = lipgloss.NewStyle().Foreground(Palette.Dim)
	dimmerStyle = lipgloss.NewStyle().Foreground(Palette.Dimmer)
	focusRowStyle = lipgloss.NewStyle().Bold(true).Background(Palette.FocusBg)
	litTextStyle = lipgloss.NewStyle().Foreground(Palette.Bright).Background(Palette.FocusBg)
	focusPointerStyle = lipgloss.NewStyle().Foreground(Palette.Info)
	addStyle = lipgloss.NewStyle().Foreground(Palette.Add)
	delStyle = lipgloss.NewStyle().Foreground(Palette.Del)
	addEmphStyle = lipgloss.NewStyle().Foreground(Palette.Add).Background(Palette.AddBg)
	delEmphStyle = lipgloss.NewStyle().Foreground(Palette.Del).Background(Palette.DelBg)
	hunkStyle = lipgloss.NewStyle().Foreground(Palette.Hunk)
	contextStyle = lipgloss.NewStyle().Foreground(Palette.Dimmer)
	accentStyle = lipgloss.NewStyle().Foreground(Palette.Accent)
	infoStyle = lipgloss.NewStyle().Foreground(Palette.Info)
	errStyle = lipgloss.NewStyle().Foreground(Palette.Del)
	spinTextStyle = lipgloss.NewStyle().Foreground(Palette.Spin)
	statusStyle = lipgloss.NewStyle().Foreground(Palette.Status)
	bodyStyle = lipgloss.NewStyle().Foreground(Palette.Body)
	queryTextStyle = lipgloss.NewStyle().Foreground(Palette.Bright)
	matchStyle = lipgloss.NewStyle().Bold(true)
	scrollTrackStyle = lipgloss.NewStyle().Foreground(Palette.Dim)
	scrollThumbStyle = lipgloss.NewStyle().Foreground(Palette.Dimmer)
}
