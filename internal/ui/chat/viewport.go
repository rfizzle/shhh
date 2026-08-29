package chat

// The transcript window (
// docs/architecture.md#the-screen-is-a-rectangle-and-so-is-everything-in-it).
//
// This is the pane the transcript is read through: a scroll offset, a size,
// and the lines the offset is into. It replaces the bubbles viewport shhh
// used to hold, and the reason is the transcript window's — that viewport
// takes its content as a string, splits it into every line of the session,
// and measures the width of all of them, so a frame cost as much as the
// history was long. The lines arrive here already split and already measured
// by lines.go, and the only ones this file touches are the ones on the
// screen.
//
// The scroll position is an absolute line index rather than the item-and-line
// pair Crush's list keeps, and that is deliberate: the selection
// is a pair of coordinates in rendered transcript space, the notice rail
// counts the lines below the pane, and the scroll gutter is a
// proportion of the whole. All three ask the same question — which line of
// the transcript is this — and an item-relative offset would make each of
// them convert.
//
// What is *not* kept from the bubbles viewport is its keymap. It bound j, k,
// u, d, f, b and the spacebar, which is the bug reading mode opens with: the
// pager fired from inside a sentence. Nothing here reads a key at all. The
// transcript is moved by scrollLines and scrollPage (navigate.go), which the
// wheel, pgup/pgdn and shift+arrows reach — and by nothing else.

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// viewport is a window onto rendered transcript lines.
//
// It holds the lines rather than copying them: lines.go hands over the slice
// it just finished building, and rebuilds the live tail in place on the next
// frame (lines.go explains why that is safe). Every call site sets the lines
// and reads the view within one frame, so nothing here outlives the render
// that produced it.
type viewport struct {
	width  int
	height int
	// yOffset is the index of the first visible line. It is clamped to
	// [0, maxYOffset] by every path that writes it, so it always names a line
	// the pane could be showing.
	yOffset int
	lines   []string
}

func newViewport(width, height int) viewport {
	return viewport{width: width, height: height}
}

func (v viewport) Width() int  { return v.width }
func (v viewport) Height() int { return v.height }

func (v *viewport) SetWidth(w int) {
	v.width = w
	v.SetYOffset(v.yOffset)
}

func (v *viewport) SetHeight(h int) {
	v.height = h
	v.SetYOffset(v.yOffset)
}

// SetLines replaces the content. An all-blank single line is no content at
// all — the empty transcript renders as one empty string, and a pane that
// called that one line would put a thumb in the gutter for a session with
// nothing in it.
func (v *viewport) SetLines(lines []string) {
	if len(lines) == 1 && ansi.StringWidth(lines[0]) == 0 {
		lines = nil
	}
	v.lines = lines
	// Content that shrank out from under the offset — a /clear, a rewind —
	// leaves the pane past its own end, so it follows the content down.
	if v.yOffset > v.maxYOffset() {
		v.GotoBottom()
	}
}

// SetContent is SetLines for the surfaces that still render to one string:
// reading mode's gutter render and an attached child's session, both of which
// are built fresh each time and have no incremental cache to feed.
func (v *viewport) SetContent(s string) {
	v.SetLines(strings.Split(s, "\n"))
}

// TotalLineCount is how many lines the transcript has, which is what the
// scroll gutter's proportion and the notice rail's count are
// both fractions of.
func (v viewport) TotalLineCount() int { return len(v.lines) }

// VisibleLineCount is how many of them are on the screen — fewer than the
// pane's height only when the transcript is shorter than the pane.
func (v viewport) VisibleLineCount() int {
	if v.width <= 0 || v.height <= 0 {
		return 0
	}
	top := min(v.yOffset, len(v.lines))
	return min(v.height, len(v.lines)-top)
}

func (v viewport) YOffset() int { return v.yOffset }

func (v *viewport) SetYOffset(n int) {
	v.yOffset = min(max(n, 0), v.maxYOffset())
}

// maxYOffset is the last offset that still fills the pane, or 0 when the
// transcript is shorter than it.
func (v viewport) maxYOffset() int { return max(0, len(v.lines)-v.height) }

func (v viewport) AtTop() bool    { return v.yOffset <= 0 }
func (v viewport) AtBottom() bool { return v.yOffset >= v.maxYOffset() }

func (v *viewport) GotoTop()    { v.SetYOffset(0) }
func (v *viewport) GotoBottom() { v.SetYOffset(v.maxYOffset()) }

func (v *viewport) ScrollUp(n int) {
	if n == 0 || len(v.lines) == 0 {
		return
	}
	v.SetYOffset(v.yOffset - n)
}

func (v *viewport) ScrollDown(n int) {
	if n == 0 || len(v.lines) == 0 {
		return
	}
	v.SetYOffset(v.yOffset + n)
}

// PageUp and PageDown move by a whole pane. They are a page rather than a
// page-less-one because that is what the key has always done here; the
// overlap belongs to the reader's eye, not to the pager.
func (v *viewport) PageUp()   { v.ScrollUp(v.height) }
func (v *viewport) PageDown() { v.ScrollDown(v.height) }

// visibleLines is the window: the lines the offset names, and no others. This
// is the whole point of the file — the work a frame does is bounded by the
// height of the pane rather than by the length of the session.
//
// A line wider than the pane is cut rather than wrapped. Nothing in the
// transcript should be: every renderer wraps to the pane width it was given
// (the scroll gutter reserves its column so the width never changes
// underneath one). The cut is what keeps a renderer that got it wrong from
// breaking the frame's shape.
func (v viewport) visibleLines() []string {
	if v.width <= 0 || v.height <= 0 || len(v.lines) == 0 {
		return nil
	}
	top := min(v.yOffset, len(v.lines))
	bottom := min(top+v.height, len(v.lines))
	out := v.lines[top:bottom]
	copied := false
	for i, line := range out {
		if ansi.StringWidth(line) <= v.width {
			continue
		}
		if !copied {
			// Copy before the first cut, so the cache's own lines are never
			// rewritten by a render of them.
			out, copied = append([]string(nil), out...), true
		}
		out[i] = ansi.Cut(line, 0, v.width)
	}
	return out
}

// View is the pane, padded to its own width and height so every row the
// scroll gutter glues itself to is the same length.
func (v viewport) View() string {
	if v.width == 0 || v.height == 0 {
		return ""
	}
	return lipgloss.NewStyle().
		Width(v.width).
		Height(v.height).
		Render(strings.Join(v.visibleLines(), "\n"))
}
