package web

import (
	"bytes"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

// Extracted is the bounded readable view of an HTML page: title, description
// metadata, and the main content as plain text with light markdown structure.
type Extracted struct {
	Title       string
	Description string
	Text        string
}

// skipElements never contribute readable text.
var skipElements = map[string]bool{
	"script": true, "style": true, "noscript": true, "template": true,
	"svg": true, "iframe": true, "object": true, "embed": true,
	"nav": true, "header": true, "footer": true, "aside": true, "form": true,
}

// blockElements end the current line when they open or close.
var blockElements = map[string]bool{
	"p": true, "div": true, "section": true, "article": true, "main": true,
	"ul": true, "ol": true, "table": true, "tr": true, "blockquote": true,
	"pre": true, "br": true, "hr": true, "li": true, "figure": true,
	"figcaption": true, "dl": true, "dt": true, "dd": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
}

var headingPrefix = map[string]string{
	"h1": "# ", "h2": "## ", "h3": "### ", "h4": "#### ", "h5": "##### ", "h6": "###### ",
}

// ExtractHTML reduces an HTML document to its readable content. It prefers
// <main> or <article> when present (falling back to <body>), drops script,
// style, and chrome elements, and renders headings, list items, links, table
// cells and preformatted blocks with light markdown markers.
//
// pageURL is the address the document was read from, which is what a relative
// href resolves against. "" is a document with no address of its own: its
// relative links keep their text and lose their destination, because a
// half-resolved URL is worse than none — it invites a fetch that cannot work.
func ExtractHTML(doc []byte, pageURL string) Extracted {
	root, err := html.Parse(bytes.NewReader(doc))
	if err != nil {
		return Extracted{Text: ""}
	}

	var out Extracted
	if n := findElement(root, "title"); n != nil {
		out.Title = strings.TrimSpace(collapseSpace(textContent(n)))
	}
	out.Description = metaDescription(root)

	content := findElement(root, "main")
	if content == nil {
		content = findElement(root, "article")
	}
	if content == nil {
		content = findElement(root, "body")
	}
	if content == nil {
		content = root
	}

	r := renderer{}
	if base, err := url.Parse(strings.TrimSpace(pageURL)); err == nil && base.IsAbs() {
		r.base = base
	}
	r.render(content, false)
	out.Text = tidyText(r.sb.String())
	return out
}

func findElement(n *html.Node, name string) *html.Node {
	if n.Type == html.ElementNode && n.Data == name {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if found := findElement(c, name); found != nil {
			return found
		}
	}
	return nil
}

func metaDescription(root *html.Node) string {
	var found string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if found != "" {
			return
		}
		if n.Type == html.ElementNode && n.Data == "meta" {
			var name, content string
			for _, a := range n.Attr {
				switch strings.ToLower(a.Key) {
				case "name", "property":
					name = strings.ToLower(a.Val)
				case "content":
					content = a.Val
				}
			}
			if name == "description" || name == "og:description" {
				found = strings.TrimSpace(collapseSpace(content))
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return found
}

func textContent(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			sb.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return sb.String()
}

// renderer walks the content tree emitting readable text. base is the page's
// own address, held for the whole walk because a relative href is only an
// address relative to it.
type renderer struct {
	sb   strings.Builder
	base *url.URL
}

// render emits n and everything under it; pre preserves whitespace inside
// <pre> blocks.
func (r *renderer) render(n *html.Node, pre bool) {
	switch n.Type {
	case html.TextNode:
		if pre {
			r.sb.WriteString(n.Data)
		} else {
			r.write(collapseSpace(n.Data))
		}
		return
	case html.ElementNode:
		name := n.Data
		if skipElements[name] {
			return
		}
		switch name {
		case "td", "th":
			r.cell(n, pre)
			return
		case "img":
			// Alt text is the whole of what a reader can use about an
			// image, and it is what the page itself offers when the image
			// cannot be seen: a figure labelled "request lifecycle" turns a
			// paragraph that refers to nothing into one that refers to a
			// diagram. An image with no alt is decoration and writes
			// nothing.
			if alt := strings.TrimSpace(collapseSpace(attr(n, "alt"))); alt != "" {
				r.sb.WriteString("[image: " + alt + "]")
			}
			return
		case "a":
			r.link(n, pre)
			return
		}
		block := blockElements[name]
		if block {
			r.sb.WriteString("\n")
		}
		switch {
		case headingPrefix[name] != "":
			r.sb.WriteString(headingPrefix[name])
		case name == "li":
			r.sb.WriteString("- ")
		case name == "pre":
			pre = true
			// The fence is what tells the reader where the whitespace it is
			// about to read is the document's and not the extractor's; a
			// block of indented code with no boundary reads as a paragraph
			// that was formatted badly.
			r.sb.WriteString("```\n")
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			r.render(c, pre)
		}
		if name == "pre" {
			r.sb.WriteString("\n```")
		}
		if block {
			r.sb.WriteString("\n")
		}
		return
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		r.render(c, pre)
	}
}

// link emits an anchor as `text (url)`.
//
// A documentation index is a list of link texts, and without the addresses it
// is a list of labels: the reader's next move is to guess a URL from a label
// or to fetch the index a second time and guess again. The destination is
// written beside the text rather than as a footnote because the two are read
// together and a numbered reference at the end of a 16 KB slice is a lookup
// into bytes that may have been cut.
// See docs/capabilities/evidence.md#a-page-is-kept-whole.
func (r *renderer) link(n *html.Node, pre bool) {
	// Inside a <pre> the text is the document's own bytes, and an address
	// written into them is a claim about what the page said. The parser puts
	// an anchor there more often than any author does: <pre> closes an open
	// <p>, so `<p><a href=…><pre>code</pre></a></p>` — a linked code sample,
	// an ordinary shape — is reconstructed as an anchor *inside* the
	// preformatted block, and the destination would land in the middle of
	// the fence. Losing the address is the smaller loss.
	if pre {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			r.render(c, pre)
		}
		return
	}
	// The link's own text is rendered on its own so the destination can go
	// between the words and the space that separated them: appending after
	// the boundary space of `<a>\n  Foo\n</a>` would leave `Foo  (url)`.
	inner := renderer{base: r.base}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		inner.render(c, pre)
	}
	text := inner.sb.String()
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		r.sb.WriteString(text)
		return
	}
	lead := text[:len(text)-len(strings.TrimLeft(text, " \n\t"))]
	trail := text[len(strings.TrimRight(text, " \n\t")):]
	r.write(lead)
	r.sb.WriteString(trimmed)
	if target := r.linkTarget(n, trimmed); target != "" {
		r.sb.WriteString(" (" + target + ")")
	}
	r.sb.WriteString(trail)
}

