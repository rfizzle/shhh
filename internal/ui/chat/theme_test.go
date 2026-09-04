package chat

// The light ground, end to end: the question this surface asks the terminal,
// the answer that picks a table, and whether what is then drawn can be read.
//
// The last of those is the only check here that is not a comparison against
// something written down. A palette is legible or it is not, and a person
// looking at a screenshot cannot tell 3.4:1 from 2.6:1 — so the ratios are
// computed from the tokens the frame was drawn with, against the background
// the terminal itself reported.

import (
	"image/color"
	"math"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/golden"
)

// themeRestore puts the palette back the way the test found it. The theme is
// a package global in components for the same reason the mono palette is, so
// a test that swapped one and left it would be choosing the colours of every
// test after it.
//
// The terminal's answer about its own ground has to go back with the theme,
// and it is the half that is easy to miss: restoring the *name* is a no-op
// when the name never left auto, and auto resolves through the ground — so a
// test that answered "light" and put only the name back would leave every
// test after it on the light table with nothing on screen saying why. Dark is
// what goes back because it is what a terminal that has not answered gets,
// and this file holds the only test in the package that answers at all.
func themeRestore(t *testing.T) {
	t.Helper()
	wasMono, wasTheme := components.Mono(), components.ThemeName()
	t.Cleanup(func() {
		components.SetGround(true)
		_ = components.SetTheme(wasTheme)
		components.SetMono(wasMono)
	})
}

// A light terminal, from the question to the frame. The reply the fake
// terminal sends is the one a white terminal sends, and every step after it
// is the product's own: caps reads the ground off the reply, the auto theme
// takes the table chosen for that ground, and the frame is drawn with it.
//
// The thresholds are WCAG's, on the two readings the tokens are for. Body is
// ordinary running text and takes the 4.5:1 that asks for; the two chrome
// greys carry counts, hints and detail bodies beside a glyph or a word that
// says the same thing
// (docs/interface/principles.md#colour-never-carries-meaning-alone), so they
// take the 3:1 that stands for everything that is not body text. #8a8a8a on
// white clears it by the same margin #626262 clears it against black by,
// which is the light table's claim to be the dark one's equal and not its
// approximation.
func TestTheme_ALightTerminalIsLegible(t *testing.T) {
	themeRestore(t)
	components.SetMono(false)
	if err := components.SetTheme(components.ThemeAuto); err != nil {
		t.Fatal(err)
	}
	was := components.Profile()
	components.SetProfile(colorprofile.TrueColor)
	t.Cleanup(func() { components.SetProfile(was) })

	// The terminal: one that answers, and answers white.
	white := color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	m := frameModel(t, 110, screenHeight)
	next, _ := m.Update(tea.EnvMsg{"TERM=xterm-ghostty"})
	m = next.(Model)
	if !m.caps.Asked {
		t.Fatal("the surface did not ask the terminal anything")
	}
	next, _ = m.Update(tea.BackgroundColorMsg{Color: white})
	m = next.(Model)

	dark, answered := m.caps.DarkGround()
	if !answered || dark {
		t.Fatalf("the reply was read as dark=%v answered=%v; a white terminal is neither", dark, answered)
	}
	if components.Palette != components.LightPalette {
		t.Fatal("the terminal said light and the surfaces are still drawing with the dark table")
	}

	// The frame, drawn through that table. Body has to be in it, or the
	// ratios below are being computed about colours nothing on screen is
	// wearing.
	frame := m.View().Content
	body := lipgloss.NewStyle().Foreground(components.Palette.Body.Color()).Render("")
	if lead, _, _ := strings.Cut(body, "m"); !strings.Contains(frame, lead) {
		t.Errorf("the frame carries no text in the light table's body colour (%s)", lead)
	}

	for _, c := range []struct {
		name  string
		token components.Token
		least float64
	}{
		{"body", components.Palette.Body, 4.5},
		{"dim", components.Palette.Dim, 3},
		{"dimmer", components.Palette.Dimmer, 3},
	} {
		if got := contrast(t, c.token.Color(), white); got < c.least {
			t.Errorf("%s is %.2f:1 against the background this terminal reported, and has to be at least %.1f:1",
				c.name, got, c.least)
		}
	}
}

// contrast is the WCAG ratio between two colours: (L₁+0.05)/(L₂+0.05) over
// their relative luminances, lighter first.
func contrast(t *testing.T, a, b color.Color) float64 {
	t.Helper()
	hi, lo := relativeLuminance(t, a), relativeLuminance(t, b)
	if hi < lo {
		hi, lo = lo, hi
	}
	return (hi + 0.05) / (lo + 0.05)
}

