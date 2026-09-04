package components

import (
	"image/color"
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
	{"add", FullPalette.Add, "#5fd75f", "10", "10"},
	{"del", FullPalette.Del, "#ff5f5f", "9", "9"},
	{"addBg", FullPalette.AddBg, "#005f00", "22", "2"},
	{"delBg", FullPalette.DelBg, "#5f0000", "52", "1"},
	{"hunk", FullPalette.Hunk, "#5fd7d7", "14", "14"},
	{"accent", FullPalette.Accent, "#ffaf00", "214", "11"},
	{"info", FullPalette.Info, "#5f87ff", "12", "12"},
	{"focusBg", FullPalette.FocusBg, "#5f5fd7", "62", "12"},
	{"dim", FullPalette.Dim, "#626262", "241", "8"},
	{"dimmer", FullPalette.Dimmer, "#8a8a8a", "245", "8"},
	{"spin", FullPalette.Spin, "#ff5faf", "205", "13"},
	{"status", FullPalette.Status, "#767676", "243", "8"},
	{"bright", FullPalette.Bright, "#eaeaea", "15", "15"},
	{"subtle", FullPalette.Subtle, "#bcbcbc", "250", "7"},
	{"body", FullPalette.Body, "#d0d0d0", "252", "7"},
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
			{"truecolor", c.token.TrueColor, lipgloss.Color(c.hex), c.hex},
			{"256 index", c.token.ANSI256, lipgloss.Color(c.ansi256), c.ansi256},
			{"16-colour fallback", c.token.ANSI, lipgloss.Color(c.ansi16), c.ansi16},
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
	derived := func(c Token) string { return sgr(colorprofile.ANSI.Convert(c.TrueColor)) }
	written := func(c Token) string { return sgr(c.ANSI) }
	for _, c := range []struct {
		one, two string
		a, b     Token
	}{
		{"bright", "body", FullPalette.Bright, FullPalette.Body},
		{"accent", "del", FullPalette.Accent, FullPalette.Del},
		{"spin", "del", FullPalette.Spin, FullPalette.Del},
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
		{"truecolor", func(c Token) string { return sgr(c.TrueColor) }},
		{"256", func(c Token) string { return sgr(c.ANSI256) }},
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
		{"bright", FullPalette.Bright},
		{"body", FullPalette.Body},
		{"subtle", FullPalette.Subtle},
		{"dimmer", FullPalette.Dimmer},
		{"status", FullPalette.Status},
		{"dim", FullPalette.Dim},
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
		if c.token.TrueColor == nil || c.token.ANSI256 == nil || c.token.ANSI == nil {
			t.Errorf("%s is not written for every profile: %+v", c.name, c.token)
		}
	}
	shades := map[Token]bool{MonoFg: true, MonoDim: true, MonoBg: true}
	for _, c := range paletteTable {
		got := tokenNamed(MonoPalette, c.name)
		if !shades[got] {
			t.Errorf("mono %s is %+v, which is none of the three shades", c.name, got)
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
		return p.AddBg
	case "delBg":
		return p.DelBg
	case "hunk":
		return p.Hunk
	case "accent":
		return p.Accent
	case "info":
		return p.Info
	case "focusBg":
		return p.FocusBg
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
	}
	return Token{}
}

// luminance is a plain weighted brightness — enough to order six greys, and
// not pretending to be a contrast model.
func luminance(t *testing.T, c Token) int {
	t.Helper()
	if c.TrueColor == nil {
		t.Fatalf("token %+v has no colour to measure", c)
	}
	r, g, b, _ := c.TrueColor.RGBA()
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
	{"addBg", LightPalette.AddBg, "#d7ffd7", "194", "10"},
	{"delBg", LightPalette.DelBg, "#ffd7d7", "224", "9"},
	{"hunk", LightPalette.Hunk, "#008787", "6", "6"},
	{"accent", LightPalette.Accent, "#af5f00", "130", "3"},
	{"info", LightPalette.Info, "#005fd7", "4", "4"},
	{"focusBg", LightPalette.FocusBg, "#d7d7ff", "189", "7"},
	{"dim", LightPalette.Dim, "#8a8a8a", "245", "8"},
	{"dimmer", LightPalette.Dimmer, "#6c6c6c", "242", "8"},
	{"spin", LightPalette.Spin, "#af005f", "125", "5"},
	{"status", LightPalette.Status, "#767676", "243", "8"},
	{"bright", LightPalette.Bright, "#121212", "0", "0"},
	{"subtle", LightPalette.Subtle, "#4e4e4e", "239", "8"},
	{"body", LightPalette.Body, "#303030", "236", "0"},
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
			{"truecolor", c.token.TrueColor, lipgloss.Color(c.hex), c.hex},
			{"256 index", c.token.ANSI256, lipgloss.Color(c.ansi256), c.ansi256},
			{"16-colour fallback", c.token.ANSI, lipgloss.Color(c.ansi16), c.ansi16},
		} {
			if rung.got != rung.want {
				t.Errorf("light %s: %s is %s, the palette says %q",
					c.name, rung.what, sgr(rung.got), rung.says)
			}
		}
	}
}

