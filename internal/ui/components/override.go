package components

// The theme file's overrides. A person names a shipped table and changes the
// tokens that do not work for them by hex; the shipped tables are never
// touched, the overrides are laid over whichever table is in effect each time
// the palette is chosen (activeTokens, GroundColor), so `/ui theme`, mono
// toggling and the terminal's ground answer all keep them, and mono, which
// never asks for a table, outranks them.
// See docs/capabilities/configuration.md#the-theme-file and
// docs/interface/departures.md#a-theme-file-override-keeps-the-other-rungs.

import (
	"fmt"
	"image/color"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
)

// TokenOverride is one token the theme file changes: its truecolor hex, and
// the 256-colour and sixteen-colour rungs where the file gives them. A rung
// the file leaves empty is the shipped table's own for that token, because
// nothing derives anything: the hex is the only value the file author chose
// unless they say more.
type TokenOverride struct {
	Token   string
	Hex     string
	ANSI256 string
	ANSI16  string
}

// overrideGround is the file's name for the theme's ground, which is not a
// field of ColorTokens.
const overrideGround = "ground"

// overrideFields maps each name a file may use to the token it sets. The
// fields stay unexported: this table is the setter.
var overrideFields = map[string]func(*ColorTokens) *Token{
	"add":      func(p *ColorTokens) *Token { return &p.Add },
	"del":      func(p *ColorTokens) *Token { return &p.Del },
	"add_bg":   func(p *ColorTokens) *Token { return &p.addBg },
	"del_bg":   func(p *ColorTokens) *Token { return &p.delBg },
	"hunk":     func(p *ColorTokens) *Token { return &p.Hunk },
	"accent":   func(p *ColorTokens) *Token { return &p.Accent },
	"info":     func(p *ColorTokens) *Token { return &p.Info },
	"focus_bg": func(p *ColorTokens) *Token { return &p.FocusBg },
	"band":     func(p *ColorTokens) *Token { return &p.band },
	"dim":      func(p *ColorTokens) *Token { return &p.Dim },
	"dimmer":   func(p *ColorTokens) *Token { return &p.Dimmer },
	"spin":     func(p *ColorTokens) *Token { return &p.Spin },
	"status":   func(p *ColorTokens) *Token { return &p.Status },
	"bright":   func(p *ColorTokens) *Token { return &p.Bright },
	"subtle":   func(p *ColorTokens) *Token { return &p.Subtle },
	"body":     func(p *ColorTokens) *Token { return &p.Body },
	"code":     func(p *ColorTokens) *Token { return &p.Code },
	"key":      func(p *ColorTokens) *Token { return &p.Key },
}

// overrideInks are the tokens that are text, held to the floor on the ground
// and the band: the same set TestPalette_EveryThemeClearsAA measures.
var overrideInks = []string{"dim", "dimmer", "status", "subtle", "body", "bright",
	"add", "del", "hunk", "accent", "info", "spin", "code", "key"}

// OverrideTokenNames is the names a theme file may override: the palette's
// own tokens, the two tints and the band, and the ground.
func OverrideTokenNames() []string {
	names := []string{overrideGround}
	for n := range overrideFields {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

// overrides is what the theme file asked for, laid over every table.
var overrides []TokenOverride

// SetOverrides lays the file's overrides over whichever table is in effect and
// redraws, unless mono is on, which outranks them and has no table to lay
// them over. A name or a hex it cannot read is refused and nothing changes.
func SetOverrides(o []TokenOverride) error {
	for _, ov := range o {
		if _, ok := parseHex(ov.Hex); !ok {
			return fmt.Errorf("%s: %q is not a #rrggbb colour", ov.Token, ov.Hex)
		}
		if _, ok := overrideFields[ov.Token]; !ok && ov.Token != overrideGround {
			return fmt.Errorf("%q is not a token (%s)", ov.Token, strings.Join(OverrideTokenNames(), ", "))
		}
	}
	overrides = slices.Clone(o)
	if !mono {
		swapPalette(activeTokens())
	}
	return nil
}

// over is the token with the file's values laid on it.
func (o TokenOverride) over(t Token) Token {
	t.trueColor = lipgloss.Color(strings.ToLower(o.Hex))
	if o.ANSI256 != "" {
		t.ansi256 = lipgloss.Color(o.ANSI256)
	}
	if o.ANSI16 != "" {
		t.ansi = lipgloss.Color(o.ANSI16)
	}
	return t
}

// withOverrides is a table with every override laid on it.
func withOverrides(p ColorTokens, o []TokenOverride) ColorTokens {
	for _, ov := range o {
		if field, ok := overrideFields[ov.Token]; ok {
			t := field(&p)
			*t = ov.over(*t)
		}
	}
	return p
}

// groundWithOverrides is a ground with the file's override of it, if any.
func groundWithOverrides(g Token, o []TokenOverride) Token {
	for _, ov := range o {
		if ov.Token == overrideGround {
			g = ov.over(g)
		}
	}
	return g
}

// Shortfall is one overridden text token under the floor on one surface.
type Shortfall struct {
	Token   string
	Hex     string
	Surface string // "ground" or "band"
	Ratio   float64
}

// String is the shortfall as the product says it, on stderr and on the
// doctor's theme row.
func (s Shortfall) String() string {
	return fmt.Sprintf("%s %s is %.2f:1 on the %s, under %.1f:1", s.Token, s.Hex, s.Ratio, s.Surface, aaFloor)
}

// LowContrast names the overridden text tokens that fall under 4.5:1 on the
// ground or the band of the table, measured on the table with the overrides
// laid on it, with the formula and the floor the shipped tables are held to.
// It is a warning's input, not a refusal: the file is the person's.
func LowContrast(table string, o []TokenOverride) []Shortfall {
	th, ok := themes[resolveTheme(table)]
	if !ok {
		return nil
	}
	p := withOverrides(th.tokens, o)
	surfaces := []struct {
		name string
		c    Token
	}{{"ground", groundWithOverrides(th.ground, o)}, {"band", p.band}}
	var out []Shortfall
	for _, ov := range o {
		if !slices.Contains(overrideInks, ov.Token) {
			continue
		}
		ink := *overrideFields[ov.Token](&p)
		for _, s := range surfaces {
			if r := tokenContrast(ink, s.c); r < aaFloor {
				out = append(out, Shortfall{Token: ov.Token, Hex: strings.ToLower(ov.Hex), Surface: s.name, Ratio: r})
			}
		}
	}
	return out
}

// tokenContrast is the WCAG 2 ratio of two tokens' truecolor values.
func tokenContrast(a, b Token) float64 {
	return wcag(rgbOf(a.trueColor), rgbOf(b.trueColor))
}

func rgbOf(c color.Color) rgb {
	r, g, b, _ := c.RGBA()
	return rgb{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8)}
}
