// Package components is the reusable TUI interaction catalog (
// docs/interface/surfaces.md): approval card, diff viewer, selectors, inline
// confirm, activity rows, cockpit rail, and agent list. Components are plain
// state with two methods — Update(tea.KeyPressMsg) (done, result) and
// View(width) string — owned by a host Bubble Tea model via states; they are
// never nested programs and never start goroutines. Esc always dismisses or
// declines, never destroys.
package components

import (
	"fmt"
	"image/color"
	"io"
	"os"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/exp/charmtone"
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

// Palette is the live token set: whichever of the shipped tables the theme
// resolves to, or the two-grey mono palette while mono conformance is on
// (mono.go). Every style in the product reads it through newStyles, which
// applyPalette re-runs whenever the palette is swapped.
var Palette = FullPalette

// FullPalette is the coloured token set chosen against a dark ground — the
// palette unless mono is on or another theme was asked for.
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

// LightPalette is the same fifteen jobs on a light ground.
//
// A table is chosen against a ground and is legible only on it
// (docs/interface/principles.md#a-colour-is-three-values-and-a-ground): body
// text at #d0d0d0 is the most readable thing on a black terminal and almost
// nothing on a white one, while the chrome greys — chosen to sit *below* the
// body on a dark ground — come out above it. So this is not the dark table
// lightened; it is the same fifteen decisions taken again with the ground
// the other way up.
//
// Three things carry over unchanged, because they are not about the ground:
//
//   - The three rungs. Every token still says what it is at truecolor, at
//     256 and at sixteen, and nothing derives anything.
//   - The five that are the terminal's own — add, del, hunk, info and
//     bright. A user's own red is still the red their diffs are deleted in;
//     what changes is which end of their theme is asked for, since on a
//     light ground the readable half of sixteen colours is the un-bolded
//     half and 15 is invisible where 0 is not.
//   - Ten hexes that are exactly the 256 index beside them, for the same
//     reason: the cube and the greyscale ramp are colours a design can name.
//
// One thing is deliberately inverted. Dim and Dimmer swap their relative
// weight: Dim is chrome and Dimmer is content, so Dimmer is always the rung
// with more contrast against the ground — which on a dark terminal makes it
// the *lighter* grey and on a light one the *darker* one. #8a8a8a is Dimmer
// in the table above and Dim here, and that is the swap rather than a
// mistake. Reverse it and the scroll gutter's thumb sinks into its own track
// exactly as it would if the two were exchanged on the dark ground.
//
// The chrome grey stands off white by 3.45:1, which is what #626262 stands
// off black by (3.44:1) — the faintest thing on the screen is equally faint
// on both grounds, and the body is equally readable.
var LightPalette = ColorTokens{
	Add:     token("#008700", "2", "2"),
	Del:     token("#d70000", "1", "1"),
	AddBg:   token("#d7ffd7", "194", "10"),
	DelBg:   token("#ffd7d7", "224", "9"),
	Hunk:    token("#008787", "6", "6"),
	Accent:  token("#af5f00", "130", "3"),
	Info:    token("#005fd7", "4", "4"),
	FocusBg: token("#d7d7ff", "189", "7"),
	Dim:     token("#8a8a8a", "245", "8"),
	Dimmer:  token("#6c6c6c", "242", "8"),
	Spin:    token("#af005f", "125", "5"),
	Status:  token("#767676", "243", "8"),
	Bright:  token("#121212", "0", "0"),
	// Subtle has no counterpart to reconcile with on either ground, so its
	// hex is the exact value of 239.
	Subtle: token("#4e4e4e", "239", "8"),
	Body:   token("#303030", "236", "0"),
}

// CharmPalette is the fifteen jobs done in CharmTone, the palette the
// libraries this interface is built on are drawn in. It is a dark table like
// the first one and it is not a variant of it: every hue is picked from the
// published set rather than approximated, which is why it exists at all —
// a theme that only shifted the greys would be a preference, and this one is
// a different set of colours doing the same fifteen jobs.
//
// The five that defer to the terminal on the other two tables do not defer
// here. A named palette that handed its green back to whatever the user's
// config says is not that palette, so every token carries a real 256 index
// and the sixteen-colour rung is the only place the terminal's own theme is
// still spent.
var CharmPalette = ColorTokens{
	Add:     tone(charmtone.Guac, "42", "10"),
	Del:     tone(charmtone.Coral, "203", "9"),
	AddBg:   tint(charmtone.Guac, "22", "2"),
	DelBg:   tint(charmtone.Coral, "52", "1"),
	Hunk:    tone(charmtone.Turtle, "44", "14"),
	Accent:  tone(charmtone.Tang, "209", "11"),
	Info:    tone(charmtone.Malibu, "39", "12"),
	FocusBg: tone(charmtone.Charple, "63", "12"),
	Dim:     tone(charmtone.Iron, "239", "8"),
	Dimmer:  tone(charmtone.Squid, "245", "8"),
	Spin:    tone(charmtone.Cheeky, "212", "13"),
	Status:  tone(charmtone.Oyster, "241", "8"),
	Bright:  tone(charmtone.Salt, "255", "15"),
	Subtle:  tone(charmtone.Smoke, "250", "7"),
	Body:    tone(charmtone.Ash, "253", "7"),
}

// tone writes one row of the CharmTone table: the published hex, and the two
// indices it stands for on a terminal that cannot show it.
func tone(k charmtone.Key, ansi256, ansi16 string) Token {
	return token(k.Hex(), ansi256, ansi16)
}

// tint is tone for the two intraline backgrounds, which CharmTone has no
// colour for: it is a foreground set, and every entry in it is a colour meant
// to be read rather than read against.
//
// Mixing the hue into the set's own ground is the obvious derivation and it
// is wrong here. The ground is a dark purple, the mix runs through hue rather
// than through light, and the short way round from purple to CharmTone's
// green is blue: an eighth of the way out of it is a slate that says nothing
// about an addition, and the deletion's eighth is a slate a shade to the left
// of it. Two tints told apart only by a reader who has both on screen at once
// is the one thing an intraline background must not be.
//
// So the hue keeps its own hue and gives up light instead: three eighths of
// it, which is where the first table's tints sit — its green is 95 of 255,
// and that is the weight a ground can carry without taking the row's text
// with it. The two index rungs are the ones that table chose, because a dark
// green and a dark red are the whole of what those two columns can say.
func tint(k charmtone.Key, ansi256, ansi16 string) Token {
	r, g, b, _ := k.RGBA()
	darken := func(v uint32) uint8 { return uint8(v >> 8 * 3 / 8) }
	return Token{
		TrueColor: color.RGBA{R: darken(r), G: darken(g), B: darken(b), A: 0xff},
		ANSI256:   lipgloss.Color(ansi256),
		ANSI:      lipgloss.Color(ansi16),
	}
}

// The theme names, which are what `/ui theme` takes and what appearance.theme
// holds. ThemeAuto is the one that is not a table: it is whichever of dark and
// light matches the ground the terminal reported, and it is the default,
// because a terminal that never answers keeps the table the product has
// always had.
const (
	ThemeAuto  = "auto"
	ThemeDark  = "dark"
	ThemeLight = "light"
	ThemeCharm = "charm"
)

// theme is one shipped table and the ground it was chosen against. The table
// is the whole of the theme — fifteen tokens, no more, so a surface cannot
// reach for a colour a theme forgot to bring — and the ground is beside it
// rather than in it because nothing draws with it: it is what the screen
// would be painted with if the reader asked for that (GroundColor), and the
// terminal's own otherwise.
type theme struct {
	tokens ColorTokens
	ground Token
}

// themes is every table that ships. A theme is added by adding a row here and
// a word to ThemeNames; nothing else in the product knows how many there are.
var themes = map[string]theme{
	ThemeDark:  {FullPalette, token("#1c1c1c", "234", "0")},
	ThemeLight: {LightPalette, token("#ffffff", "231", "15")},
	ThemeCharm: {CharmPalette, tone(charmtone.Pepper, "235", "0")},
}

// ThemeNames is the words a reader may choose between, auto first because it
// is the default and the one that needs no decision.
func ThemeNames() []string {
	return []string{ThemeAuto, ThemeDark, ThemeLight, ThemeCharm}
}

var (
	// themeName is what was asked for, which is not always what is drawn:
	// mono outranks it and auto resolves through the ground below.
	themeName = ThemeAuto
	// lightGround is what the terminal said about its own background. False
	// until it answers, which is the same answer as a dark terminal — and
	// deliberately so, since the table that stands while nobody has answered
	// should be the one that was right for every terminal before the
	// question was asked.
	lightGround bool
	// paintGround is the switch, off by default. See GroundColor.
	paintGround bool
)

// ThemeName is the theme that was asked for, by name.
func ThemeName() string { return themeName }

// SetTheme swaps the table every surface draws with, through the same door
// SetMono uses. An unknown name is refused and nothing changes: a themes
// table that silently fell back would leave the reader looking at the old
// colours and reading their own configuration to find out why.
//
// Mono outranks it. A theme asked for while mono is on is remembered and
// takes effect when mono is turned off, which is the same bargain the full
// palette has always had.
func SetTheme(name string) error {
	if name == "" {
		name = ThemeAuto
	}
	if _, ok := themes[resolveTheme(name)]; !ok {
		return fmt.Errorf("unknown theme %q (%s)", name, strings.Join(ThemeNames(), ", "))
	}
	if name == themeName {
		return nil
	}
	themeName = name
	if !mono {
		swapPalette(activeTokens())
	}
	return nil
}

// SetGround records what the terminal said its own background is and reports
// whether the palette moved because of it — which it does only under the auto
// theme, and only when the answer is new. A host that draws from a cache
// invalidates it on true.
//
// A terminal is asked once and may answer more than once (a re-attach, a
// theme changed under a running session), so this is idempotent rather than
// one-shot.
func SetGround(dark bool) bool {
	if lightGround == !dark {
		return false
	}
	lightGround = !dark
	if mono || themeName != ThemeAuto {
		return false
	}
	swapPalette(activeTokens())
	return true
}

// PaintGround turns the theme's own background on for the whole screen, and
// reports whether that changed anything. See GroundColor for why it is a
// switch.
func PaintGround(on bool) bool {
	if on == paintGround {
		return false
	}
	paintGround = on
	return true
}

// GroundPainted reports whether the screen is painted with the theme's
// background rather than left on the terminal's own.
func GroundPainted() bool { return paintGround }

// GroundColor is the colour the whole screen is painted with, or nil for the
// terminal's own — which is the default and stays it.
//
// A theme repaints the ground it was chosen against, and doing that by
// default would take a decision the reader already made: the terminal's
// background is theirs, it is what every other program on that screen sits
// on, and a session that overpainted it would be the one window that does not
// match. So it is offered — a light theme on a dark terminal is legible
// either way, and only the reader knows whether they wanted shhh to look like
// their terminal or like itself.
//
// Under mono there is nothing to paint: a third shade is exactly what two
// greys have given up.
func GroundColor() color.Color {
	if !paintGround || mono {
		return nil
	}
	return themes[resolveTheme(themeName)].ground.Color()
}

// resolveTheme is the table a name names. Everything but auto names itself.
func resolveTheme(name string) string {
	if name != ThemeAuto {
		return name
	}
	if lightGround {
		return ThemeLight
	}
	return ThemeDark
}

// activeTokens is the token set the current theme resolves to, ignoring mono
// — SetMono owns that question and asks this one on the way back out.
func activeTokens() ColorTokens { return themes[resolveTheme(themeName)].tokens }

// swapPalette installs a token set and rebuilds every derived style, here and
// in every host that registered with OnPaletteChange. It is the one door:
// mono, the theme, the terminal's own ground and the colour profile all reach
// the styles through it, so a swap can never rebuild half the product.
func swapPalette(p ColorTokens) {
	Palette = p
	applyPalette()
	for _, fn := range paletteHooks {
		fn()
	}
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

// DetectProfile settles the profile for a stream other than the one the
// palette was built against — the plain-text reports, which are written to
// whichever stream a command was handed and may be a pipe while stdout is a
// terminal. It is exported so those reports read NO_COLOR exactly the way the
// palette does rather than the way colorprofile does on its own.
func DetectProfile(out io.Writer, environ []string) colorprofile.Profile {
	return detectProfile(out, environ)
}

// SetProfile re-resolves every style against a different profile. It is the
// same door SetMono uses — a token is a colour and a profile together, and
// changing either means the derived styles are stale.
func SetProfile(p colorprofile.Profile) {
	if p == profile {
		return
	}
	profile = p
	swapPalette(Palette)
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

		// The names read backwards and the pair is right: Dimmer is the
		// lighter rung, Dim the darker one, so the thumb is the brighter of
		// the two. Swap them to "fix" the naming and the thumb sinks into its
		// own track — the gutter still draws, and stops saying where in the
		// transcript the reader is. Only the stroke survives mono, where both
		// rungs are one grey.
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
