package components

import (
	"strings"

	"github.com/rfizzle/shhh/internal/ui/keys"
)

// heldLineRows bounds how much of a held line the card shows. A line is a
// sentence for a colleague, and a card that grew with whatever it was sent
// would take the screen from the turn it is asking about.
const heldLineRows = 6

// HeldLine is the card a line from another session waits on while
// sessions.inbound holds it: who sent it, what it says, and the two answers.
// It has no default, because passing another session's words to the turn is
// a decision and not a confirmation of something already under way — enter
// and esc answer nothing here
// (docs/capabilities/sessions-and-memory.md#a-session-can-hand-another-a-line).
type HeldLine struct {
	// From is who sent it, as the steer row names it.
	From string
	Text string
	// More is how many other lines wait behind this one.
	More int
}

// View draws the card at the given width.
func (h HeldLine) View(width int) string {
	card := Card{Title: "a line from " + h.From, Tone: CardDecision, Chips: []string{"held"}}
	inner := card.Inner(width)
	var rows []string
	for _, para := range strings.Split(strings.TrimSpace(h.Text), "\n") {
		for _, l := range wrapPlain(para, inner) {
			rows = append(rows, sty.Body.Render(l))
		}
	}
	if len(rows) > heldLineRows {
		rows = append(rows[:heldLineRows-1], sty.Dim.Render("…"))
	}
	rows = append(rows, sty.Dim.Render("it joins the turn as a steer; it grants nothing"))
	if h.More > 0 {
		rows = append(rows, sty.Dim.Render(plural(h.More, "more line")+" waiting behind it"))
	}
	rows = append(rows, cardRule)
	rows = append(rows, CardHintRows([]KeyOffer{
		OfferAs(keys.Confirm.Yes, "pass it to the turn"),
		OfferAs(keys.Decision.Refuse, "drop it"),
	}, width)...)
	return card.Render(rows, width)
}
