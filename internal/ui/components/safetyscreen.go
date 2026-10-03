package components

// The safety screen (docs/interface/surfaces.md#the-safety-reading): the
// session's whole boundary on one page — what it may do without asking, where
// it may write, what contains its commands, which hosts it reaches, what the
// checkout was let load, which servers and secrets it holds, and which tools
// it has in which tier.
//
// Every fact on it belongs to another command, and the screen is built so it
// cannot become a second place to change one: it has no key that writes, and
// each section ends by naming the command that owns what it just said. It is
// a passive component like the rest of this package. The host supplies every
// line in the owning command's own words, because what a grant covers and
// what a mechanism masks are readings of the session, and this is a renderer.
// See docs/capabilities/approvals-and-safety.md#one-reading-of-the-boundary.

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// safetyIndent is where a section's lines start under its heading, and
// safetyHang how much further a line that did not fit continues.
const (
	safetyIndent = 2
	safetyHang   = 2
)

// SafetySection is one fact of the boundary, as the command that owns it
// states it.
type SafetySection struct {
	// Title is the section's name in the surface's own words — `where it may
	// write`, not the owning command's.
	Title string
	// Lines are the owning command's reading, one line each. A line longer
	// than the screen wraps under itself rather than being cut, because the
	// tail of a line here is as often the path or the host the reader came
	// for as it is decoration.
	Lines []string
	// ChangedBy is the command that changes what the section says, and the
	// one place to go and do it.
	ChangedBy string
	// Absent is a capability this session does not have. Its lines say so
	// in words and are drawn dim, so a section that has nothing in force is
	// still on the page rather than missing from it — a boundary read by
	// what is not listed is one read wrong.
	Absent bool
}

// safetyResult is how the screen closed. It only ever closes: nothing on it
// decides anything.
type safetyResult struct{ canceled bool }

// SafetyScreen is `/safety`: a takeover in the chat, full width, owning the
// keyboard for as long as it is up, and changing nothing.
type SafetyScreen struct {
	// Sections are the boundary in the host's order.
	Sections []SafetySection
	// Subject is what the header says the reading is of — `manual ·
	// sandbox-exec`. The host words it.
	Subject string
	// offset is the first body row the pane shows. It is held inside the
	// body by every render, so a key may overshoot it.
	offset int
	// showKeys is whether `?` has swapped the key row for the register.
	showKeys bool
	// maxLines bounds the screen height. 0 is unbounded.
	maxLines int
}

// Update is the screen's keyboard: it scrolls, shows its keys, and leaves.
// There is no fourth key, because every other key a screen in this family
// has changes something and this one is a reading.
func (s *SafetyScreen) Update(msg tea.KeyPressMsg) (done bool, result safetyResult) {
	switch pressed := msg.String(); {
	case keys.Is(pressed, keys.Screen.Quit):
		return true, safetyResult{canceled: true}
	case keys.Is(pressed, keys.Screen.List):
		s.showKeys = !s.showKeys
	case keys.Is(pressed, keys.Screen.Move):
		s.offset = max(s.offset+keys.Step(pressed, keys.Screen.Move), 0)
	}
	return false, safetyResult{}
}

// SetSize gives the screen the terminal's rectangle. It lays itself out from
// the width it is rendered at, so only the height is kept.
func (s *SafetyScreen) SetSize(_, height int) { s.maxLines = height }

// View renders the screen through the family's chrome.
func (s *SafetyScreen) View(width int) string {
	if width <= 0 {
		return ""
	}
	rows, heads := s.rows(width)
	// Whether the body scrolls is decided before the footer is drawn, since
	// a key to scroll a body that fits is not an offer (invariant 5). It is
	// asked against the rows the chrome would leave under the footer that
	// offers no scroll: the offer only ever takes rows away, so a body that
	// overflows that one overflows the other. The three are the header, its
	// rule and the blank under it; the one is the blank above the keys.
	chrome := screenChrome{header: s.header(), maxLines: s.maxLines}
	chrome.foot = s.footer(false).rows(width)
	if s.maxLines > 0 && len(rows) > s.maxLines-3-1-len(chrome.foot) {
		chrome.foot = s.footer(true).rows(width)
	}
	return chrome.view(width, func(budget int) []string { return s.window(rows, heads, width, budget) })
}

// rows is the whole body, every section drawn, before any of it is windowed,
// and the row each section's heading landed on — which is what the window's
// two ends name.
func (s *SafetyScreen) rows(width int) (out []string, heads []int) {
	for i, sec := range s.Sections {
		if i > 0 {
			out = append(out, "")
		}
		heads = append(heads, len(out))
		out = append(out, sty.status.Render(Clip(strings.ToUpper(sec.Title), width)))
		tone := sty.body
		if sec.Absent {
			tone = sty.dim
		}
		for _, line := range sec.Lines {
			for _, part := range safetyWrap(line, width) {
				out = append(out, tone.Render(part))
			}
		}
		if sec.ChangedBy != "" {
			out = append(out, safetyChangedBy(sec.ChangedBy, width)...)
		}
	}
	return out, heads
}

