package components

import (
	"image/color"
	"reflect"
	"strconv"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
)

// paletteTable is docs/architecture.md#colour-is-resolved-once-at-the-top
// written as data: the token, the design system's hex, the 256 index it
// stands for, and the theme colour a 16-colour terminal falls back to. The
// doc is normative, so the test's job is to fail when the code drifts from
// it.
var paletteTable = []struct {
	name    string
	token   Token
	hex     string
	ansi256 string
	ansi16  string
}{
	{"add", fullPalette.Add, "#5fd75f", "10", "10"},
	{"del", fullPalette.Del, "#ff5f5f", "9", "9"},
	{"addBg", fullPalette.addBg, "#005f00", "22", "2"},
	{"delBg", fullPalette.delBg, "#5f0000", "52", "1"},
	{"hunk", fullPalette.Hunk, "#5fd7d7", "14", "14"},
	{"accent", fullPalette.Accent, "#ffaf00", "214", "11"},
	{"info", fullPalette.Info, "#5f87ff", "12", "12"},
	{"focusBg", fullPalette.FocusBg, "#5f5fd7", "62", "12"},
	{"band", fullPalette.band, "#1c1c1c", "234", noSixteen},
	{"dim", fullPalette.Dim, "#626262", "241", "8"},
	{"dimmer", fullPalette.Dimmer, "#8a8a8a", "245", "8"},
	{"spin", fullPalette.Spin, "#ff5faf", "205", "13"},
	{"status", fullPalette.Status, "#767676", "243", "8"},
	{"bright", fullPalette.Bright, "#eaeaea", "15", "15"},
	{"subtle", fullPalette.Subtle, "#bcbcbc", "250", "7"},
	{"body", fullPalette.Body, "#d0d0d0", "252", "7"},
	{"code", fullPalette.Code, "#d7af87", "180", "3"},
	{"key", fullPalette.Key, "#8787af", "103", "12"},
}

// noSixteen is the sixteen-colour rung of a token that draws nothing there:
// the band, which at sixteen colours is its padding rows alone. It is the
// empty string because that is what lipgloss.Color reads as no colour.
const noSixteen = ""

// The palette's size is counted here and written nowhere else. The struct
// and the table above are two statements of it — a field added to one and
// not the other is a token some check never walks — and PaletteSize is what
// every sentence that needs the number reads, so both are held to it.
func TestPalette_TheCountIsHeld(t *testing.T) {
	if got := reflect.TypeFor[ColorTokens]().NumField(); got != PaletteSize {
		t.Errorf("ColorTokens has %d tokens and PaletteSize says %d", got, PaletteSize)
	}
	if got := len(paletteTable); got != PaletteSize {
		t.Errorf("the design system's table lists %d tokens and PaletteSize says %d", got, PaletteSize)
	}
}

// The token set is the design system's, at every profile. A hex alone would
// not do: a downsampler derives a 256 colour by walking the 6×6×6 cube and
// never the greyscale ramp, so body (#d0d0d0) and bright (#eaeaea) both come
// back as 188. Writing all three is what keeps the rungs apart.
func TestPalette_EveryTokenIsWrittenForEveryProfile(t *testing.T) {
	for _, c := range paletteTable {
		for _, rung := range []struct {
			what string
			got  color.Color
			want color.Color
			says string
		}{
			{"truecolor", c.token.trueColor, lipgloss.Color(c.hex), c.hex},
			{"256 index", c.token.aNSI256, lipgloss.Color(c.ansi256), c.ansi256},
			{"16-colour fallback", c.token.aNSI, lipgloss.Color(c.ansi16), c.ansi16},
		} {
			if rung.got != rung.want {
				t.Errorf("%s: %s is %s, the palette says %q",
					c.name, rung.what, sgr(rung.got), rung.says)
			}
		}
	}
}

