package components

// The ChatGPT plan's panels (docs/capabilities/chatgpt-plan.md#in-a-session):
// the one that waits on the browser, the one that says a sign-in came back
// without plan use, and the one-time confirmation that the plan is paying.
// They are one component because they are one shape: a card that is about
// to be answered or left, with its prose and the keys it takes.

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// PlanPanel is a card of prose over the keys it offers.
type PlanPanel struct {
	// Title is in the card's top border.
	Title string
	// Body is paragraphs, laid out in the card's width; an empty string is a
	// blank row. A line that is one word wider than the card — the sign-in
	// address — is broken at the card's width rather than clipped.
	Body []string
	// Offers are the keys under the prose.
	Offers []KeyOffer
}

// View draws the panel at the given width.
func (p PlanPanel) View(width int) string {
	inner := Card{}.Inner(width)
	var rows []string
	for _, para := range p.Body {
		if para == "" {
			rows = append(rows, "")
			continue
		}
		if f := strings.Fields(para); len(f) == 1 && lipgloss.Width(f[0]) > inner && inner > 0 {
			// An address is one word, and a screen that clipped it would
			// leave the person nothing to open.
			rs := []rune(f[0])
			for len(rs) > 0 {
				n := min(inner, len(rs))
				rows = append(rows, sty.accent.Render(string(rs[:n])))
				rs = rs[n:]
			}
			continue
		}
		rows = append(rows, wrapSpans([]styledSpan{{para, sty.body}}, inner)...)
	}
	var foot []string
	if len(p.Offers) > 0 {
		foot = packOffers(p.Offers, inner)
	}
	border := sty.accent
	return Card{Title: p.Title, style: &border}.Build(rows, foot, width)
}
