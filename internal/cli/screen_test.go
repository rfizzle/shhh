package cli

import (
	"fmt"
	"image/color"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// hostedScreens is every take-over screen the command line opens, each in
// the host it is opened in. They are listed once here and every screen test
// reads them from this list.
func hostedScreens() []struct {
	name string
	view func(width, height int) tea.View
} {
	return []struct {
		name string
		view func(width, height int) tea.View
	}{
		{"config", hosted(&components.ConfigScreen{})},
		{"metrics", hosted(&components.MetricsScreen{})},
		{"chats", hosted(&components.ChatScreen{Rows: []components.ChatRow{{ID: "a", Name: "a", Title: "The retry backoff"}}})},
		{"rate", hosted(&components.RateScreen{})},
		{"snippets", hosted(&components.SnippetScreen{})},
		{"history", hosted(&components.HistoryScreen{})},
		{"doctor", hosted(&components.DoctorScreen{})},
	}
}

// hosted is a screen in the host every command opens one in, sized the way
// the terminal sizes it.
func hosted[R any](screen takeover[R]) func(width, height int) tea.View {
	return func(width, height int) tea.View {
		var m tea.Model = newScreenModel(screen, width, func(bool, R) tea.Cmd { return nil })
		m, _ = m.Update(tea.WindowSizeMsg{Width: width, Height: height})
		return m.View()
	}
}

// screenCells reads a frame back into the cells a terminal would hold.
func screenCells(frame string, width, height int) uv.ScreenBuffer {
	scr := uv.NewScreenBuffer(width, height)
	uv.NewStyledString(frame).Draw(scr, scr.Bounds())
	return scr
}

func sameColor(a, b color.Color) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	ar, ag, ab, aa := a.RGBA()
	br, bg, bb, ba := b.RGBA()
	return ar == br && ag == bg && ab == bb && aa == ba
}

// TestScreens_StandOnThePaintedGround: every screen the command line takes
// the terminal for stands on the theme's ground the way the chat does. Under
// the dark theme every cell that drew no background of its own carries the
// ground, to the window's last row and column, and the terminal's default
// background is the same ground; a cell with a background of its own keeps
// it, and no character moves. Turned off, and under the light and CharmTone
// tables, which leave the terminal's own by default, nothing is painted.
func TestScreens_StandOnThePaintedGround(t *testing.T) {
	const height = 40
	wasMono, wasTheme, wasPainted, wasProfile := components.Mono(), components.ThemeName(), components.GroundPainted(), components.Profile()
	t.Cleanup(func() {
		components.SetGround(true)
		_ = components.SetTheme(wasTheme)
		components.PaintGround(wasPainted)
		components.SetMono(wasMono)
		components.SetProfile(wasProfile)
	})
	components.SetMono(false)

	for _, p := range []struct {
		profile colorprofile.Profile
		ground  string
	}{
		{colorprofile.TrueColor, "48;2;15;17;23"},
		{colorprofile.ANSI256, "48;5;233"},
	} {
		components.SetProfile(p.profile)
		for _, sc := range hostedScreens() {
			for _, width := range []int{60, 80, 110, 130} {
				t.Run(fmt.Sprintf("%v/%s/w%d", p.profile, sc.name, width), func(t *testing.T) {
					_ = components.SetTheme(components.ThemeDark)
					components.PaintGround(false)
					bare := sc.view(width, height)
					components.PaintGround(true)
					v := sc.view(width, height)
					ground := components.GroundColor()

					if !strings.Contains(v.Content, p.ground) {
						t.Fatalf("the screen never sends the ground %s", p.ground)
					}
					if !v.AltScreen || !sameColor(v.BackgroundColor, ground) {
						t.Fatalf("the frame is not the full screen on the ground: alt %v, background %v", v.AltScreen, v.BackgroundColor)
					}
					off, on := screenCells(bare.Content, width, height), screenCells(v.Content, width, height)
					for y := range height {
						for x := range width {
							was, is := off.CellAt(x, y), on.CellAt(x, y)
							if was == nil || is == nil {
								continue
							}
							if was.Content != is.Content {
								t.Fatalf("cell %d,%d: %q became %q", x, y, was.Content, is.Content)
							}
							want := was.Style.Bg
							if want == nil {
								want = ground
							}
							if !sameColor(is.Style.Bg, want) {
								t.Fatalf("cell %d,%d (%q): background %v, want %v", x, y, is.Content, is.Style.Bg, want)
							}
						}
					}

					for _, theme := range []string{components.ThemeLight, components.ThemeCharm} {
						_ = components.SetTheme(theme)
						components.PaintGround(false)
						if v := sc.view(width, height); v.BackgroundColor != nil || strings.Contains(v.Content, p.ground) {
							t.Errorf("%s paints a ground it leaves to the terminal by default", theme)
						}
					}
					_ = components.SetTheme(components.ThemeDark)
					components.PaintGround(false)
					if v := sc.view(width, height); v.BackgroundColor != nil || strings.Contains(v.Content, p.ground) {
						t.Errorf("the ground turned off is still painted")
					}
					components.PaintGround(true)
				})
			}
		}
	}
}

// TestScreens_TakeTheTableTheTerminalReports: under the auto theme a screen
// asks the terminal what its background is, as the chat does, and a light
// answer leaves the terminal's own ground standing — the light table's
// default — rather than the dark table's painted over it.
func TestScreens_TakeTheTableTheTerminalReports(t *testing.T) {
	wasMono, wasTheme, wasPainted, wasProfile := components.Mono(), components.ThemeName(), components.GroundPainted(), components.Profile()
	t.Cleanup(func() {
		components.SetGround(true)
		_ = components.SetTheme(wasTheme)
		components.PaintGround(wasPainted)
		components.SetMono(wasMono)
		components.SetProfile(wasProfile)
	})
	components.SetMono(false)
	components.SetProfile(colorprofile.TrueColor)
	_ = components.SetTheme(components.ThemeAuto)
	components.SetGround(true)
	components.PaintGround(true)

	var m tea.Model = newScreenModel(&components.HistoryScreen{}, 80, func(bool, components.HistoryResult) tea.Cmd { return nil })
	if m.Init() == nil {
		t.Fatal("the screen never asks the terminal what its background is")
	}
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	if v := m.View(); v.BackgroundColor == nil {
		t.Fatal("before the terminal answers, the dark table's ground should stand")
	}
	m, _ = m.Update(tea.BackgroundColorMsg{Color: color.White})
	if v := m.View(); v.BackgroundColor != nil || strings.Contains(v.Content, "48;2;15;17;23") {
		t.Fatal("a light terminal's screen is painted with the dark ground")
	}
}
