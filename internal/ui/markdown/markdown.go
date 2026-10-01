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
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/yuin/goldmark"
	emoji "github.com/yuin/goldmark-emoji"
	gast "github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	xast "github.com/yuin/goldmark/extension/ast"
	gparser "github.com/yuin/goldmark/parser"
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
	// Syntax highlights a fenced block, handed its lines and answering with
	// each line's segments, or is nil for a plain one. It takes the whole
	// block rather than a line at a time because a comment, a docstring, a
	// raw string or a heredoc spans lines, and a line lexed alone reads the
	// inside of one as code. It is injected rather than owned so that the
	// fence and the diff view highlight through the same register
	// (chat/highlight.go). An answer that is not one entry per line is
	// ignored, and the block is drawn plain.
	Syntax func(lang string, lines []string) [][]Segment
	// Prose is the register a paragraph's plain text is drawn in.
	Prose ProseTone
}

// ProseTone selects the grey a document's plain text takes. Everything else
// in the register — headings, code, links, rules — is the same document at
// either tone: what differs is whose sentence it is.
type ProseTone int

const (
	// ProseBody is the model's own prose, and the zero value because every
	// document that is not a person's own message is the model's.
	ProseBody ProseTone = iota
	// ProseBright is a message the reader sent. The transcript labels
	// neither half of the conversation, so weight is what tells them apart:
	// what a person typed is the brightest text in the pane and the reply
	// under it is body prose (chat/render.go,
	// docs/interface/surfaces.md#the-activity-row).
	ProseBright
)

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
//
// The extension set is glamour's, deliberately: GFM for tables, task lists,
// strikethrough and bare URLs, definition lists, and the emoji shortcodes a
// model reaches for. Dropping one of these would not be a simplification, it
// would be a document shhh renders worse than the thing it replaced.
var parser = goldmark.New(goldmark.WithParser(newParser()), goldmark.WithExtensions(
	extension.GFM,
	extension.DefinitionList,
	emoji.New(),
))

// newParser is goldmark's default parser with its fence parser told to
// remember which fences closed. goldmark ends an unclosed fence at the end
// of its container and keeps no record that it did, and the difference is
// the one between a block and a block still arriving: a fence still open in
// a stream has no end yet, so it is neither headed nor counted (code).
func newParser() gparser.Parser {
	blocks := gparser.DefaultBlockParsers()
	for i, b := range blocks {
		if b.Value == gparser.NewFencedCodeBlockParser() {
			blocks[i].Value = closingFence{gparser.NewFencedCodeBlockParser()}
		}
	}
	return gparser.NewParser(
		gparser.WithBlockParsers(blocks...),
		gparser.WithInlineParsers(gparser.DefaultInlineParsers()...),
		gparser.WithParagraphTransformers(gparser.DefaultParagraphTransformers()...),
	)
}

// closedAttr marks a fence whose closing line was read.
const closedAttr = "shhh-closed"

// closingFence is the fence parser, unchanged, except that it marks the
// node when a line closes it: Continue answers Close for exactly that line
// and for nothing else.
type closingFence struct{ gparser.BlockParser }

func (f closingFence) Continue(node gast.Node, reader text.Reader, pc gparser.Context) gparser.State {
	st := f.BlockParser.Continue(node, reader, pc)
	if st == gparser.Close {
		node.SetAttributeString(closedAttr, true)
	}
	return st
}

// closed reports whether a fenced block's closing line was read.
func closed(n gast.Node) bool {
	_, ok := n.AttributeString(closedAttr)
	return ok
}

// Render lays src out at the given width and returns the rows joined by
// newlines, with no leading or trailing blank row.
//
// The result is a whole document. Callers gluing one render onto another want
// Blocks, which hands back the rows and lets the caller own the seam.
func Render(src string, o Options) string {
	return strings.Join(Blocks(src, o), "\n")
}

