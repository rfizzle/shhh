package chat

// The surface's rectangle model (
// docs/architecture.md#the-screen-is-a-rectangle-and-so-is-everything-in-it).
// Every width and every row budget on this surface used to be its own
// subtraction: the content width was `m.width - horizontalPadding*2`, the
// pane was that less the inspector and its divider, the transcript was the
// pane less the scroll gutter, and the viewport was the terminal less five
// separate heights counted in a different file each. Every one of those was a
// second description of the same geometry, and a rung that moved had to move
// in all of them or they disagreed — which is how the live tail came to be
// drawn on a row nothing had paid for.
//
// This is the one description. The terminal is a rectangle, the layout engine
// splits it, and everything downstream reads a rectangle instead of deriving
// one. Two properties fall out that the arithmetic never gave. A block cannot
// overflow the rectangle it is drawn into, because ultraviolet clips at the
// edge rather than trusting the caller to have measured. And the rows have to
// add up, because they are one vertical split of a fixed area rather than
// five subtractions that could each be right on their own.
//
// The split is in two halves on purpose. columns() is horizontal and depends
// on nothing but the terminal's width; surface() is vertical and has to ask
// the bottom panel how many rows it wants, which the panel answers by
// rendering itself at a width. Keeping the halves apart is what stops that
// from being a cycle.

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/ultraviolet/layout"

	"github.com/rfizzle/shhh/internal/ui/components"
)

// bottomChromeHeight is what the bottom panel costs beyond the panel itself:
// the divider and the status bar, or — when the command-center frame is
// showing — the two border rails that stand in for them.
const bottomChromeHeight = dividerHeight + statusBarHeight

// frame is one paint's resolved surface: the columns, the vertical split, and
// the blocks whose own size decides that split — the bottom panel, the live
// tail, the ungated card and the rails above the prompt box. Each is resolved
// the first time the paint asks for it and read from here every time after,
// so a frame renders each of them once.
//
// The bottom panel is why this exists. Its rows are what the vertical split
// takes off the transcript, so the split has to render it to learn them, and
// the draw then rendered it a second time to paint it. Nothing about the
// panel changes between those two renders, and at a dozen frames a second
// through a stream the second one is the surface's largest avoidable cost.
//
// A frame is alive for exactly one paint. Model.framed is nil everywhere
// else, which is what makes every reader below correct outside a paint too:
// with no frame to read, it resolves its own answer as it always did.
//
// Every slot is a pointer because nil has to mean "not resolved yet" and not
// "resolved to nothing": a notice rail with nothing to say and a card that is
// not showing both resolve to no lines, and re-rendering them on every reader
// is exactly what this is here to stop.
type frame struct {
	cols      *paneColumns
	surf      *surfaceLayout
	panel     *panelBody
	tail      *tailBlock
	rails     *[]string
	interrupt *[]string
	mode      *frameLayout
}

// tailBlock is the live tail rendered once, with the width it was rendered
// at: the row count and the draw ask for the same width, and a memo that
// answered a different one would draw a block that had been wrapped for
// somewhere else.
type tailBlock struct {
	width int
	view  string
}

// panelBody is the bottom panel resolved: the rows the surface that owns it
// drew, and how many rows the vertical split gives them. Lines is nil when
// the draft box owns the panel — the box renders itself and its rows are its
// own.
type panelBody struct {
	lines  []string
	height int
}

// view fits the panel's rows to the rows it was given.
func (p panelBody) view() string { return padPanel(p.lines, p.height) }

// padPanel fits a block of rendered lines to an exact row count: a short
// block is padded with blank rows so the panel keeps its shape, and one that
// overran the rows it was given is cut at the last of them rather than
// pushing the frame off the bottom of the terminal.
//
// The padding is clipped off the caller's slice rather than appended to it,
// because the rows it is handed are the frame's own copy of the panel and
// anything else that asks for the panel gets the same slice back. An append
// into their spare capacity would blank rows the next reader is about to
// draw.
func padPanel(lines []string, height int) string {
	if height <= 0 {
		return ""
	}
	if n := len(lines); n < height {
		lines = append(slices.Clip(lines), make([]string, height-n)...)
	}
	return strings.Join(lines[:height], "\n")
}

// paneColumns is the horizontal half of the model: which columns each pane
// owns, at this terminal width, with the two-pane split already decided.
type paneColumns struct {
	// content is the surface inside the horizontal padding — what the
	// header, the reading rail and the prompt frame all span.
	content uv.Rectangle
	// pane is the transcript pane's columns: all of content when the surface
	// is single-pane, the left side of the split when it is not.
	pane uv.Rectangle
	// feed is the pane less the scroll gutter — what the transcript wraps to
	//, and the coordinate space a selection is taken in.
	feed uv.Rectangle
	// gutter is the scroll gutter's one column. The pane holds it back
	// whether or not there is a thumb to draw in it, so nothing reflows the
	// first time the transcript overflows.
	gutter uv.Rectangle
	// divider is the single │ column between the panes, empty when there is
	// only one.
	divider uv.Rectangle
	// inspector is the rail's columns, empty when the surface has not
	// split.
	inspector uv.Rectangle
}

