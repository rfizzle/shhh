package web

import (
	"bytes"
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
// style, and chrome elements, and renders headings and list items with light
// markdown markers.
func ExtractHTML(doc []byte) Extracted {
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

	var sb strings.Builder
	renderText(content, &sb, false)
	out.Text = tidyText(sb.String())
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

// renderText walks the content tree emitting readable text; pre preserves
// whitespace inside <pre> blocks.
func renderText(n *html.Node, sb *strings.Builder, pre bool) {
	switch n.Type {
	case html.TextNode:
		if pre {
			sb.WriteString(n.Data)
		} else {
			sb.WriteString(collapseSpace(n.Data))
		}
		return
	case html.ElementNode:
		name := n.Data
		if skipElements[name] {
			return
		}
		block := blockElements[name]
		if block {
			sb.WriteString("\n")
		}
		switch {
		case headingPrefix[name] != "":
			sb.WriteString(headingPrefix[name])
		case name == "li":
			sb.WriteString("- ")
		case name == "pre":
			pre = true
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			renderText(c, sb, pre)
		}
		if block {
			sb.WriteString("\n")
		}
		return
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		renderText(c, sb, pre)
	}
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
