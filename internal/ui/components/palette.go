// Package components is the reusable TUI interaction catalog (S-076,
// DESIGN-TUI.md): approval card, diff viewer, selectors, inline confirm,
// activity rows, cockpit rail, and agent list. Components are plain state with
// two methods — Update(tea.KeyMsg) (done, result) and View(width) string —
// owned by a host Bubble Tea model via states; they are never nested programs
// and never start goroutines. Esc always dismisses or declines, never
// destroys.
package components

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// Token is one palette colour, written once for every colour profile a
// terminal can report rather than once and degraded. lipgloss will happily
// derive the other two from a hex, but termenv's 256-colour conversion walks
// only the 6×6×6 cube and never the greyscale ramp, so #d0d0d0 (body) and
// #eaeaea (bright) both come back as 188 and two rungs of the grey ladder
// land on one colour. The ladder is the thing, so each rung says what it is
// at each profile.
//
// The values are transcribed from tokens/colors.css in the shhh Design System
// project (S-088), which states both halves already: a hex, and the 256 index
// it stands for.
type Token = lipgloss.CompleteColor

// token writes one row of the §10a table: the design system's hex, the 256
// index it was chosen for, and the theme colour a 16-colour terminal falls
// back to.
func token(hex, ansi256, ansi16 string) Token {
	return Token{TrueColor: hex, ANSI256: ansi256, ANSI: ansi16}
}

// ColorTokens is the shared palette (DESIGN-TUI.md §10a) promoted from the
// chat and generate style files, so every TUI surface uses identical tokens.
// No new colors without adding them here.
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
	Add     Token // diff additions, ✓, [x], permissive mode, staged hunks, healthy context
	Del     Token // diff deletions, ✗, failures, blocked agents, a rule's denial, ctx ≥90%
	AddBg   Token // intraline emphasis background for additions
	DelBg   Token // intraline emphasis background for deletions
	Hunk    Token // @@ hunk headers and nothing else
	Accent  Token // tool glyphs, ⚠ warnings, gated modes, ctx ≥70%, and the mutation rail (§14)
	Info    Token // sub-agents, block headings, and every key the interface offers
	FocusBg Token // selected option/row background, the cursor block
	Dim     Token // chrome, counts, hints, faint rules, empty meter cells, the scroll gutter's track
	Dimmer  Token // tool output, live tails, detail bodies, sparklines, the scroll gutter's thumb
	Spin    Token // anything in motion — spinner frames, ▸ running…, ✦ checking
	Status  Token // status text, the ⛨ containment line
	Bright  Token // headings, the focused row's text, the working label's crest (§10j)
	Subtle  Token // inactive labels (generate UI); no design-system counterpart
	Body    Token // ordinary body text
}

// Palette is the live token set: the full palette above, or the two-grey
// mono palette while mono conformance is on (S-095, mono.go). Every style in
// the product reads it through newStyles, which applyPalette re-runs whenever
// the palette is swapped.
var Palette = FullPalette

// FullPalette is the coloured token set — the palette unless mono is on.
//
// Ten of the fifteen hexes are exactly the 256 index beside them, because the
// cube and the greyscale ramp are colours a design can name. The other five —
// add, del, info, hunk and bright — live in the range a terminal theme owns,
// where 10 is whatever green the user's config says it is and 12 is a blue
// dark enough on some themes to lose a key the interface was offering. Those
// five are the design system's own colours on a truecolor terminal, and the
// index they were chosen for everywhere else.
var FullPalette = ColorTokens{
	Add:     token("#5fd75f", "10", "10"),
	Del:     token("#ff5f5f", "9", "9"),
	AddBg:   token("#005f00", "22", "2"),
	DelBg:   token("#5f0000", "52", "1"),
	Hunk:    token("#5fd7d7", "14", "14"),
	Accent:  token("#ffaf00", "214", "11"),
	Info:    token("#5f87ff", "12", "12"),
	FocusBg: token("#5f5fd7", "62", "12"),
	Dim:     token("#626262", "241", "8"),
	Dimmer:  token("#8a8a8a", "245", "8"),
	Spin:    token("#ff5faf", "205", "13"),
	Status:  token("#767676", "243", "8"),
	Bright:  token("#eaeaea", "15", "15"),
	// Subtle is the one token colors.css has no counterpart for, so its hex
	// is the exact value of 250 rather than a colour chosen for it.
	Subtle: token("#bcbcbc", "250", "7"),
	Body:   token("#d0d0d0", "252", "7"),
}

// tokenColor resolves a token against the terminal's profile the way a style
// does, for the two places that need the escape rather than a lipgloss.Style:
// the lit row's background (§7a) and nothing else so far. Profiles with no
// colour to give return nil, which is the caller's cue to draw the row plain.
func tokenColor(t Token) termenv.Color {
	p := lipgloss.ColorProfile()
	switch p {
	case termenv.TrueColor:
		return p.Color(t.TrueColor)
	case termenv.ANSI256:
		return p.Color(t.ANSI256)
	case termenv.ANSI:
		return p.Color(t.ANSI)
	}
	return nil
}