// sgr is the colour as a terminal is actually sent it, which is the only
// comparison that means anything once a token holds three image/color.Color
// values instead of three strings.
func sgr(c color.Color) string {
	return strconv.Quote(ansi.NewStyle().ForegroundColor(c).String())
}

// The values in the table are what a terminal is actually sent: rendered at
// 256 colours a token paints the index it was chosen for, and at truecolor
// the design system's hex — not a re-derivation of one from the other.
//
// The counter-assertion is the point of the type. Body and bright written the
// naive way, as a hex lipgloss degrades, arrive at 256 colours as the same
// colour; written as tokens they do not.
func TestPalette_ProfilesEmitTheDocumentedValue(t *testing.T) {
	for _, c := range []struct {
		profile colorprofile.Profile
		want    func(i int) color.Color
	}{
		{colorprofile.ANSI256, func(i int) color.Color { return lipgloss.Color(paletteTable[i].ansi256) }},
		{colorprofile.TrueColor, func(i int) color.Color { return lipgloss.Color(paletteTable[i].hex) }},
	} {
		withColorProfile(t, c.profile)
		for i, tc := range paletteTable {
			got := lipgloss.NewStyle().Foreground(tc.token.Color()).Render("x")
			want := lipgloss.NewStyle().Foreground(c.want(i)).Render("x")
			if got != want {
				t.Errorf("%s at %v renders %q, want %q", tc.name, c.profile, got, want)
			}
		}
	}
}

// Invariant 1's counter-assertion: the reason a token holds three colours
// rather than one hex for a writer to downsample on the way out.
//
// The 256 rung no longer makes this case. Under v1 it did — termenv derived a
// 256 colour by walking the 6×6×6 cube and never the greyscale ramp, so body
// and bright both came back as 188 — and v2's downsampler walks the ramp, so
// every grey now derives to the index written beside it. What it
// cannot do is the sixteen: derived from their hexes, bright, body and subtle
// all land on the same white, and accent and spin both land on del's red — a
// warning, a thing in motion and a failure told apart by a hue that is no
// longer three hues. The 16-colour rung says which theme colour each of them
// is instead, and that is a decision a nearest-match cannot make.
//
// (The 256 rung earns itself for a different reason, pinned by the table
// above: five tokens — add, del, hunk, info, bright — are the terminal's own
// colours by choice, and deriving them from a hex would replace a theme
// colour with a literal approximation of it.)
func TestPalette_AHexAloneWouldCollapseTheSixteen(t *testing.T) {
	derived := func(c Token) string { return sgr(colorprofile.ANSI.Convert(c.trueColor)) }
	written := func(c Token) string { return sgr(c.aNSI) }
	for _, c := range []struct {
		one, two string
		a, b     Token
	}{
		{"bright", "body", fullPalette.Bright, fullPalette.Body},
		{"accent", "del", fullPalette.Accent, fullPalette.Del},
		{"spin", "del", fullPalette.Spin, fullPalette.Del},
	} {
		if derived(c.a) != derived(c.b) {
			t.Errorf("the downsampler now keeps %s and %s apart at sixteen colours; "+
				"the ANSI rung may no longer be what stops them collapsing", c.one, c.two)
			continue
		}
		if written(c.a) == written(c.b) {
			t.Errorf("%s and %s must stay two colours at sixteen, which is what the token's own ANSI rung is for",
				c.one, c.two)
		}
	}
}

// Invariant 1's other half: two tokens that mean different things must not
// arrive as the same colour, or a state would be told apart by a distinction
// the terminal threw away. Sixteen colours cannot hold six greys, so the
// 16-colour fallback is exempt — that is the profile invariant 1 is for, and
// the glyphs and words carry it there.
func TestPalette_NoTwoTokensCollapse(t *testing.T) {
	for _, field := range []struct {
		what string
		of   func(Token) string
	}{
		{"truecolor", func(c Token) string { return sgr(c.trueColor) }},
		{"256", func(c Token) string { return sgr(c.aNSI256) }},
	} {
		seen := map[string]string{}
		for _, c := range paletteTable {
			v := field.of(c.token)
			if prev, ok := seen[v]; ok {
				t.Errorf("%s: %s and %s are both %s", field.what, prev, c.name, v)
			}
			seen[v] = c.name
		}
	}
}

