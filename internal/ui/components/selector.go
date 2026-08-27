package components

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// SelectOption is one row of a selector. Desc, when set, renders dimmed under
// the option while it is focused. RequireNote marks options that a NoteSelect
// refuses to confirm without a note.
type SelectOption struct {
	Label       string
	Desc        string
	RequireNote bool
	// Header marks a row that labels the run of options beneath it rather
	// than offering one — the palette's COMMANDS / SESSIONS / FILES rails
	// (S-112, DESIGN-TUI.md §18a). Focus steps over it, it is never
	// numbered, and no key can land on it.
	Header bool
	// Dim marks an option that is showing but cannot be acted on right now,
	// rendered behind ⊘ with its Desc stating why (S-112). It stays
	// selectable, because choosing it is how the surface says why.
	Dim bool
}

// SelectResult is the single-select Update result.
type SelectResult struct {
	Index    int
	Canceled bool
}

// queryCursor is the block that follows what has been typed into the filter
// row. It is a character rather than a terminal cursor because the card is
// drawn into a viewport, where there is only ever one real cursor and the
// input frame owns it.
const queryCursor = "█"

// Select is the single-select card (DESIGN-TUI.md §4a): ↑↓/jk move, enter
// selects, number keys select immediately, esc cancels, and — on a list long
// enough to want one — / opens a filter row above the window.
type Select struct {
	Title   string
	Options []SelectOption
	Focus   int
	// MaxLines bounds the card height, frame included; long lists scroll with
	// … markers. 0 means unbounded.
	MaxLines int
	// Chips ride the right end of the title border (§2). A card that sets
	// none gets the window's own count instead — see chips.
	Chips []string
	// Hint replaces the default key-hint line for a surface whose keys are
	// not the default ones.
	Hint string
	// Unnumbered drops the "1." prefixes and the number-jump keys, for a
	// surface where a digit is text rather than a jump (S-112).
	Unnumbered bool

	// Filterable offers / on the key row and lets it open the query line.
	// Past a dozen entries walking is the slow way (§4a), so every picker
	// that opens over a catalog sets this; a card that is a fixed set of
	// answers does not.
	Filterable bool
	// Filtering is whether the query line is open. It is the card's own
	// state when / opened it, and a caller's when the surface is a query
	// line with a list under it from the start — which is what the palette
	// is (§18a).
	Filtering bool
	// Query is the text that produced Options. The component never filters:
	// the caller passes the matches and the query that made them, so the
	// match rule stays where it is chosen rather than hiding in a primitive
	// (§4a). Query is what the row echoes and what a matched run is bolded
	// against.
	Query string
	// Total is how many options Query was applied to, for the row's "4 of 24
	// match" and for the title rail. 0 means the caller has not said, and
	// the card counts what it was given instead.
	Total int
	// Closest names the nearest option that does exist, shown on the card
	// that matched nothing. The caller knows it because the caller is what
	// matched.
	Closest string

	// queryEdited records that the last Update changed Query, so a host can
	// re-run its match rule. QueryChanged reads and clears it.
	queryEdited bool

	// window is the slice of Options the card shows when the list is taller
	// than the card (S-116), and the shared one every long list scrolls
	// through since S-124 — see listwindow.go. A filter that shortened the
	// list clamps it and a Focus outside it pulls it back, so no host has to
	// reset it.
	window listWindow
}

// geometry is what the shared window needs to know about this list: every
// option is one row except the focused one, which carries its description
// underneath, and group rails are labels rather than options, so the markers
// do not offer to scroll to them.
func (s *Select) geometry() listGeometry {
	return listGeometry{
		n:     len(s.Options),
		focus: s.Focus,
		height: func(i int) int {
			if i == s.Focus && !s.Options[i].Header && s.Options[i].Desc != "" {
				return 2
			}
			return 1
		},
		counts: func(i int) bool { return !s.Options[i].Header },
	}
}

