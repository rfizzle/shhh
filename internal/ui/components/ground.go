package components

// The ground under a screen that takes the whole terminal.
//
// The theme's ground is the binary's, not one surface's: a reader who leaves
// the chat for the saved-chat browser, the settings screen or the doctor is
// still looking at the same window, and a ground that stopped at the chat's
// edge would jump colour on every terminal whose own background is not the
// theme's. So every full screen reaches the terminal through FullScreen, and
// nothing else asks for the alternate screen — which is what lets a screen
// added later stand on the ground without remembering to.
// See docs/interface/principles.md#a-colour-is-three-values-and-a-ground.

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

// FullScreen is the frame of a screen that takes the whole terminal: the
// frame on the alternate screen, with the terminal's default background set
// to the theme's ground — which is what the terminal fills the window's
// margin with, outside every cell. The cells carry the ground too (LayGround,
// GroundFrame); this is the part of the window no cell covers. Nil, where the
// theme leaves the terminal's own background standing or the reader turned
// the ground off, asks for nothing.
func FullScreen(frame string) tea.View {
	v := tea.NewView(frame)
	v.AltScreen = true
	v.BackgroundColor = GroundColor()
	return v
}

// GroundFrame draws a screen's render into width × height cells and lays the
// ground under it, for a host that holds its screen as a string rather than
// as cells. A size the terminal has not reported yet is the render's own, so
// the first frame is painted as far as it reaches.
func GroundFrame(view string, width, height int) string {
	if GroundColor() == nil {
		return view
	}
	if width <= 0 {
		width = lipgloss.Width(view)
	}
	if height <= 0 {
		height = lipgloss.Height(view)
	}
	scr := uv.NewScreenBuffer(width, height)
	uv.NewStyledString(view).Draw(scr, scr.Bounds())
	LayGround(scr)
	return strings.ReplaceAll(scr.Render(), "\r\n", "\n")
}

// LayGround lays the theme's ground under every cell that drew no background
// of its own. It is written into the cells rather than left to the
// terminal's default background alone, because a terminal draws its default
// background and a cell's own through two different paths — one that a
// window's opacity, blur or theme may change, and one it draws as sent — and
// the band is a cell's own: the band and the ground under it reach the screen
// as the same kind of colour, the catalogue's pair, or not at all. A cell
// that already has a ground — the band, a lit row, a diff's tint — keeps it.
// Where there is no ground to paint it changes nothing.
func LayGround(scr uv.ScreenBuffer) {
	ground := GroundColor()
	if ground == nil {
		return
	}
	for y := range scr.Height() {
		line := scr.Line(y)
		for x := range line {
			if c := line.At(x); c != nil && !c.IsZero() && c.Style.Bg == nil {
				c.Style.Bg = ground
			}
		}
	}
}