// The grey ladder is an ordering, not six unrelated greys: bright reads over
// body, body over subtle, and so on down to the chrome. A theme that
// scrambles it would still pass every other check here and be unreadable.
func TestPalette_GreyLadderDescends(t *testing.T) {
	ladder := []struct {
		name  string
		token Token
	}{
		{"bright", fullPalette.Bright},
		{"body", fullPalette.Body},
		{"subtle", fullPalette.Subtle},
		{"dimmer", fullPalette.Dimmer},
		{"status", fullPalette.Status},
		{"dim", fullPalette.Dim},
	}
	for i := 1; i < len(ladder); i++ {
		hi, lo := luminance(t, ladder[i-1].token), luminance(t, ladder[i].token)
		if hi <= lo {
			t.Errorf("%s (%d) does not read over %s (%d)",
				ladder[i-1].name, hi, ladder[i].name, lo)
		}
	}
}

// Mono is a token set like any other: three shades, each written for
// every profile, and every rung of the coloured palette lands on one of them.
func TestPalette_MonoCollapsesOntoItsThreeShades(t *testing.T) {
	for _, c := range []struct {
		name  string
		token Token
	}{{"mono-fg", MonoFg}, {"mono-dim", MonoDim}, {"mono-bg", MonoBg}} {
		if c.token.trueColor == nil || c.token.aNSI256 == nil || c.token.aNSI == nil {
			t.Errorf("%s is not written for every profile: %+v", c.name, c.token)
		}
	}
	shades := map[Token]bool{MonoFg: true, MonoDim: true, MonoBg: true}
	for _, c := range paletteTable {
		if c.name == "band" {
			continue // no ground at all under mono; TestPalette_TheBandIsPaddingAloneWhereItWouldMislead
		}
		got := tokenNamed(monoPalette, c.name)
		if !shades[got] {
			t.Errorf("mono %s is %+v, which is none of the three shades", c.name, got)
		}
	}
}

// The band draws nothing at sixteen colours on any table, and nothing at any
// rung under mono: at sixteen the only grey between the ground and the
// chrome grey would read as chrome, and mono's one background means
// selection. A card there is its padding rows alone.
func TestPalette_TheBandIsPaddingAloneWhereItWouldMislead(t *testing.T) {
	for _, name := range ThemeNames() {
		if name == ThemeAuto {
			continue
		}
		if got := themes[name].tokens.band.aNSI; got != (lipgloss.NoColor{}) {
			t.Errorf("%s theme: the band is %s at sixteen colours, want no ground", name, sgr(got))
		}
	}
	for _, rung := range []color.Color{monoPalette.band.trueColor, monoPalette.band.aNSI256, monoPalette.band.aNSI} {
		if rung != (lipgloss.NoColor{}) {
			t.Errorf("mono: the band is %s, want no ground at every rung", sgr(rung))
		}
	}
}

// tokenNamed reads one token out of a palette by the name the design system
// gives it, so the mono check can walk the same table the coloured one does.
func tokenNamed(p ColorTokens, name string) Token {
	switch name {
	case "add":
		return p.Add
	case "del":
		return p.Del
	case "addBg":
		return p.addBg
	case "delBg":
		return p.delBg
	case "hunk":
		return p.Hunk
	case "accent":
		return p.Accent
	case "info":
		return p.Info
	case "focusBg":
		return p.FocusBg
	case "band":
		return p.band
	case "dim":
		return p.Dim
	case "dimmer":
		return p.Dimmer
	case "spin":
		return p.Spin
	case "status":
		return p.Status
	case "bright":
		return p.Bright
	case "subtle":
		return p.Subtle
	case "body":
		return p.Body
	case "code":
		return p.Code
	case "key":
		return p.Key
	}
	return Token{}
}