// Fence is where one fenced code block landed in a render: the heading row
// above it and the half-open range of its code rows, as indexes into the
// rows Layout returned.
//
// Index is the block's place among the headed blocks in source order,
// counted from 0, and is the same count FenceTexts makes, so a row can be
// resolved to the block a copy by number takes without parsing the source
// again. Only a closed fence is headed and counted: one still open in a
// stream has no end yet, and an indented block has no fence to head.
type Fence struct {
	Index      int
	Heading    int
	Start, End int
}

// FenceText is one headed block as the source wrote it: the fence's
// language word, empty where it named none, and the body with no fence
// lines, no indent its container gave it, and its tabs kept.
type FenceText struct {
	Lang, Body string
}

// FenceTexts returns the headed blocks of src in source order — the blocks
// Layout reports, counted the same way.
func FenceTexts(src string) []FenceText {
	source := []byte(src)
	doc := parser.Parser().Parse(text.NewReader(source))
	var out []FenceText
	_ = gast.Walk(doc, func(n gast.Node, entering bool) (gast.WalkStatus, error) {
		f, ok := n.(*gast.FencedCodeBlock)
		if !entering || !ok || !closed(f) {
			return gast.WalkContinue, nil
		}
		lines := f.Lines()
		body := make([]string, lines.Len())
		for i := range lines.Len() {
			seg := lines.At(i)
			body[i] = strings.TrimRight(string(seg.Value(source)), "\n")
		}
		out = append(out, FenceText{Lang: string(f.Language(source)), Body: strings.Join(body, "\n")})
		return gast.WalkSkipChildren, nil
	})
	return out
}

// Layout is Blocks with the fenced blocks it drew: which rows each one
// occupies and which block it is. The ranges hold through everything that
// shifts or prefixes a block's rows — a quote's rail, a list item's hang, a
// fold — because they are read off the finished rows rather than added up.
func Layout(src string, o Options) ([]string, []Fence) {
	if o.Width <= 0 {
		o.Width = 80
	}
	source := []byte(src)
	doc := parser.Parser().Parse(text.NewReader(source))
	r := &renderer{opt: o, src: source, sty: newStyles(o.Mono, o.Prose), headMark: headingMark(src)}
	rows := r.children(doc, o.contentWidth())
	var fences []Fence
	for i, row := range rows {
		if at := strings.Index(row, r.headMark); at >= 0 && len(fences) < len(r.fenceRows) {
			row = row[:at] + row[at+len(r.headMark):]
			n := len(fences)
			fences = append(fences, Fence{Index: n, Heading: i, Start: i + 1, End: i + 1 + r.fenceRows[n]})
		}
		rows[i] = r.pad(row)
	}
	return rows, fences
}

// Blocks is Render as the rows it produced.
//
// The streaming cache (chat/streammd.go) needs this rather than a string: it
// renders a message in pieces and glues them, and a seam it can see is a seam
// it does not have to reverse-engineer. The seam between two top-level blocks
// is one padded blank row, always — which is the whole reason the sentinel
// paragraph that used to measure glamour's unpredictable seam is gone.
func Blocks(src string, o Options) []string {
	rows, _ := Layout(src, o)
	return rows
}

// renderer carries what every block needs: the options, the source the AST
// points into, and the resolved styles.
type renderer struct {
	opt Options
	src []byte
	sty styles
	// fenceRows is how many code rows each headed block drew, in the order
	// they were drawn, which is source order.
	fenceRows []int
	// headMark is what a heading row is drawn behind (headingMark).
	headMark string
}

