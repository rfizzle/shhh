package components

// The scroll gutter (
// docs/architecture.md#the-screen-is-a-rectangle-and-so-is-everything-in-it).
// One column down the right edge of the transcript pane saying where in the
// whole transcript the pane is, and how much of the whole it is showing.
//
// The transcript already states how far off the live end a scrolled reader is
// — `↓ 12 lines below · [pgdn] the live end` on the notice rail — and
// that is the measurement. This is the shape. It gives the one thing the
// count cannot, which is proportion: twelve lines from the end of a screenful
// and twelve lines from the end of four hundred read the same as a number and
// do not read the same as a thumb. It is also on the screen while the reader
// is still pinned to the live end, which is when the count says nothing at
// all.
//
// Nothing here is clickable, for the reason reading mode gives about every
// other cell of this pane: a press inside the transcript anchors a selection,
// and a gutter you were meant to grab would make every selection started near
// the right edge a gamble.

// ScrollGutterWidth is the column the transcript pane holds back for the
// gutter. The pane reserves it whether or not there is anything to draw in
// it: a column that appeared on the first overflow would reflow every line of
// the transcript at the moment the reader least expects it, and a reflow
// drops the selection and throws away the render cache. So the
// transcript wraps one column narrower always, and the gutter stays empty
// until there is something below.
const ScrollGutterWidth = 1

// The gutter's two glyphs: the track is the light rule the interface
// draws every other rule with, the thumb its heavy twin. What separates them
// is the stroke and not the hue — they sit one dim/dimmer step apart in
// colour and collapse onto the same grey in mono, where the glyph carries all
// of it (invariant 1).
const (
	scrollTrack = "│"
	scrollThumb = "┃"
)

// Scrollbar renders the gutter as one styled string per row: height rows of
// track with the thumb laid over the run standing for the visible window.
// content is the transcript's total line count, viewport how many of those
// lines fit, and offset the first one showing.
//
// It returns nil when everything fits — the gutter drawing nothing rather
// than drawing a full-height thumb, because a bar that is always there says
// nothing, and an empty column is what keeps the geometry still.
func Scrollbar(height, content, viewport, offset int) []string {
	if height <= 0 || viewport <= 0 || content <= viewport {
		return nil
	}
	// The thumb is the visible share of the whole, and never less than one
	// row: a transcript long enough to round it away is exactly the one whose
	// reader needs to see where they are.
	thumb := min(max(height*viewport/content, 1), height)
	track := height - thumb
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
		pos = track
	case offset > 0 && track > 1:
		pos = min(max(offset*track/maxOffset, 1), track-1)
	}
	rows := make([]string, height)
	for i := range rows {
		if i >= pos && i < pos+thumb {
			rows[i] = sty.ScrollThumb.Render(scrollThumb)
			continue
		}
		rows[i] = sty.ScrollTrack.Render(scrollTrack)
	}
	return rows
}