// luminance is a plain weighted brightness — enough to order six greys, and
// not pretending to be a contrast model.
func luminance(t *testing.T, c Token) int {
	t.Helper()
	if c.trueColor == nil {
		t.Fatalf("token %+v has no colour to measure", c)
	}
	r, g, b, _ := c.trueColor.RGBA()
	return int(299*uint64(r>>8)+587*uint64(g>>8)+114*uint64(b>>8)) / 1000
}

// lightTable is the light ground's column, written out the same way the dark
// one is so the two are read side by side. The rungs are chosen here rather
// than transcribed (docs/interface/departures.md), and this is what a design
// column reconciles against when there is one.
var lightTable = []struct {
	name    string
	token   Token
	hex     string
	ansi256 string
	ansi16  string
}{
	{"add", LightPalette.Add, "#008700", "2", "2"},
	{"del", LightPalette.Del, "#d70000", "1", "1"},
	{"addBg", LightPalette.addBg, "#d7ffd7", "194", "10"},
	{"delBg", LightPalette.delBg, "#ffd7d7", "224", "9"},
	{"hunk", LightPalette.Hunk, "#008787", "6", "6"},
	{"accent", LightPalette.Accent, "#af5f00", "130", "3"},
	{"info", LightPalette.Info, "#005fd7", "4", "4"},
	{"focusBg", LightPalette.FocusBg, "#d7d7ff", "189", "7"},
	{"band", LightPalette.band, "#e4e4e4", "254", noSixteen},
	{"dim", LightPalette.Dim, "#8a8a8a", "245", "8"},
	{"dimmer", LightPalette.Dimmer, "#6c6c6c", "242", "8"},
	{"spin", LightPalette.Spin, "#af005f", "125", "5"},
	{"status", LightPalette.Status, "#767676", "243", "8"},
	{"bright", LightPalette.Bright, "#121212", "0", "0"},
	{"subtle", LightPalette.Subtle, "#4e4e4e", "239", "8"},
	{"body", LightPalette.Body, "#303030", "236", "0"},
	{"code", LightPalette.Code, "#875f00", "94", "3"},
	{"key", LightPalette.Key, "#5f5f87", "60", "4"},
}

// The light column is written the same way the dark one is: three rungs per
// token and nothing derived. A table that shipped with a hex and no indices
// would be a table that is only right on the terminals that need it least.
func TestPalette_LightIsWrittenForEveryProfile(t *testing.T) {
	for _, c := range lightTable {
		for _, rung := range []struct {
			what string
			got  color.Color
			want color.Color
			says string
		}{
			{"truecolor", c.token.trueColor, lipgloss.Color(c.hex), c.hex},
			{"256 index", c.token.aNSI256, lipgloss.Color(c.ansi256), c.ansi256},
			{"16-colour fallback", c.token.aNSI, lipgloss.Color(c.ansi16), c.ansi16},
		} {
			if rung.got != rung.want {
				t.Errorf("light %s: %s is %s, the palette says %q",
					c.name, rung.what, sgr(rung.got), rung.says)
			}
		}
	}
}

// Every shipped table answers for every job at all three profiles. A
// theme is a token set and not a patch over one, so a table that left a token
// nil would draw that surface in whatever the terminal was last told.
func TestPalette_EveryThemeAnswersForEveryToken(t *testing.T) {
	// The list a reader chooses from and the tables that ship are two
	// places, so they are held together here: a table nobody can name is
	// unreachable, and a name with no table behind it is refused at the door
	// with the words "unknown theme" in front of a word this list offered.
	if len(themes) != len(ThemeNames())-1 {
		t.Errorf("%d tables ship and %d names are offered (auto included)", len(themes), len(ThemeNames()))
	}
	for _, name := range ThemeNames() {
		if name == ThemeAuto {
			continue
		}
		p := themes[name].tokens
		for _, c := range paletteTable {
			tok := tokenNamed(p, c.name)
			if tok.trueColor == nil || tok.aNSI256 == nil || tok.aNSI == nil {
				t.Errorf("%s theme: %s is not written for every profile: %+v", name, c.name, tok)
			}
		}
		if themes[name].ground.trueColor == nil {
			t.Errorf("%s theme has no ground to have been chosen against", name)
		}
	}
}