// headingMark is what finds a heading row again once the containers around
// its block have prefixed and shifted it. It is an escape string, which
// every width function counts as nothing, so the row lays out as if it were
// not there; Layout takes it out before any row leaves the package.
//
// It is chosen to be absent from the source. Every byte of a row that is not
// the renderer's own comes from the source, so a mark the source does not
// hold cannot be forged by it — a reply that quoted the mark would otherwise
// have been read as one more heading than the render drew, and counted past
// the end of the blocks.
func headingMark(src string) string {
	mark := "\x1b_shhh-fence\x1b\\"
	for n := 0; strings.Contains(src, mark); n++ {
		mark = "\x1b_shhh-fence-" + strconv.Itoa(n) + "\x1b\\"
	}
	return mark
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
		return r.code(n, string(n.Language(r.src)), closed(n), width)
	case *gast.CodeBlock:
		return r.code(n, "", false, width)
	case *gast.Blockquote:
		return r.quote(n, width)
	case *gast.List:
		return r.list(n, width)
	case *gast.ThematicBreak:
		return []string{r.sty.rule.Render(strings.Repeat(r.sty.hline(), width))}
	case *gast.HTMLBlock:
		return r.raw(n, width)
	case *xast.DefinitionList:
		return r.definitions(n, width)
	case *xast.DefinitionTerm:
		return r.wrap(r.inline(n), width)
	case *xast.DefinitionDescription:
		return r.indent(r.children(n, max(width-defIndent, 1)), defIndent)
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
//
// A closed fence is headed by a row naming its language — `code` where the
// fence named none — at the block's own indent, one row above it. Once the
// fence lines are gone the indent alone says a block has started, and two
// blocks one after the other read as one; the heading is the start marker,
// and the row a pointer copies the block from
// (docs/interface/surfaces.md#the-activity-row).
func (r *renderer) code(n gast.Node, lang string, headed bool, width int) []string {
	inner := max(width-codeIndent, 1)
	lines := n.Lines()
	text := make([]string, lines.Len())
	for i := range lines.Len() {
		seg := lines.At(i)
		text[i] = expandTabs(strings.TrimRight(string(seg.Value(r.src)), "\n"))
	}
	var tones [][]Segment
	if r.opt.Syntax != nil && !r.opt.Mono && len(text) > 0 {
		if tones = r.opt.Syntax(lang, text); len(tones) != len(text) {
			tones = nil
		}
	}
	var rows []string
	if headed {
		rows = append(rows, r.headMark+strings.Repeat(" ", codeIndent)+r.fenceHeading(lang, inner))
	}
	for i, line := range text {
		var segs []Segment
		if tones != nil {
			segs = tones[i]
		}
		if len(segs) == 0 {
			segs = []Segment{{Text: line, Style: r.sty.code, Styled: !r.opt.Mono}}
		}
		for _, row := range fold(segs, inner) {
			rows = append(rows, strings.Repeat(" ", codeIndent)+row)
		}
	}
	if headed {
		r.fenceRows = append(r.fenceRows, len(rows)-1)
	}
	return rows
}

// fenceHeading is the word a heading row carries, in the dim tone, cut to
// the room there is rather than folded: it is one word, and a second row of
// it would read as the first line of the code.
func (r *renderer) fenceHeading(lang string, width int) string {
	if lang == "" {
		lang = "code"
	}
	return Segment{Text: ansi.Truncate(lang, width, ""), Style: r.sty.fence, Styled: !r.opt.Mono}.Render()
}

// defIndent is how far a definition sits under its term. A description that
// began in the term's own column would read as a second term.
const defIndent = 2

// definitions renders a definition list: each term on its own row with its
// descriptions indented under it, and a blank row between one pair and the
// next rather than between a term and its own description.
func (r *renderer) definitions(n gast.Node, width int) []string {
	var rows []string
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		block := r.block(c, width)
		if len(block) == 0 {
			continue
		}
		if _, term := c.(*xast.DefinitionTerm); term && len(rows) > 0 {
			rows = append(rows, "")
		}
		rows = append(rows, block...)
	}
	return rows
}

// indent shifts a block's rows right by n columns.
func (r *renderer) indent(rows []string, n int) []string {
	pad := strings.Repeat(" ", n)
	for i, row := range rows {
		rows[i] = pad + row
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
