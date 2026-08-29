package chat

// The transcript's line cache (S-160,
// docs/architecture.md#the-screen-is-a-rectangle-and-so-is-everything-in-it).
//
// §13's block freeze is what makes the transcript cheap to redraw: a step
// block that has a successor can never change, so it is rendered once and
// kept. What it was kept as was one string, and that is what this file
// changes. The string had to be concatenated with the live tail on every
// frame, walked again if a selection was lit over it, and split into every
// line of the session by the pane it was handed to — three passes over the
// whole history to redraw the last forty lines of it.
//
// So the cache holds lines. A frozen block's lines are appended once and
// never touched again; the live tail is rebuilt in place after them; and the
// pane windows the result (viewport.go). The work a frame does is the length
// of what changed, not the length of the session.
//
// # The open line
//
// Blocks are rendered as text, not as lines, because joinUnits owns the
// spacing between them and a separator is a newline the block after it
// begins with. Appending text to lines is therefore not appending lines: the
// first part of what arrives continues the line the last block left open.
// That open line is the element at index frozen, it is rebuilt from empty on
// every frame, and it is the only element after the frozen prefix that a
// second write can reach. Everything below index frozen is settled.
//
// The result is byte-identical to the concatenation it replaces — the same
// lines strings.Split would have produced from the same string — which is
// what renderHistory() still returns for the goldens, and what lines_test.go
// asserts against a render that never used the cache.
//
// # Ownership
//
// The slice is handed to the pane rather than copied into it, and the next
// frame rewrites the tail of that same slice. Two rules keep that safe.
//
// Every caller that builds the lines hands them straight to the pane, so the
// render and the read are one frame apart at most. The one that does not is
// the clipboard extraction (select.go), which reads the current render to see
// what the selection names: it rebuilds the same tail from the same
// transcript at the same width, so what it writes is what was already there —
// and while a selection is lit the pane is holding a copy anyway, because the
// highlight cannot restyle the cache's own lines.
//
// And the live Model owns the backing array. A Model copy that has been
// superseded holds a cache it may no longer render from. That was true of the
// string cache too; the sharing is what makes it worth saying.

import "strings"

// lineCache is the rendered transcript, one display line per element.
type lineCache struct {
	// lines is the whole render: the frozen prefix, then the live tail.
	lines []string
	// frozen is how many leading lines are settled. lines[frozen] is the open
	// line the tail extends; [0, frozen) belongs to blocks that can no longer
	// change and is never rewritten.
	frozen int
	// count is how many transcript entries the frozen prefix covers, always a
	// whole number of step blocks (S-090).
	count int
	// width is the pane width the lines were rendered at. A different width
	// reflows every one of them, so it drops the cache.
	width int
	// sep and hasSep carry joinUnits' rhythm across the seam: the last frozen
	// unit's spacing entry, so the tail joins onto it the way it would have
	// inside one call.
	sep    entry
	hasSep bool
}

// reset drops every rendered line. The width is not part of it — it is the
// key the cache is checked against, not content, and the caller that changes
// it says so itself.
func (c *lineCache) reset() {
	c.lines, c.frozen, c.count = nil, 0, 0
	c.sep, c.hasSep = entry{}, false
}

// rewind truncates back to the frozen prefix and reopens the tail. It is the
// first thing a render does, and the reason the live blocks cost only what
// they are: the frozen lines below stay where they are.
func (c *lineCache) rewind() {
	c.lines = append(c.lines[:c.frozen], "")
}

// write appends rendered text, continuing the open line with whatever comes
// before the first newline. This is string concatenation done in line space:
// write(a); write(b) leaves exactly the lines strings.Split(a+b, "\n") would.
func (c *lineCache) write(s string) {
	if s == "" {
		return
	}
	parts := strings.Split(s, "\n")
	c.lines[len(c.lines)-1] += parts[0]
	c.lines = append(c.lines, parts[1:]...)
}

// freeze settles everything written so far. A block's text always ends in a
// newline, so the open line it leaves behind is empty and belongs to whatever
// is written next rather than to the block being frozen.
func (c *lineCache) freeze() {
	c.frozen = len(c.lines) - 1
}
