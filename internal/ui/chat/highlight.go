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
	cachedRenderer *glamour.TermRenderer
)

func getRenderer(width int) *glamour.TermRenderer {
	rendererMu.Lock()
	defer rendererMu.Unlock()
	if cachedRenderer != nil && cachedWidth == width {
		return cachedRenderer
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStylePath("dark"),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return nil
	}
	cachedRenderer = r
	cachedWidth = width
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
