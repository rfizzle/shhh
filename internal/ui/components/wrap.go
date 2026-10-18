package components

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// The functions here are how a width is kept: Clip cuts a row, PadRight and
// PadLeft fill one, and Wrap lays prose across several. A surface outside
// this package reaches for them rather than writing its own, because the
// measure is the same everywhere (display cells, escapes free) and a width
// bug is fixed in one place.

// PadRight pads s with spaces to the given display width.
func PadRight(s string, width int) string {
	if pad := width - lipgloss.Width(s); pad > 0 {
		return s + strings.Repeat(" ", pad)
	}
	return s
}

// PadLeft right-aligns s in the given display width. It is what a numbering
// column is made of: `24.` and ` 7.` end in the same place, so the labels
// beside them start in the same place.
func PadLeft(s string, width int) string {
	if pad := width - lipgloss.Width(s); pad > 0 {
		return strings.Repeat(" ", pad) + s
	}
	return s
}

// PadLines carries every line of a block out to the given width, so a row a
// frame ends early cannot show the last frame's tail through it.
func PadLines(s string, width int) string {
	if width < 1 {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = PadRight(line, width)
	}
	return strings.Join(lines, "\n")
}

// ClipLeft trims s to width display cells from the front, keeping its tail
// and marking the cut with a leading …: the end of a path is the part that
// names it.
func ClipLeft(s string, width int) string {
	if width <= 0 {
		return ""
	}
	over := lipgloss.Width(s) - width
	if over <= 0 {
		return s
	}
	if width == 1 {
		return "…"
	}
	return ansi.TruncateLeft(s, over+1, "…")
}

// Wrap breaks text into unstyled rows no wider than width, between words. A
// word wider than the row is left whole for the frame to clip, never broken:
// a break inside one would be a hyphen the interface invented. Text with no
// words is one empty row, and a width below one leaves the text as it is.
func Wrap(text string, width int) []string {
	if width <= 0 {
		return []string{text}
	}
	if rows := WrapHang(text, width, width); len(rows) > 0 {
		return rows
	}
	return []string{""}
}

// WrapHang is Wrap with a hanging indent: the first row holds first columns
// and every row after it holds rest, the difference being the room a head or
// a term takes on the first row. The rows carry no indent of their own; the
// caller puts its head in front of the first and its hang in front of the
// rest. When the first word does not fit beside the head and the head is the
// narrower row, the first row comes back empty and the word opens the next.
func WrapHang(text string, first, rest int) []string {
	var (
		rows []string
		line string
		room = first
	)
	for _, word := range strings.Fields(text) {
		w := lipgloss.Width(word)
		switch {
		case line == "" && len(rows) == 0 && w > first && first < rest:
			rows = append(rows, "")
			room = rest
			line = word
		case line == "":
			line = word
		case lipgloss.Width(line)+1+w <= room:
			line += " " + word
		default:
			rows = append(rows, line)
			room = rest
			line = word
		}
	}
	if line != "" {
		rows = append(rows, line)
	}
	return rows
}

// WrapParagraphs is Wrap over text that already has breaks: each paragraph is
// wrapped on its own and a blank one stays blank, because joining them would
// be the surface editing what its author wrote.
func WrapParagraphs(text string, width int) []string {
	var rows []string
	for _, para := range strings.Split(text, "\n") {
		if strings.TrimSpace(para) == "" {
			rows = append(rows, "")
			continue
		}
		rows = append(rows, Wrap(para, width)...)
	}
	return rows
}