// Every shipped table answers for all fifteen jobs at all three profiles. A
// theme is a token set and not a patch over one, so a table that left a token
// nil would draw that surface in whatever the terminal was last told.
func TestPalette_EveryThemeAnswersForTheFifteen(t *testing.T) {
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
			if tok.TrueColor == nil || tok.ANSI256 == nil || tok.ANSI == nil {
				t.Errorf("%s theme: %s is not written for every profile: %+v", name, c.name, tok)
			}
		}
		if themes[name].ground.TrueColor == nil {
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
		del := sgr(p.Del.ANSI)
		for _, c := range paletteTable {
			if c.name == "del" {
				continue
			}
			if got := sgr(tokenNamed(p, c.name).ANSI); got == del {
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
			{"truecolor", func(c Token) string { return sgr(c.TrueColor) }},
			{"256", func(c Token) string { return sgr(c.ANSI256) }},
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
		{"addBg", CharmPalette.AddBg,
			func(_, g, _ uint32) uint32 { return g },
			func(r, _, _ uint32) uint32 { return r }},
		{"delBg", CharmPalette.DelBg,
			func(r, _, _ uint32) uint32 { return r },
			func(_, g, _ uint32) uint32 { return g }},
	} {
		r, g, b, _ := c.token.TrueColor.RGBA()
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
	if luminance(t, FullPalette.Dim) >= luminance(t, FullPalette.Dimmer) {
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
	if sty.Body.GetForeground() != LightPalette.Body.Color() {
		t.Error("the derived styles did not rebuild on the swapped table")
	}

	SetMono(true)
	if Palette != MonoPalette {
		t.Error("mono outranks the theme")
	}
	if err := SetTheme(ThemeCharm); err != nil {
		t.Fatalf("charm is a shipped table: %v", err)
	}
	if Palette != MonoPalette {
		t.Error("a theme asked for under mono must not paint over the two greys")
	}
	SetMono(false)
	if Palette != CharmPalette {
		t.Error("the theme asked for under mono is what comes back when mono goes off")
	}

	if err := SetTheme("solarized"); err == nil {
		t.Error("a name no table answers to must be refused, not fallen back from")
	}
	if Palette != CharmPalette {
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
	if Palette != FullPalette {
		t.Error("auto did not follow the terminal back onto the dark table")
	}

	if err := SetTheme(ThemeDark); err != nil {
		t.Fatal(err)
	}
	if SetGround(false) {
		t.Error("a named theme does not move when the terminal answers")
	}
	if Palette != FullPalette {
		t.Error("a named theme was overruled by the ground")
	}
}

// The ground is offered and never assumed: nothing is painted until it is
// asked for, and under mono there is nothing to paint — a third shade is
// exactly what two greys have given up.
func TestPalette_TheGroundIsOfferedNeverTheDefault(t *testing.T) {
	themeRestore(t)
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
