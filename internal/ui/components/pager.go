package components

// The offset a body longer than its pane is read through
// (docs/interface/surfaces.md#the-activity-row).
//
// Four surfaces here scroll a block of rendered rows: the full-screen diff,
// the full-screen output view, review mode's hunk pane and the approval
// card's body. Every one of them had written the same three lines — hold the
// offset inside the body, take the run it names, pad the pane out to its own
// height — and the copies disagreed about the corners. Two clamped before
// slicing and one clamped after; the one that kept a focused row visible did
// it twice with different heights; and the pane that pads to a fixed height
// did it in two places with the same off-by-one comment above each.
//
// So the arithmetic is here. What a surface still owns is what its rows say,
// which is the part that is a fact about that surface: a diff hunk, a line of
// program output, a counted tail with a key on it.

import "strings"

// Pager is a window onto rendered rows: where the pane starts, how many rows
// it has, and how many rows there are.
//
// It is a value the caller builds where the scrolling happens rather than a
// field it keeps. Every surface here already carries its own offset as an
// exported field — a host resets the diff's to zero when it opens a different
// file — so what moves in here is the arithmetic, and the offset stays where
// the hosts can see it. Window writes the held offset back, so the caller
// reads it off the pager afterwards.
type Pager struct {
	// Offset is the first row the pane shows. It is held inside the body by
	// every method here, so a caller may write an overshoot into it and let
	// the next read pull it back.
	Offset int
	// Height is how many rows the pane has for the body.
	Height int
	// Total is how many rows the body has. Window sets it from the rows it
	// is given; a caller that only asks about the offset sets it itself.
	Total int
}

// Held is the offset with the body's own ends applied: never before the first
// row, and never so far down that the pane hangs past the last one. A body
// shorter than the pane holds at the top, which is what makes the pad below
// the only thing that fills the difference.
func (p Pager) Held() int { return max(0, min(p.Offset, p.Total-p.Height)) }

// Window is the run of rows the pane shows, with the offset held first. The
// offset is written back, so a press past the end settles at the end rather
// than scrolling into nothing — one press to overshoot, and none to recover.
func (p *Pager) Window(rows []string) []string {
	p.Total = len(rows)
	p.Offset = p.Held()
	return rows[p.Offset:min(p.Offset+p.Height, len(rows))]
}

// Reveal moves the offset the least it can to bring a row inside the pane: a
// row above the pane pulls it up to meet it, a row below pushes it down to
// end on it, and a row already showing moves nothing. It is what a pane
// scrolled by something other than the wheel does — review mode moves between
// hunks and the pane follows.
func (p *Pager) Reveal(row int) {
	switch {
	case row < p.Offset:
		p.Offset = row
	case row >= p.Offset+p.Height:
		p.Offset = row - p.Height + 1
	}
	p.Offset = p.Held()
}

// Above is how many rows the pane has scrolled past, and Below how many are
// still under it. They are what a counted marker states: a fold is only a
// fold while it says how much it folded
// (docs/interface/principles.md#fold-never-hide).
func (p Pager) Above() int { return p.Held() }

// Below is how many rows sit under the pane's last one.
func (p Pager) Below() int { return max(0, p.Total-p.Held()-p.Height) }

// Screen is the full-screen reading shape: a header row, the body scrolled to
// the offset and padded out to the pane's rows, and a footer under it. The
// pad is what keeps the footer on the bottom row of the terminal for a body
// too short to reach it — a viewer whose keys walk up the screen as its
// content shortens is one a reader has to look for.
//
// The rows are taken already rendered, because what the pane paints on them
// is the surface's own business: the output view re-paints foreign bytes into
// the palette as it draws, and the diff has coloured its lines long before
// this sees them.
func (p Pager) Screen(header string, rows []string, footer string) string {
	out := append([]string{header}, rows...)
	for len(out) < p.Height+1 {
		out = append(out, "")
	}
	return strings.Join(append(out, footer), "\n")
}
