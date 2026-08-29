// Package components is the reusable TUI interaction catalog (
// docs/interface/surfaces.md): approval card, diff viewer, selectors, inline
// confirm, activity rows, cockpit rail, and agent list. Components are plain
// state with two methods — Update(tea.KeyPressMsg) (done, result) and
// View(width) string — owned by a host Bubble Tea model via states; they are
// never nested programs and never start goroutines. Esc always dismisses or
// declines, never destroys.
package components

import (
	"image/color"
	"io"
	"os"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
)

// Token is one palette colour, written once for every colour profile a
// terminal can report rather than once and degraded.
//
// A downsampler will happily derive the other two from a hex, and it gets the
// greys right — every rung of the ladder derives to the 256 index written
// beside it. It gets the other two profiles wrong in ways that cost meaning.
// At sixteen colours it is a nearest match, and the nearest match to accent
// (#ffaf00) and to spin (#ff5faf) is del's red: a warning, a thing in motion
// and a failure, all one hue, which is invariant 1 losing its argument on the
// profile invariant 1 exists for. And at 256 it would replace the five tokens
// that are the terminal's own colours *by choice* — add, del, hunk, info,
// bright — with literal approximations of the design system's hexes, taking
// the user's theme out of the five places the design gave it.
//
// So each rung says what it is, and nothing derives anything.
//
// The values are transcribed from tokens/colors.css in the shhh Design System
// project, which states both halves already: a hex, and the 256 index
// it stands for.
//
// Lip Gloss v2 has no CompleteColor and no renderer to degrade through: a
// Style holds a resolved image/color.Color and Render always emits it at full
// fidelity. So a token stays three colours and Color picks the one
// the profile asked for, at the moment the styles are built rather than at
// the moment they are drawn — which is the rule the palette always stated,
// now with somewhere of its own to live.
type Token struct {
	TrueColor, ANSI256, ANSI color.Color
}

// token writes one row of the palette table: the design system's hex, the 256
// index it was chosen for, and the theme colour a 16-colour terminal falls
// back to.
//
// All three rungs go through lipgloss.Color, which reads a number under
// sixteen as one of the terminal's own sixteen and anything above it as an
// index into the 256-colour table. That is the right reading for both index
// rungs: the five tokens whose 256 index is under sixteen — add, del, hunk,
// info, bright — were chosen *as* theme colours, which is why their
// two index columns hold the same number, and a terminal is told so in the
// form that says it.
func token(hex, ansi256, ansi16 string) Token {
	return Token{
		TrueColor: lipgloss.Color(hex),
		ANSI256:   lipgloss.Color(ansi256),
		ANSI:      lipgloss.Color(ansi16),
	}
}

// ColorTokens is the shared palette
// (docs/architecture.md#colour-is-resolved-once-at-the-top) promoted from the
// chat and generate style files, so every TUI surface uses identical tokens.
// No new colors without adding them here.
//
// The assignments below are reconciled with tokens/colors.css in the shhh
// Design System project: same token set, one documented job each.
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
	Accent  Token // tool glyphs, ⚠ warnings, gated modes, ctx ≥70%, and the mutation rail
	Info    Token // sub-agents, block headings, and every key the interface offers
	FocusBg Token // selected option/row background, the cursor block
	Dim     Token // chrome, counts, hints, faint rules, empty meter cells, the scroll gutter's track
	Dimmer  Token // tool output, live tails, detail bodies, sparklines, the scroll gutter's thumb
	Spin    Token // anything in motion — spinner frames, ▸ running…, ✦ checking
	Status  Token // status text, the ⛨ containment line
	Bright  Token // headings, the focused row's text, the working label's crest
	Subtle  Token // inactive labels (generate UI); no design-system counterpart
	Body    Token // ordinary body text
}

// Palette is the live token set: the full palette above, or the two-grey
// mono palette while mono conformance is on (mono.go). Every style in
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

// profile is the colour profile every token resolves against: what the
// terminal shhh is drawing on can actually show. It is detected once, from
// stdout and the environment, and is the same value the program hands Bubble
// Tea, so the writer downsampling on the way out has nothing left to do.
//
// It is a package var rather than a field because the palette it belongs to
// is one too (Palette, above), and for the same reason: the components
// contract is View(width) string, which leaves nowhere to thread a
// theme through. Both are swapped through the same door, applyPalette.
var profile = detectProfile(os.Stdout, os.Environ())

// detectProfile settles the profile for one output and environment. shhh's
// reading of NO_COLOR is stricter than colorprofile's — any non-empty value,
// not a parseable truth, which is what no-color.org actually asks for — and
// TERM=dumb means the same thing here, so both land on ASCII: no colour at
// all, bold and glyphs intact.
func detectProfile(out io.Writer, environ []string) colorprofile.Profile {
	p := colorprofile.Detect(out, environ)
	if monoFromEnviron(environ) && p > colorprofile.ASCII {
		p = colorprofile.ASCII
	}
	return p
}

// Profile is the colour profile the palette is resolving against. The program
// reads it to tell Bubble Tea what it is already drawing for.
func Profile() colorprofile.Profile { return profile }

// SetProfile re-resolves every style against a different profile. It is the
// same door SetMono uses — a token is a colour and a profile together, and
// changing either means the derived styles are stale.
func SetProfile(p colorprofile.Profile) {
	if p == profile {
		return
	}
	profile = p
	applyPalette()
	for _, fn := range paletteHooks {
		fn()
	}
}

