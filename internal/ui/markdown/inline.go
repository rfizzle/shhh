package markdown

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	east "github.com/yuin/goldmark-emoji/ast"
	gast "github.com/yuin/goldmark/ast"
	xast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/util"
)

// TaskBoxWidth is the width a rendered checkbox occupies, `[x] `. The list
// layout needs it to set the hang for an item whose marker is the box.
const TaskBoxWidth = 4

// Segment is a run of text drawn in one treatment. The wrapper works on these
// rather than on a styled string, because a word wrap has to measure text and
// an escape is not text.
type Segment struct {
	Text   string
	Style  lipgloss.Style
	Styled bool
	// Link makes the run a clickable hyperlink (OSC 8) in the terminals that
	// support one. It is zero width, so nothing that measures a row has to
	// know about it, and a terminal that does not understand it ignores it —
	// which is why the URL is still printed beside the label as well.
	Link string
	// key identifies the treatment, for the merge in push. It is set there
	// and nowhere else: a segment built outside this package (a fence's
	// highlighter) never goes through the merge, so it never needs one.
	key string
}

// Render draws the segment, or hands back its bare text where nothing styles
// it — which is every segment in mono, and is why a mono render carries no
// escapes at all.
func (s Segment) Render() string {
	out := s.Text
	if s.Styled {
		out = s.Style.Render(out)
	}
	if s.Link != "" {
		out = ansi.SetHyperlink(s.Link) + out + ansi.ResetHyperlink()
	}
	return out
}

// inline flattens a node's inline children into segments.
//
// The base treatment is the body token, in force from the first character: a
// paragraph is drawn in the palette's body colour the way every other run of
// prose in the interface is. In mono nothing is in force, which is what makes
// a mono render carry no escapes at all.
func (r *renderer) inline(n gast.Node) []Segment {
	var out []Segment
	r.appendInline(&out, n, r.sty.body, !r.opt.Mono, "")
	return out
}

// appendInline walks the inline tree, carrying the style and the enclosing
// link down it so that bold inside a link is both.
//
// The link travels as a parameter rather than being stamped onto the segments
// a link produced. Stamping meant finding them again by index afterwards, and
// the index was wrong whenever push had merged the link's text into the prose
// in front of it — which happens exactly when the two are drawn alike, which
// happens on any terminal reporting no colour.
func (r *renderer) appendInline(out *[]Segment, n gast.Node, style lipgloss.Style, styled bool, link string) {
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		switch c := c.(type) {
		case *gast.Text:
			r.appendText(out, c, style, styled, link)
		case *gast.String:
			r.push(out, r.text(c.Value, c.IsRaw()), style, styled, link)
		case *east.Emoji:
			r.push(out, string(c.Value.Unicode), style, styled, link)
		case *xast.TaskCheckBox:
			// The box is the content. Dropping it turns "not done yet" into
			// "done", which is the worst answer available.
			box := "[ ] "
			if c.IsChecked {
				box = "[x] "
			}
			r.push(out, box, r.sty.marker, !r.opt.Mono, link)
		case *gast.CodeSpan:
			r.codeSpan(out, c, link)
		case *gast.Emphasis:
			r.emphasis(out, c, style, styled, link)
		case *xast.Strikethrough:
			r.mark(out, c, "~~", r.sty.strike, style, styled, link)
		case *gast.Link:
			url := string(c.Destination)
			ls, lok := r.merge(style, styled, r.sty.link)
			r.appendInline(out, c, ls, lok, url)
			r.push(out, " "+url, r.sty.url, !r.opt.Mono, url)
		case *gast.AutoLink:
			url := string(c.URL(r.src))
			r.push(out, url, r.sty.url, !r.opt.Mono, url)
		case *gast.Image:
			url := string(c.Destination)
			r.push(out, "!", style, styled, link)
			is, iok := r.merge(style, styled, r.sty.link)
			r.appendInline(out, c, is, iok, url)
			r.push(out, " "+url, r.sty.url, !r.opt.Mono, url)
		case *gast.RawHTML:
			for i := range c.Segments.Len() {
				seg := c.Segments.At(i)
				r.push(out, string(seg.Value(r.src)), r.sty.faint, !r.opt.Mono, link)
			}
		default:
			r.appendInline(out, c, style, styled, link)
		}
	}
}

