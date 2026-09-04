package chat

// The transcript window (
// docs/architecture.md#the-screen-is-a-rectangle-and-so-is-everything-in-it).
//
// This is the pane the transcript is read through: a scroll offset, a size,
// and the lines the offset is into. It replaces the bubbles viewport shhh
// used to hold, and the reason is the transcript window's — that viewport
// takes its content as a string, splits it into every line of the session,
// and measures the width of all of them, so a frame cost as much as the
// history was long. The lines arrive here already split and already measured
// by lines.go, and the only ones this file touches are the ones on the
// screen.
//
// The scroll position is an absolute line index rather than the item-and-line
// pair Crush's list keeps, and that is deliberate: the selection
// is a pair of coordinates in rendered transcript space, the notice rail
// counts the lines below the pane, and the scroll gutter is a
// proportion of the whole. All three ask the same question — which line of
// the transcript is this — and an item-relative offset would make each of
// them convert.
//
// What is *not* kept from the bubbles viewport is its keymap. It bound j, k,
// u, d, f, b and the spacebar, which is the bug reading mode opens with: the
// pager fired from inside a sentence. Nothing here reads a key at all. The
// transcript is moved by scrollLines and scrollPage (navigate.go), which the
// wheel, pgup/pgdn and shift+arrows reach — and by nothing else.
//
// # Why the search is here and not the viewport's
//
// That viewport grew a highlight search — SetContentLines plus SetHighlights
// and a next/previous pair — and it was measured against this pane before the
// search below was written, because inheriting one would have been cheaper
// than keeping one. It does not fit, for two reasons that are about ownership
// rather than speed.
//
// It keeps the caller's slice and writes into it. SetContentLines assigns the
// slice it is handed and then rewrites elements of it in place, so the line
// cache and the pane end up sharing an array that both of them edit: a line
// rebuilt by the next frame's tail silently changes what the pane is already
// showing, and a line carrying a CRLF is rewritten under the cache that built
// it. Both were reproduced — a caller's element written after the hand-off
// showed up in the viewport's own content, and an element the caller still
// held came back normalised.
//
// And every content change drops the highlights. SetContentLines calls
// ClearHighlights, so a search would survive exactly until the next frame
// that touched the transcript — which, while a turn is streaming, is every
// frame. That is the half the assessment recorded as unchecked, and it is the
// half that decides it: a search that cannot outlive the live tail is not a
// search of the transcript.
//
// The costs came out the same way. On a twenty-thousand-line transcript
// SetContentLines takes 8.3ms against this pane's 0.39ms, because it scans
// every line for an embedded newline and measures every line's width on every
// call; and SetHighlights takes 60ms, because it joins the whole session into
// one string and walks it grapheme by grapheme to convert byte offsets into
// line and column. The search below scans the lines it already has, once per
// query rather than once per frame, and converts nothing.
//
// So the absolute line offset survives the move — with soft wrap off the
// viewport's own offset is a real line index, which is what the selection and
// the gutter need — and the line cache's ownership rule does not. The pane
// stays, and the search is thirty lines on top of it.

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// viewport is a window onto rendered transcript lines.
//
// It holds the lines rather than copying them: lines.go hands over the slice
// it just finished building, and rebuilds the live tail in place on the next
// frame (lines.go explains why that is safe). Every call site sets the lines
// and reads the view within one frame, so nothing here outlives the render
// that produced it.
type viewport struct {
	width  int
	height int
	// yOffset is the index of the first visible line. It is clamped to
	// [0, maxYOffset] by every path that writes it, so it always names a line
	// the pane could be showing.
	yOffset int
	lines   []string
	// query is what the reader is looking for, folded to lower case, and
	// matches is where it was found. The query is kept as well as the
	// matches because the lines move under them: the live tail is rebuilt on
	// every frame of a streaming turn, so the positions are re-found from the
	// query rather than carried forward.
	query   string
	matches []match
	// at is which match the pointer is on, and -1 when there is none. It is
	// an index into matches rather than a line, so next and previous walk the
	// occurrences and not the rows they happen to share.
	at int
	// typed is the query as the reader typed it — what the query row draws —
	// and typing says that row is open and taking every key.
	//
	// Both are here rather than on the surface that draws the row because a
	// search is one thing: the text, the occurrences and the pointer have to
	// agree on every frame of a streaming turn, and a session holding the
	// text while the pane held the marks would be two records of one search
	// with nothing keeping them in step.
	typed  string
	typing bool
}

// match is one occurrence: the line it is on and the display cells it covers.
// Cells rather than byte offsets, because the line it is in is full of escape
// sequences and a byte index into one of those is not a column.
type match struct {
	line     int
	from, to int
}

func newViewport(width, height int) viewport {
	return viewport{width: width, height: height, at: -1}
}

func (v viewport) Width() int  { return v.width }
func (v viewport) Height() int { return v.height }

func (v *viewport) SetWidth(w int) {
	v.width = w
	v.SetYOffset(v.yOffset)
}

func (v *viewport) SetHeight(h int) {
	v.height = h
	v.SetYOffset(v.yOffset)
}

