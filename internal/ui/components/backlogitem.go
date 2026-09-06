package components

// The backlog screen's right pane: the item under the pointer — its fields,
// its edges in the graph and its prose — and the reading mode that gives it
// the whole surface. It is its own file because this pane renders a document
// where the list beside it renders rows, so wrapping, folding and the note
// that says how much was folded are its rules and nothing else's.

import (
	"fmt"
	"strings"

	"github.com/rfizzle/shhh/internal/ui/keys"
)

// readingRows is the body with the surface to itself, scrolled through the
// pager and counted at both ends: a fold that does not say how much it
// folded is a fold nobody can act on.
func (b *BacklogScreen) readingRows(width, budget int) []string {
	rows := b.itemRows(width)
	if budget <= 0 {
		return rows
	}
	if len(rows) <= budget {
		// The whole item is on screen, so no row is spent saying what was
		// folded: the marker is a fold's own account of itself, and a blank
		// line where it would be costs a line of the item for nothing.
		b.pager.Height, b.pager.Total = budget, len(rows)
		return rows
	}
	b.pager.Height = max(budget-1, 1)
	shown := b.pager.Window(rows)
	return append(shown, sty.Dim.Render(Clip(b.scrollNote(), width)))
}

// scrollNote is the row under a folded body saying what is off each end. It
// is only asked for once something has been folded, which is why none of its
// three answers is a blank.
func (b *BacklogScreen) scrollNote() string {
	above, below := b.pager.Above(), b.pager.Below()
	switch {
	case above == 0:
		return fmt.Sprintf("%d more rows below", below)
	case below == 0:
		return fmt.Sprintf("%d rows above", above)
	}
	return fmt.Sprintf("%d rows above · %d below", above, below)
}

// itemRows is the right pane: the item under the pointer, drawn as the file
// it is — the header's fields as one compact row, the edges under them, and
// the sections through the renderer the transcript lays prose out with.
//
// It is a preview, not a second list: nothing in it is focusable, and the
// keys reach it only once `[enter]` has said so.
func (b *BacklogScreen) itemRows(width int) []string {
	row := b.current()
	if row == nil {
		return []string{sty.Dim.Render(Clip("no item selected", width))}
	}
	rows := []string{brightStyle().Render(Clip(row.Slug, width))}
	if row.Title != "" {
		rows = append(rows, wrapDim(row.Title, width)...)
	}
	if fields := b.fieldRow(*row); fields != "" {
		rows = append(rows, sty.Dim.Render(Clip(fields, width)))
	}
	if edges := b.edgeRow(*row); edges != "" {
		rows = append(rows, sty.Dim.Render(Clip(edges, width)))
	}
	for _, w := range row.Warnings {
		rows = append(rows, sty.Warn.Render(Clip("⚠ "+w, width)))
	}
	rows = append(rows, screenRule(width), "")
	return append(rows, b.bodyRows(*row, width)...)
}

// fieldRow is the header's own fields on one line: what sort of work it is,
// how soon, how big, where it stands. The file has them one per line and
// that is right for a file; on a pane beside a list they are a row.
func (b *BacklogScreen) fieldRow(row BacklogRow) string {
	fields := append([]string(nil), row.Fields...)
	if row.InSprint {
		fields = append(fields, "in "+b.Sprint)
	}
	return strings.Join(fields, " · ")
}

// edgeRow is the item's place in the graph, both ways round. A listing has
// always said what an item waits on; what waits on *it* is the half that
// decides whether finishing it is worth anything, and it has never been on
// screen anywhere.
func (b *BacklogScreen) edgeRow(row BacklogRow) string {
	var parts []string
	if len(row.Waits) > 0 {
		parts = append(parts, "waits on "+strings.Join(row.Waits, ", ")+
			" · "+keys.Bracket(keys.Backlog.Depends)+" goes there")
	}
	if len(row.Blocks) > 0 {
		parts = append(parts, plural(len(row.Blocks), b.noun())+" waits on this: "+
			strings.Join(row.Blocks, ", "))
	}
	return strings.Join(parts, "   ")
}

// bodyRows is the item's prose. An unreadable file has none, and what goes
// there instead is the reason it would not load — which is the one thing
// that row exists to say.
func (b *BacklogScreen) bodyRows(row BacklogRow, width int) []string {
	if row.State == BacklogUnreadable {
		return append(wrapWarn(row.Reason, width),
			sty.Dim.Render(Clip(row.Path+" is still on disk; "+
				keys.Bracket(keys.Backlog.Edit)+" opens it", width)))
	}
	body := strings.TrimSpace(row.Body)
	if body == "" {
		if row.State == BacklogArchived {
			return []string{sty.Dim.Render(Clip("archived without a report", width))}
		}
		return []string{sty.Dim.Render(Clip("nothing written under the header yet", width))}
	}
	key := fmt.Sprintf("%s\x00%d\x00%d\x00%t", row.Slug, b.tab, width, Mono())
	if key != b.bodyKey {
		// A markdown body is parsed rather than formatted, and the screen
		// redraws on every keystroke, so the render is kept until the row,
		// the width or the palette moves under it.
		b.bodyKey = key
		b.body = b.prose(body, width)
	}
	return b.body
}

// prose is the body laid out, through the host's renderer where there is one
// and as the file's own lines where there is not.
func (b *BacklogScreen) prose(body string, width int) []string {
	if b.Prose != nil {
		return b.Prose(body, width)
	}
	var rows []string
	for _, line := range strings.Split(body, "\n") {
		if strings.TrimSpace(line) == "" {
			rows = append(rows, "")
			continue
		}
		rows = append(rows, wrapDim(line, width)...)
	}
	return rows
}

// wrapDim and wrapWarn are a run of prose laid out in the pane's width, in
// the one treatment each is drawn in.
func wrapDim(text string, width int) []string {
	return wrapSpans([]styledSpan{{text, sty.Dim}}, max(width, 1))
}

func wrapWarn(text string, width int) []string {
	return wrapSpans([]styledSpan{{text, sty.Warn}}, max(width, 1))
}
