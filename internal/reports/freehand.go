package reports

import (
	_ "embed"
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

//go:embed report.css
var reportCSS string

// tokenNames is every var(--name) the token file defines — the whole color
// vocabulary freehand markup may speak. Parsed from the embedded report.css
// so the validator and the stylesheet can never disagree.
var tokenNames = func() map[string]bool {
	names := map[string]bool{}
	for _, m := range regexp.MustCompile(`--([a-z0-9-]+)\s*:`).FindAllStringSubmatch(reportCSS, -1) {
		names[m[1]] = true
	}
	return names
}()

// The freehand grammar: enough HTML to write prose and tables, enough SVG to
// draw. Everything else — scripts, styles, frames, images, links, foreign
// objects — is rejected by name. The page's CSP is the backstop; this
// validator is the front door.
var (
	allowedHTML = map[string]bool{
		"div": true, "span": true, "p": true, "h1": true, "h2": true,
		"h3": true, "h4": true, "ul": true, "ol": true, "li": true,
		"table": true, "thead": true, "tbody": true, "tr": true,
		"th": true, "td": true, "strong": true, "em": true,
		"code": true, "pre": true, "br": true, "hr": true,
	}
	allowedSVG = map[string]bool{
		"svg": true, "g": true, "rect": true, "circle": true,
		"ellipse": true, "line": true, "path": true, "polyline": true,
		"polygon": true, "text": true, "tspan": true, "title": true,
	}

	// Attributes whose values are geometry or plain identifiers; they still
	// pass the forbidden-substring check but not the token grammar.
	plainAttrs = map[string]bool{
		"class": true, "viewbox": true, "d": true, "points": true,
		"x": true, "y": true, "x1": true, "y1": true, "x2": true, "y2": true,
		"cx": true, "cy": true, "r": true, "rx": true, "ry": true,
		"dx": true, "dy": true, "width": true, "height": true,
		"transform": true, "opacity": true, "fill-opacity": true,
		"stroke-opacity": true, "stroke-width": true,
		"stroke-dasharray": true, "stroke-linecap": true,
		"stroke-linejoin": true, "font-size": true, "font-weight": true,
		"text-anchor": true, "dominant-baseline": true, "colspan": true,
		"rowspan": true, "preserveaspectratio": true, "role": true,
		"aria-label": true,
	}
	// Attributes whose value must be a color: var(--token) or a neutral
	// keyword, nothing literal.
	colorAttrs = map[string]bool{"fill": true, "stroke": true, "stop-color": true, "color": true}

	colorKeywords = map[string]bool{"none": true, "transparent": true, "currentcolor": true, "inherit": true}

	varRe        = regexp.MustCompile(`^var\(--([a-z0-9-]+)\)$`)
	hexRe        = regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b`)
	numberRe     = regexp.MustCompile(`^-?[0-9.]+(px|em|ch|%|fr|vw|vh)?$`)
	styleFuncRe  = regexp.MustCompile(`(rgb|rgba|hsl|hsla|oklch|oklab|color|url|expression|image-set|attr)\s*\(`)
	styleValueOK = map[string]bool{
		// layout and text keywords a report section legitimately reaches for;
		// anything not listed is rejected by name so the author can rewrite.
		"auto": true, "none": true, "block": true, "inline": true,
		"inline-block": true, "flex": true, "grid": true, "row": true,
		"column": true, "wrap": true, "nowrap": true, "center": true,
		"left": true, "right": true, "justify": true, "start": true,
		"end": true, "flex-start": true, "flex-end": true,
		"space-between": true, "space-around": true, "stretch": true,
		"baseline": true, "bold": true, "normal": true, "italic": true,
		"uppercase": true, "lowercase": true, "capitalize": true,
		"underline": true, "solid": true, "dashed": true, "dotted": true,
		"collapse": true, "separate": true, "hidden": true, "visible": true,
		"relative": true, "static": true, "middle": true, "top": true,
		"bottom": true, "tabular-nums": true, "monospace": true,
		"break-word": true, "break-all": true, "anywhere": true,
		"pre": true, "pre-wrap": true, "inherit": true, "transparent": true,
		"currentcolor": true,
	}
)

// ValidateFreehand checks one freehand fragment against the grammar and
// returns its canonical serialization — the exact bytes that are stored and
// replayed, so what was checked is what ships. The first violation is
// returned verbatim; the tool result is the model's only feedback channel.
func ValidateFreehand(fragment string) (string, error) {
	ctx := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	nodes, err := html.ParseFragment(strings.NewReader(fragment), ctx)
	if err != nil {
		return "", fmt.Errorf("freehand rejected: unparseable markup: %w", err)
	}
	var out strings.Builder
	for _, n := range nodes {
		if err := checkNode(n); err != nil {
			return "", err
		}
		if n == nil {
			continue
		}
		if err := html.Render(&out, n); err != nil {
			return "", fmt.Errorf("freehand rejected: %w", err)
		}
	}
	return out.String(), nil
}

// checkNode walks one tree, dropping comments and rejecting the first
// violation it meets.
func checkNode(n *html.Node) error {
	switch n.Type {
	case html.CommentNode:
		detach(n)
		return nil
	case html.TextNode:
		return nil
	case html.ElementNode:
		name := strings.ToLower(n.Data)
		if n.Namespace == "svg" {
			if !allowedSVG[name] {
				return fmt.Errorf("freehand rejected: <%s> is not allowed", n.Data)
			}
		} else if !allowedHTML[name] {
			return fmt.Errorf("freehand rejected: <%s> is not allowed", n.Data)
		}
		for _, a := range n.Attr {
			if err := checkAttr(n.Data, a); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("freehand rejected: unsupported node")
	}
	// Children, with detach-safe iteration for dropped comments.
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		if err := checkNode(c); err != nil {
			return err
		}
		c = next
	}
	return nil
}

func detach(n *html.Node) {
	if n.Parent != nil {
		n.Parent.RemoveChild(n)
	}
}

func checkAttr(element string, a html.Attribute) error {
	key := strings.ToLower(a.Key)
	if strings.HasPrefix(key, "on") {
		return fmt.Errorf("freehand rejected: event handler %q is not allowed", a.Key)
	}
	if hexRe.MatchString(a.Val) {
		return fmt.Errorf("freehand rejected: literal color %q on <%s> — use var(--token) from report.css", hexRe.FindString(a.Val), element)
	}
	switch {
	case colorAttrs[key]:
		return checkColorValue(element, a.Key, a.Val)
	case key == "style":
		return checkStyle(element, a.Val)
	case plainAttrs[key]:
		if styleFuncRe.MatchString(strings.ToLower(a.Val)) || strings.Contains(a.Val, "//") {
			return fmt.Errorf("freehand rejected: %q in %s=%q is not allowed", styleFuncRe.FindString(strings.ToLower(a.Val)), a.Key, a.Val)
		}
		return nil
	default:
		return fmt.Errorf("freehand rejected: attribute %q on <%s> is not allowed", a.Key, element)
	}
}

// checkColorValue admits exactly var(--token) for a token report.css defines,
// or a neutral keyword.
func checkColorValue(element, attr, val string) error {
	v := strings.TrimSpace(strings.ToLower(val))
	if colorKeywords[v] {
		return nil
	}
	if m := varRe.FindStringSubmatch(v); m != nil {
		if !tokenNames[m[1]] {
			return fmt.Errorf("freehand rejected: unknown token var(--%s) in %s on <%s>", m[1], attr, element)
		}
		return nil
	}
	return fmt.Errorf("freehand rejected: %s=%q on <%s> — colors are var(--token) from report.css", attr, val, element)
}

// checkStyle walks the declarations of a style attribute. Every value token
// must be a var() of a known token, a number or length, or a listed layout
// keyword; a literal color has no way in.
func checkStyle(element, style string) error {
	for _, decl := range strings.Split(style, ";") {
		decl = strings.TrimSpace(decl)
		if decl == "" {
			continue
		}
		prop, val, ok := strings.Cut(decl, ":")
		if !ok {
			return fmt.Errorf("freehand rejected: malformed style %q on <%s>", decl, element)
		}
		val = strings.ToLower(strings.TrimSpace(val))
		if m := styleFuncRe.FindString(val); m != "" {
			return fmt.Errorf("freehand rejected: %q in style of <%s> is not allowed", strings.TrimSuffix(m, "("), element)
		}
		for _, tok := range strings.Fields(strings.ReplaceAll(val, ",", " ")) {
			if err := checkStyleToken(element, strings.TrimSpace(prop), tok); err != nil {
				return err
			}
		}
	}
	return nil
}

func checkStyleToken(element, prop, tok string) error {
	if m := varRe.FindStringSubmatch(tok); m != nil {
		if !tokenNames[m[1]] {
			return fmt.Errorf("freehand rejected: unknown token var(--%s) in style of <%s>", m[1], element)
		}
		return nil
	}
	if numberRe.MatchString(tok) || styleValueOK[tok] {
		return nil
	}
	if strings.HasPrefix(tok, "calc(") || strings.Contains(tok, "var(") {
		return fmt.Errorf("freehand rejected: %q in style of <%s> — write var(--token) as the whole value", tok, element)
	}
	return fmt.Errorf("freehand rejected: %q in %s of <%s> is not a token, length or listed keyword", tok, prop, element)
}
