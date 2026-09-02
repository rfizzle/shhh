// Package markdown lays a markdown document out for a terminal pane.
//
// It exists because glamour got four things wrong on the surface shhh cares
// about most, and three of them destroy content rather than merely looking
// wrong:
//
//   - A fenced code block was word-wrapped as if it were prose, so a long
//     line came back reflowed and re-indented. That is not a rendering of the
//     code; it is different code.
//   - A list item's wrapped lines got no hanging indent, so a continuation
//     sat under the bullet instead of under the text and read as a new item.
//   - A loose list item's second paragraph was concatenated onto the first
//     with no separator at all: `2. secondnested paragraph under item two`.
//   - Every trailing pad space carried its own colour escape, so 774 visible
//     characters cost 7,984 bytes, and the transcript re-renders the arriving
//     message on every frame.
//
// None of that is configurable, glamour v2.0.1 is the newest v2, and the
// package this replaces was the last thing in the interface drawing outside
// the three-rung palette (components/palette.go).
//
// What is deliberate here:
//
//   - Padding stays. Every row is filled out to the block width, because the
//     selection reads that padding as the record of how far the wrapper was
//     allowed to go (chat/select.go). It is a plain run of spaces carrying no
//     escapes, which is where the byte savings come from — the padding was
//     never the problem, the escape per space was.
//   - Code is folded, never reflowed. A line too long for the pane breaks at
//     the column and continues on the next row, so every character survives
//     and no word moves. Clipping would have been tidier and would have lied.
//   - Mono keeps the marks. When the colour goes the `**` stays, and so do
//     the backticks and the heading's `#` — the invariant the rest of the
//     interface holds, applied to prose (docs/interface/principles.md).
package markdown

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/yuin/goldmark"
	gast "github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
)

// Margin is the document's left inset, and the amount a row is held back from
// the right edge. It is two columns on each side, which is what the selection's
// dedent expects to strip (chat/select.go) and what the goldens were drawn at.
const Margin = 2

// Options is everything a render depends on besides the source.
type Options struct {
	// Width is the pane. The document lays out inside it, not up to it.
	Width int
	// Mono drops every colour and puts the markdown's own marks back in
	// their place.
	Mono bool
	// Syntax highlights one line of a fenced block, or is nil for a plain
	// one. It is injected rather than owned so that the fence and the diff
	// view highlight through the same register (chat/highlight.go).
	Syntax func(lang, line string) []Segment
}

// contentWidth is the widest a row's own content may be.
func (o Options) contentWidth() int { return max(o.Width-2*Margin, 1) }

// FillWidth is the column a row is padded out to: the content plus the left
// margin, leaving the right margin empty.
//
// It is exported because the streaming cache writes the seam between two
// blocks itself, and a seam padded to anything else would break the rule that
// a glued render is the render of the whole message (chat/streammd.go).
func (o Options) FillWidth() int { return o.contentWidth() + Margin }

// parser is built once. goldmark's parser holds no per-document state.
var parser = goldmark.New(goldmark.WithExtensions(extension.GFM))

// Render lays src out at the given width and returns the rows joined by
// newlines, with no leading or trailing blank row.
//
// The result is a whole document. Callers gluing one render onto another want
// Blocks, which hands back the rows and lets the caller own the seam.
func Render(src string, o Options) string {
	return strings.Join(Blocks(src, o), "\n")
}

// Blocks is Render as the rows it produced.
//
// The streaming cache (chat/streammd.go) needs this rather than a string: it
// renders a message in pieces and glues them, and a seam it can see is a seam
// it does not have to reverse-engineer. The seam between two top-level blocks
// is one padded blank row, always — which is the whole reason the sentinel
// paragraph that used to measure glamour's unpredictable seam is gone.
func Blocks(src string, o Options) []string {
	if o.Width <= 0 {
		o.Width = 80
	}
	source := []byte(src)
	doc := parser.Parser().Parse(text.NewReader(source))
	r := &renderer{opt: o, src: source, sty: newStyles(o.Mono)}
	rows := r.children(doc, o.contentWidth())
	for i, row := range rows {
		rows[i] = r.pad(row)
	}
	return rows
}

// renderer carries what every block needs: the options, the source the AST
// points into, and the resolved styles.
type renderer struct {
	opt Options
	src []byte
	sty styles
}

// pad puts the left margin on a row and fills it out to the block width.
//
// The fill is written as bare spaces on purpose. A styled space costs eleven
// bytes and says nothing: the colour of a space is the colour of nothing.
func (r *renderer) pad(row string) string {
	row = strings.Repeat(" ", Margin) + row
	if n := r.opt.FillWidth() - ansi.StringWidth(row); n > 0 {
		row += strings.Repeat(" ", n)
	}
	return row
}