// Nothing else wears the failure colour at sixteen. Sixteen colours cannot
// hold six greys and the rest of the palette crowds accordingly, but a token
// that arrived as del's red would make a rule's denial, a warning or a thing
// in motion read as a failure on the profile
// docs/interface/principles.md#colour-never-carries-meaning-alone exists for.
func TestPalette_NoThemeSpendsDelsSixteenTwice(t *testing.T) {
	for _, name := range ThemeNames() {
		if name == ThemeAuto {
			continue
		}
		p := themes[name].tokens
		del := sgr(p.Del.aNSI)
		for _, c := range paletteTable {
			if c.name == "del" {
				continue
			}
			if got := sgr(tokenNamed(p, c.name).aNSI); got == del {
				t.Errorf("%s theme: %s is del's %s at sixteen colours", name, c.name, del)
			}
		}
	}
}

// Invariant 1's other half, on every table that ships rather than only the
// first. It is the check a second table needs most: a token derived from
// another rather than chosen — the two intraline tints on the CharmTone table
// are the only derived colours in the product — is exactly the one that can
// come out the same colour as its neighbour without anybody looking at it.
func TestPalette_NoThemeCollapsesTwoTokens(t *testing.T) {
	for _, name := range ThemeNames() {
		if name == ThemeAuto {
			continue
		}
		p := themes[name].tokens
		for _, field := range []struct {
			what string
			of   func(Token) string
		}{
			{"truecolor", func(c Token) string { return sgr(c.trueColor) }},
			{"256", func(c Token) string { return sgr(c.aNSI256) }},
		} {
			seen := map[string]string{}
			for _, c := range paletteTable {
				v := field.of(tokenNamed(p, c.name))
				if prev, ok := seen[v]; ok {
					t.Errorf("%s theme, %s: %s and %s are both %s", name, field.what, prev, c.name, v)
				}
				seen[v] = c.name
			}
		}
	}
}

// The two derived colours in the product have to stay in the hue family they
// were derived from: an addition's ground is green and a deletion's is red,
// and a derivation that took the short way round the hue circle would hand
// back two slates that mean the same thing.
func TestPalette_TheDerivedTintsKeepTheirHue(t *testing.T) {
	for _, c := range []struct {
		name         string
		token        Token
		lead, second func(r, g, b uint32) uint32
	}{
		{"addBg", charmPalette.addBg,
			func(_, g, _ uint32) uint32 { return g },
			func(r, _, _ uint32) uint32 { return r }},
		{"delBg", charmPalette.delBg,
			func(r, _, _ uint32) uint32 { return r },
			func(_, g, _ uint32) uint32 { return g }},
	} {
		r, g, b, _ := c.token.trueColor.RGBA()
		if c.lead(r, g, b) <= c.second(r, g, b) {
			t.Errorf("%s is #%02x%02x%02x, which is not the hue it was derived from",
				c.name, r>>8, g>>8, b>>8)
		}
	}
}

