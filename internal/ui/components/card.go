package components

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// cardFrameWidth is what the border and inner padding consume of the total
// width ("│ " + " │").
const cardFrameWidth = 4

// minCardWidth is the narrowest card worth framing; below it rows render
// bare.
const minCardWidth = 12

// narrowWidth is the threshold below which hint rows stack one segment per
// line instead of truncating (AGENTS.md).
const narrowWidth = 60

// Clip truncates s to the given display width, ANSI-aware, ending with … when
// anything was dropped. It measures display cells rather than bytes or runes,
// which is why a surface outside this package reaches for it rather than
// writing its own: a slice by rune cuts a wide glyph in half and a slice by
// byte cuts an escape sequence in half.
func Clip(s string, width int) string {
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
// the body (docs/interface/surfaces.md#the-approval-card). It is a
// sentinel because rows are already rendered strings by the time the frame
// sees them.
const cardRule = "\x00rule"

// Card is a card's frame beyond its rows: the title, the chips that ride the
// top border right-aligned, and the border colour. A zero value is the plain
// gray frame every other card has always had.
type Card struct {
	Title string
	// Chips sit at the right end of the top border, joined by ─ separators.
	// They drop from the front as the terminal narrows, so the last chip —
	// the one that leads the decision — is the one that survives.
	Chips []string
	// Style colours the border; nil is the default gray.
	Style *lipgloss.Style
}

// Inner is the width a card's rows are laid out in at the given total width:
// the frame takes four columns and the rows get the rest. Callers wrap and
// measure against this rather than against the total, which is why it is a
// method and not four columns subtracted in eight places.
func (c Card) Inner(width int) int { return max(width-cardFrameWidth, 1) }

// Render frames rows in the card: the title in the top border, the chips at
// its right end (docs/interface/surfaces.md#the-approval-card). Rows are
// clipped and padded to the inner width.
func (c Card) Render(rows []string, width int) string {
	border := sty.Border
	if c.Style != nil {
		border = *c.Style
	}
	if width < minCardWidth {
		return strings.Join(dropRules(rows), "\n")
	}
	inner := width - cardFrameWidth

	var b strings.Builder
	b.WriteString(cardTop(c, border, width))
	for _, row := range rows {
		if row == cardRule {
			b.WriteString("\n" + border.Render("├"+strings.Repeat("─", max(0, width-2))+"┤"))
			continue
		}
		row = Clip(row, inner)
		pad := strings.Repeat(" ", max(0, inner-lipgloss.Width(row)))
		b.WriteString("\n" + border.Render("│") + " " + row + pad + " " + border.Render("│"))
	}
	b.WriteString("\n" + border.Render("└"+strings.Repeat("─", max(0, width-2))+"┘"))
	return b.String()
}

// cardTop draws the top border: the title on the left, the chips on the
// right, and the texture between them. Chips are dropped from the front until
// what is left fits beside the title; a title that still does not fit is
// clipped, which is the one thing that never happens to a chip.
func cardTop(c Card, border lipgloss.Style, width int) string {
	left := "┌─ " + c.Title + " "
	chips := c.Chips
	for {
		right := chipRun(chips)
		if lipgloss.Width(left)+lipgloss.Width(right)+1 <= width-1 {
			fill := max(0, width-1-lipgloss.Width(left)-lipgloss.Width(right))
			return paintCardTop(border, left, fill, right+"┐")
		}
		if len(chips) == 0 {
			break
		}
		chips = chips[1:]
	}
	left = Clip(left, width-1)
	return paintCardTop(border, left, max(0, width-1-lipgloss.Width(left)), "┐")
}

// paintCardTop paints the three parts of the top edge. The fill is drawn in
// the one chrome tone whatever the frame's own colour is: a card's border
// carries how much the decision on it weighs, and the run between the title
// and the chips carries nothing, so the weight stays on the parts that mean
// something — the corners, the title's lead-in and the chips. That is also
// what makes this edge and a screen's title rule the same material at the
// same tone, rather than the same shape in two colours
// (docs/interface/surfaces.md#the-approval-card).
//
// Under mono there is no second tone to hold, so the whole row goes through
// the frame's own style in one call — which is the row the frame drew before
// there was a texture, byte for byte. An edge with no room left for a fill
// takes that call too: a style renders a pair of escapes around an empty
// string, and three runs where there is nothing between the title and the
// corner is two of those for nothing.
func paintCardTop(border lipgloss.Style, left string, fill int, right string) string {
	if Mono() || fill <= 0 {
		return border.Render(left + textureFill(fill) + right)
	}
	return border.Render(left) + sty.Dim.Render(textureFill(fill)) + border.Render(right)
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

// notYetLiveWords is what a key row says about itself while the surface
// offering it does not hold the keyboard. It is words rather than a border
// colour because invariant 1 does not stop applying to the state of a key.
const notYetLiveWords = "not live yet"

// notYetLiveRows renders a decision surface's key row while that surface does
// not hold the keyboard
// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard,
// invariant 5). The keys are dimmed and said to be waiting in words, and
// handover — the one key that is live — is offered underneath with what it
// does and what the letters do until it is pressed. A key that is not yet
// live is a different thing from
// one that cannot be pressed at all (the palette's ⊘), so the two never render
// alike: this one is waiting for the keyboard, that one is refused.
func notYetLiveRows(keys, handover string, width int) []string {
	inner := Card{}.Inner(width)
	keys = Clip(keys, inner)
	rows := make([]string, 0, 3)
	// The words sit on the key row itself where the terminal carries them,
	// so the state is read in the same glance as the keys it describes.
	if pad := inner - lipgloss.Width(keys) - lipgloss.Width(notYetLiveWords); pad >= 2 {
		rows = append(rows, sty.Dimmer.Render(keys)+strings.Repeat(" ", pad)+sty.Dim.Render(notYetLiveWords))
	} else {
		rows = append(rows, sty.Dimmer.Render(keys), sty.Dim.Render(Clip(notYetLiveWords, inner)))
	}
	if handover != "" {
		rows = append(rows, handoverRow(handover, inner))
	}
	return rows
}

// graceWords is what the key row says while an arrival's grace window holds
// its keys: the same dimmed-run grammar as not-yet-live, with a phrase that
// promises the keys rather than a chord, because nothing needs pressing —
// the window ends the moment the keyboard has been quiet for a beat.
const graceWords = "keys live in a moment"

// graceRows renders the key row of a card whose grace window is open. It is
// the not-yet-live row's shape — dim keys, the state in words in the same
// glance (invariant 1: the dimming never carries the meaning alone) — with
// no handover row, because the card already holds the keyboard.
func graceRows(keys string, width int) []string {
	inner := Card{}.Inner(width)
	keys = Clip(keys, inner)
	if pad := inner - lipgloss.Width(keys) - lipgloss.Width(graceWords); pad >= 2 {
		return []string{sty.Dimmer.Render(keys) + strings.Repeat(" ", pad) + sty.Dim.Render(graceWords)}
	}
	return []string{sty.Dimmer.Render(keys), sty.Dim.Render(Clip(graceWords, inner))}
}

// handoverRow is the one live key on a not-yet-live surface. Its wording is
// the card's rather than the caller's, because the mid-sentence rule fixes
// it: the key, what it does, and where the letters go until it is pressed.
func handoverRow(key string, inner int) string {
	head := sty.Info.Render("["+key+"]") + sty.Body.Render(" answer it")
	tail := sty.Dim.Render(" — until then these letters go into your draft")
	if lipgloss.Width(head)+lipgloss.Width(tail) > inner {
		return Clip(head, inner)
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
// beside them start in the same place.
func padLeft(s string, width int) string {
	if pad := width - lipgloss.Width(s); pad > 0 {
		return strings.Repeat(" ", pad) + s
	}
	return s
}
