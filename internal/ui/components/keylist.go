package components

// The key list (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard):
// what `?` opens over a card that holds the keyboard, and over reading mode.
//
// A takeover screen answers `?` in its own foot, because its foot is where
// its keys already are. A card has no foot to swap: it is bounded to a share
// of the screen, and its register with the glyph legend under it is taller
// than the card. So the list takes the transcript pane for as long as it is
// up, drawn in the screens' chrome, and the same key — or esc — puts the card
// back exactly as it was. The rows are the ones a screen's foot draws
// (KeyListRows), so the answer to `?` reads the same wherever it was asked.

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// KeyListScreen is the register of one surface, and the glyph legend under
// it, full width. It is passive: the host says which surface it is about and
// hands over that surface's keys.
type KeyListScreen struct {
	// Surface is what the keys are for, in the register's own words — "the
	// approval card", "reading mode". The header states it, so a reader who
	// opened the list over one card does not read it as another's.
	Surface string
	// Register is every key the surface answers, in the order it offers them.
	Register []KeyOffer
	// Row is the offers the row under reading mode's cursor makes. They keep
	// the rail that says they are the row's rather than the mode's, the way
	// the reading bar draws them; a surface with no row under a cursor leaves
	// it empty.
	Row []KeyOffer
	// MaxLines bounds the screen to the pane it is drawn into.
	MaxLines int
	// pager is the offset the list is read through where the pane is shorter
	// than it.
	pager Pager
}

// KeyListResult carries nothing: the list decides nothing, so leaving is all
// it has to report.
type KeyListResult struct{}

// SetSize gives the screen the pane's rectangle.
func (k *KeyListScreen) SetSize(_, height int) { k.MaxLines = height }

// Update answers one key. The same `?` that opened the list closes it, and so
// does the way out every screen answers; the arrows read a list longer than
// the pane.
func (k *KeyListScreen) Update(msg tea.KeyPressMsg) (done bool, result KeyListResult) {
	switch pressed := msg.String(); {
	case keys.Is(pressed, keys.Screen.List, keys.Screen.Quit):
		return true, KeyListResult{}
	case keys.Is(pressed, keys.Screen.Move):
		k.pager.Offset += keys.Step(pressed, keys.Screen.Move)
	}
	return false, KeyListResult{}
}

// View renders the list at the given width.
func (k *KeyListScreen) View(width int) string {
	if width <= 0 {
		return ""
	}
	header := ScreenHeader{
		Left: []RailSegment{screenTitle("keys")},
		Keys: words(keys.Screen.List, "back"),
	}
	if k.Surface != "" {
		header.Left = append(header.Left, screenField(k.Surface))
	}
	return ScreenChrome{Header: header, MaxLines: k.MaxLines}.
		View(width, func(budget int) []string { return k.bodyRows(width, budget) })
}

// bodyRows is the register, the row's offers under it, and the legend,
// windowed to the budget. What the window leaves out is counted at its edge
// rather than dropped (docs/interface/principles.md#fold-never-hide).
func (k *KeyListScreen) bodyRows(width, budget int) []string {
	rows := KeyListRows(k.Register, width)
	if len(k.Row) > 0 {
		// The row's offers sit with the keys, above the blank row the legend
		// starts after.
		split := len(rows) - len(GlyphLegend(width)) - 1
		rail := sty.Accent.Render("▎")
		var own []string
		for _, o := range k.Row {
			for _, r := range packOffers([]KeyOffer{o}, width-1) {
				own = append(own, rail+r)
			}
		}
		rows = append(append(append([]string{}, rows[:split]...), own...), rows[split:]...)
	}
	if budget <= 0 || len(rows) <= budget {
		return rows
	}
	// Two rows of the budget go to the counted edges, so the window is what
	// is left of it.
	k.pager.Height = max(budget-2, 1)
	shown := k.pager.Window(rows)
	above := fmt.Sprintf("↑ %d more", k.pager.Above())
	below := fmt.Sprintf("↓ %d more · %s", k.pager.Below(), words(keys.Screen.Move, "read on"))
	if k.pager.Above() == 0 {
		above = ""
	}
	if k.pager.Below() == 0 {
		below = ""
	}
	out := []string{sty.Dim.Render(Clip(above, width))}
	out = append(out, shown...)
	return append(out, sty.Dim.Render(Clip(below, width)))
}

// A card's run says `[?] keys` wherever its host answers the key, so the list
// is found from the card and not only from /help. It takes the last slot
// before the way out, the way the approval card's run ends, and a run too
// long for its row takes another row for it rather than losing an offer
// (docs/interface/principles.md#fold-never-hide). A one-line confirm has no
// second row to take, so there it is the first thing the line gives up
// (Confirm.withKeyList).

// withKeyListOffer is a run of offers with `[?] keys` placed before its way
// out — the trailing safe offer, or the end where there is none.
func withKeyListOffer(offers []KeyOffer) []KeyOffer {
	at := len(offers)
	for at > 0 && offers[at-1].Safe {
		at--
	}
	with := make([]KeyOffer, 0, len(offers)+1)
	with = append(with, offers[:at]...)
	with = append(with, keyOffer(keys.Screen.List))
	return append(with, offers[at:]...)
}

// KeyListOffers is withKeyListOffer for a run a host lays itself.
func KeyListOffers(offers []KeyOffer) []KeyOffer { return withKeyListOffer(offers) }

// keyListSegment is `[?] keys` as a painted segment of a run whose way out
// the caller lays after it.
func keyListSegment() string {
	return offerSegment(keys.Bracket(keys.Screen.List), keys.Words(keys.Screen.List))
}