// The grey ladder holds on the light ground too, upside down: what reads
// hardest is what is furthest from the ground, so the order that descends in
// luminance on black ascends on white. Dim and Dimmer swapping their hexes
// between the two tables is this rule and not a slip — dim is the one nearer
// the ground on both.
func TestPalette_LightGreyLadderAscends(t *testing.T) {
	ladder := []struct {
		name  string
		token Token
	}{
		{"bright", LightPalette.Bright},
		{"body", LightPalette.Body},
		{"subtle", LightPalette.Subtle},
		{"dimmer", LightPalette.Dimmer},
		{"status", LightPalette.Status},
		{"dim", LightPalette.Dim},
	}
	for i := 1; i < len(ladder); i++ {
		lo, hi := luminance(t, ladder[i-1].token), luminance(t, ladder[i].token)
		if lo >= hi {
			t.Errorf("%s (%d) does not read over %s (%d) on a light ground",
				ladder[i-1].name, lo, ladder[i].name, hi)
		}
	}
	if luminance(t, LightPalette.Dim) <= luminance(t, LightPalette.Dimmer) {
		t.Error("dim must be the lighter of the two chrome greys on a light ground")
	}
	if luminance(t, fullPalette.Dim) >= luminance(t, fullPalette.Dimmer) {
		t.Error("dim must be the darker of the two chrome greys on a dark ground")
	}
}

// themeRestore puts the palette back the way the test found it: these are
// package globals by design, and a test that left one swapped would be
// deciding the colours of every test after it.
func themeRestore(t *testing.T) {
	t.Helper()
	name, wasMono, wasPaint := themeName, mono, paintGround
	wasLight := lightGround
	t.Cleanup(func() {
		// The four settle in this order for one reason: mono outranks a
		// theme, so the unconditional swap has to be the one that does not
		// run while the two greys are up. Reverse it and a package that
		// found mono on comes back with mono still reported on and the
		// coloured table underneath it.
		themeName, lightGround, paintGround = name, wasLight, wasPaint
		SetMono(wasMono)
		if !mono {
			swapPalette(activeTokens())
		}
	})
}

// A theme reaches the styles through the door mono uses, and mono outranks
// it: a table asked for while the two greys are up is remembered rather than
// drawn, and is what comes back when they go down.
func TestPalette_TheThemeGoesThroughMonosDoor(t *testing.T) {
	themeRestore(t)
	SetMono(false)

	if err := SetTheme(ThemeLight); err != nil {
		t.Fatalf("light is a shipped table: %v", err)
	}
	if Palette != LightPalette {
		t.Error("the light table was asked for and is not the one being drawn with")
	}
	if sty.body.GetForeground() != LightPalette.Body.Color() {
		t.Error("the derived styles did not rebuild on the swapped table")
	}

	SetMono(true)
	if Palette != monoPalette {
		t.Error("mono outranks the theme")
	}
	if err := SetTheme(ThemeCharm); err != nil {
		t.Fatalf("charm is a shipped table: %v", err)
	}
	if Palette != monoPalette {
		t.Error("a theme asked for under mono must not paint over the two greys")
	}
	SetMono(false)
	if Palette != charmPalette {
		t.Error("the theme asked for under mono is what comes back when mono goes off")
	}

	if err := SetTheme("solarized"); err == nil {
		t.Error("a name no table answers to must be refused, not fallen back from")
	}
	if Palette != charmPalette {
		t.Error("a refused name changed the palette")
	}
}

// The auto theme is the terminal's answer, and only the auto theme is. A
// reader who named a table gets that table on whatever ground they are on.
func TestPalette_AutoFollowsTheGroundAndANamedThemeDoesNot(t *testing.T) {
	themeRestore(t)
	SetMono(false)
	if err := SetTheme(ThemeAuto); err != nil {
		t.Fatal(err)
	}

	if !SetGround(false) {
		t.Error("a light ground under the auto theme is a palette swap the host has to repaint for")
	}
	if Palette != LightPalette {
		t.Error("auto did not follow the terminal onto the light table")
	}
	if SetGround(false) {
		t.Error("the same answer twice is not a swap")
	}
	if !SetGround(true) {
		t.Error("a terminal that changed its background is a swap back")
	}
	if Palette != fullPalette {
		t.Error("auto did not follow the terminal back onto the dark table")
	}

	if err := SetTheme(ThemeDark); err != nil {
		t.Fatal(err)
	}
	if SetGround(false) {
		t.Error("a named theme does not move when the terminal answers")
	}
	if Palette != fullPalette {
		t.Error("a named theme was overruled by the ground")
	}
}

