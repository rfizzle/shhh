package components

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// cardFrameWidth is what the border and inner padding consume of the total
// width ("│ " + " │").
const cardFrameWidth = 4

// minCardWidth is the narrowest card worth framing; below it rows render bare.
const minCardWidth = 12

// narrowWidth is the threshold below which hint rows stack one segment per
// line instead of truncating (DESIGN-TUI.md §11).
const narrowWidth = 60

// clip truncates s to the given display width, ANSI-aware, ending with … when
// anything was dropped.
func clip(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	return ansi.Truncate(s, width, "…")
}

// renderCard frames rows in a box with the title in the top border
// (DESIGN-TUI.md §2). Rows are clipped and padded to the inner width.
func renderCard(title string, rows []string, width int) string {
	if width < minCardWidth {
		return strings.Join(rows, "\n")
	}
	inner := width - cardFrameWidth

	var b strings.Builder
	top := "┌─ " + title + " "
	if lipgloss.Width(top) > width-1 {
		top = clip(top, width-1)
	}
	b.WriteString(borderStyle.Render(top + strings.Repeat("─", max(0, width-1-lipgloss.Width(top))) + "┐"))
	for _, row := range rows {
		row = clip(row, inner)
		pad := strings.Repeat(" ", max(0, inner-lipgloss.Width(row)))
		b.WriteString("\n" + borderStyle.Render("│") + " " + row + pad + " " + borderStyle.Render("│"))
	}
	b.WriteString("\n" + borderStyle.Render("└"+strings.Repeat("─", max(0, width-2))+"┘"))
	return b.String()
}

// hintRows renders key-hint segments: joined with · on one line when the
// width allows, stacked one per line on narrow terminals rather than
// truncated.
func hintRows(segments []string, width int) []string {
	joined := strings.Join(segments, " · ")
	if width >= narrowWidth || lipgloss.Width(joined) <= width-cardFrameWidth {
		return []string{hintStyle.Render(joined)}
	}
	rows := make([]string, 0, len(segments))
	for _, s := range segments {
		rows = append(rows, hintStyle.Render(s))
	}
	return rows
}

// padRight pads s with spaces to the given display width.
func padRight(s string, width int) string {
	if pad := width - lipgloss.Width(s); pad > 0 {
		return s + strings.Repeat(" ", pad)
	}
	return s
}
