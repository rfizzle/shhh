package components

// The staged attachment strip (
// docs/interface/surfaces.md#the-input-frame). What is waiting to ride on the
// next message, one chip per file: the mark for what kind of thing it is,
// what it is called, and how big it is.
//
// It replaces `2 attachments · 4 KB`, which was true and said nothing. The
// count is the one fact about a staging area a reader never has to be told —
// they just attached them — and the names are the ones they do: two
// screenshots and a spec were the same sentence as three screenshots, and a
// file attached by accident looked exactly like one attached on purpose.
//
// Nothing here is a key and nothing here is clickable. The strip sits above a
// live draft, so a key written on a chip would be an offer nothing accepts
//, and a `✕` would be a control the keyboard cannot reach — which is
// the test the click targets came with: the pointer names
// one thing, and that thing already has a key. Taking one back out is `/paste
// drop <name>` — which is why the name is the field a chip gives up last, and
// why what does not fit is counted rather than half-drawn.

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
)

// ChipKind is what a staged attachment is, as the strip marks it. The three
// marks are the glyph set's additions, and they are closed for the same
// reason the rest of that set is.
type ChipKind int

const (
	// ChipText is text bound for the prompt itself: lines, unbounded.
	ChipText ChipKind = iota
	// ChipImage is a raster image: a frame with a subject inside it.
	ChipImage
	// ChipDocument is a document the model reads whole — a PDF. The same
	// lines as text, inside the boundary that makes it a single artifact.
	ChipDocument
)

// mark is the kind's glyph. Colour reinforces nothing here: the chips
// are drawn in body text and the mark carries the whole distinction, which is
// what makes the strip read the same in mono (invariant 1).
func (k ChipKind) mark() string {
	switch k {
	case ChipImage:
		return "▣"
	case ChipDocument:
		return "▤"
	}
	return "≡"
}

// AttachmentChip is one staged attachment as the strip draws it: a base name,
// never a path, and a size already in the rails' own units.
type AttachmentChip struct {
	Kind ChipKind
	Name string
	Size string
	// Lines is how far the text runs, for the chips that have text in them.
	// A size answers "will this fit"; it does not answer "which of these is
	// the stack trace", and for a paste that arrived without a name of its
	// own the height is the only other thing there is to say about it.
	//
	// Zero draws nothing. A picture and a PDF have no lines to count, and a
	// stat that cannot be reported is left out rather than reported as zero
	// (docs/interface/principles.md#a-stat-that-cannot-be-reported-is-left-out).
	Lines int
}

// chipNameWidth caps a chip's name. A staging area holds a handful of files
// and the strip is one line, so a name long enough to push another chip off
// the row costs more than its tail is worth — and the head is the half that
// tells two screenshots apart.
const chipNameWidth = 20

// chipSeparator joins chips the way every other rail joins its fields.
const chipSeparator = " · "

// AttachmentChips renders the staged set as one row, or "" when nothing is
// staged.
//
// Chips are dropped whole from the end rather than clipped, because half a
// name is a file that cannot be named to `/paste drop`, and what was dropped
// is counted where it stood — the number of files you are not looking at is
// the one thing the row cannot otherwise say.
func AttachmentChips(chips []AttachmentChip, width int) string {
	if len(chips) == 0 || width <= 0 {
		return ""
	}
	parts := make([]string, len(chips))
	for i, c := range chips {
		parts[i] = c.render()
	}
	row := joinChips(parts)
	if lipgloss.Width(row) <= width {
		return row
	}
	// Give the row back one chip at a time until it fits alongside the count
	// of the ones it gave up. The last rung keeps one chip whatever happens:
	// a strip that is only a number has lost the thing it is for, so at that
	// point the name clips like any other field.
	for kept := len(chips) - 1; kept >= 1; kept-- {
		row = joinChips(parts[:kept]) + sty.Dim.Render(chipSeparator+chipTail(len(chips)-kept))
		if lipgloss.Width(row) <= width || kept == 1 {
			break
		}
	}
	return Clip(row, width)
}

// joinChips lays a run of already-rendered chips on one row.
func joinChips(parts []string) string {
	return strings.Join(parts, sty.Dim.Render(chipSeparator))
}

// countedLines is a line count as the rails write one. It is here rather than
// beside either caller because the strip and the preview card are the two
// surfaces that report a text attachment's height, and two spellings of the
// same count on two surfaces describing the same file is the kind of drift
// nobody notices until it is on a screenshot.
func countedLines(n int) string {
	if n == 1 {
		return "1 line"
	}
	return strconv.Itoa(n) + " lines"
}

// chipTail counts the chips the row could not take.
func chipTail(hidden int) string {
	return "+" + strconv.Itoa(hidden) + " more"
}

// render lays one chip: the kind's mark and the name in body text, the counts
// dim beside it. The counts read like every other count on the rails; the
// name is the content, and is the only part drawn as such.
func (c AttachmentChip) render() string {
	s := sty.Body.Render(c.Kind.mark() + " " + Clip(c.Name, chipNameWidth))
	if c.Size != "" {
		s += sty.Dim.Render(" " + c.Size)
	}
	if c.Lines > 0 {
		s += sty.Dim.Render(" " + countedLines(c.Lines))
	}
	return s
}
