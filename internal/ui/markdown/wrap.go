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

// wrap lays segments out as rows of at most width columns, breaking between
// words and honouring any hard break the segments carry.
func wrap(segs []Segment, width int) []string {
	var (
		rows []string
		line strings.Builder
		used int
	)
	flush := func() {
		rows = append(rows, strings.TrimRight(line.String(), " "))
		line.Reset()
		used = 0
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
					if used > 0 && used < width {
						line.WriteString(styled(seg, word))
						used++
					}
				case used+w <= width:
					line.WriteString(styled(seg, word))
					used += w
				case w > width:
					// A single word wider than the pane cannot be wrapped
					// anywhere, so it is folded — losing it, or letting it
					// run past the edge, are both worse.
					if used > 0 {
						flush()
					}
					for _, piece := range foldText(word, width) {
						if used > 0 {
							flush()
						}
						line.WriteString(styled(seg, piece))
						used = ansi.StringWidth(piece)
					}
				default:
					flush()
					line.WriteString(styled(seg, word))
					used = w
				}
			}
		}
	}
	if line.Len() > 0 || len(rows) == 0 {
		flush()
	}
	return rows
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
		rows []string
		line strings.Builder
		used int
	)
	for _, seg := range segs {
		for _, piece := range foldText(seg.Text, width) {
			w := ansi.StringWidth(piece)
			if used+w > width && used > 0 {
				rows = append(rows, line.String())
				line.Reset()
				used = 0
			}
			line.WriteString(styled(seg, piece))
			used += w
		}
	}
	rows = append(rows, line.String())
	return rows
}

// styled draws one piece of a segment in that segment's treatment.
func styled(seg Segment, text string) string {
	return Segment{Text: text, Style: seg.Style, Styled: seg.Styled}.Render()
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
