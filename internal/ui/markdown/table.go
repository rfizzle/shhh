package markdown

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	gast "github.com/yuin/goldmark/ast"
	xast "github.com/yuin/goldmark/extension/ast"
)

// A GFM table, laid out to the columns the content needs and then squeezed to
// the pane.
//
// Squeezing takes from the widest column first, which is the one that can
// afford it: a table of one prose column and three numbers should lose width
// from the prose, not a character from each.

func (r *renderer) table(n gast.Node, width int) []string {
	t, ok := n.(*xast.Table)
	if !ok {
		return nil
	}
	rows := r.tableCells(t)
	if len(rows) == 0 {
		return nil
	}
	widths := columnWidths(rows, width)

	var out []string
	for i, row := range rows {
		out = append(out, r.tableRow(row, widths))
		// The rule sits under the header, and only there: a rule between
		// every row is a border, and this table has none.
		if i == 0 && t.FirstChild() != nil {
			var parts []string
			for _, w := range widths {
				parts = append(parts, strings.Repeat(r.sty.hline(), w))
			}
			rule := r.sty.hline() + r.sty.cross() + r.sty.hline()
			out = append(out, r.sty.rule.Render(strings.Join(parts, rule)))
		}
	}
	return out
}

// tableCells flattens the table to the rendered text of each cell.
func (r *renderer) tableCells(t *xast.Table) [][]string {
	var rows [][]string
	collect := func(row gast.Node) {
		var cells []string
		for c := row.FirstChild(); c != nil; c = c.NextSibling() {
			cells = append(cells, strings.Join(segmentText(r.inline(c)), ""))
		}
		rows = append(rows, cells)
	}
	for c := t.FirstChild(); c != nil; c = c.NextSibling() {
		if h, ok := c.(*xast.TableHeader); ok {
			collect(h)
			continue
		}
		collect(c)
	}
	return rows
}

// tableRow draws one row's cells at the settled widths.
func (r *renderer) tableRow(cells []string, widths []int) string {
	var parts []string
	for i, w := range widths {
		cell := ""
		if i < len(cells) {
			cell = cells[i]
		}
		if pieces := foldText(cell, w); len(pieces) > 0 {
			cell = pieces[0]
		}
		parts = append(parts, r.sty.body.Render(cell)+strings.Repeat(" ", max(w-ansi.StringWidth(cell), 0)))
	}
	return strings.Join(parts, r.sty.rule.Render(" "+r.sty.vline()+" "))
}

// columnWidths is what each column needs, squeezed to fit the pane.
func columnWidths(rows [][]string, width int) []int {
	cols := 0
	for _, row := range rows {
		cols = max(cols, len(row))
	}
	if cols == 0 {
		return nil
	}
	widths := make([]int, cols)
	for _, row := range rows {
		for i, cell := range row {
			widths[i] = max(widths[i], ansi.StringWidth(cell))
		}
	}
	// Three columns of separator between each pair: the rule and a space on
	// each side of it.
	budget := width - 3*(cols-1)
	for total(widths) > budget {
		widest := 0
		for i, w := range widths {
			if w > widths[widest] {
				widest = i
			}
		}
		if widths[widest] <= 1 {
			break
		}
		widths[widest]--
	}
	return widths
}

func total(ns []int) int {
	sum := 0
	for _, n := range ns {
		sum += n
	}
	return sum
}

func segmentText(segs []Segment) []string {
	out := make([]string, len(segs))
	for i, s := range segs {
		out[i] = s.Text
	}
	return out
}

// The three glyphs a table is drawn with, in the two registers. Mono gets the
// ASCII forms for the same reason it gets the markdown marks back.
func (s styles) hline() string {
	if s.isMono() {
		return "-"
	}
	return "─"
}

func (s styles) vline() string {
	if s.isMono() {
		return "|"
	}
	return "│"
}

func (s styles) cross() string {
	if s.isMono() {
		return "+"
	}
	return "┼"
}

// isMono is the register's own answer, not a re-reading of the global: the
// glyphs have to agree with the styles they are drawn beside, and the styles
// were settled once when the register was built.
func (s styles) isMono() bool { return s.mono }