// Color is the rung of the token the profile can show — the one value a
// lipgloss.Style can hold, picked at the moment the styles are built. A
// profile with no colour to give returns lipgloss's NoColor, which a Style
// draws by leaving the colour out of the escape entirely rather than by
// writing a default.
//
// It is a method rather than a package function because every host builds its
// own Styles from these tokens (chat, browse, the generate UI) and reads them
// the same way: p.Info.Color() is the token and the profile in one place.
func (t Token) Color() color.Color {
	return lipgloss.Complete(profile)(t.ANSI, t.ANSI256, t.TrueColor)
}

// Styles is the derived style set: one populated value per theme, built by
// newStyles from the token set and nothing else. Every style this package
// draws with is a field on it, so a theme is a struct to build rather than a
// list of globals to remember to rebuild — which is what the seven
// applyXStyles functions were (Finding 2).
//
// The fields are grouped the way the design doc groups them, and the group
// comments are the argument for the assignment; the palette is the table they
// answer to.
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

	// The reading cursor: the row it sits on is lit, and the pointer
	// that names it stays outside the highlight.
	FocusRow     lipgloss.Style
	LitText      lipgloss.Style
	FocusPointer lipgloss.Style

	// The diff body: the kind's colour, the intraline background tints,
	// and the context lines the tints sit between.
	Add     lipgloss.Style
	Del     lipgloss.Style
	AddEmph lipgloss.Style
	DelEmph lipgloss.Style
	Hunk    lipgloss.Style
	Context lipgloss.Style

	// The filter row: what has been typed reads bright against the
	// card, and the run of an option the query named is bold — the one
	// emphasis that costs no colour and survives mono.
	QueryText lipgloss.Style
	Match     lipgloss.Style

	// The scroll gutter: the track is chrome like every other faint
	// rule, the thumb a step brighter — the same step a sparkline stands off
	// the chrome by, and for the same reason. It is a shape, not a
	// measurement.
	ScrollTrack lipgloss.Style
	ScrollThumb lipgloss.Style

	// The working label's sweep: the crest of the light that runs
	// along a label in motion, over a base of Spin. It is the second rung of
	// a two-rung ramp and not a colour of its own — under mono it is the same
	// grey as the base, which is how the sweep goes away.
	AnimCrest lipgloss.Style
}

// sty is the live style set, rebuilt by applyPalette whenever the theme or
// the profile is swapped. It is a var rather than a value threaded through
// View(width) because that signature is the components contract, and
// the v2 migration did not change it: v2 moved the renderer out of the Style,
// which is what made a resolved profile something this package has to own
// , but it left the catalog's own contract alone.
var sty = newStyles(Palette)

// newStyles builds the whole style set from one token set. It reads its
// argument and no global, so a theme can be rendered in a test without
// swapping the one the session is using.
func newStyles(p ColorTokens) Styles {
	return Styles{
		Border:   lipgloss.NewStyle().Foreground(p.Dim.Color()),
		Headline: lipgloss.NewStyle().Bold(true).Foreground(p.Info.Color()),
		Hint:     lipgloss.NewStyle().Foreground(p.Dim.Color()).Italic(true),
		Warn:     lipgloss.NewStyle().Foreground(p.Del.Color()),
		Shield:   lipgloss.NewStyle().Foreground(p.Status.Color()),
		Dim:      lipgloss.NewStyle().Foreground(p.Dim.Color()),
		Dimmer:   lipgloss.NewStyle().Foreground(p.Dimmer.Color()),
		Status:   lipgloss.NewStyle().Foreground(p.Status.Color()),
		Body:     lipgloss.NewStyle().Foreground(p.Body.Color()),
		Accent:   lipgloss.NewStyle().Foreground(p.Accent.Color()),
		Info:     lipgloss.NewStyle().Foreground(p.Info.Color()),
		Err:      lipgloss.NewStyle().Foreground(p.Del.Color()),
		SpinText: lipgloss.NewStyle().Foreground(p.Spin.Color()),

		FocusRow:     lipgloss.NewStyle().Bold(true).Background(p.FocusBg.Color()),
		LitText:      lipgloss.NewStyle().Foreground(p.Bright.Color()).Background(p.FocusBg.Color()),
		FocusPointer: lipgloss.NewStyle().Foreground(p.Info.Color()),

		Add:     lipgloss.NewStyle().Foreground(p.Add.Color()),
		Del:     lipgloss.NewStyle().Foreground(p.Del.Color()),
		AddEmph: lipgloss.NewStyle().Foreground(p.Add.Color()).Background(p.AddBg.Color()),
		DelEmph: lipgloss.NewStyle().Foreground(p.Del.Color()).Background(p.DelBg.Color()),
		Hunk:    lipgloss.NewStyle().Foreground(p.Hunk.Color()),
		Context: lipgloss.NewStyle().Foreground(p.Dimmer.Color()),

		QueryText: lipgloss.NewStyle().Foreground(p.Bright.Color()),
		Match:     lipgloss.NewStyle().Bold(true),

		ScrollTrack: lipgloss.NewStyle().Foreground(p.Dim.Color()),
		ScrollThumb: lipgloss.NewStyle().Foreground(p.Dimmer.Color()),

		AnimCrest: lipgloss.NewStyle().Foreground(p.Bright.Color()),
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