// linkTarget is the address an anchor points at, or "" for one worth no
// bytes: a fragment, which addresses the page already in hand; a scheme no
// fetch can follow (javascript:, mailto:, data:); a relative href on a
// document with no address to resolve it against; and a link whose text is
// already its own URL, which is the commonest line in a link list and would
// otherwise be written twice.
func (r *renderer) linkTarget(n *html.Node, text string) string {
	href := strings.TrimSpace(attr(n, "href"))
	if href == "" || strings.HasPrefix(href, "#") {
		return ""
	}
	target, err := url.Parse(href)
	if err != nil {
		return ""
	}
	if !target.IsAbs() {
		if r.base == nil {
			return ""
		}
		target = r.base.ResolveReference(target)
	}
	if target.Scheme != "http" && target.Scheme != "https" {
		return ""
	}
	if s := target.String(); s != text {
		return s
	}
	return ""
}

// cell emits one table cell onto the row's line.
//
// Its content is rendered on its own and its line breaks folded, because a
// cell that wraps its text in a <p> or a <div> would otherwise end the row:
// a three-column table would arrive as nine lines with the separators
// stranded on some of them. Folding is what makes the separator mean
// something — without it a parameter table reads as `namestringthe thing`,
// and with a half-applied one it reads worse.
func (r *renderer) cell(n *html.Node, pre bool) {
	// The first cell takes no separator: <tr> already opened the line.
	if hasPreviousCell(n) {
		r.sb.WriteString(" | ")
	}
	inner := renderer{base: r.base}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		inner.render(c, pre)
	}
	r.sb.WriteString(strings.TrimSpace(collapseSpace(inner.sb.String())))
}

// write appends collapsed text, dropping a leading space where what is
// already written ends in one. Every inline element's text is collapsed on
// its own — `a <b> b </b> c` is three collapses, each keeping the boundary
// space it was handed — so without this the spaces around an emphasis or a
// link are written twice, and a line that starts one begins with an indent
// nothing in the document asked for.
func (r *renderer) write(s string) {
	if strings.HasPrefix(s, " ") && r.endsInSpace() {
		s = strings.TrimLeft(s, " ")
	}
	r.sb.WriteString(s)
}

// endsInSpace reports whether the text so far ends on whitespace. An empty
// builder does not: it may be a link's own text, whose leading space is the
// one that separates it from the words before it.
func (r *renderer) endsInSpace() bool {
	s := r.sb.String()
	if s == "" {
		return false
	}
	switch s[len(s)-1] {
	case ' ', '\n', '\t':
		return true
	}
	return false
}

// hasPreviousCell reports whether a cell has one before it in its row.
func hasPreviousCell(n *html.Node) bool {
	for p := n.PrevSibling; p != nil; p = p.PrevSibling {
		if p.Type == html.ElementNode && (p.Data == "td" || p.Data == "th") {
			return true
		}
	}
	return false
}

// attr is an element's attribute by name, matched the way HTML does it.
func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}

// collapseSpace folds runs of whitespace into single spaces (HTML rendering
// semantics outside <pre>), preserving one boundary space so adjacent inline
// nodes stay separated; tidyText cleans up line edges afterwards.
func collapseSpace(s string) string {
	var sb strings.Builder
	space := false
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			space = true
			continue
		}
		if space {
			sb.WriteByte(' ')
		}
		space = false
		sb.WriteRune(r)
	}
	if space {
		sb.WriteByte(' ')
	}
	return sb.String()
}

// tidyText trims trailing space per line and folds runs of blank lines.
func tidyText(s string) string {
	lines := strings.Split(s, "\n")
	var out []string
	blanks := 0
	for _, line := range lines {
		line = strings.TrimRight(line, " \t")
		if strings.TrimSpace(line) == "" {
			blanks++
			if blanks > 1 {
				continue
			}
			out = append(out, "")
			continue
		}
		blanks = 0
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}
