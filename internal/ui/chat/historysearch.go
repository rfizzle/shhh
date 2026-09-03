package chat

// Reverse search over the input ring (
// docs/interface/surfaces.md#the-input-frame). Ctrl+R does in the draft what
// it does at the shell: typing filters what was typed before, the chord again
// steps to an older match, enter keeps the match in the draft, and esc puts
// the draft back exactly as it was — the safe answer, as everywhere
// (docs/interface/principles.md#esc-is-always-the-safe-answer).
//
// It is a mode of the draft, not a panel of its own: the frame stays on
// screen, the match is shown in the box the way the shell shows it on the
// line, and the search states itself on one row under the draft — the row the
// completion menu uses, because both are the input explaining what the next
// keystroke will do to it. While the search is open every key is routed here
// first, which is what makes typing filter rather than edit; a key the
// search has no meaning for keeps the match and goes back to the ordinary
// dispatch, so a chord like the mode cycle is deferred, not eaten.

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// historySearch is the open search: the draft as it was when it opened, what
// has been typed into the query, and which match is showing (0 the newest).
type historySearch struct {
	saved string
	query string
	idx   int
}

// searchStyles is the history-search row's own group.
type searchStyles struct {
	Label lipgloss.Style
	Query lipgloss.Style
	State lipgloss.Style
	Hint  lipgloss.Style
}

func newSearchStyles(p components.ColorTokens) searchStyles {
	return searchStyles{
		Label: lipgloss.NewStyle().Bold(true).Foreground(p.Info.Color()),
		Query: lipgloss.NewStyle().Foreground(p.Body.Color()),
		State: lipgloss.NewStyle().Foreground(p.Dim.Color()),
		Hint:  lipgloss.NewStyle().Foreground(p.Dim.Color()).Italic(true),
	}
}

// historySearching reports whether the search holds the keyboard.
func (m Model) historySearching() bool { return m.histSearch != nil }

// openHistorySearch starts the search. A session with nothing in the ring
// has nothing to filter, and says so instead of opening a mode whose every
// keystroke would come to nothing.
func (m Model) openHistorySearch() (tea.Model, tea.Cmd) {
	if len(m.inputHistory) == 0 {
		return m.systemNotice("No input history to search yet.")
	}
	m.histSearch = &historySearch{saved: m.input.Value()}
	m.syncViewport()
	return m, nil
}

// closeHistorySearch ends the search. restore puts the draft back as it was;
// otherwise whatever match is in the box stays there.
func (m *Model) closeHistorySearch(restore bool) {
	if restore {
		m.input.SetValue(m.histSearch.saved)
	}
	m.histSearch = nil
	m.historyIdx = len(m.inputHistory)
	m.syncViewport()
}

// updateHistorySearch routes keys while the search is open.
func (m Model) updateHistorySearch(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	hs := m.histSearch
	switch {
	case keys.Match(msg, keys.Search.Cancel):
		m.closeHistorySearch(true)
		return m, nil
	case keys.Match(msg, keys.Search.Keep):
		m.closeHistorySearch(false)
		return m, nil
	case keys.Match(msg, keys.Search.Older):
		if hs.idx+1 < len(m.historyMatches(hs.query)) {
			hs.idx++
			m.placeHistoryMatch()
		}
		return m, nil
	case msg.Code == tea.KeyBackspace:
		if r := []rune(hs.query); len(r) > 0 {
			hs.query = string(r[:len(r)-1])
			hs.idx = 0
			m.placeHistoryMatch()
		}
		return m, nil
	case typedRune(msg):
		hs.query += msg.Text
		hs.idx = 0
		m.placeHistoryMatch()
		return m, nil
	}
	// A key the search has no meaning for — an arrow, the mode cycle, the
	// quit chord — keeps the match and goes back to the ordinary dispatch,
	// so leaving the search never eats a keystroke.
	m.closeHistorySearch(false)
	return m.update(msg)
}

// historyMatches are the ring entries containing the query, newest first —
// the order the chord steps through them. Case-insensitive, because the ring
// is prose and a prompt remembered as "Go test" should be findable as such.
func (m Model) historyMatches(query string) []string {
	if query == "" {
		return nil
	}
	q := strings.ToLower(query)
	var out []string
	for i := len(m.inputHistory) - 1; i >= 0; i-- {
		if strings.Contains(strings.ToLower(m.inputHistory[i]), q) {
			out = append(out, m.inputHistory[i])
		}
	}
	return out
}

// placeHistoryMatch puts the current match in the draft. An empty query has
// asked for nothing yet, so the draft shows what it held when the search
// opened; a query with no match keeps the last match on screen — the row
// says "no match", and deleting a character brings the matches back.
func (m *Model) placeHistoryMatch() {
	hs := m.histSearch
	if hs.query == "" {
		m.input.SetValue(hs.saved)
		return
	}
	matches := m.historyMatches(hs.query)
	if len(matches) == 0 {
		return
	}
	if hs.idx >= len(matches) {
		hs.idx = len(matches) - 1
	}
	m.input.SetValue(matches[hs.idx])
}

// searchRowHead is the row up to the end of the query — the label and what
// has been typed. The cursor stands at the end of it, so it is measured here
// rather than being written down a second time.
func (m Model) searchRowHead() string {
	return sty.Search.Label.Render("history search") +
		sty.Search.State.Render(": ") + sty.Search.Query.Render(m.histSearch.query)
}

// searchCursor is where the terminal's cursor stands on the search row, in
// that row's own cells: after the query, which is what the next keystroke
// extends. It is not in the draft above, even though the draft is where the
// match shows — a cursor sitting in the match would say the match was what
// was being edited, and the next character would go somewhere else.
func (m Model) searchCursor() *tea.Cursor {
	if m.histSearch == nil {
		return nil
	}
	return tea.NewCursor(lipgloss.Width(m.searchRowHead()), 0)
}

// historySearchLines is the search stating itself under the draft: the query
// and where it stands, then the keys. It rides the completion menu's rows
// because it is the same kind of thing — the input explaining itself.
func (m Model) historySearchLines() []string {
	hs := m.histSearch
	if hs == nil {
		return nil
	}
	width := m.contentWidth()
	state := "type to search"
	if hs.query != "" {
		if n := len(m.historyMatches(hs.query)); n == 0 {
			state = "no match"
		} else {
			state = fmt.Sprintf("%d of %d", hs.idx+1, n)
		}
	}
	row := m.searchRowHead() + sty.Search.State.Render(" · "+state)
	// The bar's words are shorter than the register's: the box clips at its
	// border rather than shedding, so the row has to fit the narrow layouts
	// whole. The keys are still the register's.
	hint := strings.Join([]string{
		keys.Shown(keys.Search.Older) + " older",
		keys.Shown(keys.Search.Keep) + " keep",
		keys.Shown(keys.Search.Cancel) + " " + keys.Words(keys.Search.Cancel),
	}, " · ")
	return []string{clipRow(row, width), sty.Search.Hint.Render(clipRow(hint, width))}
}
