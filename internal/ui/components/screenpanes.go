package components

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// The body a screen with a list and a preview draws
// (docs/interface/surfaces.md#the-supporting-screens).
//
// Three take-over screens are the same shape underneath: a list on the left,
// the one thing the pointer is on to the right of it, and a terminal too
// narrow for two columns stacking them instead. History over past commands,
// snippets over saved ones and the saved-chat browser over conversations all
// want the same three answers — how wide the list is, when the panes stack,
// and which of the two gives way when the rows run out — and a copy each is
// how the answers come to differ for no reason a reader could name.
//
// So the arithmetic is here and the numbers are the screen's. Every one of
// them is a fact about what that screen's rows carry: a list whose rows hold
// four fields needs half the terminal and one whose rows hold a name does
// not, and the floor under a preview is however many lines it takes to say
// what the pointer is on.

// screenPanes is one screen's split. The two closures render the panes; the
// four numbers say how the rows and the columns are shared out.
type screenPanes struct {
	// stackAt is the width below which the panes stack rather than sitting
	// side by side.
	stackAt int
	// listMin and listMax bound the list's column in the two-pane layout,
	// which is otherwise half of what there is.
	listMin, listMax int
	// minPreview is the smallest preview the stacked layout leaves standing.
	// Below it the list takes the whole body, because a screen that cannot
	// preview an item can still say which items there are.
	minPreview int

	list    func(width, budget int) []string
	preview func(width int) []string
}

// rows is the body: the two panes side by side where the terminal can carry
// two columns, and stacked where it cannot.
func (p screenPanes) rows(width, budget int) []string {
	if width < p.stackAt {
		return p.stackedRows(width, budget)
	}
	listWidth := min(max(width/2, p.listMin), p.listMax)
	paneWidth := max(width-listWidth-lipgloss.Width(reviewDivider), 8)
	list := p.list(listWidth, budget)
	pane := p.preview(paneWidth)
	rows := max(len(list), len(pane))
	if budget > 0 {
		rows = min(rows, budget)
	}
	return joinReviewPanes(list, pane, listWidth, rows)
}

// stackedRows is the narrow layout: the list above, the preview below,
// nothing truncated sideways.
func (p screenPanes) stackedRows(width, budget int) []string {
	pane := p.preview(width)
	if budget <= 0 {
		return append(append(p.list(width, 0), screenRule(width)), pane...)
	}
	// The rule between the panes costs a row.
	avail := budget - 1
	if avail < p.minPreview+2 {
		// No room for both: the list wins, because a screen that cannot preview an
		// item can still say which items there are.
		return truncRows(p.list(width, budget), budget, width)
	}
	keep := min(len(pane), max(avail/2, p.minPreview))
	rows := p.list(width, avail-keep)
	rows = append(rows, screenRule(width))
	return append(rows, truncRows(pane, keep, width)...)
}

// paneTitle is the row a preview opens with: what the pointer is on, and the
// short field that qualifies it right-aligned at the other end. Both arrive
// painted, because which token each of them spends is the screen's own
// reading — the two fields are a name and a date on one screen and a
// timestamp and a verb on the next.
//
// The right-hand field is what goes when the pane cannot carry both: it
// qualifies the subject, and a subject clipped to keep its qualifier would
// leave the pane naming nothing.
func paneTitle(left, right string, width int) string {
	if pad := width - lipgloss.Width(left) - lipgloss.Width(right); pad >= 2 && right != "" {
		return left + strings.Repeat(" ", pad) + right
	}
	return Clip(left, width)
}
