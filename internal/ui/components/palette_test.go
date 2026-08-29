package components

import (
	"strconv"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// paletteTable is DESIGN-TUI.md §10a written as data: the token, the design
// system's hex, the 256 index it stands for, and the theme colour a
// 16-colour terminal falls back to. The doc is normative, so the test's job
// is to fail when the code drifts from it.
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
// not do: termenv derives a 256 colour by walking the 6×6×6 cube and never
// the greyscale ramp, so body (#d0d0d0) and bright (#eaeaea) both come back
// as 188. Writing all three is what keeps the rungs apart.
func TestPalette_EveryTokenIsWrittenForEveryProfile(t *testing.T) {
	for _, c := range paletteTable {
		if c.token.TrueColor != c.hex {
			t.Errorf("%s: truecolor is %q, §10a says %q", c.name, c.token.TrueColor, c.hex)
		}
		if c.token.ANSI256 != c.ansi256 {
			t.Errorf("%s: 256 index is %q, §10a says %q", c.name, c.token.ANSI256, c.ansi256)
		}
		if c.token.ANSI != c.ansi16 {
			t.Errorf("%s: 16-colour fallback is %q, §10a says %q", c.name, c.token.ANSI, c.ansi16)
		}
	}
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
		profile termenv.Profile
		want    func(i int) string
	}{
		{termenv.ANSI256, func(i int) string { return paletteTable[i].ansi256 }},
		{termenv.TrueColor, func(i int) string { return paletteTable[i].hex }},
	} {
		withColorProfile(t, c.profile)
		for i, tc := range paletteTable {
			got := lipgloss.NewStyle().Foreground(tc.token).Render("x")
			want := lipgloss.NewStyle().Foreground(lipgloss.Color(c.want(i))).Render("x")
			if got != want {
				t.Errorf("%s at %v renders %q, want %q", tc.name, c.profile, got, want)
			}
		}
	}
}

func TestPalette_AHexAloneWouldCollapseTheLadder(t *testing.T) {
	withColorProfile(t, termenv.ANSI256)
	naive := func(c Token) string {
		return lipgloss.NewStyle().Foreground(lipgloss.Color(c.TrueColor)).Render("x")
	}
	if naive(FullPalette.Body) != naive(FullPalette.Bright) {
		t.Skip("termenv now walks the greyscale ramp; the ANSI256 field may be redundant")
	}
	as := func(c Token) string { return lipgloss.NewStyle().Foreground(c).Render("x") }
	if as(FullPalette.Body) == as(FullPalette.Bright) {
		t.Fatal("body and bright must stay two colours at 256, which is what the token's own index is for")
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
		{"truecolor", func(c Token) string { return c.TrueColor }},
		{"256", func(c Token) string { return c.ANSI256 }},
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
// body, body over subtle, and so on down to the chrome. A theme that scrambles
// it would still pass every other check here and be unreadable.
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

// Mono is a token set like any other (§10f): three shades, each written for
// every profile, and every rung of the coloured palette lands on one of them.
func TestPalette_MonoCollapsesOntoItsThreeShades(t *testing.T) {
	for _, c := range []struct {
		name  string
		token Token
	}{{"mono-fg", MonoFg}, {"mono-dim", MonoDim}, {"mono-bg", MonoBg}} {
		if c.token.TrueColor == "" || c.token.ANSI256 == "" || c.token.ANSI == "" {
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

// tokenNamed reads one token out of a palette by the name §10a gives it, so
// the mono check can walk the same table the coloured one does.
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
	if len(c.TrueColor) != 7 {
		t.Fatalf("token %+v has no hex to measure", c)
	}
	r, _ := strconv.ParseUint(c.TrueColor[1:3], 16, 8)
	g, _ := strconv.ParseUint(c.TrueColor[3:5], 16, 8)
	b, _ := strconv.ParseUint(c.TrueColor[5:7], 16, 8)
	return int(299*r+587*g+114*b) / 1000
}
