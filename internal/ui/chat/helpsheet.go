package chat

// The help sheet: /help and the key list laid out on the grid.
//
// They were one string each, authored with their line breaks at about eighty
// columns and printed as a notice that had laid itself out — which a notice
// is allowed to be, and which ran every row off a pane narrower than the
// author's (docs/interface/principles.md#one-grid). So the rows are data and
// the layout is done here, at the width the transcript is drawn at: a head
// column, then each paragraph wrapped under the prose column, the key
// column in the colour a key is offered in and the prose in the notice's
// grey.

import (
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"

	"github.com/rfizzle/shhh/internal/ui/components"
)

// helpSheet is the sections of one help notice, in order.
type helpSheet []helpSection

// helpSection is one titled block of the sheet: its rows under a head column
// of the given width. A row with no head is prose across the prose column.
type helpSection struct {
	title string
	// head is the head column's width, the two columns that part it from the
	// prose included.
	head int
	// keys says the heads are keys, painted the way a key is offered; a
	// command or a policy field is painted as the body a row is read for.
	keys bool
	rows []helpRow
}

// helpRow is one row: its head, a line of the column each, and its
// paragraphs.
type helpRow struct {
	head  []string
	paras []string
}

// helpTextWidth is the width the sheet is laid out at when it is text rather
// than a render: what a copy of the row and a search over it read. It is wide
// enough that no paragraph wraps, so a copy is re-flowed by whatever it is
// pasted into rather than broken at a width nobody chose.
const helpTextWidth = 1 << 12

// helpIndent is where a section's rows start under its title.
const helpIndent = 2

// helpMinProse is the narrowest the prose column may be before the row gives
// up its side-by-side layout and puts the paragraphs under the head instead:
// a column of three words a line is harder to read than a row that stacks.
const helpMinProse = 24

// text is the sheet laid out unpainted at helpTextWidth.
func (s helpSheet) text() string {
	return strings.Join(s.lines(helpTextWidth, false), "\n")
}

// lines lays the sheet out at width, painted or not. No line is wider than
// width, whatever the paragraphs say: a word too long for its column is the
// one thing that clips, and only once the wrap has nowhere else to put it.
func (s helpSheet) lines(width int, paint bool) []string {
	var out []string
	for i, sec := range s {
		if i > 0 {
			out = append(out, "")
		}
		out = append(out, sec.lines(width, paint)...)
	}
	return out
}

func (sec helpSection) lines(width int, paint bool) []string {
	title, body, prose := plainStyle, plainStyle, plainStyle
	if paint {
		title, body, prose = sty.StatusBar, sty.Step.Title, sty.SystemMsg
	}
	// A head in brackets is a key and is painted the way a key is offered;
	// a paste, a wheel or a click is not pressed, and is painted as the
	// body a row is read for, as a command or a policy field is.
	head := func(h string) lipgloss.Style {
		if paint && sec.keys && strings.HasPrefix(h, "[") {
			return sty.Hint.Key
		}
		return body
	}
	out := []string{components.Clip(title.Render(sec.title), width)}
	proseCol := helpIndent + sec.head
	stacked := width-proseCol < helpMinProse
	for _, r := range sec.rows {
		if len(r.head) == 0 {
			// A row with no head is prose about the section as a whole, on
			// the column the section's rows start at.
			for _, p := range r.paras {
				for _, l := range wrapHanging(p, max(width-helpIndent, 1)) {
					out = append(out, components.Clip(pad(helpIndent)+prose.Render(l), width))
				}
			}
			continue
		}
		var text []string
		bodyCol := proseCol
		if stacked {
			bodyCol = 2 * helpIndent
		}
		for _, p := range r.paras {
			text = append(text, wrapHanging(p, max(width-bodyCol, 1))...)
		}
		if stacked {
			for _, h := range r.head {
				out = append(out, components.Clip(pad(helpIndent)+head(h).Render(h), width))
			}
			for _, l := range text {
				out = append(out, components.Clip(pad(bodyCol)+prose.Render(l), width))
			}
			continue
		}
		for i := 0; i < max(len(r.head), len(text)); i++ {
			var line string
			h := ""
			if i < len(r.head) {
				h = r.head[i]
			}
			if h != "" {
				line = pad(helpIndent) + head(h).Render(h)
			}
			if i < len(text) && text[i] != "" {
				gap := proseCol - helpIndent - utf8.RuneCountInString(h)
				if h == "" {
					gap = proseCol
				} else {
					gap = max(gap, 1)
				}
				line += pad(gap) + prose.Render(text[i])
			}
			out = append(out, components.Clip(line, width))
		}
	}
	return out
}

// plainStyle renders a string as it is: the sheet laid out as text.
var plainStyle = lipgloss.NewStyle()

func pad(n int) string { return strings.Repeat(" ", max(n, 0)) }

// wrapHanging wraps one paragraph to width. A paragraph's leading spaces are
// its indent, kept on every line of it; a paragraph whose first words are
// followed by two spaces or more is a row inside the paragraph — a term and
// what it does — and its continuation lines hang under the text after the
// gap rather than under the term. Where the column is too narrow for the
// hang to leave room to read, the text goes under the term instead.
func wrapHanging(p string, width int) []string {
	body := strings.TrimLeft(p, " ")
	lead := len(p) - len(body)
	term := ""
	if i := strings.Index(body, "  "); i > 0 {
		j := i
		for j < len(body) && body[j] == ' ' {
			j++
		}
		term, body = body[:j], body[j:]
	}
	hang := lead + utf8.RuneCountInString(term)
	if term != "" && width-hang < helpMinProse/2 {
		head := pad(lead) + strings.TrimRight(term, " ")
		return append([]string{head}, wrapHanging(pad(lead+helpIndent)+body, width)...)
	}
	words := strings.Fields(body)
	if len(words) == 0 {
		return []string{strings.TrimRight(pad(lead)+term, " ")}
	}
	var out []string
	line := pad(lead) + term
	col := hang
	fresh := true
	for _, w := range words {
		ww := lipgloss.Width(w)
		if !fresh && col+1+ww > width {
			out = append(out, line)
			line, col = pad(hang), hang
			fresh = true
		}
		if !fresh {
			line += " "
			col++
		}
		line += w
		col += ww
		fresh = false
	}
	return append(out, line)
}