// columns resolves the horizontal split. It reads m.width and the two
// conditions the split has — the width rung and whether something is
// covering the rail — and nothing else, which is what lets every width
// reader on the surface go through it without asking a question that needs a
// width to answer.
func (m Model) columns() paneColumns {
	if m.framed == nil {
		return m.resolveColumns()
	}
	if m.framed.cols == nil {
		cols := m.resolveColumns()
		m.framed.cols = &cols
	}
	return *m.framed.cols
}

// resolveColumns is the split itself, taken once per frame.
func (m Model) resolveColumns() paneColumns {
	// A terminal is never negative, and the arithmetic this replaces could
	// hand one out: `m.width - 4` at three columns is -1, which the first
	// strings.Repeat downstream would have panicked on.
	area := uv.Rect(0, 0, max(m.width, 0), max(m.height, 0))

	var cols paneColumns
	layout.Horizontal(
		layout.Len(horizontalPadding),
		layout.Fill(1),
		layout.Len(horizontalPadding),
	).Split(area).Assign(new(uv.Rectangle), &cols.content, new(uv.Rectangle))

	// Past the top rung of the width ladder the rail takes its columns
	// off the right of the content and one dim column divides the panes.
	cols.pane = cols.content
	if cols.content.Dx() >= components.InspectorMinContentWidth && !m.inspectorHidden() {
		layout.Horizontal(
			layout.Fill(1),
			layout.Len(paneDividerWidth),
			layout.Len(m.railWidth(cols.content.Dx())),
		).Split(cols.content).Assign(&cols.pane, &cols.divider, &cols.inspector)
	}

	layout.Horizontal(
		layout.Fill(1),
		layout.Len(components.ScrollGutterWidth),
	).Split(cols.pane).Assign(&cols.feed, &cols.gutter)

	return cols
}

// railWidth is how many columns the rail gets at a content width, and the one
// place that answers it: the split reads it, and so does every surface that
// reports the arrangement or renders the rail on its own. It takes the
// content width rather than reading it off columns(), because columns() is
// what calls it.
//
// The ladder is the answer unless the session was given a number, and a given
// number is held to the rail's own floor and to what the ladder allows here —
// two limits and not three, because the ladder is already capped at the
// rail's ceiling, so the one min holds both. The ladder's limit is the one
// that matters: without it `/ui rail 72` on a terminal that has just crossed
// the rung leaves the transcript narrower than the rail, which is the
// arrangement the ladder exists to prevent
// (docs/interface/surfaces.md#the-inspector-rail).
func (m Model) railWidth(content int) int {
	ladder := components.InspectorWidthFor(content)
	if m.railCols <= 0 {
		return ladder
	}
	return min(max(m.railCols, components.InspectorWidth), ladder)
}

// surfaceLayout is every rectangle View() paints into, in terminal
// coordinates. The vertical rects span the content columns; a renderer that
// belongs to one pane intersects them with that pane's columns, which is
// what `in` is for.
type surfaceLayout struct {
	paneColumns

	// header is the title row and rail the line under it that says which pane
	// has the keyboard.
	header uv.Rectangle
	rail   uv.Rectangle
	// body is everything between the rail and the bottom panel: the
	// transcript, whatever the turn is doing under it, and the working
	// children's rows. The inspector rail spans all of it.
	body uv.Rectangle
	// view is the transcript's own rows — the viewport's height.
	view uv.Rectangle
	// tail is the live block under the transcript: the thinking
	// spinner, the running command's row, the retry countdown. Empty
	// whenever the turn has nothing to say there.
	tail uv.Rectangle
	// agents is the working children's compact progress rows.
	agents uv.Rectangle
	// bottom is the command-center frame, or the divider + status bar + the
	// takeover surface that replaced it.
	bottom uv.Rectangle
}

// in narrows one of the vertical rects to a pane's columns.
func (s surfaceLayout) in(rows, cols uv.Rectangle) uv.Rectangle {
	return rows.Intersect(cols)
}

// surface resolves the whole arrangement. The vertical split is the one that
// has to be exact: header, rail, everything the pane shows, and the bottom
// panel are four segments of the terminal's rows, so a panel that grew is a
// transcript that shrank by construction rather than by an accounting entry
// somebody has to remember to make.
func (m Model) surface() surfaceLayout {
	if m.framed == nil {
		return m.resolveSurface()
	}
	if m.framed.surf == nil {
		s := m.resolveSurface()
		m.framed.surf = &s
	}
	return *m.framed.surf
}

