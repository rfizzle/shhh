package components

// The backlog screen's left pane: the window of items, the filter row over
// it, the count of what the filters took out from under it, and how one item
// is drawn as a row. It is its own file because the row is where the
// project's own vocabulary lands — the letter each field is drawn as, the
// order the priorities are read in, the words a state says — which is a
// decision apart from what the pane beside it shows.

import (
	"fmt"

	"charm.land/lipgloss/v2"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// splitRows is the wide layout: the list on the left and the item beside it.
func (b *BacklogScreen) splitRows(width, budget int) []string {
	listWidth := min(max(width*2/5, backlogListMin), backlogListMax)
	paneWidth := max(width-listWidth-lipgloss.Width(reviewDivider), 8)
	list := b.listRows(listWidth, budget)
	pane := b.itemRows(paneWidth)
	rows := max(len(list), len(pane))
	if budget > 0 {
		// The pane says what it could not fit rather than ending mid-item:
		// a body is longer than a preview, and `[enter]` is what the row
		// naming the rest is pointing at (invariant 4).
		pane = truncRows(pane, budget, paneWidth)
		rows = min(rows, budget)
	}
	return joinReviewPanes(list, pane, listWidth, rows)
}

// stackedRows is the narrow layout: the list above, the item below, nothing
// truncated sideways (invariant 4).
func (b *BacklogScreen) stackedRows(width, budget int) []string {
	pane := b.itemRows(width)
	if budget <= 0 {
		return append(append(b.listRows(width, 0), screenRule(width)), pane...)
	}
	// The rule between the panes costs a row.
	avail := budget - 1
	if avail < backlogMinBody+2 {
		// No room for both: the list wins, because a screen that cannot show
		// an item can still say which items there are.
		return truncRows(b.listRows(width, budget), budget, width)
	}
	keep := min(len(pane), max(avail/2, backlogMinBody))
	rows := b.listRows(width, avail-keep)
	rows = append(rows, screenRule(width))
	return append(rows, truncRows(pane, keep, width)...)
}

// listRows is the left pane: the filter row where it is open, the window of
// items, and the count of what the filter took out from under it.
func (b *BacklogScreen) listRows(width, budget int) []string {
	head := b.queryRows(width)
	if len(head) > 0 {
		head = append(head, screenRule(width))
	}
	tail := b.hiddenRows(width)
	rows := append(head, b.windowRows(width, listBudget(budget, len(head)+len(tail)))...)
	return append(rows, tail...)
}

// queryRows is the filter row: what has been typed, and where the next
// character goes.
func (b *BacklogScreen) queryRows(width int) []string {
	if !b.filtering {
		return nil
	}
	typed := sty.Info.Render(queryPrompt) + sty.QueryText.Render(b.query+queryCursor)
	if b.query == "" {
		typed += sty.Dim.Render(" type to filter by slug or title")
	}
	return []string{Clip(typed, width)}
}

// hiddenRows is the line under the list saying what the filters took out of
// it, and the key that puts them back. It is drawn only while something is
// hidden: a filter that hid nothing has nothing to confess.
func (b *BacklogScreen) hiddenRows(width int) []string {
	hidden := len(b.rows()) - len(b.shown)
	if hidden <= 0 {
		return nil
	}
	row := sty.Dim.Render(fmt.Sprintf("%d hidden · ", hidden)) +
		sty.Info.Render(keys.Bracket(keys.Backlog.ClearQ)) + sty.Dim.Render(" clear it")
	return []string{screenRule(width), Clip(row, width)}
}

// windowRows is the run of items the budget shows, with the pointer inside
// it. An empty list says which of the two empties it is: a backlog with
// nothing in it and a filter that matched nothing are different answers, and
// only one of them is fixed by clearing something.
func (b *BacklogScreen) windowRows(width, budget int) []string {
	if len(b.shown) == 0 {
		if len(b.rows()) == 0 {
			return []string{sty.Dim.Render(Clip(b.emptyWords(), width))}
		}
		return []string{sty.Dim.Render(Clip("nothing matches the filter", width))}
	}
	lo, hi := b.list.Range(budget)
	rows := make([]string, 0, hi-lo)
	for i := lo; i < hi; i++ {
		rows = append(rows, b.itemRow(b.rows()[b.shown[i]], i == b.list.Focus, width))
	}
	if above := lo; above > 0 {
		rows = append([]string{sty.Dim.Render(Clip(fmt.Sprintf("↑ %d above", above), width))}, rows...)
	}
	if below := len(b.shown) - hi; below > 0 {
		rows = append(rows, sty.Dim.Render(Clip(fmt.Sprintf("↓ %d below", below), width)))
	}
	return rows
}

// emptyWords is what an empty tab says. The backlog's own emptiness names
// the key that ends it; the archive's is a statement of fact and offers
// nothing, because nothing here can archive an item that does not exist.
func (b *BacklogScreen) emptyWords() string {
	switch {
	case b.archived():
		return "nothing archived yet"
	case b.sprinting() && b.Board != nil && b.Board.Closed:
		// A closed sprint is a record and its items are back in the
		// backlog's own tabs. Offering a verb that adds one to a set
		// nothing can be added to would be a key that cannot act.
		return "this sprint is closed; its items are in the backlog and the archive"
	case b.sprinting():
		// The set is empty rather than the backlog, and the file is where
		// that is fixed: the sprint's own list is the one thing on this
		// screen no key here writes.
		return "the sprint names no items · /todo sprint add <slug> puts one in"
	}
	return "no items yet · " + keys.Bracket(keys.Backlog.New) + " starts one"
}

// itemRow is one item on the list. The fields go in the order they
// identify it — the slug, the two grade letters, the state — and the title
// takes what is left of the row. The title is the field that gives ground
// because the pane beside the list carries it in full, which is what makes
// the trade a fold rather than a loss (invariant 4).
func (b *BacklogScreen) itemRow(row BacklogRow, focused bool, width int) string {
	glyph, name := b.rowTone(row)
	pointer := "  "
	if focused {
		pointer, name = sty.FocusPointer.Render("❯ "), brightStyle()
	}
	lead := glyph + " " + name.Render(row.Slug)
	if grade := b.grade(row); grade != "" {
		lead += "  " + sty.Dim.Render(grade)
	}
	inner := max(width-2, 1)
	room := inner - lipgloss.Width(lead)
	if room < 4 {
		return pointer + Clip(lead, inner)
	}
	// The state is the field that clips and the title the field that goes.
	// The order is the row's whole argument: what an item is called and where
	// it stands are why the list is on screen, and the title is a sentence
	// the pane beside it carries in full (invariant 4).
	state := Clip("  "+b.stateWords(row), room)
	rest := room - lipgloss.Width(state)
	if row.Title == "" || rest < minBacklogTitle+2 {
		return pointer + lead + sty.Dim.Render(state)
	}
	return pointer + lead + sty.Dim.Render(state+"  "+Clip(row.Title, rest-2))
}

// rowTone is the row's glyph and the weight its slug carries. The four
// states an active item can be in are the rail's, drawn the same way, so a
// row means the same thing in both places; the two this screen adds are the
// archive's tick and the warning on a file that will not parse.
func (b *BacklogScreen) rowTone(row BacklogRow) (string, lipgloss.Style) {
	switch row.State {
	case BacklogUnreadable:
		return sty.Warn.Render("⚠"), sty.Warn
	case BacklogArchived:
		return sty.Add.Render("✓"), sty.Dim
	case BacklogRunning:
		return todoRowTone(TodoRunning)
	case BacklogBlocked:
		return todoRowTone(TodoBlocked)
	case BacklogWaiting:
		return todoRowTone(TodoWaiting)
	}
	return todoRowTone(TodoReady)
}

// grade is the letters that decide the order and the ceremony: the
// priority's, then one for each field the project gave letters to. A field
// the file left unset draws a hyphen rather than a blank, because a blank
// column reads as a field missing from the row and this one is missing from
// the file.
func (b *BacklogScreen) grade(row BacklogRow) string {
	if row.State == BacklogUnreadable {
		// A file that would not parse has no header to read a grade off,
		// and hyphens where the letters go would be this screen claiming
		// it did.
		return ""
	}
	out := b.Priority.glyph(row.Priority)
	for _, f := range b.Fields {
		if f.lettered() {
			out += f.glyph(row.Values[f.Name])
		}
	}
	return out
}

// glyph is the one letter the field draws a word as, and a hyphen for a
// word it does not hold — which is what an unset field and a misspelt one
// both are.
func (f BacklogField) glyph(word string) string {
	for _, v := range f.Values {
		if v.Word == word && v.Glyph != "" {
			return v.Glyph
		}
	}
	return "-"
}

// lettered reports the field carrying letters at all.
func (f BacklogField) lettered() bool {
	for _, v := range f.Values {
		if v.Glyph != "" {
			return true
		}
	}
	return false
}

// priorityStops is the priority cycle: its words behind the empty stop.
func (b *BacklogScreen) priorityStops() []string {
	stops := make([]string, 0, len(b.Priority.Values)+1)
	stops = append(stops, "")
	for _, v := range b.Priority.Values {
		stops = append(stops, v.Word)
	}
	return stops
}

// priorityStop is the word the priority cycle is standing on, and "" for
// the stop that shows everything.
func (b *BacklogScreen) priorityStop() string {
	stops := b.priorityStops()
	if b.priority >= len(stops) {
		return ""
	}
	return stops[b.priority]
}

// fieldStop is where the field cycle is standing.
func (b *BacklogScreen) fieldStop() stop {
	stops := fieldStops(b.Fields)
	if b.field >= len(stops) {
		return stop{}
	}
	return stops[b.field]
}

// stateWords is the row's state field. A waiting item states what it is
// waiting on rather than only that it is waiting: the slug is the reason,
// and `[w]` goes to it.
func (b *BacklogScreen) stateWords(row BacklogRow) string {
	// A row the host gave its own words to says those. It is how the sprint
	// tab draws where a slug stands in the set — finished, waiting, dropped
	// out of the backlog, or the stage the one in flight is at — which is a
	// different reading from the item's status in the backlog.
	if row.Note != "" {
		return row.Note
	}
	switch row.State {
	case BacklogUnreadable:
		return "will not load"
	case BacklogArchived:
		return "done"
	case BacklogRunning:
		return "in progress"
	case BacklogBlocked:
		return "blocked"
	case BacklogWaiting:
		words := "waits on " + row.Waits[0]
		if rest := len(row.Waits) - 1; rest > 0 {
			words += fmt.Sprintf(" +%d", rest)
		}
		return words
	}
	return "ready"
}
