package chat

import (
	"fmt"
	"image/color"
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// waitLineModel is a session in the given wait, with nothing else under the
// transcript to draw.
func waitLineModel(t *testing.T, st state, mut func(*Model)) Model {
	t.Helper()
	m := frameModel(t, 80, 40)
	m.turnOpen = true
	m.state = st
	if mut != nil {
		mut(&m)
	}
	return m
}

// TestNotice_TheWaitLinesDoNotSpin: what the session draws under the
// transcript while it waits on something is a still notice line, or nothing
// where the frame's status or the transcript already says it. The frame's
// status is the one thing on screen that animates, so no frame of the tick
// moves any of these.
func TestNotice_TheWaitLinesDoNotSpin(t *testing.T) {
	for _, tc := range []struct {
		name string
		st   state
		mut  func(*Model)
		// want is the line, its spaces folded; empty is no line at all.
		want string
	}{
		{"applying changes", stateRunningCmd, func(m *Model) {
			m.pendingApproval = &approvalRequest{kind: approvalDiff}
		}, "· Applying changes…"},
		{"listing models", stateModelList, nil, "· Listing models…"},
		{"running the quality gate", stateCloseGate, nil, "· Running the quality gate…"},
		// The frame's status says `deciding…`, which is the same wait.
		{"checking permission", stateClassifying, nil, ""},
		// The transcript's last row is `· Compacting conversation…`.
		{"compacting", stateStreaming, func(m *Model) {
			m.compacting = true
			m.appendEntry(entry{kind: entrySystem, text: compactingNotice})
		}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := waitLineModel(t, tc.st, tc.mut)
			for frame := range 8 {
				m.spinFrame = frame
				tail := stripANSI(m.liveTail(80))
				if strings.ContainsAny(tail, brailleFrames) {
					t.Fatalf("frame %d: the wait line spins: %q", frame, tail)
				}
				if got := strings.Join(strings.Fields(tail), " "); got != tc.want {
					t.Fatalf("frame %d: the wait line is %q, want %q", frame, got, tc.want)
				}
			}
		})
	}
}

// sameColor compares two colours by what they draw, since a colour read back
// off the wire is not the type the palette wrote.
func sameColor(a, b color.Color) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	ar, ag, ab, aa := a.RGBA()
	br, bg, bb, ba := b.RGBA()
	return ar == br && ag == bg && ab == bb && aa == ba
}

// TestScreens_StandOnThePaintedGround: every screen the session draws — the
// chat, and every surface the register lists, read from the register itself
// so a surface added to it is in this test — stands on the theme's ground.
// Under the dark theme every cell with no background of its own carries it
// and the terminal's default background is the same; a cell with a
// background of its own keeps it, and no character moves. Turned off, and
// under the light and CharmTone tables, nothing is painted. The command
// line's own screens are held to the same in the command package.
func TestScreens_StandOnThePaintedGround(t *testing.T) {
	const height = 40
	themeRestore(t)
	wasProfile := components.Profile()
	t.Cleanup(func() { components.SetProfile(wasProfile) })
	components.SetMono(false)

	screens := map[string]func(*Model){
		"the chat":          func(*Model) {},
		"the agent manager": func(m *Model) { m.agentList = &components.AgentList{} },
	}
	for s := range overlays() {
		screens[fmt.Sprintf("state %d", s)] = func(m *Model) { m.state = s }
	}
	view := func(open func(*Model), width int) (string, color.Color) {
		m := frameModel(t, width, height)
		open(&m)
		v := m.View()
		return v.Content, v.BackgroundColor
	}
	cells := func(frame string, width int) uv.ScreenBuffer {
		scr := uv.NewScreenBuffer(width, height)
		uv.NewStyledString(frame).Draw(scr, scr.Bounds())
		return scr
	}

	for _, p := range []struct {
		profile colorprofile.Profile
		ground  string
	}{
		{colorprofile.TrueColor, "48;2;15;17;23"},
		{colorprofile.ANSI256, "48;5;233"},
	} {
		components.SetProfile(p.profile)
		for name, open := range screens {
			for _, width := range goldenWidths {
				t.Run(fmt.Sprintf("%v/%s/w%d", p.profile, name, width), func(t *testing.T) {
					_ = components.SetTheme(components.ThemeDark)
					components.PaintGround(false)
					bare, _ := view(open, width)
					components.PaintGround(true)
					frame, bg := view(open, width)
					ground := components.GroundColor()

					if !strings.Contains(frame, p.ground) || !sameColor(bg, ground) {
						t.Fatalf("the screen is not on the ground %s: default background %v", p.ground, bg)
					}
					off, on := cells(bare, width), cells(frame, width)
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
						if frame, bg := view(open, width); bg != nil || strings.Contains(frame, p.ground) {
							t.Errorf("%s paints a ground it leaves to the terminal by default", theme)
						}
					}
					_ = components.SetTheme(components.ThemeDark)
					components.PaintGround(false)
					if frame, bg := view(open, width); bg != nil || strings.Contains(frame, p.ground) {
						t.Errorf("the ground turned off is still painted")
					}
					components.PaintGround(true)
				})
			}
		}
	}
}
