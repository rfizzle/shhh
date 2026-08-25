package components

// The approval queue strip (S-102, DESIGN-TUI.md §2e; the Approvals artboard
// in the shhh Design System project). Five separate cards, one after the
// other, is how you train someone to hit enter without reading. The strip is
// the alternative: what is stacked behind this decision, in the order it will
// be asked, and — before the key applies it — which of them one keystroke
// would answer together.

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// queueMarkKey is the mark a batchable row carries: the key that would answer
// it, written where the row is, so membership is a fact on the row rather
// than a count on the card.
const queueMarkKey = "[A]"

// QueueItem is one pending decision as the strip lists it.
type QueueItem struct {
	// Number is the item's place in the round, 1-based — the round's
	// numbering, not the remaining queue's, so an item keeps its number as
	// the ones ahead of it are answered.
	Number int
	// Label is the action in one line: the command, or the verb and path.
	Label string
	// Detail is a fact that belongs beside the rating rather than inside the
	// label — an edit's +N −M — so a long path shortens and the fact does
	// not.
	Detail string
	// Severity is the item's rating, printed as a word like everywhere else.
	Severity Severity
	// Batch marks an item [A] would answer along with the current one.
	Batch bool
}

// QueueStrip lists the approvals stacked behind the card it sits above. It
// renders nothing for a single decision: one card is already the whole queue.
type QueueStrip struct {
	// Items are the pending decisions in the order they will be asked. The
	// first is the one the card below is showing.
	Items []QueueItem
	// Note rides the header beside the count, e.g. "[A] answers the 3
	// marked". It is what states the batch's membership before it applies.
	Note string
	// MaxRows bounds the item rows; what does not fit is counted on a final
	// row rather than dropped silently. 0 means unbounded.
	MaxRows int
}

// View renders the strip as one row per element, or nil when there is nothing
// stacked. The caller places it directly above the card and takes its height
// out of the card's own budget.
func (q QueueStrip) View(width int) []string {
	if len(q.Items) < 2 {
		return nil
	}
	rows := []string{q.header(width)}
	items, hidden := q.Items, []QueueItem(nil)
	if q.MaxRows > 0 && len(items) > q.MaxRows {
		// The last row is spent saying how many are not shown, so the strip
		// never implies the queue ends where the list does.
		items, hidden = items[:q.MaxRows-1], items[q.MaxRows-1:]
	}
	for i, item := range items {
		rows = append(rows, item.render(width, i == 0))
	}
	if len(hidden) > 0 {
		rows = append(rows, queueIndent+dimStyle.Render(overflowRow(hidden)))
	}
	return rows
}

// Rows is how many lines View would render, so a host can reserve them before
// it lays out the surface beneath.
func (q QueueStrip) Rows() int {
	if len(q.Items) < 2 {
		return 0
	}
	if q.MaxRows > 0 && len(q.Items) > q.MaxRows {
		return 1 + q.MaxRows
	}
	return 1 + len(q.Items)
}

// overflowRow counts what the strip could not show — and, when the header has
// claimed a batch, how many of those are in it, so the marks on screen never
// have to account for the whole count on their own.
func overflowRow(hidden []QueueItem) string {
	marked := 0
	for _, item := range hidden {
		if item.Batch {
			marked++
		}
	}
	row := "… " + strconv.Itoa(len(hidden)) + " more"
	if marked > 0 {
		row += ", " + strconv.Itoa(marked) + " marked"
	}
	return row
}

// queueIndent insets the strip from the card below it, so the two read as a
// list above a surface rather than as one taller frame.
const queueIndent = "  "

// header is the dot run — one per decision still waiting, the current one
// filled — the count in words, and the note that names what [A] covers.
func (q QueueStrip) header(width int) string {
	dots := spinTextStyle.Render("●") + dimStyle.Render(strings.Repeat("○", len(q.Items)-1))
	head := dots + dimStyle.Render("  "+strconv.Itoa(len(q.Items))+" pending")
	if q.Note != "" {
		head += dimStyle.Render("  ·  ") + infoStyle.Render(q.Note)
	}
	return queueIndent + clip(head, max(width-len(queueIndent), 0))
}

// render lays one item: the pointer column, its number in the round, the
// action, and — right-aligned — its severity and batch mark. The label is the
// only part that gives up width, because it is the only part that can be
// shortened and still say something.
func (item QueueItem) render(width int, current bool) string {
	pointer := "  "
	if current {
		pointer = spinTextStyle.Render("▸") + " "
	}
	number := strconv.Itoa(item.Number) + " "
	right := item.right()

	// Indent, pointer, number, one gap column, then the right-hand block.
	room := width - len(queueIndent) - 2 - len(number) - 2 - lipgloss.Width(right)
	label := clip(item.Label, max(room, 0))
	style := dimStyle
	if current {
		style = bodyStyle
	}
	pad := strings.Repeat(" ", max(room-lipgloss.Width(label), 0))
	return queueIndent + pointer + dimStyle.Render(number) + style.Render(label) + pad + "  " + right
}

// right is the item's detail and rating and, when [A] would answer it, the
// key that would. All three are words: a row's membership is never a hue.
func (item QueueItem) right() string {
	var b strings.Builder
	if item.Detail != "" {
		b.WriteString(dimmerStyle.Render(item.Detail))
	}
	if word := item.Severity.Word(); word != "" {
		if b.Len() > 0 {
			b.WriteString("  ")
		}
		style := dimStyle
		if item.Severity >= SeverityMedium {
			style = warnStyle
		}
		b.WriteString(style.Render(word))
	}
	if item.Batch {
		if b.Len() > 0 {
			b.WriteString("  ")
		}
		b.WriteString(infoStyle.Render(queueMarkKey))
	}
	return b.String()
}
