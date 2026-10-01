package components

// The tray (docs/interface/surfaces.md#the-input-frame): what a sent message
// carried, one row per attachment, on one flush band under the words.
//
// The tray is the chip the attachment was, kept: the handle spelled the way
// the sentence spells it, what it is called, the figures it is counted by and
// its size. Every attachment the message carried has a row, whether its fold
// is in the words or not, because a row is the way back to what was sent.
//
// No key is printed on a row. The rows sit above a live draft, where a key
// written on them would be an offer nothing accepts; reading mode's own bar
// says what enter does on the row under its cursor
// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
// The one key the tray draws is the opened paste's count, and it is drawn in
// the hint grey for the same reason.

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// TrayHandleSlot is the columns the handle takes on a tray row: wide enough
// for `⟨▣ Image#12⟩` and one cell of air, so the names on a tray start in one
// column and the handle in the sentence and the handle on its row line up by
// eye.
const TrayHandleSlot = 13

// trayMargin is the run held back at the band's right end, so the size does
// not sit against the edge of the pane.
const trayMargin = 2

// TrayBodyIndent is where an opened paste's lines start inside the tray: the
// detail column every body in the transcript is set in.
const TrayBodyIndent = GridDetailIndent

// trayMark is the kind's mark on a tray row. A paste is `¶` here where the
// staged chip draws `≡`, because the transcript's summary row already wears
// `≡` and a tray row under a message is read beside it; the strip above the
// draft has no summary row to be confused with.
func (k ChipKind) trayMark() string {
	if k == ChipText {
		return "¶"
	}
	return k.mark()
}

// TrayHandle is the handle as a tray row spells it: the angle quotes the
// sentence's fold wears, with the kind's mark inside them. An attachment with
// no handle — only a message saved before handles existed carries one — is
// its mark alone.
func TrayHandle(k ChipKind, handle string) string {
	if handle == "" {
		return string(PasteFoldOpen) + k.trayMark() + string(PasteFoldClose)
	}
	return string(PasteFoldOpen) + k.trayMark() + " " + handle + string(PasteFoldClose)
}

// TrayRow is one attachment a sent message carried, as its row draws it.
type TrayRow struct {
	Kind   ChipKind
	Handle string
	Name   string
	// Facts are the figures after the name, dim: the picture's dimensions
	// and where it came from, a paste's lines and tokens, a document's pages.
	// They are given up from the end as the pane narrows, so the one a row
	// can best spare goes last in the list.
	Facts []string
	// Size is the bytes, at the right end. It is the one figure every
	// attachment has, and it is never given up.
	Size string
	// Lit is the row under the reading cursor, which the caller lights
	// (LitRow). It is drawn without the band and with the handle bright, so
	// the focus ground runs the whole row rather than stopping where the
	// handle's own ground was, and the handle reads as part of the lit row
	// rather than as a mark kept in its colour.
	Lit bool
}

// View draws the row at the pane's width: the handle in the reader's colour
// in its slot, the name bright, the facts dim, and the size at the right,
// all on the band.
//
// What does not fit is given up in an order and the handle never is: the
// facts from the last, then the name is clipped. Three pictures can all be
// called clipboard.png, and the handle is the one thing that tells them
// apart and opens one by name.
func (r TrayRow) View(width int) string {
	handle := TrayHandle(r.Kind, r.Handle)
	slot := max(TrayHandleSlot, lipgloss.Width(handle)+1)
	lead := strings.Repeat(" ", GridPointerWidth)
	right := " " + r.Size + strings.Repeat(" ", trayMargin)
	room := width - GridPointerWidth - slot - lipgloss.Width(right)
	facts := r.Facts
	for len(facts) > 0 && lipgloss.Width(r.Name+factRun(facts)) > room {
		facts = facts[:len(facts)-1]
	}
	name := Clip(r.Name, max(room, 0))
	tail := Clip(factRun(facts), max(room-lipgloss.Width(name), 0))
	fill := max(room-lipgloss.Width(name)-lipgloss.Width(tail), 0)
	band, handleTone := trayBand(), sty.Info
	if r.Lit {
		band = func(s lipgloss.Style) lipgloss.Style { return s }
		handleTone = sty.Bright
	}
	return band(sty.Dim).Render(lead) +
		band(handleTone).Render(padRight(handle, slot)) +
		band(sty.Bright).Render(name) +
		band(sty.Dim).Render(tail+strings.Repeat(" ", fill)+right)
}

// factRun is the facts as they follow the name: each led by the separator
// every rail joins its fields with.
func factRun(facts []string) string {
	if len(facts) == 0 {
		return ""
	}
	return chipSeparator + strings.Join(facts, chipSeparator)
}

// TrayLine is one line under a tray row on the same band — an opened
// paste's line, in the dimmer tone every body is drawn in, or its count with
// the tray's one key, in the hint grey — set in at the body's indent and
// carried to the pane's edge so the band does not stop where the words do.
func TrayLine(text string, count bool, width int) string {
	inner := max(width-TrayBodyIndent, 1)
	text = Clip(text, inner)
	tone := sty.Dimmer
	if count {
		tone = sty.Hint
	}
	band := trayBand()
	pad := ""
	if trayBandShows() {
		pad = strings.Repeat(" ", max(inner-lipgloss.Width(text), 0))
	}
	return band(sty.Dim).Render(strings.Repeat(" ", TrayBodyIndent)) + band(tone).Render(text+pad)
}

// trayBand puts the band under a style. Where the palette has no band — mono,
// and a terminal of sixteen colours — the style is left as it was, and the
// rows are the tray on their own.
func trayBand() func(lipgloss.Style) lipgloss.Style {
	if !trayBandShows() {
		return func(s lipgloss.Style) lipgloss.Style { return s }
	}
	bg := Palette.Band.Color()
	return func(s lipgloss.Style) lipgloss.Style { return s.Background(bg) }
}

// trayBandShows reports whether the band is a colour at all here.
func trayBandShows() bool { return backgroundSeq(Palette.Band) != "" }