func (s *Select) Update(msg tea.KeyMsg) (done bool, result any) {
	s.normalizeFocus()
	key := msg.String()
	switch key {
	case "up":
		s.move(-1)
		return false, nil
	case "down":
		s.move(1)
		return false, nil
	case "enter":
		// A card that matched nothing has nothing for enter to take, and a
		// key that cannot act does not act (invariant 5).
		if s.selectable() == 0 {
			return false, nil
		}
		return true, SelectResult{Index: s.Focus}
	case "esc", "ctrl+c":
		// esc leaves the picker rather than closing the query line: §4a asks
		// that leaving change nothing, and a filter you have to escape twice
		// is a mode.
		return true, SelectResult{Index: -1, Canceled: true}
	}
	// With the query line open, the query line is the surface: everything
	// that is not movement or dispatch is text. A digit typed into a model
	// name is a digit and so is a j — the reading the palette has always had
	// (§18a), which the filter row is what generalizes. It is also what
	// stops a model name with a 5 in it from switching the model mid-word.
	if s.Filtering {
		s.editQuery(msg)
		return false, nil
	}
	switch key {
	case "/":
		if s.Filterable {
			s.Filtering = true
		}
	case "k", "j":
		// On a list that is typed into, j and k are letters (S-112).
		if s.Unnumbered {
			break
		}
		if key == "k" {
			s.move(-1)
		} else {
			s.move(1)
		}
	default:
		if s.Unnumbered {
			break
		}
		if n := digitIndex(key, s.selectable()); n >= 0 {
			s.Focus = s.selectableIndex(n)
			return true, SelectResult{Index: s.Focus}
		}
	}
	return false, nil
}

// editQuery applies one keystroke to the open query line: ctrl+u clears it,
// backspace takes a rune back, and anything that types adds to it.
func (s *Select) editQuery(msg tea.KeyMsg) {
	switch msg.String() {
	case "ctrl+u":
		if s.Query == "" {
			return
		}
		s.Query, s.queryEdited = "", true
	case "backspace":
		if r := []rune(s.Query); len(r) > 0 {
			s.Query, s.queryEdited = string(r[:len(r)-1]), true
		}
	default:
		if text := typedRunes(msg); text != "" {
			s.Query, s.queryEdited = s.Query+text, true
		}
	}
}

// QueryChanged reports — and clears — whether the last Update edited the
// query. It is how a host learns to re-run its match rule: the card does not
// filter, so the answer to a changed query is new Options from the caller
// (§4a).
func (s *Select) QueryChanged() bool {
	changed := s.queryEdited
	s.queryEdited = false
	return changed
}

// typedRunes is what a key contributes to a query line, or "" for a key that
// types nothing.
func typedRunes(msg tea.KeyMsg) string {
	switch msg.Type {
	case tea.KeyRunes:
		if msg.Alt {
			return ""
		}
		return string(msg.Runes)
	case tea.KeySpace:
		return " "
	}
	return ""
}

func (s *Select) View(width int) string {
	s.normalizeFocus()
	// The query line and the key hints are pinned: the list scrolls under
	// them, so what the card spends on them comes off the list's budget
	// before the window is drawn (§4a, S-116).
	head := s.queryRows(width)
	tail := hintRows(s.hintSegments(width), width)
	body, shown := s.visibleRows(width, s.bodyBudget(len(head)+len(tail)), !s.Unnumbered)
	rows := append(head, body...)
	rows = append(rows, tail...)
	rows = boundRows(rows, s.MaxLines)
	return renderChromeCard(cardChrome{title: s.Title, chips: s.chips(shown)}, rows, width)
}

// hintSegments is the card's key row, and the order it gives things up in.
// The filter changes it twice: an open query line offers the key that clears
// it, and a card that matched nothing offers only that one and esc, because
// nothing else on it can act.
//
// Nothing on a key row is ever truncated (invariant 4), so a row too long for
// the terminal sheds a segment rather than clipping one: the number-jump
// reminder goes first, because every row on screen is already carrying its
// own number, and then j/k, which is a second name for a key the row still
// offers. What an offer is — the filter, the selection, the way out — never
// goes.
func (s *Select) hintSegments(width int) []string {
	if s.Hint != "" {
		return []string{s.Hint}
	}
	if s.Filtering {
		if s.selectable() == 0 {
			return []string{"ctrl+u clear the filter", "esc cancel"}
		}
		return []string{"↑↓ move", "enter select", "ctrl+u clear", "esc cancel"}
	}
	move, jump, filter := "↑↓/jk move", fmt.Sprintf("1–%d jump", s.selectable()), ""
	if s.Unnumbered {
		// No numbers to offer means no j/k either, on a list typed into.
		move, jump = "↑↓ move", ""
	}
	if s.Filterable {
		filter = "/ filter"
	}
	inner := max(width-cardFrameWidth, 1)
	rungs := [][]string{
		{move, "enter select", jump, filter, "esc cancel"},
		{move, "enter select", filter, "esc cancel"},
		{"↑↓ move", "enter select", filter, "esc cancel"},
	}
	for _, rung := range rungs {
		segs := presentSegments(rung)
		if lipgloss.Width(strings.Join(segs, " · ")) <= inner {
			return segs
		}
	}
	return presentSegments(rungs[len(rungs)-1])
}

