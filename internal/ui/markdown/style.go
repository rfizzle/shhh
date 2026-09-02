package markdown

import (
	"charm.land/lipgloss/v2"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// The prose register.
//
// Every tone here is a palette token, which is the point: the transcript's
// prose was the last thing in the interface picking its own colours, and a
// heading that is not the same colour as every other heading in the product
// is a second design system running beside the first.
//
// Mono resolves to no styles at all rather than to grey ones. A render with
// no escapes in it is the honest answer to a terminal that was told not to
// use colour, it is what the mono goldens already record, and it means the
// marks the inline renderer puts back are carrying the whole distinction.
type styles struct {
	mono    bool
	body    lipgloss.Style
	heading lipgloss.Style
	bold    lipgloss.Style
	italic  lipgloss.Style
	strike  lipgloss.Style
	code    lipgloss.Style
	link    lipgloss.Style
	url     lipgloss.Style
	rule    lipgloss.Style
	marker  lipgloss.Style
	faint   lipgloss.Style
}

func newStyles(mono bool) styles {
	if mono {
		return styles{mono: true}
	}
	p := components.Palette
	return styles{
		body:    lipgloss.NewStyle().Foreground(p.Body.Color()),
		heading: lipgloss.NewStyle().Bold(true).Foreground(p.Bright.Color()),
		bold:    lipgloss.NewStyle().Bold(true).Foreground(p.Bright.Color()),
		italic:  lipgloss.NewStyle().Italic(true),
		strike:  lipgloss.NewStyle().Strikethrough(true).Foreground(p.Dim.Color()),
		code:    lipgloss.NewStyle().Foreground(p.Accent.Color()),
		link:    lipgloss.NewStyle().Underline(true).Foreground(p.Info.Color()),
		url:     lipgloss.NewStyle().Foreground(p.Dim.Color()),
		rule:    lipgloss.NewStyle().Foreground(p.Dim.Color()),
		marker:  lipgloss.NewStyle().Foreground(p.Info.Color()),
		faint:   lipgloss.NewStyle().Foreground(p.Dimmer.Color()),
	}
}
