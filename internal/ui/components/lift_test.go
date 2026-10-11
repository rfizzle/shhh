package components

import (
	"math"
	"testing"
)

// The OKLCH conversion is checked against two published values (Ottosson's
// reference: sRGB red is oklch(0.6280 0.2577 29.23), sRGB blue is
// oklch(0.4520 0.3132 264.05)), and round-trips every grey and a spread of
// hues.
func TestLift_OKLCHMatchesTheReference(t *testing.T) {
	for _, c := range []struct {
		hex      string
		l, ch, h float64
	}{
		{"#ff0000", 0.6280, 0.2577, 29.23},
		{"#0000ff", 0.4520, 0.3132, 264.05},
	} {
		v, _ := parseHex(c.hex)
		l, ch, h := toOKLCH(v)
		if math.Abs(l-c.l) > 0.001 || math.Abs(ch-c.ch) > 0.001 || math.Abs(h-c.h) > 0.1 {
			t.Errorf("%s is oklch(%.4f %.4f %.2f), want (%.4f %.4f %.2f)", c.hex, l, ch, h, c.l, c.ch, c.h)
		}
		if back := fromOKLCH(l, ch, h).hex(); back != c.hex {
			t.Errorf("%s came back as %s", c.hex, back)
		}
	}
	for _, hx := range []string{"#000000", "#808080", "#ffffff", "#5fd75f", "#d7af87", "#8787af", "#1e1e2e"} {
		v, _ := parseHex(hx)
		l, ch, h := toOKLCH(v)
		if back := fromOKLCH(l, ch, h).hex(); back != hx {
			t.Errorf("%s came back as %s", hx, back)
		}
	}
}

// shortfalls are the inks the mapping study measured under 4.5:1 on a
// theme's own ground or band, for the thirteen published palettes it
// covered: the theme, its ground, its band, and each published hex that fell
// short. The lift has to bring every one over the floor on both surfaces, in
// its own hue, and in the direction away from the ground.
var shortfalls = []struct {
	name, ground, band string
	hexes              []string
}{
	{"Solarized Dark", "#002b36", "#073642", []string{"#859900", "#dc322f", "#2aa198", "#cb4b16", "#268bd2", "#657b83", "#839496", "#d33682", "#74888c", "#b58900", "#6c71c4"}},
	{"Solarized Light", "#fdf6e3", "#eee8d5", []string{"#859900", "#dc322f", "#2aa198", "#cb4b16", "#268bd2", "#839496", "#657b83", "#d33682", "#74888c", "#586e75", "#b58900", "#6c71c4"}},
	{"Dracula", "#282a36", "#363848", []string{"#ff5555", "#6272a4", "#a289df"}},
	{"Catppuccin Mocha", "#1e1e2e", "#313244", []string{"#6c7086", "#9399b2", "#7f849c"}},
	{"Catppuccin Latte", "#eff1f5", "#e6e9ef", []string{"#40a02b", "#d20f39", "#179299", "#fe640b", "#1e66f5", "#9ca0b0", "#7c7f93", "#ea76cb", "#8c8fa1", "#6c6f85", "#df8e1d", "#7287fd"}},
	{"Gruvbox Dark", "#282828", "#3c3836", []string{"#fb4934", "#83a598", "#928374", "#d3869b", "#a89984", "#ab969a"}},
	{"Gruvbox Light", "#fbf1c7", "#ebdbb2", []string{"#79740e", "#427b58", "#af3a03", "#928374", "#7c6f64", "#b57614"}},
	{"Nord", "#2e3440", "#3b4252", []string{"#bf616a", "#d08770", "#81a1c1", "#818a9a", "#b48ead", "#929aaa"}},
	{"Tokyo Night", "#1a1b26", "#292e42", []string{"#565f89", "#737aa2", "#676f98", "#9d7cd8"}},
	{"Tokyo Night Day", "#e1e2e7", "#d0d5e3", []string{"#587539", "#f52a65", "#007197", "#b15c00", "#2e7de9", "#848cb5", "#68709a", "#9854f1", "#737ba5", "#6172b0", "#3760bf", "#8c6c3e", "#7847bd"}},
	{"One Dark", "#282c34", "#2c323c", []string{"#e06c75", "#5c6370", "#878e9b", "#c678dd", "#7c8390"}},
	{"Rose Pine", "#191724", "#1f1d2e", []string{"#31748f", "#6e6a86", "#827e9c", "#787491"}},
	{"Everforest Dark", "#2d353b", "#343f44", []string{"#e67e80", "#7a8478", "#919e94", "#859289", "#9da9a0"}},
}

// hueGap is the shortest way round the hue circle between two angles.
func hueGap(a, b float64) float64 {
	d := math.Abs(a-b) - 360*math.Floor(math.Abs(a-b)/360)
	return math.Min(d, 360-d)
}