// children renders every top-level block of n at the given content width,
// separating each pair with one blank row.
func (r *renderer) children(n gast.Node, width int) []string {
	var rows []string
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		block := r.block(c, width)
		if len(block) == 0 {
			continue
		}
		if len(rows) > 0 {
			rows = append(rows, "")
		}
		rows = append(rows, block...)
	}
	return rows
}

// block renders one block node to rows of at most width columns.
func (r *renderer) block(n gast.Node, width int) []string {
	switch n := n.(type) {
	case *gast.Heading:
		return r.heading(n, width)
	case *gast.Paragraph, *gast.TextBlock:
		return r.wrap(r.inline(n), width)
	case *gast.FencedCodeBlock:
		return r.code(n, string(n.Language(r.src)), width)
	case *gast.CodeBlock:
		return r.code(n, "", width)
	case *gast.Blockquote:
		return r.quote(n, width)
	case *gast.List:
		return r.list(n, width)
	case *gast.ThematicBreak:
		return []string{r.sty.rule.Render(strings.Repeat(r.sty.hline(), width))}
	case *gast.HTMLBlock:
		return r.raw(n, width)
	}
	if table := r.table(n, width); table != nil {
		return table
	}
	// An unknown block still has children, and rendering them is closer to
	// right than dropping the text on the floor.
	if n.HasChildren() {
		return r.children(n, width)
	}
	return nil
}

// heading is the one block whose mark mono keeps and colour drops. A reader
// in colour has the weight and the tone to go on; a reader in mono has the
// hashes, which is what they would have typed.
func (r *renderer) heading(n *gast.Heading, width int) []string {
	segs := r.inline(n)
	if r.opt.Mono {
		mark := strings.Repeat("#", n.Level) + " "
		segs = append([]Segment{{Text: mark}}, segs...)
		return r.wrap(segs, width)
	}
	for i := range segs {
		segs[i].Style, segs[i].Styled = r.sty.heading, true
	}
	return r.wrap(segs, width)
}

// codeIndent insets a fenced block from the prose around it. Without it a
// block of code is flush with the paragraph above and reads as more of it;
// the indent is the only thing left saying "this is a block", since the fence
// markers are gone by the time it is drawn.
const codeIndent = 2

// tabWidth is what a tab in a code block is drawn as.
//
// It has to be drawn as something. A literal tab measures zero columns to
// every width function there is and then takes eight on the screen, so a row
// carrying one is padded to the wrong width, overflows the pane, and drags
// the selection's line arithmetic with it.
const tabWidth = 4

// code renders a fenced or indented block: highlighted where a lexer claims
// the language, folded rather than wrapped, and never reflowed.
func (r *renderer) code(n gast.Node, lang string, width int) []string {
	inner := max(width-codeIndent, 1)
	var rows []string
	lines := n.Lines()
	for i := range lines.Len() {
		seg := lines.At(i)
		line := expandTabs(strings.TrimRight(string(seg.Value(r.src)), "\n"))
		var segs []Segment
		if r.opt.Syntax != nil && !r.opt.Mono {
			segs = r.opt.Syntax(lang, line)
		}
		if len(segs) == 0 {
			segs = []Segment{{Text: line, Style: r.sty.code, Styled: !r.opt.Mono}}
		}
		for _, row := range fold(segs, inner) {
			rows = append(rows, strings.Repeat(" ", codeIndent)+row)
		}
	}
	return rows
}

// expandTabs replaces every tab with spaces to the next tab stop, so that the
// column a character is drawn at is the column it measures at.
func expandTabs(s string) string {
	if !strings.ContainsRune(s, '\t') {
		return s
	}
	var b strings.Builder
	col := 0
	for _, rn := range s {
		if rn == '\t' {
			n := tabWidth - col%tabWidth
			b.WriteString(strings.Repeat(" ", n))
			col += n
			continue
		}
		b.WriteRune(rn)
		col++
	}
	return b.String()
}

// quote prefixes its body with a rail, and renders that body as a document of
// its own so a quoted list or fence is still a list or a fence.
func (r *renderer) quote(n gast.Node, width int) []string {
	rail := "│ "
	if r.opt.Mono {
		rail = "| "
	}
	inner := max(width-len([]rune(rail)), 1)
	var rows []string
	for _, row := range r.children(n, inner) {
		rows = append(rows, r.sty.rule.Render(rail)+row)
	}
	return rows
}

// raw passes an HTML block through as the text it is. Nobody is rendering
// HTML in a terminal, and swallowing it would lose whatever the model meant
// by it.
func (r *renderer) raw(n gast.Node, width int) []string {
	var rows []string
	lines := n.Lines()
	for i := range lines.Len() {
		seg := lines.At(i)
		line := expandTabs(strings.TrimRight(string(seg.Value(r.src)), "\n"))
		rows = append(rows, fold([]Segment{{Text: line, Style: r.sty.faint, Styled: !r.opt.Mono}}, width)...)
	}
	return rows
}
