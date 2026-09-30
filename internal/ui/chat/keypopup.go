package chat

// The key list (docs/interface/surfaces.md#the-key-list): the chord, and a
// bare /help, open every key the register binds over the session, grouped
// the way a keymap file names them, and filtered as you type.
//
// It used to be a system row: the whole register appended to the transcript,
// where it scrolled the conversation away, was saved with it and came back on
// every resume while saying nothing about the session it was in. Reference is
// looked up and put away, so it is a surface now and leaves nothing behind.
//
// It is the palette's shape rather than a list of its own: the same
// components.Select with its query line open from the first keystroke, in the
// same bottom panel, borrowing the screen the same way, so the draft under it
// — a half-written prompt included — is exactly as it was when it closes and
// a running turn streams on underneath. What it adds is the rows, which are
// read off the register as this process holds it when it opens, so a key a
// keybindings.toml moved is listed at the keystrokes it answers now.

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// keyPopup is the open key list: every binding in the register, and the card
// showing the ones the query left, one option per row the card draws.
type keyPopup struct {
	card components.Select
	all  []keyPopupRow
}

// keyPopupRow is one binding as the list shows it and as the query is
// matched against it.
type keyPopupRow struct {
	group string
	// keys is the spelling a hint prints for it in this process, bracketed.
	keys  string
	words string
	// name is what a keymap file calls it, which is searched as well: a
	// reader who has the file open types the name they are looking at.
	name string
}

// keyPopupRows is the register, a row per binding, in the order movable()
// declares the groups and each group declares its keys.
//
// A conversation's list has no row for the mode chord, as /help's key section
// has none: it has one mode, and the chord answers with a sentence saying so,
// so a row offering to cycle it would offer a key the session refuses
// (docs/capabilities/chat.md#a-conversation-has-one-mode).
func keyPopupRows(conversation bool) []keyPopupRow {
	var rows []keyPopupRow
	for _, g := range keys.Keyboard() {
		for _, a := range g.Acts {
			if conversation && a.Shown == keys.Shown(keys.Draft.Mode) && a.Words == keys.Words(keys.Draft.Mode) {
				continue
			}
			rows = append(rows, keyPopupRow{group: g.Name, keys: keys.Bracketed(a.Shown), words: a.Words, name: a.Name})
		}
	}
	return rows
}

// keyPopupHint is the list's key row. Every spelling on it is the
// declaration the handler answers, so a file that moves one moves both; the
// way out is its own binding's rather than the selector family's esc, which
// is why the card is given no CancelLabel.
func keyPopupHint() []components.KeyOffer {
	return []components.KeyOffer{
		components.Offer(keys.KeyList.Move),
		components.Offer(keys.KeyList.Page),
		components.Offer(keys.KeyList.Ends),
		components.Offer(keys.KeyList.Close),
	}
}

// openKeyPopup puts the list up over whatever the session is doing. It reads
// the register here and not per keystroke: what is bound does not move while
// a session runs.
func (m Model) openKeyPopup() (tea.Model, tea.Cmd) {
	p := &keyPopup{all: keyPopupRows(m.conversation)}
	p.card = components.Select{
		// The chord is the title, as it is on the palette: the card is the
		// answer to a key, and naming it is how a reader who came in through
		// /help learns the shorter door.
		Title:      keys.Shown(keys.Draft.KeyList),
		Unnumbered: true,
		Filtering:  true,
		HintKeys:   keyPopupHint(),
	}
	// The panel places the terminal's own cursor on the query row
	// (keyPopupCursor), so the card stops painting one.
	p.card.SetVirtualCursor(false)
	p.card.MaxLines = m.maxConfirmPanelHeight()
	p.refresh()
	m.screens = m.screens.with(stateKeyPopup, p)
	m.enterSurface(stateKeyPopup)
	m.syncViewport()
	return m, nil
}

// closeKeyPopup hands the screen back to whatever the turn became while the
// list had it. The draft was never touched, so there is nothing to put back.
func (m Model) closeKeyPopup() (tea.Model, tea.Cmd) {
	m.screens = m.screens.without(stateKeyPopup)
	m.leaveSurface()
	m.syncViewport()
	return m, nil
}