// relativeLuminance is WCAG 2.x's: each channel linearised out of sRGB, then
// weighted for the eye's response.
func relativeLuminance(t *testing.T, c color.Color) float64 {
	t.Helper()
	if c == nil {
		t.Fatal("a token with no colour has no luminance")
	}
	r, g, b, _ := c.RGBA()
	lin := func(v uint32) float64 {
		f := float64(v>>8) / 255
		if f <= 0.03928 {
			return f / 12.92
		}
		return math.Pow((f+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(r) + 0.7152*lin(g) + 0.0722*lin(b)
}

// TestGolden_LightScreen captures the whole surface through the light table.
// One state at two widths, because what this file pins is the colour
// assignment and not the arrangement — the arrangement is already held at six
// widths in two palettes by the whole-screen captures beside it, and the
// table cannot move a column.
func TestGolden_LightScreen(t *testing.T) {
	themeRestore(t)
	was := components.Profile()
	components.SetProfile(colorprofile.ANSI256)
	t.Cleanup(func() { components.SetProfile(was) })
	components.SetMono(false)
	if err := components.SetTheme(components.ThemeLight); err != nil {
		t.Fatal(err)
	}

	for _, width := range []int{80, 144} {
		m := frameModel(t, width, screenHeight)
		m.transcript = goldenTranscript()
		m.invalidateRenderCache()
		m.syncViewport()
		m.viewport.SetLines(m.renderHistoryLines())
		m.viewport.GotoBottom()
		golden.Assert(t, "screen-light.w"+strconv.Itoa(width), golden.Case{
			Surface: "the whole surface on a light ground",
			Width:   width,
			Panels:  []golden.Panel{{Label: "idle · the draft has the keyboard", View: m.View().Content}},
		})
	}
}

// The two /ui settings this file adds, from the surface a person types at:
// the table, which persists, and the ground, which does not.
func TestUICommand_ThemeSwapsTheTableAndSaysSo(t *testing.T) {
	themeRestore(t)
	components.SetMono(false)
	m := readyModel(t)
	var written map[string]string
	m.writeConfig = func(key, value string) error {
		if written == nil {
			written = map[string]string{}
		}
		written[key] = value
		return nil
	}

	handled, result := m.handleSlashCommand("/ui theme light")
	if !handled {
		t.Fatal("/ui theme should be handled")
	}
	if components.Palette != components.LightPalette {
		t.Fatal("the light table was asked for and is not the one being drawn with")
	}
	if !strings.Contains(result, "Theme: light") || !strings.Contains(result, "Saved") {
		t.Fatalf("the reply should say what changed and that it will last, got %q", result)
	}
	if written["appearance.theme"] != "light" {
		t.Errorf("the answer was not persisted: %v", written)
	}

	if _, result = m.handleSlashCommand("/ui"); !strings.Contains(result, "Theme: light") {
		t.Fatalf("bare /ui should report the theme alongside the rest, got %q", result)
	}
	if _, result = m.handleSlashCommand("/ui theme solarized"); !strings.Contains(result, "unknown theme") {
		t.Fatalf("a name no table answers to should be an error, got %q", result)
	}
	if components.Palette != components.LightPalette {
		t.Error("a refused name changed the palette")
	}
}

func TestUICommand_GroundIsOfferedAndSessionOnly(t *testing.T) {
	themeRestore(t)
	components.SetMono(false)
	m := readyModel(t)
	m.writeConfig = func(key, value string) error {
		t.Errorf("the screen ground is a session switch and must not be written to the config file (%s=%s)", key, value)
		return nil
	}
	t.Cleanup(func() { components.PaintGround(false) })

	if _, result := m.handleSlashCommand("/ui ground"); !strings.Contains(result, "the terminal's own") {
		t.Fatalf("the ground starts on the terminal's own, got %q", result)
	}
	if bg := m.View().BackgroundColor; bg != nil {
		t.Fatalf("the frame paints a background nobody asked for: %v", bg)
	}
	if _, result := m.handleSlashCommand("/ui ground on"); !strings.Contains(result, "the theme's own") {
		t.Fatalf("the reply should say what is painted now, got %q", result)
	}
	if !components.GroundPainted() {
		t.Fatal("the switch did not take")
	}
	if m.View().BackgroundColor == nil {
		t.Error("the switch is on and the frame still paints no background")
	}
	if _, result := m.handleSlashCommand("/ui ground sepia"); !strings.Contains(result, "unknown ground setting") {
		t.Fatalf("an unknown setting should be an error, got %q", result)
	}
}