func TestLift_ClearsTheFloorInHue(t *testing.T) {
	for _, th := range shortfalls {
		g, _ := parseHex(th.ground)
		b, _ := parseHex(th.band)
		darker := relLuminance(g) >= 0.18
		for _, hx := range th.hexes {
			got := LiftToAA(hx, th.ground, th.band)
			v, ok := parseHex(got)
			if !ok {
				t.Errorf("%s %s: lift returned %q", th.name, hx, got)
				continue
			}
			if r := wcag(v, g); r < aaFloor {
				t.Errorf("%s %s lifted to %s is %.2f:1 on the ground", th.name, hx, got, r)
			}
			if r := wcag(v, b); r < aaFloor {
				t.Errorf("%s %s lifted to %s is %.2f:1 on the band", th.name, hx, got, r)
			}
			in, _ := parseHex(hx)
			l0, c0, h0 := toOKLCH(in)
			l1, c1, h1 := toOKLCH(v)
			if (l1 > l0) == darker {
				t.Errorf("%s %s lifted to %s moved lightness the wrong way (%.3f to %.3f)", th.name, hx, got, l0, l1)
			}
			// Hue is only defined where there is chroma to carry it; a
			// near-grey's hue is the rounding of its hex.
			if c0 > 0.03 && hueGap(h0, h1) > 5 {
				t.Errorf("%s %s lifted to %s left its hue (%.1f to %.1f degrees)", th.name, hx, got, h0, h1)
			}
			if c1 > c0+0.01 {
				t.Errorf("%s %s lifted to %s gained chroma (%.3f to %.3f)", th.name, hx, got, c0, c1)
			}
			// Nearest: one step less lift must not have cleared the floor.
			if prev := fromOKLCH(l1-map[bool]float64{false: 1, true: -1}[darker]*liftStep*2, c0, h0); wcag(prev, g) >= aaFloor && wcag(prev, b) >= aaFloor && prev != v {
				t.Errorf("%s %s lifted to %s, but %s already clears the floor", th.name, hx, got, prev.hex())
			}
		}
	}
}

func TestLift_LeavesAClearingInkAlone(t *testing.T) {
	if got := LiftToAA("#CDD6F4", "#1e1e2e", "#313244"); got != "#cdd6f4" {
		t.Errorf("an ink over the floor came back as %s", got)
	}
	for _, bad := range []string{"", "red", "#fff", "#gggggg"} {
		if got := LiftToAA(bad, "#1e1e2e", "#313244"); got != "" {
			t.Errorf("LiftToAA(%q) = %q, want empty", bad, got)
		}
	}
	// A ground and a band on opposite ends that nothing stands off on both
	// are refused rather than answered with something short.
	if got := LiftToAA("#777777", "#000000", "#ffffff"); got != "" {
		t.Errorf("a black ground and a white band answered %s", got)
	}
}

// A plain nearest pick for each token puts neighbouring greys on one rung;
// the greedy pick does not, and says how far it had to go.
func TestRungs_AreDistinct(t *testing.T) {
	// Catppuccin Mocha's lifted ladder, a few points apart, as it comes out
	// of LiftToAA.
	greys := []string{"#989bac", "#a0a5b8", "#a2a7ba", "#a9aec5", "#b2b8d2"}
	plain := map[int]bool{}
	collided := false
	for _, hx := range greys {
		idx, _ := nearestFree(hx, nil)
		if plain[idx] {
			collided = true
		}
		plain[idx] = true
	}
	if !collided {
		t.Fatal("a plain nearest pick no longer collides on these greys; pick greys that show the failure")
	}

	rungs, worst := DistinctRungs(greys)
	seen := map[int]bool{}
	for i, r := range rungs {
		if r < 16 || r > 255 {
			t.Errorf("%s got rung %d, outside 16 to 255", greys[i], r)
		}
		if seen[r] {
			t.Errorf("%s shares rung %d", greys[i], r)
		}
		seen[r] = true
	}
	if worst <= 0 || worst > 30 {
		t.Errorf("the worst error is %.1f; five greys a few points apart should land within 30", worst)
	}

	// A whole table: the eighteen tokens of a published palette (Catppuccin
	// Mocha with its short greys lifted) take eighteen rungs and the worst
	// error stays small enough to be the same colour family.
	table := []string{"#cdd6f4", "#bac2de", "#b2b8d2", "#a9aec5", "#a0a5b8", "#989bac",
		"#a6e3a1", "#f38ba8", "#94e2d5", "#fab387", "#89b4fa", "#f5c2e7", "#f9e2af", "#b4befe",
		"#45475a", "#313244", "#44554e", "#5a3d50", "#1e1e2e"}
	rungs, worst = DistinctRungs(table)
	seen = map[int]bool{}
	for i, r := range rungs {
		if seen[r] {
			t.Errorf("%s shares rung %d", table[i], r)
		}
		seen[r] = true
	}
	if worst > 60 {
		t.Errorf("worst error %.1f on a table that maps within 30", worst)
	}
	t.Logf("worst 256-colour error over the table: %.1f", worst)
}
