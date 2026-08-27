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

// cardRule is a row that renders as a horizontal rule across the card rather
// than as content: the divider that keeps the key hints from blending into
// the body (S-101, DESIGN-TUI.md §2). It is a sentinel because rows are
// already rendered strings by the time the frame sees them.
const cardRule = "\x00rule"

// cardChrome is a card's frame beyond its rows: the title, the chips that
// ride the top border right-aligned, and the border colour. A zero value is
// the plain gray frame every other card has always had.
type cardChrome struct {
	title string
	// chips sit at the right end of the top border, joined by ─ separators.
	// They drop from the front as the terminal narrows, so the last chip —
	// the one that leads the decision — is the one that survives.
	chips []string
	// style colours the border; nil is the default gray.
	style *lipgloss.Style
}

// renderCard frames rows in a box with the title in the top border
// (DESIGN-TUI.md §2). Rows are clipped and padded to the inner width.
func renderCard(title string, rows []string, width int) string {
	return renderChromeCard(cardChrome{title: title}, rows, width)
}

// renderChromeCard is renderCard with the top border's chips and the border
// colour under the caller's control.
func renderChromeCard(c cardChrome, rows []string, width int) string {
	border := borderStyle
	if c.style != nil {
		border = *c.style
	}
	if width < minCardWidth {
		return strings.Join(dropRules(rows), "\n")
	}
	inner := width - cardFrameWidth

	var b strings.Builder
	b.WriteString(border.Render(cardTop(c, width)))
	for _, row := range rows {
		if row == cardRule {
			b.WriteString("\n" + border.Render("├"+strings.Repeat("─", max(0, width-2))+"┤"))
			continue
		}
		row = clip(row, inner)
		pad := strings.Repeat(" ", max(0, inner-lipgloss.Width(row)))
		b.WriteString("\n" + border.Render("│") + " " + row + pad + " " + border.Render("│"))
	}
	b.WriteString("\n" + border.Render("└"+strings.Repeat("─", max(0, width-2))+"┘"))
	return b.String()
}

// cardTop draws the top border: the title on the left, the chips on the
// right, and the rule between them. Chips are dropped from the front until
// what is left fits beside the title; a title that still does not fit is
// clipped, which is the one thing that never happens to a chip.
func cardTop(c cardChrome, width int) string {
	left := "┌─ " + c.title + " "
	chips := c.chips
	for {
		right := chipRun(chips)
		if lipgloss.Width(left)+lipgloss.Width(right)+1 <= width-1 {
			fill := max(0, width-1-lipgloss.Width(left)-lipgloss.Width(right))
			return left + strings.Repeat("─", fill) + right + "┐"
		}
		if len(chips) == 0 {
			break
		}
		chips = chips[1:]
	}
	left = clip(left, width-1)
	return left + strings.Repeat("─", max(0, width-1-lipgloss.Width(left))) + "┐"
}

// chipRun renders the chips as they sit in the border: each between ─ and a
// space, so they read as labels on the rule rather than as content.
func chipRun(chips []string) string {
	if len(chips) == 0 {
		return ""
	}
	var b strings.Builder
	for _, chip := range chips {
		b.WriteString("─ " + chip + " ")
	}
	return b.String()
}

// dropRules removes rule sentinels for the unframed narrow rendering, where
// there is no frame for a divider to span.
func dropRules(rows []string) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		if row != cardRule {
			out = append(out, row)
		}
	}
	return out
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

// notYetLiveWords is what a key row says about itself while the surface
// offering it does not hold the keyboard. It is words rather than a border
// colour because invariant 1 does not stop applying to the state of a key.
const notYetLiveWords = "not live yet"

// notYetLiveRows renders a decision surface's key row while that surface does
// not hold the keyboard (DESIGN-TUI.md §7b, invariant 5, S-117). The keys are
// dimmed and said to be waiting in words, and handover — the one key that is
// live — is offered underneath with what it does and what the letters do
// until it is pressed. A key that is not yet live is a different thing from
// one that cannot be pressed at all (§18a's ⊘), so the two never render
// alike: this one is waiting for the keyboard, that one is refused.
func notYetLiveRows(keys, handover string, width int) []string {
	inner := max(width-cardFrameWidth, 1)
	keys = clip(keys, inner)
	rows := make([]string, 0, 3)
	// The words sit on the key row itself where the terminal carries them,
	// so the state is read in the same glance as the keys it describes.
	if pad := inner - lipgloss.Width(keys) - lipgloss.Width(notYetLiveWords); pad >= 2 {
		rows = append(rows, dimmerStyle.Render(keys)+strings.Repeat(" ", pad)+dimStyle.Render(notYetLiveWords))
	} else {
		rows = append(rows, dimmerStyle.Render(keys), dimStyle.Render(clip(notYetLiveWords, inner)))
	}
	if handover != "" {
		rows = append(rows, handoverRow(handover, inner))
	}
	return rows
}

// handoverRow is the one live key on a not-yet-live surface. Its wording is
// the card's rather than the caller's, because §7b fixes it: the key, what it
// does, and where the letters go until it is pressed.
func handoverRow(key string, inner int) string {
	head := infoStyle.Render("["+key+"]") + bodyStyle.Render(" answer it")
	tail := dimStyle.Render(" — until then these letters go into your draft")
	if lipgloss.Width(head)+lipgloss.Width(tail) > inner {
		return clip(head, inner)
	}
	return head + tail
}

// padRight pads s with spaces to the given display width.
func padRight(s string, width int) string {
	if pad := width - lipgloss.Width(s); pad > 0 {
		return s + strings.Repeat(" ", pad)
	}
	return s
}

// padLeft right-aligns s in the given display width. It is what a numbering
// column is made of: `24.` and ` 7.` end in the same place, so the labels
// beside them start in the same place (§4a).
func padLeft(s string, width int) string {
	if pad := width - lipgloss.Width(s); pad > 0 {
		return strings.Repeat(" ", pad) + s
	}
	return s
}