// SetLines replaces the content. An all-blank single line is no content at
// all — the empty transcript renders as one empty string, and a pane that
// called that one line would put a thumb in the gutter for a session with
// nothing in it.
func (v *viewport) SetLines(lines []string) {
	if len(lines) == 1 && ansi.StringWidth(lines[0]) == 0 {
		lines = nil
	}
	v.lines = lines
	// An open search is re-found against the lines that are there now. This
	// is the rule the bubbles viewport does not hold: it drops its highlights
	// on every content change, which during a turn is every frame.
	v.find()
	// Content that shrank out from under the offset — a /clear, a rewind —
	// leaves the pane past its own end, so it follows the content down.
	if v.yOffset > v.maxYOffset() {
		v.GotoBottom()
	}
}

// Search looks for what the reader typed and reports how many times it is in
// the transcript. It is case-insensitive and a plain substring: the
// transcript is prose and paths, and a reader typing a path is naming it.
//
// An empty query clears the search, which is what leaving it does.
func (v *viewport) Search(query string) int {
	folded := strings.ToLower(query)
	if folded != v.query {
		// A different query is a different set of occurrences, so the pointer
		// starts again rather than keeping an index it was numbered against.
		// Re-finding the same query is the other case, and that one keeps it:
		// the reader has not moved, the lines under them have.
		v.at = -1
	}
	v.typed, v.query = query, folded
	v.find()
	return len(v.matches)
}

// Searching reports whether a search is open, which is what tells a caller
// that next and previous have somewhere to go.
func (v viewport) Searching() bool { return v.query != "" }

// OpenSearch opens the query row, keeping whatever is already in it: the key
// that opens a live search is how the reader edits that query rather than
// starting a second one.
func (v *viewport) OpenSearch() { v.typing = true }

// SearchOpen reports that the query row is taking keys, which is what makes
// every letter text rather than one of the surface's own.
func (v viewport) SearchOpen() bool { return v.typing }

// SearchQuery is the query as it was typed. The folded copy is what the lines
// are matched against, and is not what a reader is shown back.
func (v viewport) SearchQuery() string { return v.typed }

// KeepSearch closes the row and leaves the search standing.
func (v *viewport) KeepSearch() { v.typing = false }

// ClearSearch drops the query, the marks and the row together — one act,
// because a mark with no query behind it is a highlight nothing can clear.
func (v *viewport) ClearSearch() {
	v.typing = false
	v.Search("")
}

// RevealMatch brings the occurrence the pointer is on back into the pane. It
// is what a pane that changed height under a live search needs: the panel
// grew, the match the reader was reading did not move, and the window did.
func (v *viewport) RevealMatch() {
	if v.at >= 0 && v.at < len(v.matches) {
		v.reveal(v.matches[v.at].line)
	}
}

// MatchPosition is which occurrence the pointer is on, 1-based, and how many
// there are. Zero of zero is a query that found nothing.
func (v viewport) MatchPosition() (at, total int) {
	if v.at < 0 || v.at >= len(v.matches) {
		return 0, len(v.matches)
	}
	return v.at + 1, len(v.matches)
}

// find re-locates the query in the lines as they now stand, and keeps the
// pointer on the occurrence nearest where it already was. The occurrences are
// re-found rather than moved because a rebuilt tail can add, drop or reflow
// the lines they were on, and an index carried across that names a different
// row.
func (v *viewport) find() {
	v.matches = nil
	if v.query == "" {
		v.at = -1
		return
	}
	for i, line := range v.lines {
		plain := strings.ToLower(ansi.Strip(line))
		for at := 0; ; {
			j := strings.Index(plain[at:], v.query)
			if j < 0 {
				break
			}
			start := at + j
			from := ansi.StringWidth(plain[:start])
			v.matches = append(v.matches, match{
				line: i,
				from: from,
				to:   from + ansi.StringWidth(plain[start:start+len(v.query)]),
			})
			at = start + len(v.query)
		}
	}
	switch {
	case len(v.matches) == 0:
		v.at = -1
	case v.at < 0:
		// A search that has just been typed starts at the first occurrence
		// at or below the top of the pane, so the answer to "is it below me"
		// is the first thing the reader is shown.
		v.at = v.nearest()
	default:
		v.at = min(v.at, len(v.matches)-1)
	}
}

// nearest is the first occurrence the pane has not already scrolled past, or
// the last one when it has scrolled past all of them.
func (v viewport) nearest() int {
	for i, mt := range v.matches {
		if mt.line >= v.yOffset {
			return i
		}
	}
	return len(v.matches) - 1
}

// NextMatch and PrevMatch walk the occurrences and bring the one they land on
// into the pane. They wrap, because a search that stopped at the last
// occurrence would make the reader scroll back to start again.
func (v *viewport) NextMatch() { v.step(1) }

// PrevMatch walks back through the occurrences.
func (v *viewport) PrevMatch() { v.step(-1) }

func (v *viewport) step(delta int) {
	if len(v.matches) == 0 {
		return
	}
	v.at = (v.at + delta + len(v.matches)) % len(v.matches)
	v.reveal(v.matches[v.at].line)
}

