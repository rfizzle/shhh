package chat

import (
	"path/filepath"
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/glamour"
	"github.com/rfizzle/shhh/internal/ui/components"
)

var (
	rendererMu     sync.Mutex
	cachedWidth    int
	cachedMono     bool
	cachedRenderer *glamour.TermRenderer
)

// markdownStyle is the glamour style the transcript renders assistant prose
// with. Mono mode (S-095) swaps the coloured "dark" theme for "ascii", which
// marks emphasis, headings and code with characters instead of colour — the
// invariant applied to prose: the ** stays when the colour goes.
func markdownStyle() string {
	if components.Mono() {
		return "ascii"
	}
	return "dark"
}

func getRenderer(width int) *glamour.TermRenderer {
	rendererMu.Lock()
	defer rendererMu.Unlock()
	mono := components.Mono()
	if cachedRenderer != nil && cachedWidth == width && cachedMono == mono {
		return cachedRenderer
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStylePath(markdownStyle()),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return nil
	}
	cachedRenderer = r
	cachedWidth = width
	cachedMono = mono
	return r
}

func renderMarkdown(text string, width int) string {
	if width <= 0 {
		width = 80
	}
	r := getRenderer(width)
	if r == nil {
		return text
	}
	out, err := r.Render(text)
	if err != nil {
		return text
	}
	return strings.TrimSpace(out)
}

// diffSyntaxStyle is the chroma style diff views highlight with; the diff
// renderer layers add/remove coloring over it (S-074, DESIGN-TUI.md §3b).
const diffSyntaxStyle = "monokai"

// diffSyntax returns a per-line syntax highlighter for path, or nil when no
// lexer matches (the diff then renders with plain diff colors).
func diffSyntax(path string) components.Syntax {
	// Chroma's colours are not the product's palette, so mono mode declines
	// highlighting outright and the diff renders in its plain +/- styling
	// (S-095).
	if components.Mono() {
		return nil
	}
	lexer := lexers.Match(filepath.Base(path))
	if lexer == nil {
		return nil
	}
	lexer = chroma.Coalesce(lexer)
	style := styles.Get(diffSyntaxStyle)
	return func(line string) []components.Segment {
		it, err := lexer.Tokenise(nil, line)
		if err != nil {
			return nil
		}
		var segs []components.Segment
		for _, tok := range it.Tokens() {
			color := ""
			if entry := style.Get(tok.Type); entry.Colour.IsSet() {
				color = entry.Colour.String()
			}
			segs = append(segs, components.Segment{Text: tok.Value, Color: color})
		}
		// Lexers append the trailing newline they require; the renderer works
		// on bare lines, so trim it to reconstruct the input exactly.
		if n := len(segs) - 1; n >= 0 {
			segs[n].Text = strings.TrimSuffix(segs[n].Text, "\n")
			if segs[n].Text == "" {
				segs = segs[:n]
			}
		}
		return segs
	}
}