// safetyChangedBy is the line a section ends on: the one command that owns
// what the section said. It is the section's last line rather than its
// heading's right-hand half because a heading is dropped to the width before
// a sentence is, and this is the sentence that keeps the screen from being a
// second place to edit anything.
func safetyChangedBy(owner string, width int) []string {
	var out []string
	for _, part := range safetyWrap("changed with "+owner, width) {
		out = append(out, sty.dim.Render(part))
	}
	return out
}

// safetyWrap lays one of the owner's lines into the screen's width under the
// section heading. The line's own leading spaces are kept — the owners align
// their columns with them — and what did not fit continues a hang further
// in, so a wrapped line reads as the continuation it is.
func safetyWrap(line string, width int) []string {
	body := strings.TrimLeft(line, " ")
	lead := strings.Repeat(" ", safetyIndent+len(line)-len(body))
	room := max(width-lipgloss.Width(lead)-safetyHang, 1)
	if body == "" {
		return []string{""}
	}
	parts := strings.Split(lipgloss.Wrap(body, room, " "), "\n")
	out := make([]string, 0, len(parts))
	for i, part := range parts {
		if i == 0 {
			out = append(out, lead+part)
			continue
		}
		out = append(out, lead+strings.Repeat(" ", safetyHang)+strings.TrimLeft(part, " "))
	}
	return out
}

// window is the run of body rows the pane shows. A body longer than its pane
// is read through a pager rather than cut down to the sections that fit: every
// section here is part of one answer, and a boundary with its last three
// sections folded away is the boundary misstated. The two ends say what they
// are sitting on by the sections' names, so a fold is still an answer
// (docs/interface/principles.md#fold-never-hide).
func (s *SafetyScreen) window(rows []string, heads []int, width, budget int) []string {
	if budget <= 0 || len(rows) <= budget {
		s.offset = 0
		return rows
	}
	// The markers take rows of their own, so the pane is the budget less
	// whichever of them will be drawn. The top one is decided by the offset
	// asked for, held against the smallest pane it could be.
	height := budget - 1
	if (Pager{Offset: s.offset, Height: height - 1, total: len(rows)}).Held() > 0 {
		height--
	}
	p := Pager{Offset: s.offset, Height: max(height, 1)}
	shown := p.Window(rows)
	s.offset = p.Offset
	var out []string
	if p.above() > 0 {
		out = append(out, s.marker("↑", heads, 0, p.Offset, width))
	}
	out = append(out, shown...)
	if p.below() > 0 {
		out = append(out, s.marker("↓", heads, p.Offset+len(shown), len(rows), width))
	}
	return out
}

// marker is one end of the window: the sections whose headings sit in the
// hidden rows [from, to), named, or how many rows of the section the window
// cut into where no heading is hidden.
func (s *SafetyScreen) marker(arrow string, heads []int, from, to, width int) string {
	var names []string
	for i, at := range heads {
		if at >= from && at < to {
			names = append(names, s.Sections[i].Title)
		}
	}
	if len(names) == 0 {
		n := to - from
		return sty.dim.Render(Clip(fmt.Sprintf("%s %d more %s", arrow, n, plainPlural(n, "row", "rows")), width))
	}
	return listOverflowRow(arrow, len(names), strings.Join(names, " · "), width)
}

// plainPlural picks a noun's spelling for a count.
func plainPlural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// header names the surface and what it is a reading of.
func (s *SafetyScreen) header() screenHeader {
	h := screenHeader{left: []RailSegment{screenTitle("/safety")}, keys: screenBackKeys()}
	if s.showKeys {
		h.keys = keys.Bracket(keys.Screen.List) + " hide the keys · " + words(keys.Screen.Quit, "back")
	}
	if s.Subject != "" {
		h.left = append(h.left, screenField(s.Subject))
	}
	return h
}

// footer is the keys the screen offers and the field that says what it is.
// The field is the promise the screen exists to keep: it reads, and each
// section names where the fact it states is changed.
func (s *SafetyScreen) footer(scrolls bool) keyFooter {
	var offers []KeyOffer
	if scrolls {
		offers = append(offers, keyOfferAs(keys.Screen.Move, "scroll"))
	}
	offers = append(offers, wayOut(backToPrompt))
	return keyFooter{
		offers:  offers,
		field:   "a reading · nothing here changes it",
		showing: s.showKeys,
		register: []KeyOffer{
			keyOfferAs(keys.Screen.Move, "scroll the reading"),
			wayOut(backToPrompt),
			keyOfferAs(keys.Screen.Quit, backToPrompt),
		},
	}
}