// The light ground is offered and never assumed: nothing is painted until it
// is asked for, and under mono there is nothing to paint — a third shade is
// exactly what two greys have given up.
func TestPalette_TheGroundIsOfferedNeverTheDefault(t *testing.T) {
	themeRestore(t)
	withColorProfile(t, colorprofile.ANSI256)
	SetMono(false)
	if err := SetTheme(ThemeLight); err != nil {
		t.Fatal(err)
	}
	if GroundColor() != nil {
		t.Error("a theme must not repaint the terminal's own background by default")
	}
	if !PaintGround(true) {
		t.Error("the switch reports what it changed")
	}
	if got := GroundColor(); got != themes[ThemeLight].ground.Color() {
		t.Errorf("the painted ground is %v, want the light table's own", got)
	}
	if PaintGround(true) {
		t.Error("the switch is idempotent")
	}
	SetMono(true)
	if GroundColor() != nil {
		t.Error("mono has no ground to paint")
	}
}

// The dark table paints the ground it was chosen against unless the reader
// turns it off, because its band is half of the catalogue's pair with that
// ground: #1c1c1c on #0f1117, 234 on 233, and no step in between. The light
// and CharmTone tables keep the terminal's own until asked, and a profile
// with no rung for the ground paints nothing.
func TestPalette_TheDarkGroundIsPaintedByDefault(t *testing.T) {
	themeRestore(t)
	SetMono(false)
	cases := []struct {
		profile      colorprofile.Profile
		ground, band color.Color
	}{
		{colorprofile.TrueColor, lipgloss.Color("#0f1117"), lipgloss.Color("#1c1c1c")},
		{colorprofile.ANSI256, lipgloss.Color("233"), lipgloss.Color("234")},
		{colorprofile.ANSI, nil, lipgloss.NoColor{}},
	}
	for _, theme := range []string{ThemeAuto, ThemeDark} {
		if err := SetTheme(theme); err != nil {
			t.Fatal(err)
		}
		if !GroundPainted() {
			t.Fatalf("%s: the dark ground is not painted by default", theme)
		}
		for _, c := range cases {
			withColorProfile(t, c.profile)
			if got := GroundColor(); !sameColor(got, c.ground) {
				t.Errorf("%s at %v: the ground is %v, want %v", theme, c.profile, got, c.ground)
			}
			if got := cardBand().Color(); !sameColor(got, c.band) {
				t.Errorf("%s at %v: the card's band is %v, want %v and no step", theme, c.profile, got, c.band)
			}
		}
	}

	withColorProfile(t, colorprofile.ANSI256)
	for _, theme := range []string{ThemeLight, ThemeCharm} {
		if err := SetTheme(theme); err != nil {
			t.Fatal(err)
		}
		if GroundPainted() || GroundColor() != nil {
			t.Errorf("%s: the terminal's own ground is overpainted by default", theme)
		}
	}

	if err := SetTheme(ThemeDark); err != nil {
		t.Fatal(err)
	}
	if !PaintGround(false) {
		t.Error("turning the dark ground off reports no change")
	}
	if GroundColor() != nil {
		t.Error("the switch is off and the dark ground is still painted")
	}
	if err := SetTheme(ThemeLight); err != nil {
		t.Fatal(err)
	}
	if err := SetTheme(ThemeDark); err != nil {
		t.Fatal(err)
	}
	if GroundPainted() {
		t.Error("the reader's off did not outlast a change of theme")
	}
	if !PaintGround(true) || !GroundPainted() {
		t.Error("the switch does not turn the dark ground back on")
	}

	SetMono(true)
	if GroundColor() != nil {
		t.Error("mono has no ground to paint")
	}
}
