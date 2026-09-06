package components

// The scroll gutter (
// docs/architecture.md#the-screen-is-a-rectangle-and-so-is-everything-in-it).
// One column down the right edge of the transcript pane saying where in the
// whole transcript the pane is, and how much of the whole it is showing. It
// is one short heavy mark in an otherwise empty column: place is position,
// length is share, and the mark touching the bottom is the fact the column is
// read for.
//
// The drawing is the `Scroll` artboard of the cockpit kit in the `shhh Design
// System` (`ui_kits/cockpit/Scroll.html`), which is what settles the glyph and
// the rung rather than this file (docs/interface/README.md). There is no
// track: any full-height run one column from the pane divider grows the double
// border back, and the pane's own top and bottom already say what the whole
// is.
//
// The transcript already states how far off the live end a scrolled reader is
// — `↓ 12 lines below · [pgdn] the live end` on the notice rail — and
// that is the measurement. This is the shape. It gives the one thing the
// count cannot, which is proportion: twelve lines from the end of a screenful
// and twelve lines from the end of four hundred read the same as a number and
// do not read the same as a mark. It is also on the screen while the reader
// is still pinned to the live end, which is when the count says nothing at
// all.
//
// Nothing here is clickable, for the reason reading mode gives about every
// other cell of this pane: a press inside the transcript anchors a selection,
// and a gutter you were meant to grab would make every selection started near
// the right edge a gamble. Reading mode's lit row ends before this column for
// the same reason it is not a key — the gutter reports where the pane is, and
// a row's cursor has nothing to say about that.

// ScrollGutterWidth is the column the transcript pane holds back for the
// gutter. The pane reserves it whether or not there is anything to draw in
// it: a column that appeared on the first overflow would reflow every line of
// the transcript at the moment the reader least expects it, and a reflow
// drops the selection and throws away the render cache. So the
// transcript wraps one column narrower always, and the gutter stays empty
// until there is something below.
const ScrollGutterWidth = 1

// The gutter's one glyph, drawn on one token: the heavy vertical the drawing
// kit assigns the scroll thumb, in the chrome rung. Every other row of the
// column is a blank cell.
//
// It is not the `│` the frame and the pane divider draw, and what separates
// them is stroke rather than shade — the mark is heavier, and it is short
// where a border runs the pane's whole height. That is the difference a
// monochrome terminal and a sixteen-colour one both keep, where every chrome
// rung collapses onto one grey
// (docs/interface/principles.md#colour-never-carries-meaning-alone). Weight
// alone would not carry it against a full-height run beside the divider,
// which is the other reason the column has none.
const (
	scrollThumb = "┃"
	scrollBlank = " "
)

// Scrollbar renders the gutter as one styled string per row: the run standing
// for the visible window drawn as the thumb, every other row an empty cell.
// content is the transcript's total line count, viewport how many of those
// lines fit, and offset the first one showing.
//
// It returns nil when everything fits — the gutter drawing nothing rather
// than drawing a full-height thumb, because a mark that is always there says
// nothing, and the reserved column is what keeps the geometry still.
func Scrollbar(height, content, viewport, offset int) []string {
	if height <= 0 || viewport <= 0 || content <= viewport {
		return nil
	}
	// The thumb is the visible share of the whole, and never less than one
	// row: a transcript long enough to round it away is exactly the one whose
	// reader needs to see where they are.
	thumb := min(max(height*viewport/content, 1), height)
	travel := height - thumb
	maxOffset := content - viewport
	offset = min(max(offset, 0), maxOffset)
	// The thumb touches an end only when the transcript is at that end. Floor
	// division alone would park it against the top for the first several lines
	// of scroll, and rounding would park it against the bottom before the live
	// end — and whether there is anything below is the one thing the gutter is
	// read for. A single row of travel spends that row on the live end for the
	// same reason.
	pos := 0
	switch {
	case offset >= maxOffset:
		pos = travel
	case offset > 0 && travel > 1:
		pos = min(max(offset*travel/maxOffset, 1), travel-1)
	}
	rows := make([]string, height)
	for i := range rows {
		if i >= pos && i < pos+thumb {
			rows[i] = sty.ScrollThumb.Render(scrollThumb)
			continue
		}
		rows[i] = scrollBlank
	}
	return rows
}
