package markdown

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Two ways of ending a line, and the difference between them is the whole
// point.
//
// wrap is for prose: it breaks between words, so a paragraph reflows to the
// pane and reads the same at every width.
//
// fold is for code: it breaks at the column, so nothing moves. A long line
// comes back in pieces that concatenate to exactly what was written. Wrapping
// code between words is what produced the reflowed, re-indented `func main()`
// this package was written to stop, and clipping it would have been tidier
// and would have lost the end of the line.

// row accumulates one output line as runs, so that a treatment spanning three
// words is drawn once rather than three times.
//
// Drawing per word was the first version and it was wrong twice over: every
// styled phrase cost an escape pair per word, and a link came back as one OSC
// 8 hyperlink per word — which some terminals render as several separate
// links.
type row struct {
	runs []struct {
		seg  Segment
		text strings.Builder
	}
	used int
}

// add appends text drawn in seg, extending the current run where seg is the
// one already open.
func (r *row) add(seg Segment, text string, width int) {
	if n := len(r.runs); n > 0 && sameRun(r.runs[n-1].seg, seg) {
		r.runs[n-1].text.WriteString(text)
		r.used += width
		return
	}
	r.runs = append(r.runs, struct {
		seg  Segment
		text strings.Builder
	}{seg: seg})
	r.runs[len(r.runs)-1].text.WriteString(text)
	r.used += width
}

// String draws the row, one escape run per treatment.
func (r *row) String() string {
	var b strings.Builder
	for i := range r.runs {
		b.WriteString(styled(r.runs[i].seg, r.runs[i].text.String()))
	}
	return b.String()
}

func (r *row) reset() {
	r.runs, r.used = r.runs[:0], 0
}

// sameRun reports whether two segments draw identically, which is when their
// text can share one escape run.
//
// It compares what they draw (styleKey) rather than lipgloss.Style.String,
// which is not an identity — see push.
func sameRun(a, b Segment) bool {
	return a.Styled == b.Styled && a.Link == b.Link && styleKey(a.Style, a.Styled) == styleKey(b.Style, b.Styled)
}

// wrap lays segments out as rows of at most width columns, breaking between
// words and honouring any hard break the segments carry.
func wrap(segs []Segment, width int) []string {
	var (
		out  []string
		line row
	)
	flush := func() {
		out = append(out, strings.TrimRight(line.String(), " "))
		line.reset()
	}
	for _, seg := range segs {
		for i, part := range strings.Split(seg.Text, "\n") {
			if i > 0 {
				flush()
			}
			for _, word := range splitWords(part) {
				w := ansi.StringWidth(word)
				switch {
				case word == " ":
					// A space that would open a row is the space a wrap just
					// consumed, and it is dropped rather than indenting the
					// row by one.
					if line.used > 0 && line.used < width {
						line.add(seg, word, w)
					}
				case line.used+w <= width:
					line.add(seg, word, w)
				case w > width:
					// A single word wider than the pane cannot be wrapped
					// anywhere, so it is folded — losing it, or letting it
					// run past the edge, are both worse.
					if line.used > 0 {
						flush()
					}
					for _, piece := range foldText(word, width) {
						if line.used > 0 {
							flush()
						}
						line.add(seg, piece, ansi.StringWidth(piece))
					}
				default:
					flush()
					line.add(seg, word, w)
				}
			}
		}
	}
	if line.used > 0 || len(out) == 0 {
		flush()
	}
	return out
}

// wrap is the renderer's, so a block gets the trailing-space trim and the
// empty-block answer in one place.
func (r *renderer) wrap(segs []Segment, width int) []string {
	if len(segs) == 0 {
		return nil
	}
	rows := wrap(segs, width)
	if len(rows) == 1 && rows[0] == "" {
		return nil
	}
	return rows
}

// fold breaks segments at the column, keeping every character and moving none.
func fold(segs []Segment, width int) []string {
	var (
		out  []string
		line row
	)
	for _, seg := range segs {
		for _, piece := range foldText(seg.Text, width) {
			w := ansi.StringWidth(piece)
			if line.used+w > width && line.used > 0 {
				out = append(out, line.String())
				line.reset()
			}
			line.add(seg, piece, w)
		}
	}
	out = append(out, line.String())
	return out
}

// styled draws one piece of a segment in that segment's treatment, hyperlink
// included: a link broken across two rows is a link on both of them.
func styled(seg Segment, text string) string {
	return Segment{Text: text, Style: seg.Style, Styled: seg.Styled, Link: seg.Link}.Render()
}

// splitWords splits on spaces, keeping each space as its own token so the
// wrapper can decide whether it survives the break.
func splitWords(s string) []string {
	var out []string
	var word strings.Builder
	for _, rn := range s {
		if rn == ' ' {
			if word.Len() > 0 {
				out = append(out, word.String())
				word.Reset()
			}
			out = append(out, " ")
			continue
		}
		word.WriteRune(rn)
	}
	if word.Len() > 0 {
		out = append(out, word.String())
	}
	return out
}

// foldText cuts a string into pieces of at most width columns, on rune
// boundaries and by display width, so a wide glyph is never split in half.
func foldText(s string, width int) []string {
	if s == "" {
		return []string{""}
	}
	var (
		out  []string
		cur  strings.Builder
		used int
	)
	for _, rn := range s {
		w := ansi.StringWidth(string(rn))
		if used+w > width && cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
			used = 0
		}
		cur.WriteRune(rn)
		used += w
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}