// updateKeyPopup answers a key on the list. Its own keys come first; what
// is left goes to the card only where it edits the query, because the card
// would read enter, esc and the arrows by the selector family's bindings
// rather than by the list's own.
func (m Model) updateKeyPopup(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	p := m.screens.keyPopup()
	if p == nil {
		return m.closeKeyPopup()
	}
	pressed := msg.String()
	switch {
	case keys.Is(pressed, keys.KeyList.Close, keys.Draft.KeyList):
		return m.closeKeyPopup()
	case keys.Is(pressed, keys.KeyList.Move):
		p.step(keys.Step(pressed, keys.KeyList.Move))
	case keys.Is(pressed, keys.KeyList.Page):
		for range p.page() {
			p.step(keys.Step(pressed, keys.KeyList.Page))
		}
	case keys.Is(pressed, keys.KeyList.Ends):
		if keys.Step(pressed, keys.KeyList.Ends) < 0 {
			p.card.Focus = p.card.FirstSelectable()
		} else {
			p.card.Focus = p.last()
		}
	case msg.Text != "", keys.Is(pressed, keys.Query.Rub),
		keys.Is(pressed, keys.Select.ClearQ) && p.card.Query != "":
		// ctrl+u on an empty query would close the card's query line, and
		// this list is nothing but a query line with rows under it.
		p.card.Update(msg)
		if p.card.QueryChanged() {
			p.refresh()
		}
	}
	return m, nil
}

// refresh rebuilds the card from the query: a group's rail above the first
// of its rows the query left, the count on the title rail, and the pointer
// back on the first row. The match is the substring one every catalog
// filter here shares, over the keys, the words and the file's name for it:
// a reader typing `copy` or `ctrl+g` is naming a thing, and a fuzzy match
// over short phrases would leave half the register looking like a near miss.
func (p *keyPopup) refresh() {
	shown := components.Filter(p.all, p.card.Query, func(r keyPopupRow) []string {
		return []string{r.keys, r.words, r.name}
	})
	opts := make([]components.SelectOption, 0, len(shown))
	group := ""
	for _, i := range shown {
		r := p.all[i]
		if r.group != group {
			group = r.group
			opts = append(opts, components.SelectOption{Label: strings.ToUpper(group), Header: true})
		}
		opts = append(opts, components.SelectOption{Label: r.keys, Desc: r.words})
	}
	p.card.Options = opts
	p.card.Chips = []string{keyPopupCount(len(shown), len(p.all))}
	p.card.Focus = p.card.FirstSelectable()
}

// keyPopupCount is the title rail's chip: how many keys the query left of
// the whole register, and the whole register alone when it left them all.
func keyPopupCount(matched, all int) string {
	switch {
	case matched == 0:
		return "no matches"
	case matched >= all:
		return fmt.Sprintf("%d keys", all)
	}
	return fmt.Sprintf("%d of %d keys", matched, all)
}

// step moves the pointer one key, over the group rails, and stays put at
// either end rather than wrapping past it.
func (p *keyPopup) step(delta int) {
	for i := p.card.Focus + delta; i >= 0 && i < len(p.card.Options); i += delta {
		if !p.card.Options[i].Header {
			p.card.Focus = i
			return
		}
	}
}

// last is the last row a key can land on.
func (p *keyPopup) last() int {
	for i := len(p.card.Options) - 1; i >= 0; i-- {
		if !p.card.Options[i].Header {
			return i
		}
	}
	return p.card.Focus
}

// page is how many keys a page moves: the rows the card has for its list,
// less its frame, the query row and the key row.
func (p *keyPopup) page() int {
	return max(p.card.MaxLines-5, 1)
}

// keyPopupLines is the card in the panel, measured against the panel's bound
// as it stands now — a resize moves it — rather than as it stood at opening.
func (m Model) keyPopupLines() []string {
	p := m.screens.keyPopup()
	if p == nil {
		return nil
	}
	p.card.MaxLines = m.maxConfirmPanelHeight()
	return strings.Split(p.card.View(m.contentWidth()), "\n")
}

// keyPopupCursor is where the terminal's cursor stands on the query row.
func (m Model) keyPopupCursor(width int) *tea.Cursor {
	p := m.screens.keyPopup()
	if p == nil {
		return nil
	}
	return p.card.Cursor(width)
}
