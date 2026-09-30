package components

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// The queue: what was typed while a turn ran and has not been sent yet
// (docs/interface/surfaces.md#the-input-frame). It is drawn two ways — as
// rows above the box while the draft has the keyboard, and as a card once
// the keyboard has moved into it — and both are drawn from the same list, in
// the order the messages will go out.

// QueuedMessage is one message waiting for the turn.
type QueuedMessage struct {
	// FollowUp is a message waiting for the turn to end; otherwise it is
	// steering, which joins the turn at its next round.
	FollowUp bool
	Text     string
	// Handles are the attachments riding with it, by the handle its chip
	// had, so a message that carries a log says so on its row.
	Handles []string
}

// queueKindWidth is the kind column, as wide as its longer word.
const queueKindWidth = len("follow-up")

// queueCardRows bounds how many messages the card lists before it counts the
// rest: a queue long enough to need more is a queue to cancel, not to read.
const queueCardRows = 8

func (q QueuedMessage) kind() string {
	if q.FollowUp {
		return "follow-up"
	}
	return "steering"
}

// row is the message on one line: its kind, the first line of what it says,
// and what rides with it, clipped to width. The handles are kept whatever
// the text gives up, because a message that loses its attachments from its
// row reads as a message without any.
func (q QueuedMessage) row(width int) string {
	lead := sty.Info.Render(fmt.Sprintf("%-*s", queueKindWidth, q.kind())) + "  "
	room := width - queueKindWidth - 2
	text, _, more := strings.Cut(strings.TrimSpace(q.Text), "\n")
	text = strings.TrimSpace(text)
	if more {
		text += " …"
	}
	var tail string
	if len(q.Handles) > 0 {
		tail = " · " + strings.Join(q.Handles, " ")
		room -= lipgloss.Width(tail)
	}
	if room < 1 {
		return Clip(lead+sty.Body.Render(text)+sty.Dim.Render(tail), width)
	}
	return lead + sty.Body.Render(Clip(text, room)) + sty.Dim.Render(tail)
}

// QueueRows is the queue above the box: one row per message, in delivery
// order, at most max rows. A queue longer than that keeps its oldest and
// says how many more wait behind them, since the oldest is the next to go.
func QueueRows(msgs []QueuedMessage, width, max int) []string {
	if len(msgs) == 0 || width <= 0 || max <= 0 {
		return nil
	}
	shown := msgs
	var more int
	if len(msgs) > max {
		shown, more = msgs[:max-1], len(msgs)-(max-1)
	}
	rows := make([]string, 0, len(shown)+1)
	for _, q := range shown {
		rows = append(rows, q.row(width))
	}
	if more > 0 {
		rows = append(rows, sty.Dim.Render(Clip(fmt.Sprintf("+%d more queued", more), width)))
	}
	return rows
}

// QueueCard is the queue once the keyboard is in it: the same rows with the
// pointer on one, and the keys that act on it.
type QueueCard struct {
	Messages []QueuedMessage
	// Selected is the row the pointer is on, or -1 when the message it was
	// on has been sent from under it.
	Selected int
	// Held says the follow-ups are held after a cancel, and will not send
	// themselves until one is queued again.
	Held bool
}

// View draws the card at the given width.
func (c QueueCard) View(width int) string {
	var chips []string
	if c.Held {
		chips = []string{"follow-ups held"}
	}
	card := Card{Title: "queued · " + plural(len(c.Messages), "message"), Tone: CardChrome, Chips: chips}
	inner := card.Inner(width)
	var rows []string
	if len(c.Messages) == 0 {
		rows = append(rows, sty.Dim.Render("nothing is queued — everything was sent"))
	}
	// The window follows the pointer: it starts at the top and moves only as
	// far as it must to keep the selected row in it.
	start := 0
	if c.Selected >= queueCardRows {
		start = c.Selected - queueCardRows + 1
	}
	end := min(len(c.Messages), start+queueCardRows)
	if start > 0 {
		rows = append(rows, sty.Dim.Render(fmt.Sprintf("  %d more above", start)))
	}
	for i := start; i < end; i++ {
		body := c.Messages[i].row(inner - GridPointerWidth)
		if i == c.Selected {
			rows = append(rows, sty.FocusPointer.Render("❯")+" "+LitRow(body, 0, lipgloss.Width(body)))
			continue
		}
		rows = append(rows, "  "+body)
	}
	if end < len(c.Messages) {
		rows = append(rows, sty.Dim.Render(fmt.Sprintf("  %d more below", len(c.Messages)-end)))
	}
	for _, l := range wrapPlain("steering joins the turn at its next round; a follow-up goes when it ends", inner) {
		rows = append(rows, sty.Dim.Render(l))
	}
	rows = append(rows, cardRule)
	rows = append(rows, CardHintRows(withKeyListOffer([]KeyOffer{
		Offer(keys.Queue.Move),
		Offer(keys.Queue.Edit),
		Offer(keys.Queue.Cancel),
		Offer(keys.Queue.Back),
	}), width)...)
	return card.Render(rows, width)
}
