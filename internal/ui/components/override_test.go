package components

import (
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"

	"github.com/rfizzle/shhh/internal/ui/golden"
)

// installOverrides installs overrides for one test and takes them out after,
// with the theme it found.
func installOverrides(t *testing.T, o []TokenOverride) {
	t.Helper()
	wasTheme := ThemeName()
	t.Cleanup(func() {
		_ = SetOverrides(nil)
		_ = SetTheme(wasTheme)
	})
	if err := SetOverrides(o); err != nil {
		t.Fatal(err)
	}
}

func TestTheme_AnOverrideKeepsTheOtherRungs(t *testing.T) {
	withColorProfile(t, colorprofile.TrueColor)
	if err := SetTheme(ThemeDark); err != nil {
		t.Fatal(err)
	}
	shipped := themes[ThemeDark].tokens
	installOverrides(t, []TokenOverride{
		{Token: "accent", Hex: "#112233"},
		{Token: "key", Hex: "#445566", ANSI256: "60", ANSI16: "4"},
	})
	if got := Palette.Accent; sgr(got.trueColor) != sgr(token("#112233", "0", "0").trueColor) ||
		got.ansi256 != shipped.Accent.ansi256 || got.ansi != shipped.Accent.ansi {
		t.Errorf("a hex alone moved a rung: %+v against the shipped %+v", got, shipped.Accent)
	}
	if got := Palette.Key; got.ansi256 != token("", "60", "").ansi256 || got.ansi != token("", "", "4").ansi {
		t.Errorf("the rungs the file gave were not used: %+v", got)
	}
	if Palette.Body != shipped.Body {
		t.Error("a token the file did not name changed")
	}
	if themes[ThemeDark].tokens != shipped {
		t.Error("the shipped table was written through")
	}
}

// The overrides survive every door that chooses a table, and mono, which
// chooses none, outranks them.
func TestTheme_OverridesAreReappliedOnEverySwap(t *testing.T) {
	withColorProfile(t, colorprofile.TrueColor)
	was := Mono()
	t.Cleanup(func() { SetMono(was) })
	SetMono(false)
	if err := SetTheme(ThemeDark); err != nil {
		t.Fatal(err)
	}
	installOverrides(t, []TokenOverride{{Token: "dim", Hex: "#123456"}, {Token: "ground", Hex: "#010203"}})
	want := token("#123456", "0", "0").trueColor
	has := func() bool { return Palette.Dim.trueColor == want }

	if !has() {
		t.Fatal("not applied when set")
	}
	for _, name := range []string{ThemeLight, ThemeCharm, ThemeAuto} {
		if err := SetTheme(name); err != nil {
			t.Fatal(err)
		}
		if !has() {
			t.Errorf("lost on /ui theme %s", name)
		}
	}
	SetGround(false)
	if !has() {
		t.Error("lost on the terminal's ground answer")
	}
	SetGround(true)
	SetMono(true)
	if has() || Palette != monoPalette {
		t.Error("an override outranked mono")
	}
	SetMono(false)
	if !has() {
		t.Error("lost across a mono toggle")
	}
	if err := SetTheme(ThemeDark); err != nil {
		t.Fatal(err)
	}
	PaintGround(true)
	t.Cleanup(func() { PaintGround(groundPaintedByDefault()) })
	if got := GroundColor(); got == nil || sgr(got) != sgr(token("#010203", "0", "0").trueColor) {
		t.Errorf("the ground override was not painted: %v", got)
	}
}

func TestTheme_LowContrastOverrideIsNamed(t *testing.T) {
	low := LowContrast(ThemeDark, []TokenOverride{
		{Token: "dim", Hex: "#303030"},   // under the floor on both surfaces
		{Token: "body", Hex: "#ffffff"},  // clears
		{Token: "focus_bg", Hex: "#000000"}, // not text; never measured
	})
	if len(low) != 2 || low[0].Token != "dim" || low[0].Surface != "ground" || low[1].Surface != "band" {
		t.Fatalf("got %+v, want dim named on the ground and on the band", low)
	}
	if !strings.Contains(low[0].String(), "dim #303030 is ") || !strings.Contains(low[0].String(), ":1 on the ground, under 4.5:1") {
		t.Errorf("the words: %s", low[0])
	}
	// The measure is the shipped tables': every shipped ink, put back as an
	// override of itself, is clear of the floor wherever the table clears it.
	for _, name := range ThemeNames() {
		th, ok := themes[name]
		if !ok || name == ThemeCharm {
			continue
		}
		var same []TokenOverride
		for _, ink := range overrideInks {
			c := rgbOf(tokenNamed(th.tokens, ink).trueColor)
			same = append(same, TokenOverride{Token: ink, Hex: c.hex()})
		}
		if got := LowContrast(name, same); len(got) != 0 {
			t.Errorf("%s: its own inks named as low: %v", name, got)
		}
	}
	// A ground override moves what the ink is measured against.
	if got := LowContrast(ThemeDark, []TokenOverride{{Token: "body", Hex: "#d0d0d0"}, {Token: "ground", Hex: "#d0d0d0"}}); len(got) == 0 {
		t.Error("an ink on its own colour was not named")
	}
}

func TestTheme_OverrideRefusedWhole(t *testing.T) {
	installOverrides(t, []TokenOverride{{Token: "dim", Hex: "#123456"}})
	for _, bad := range [][]TokenOverride{
		{{Token: "dim", Hex: "#654321"}, {Token: "magenta", Hex: "#ff00ff"}},
		{{Token: "dim", Hex: "#654321"}, {Token: "body", Hex: "red"}},
	} {
		if err := SetOverrides(bad); err == nil {
			t.Errorf("%+v was applied", bad)
		}
	}
	if Palette.Dim.trueColor != token("#123456", "0", "0").trueColor {
		t.Error("a refused set changed the overrides in effect")
	}
	if names := OverrideTokenNames(); len(names) != PaletteSize+1 || !slices.Contains(names, "ground") {
		t.Errorf("the names a file may use are the %d tokens and the ground: %v", PaletteSize, names)
	}
}

// The doctor's theme row when the file's override is under the floor: a
// warning that says it was applied.
func TestGolden_DoctorThemeOverride(t *testing.T) {
	low := LowContrast(ThemeDark, []TokenOverride{{Token: "dim", Hex: "#404040"}})
	var fix []string
	for _, s := range low {
		fix = append(fix, s.String())
	}
	captureGolden(t, "doctor-theme-override", "the doctor's theme row, a file's override under the floor", goldenWidths, func(width int) []golden.Panel {
		warned := func() *DoctorScreen {
			return &DoctorScreen{Elapsed: "0.2s", Checks: []DoctorCheck{
				{Name: "theme", Subject: "~/.config/shhh/theme.toml", Detail: "dark · 1 over it",
					Outcome: "low contrast", State: DoctorWarned,
					Consequence: "applied as written; under 4.5:1 is hard to read",
					FixLabel:    "lift the colour",
					Fix:         fix},
			}}
		}
		return []golden.Panel{
			{Label: "an override under 4.5:1 · applied as written and named", View: warned().View(width)},
			{Label: "the fix open · each token named with its ratio and the surface it falls short on",
				View: func() string {
					d := warned()
					d.Update(key("enter"))
					return d.View(width)
				}()},
			{Label: "the same file with the override clear of the floor · ok, and says how many tokens it changes",
				View: (&DoctorScreen{Elapsed: "0.2s", Checks: []DoctorCheck{
					{Name: "theme", Subject: "~/.config/shhh/theme.toml", Detail: "dark · 2 over it", Outcome: "ok"},
				}}).View(width)},
		}
	})
}