// Styles is the derived style set: one populated value per theme, built by
// newStyles from the token set and nothing else. Every style this package
// draws with is a field on it, so a theme is a struct to build rather than a
// list of globals to remember to rebuild — which is what the seven
// applyXStyles functions of S-088 were (Finding 2).
//
// The fields are grouped the way the design doc groups them, and the group
// comments are the argument for the assignment; §10a is the table they answer
// to.
type Styles struct {
	Border   lipgloss.Style
	Headline lipgloss.Style
	Hint     lipgloss.Style
	Warn     lipgloss.Style
	Shield   lipgloss.Style
	Dim      lipgloss.Style
	Dimmer   lipgloss.Style
	Status   lipgloss.Style
	Body     lipgloss.Style
	Accent   lipgloss.Style
	Info     lipgloss.Style
	Err      lipgloss.Style
	SpinText lipgloss.Style

	// The reading cursor (§7a): the row it sits on is lit, and the pointer
	// that names it stays outside the highlight.
	FocusRow     lipgloss.Style
	LitText      lipgloss.Style
	FocusPointer lipgloss.Style

	// The diff body (§3): the kind's colour, the intraline tints of §10b,
	// and the context lines the tints sit between.
	Add     lipgloss.Style
	Del     lipgloss.Style
	AddEmph lipgloss.Style
	DelEmph lipgloss.Style
	Hunk    lipgloss.Style
	Context lipgloss.Style

	// The filter row (§4a): what has been typed reads bright against the
	// card, and the run of an option the query named is bold — the one
	// emphasis that costs no colour and survives mono.
	QueryText lipgloss.Style
	Match     lipgloss.Style

	// The scroll gutter (§10g): the track is chrome like every other faint
	// rule, the thumb a step brighter — the same step a sparkline stands off
	// the chrome by, and for the same reason. It is a shape, not a
	// measurement.
	ScrollTrack lipgloss.Style
	ScrollThumb lipgloss.Style

	// The working label's sweep (§10c): the crest of the light that runs
	// along a label in motion, over a base of Spin. It is the second rung of
	// a two-rung ramp and not a colour of its own — under mono it is the same
	// grey as the base, which is how the sweep goes away (§10f).
	AnimCrest lipgloss.Style
}

// sty is the live style set, rebuilt by applyPalette whenever the theme is
// swapped. It is a var rather than a value threaded through View(width)
// because that signature is the components contract (§20); passing the styles
// down is the v2 migration's business, not this file's.
var sty = newStyles(Palette)

// newStyles builds the whole style set from one token set. It reads its
// argument and no global, so a theme can be rendered in a test without
// swapping the one the session is using.
func newStyles(p ColorTokens) Styles {
	return Styles{
		Border:   lipgloss.NewStyle().Foreground(p.Dim),
		Headline: lipgloss.NewStyle().Bold(true).Foreground(p.Info),
		Hint:     lipgloss.NewStyle().Foreground(p.Dim).Italic(true),
		Warn:     lipgloss.NewStyle().Foreground(p.Del),
		Shield:   lipgloss.NewStyle().Foreground(p.Status),
		Dim:      lipgloss.NewStyle().Foreground(p.Dim),
		Dimmer:   lipgloss.NewStyle().Foreground(p.Dimmer),
		Status:   lipgloss.NewStyle().Foreground(p.Status),
		Body:     lipgloss.NewStyle().Foreground(p.Body),
		Accent:   lipgloss.NewStyle().Foreground(p.Accent),
		Info:     lipgloss.NewStyle().Foreground(p.Info),
		Err:      lipgloss.NewStyle().Foreground(p.Del),
		SpinText: lipgloss.NewStyle().Foreground(p.Spin),

		FocusRow:     lipgloss.NewStyle().Bold(true).Background(p.FocusBg),
		LitText:      lipgloss.NewStyle().Foreground(p.Bright).Background(p.FocusBg),
		FocusPointer: lipgloss.NewStyle().Foreground(p.Info),

		Add:     lipgloss.NewStyle().Foreground(p.Add),
		Del:     lipgloss.NewStyle().Foreground(p.Del),
		AddEmph: lipgloss.NewStyle().Foreground(p.Add).Background(p.AddBg),
		DelEmph: lipgloss.NewStyle().Foreground(p.Del).Background(p.DelBg),
		Hunk:    lipgloss.NewStyle().Foreground(p.Hunk),
		Context: lipgloss.NewStyle().Foreground(p.Dimmer),

		QueryText: lipgloss.NewStyle().Foreground(p.Bright),
		Match:     lipgloss.NewStyle().Bold(true),

		ScrollTrack: lipgloss.NewStyle().Foreground(p.Dim),
		ScrollThumb: lipgloss.NewStyle().Foreground(p.Dimmer),

		AnimCrest: lipgloss.NewStyle().Foreground(p.Bright),
	}
}

// applyPalette rebuilds this package's styles from the current Palette, and
// drops the animation's prerendered frames with them (anim.go) — they are
// styled strings, so a frame kept across a swap is a colour from the theme
// the session just left.
func applyPalette() {
	sty = newStyles(Palette)
	clearAnimCache()
}
