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

// backlogReader is the item pane and the reading mode. It owns whether the
// body has the keys, the body's scroll, and the last render of an item's
// prose; the screen hands it the item for each draw, and opens and closes it
// as the row under it moves. See
// docs/architecture.md#the-backlog-screens-pieces.
type backlogReader struct {
	// reading is the body holding the keys, scrolled through pager.
	reading bool
	pager   Pager
	// body is the last markdown render and the row, width and palette it was
	// made for. The screen redraws on every keystroke and a document is
	// parsed rather than formatted, so the render outlives the frame.
	body    []string
	bodyKey string
}

// backlogItem is what the reader draws, read off the screen for one draw:
// the row under the pointer, the words the screen says around it, the tab it
// is on, and the host's prose renderer.
type backlogItem struct {
	row    *BacklogRow
	sprint string
	noun   string
	tab    int
	prose  func(src string, width int) []string
}

// open gives the body the keys, from its top; close hands them back to the
// list, for a key that moved the row out from under it.
func (b *backlogReader) open() { b.reading, b.pager.Offset = true, 0 }

func (b *backlogReader) close() { b.reading, b.pager.Offset = false, 0 }

// update is the keyboard while the body has it: the pager, and the way back
// to the list. Back goes to the list rather than out of the screen, because
// the reader is one level in and esc is a step back rather than an exit. The
// register's key is the screen's, so it is handed back rather than answered.
func (b *backlogReader) update(pressed string) (register bool) {
	switch {
	case keys.Is(pressed, keys.Backlog.Move):
		b.pager.Offset += keys.Step(pressed, keys.Backlog.Move)
	case keys.Is(pressed, keys.Backlog.Page):
		b.pager.Offset += keys.Step(pressed, keys.Backlog.Page) * max(b.pager.Height, 1)
	case keys.Is(pressed, keys.Backlog.Read), keys.Is(pressed, keys.Backlog.Back):
		b.reading = false
	case keys.Is(pressed, keys.Backlog.List):
		return true
	}
	return false
}

// readingRows is the body with the surface to itself, scrolled through the
// pager and counted at both ends: a fold that does not say how much it
// folded is a fold nobody can act on.
func (b *backlogReader) readingRows(it backlogItem, width, budget int) []string {
	rows := b.itemRows(it, width)
	if budget <= 0 {
		return rows
	}
	if len(rows) <= budget {
		// The whole item is on screen, so no row is spent saying what was
		// folded: the marker is a fold's own account of itself, and a blank
		// line where it would be costs a line of the item for nothing.
		b.pager.Height, b.pager.total = budget, len(rows)
		return rows
	}
	b.pager.Height = max(budget-1, 1)
	shown := b.pager.Window(rows)
	return append(shown, sty.dim.Render(Clip(b.scrollNote(), width)))
}

// scrollNote is the row under a folded body saying what is off each end. It
// is only asked for once something has been folded, which is why none of its
// three answers is a blank.
func (b *backlogReader) scrollNote() string {
	above, below := b.pager.above(), b.pager.below()
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
func (b *backlogReader) itemRows(it backlogItem, width int) []string {
	row := it.row
	if row == nil {
		return []string{sty.dim.Render(Clip("no item selected", width))}
	}
	rows := []string{brightStyle().Render(Clip(row.Slug, width))}
	if row.Title != "" {
		rows = append(rows, wrapDim(row.Title, width)...)
	}
	if fields := b.fieldRow(it, *row); fields != "" {
		rows = append(rows, sty.dim.Render(Clip(fields, width)))
	}
	if edges := b.edgeRow(it, *row); edges != "" {
		rows = append(rows, sty.dim.Render(Clip(edges, width)))
	}
	for _, w := range row.Warnings {
		rows = append(rows, sty.warn.Render(Clip("⚠ "+w, width)))
	}
	rows = append(rows, screenRule(width), "")
	return append(rows, b.bodyRows(it, *row, width)...)
}

// fieldRow is the header's own fields on one line: what sort of work it is,
// how soon, how big, where it stands. The file has them one per line and
// that is right for a file; on a pane beside a list they are a row.
func (b *backlogReader) fieldRow(it backlogItem, row BacklogRow) string {
	fields := append([]string(nil), row.Fields...)
	if row.InSprint {
		fields = append(fields, "in "+it.sprint)
	}
	return strings.Join(fields, " · ")
}

// edgeRow is the item's place in the graph, both ways round. A listing has
// always said what an item waits on; what waits on *it* is the half that
// decides whether finishing it is worth anything, and it has never been on
// screen anywhere.
func (b *backlogReader) edgeRow(it backlogItem, row BacklogRow) string {
	var parts []string
	if len(row.Waits) > 0 {
		parts = append(parts, "waits on "+strings.Join(row.Waits, ", "))
	}
	if len(row.Blocks) > 0 {
		parts = append(parts, plural(len(row.Blocks), it.noun)+" waits on this: "+
			strings.Join(row.Blocks, ", "))
	}
	return strings.Join(parts, "   ")
}

// bodyRows is the item's prose. An unreadable file has none, and what goes
// there instead is the reason it would not load — which is the one thing
// that row exists to say.
func (b *backlogReader) bodyRows(it backlogItem, row BacklogRow, width int) []string {
	if row.State == BacklogUnreadable {
		return append(wrapWarn(row.Reason, width),
			sty.dim.Render(Clip(row.Path+" is still on disk; "+
				keys.Bracket(keys.Backlog.Edit)+" opens it", width)))
	}
	body := strings.TrimSpace(row.Body)
	if body == "" {
		if row.State == BacklogArchived {
			return []string{sty.dim.Render(Clip("archived without a report", width))}
		}
		return []string{sty.dim.Render(Clip("nothing written under the header yet", width))}
	}
	key := fmt.Sprintf("%s\x00%d\x00%d\x00%t", row.Slug, it.tab, width, Mono())
	if key != b.bodyKey {
		// A markdown body is parsed rather than formatted, and the screen
		// redraws on every keystroke, so the render is kept until the row,
		// the width or the palette moves under it.
		b.bodyKey = key
		b.body = b.prose(it, body, width)
	}
	return b.body
}

// prose is the body laid out, through the host's renderer where there is one
// and as the file's own lines where there is not.
func (b *backlogReader) prose(it backlogItem, body string, width int) []string {
	if it.prose != nil {
		return it.prose(body, width)
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
	return wrapSpans([]styledSpan{{text, sty.dim}}, max(width, 1))
}

func wrapWarn(text string, width int) []string {
	return wrapSpans([]styledSpan{{text, sty.warn}}, max(width, 1))
}
