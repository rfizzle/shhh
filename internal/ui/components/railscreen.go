package components

// The rail in full (docs/interface/surfaces.md#the-inspector-rail): what the
// rail's foot line opens once a short terminal has dropped blocks from it,
// and what `/rail` opens at any height. It is the rail itself with nothing
// shed — every block, every row its presets keep — read through a pager in
// the take-over family's chrome, so a terminal of any height can read what
// the rail had to leave out, and nothing on the rail itself ever takes the
// keyboard to show it.

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// RailScreen is `/rail`: a take-over, full width, owning the keyboard while
// it is up, and changing nothing.
type RailScreen struct {
	// Rail is the rail as the session reads it now. The host hands it over
	// at every paint, so the screen is the live rail and not a snapshot.
	Rail InspectorRail
	// offset is the first body row the pane shows, held inside the body by
	// every render.
	offset int
	// showKeys is whether `?` has swapped the key row for the register.
	showKeys bool
	// maxLines bounds the screen height. 0 is unbounded.
	maxLines int
}

// Update is the screen's keyboard: it scrolls, shows its keys, and leaves.
func (s *RailScreen) Update(msg tea.KeyPressMsg) (done bool) {
	switch pressed := msg.String(); {
	case keys.Is(pressed, keys.Screen.Quit):
		return true
	case keys.Is(pressed, keys.Screen.List):
		s.showKeys = !s.showKeys
	case keys.Is(pressed, keys.Screen.Move):
		s.offset = max(s.offset+keys.Step(pressed, keys.Screen.Move), 0)
	}
	return false
}

// SetSize gives the screen the terminal's rectangle. It lays itself out from
// the width it is rendered at, so only the height is kept.
func (s *RailScreen) SetSize(_, height int) { s.maxLines = height }

// View renders the screen through the family's chrome. The rail is drawn at
// its own widest rather than the pane's: its rows are laid out for the rail's
// range, and a heading whose figure stood sixty columns from its label would
// be two things rather than one row.
func (s *RailScreen) View(width int) string {
	if width <= 0 {
		return ""
	}
	railWidth := min(width, InspectorMaxWidth)
	rows, heads, names := s.rows(railWidth)
	chrome := screenChrome{header: s.header(len(names)), maxLines: s.maxLines}
	chrome.foot = s.footer(false).rows(width)
	if s.maxLines > 0 && len(rows) > s.maxLines-3-1-len(chrome.foot) {
		chrome.foot = s.footer(true).rows(width)
	}
	return chrome.view(width, func(budget int) []string { return s.window(rows, heads, names, railWidth, budget) })
}

// rows is the whole rail unshed, and the row each block's heading is on with
// its name, which is what the window's two ends name.
func (s *RailScreen) rows(width int) (out []string, heads []int, names []string) {
	out = s.Rail.Lines(width, 0)
	at := 0
	for _, b := range s.Rail.blocks(width) {
		names = append(names, strings.ToLower(b.name))
		heads = append(heads, at)
		at += b.height() + 1
	}
	return out, heads, names
}

// window is the run of rail rows the pane shows. The two ends say what they
// sit on by the blocks' names, so a rail longer than the pane is still read
// whole (docs/interface/principles.md#fold-never-hide).
func (s *RailScreen) window(rows []string, heads []int, names []string, width, budget int) []string {
	if budget <= 0 || len(rows) <= budget {
		s.offset = 0
		return rows
	}
	height := budget - 1
	if (Pager{Offset: s.offset, Height: height - 1, total: len(rows)}).Held() > 0 {
		height--
	}
	p := Pager{Offset: s.offset, Height: max(height, 1)}
	shown := p.Window(rows)
	s.offset = p.Offset
	var out []string
	if p.above() > 0 {
		out = append(out, railScreenMarker("↑", heads, names, 0, p.Offset, width))
	}
	out = append(out, shown...)
	if p.below() > 0 {
		out = append(out, railScreenMarker("↓", heads, names, p.Offset+len(shown), len(rows), width))
	}
	return out
}

// railScreenMarker is one end of the window: the blocks whose headings sit in
// the hidden rows [from, to), named, or how many rows of a block the window
// cut into where no heading is hidden.
func railScreenMarker(arrow string, heads []int, names []string, from, to, width int) string {
	var hidden []string
	for i, at := range heads {
		if at >= from && at < to {
			hidden = append(hidden, names[i])
		}
	}
	if len(hidden) == 0 {
		n := to - from
		return sty.dim.Render(Clip(fmt.Sprintf("%s %d more %s", arrow, n, plainPlural(n, "row", "rows")), width))
	}
	return listOverflowRow(arrow, len(hidden), strings.Join(hidden, " · "), width)
}

// header names the surface and how many blocks it holds.
func (s *RailScreen) header(blocks int) screenHeader {
	h := screenHeader{left: []RailSegment{screenTitle("/rail")}, keys: screenBackKeys()}
	if s.showKeys {
		h.keys = keys.Bracket(keys.Screen.List) + " hide the keys · " + words(keys.Screen.Quit, "back")
	}
	if blocks > 0 {
		h.left = append(h.left, screenField("the rail in full · "+plainPlural(blocks, "1 block", fmt.Sprintf("%d blocks", blocks))))
	}
	return h
}

// footer is the keys the screen offers and the field that says what it is.
func (s *RailScreen) footer(scrolls bool) keyFooter {
	var offers []KeyOffer
	if scrolls {
		offers = append(offers, keyOfferAs(keys.Screen.Move, "scroll"))
	}
	offers = append(offers, wayOut(backToPrompt))
	return keyFooter{
		offers:  offers,
		field:   "a reading · every block, nothing shed",
		showing: s.showKeys,
		register: []KeyOffer{
			keyOfferAs(keys.Screen.Move, "scroll the rail"),
			wayOut(backToPrompt),
			keyOfferAs(keys.Screen.Quit, backToPrompt),
		},
	}
}