// appendText handles the two line breaks a paragraph can hold. A soft break
// is a space, because the wrapper below decides where lines end; a hard break
// is a break the author asked for and survives.
func (r *renderer) appendText(out *[]Segment, t *gast.Text, style lipgloss.Style, styled bool, link string) {
	seg := t.Segment
	r.push(out, r.text(seg.Value(r.src), t.IsRaw()), style, styled, link)
	switch {
	case t.HardLineBreak():
		r.push(out, "\n", style, false, "")
	case t.SoftLineBreak():
		r.push(out, " ", style, false, "")
	}
}

// text is a source span as the reader should see it: `&amp;` is an ampersand
// and `\*` is an asterisk.
//
// goldmark hands a renderer the raw bytes and expects it to resolve these,
// which the HTML renderer does on the way out. Skipping it leaves entities
// and escapes on the screen exactly as typed — glamour resolved the entities
// and left the backslashes, and both are the document not being read.
//
// Raw text is exempt: inside a code span or an HTML block the backslash and
// the ampersand are characters, not syntax.
func (r *renderer) text(src []byte, raw bool) string {
	if raw {
		return string(src)
	}
	return string(util.ResolveEntityNames(util.ResolveNumericReferences(util.UnescapePunctuations(src))))
}

// codeSpan keeps its backticks in mono and takes the accent in colour. The
// backticks are not decoration there: they are the only thing left saying the
// words inside are a name rather than prose.
func (r *renderer) codeSpan(out *[]Segment, c *gast.CodeSpan, link string) {
	var b strings.Builder
	for s := c.FirstChild(); s != nil; s = s.NextSibling() {
		if t, ok := s.(*gast.Text); ok {
			seg := t.Segment
			b.Write(seg.Value(r.src))
		}
	}
	if r.opt.Mono {
		r.push(out, "`"+b.String()+"`", lipgloss.Style{}, false, link)
		return
	}
	r.push(out, b.String(), r.sty.code, true, link)
}

// emphasis is bold at two markers and italic at one.
func (r *renderer) emphasis(out *[]Segment, e *gast.Emphasis, style lipgloss.Style, styled bool, link string) {
	mark, add := "*", r.sty.italic
	if e.Level >= 2 {
		mark, add = "**", r.sty.bold
	}
	r.mark(out, e, mark, add, style, styled, link)
}

// mark renders an inline span that mono spells with characters and colour
// spells with a treatment.
func (r *renderer) mark(out *[]Segment, n gast.Node, mark string, add lipgloss.Style, style lipgloss.Style, styled bool, link string) {
	if r.opt.Mono {
		r.push(out, mark, style, styled, link)
		r.appendInline(out, n, style, styled, link)
		r.push(out, mark, style, styled, link)
		return
	}
	ms, mok := r.merge(style, styled, add)
	r.appendInline(out, n, ms, mok, link)
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
//
// The two are compared by what they draw, not by lipgloss.Style.String, which
// is not an identity: an underlined style stringifies to "" while a coloured
// one does not, so two different treatments compared equal.
func (r *renderer) push(out *[]Segment, s string, style lipgloss.Style, styled bool, link string) {
	if s == "" {
		return
	}
	key := styleKey(style, styled)
	if n := len(*out); n > 0 {
		if prev := (*out)[n-1]; prev.Link == link && prev.Styled == styled && prev.key == key {
			(*out)[n-1].Text += s
			return
		}
	}
	*out = append(*out, Segment{Text: s, Style: style, Styled: styled, Link: link, key: key})
}

// styleKey is what a treatment draws, which is the only comparison that holds
// for every field a style can carry.
func styleKey(style lipgloss.Style, styled bool) string {
	if !styled {
		return ""
	}
	return style.Render("\x00")
}