// resolveSurface is the arrangement itself, taken once per frame.
func (m Model) resolveSurface() surfaceLayout {
	s := surfaceLayout{paneColumns: m.columns()}

	layout.Vertical(
		layout.Len(headerHeight),
		layout.Len(dividerHeight),
		layout.Fill(1),
		layout.Len(m.bottomRows()),
	).Split(s.content).Assign(&s.header, &s.rail, &s.body, &s.bottom)

	// Inside the body, the transcript takes what the two blocks under it do
	// not. They are drawn in this order, so they are split in it.
	layout.Vertical(
		layout.Fill(1),
		layout.Len(m.liveTailHeight()),
		layout.Len(m.agentRowsHeight()),
	).Split(s.body).Assign(&s.view, &s.tail, &s.agents)

	return s
}

// bottomRows is how many rows the bottom of the surface occupies: the panel
// itself, the rails or chrome around it, and the extra rails the frame adds
// . It is the only vertical segment that is measured rather than
// fixed, because it is the only one whose content decides its own size.
func (m Model) bottomRows() int {
	return m.bottomPanelHeight() + bottomChromeHeight + m.frameExtraHeight()
}

// cursorSink collects the one cursor a frame has. A terminal draws exactly
// one, so the block that owns the keyboard reports its own — in its own cells
// — at the moment it is painted, which is the only moment the rectangle it
// was painted into is known. A paint with no sink is a measurement or a
// capture rather than a frame anyone is looking at, and places none.
type cursorSink struct{ at *tea.Cursor }

// place moves a block's own cursor into the rectangle the block was drawn in.
//
// The two edges are not the same kind of edge. A column one past the last
// cell is the ordinary end-of-line position — a draft filling its width to
// the last column reports it, and the next character lands on the row below —
// so it stands on the last cell rather than a column the block does not own.
// A row outside the rectangle means the block was clipped there (drawIn), and
// a cursor on a row somebody else drew would point at their text: no cursor is
// a smaller lie than one in the wrong place, so that one is dropped.
func (c *cursorSink) place(cur *tea.Cursor, into uv.Rectangle) {
	if c == nil || cur == nil || into.Empty() {
		return
	}
	at := *cur
	at.X = min(at.X+into.Min.X, into.Max.X-1)
	at.Y += into.Min.Y
	if at.X < into.Min.X || at.Y < into.Min.Y || at.Y >= into.Max.Y {
		return
	}
	c.at = &at
}

// fitDraft re-fits the draft box to the terminal: its ceiling first, because
// the ceiling is what the row count is clamped to, then the width, because
// the width is what the box re-wraps and re-counts its rows against.
//
// This is the horizontal pass, and the one place the two halves of the split
// are not independent: the box counts its own wrapped rows
// (docs/interface/surfaces.md#the-input-frame), so a narrower terminal can
// wrap a line that was not wrapped before and make the bottom panel a row
// taller than the vertical split last budgeted for. So the vertical pass is
// taken after this one rather than beside it — a second pass, and the last
// one, because it moves no width and nothing it counts can move under it.
func (m *Model) fitDraft() {
	m.input.MaxHeight = m.draftMaxRows()
	m.syncInputWidth()
}

// drawIn paints a rendered block into one rectangle. Everything the surface
// draws goes through here, which is where the screen model's guarantee lives:
// a block that is wider or taller than the rectangle it was given is cut at
// the edge, and one that is smaller leaves the rest of the rectangle blank.
func drawIn(scr uv.Screen, view string, area uv.Rectangle) {
	if view == "" || area.Empty() {
		return
	}
	uv.NewStyledString(view).Draw(scr, area)
}

// renderScreen turns a buffer back into the styled string the rest of the
// tree passes around. The newline normalisation is ultraviolet's: it writes
// what a raw terminal would.
func renderScreen(scr uv.ScreenBuffer) string {
	return strings.ReplaceAll(scr.Render(), "\r\n", "\n")
}

// rowAt is one row of a rectangle, counted from its top.
func rowAt(area uv.Rectangle, i int) uv.Rectangle {
	row := area
	row.Min.Y += i
	row.Max.Y = row.Min.Y + 1
	return row.Intersect(area)
}

// transcriptOrigin is the screen cell the transcript's first rendered cell
// lands in: the top-left corner of the rows the pane was given. The pointer
// reads it to say which line and column it is on (select.go).
func (m Model) transcriptOrigin() uv.Position {
	s := m.surface()
	return s.in(s.view, s.pane).Min
}
