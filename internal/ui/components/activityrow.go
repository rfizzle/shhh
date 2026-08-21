package components

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// ActivityKind selects an activity row's glyph and accent (DESIGN-TUI.md §6).
type ActivityKind int

const (
	ActivityTool     ActivityKind = iota // ⚙ read-only tool
	ActivityCommand                      // $ command
	ActivityEdit                         // ✎ edit/write
	ActivitySubagent                     // ◇ sub-agent
)

// ActivityRow is the compact one-row rendering of a tool call:
// glyph name key-arg → outcome · counts · duration. It is a passive
// transcript renderer — focus mode (§7) owns expansion keys, so the row has
// no Update.
type ActivityRow struct {
	Kind     ActivityKind
	Name     string
	Arg      string
	Outcome  string
	Counts   string
	Duration string
	// Detail is the bounded detail body shown when Expanded; failed rows
	// auto-expand with error lines first.
	Detail    []string
	MaxDetail int
	// Tail is a running command's last output line, shown live beneath the
	// row.
	Tail     string
	Running  bool
	Failed   bool
	Expanded bool
	// Selected draws the focus-mode gutter pointer.
	Selected bool
}

// glyph is the row's state glyph: ✗ failure and ▸ running override the kind
// glyph.
func (r ActivityRow) glyph() string {
	switch {
	case r.Failed:
		return errStyle.Render("✗")
	case r.Running:
		return spinTextStyle.Render("▸")
	}
	switch r.Kind {
	case ActivityCommand:
		return accentStyle.Render("$")
	case ActivityEdit:
		return accentStyle.Render("✎")
	case ActivitySubagent:
		return infoStyle.Render("◇")
	default:
		return accentStyle.Render("⚙")
	}
}

// View renders the row (plus tail/detail lines) at the given width.
func (r ActivityRow) View(width int) string {
	gutter := "  "
	if r.Selected {
		gutter = focusRowStyle.Render("❯") + " "
	}

	var right []string
	if r.Outcome != "" {
		right = append(right, r.Outcome)
	}
	if r.Counts != "" {
		right = append(right, r.Counts)
	}
	rightStr := dimStyle.Render(strings.Join(right, " · "))
	if r.Duration != "" {
		rightStr += "  " + dimmerStyle.Render(r.Duration)
	}

	left := gutter + r.glyph() + " " + r.Name
	if r.Arg != "" {
		left += "  " + dimStyle.Render(clip(r.Arg, max(width/2, 8)))
	}
	gap := width - lipgloss.Width(left) - lipgloss.Width(rightStr)
	row := left
	if rightStr != "" {
		if gap < 2 {
			row = clip(left, max(width-lipgloss.Width(rightStr)-2, 0)) + "  " + rightStr
		} else {
			row += strings.Repeat(" ", gap) + rightStr
		}
	}
	lines := []string{row}

	if r.Running && r.Tail != "" {
		lines = append(lines, "      "+dimmerStyle.Render(clip(r.Tail, max(width-6, 1))))
	}
	// Failed rows auto-expand to their bounded detail; successful rows stay
	// collapsed until focus mode expands them.
	if (r.Expanded || r.Failed) && len(r.Detail) > 0 {
		detail := r.Detail
		if r.MaxDetail > 0 && len(detail) > r.MaxDetail {
			detail = detail[:r.MaxDetail]
		}
		for _, d := range detail {
			lines = append(lines, "      "+dimmerStyle.Render(clip(d, max(width-6, 1))))
		}
	}
	return strings.Join(lines, "\n")
}
