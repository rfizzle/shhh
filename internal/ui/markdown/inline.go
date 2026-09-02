package markdown

import (
	"strings"

	"charm.land/lipgloss/v2"
	gast "github.com/yuin/goldmark/ast"
	xast "github.com/yuin/goldmark/extension/ast"
)

// Segment is a run of text drawn in one treatment. The wrapper works on these
// rather than on a styled string, because a word wrap has to measure text and
// an escape is not text.
type Segment struct {
	Text   string
	Style  lipgloss.Style
	Styled bool
}

// Render draws the segment, or hands back its bare text where nothing styles
// it — which is every segment in mono, and is why a mono render carries no
// escapes at all.
func (s Segment) Render() string {
	if !s.Styled {
		return s.Text
	}
	return s.Style.Render(s.Text)
}

// inline flattens a node's inline children into segments.
//
// The base treatment is the body token, in force from the first character: a
// paragraph is drawn in the palette's body colour the way every other run of
// prose in the interface is. In mono nothing is in force, which is what makes
// a mono render carry no escapes at all.
func (r *renderer) inline(n gast.Node) []Segment {
	var out []Segment
	r.appendInline(&out, n, r.sty.body, !r.opt.Mono)
	return out
}

// appendInline walks the inline tree, carrying the style in force down it so
// that bold inside a link is both.
func (r *renderer) appendInline(out *[]Segment, n gast.Node, style lipgloss.Style, styled bool) {
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		switch c := c.(type) {
		case *gast.Text:
			r.appendText(out, c, style, styled)
		case *gast.String:
			r.push(out, string(c.Value), style, styled)
		case *gast.CodeSpan:
			r.codeSpan(out, c)
		case *gast.Emphasis:
			r.emphasis(out, c, style, styled)
		case *xast.Strikethrough:
			r.mark(out, c, "~~", r.sty.strike, style, styled)
		case *gast.Link:
			ls, lok := r.merge(style, styled, r.sty.link)
			r.appendInline(out, c, ls, lok)
			r.push(out, " "+string(c.Destination), r.sty.url, !r.opt.Mono)
		case *gast.AutoLink:
			r.push(out, string(c.URL(r.src)), r.sty.url, !r.opt.Mono)
		case *gast.Image:
			r.push(out, "!", style, styled)
			is, iok := r.merge(style, styled, r.sty.link)
			r.appendInline(out, c, is, iok)
			r.push(out, " "+string(c.Destination), r.sty.url, !r.opt.Mono)
		case *gast.RawHTML:
			for i := range c.Segments.Len() {
				seg := c.Segments.At(i)
				r.push(out, string(seg.Value(r.src)), r.sty.faint, !r.opt.Mono)
			}
		default:
			r.appendInline(out, c, style, styled)
		}
	}
}

// appendText handles the two line breaks a paragraph can hold. A soft break
// is a space, because the wrapper below decides where lines end; a hard break
// is a break the author asked for and survives.
func (r *renderer) appendText(out *[]Segment, t *gast.Text, style lipgloss.Style, styled bool) {
	seg := t.Segment
	r.push(out, string(seg.Value(r.src)), style, styled)
	switch {
	case t.HardLineBreak():
		r.push(out, "\n", style, false)
	case t.SoftLineBreak():
		r.push(out, " ", style, false)
	}
}

// codeSpan keeps its backticks in mono and takes the accent in colour. The
// backticks are not decoration there: they are the only thing left saying the
// words inside are a name rather than prose.
func (r *renderer) codeSpan(out *[]Segment, c *gast.CodeSpan) {
	var b strings.Builder
	for s := c.FirstChild(); s != nil; s = s.NextSibling() {
		if t, ok := s.(*gast.Text); ok {
			seg := t.Segment
			b.Write(seg.Value(r.src))
		}
	}
	if r.opt.Mono {
		r.push(out, "`"+b.String()+"`", lipgloss.Style{}, false)
		return
	}
	r.push(out, b.String(), r.sty.code, true)
}

// emphasis is bold at two markers and italic at one.
func (r *renderer) emphasis(out *[]Segment, e *gast.Emphasis, style lipgloss.Style, styled bool) {
	mark, add := "*", r.sty.italic
	if e.Level >= 2 {
		mark, add = "**", r.sty.bold
	}
	r.mark(out, e, mark, add, style, styled)
}

// mark renders an inline span that mono spells with characters and colour
// spells with a treatment.
func (r *renderer) mark(out *[]Segment, n gast.Node, mark string, add lipgloss.Style, style lipgloss.Style, styled bool) {
	if r.opt.Mono {
		r.push(out, mark, style, styled)
		r.appendInline(out, n, style, styled)
		r.push(out, mark, style, styled)
		return
	}
	ms, mok := r.merge(style, styled, add)
	r.appendInline(out, n, ms, mok)
}

// merge lays one treatment over another, or returns the new one alone where
// nothing was in force. In mono nothing is ever in force.
func (r *renderer) merge(style lipgloss.Style, styled bool, add lipgloss.Style) (lipgloss.Style, bool) {
	if r.opt.Mono {
		return style, false
	}
	if !styled {
		return add, true
	}
	return style.Inherit(add), true
}

// push appends a segment, folding it into the previous one where the two are
// drawn the same way. Merging here is what keeps a paragraph from becoming one
// escape pair per word.
func (r *renderer) push(out *[]Segment, s string, style lipgloss.Style, styled bool) {
	if s == "" {
		return
	}
	if n := len(*out); n > 0 {
		if prev := (*out)[n-1]; prev.Styled == styled && (!styled || prev.Style.String() == style.String()) {
			(*out)[n-1].Text += s
			return
		}
	}
	*out = append(*out, Segment{Text: s, Style: style, Styled: styled})
}
