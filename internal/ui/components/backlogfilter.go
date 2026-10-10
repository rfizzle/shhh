package components

// The backlog screen's filters: the query row typed into, and the cycles
// that narrow the list by status, by priority, by one of the profile's own
// fields, and to what can be started now. It is its own file because the
// rule a row is matched by and the words the header says about it are one
// decision, and the list beside it only draws what survives.

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// backlogFilter is the query row and the cycles over the list screens'
// shared filter, which holds the rows showing. It owns the query and whether
// its row is open, the three cycles' stops and the ready toggle; the fields
// it cycles over are the host's, handed in by the screen. See
// docs/architecture.md#the-backlog-screens-pieces.
type backlogFilter struct {
	// query and filtering are the text filter; the three indices are the
	// cycles' stops and ready is the toggle.
	query     string
	filtering bool
	status    int
	priority  int
	field     int
	ready     bool
	listFilter
}

// open puts the query row up, taking every letter.
func (b *backlogFilter) open() { b.filtering = true }

// cycleStatus, cyclePriority and cycleField step a cycle to its next stop,
// round to the empty one; toggleReady flips the ready toggle.
func (b *backlogFilter) cycleStatus() { b.status = (b.status + 1) % len(backlogStatuses) }

func (b *backlogFilter) cyclePriority(priority BacklogField) {
	b.priority = (b.priority + 1) % len(priority.stops())
}

func (b *backlogFilter) cycleField(fields []BacklogField) {
	b.field = (b.field + 1) % len(fieldStops(fields))
}

func (b *backlogFilter) toggleReady() { b.ready = !b.ready }

// forArchive drops the status and ready filters, for a step onto the
// archive: every archived item has the same status, so a status filter
// carried in there would empty a list the reader had just asked to see.
func (b *backlogFilter) forArchive() { b.status, b.ready = 0, false }

// edit is the keyboard while the filter row is open, once the screen has
// taken the arrows for the pointer. Every letter is a letter here, and esc
// backs out of the row one level at a time. It reports whether the filter
// changed, which every key here does but the one that closes an empty row.
func (b *backlogFilter) edit(msg tea.KeyPressMsg, pressed string) bool {
	switch {
	case keys.Is(pressed, keys.Backlog.Back):
		// Esc clears what was typed, and on an empty filter it closes the
		// row and hands the letters back — the rule every list in the
		// product answers to
		// (docs/interface/principles.md#esc-is-always-the-safe-answer).
		if b.query == "" {
			b.filtering = false
			return false
		}
		b.query = ""
	case keys.Is(pressed, keys.Query.Rub):

		if r := []rune(b.query); len(r) > 0 {
			b.query = string(r[:len(r)-1])
		}
	default:
		b.query += typedRunes(msg)
	}
	return true
}

// clear puts every filter back to showing everything.
func (b *backlogFilter) clear() {
	b.query, b.filtering = "", false
	b.status, b.priority, b.field, b.ready = 0, 0, 0, false
}

// words is what the header says the filters are, in words rather than
// as a state the reader has to remember pressing into. A list that is
// shorter than the backlog must say why on the screen that shortened it
// (docs/interface/principles.md#fold-never-hide).
func (b *backlogFilter) words(priority BacklogField, fields []BacklogField) string {
	var parts []string
	if q := strings.TrimSpace(b.query); q != "" {
		parts = append(parts, "matching "+q)
	}
	if s := backlogStatuses[b.status]; s != "" {
		parts = append(parts, s)
	}
	if p := b.priorityStop(priority); p != "" {
		parts = append(parts, p+" priority")
	}
	if f := b.fieldStop(fields); f.field != "" {
		parts = append(parts, f.field+" "+f.word)
	}
	if b.ready {
		parts = append(parts, "ready")
	}
	return strings.Join(parts, " · ")
}

// match is the positions of the rows the filters leave showing: the query,
// trimmed, found in the slug or the title by the rule every list screen
// matches with, and then the cycles.
func (b *backlogFilter) match(rows []BacklogRow, priority BacklogField, fields []BacklogField) []int {
	shown := Filter(rows, strings.TrimSpace(b.query), backlogQueryFields)
	kept := shown[:0]
	for _, i := range shown {
		if b.cycled(rows[i], priority, fields) {
			kept = append(kept, i)
		}
	}
	return kept
}

// backlogQueryFields is what the query is found in: the slug and the title.
func backlogQueryFields(row BacklogRow) []string { return []string{row.Slug, row.Title} }

// cycled is the cycles' rule over one row. A file that will not parse
// answers none of the field filters — it has no fields — and it survives
// them rather than being hidden by one: the row is the only thing on screen
// saying the file is there, and a filter that swallowed it would be hiding
// exactly the item the reader has to go and fix.
func (b *backlogFilter) cycled(row BacklogRow, priority BacklogField, fields []BacklogField) bool {
	if row.State == BacklogUnreadable {
		return true
	}
	if s := backlogStatuses[b.status]; s != "" && s != row.Status {
		return false
	}
	if p := b.priorityStop(priority); p != "" && p != row.Priority {
		return false
	}
	if f := b.fieldStop(fields); f.field != "" && f.word != row.Values[f.field] {
		return false
	}
	return !b.ready || row.State == BacklogReady
}

// rows is the filter row: what has been typed, and where the next
// character goes.
func (b *backlogFilter) rows(width int) []string {
	if !b.filtering {
		return nil
	}
	typed := sty.info.Render(queryPrompt) + sty.queryText.Render(b.query+queryCursor)
	if b.query == "" {
		typed += sty.dim.Render(" type to filter by slug or title")
	}
	return []string{Clip(typed, width)}
}

// stops is a field's cycle — the priority's — its words behind the empty
// stop.
func (f BacklogField) stops() []string {
	stops := make([]string, 0, len(f.Values)+1)
	stops = append(stops, "")
	for _, v := range f.Values {
		stops = append(stops, v.Word)
	}
	return stops
}

// priorityStop is the word the priority cycle is standing on, and "" for
// the stop that shows everything.
func (b *backlogFilter) priorityStop(priority BacklogField) string {
	stops := priority.stops()
	if b.priority >= len(stops) {
		return ""
	}
	return stops[b.priority]
}

// fieldStop is where the field cycle is standing.
func (b *backlogFilter) fieldStop(fields []BacklogField) stop {
	stops := fieldStops(fields)
	if b.field >= len(stops) {
		return stop{}
	}
	return stops[b.field]
}
