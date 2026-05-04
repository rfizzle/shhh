package chat

import (
	"strings"
	"sync"

	"github.com/charmbracelet/glamour"
)

var (
	rendererMu    sync.Mutex
	cachedWidth   int
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
