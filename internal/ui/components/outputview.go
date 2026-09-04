package components

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// OutputView is the full-screen form of a bounded detail body — a command's
// output, a read's file content, a search's matches — opened whole from
// reading mode when the in-place view was not all of it
// (docs/interface/surfaces.md#the-activity-row). It is the diff viewer's
// host shape with lines for hunks: header, scrollable body, footer, and
// nothing to decide.
//
// Like DiffView it is a plain-state component: the host owns it and routes
// keys to Update while it is focused.
type OutputView struct {
	// Title is the header label — the row's own words, e.g. "$ go test ./..."
	// or "read internal/agent/loop.go".
	Title string
	// Lines is the body, raw. Foreign bytes are re-painted into the palette
	// as they are drawn, the same door a detail body's take
	// (docs/interface/principles.md#one-grid: detail bodies indent, they do
	// not re-grid — and nothing arrives with colours of its own).
	Lines []string
	// Height is the view's row budget, header and footer included.
	Height int
	// Offset is the first visible body row.
	Offset int
	// Wrap soft-wraps lines to the width instead of clipping them. Program
	// output clips — a log line's information is at its head — but the
	// command card's full view exists to read a long command whole, and a
	// clip there would hide the thing the screen was opened for.
	Wrap bool
}

// OutputResult is Update's answer when a key ends the view.
type OutputResult int

const (
	// OutputStay: the key scrolled; the view is still up.
	OutputStay OutputResult = iota
	// OutputBack: esc or q — back to where it was opened from, the row
	// still open in place.
	OutputBack
	// OutputCollapse: enter — the depth past full screen is closed, so the
	// host also folds the row the view came from.
	OutputCollapse
)

// Update handles keys while the viewer holds the screen.
func (v *OutputView) Update(msg tea.KeyPressMsg) OutputResult {
	switch pressed := msg.String(); {
	case keys.Is(pressed, keys.Output.Back, keys.Output.Leave):
		return OutputBack
	case keys.Is(pressed, keys.Output.Collapse):
		return OutputCollapse
	case keys.Is(pressed, keys.Output.Scroll):
		if pressed == "j" || pressed == "down" {
			v.Scroll(1)
		} else {
			v.Scroll(-1)
		}
	case keys.Is(pressed, keys.Output.PageUp):
		v.Scroll(-v.bodyHeight())
	case keys.Is(pressed, keys.Output.PageDown):
		v.Scroll(v.bodyHeight())
	}
	return OutputStay
}

// Scroll moves the body by delta rows. The wheel routes here too. The clamp
// lives in View, which knows the width the body wraps at; a press past the
// end is pulled back on the next frame, so it still costs one press to
// recover.
// SetSize gives the view the terminal's rectangle. It lays itself out from
// the width it is rendered at, so only the height is kept.
func (v *OutputView) SetSize(_, height int) { v.Height = height }

func (v *OutputView) Scroll(delta int) {
	v.Offset = max(v.Offset+delta, 0)
}

// bodyHeight is the rows left for content once the header and footer have
// theirs.
func (v *OutputView) bodyHeight() int {
	return max(v.Height-2, 1)
}

// View renders header, visible body and footer at the given width.
func (v *OutputView) View(width int) string {
	stats := plural(len(v.Lines), "line")
	header := padRight(Clip(" "+v.Title, max(0, width-lipgloss.Width(stats))),
		max(0, width-lipgloss.Width(stats))) + sty.Dim.Render(stats)
	footer := sty.Hint.Render("output · " + strings.Join([]string{
		offer(keys.Output.Scroll), offer(keys.Output.PageUp), offer(keys.Output.PageDown),
		offer(keys.Output.Collapse), offer(keys.Output.Back),
	}, " · "))

	body := v.bodyRows(width)
	rows := v.bodyHeight()
	v.Offset = clampOffset(v.Offset, len(body), rows)
	end := min(v.Offset+rows, len(body))

	out := []string{header}
	for _, l := range body[v.Offset:end] {
		// The one door foreign bytes come through here, exactly as a detail
		// body's: re-painted into the palette before they are measured.
		if painted, ok := repaint(l, Palette.Dimmer); ok {
			out = append(out, Clip(painted, width))
			continue
		}
		out = append(out, sty.Dimmer.Render(Clip(l, width)))
	}
	for len(out) < rows+1 {
		out = append(out, "")
	}
	return strings.Join(append(out, footer), "\n")
}

// bodyRows is the body as display rows: the lines themselves, or their
// soft-wrapped form when Wrap is set.
func (v *OutputView) bodyRows(width int) []string {
	if !v.Wrap || width <= 0 {
		return v.Lines
	}
	var out []string
	for _, l := range v.Lines {
		if lipgloss.Width(l) <= width {
			out = append(out, l)
			continue
		}
		out = append(out, strings.Split(ansi.Wrap(l, width, ""), "\n")...)
	}
	return out
}
