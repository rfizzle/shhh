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
// values instead of three strings (S-155).
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