// reveal scrolls the least it can to bring a line into the pane: a line above
// it pulls the pane up to meet it, one below pushes the pane down to end on
// it, and a line already showing moves nothing.
func (v *viewport) reveal(line int) {
	switch {
	case line < v.yOffset:
		v.SetYOffset(line)
	case line >= v.yOffset+v.height:
		v.SetYOffset(line - v.height + 1)
	}
}

// SetContent is SetLines for the surfaces that still render to one string:
// reading mode's gutter render and an attached child's session, both of which
// are built fresh each time and have no incremental cache to feed.
func (v *viewport) SetContent(s string) {
	v.SetLines(strings.Split(s, "\n"))
}

// TotalLineCount is how many lines the transcript has, which is what the
// scroll gutter's proportion and the notice rail's count are
// both fractions of.
func (v viewport) TotalLineCount() int { return len(v.lines) }

// VisibleLineCount is how many of them are on the screen — fewer than the
// pane's height only when the transcript is shorter than the pane.
func (v viewport) VisibleLineCount() int {
	if v.width <= 0 || v.height <= 0 {
		return 0
	}
	top := min(v.yOffset, len(v.lines))
	return min(v.height, len(v.lines)-top)
}

func (v viewport) YOffset() int { return v.yOffset }

func (v *viewport) SetYOffset(n int) {
	v.yOffset = min(max(n, 0), v.maxYOffset())
}

// maxYOffset is the last offset that still fills the pane, or 0 when the
// transcript is shorter than it.
func (v viewport) maxYOffset() int { return max(0, len(v.lines)-v.height) }

func (v viewport) AtTop() bool    { return v.yOffset <= 0 }
func (v viewport) AtBottom() bool { return v.yOffset >= v.maxYOffset() }

func (v *viewport) GotoTop()    { v.SetYOffset(0) }
func (v *viewport) GotoBottom() { v.SetYOffset(v.maxYOffset()) }

func (v *viewport) ScrollUp(n int) {
	if n == 0 || len(v.lines) == 0 {
		return
	}
	v.SetYOffset(v.yOffset - n)
}

func (v *viewport) ScrollDown(n int) {
	if n == 0 || len(v.lines) == 0 {
		return
	}
	v.SetYOffset(v.yOffset + n)
}

// PageUp and PageDown move by a whole pane. They are a page rather than a
// page-less-one because that is what the key has always done here; the
// overlap belongs to the reader's eye, not to the pager.
func (v *viewport) PageUp()   { v.ScrollUp(v.height) }
func (v *viewport) PageDown() { v.ScrollDown(v.height) }

// visibleLines is the window: the lines the offset names, and no others. This
// is the whole point of the file — the work a frame does is bounded by the
// height of the pane rather than by the length of the session.
//
// A line wider than the pane is cut rather than wrapped. Nothing in the
// transcript should be: every renderer wraps to the pane width it was given
// (the scroll gutter reserves its column so the width never changes
// underneath one). The cut is what keeps a renderer that got it wrong from
// breaking the frame's shape.
func (v viewport) visibleLines() []string {
	if v.width <= 0 || v.height <= 0 || len(v.lines) == 0 {
		return nil
	}
	top := min(v.yOffset, len(v.lines))
	bottom := min(top+v.height, len(v.lines))
	out := v.lines[top:bottom]
	copied := false
	// Copy before the first write, so the cache's own lines are never
	// rewritten by a render of them.
	own := func() {
		if !copied {
			out, copied = append([]string(nil), out...), true
		}
	}
	for i, line := range out {
		if ansi.StringWidth(line) <= v.width {
			continue
		}
		own()
		out[i] = ansi.Cut(line, 0, v.width)
	}
	// A match in the part of an over-wide line the cut above took is counted
	// and not marked: the range lands past the end of what is left and
	// styling it is a no-op. Nothing in the transcript should be wider than
	// the pane, so this is the same corner the cut itself is here for.
	for i, mt := range v.matches {
		if mt.line < top || mt.line >= bottom {
			continue
		}
		style := matchStyle
		if i == v.at {
			style = matchAtStyle
		}
		own()
		row := mt.line - top
		out[row] = lipgloss.StyleRanges(out[row], lipgloss.NewRange(mt.from, mt.to, style))
	}
	return out
}

// matchStyle marks a line the query was found on and matchAtStyle the
// occurrence the pointer is on. Both are structural rather than coloured, for
// the reason every mark in the transcript is: the two have to be told apart
// in mono as loudly as in colour (invariant 1). Underline says "here it is"
// without covering the syntax colours underneath; reverse says "this one",
// which is the same thing a selected span says, and the two are never up at
// once — a drag clears the search's pointer the way it clears everything else
// the pane was showing.
var (
	matchStyle   = lipgloss.NewStyle().Underline(true)
	matchAtStyle = lipgloss.NewStyle().Reverse(true)
)

// View is the pane, padded to its own width and height so every row the
// scroll gutter glues itself to is the same length.
func (v viewport) View() string {
	if v.width == 0 || v.height == 0 {
		return ""
	}
	return sty.Viewport.
		Width(v.width).
		Height(v.height).
		Render(strings.Join(v.visibleLines(), "\n"))
}