// presentSegments drops the rungs' empty placeholders, which is what a
// segment the card has no reason to offer leaves behind.
func presentSegments(segs []string) []string {
	out := make([]string, 0, len(segs))
	for _, s := range segs {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// queryRows is the pinned filter row (§4a): the ▸ prompt with what has been
// typed and its block cursor, and — for a caller that said how big the
// catalog behind it is — how many of that catalog the query matched. Both
// counts are on the row so the list it came from is never hidden, and where
// the terminal cannot carry them side by side they stack rather than one
// being dropped (invariant 4).
func (s *Select) queryRows(width int) []string {
	if !s.Filtering {
		return nil
	}
	inner := max(width-cardFrameWidth, 1)
	typed := infoStyle.Render("▸ ") + queryTextStyle.Render(s.Query+queryCursor)
	if s.Total <= 0 {
		return []string{clip(typed, inner)}
	}
	count := dimStyle.Render(fmt.Sprintf("%d of %d match", s.selectable(), s.Total))
	if pad := inner - lipgloss.Width(typed) - lipgloss.Width(count); pad >= 2 {
		return []string{typed + strings.Repeat(" ", pad) + count}
	}
	return []string{clip(typed, inner), clip(count, inner)}
}

// chips is the count on the title rail: how big the catalog is, and — when
// the card could not show all of it — how much of it is on screen. A caller
// that set its own chips keeps them, and a list that fits with no filter open
// spends no rail saying that nothing was hidden.
func (s *Select) chips(shown int) []string {
	if len(s.Chips) > 0 {
		return s.Chips
	}
	matched := s.selectable()
	if !s.Filtering && shown >= matched {
		return nil
	}
	total := s.Total
	if total <= 0 {
		total = matched
	}
	chip := fmt.Sprintf("%d available", total)
	if shown < matched {
		chip += fmt.Sprintf(" · %d showing", shown)
	}
	return []string{chip}
}

// optionRows renders Options[lo:hi] with the ❯ pointer on the focused row and
// the focused option's description beneath it. Numbering counts from the
// start of the list rather than from the window, because the number keys
// address the list and not what happens to be on screen.
func (s *Select) optionRows(width int, numbered bool, lo, hi int) []string {
	inner := width - cardFrameWidth
	var rows []string
	n := 0
	for i, opt := range s.Options {
		if opt.Header {
			if i >= lo && i < hi {
				rows = append(rows, dimStyle.Render(clip(opt.Label, inner)))
			}
			continue
		}
		n++
		if i < lo || i >= hi {
			continue
		}
		label := opt.Label
		if numbered {
			label = fmt.Sprintf("%d. %s", n, label)
		}
		if opt.Dim {
			// The glyph, not the dimming, is what says unavailable: colour
			// never carries meaning alone (invariant 1).
			label = "⊘ " + label
		}
		switch {
		case i == s.Focus:
			// The focused row is already bold, so a matched run has no
			// emphasis left to spend on it; the pointer and the background
			// are what say which row this is.
			rows = append(rows, focusRowStyle.Render(clip("❯ "+label, inner)))
			if opt.Desc != "" {
				rows = append(rows, dimStyle.Render(clip("    "+opt.Desc, inner)))
			}
		case opt.Dim:
			// A row that cannot be acted on is not a row the query is
			// hunting for, and the dimming is one run: emphasis inside it
			// would break the run and say the wrong thing twice.
			rows = append(rows, dimmerStyle.Render(clip("  "+label, inner)))
		default:
			rows = append(rows, clip("  "+emphasizeMatch(label, s.Query), inner))
		}
	}
	return rows
}

// emphasizeMatch bolds the run of the label the query names (§4a). Bold and
// never a tint: exactly three background tints exist inside a screen and each
// already means one thing, and bold is the emphasis that survives mono.
//
// The match rule stays with the caller, so this only shows where a query the
// caller already accepted lands inside a row. A query that is not a literal
// run of the label — a subsequence match, a match on a field the row does not
// show — emphasizes nothing rather than guessing.
func emphasizeMatch(label, query string) string {
	if query == "" {
		return label
	}
	lower, lq := strings.ToLower(label), strings.ToLower(query)
	if len(lower) != len(label) || len(lq) != len(query) {
		// Lowering moved the bytes, so an offset into it is not an offset
		// into the label. Rare, and not worth emphasizing the wrong run.
		return label
	}
	i := strings.Index(lower, lq)
	if i < 0 {
		return label
	}
	return label[:i] + matchStyle.Render(label[i:i+len(query)]) + label[i+len(query):]
}

// bodyBudget is how many rows the option list may spend on a card of this
// height: the total less its frame and less everything pinned above and below
// the list. It returns 0 — unbounded — for a card with no height bound, which
// is what a test or a surface that sizes itself gets.
func (s *Select) bodyBudget(pinned int) int {
	return bodyBudget(s.MaxLines, pinned)
}

// visibleRows renders the option list windowed to a body budget, with the
// overflow markers the window makes necessary, and reports how many options
// ended up on screen. A card that wraps a Select renders the list itself —
// NoteSelect puts a note field under it (§4c) — and goes through here too, so
// the pointer is never clipped off the bottom.
func (s *Select) visibleRows(width, budget int, numbered bool) ([]string, int) {
	s.normalizeFocus()
	if s.Filtering && s.selectable() == 0 {
		return s.noMatchRows(width), 0
	}
	g := s.geometry()
	lo, hi := s.window.rangeFor(g, budget)
	rows := s.optionRows(width, numbered, lo, hi)
	if lo > 0 {
		rows = append([]string{listOverflowRow("↑", g.countIn(0, lo), "", width)}, rows...)
	}
	if hi < len(s.Options) {
		rows = append(rows, listOverflowRow("↓", g.countIn(hi, len(s.Options)), "", width))
	}
	return rows, g.countIn(lo, hi)
}

// noMatchRows is what a filter that matched nothing renders (§4a): a row, not
// an empty pane. The card keeps its frame, the query row above keeps both
// counts, the key that clears the filter stays on the key row, and a line
// names the nearest thing that does exist — which the caller supplies,
// because the caller is what matched.
func (s *Select) noMatchRows(width int) []string {
	inner := max(width-cardFrameWidth, 1)
	rows := []string{dimStyle.Render(clip("  "+fmt.Sprintf("no match for %q", s.Query), inner))}
	if s.Closest != "" {
		rows = append(rows, dimStyle.Render(clip("  closest is "+s.Closest, inner)))
	}
	return rows
}

// move steps the focus by delta, over any header rows in the way. A move that
// runs off either end leaves the focus where it was.
func (s *Select) move(delta int) {
	for i := s.Focus + delta; i >= 0 && i < len(s.Options); i += delta {
		if !s.Options[i].Header {
			s.Focus = i
			return
		}
	}
}

// normalizeFocus keeps the pointer on a row that can be chosen: a list that
// opens on a header — or on nothing, after a filter shortened it — moves the
// focus to the nearest option instead.
func (s *Select) normalizeFocus() {
	if len(s.Options) == 0 {
		s.Focus = 0
		return
	}
	if s.Focus < 0 {
		s.Focus = 0
	}
	if s.Focus >= len(s.Options) {
		s.Focus = len(s.Options) - 1
	}
	if !s.Options[s.Focus].Header {
		return
	}
	for i := s.Focus; i < len(s.Options); i++ {
		if !s.Options[i].Header {
			s.Focus = i
			return
		}
	}
	for i := s.Focus; i >= 0; i-- {
		if !s.Options[i].Header {
			s.Focus = i
			return
		}
	}
}

// FirstSelectable is the index of the first row a key can land on. A filtered
// list puts its pointer here after every keystroke (S-112), because the rows
// under it are not the rows that were there a moment ago.
func (s *Select) FirstSelectable() int {
	for i, opt := range s.Options {
		if !opt.Header {
			return i
		}
	}
	return 0
}

// selectable counts the rows a key can land on, which is every row until a
// list carries headers.
func (s *Select) selectable() int {
	n := 0
	for _, opt := range s.Options {
		if !opt.Header {
			n++
		}
	}
	return n
}

// selectableIndex maps a 1-based position among the selectable rows — what
// the number keys and the "1." prefixes count — to its index in Options.
func (s *Select) selectableIndex(n int) int {
	seen := 0
	for i, opt := range s.Options {
		if opt.Header {
			continue
		}
		if seen++; seen == n {
			return i
		}
	}
	return 0
}

// digitIndex maps a number key to a 1-based position among n rows, or -1.
func digitIndex(key string, n int) int {
	if len(key) != 1 || key[0] < '1' || key[0] > '9' {
		return -1
	}
	if pos := int(key[0] - '0'); pos <= n {
		return pos
	}
	return -1
}

// boundRows clips a row list to a card's MaxLines budget (frame included),
// replacing the last visible row with an … marker when rows were dropped.
func boundRows(rows []string, maxLines int) []string {
	if maxLines <= 0 {
		return rows
	}
	budget := max(maxLines-2, 1)
	if len(rows) <= budget {
		return rows
	}
	keep := max(budget-1, 1)
	return append(rows[:keep:keep], dimStyle.Render("…"))
}
