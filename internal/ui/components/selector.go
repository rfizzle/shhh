package components

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
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

// Select is the single-select card (DESIGN-TUI.md §4a): ↑↓/jk move, enter
// selects, number keys select immediately, esc cancels.
type Select struct {
	Title   string
	Options []SelectOption
	Focus   int
	// MaxLines bounds the card height, frame included; long lists scroll with
	// … markers. 0 means unbounded.
	MaxLines int
	// Prompt renders above the options as the card's own input line — the
	// palette's typed query (S-112). Empty renders nothing, which is every
	// other selector.
	Prompt string
	// Chips ride the right end of the title border (§2): the palette's
	// result count.
	Chips []string
	// Hint replaces the default key-hint line for a surface whose keys are
	// not the default ones.
	Hint string
	// Unnumbered drops the "1." prefixes and the number-jump keys, for a
	// surface where a digit is text rather than a jump (S-112).
	Unnumbered bool

	// scroll is the first Options index the window shows when the list is
	// taller than the card (S-116). It is state rather than arithmetic on
	// Focus because the window has to stay still while the pointer moves
	// inside it — a list that re-centres on every keystroke is unreadable —
	// and it self-heals, so no host has to reset it: a filter that shortened
	// the list clamps it, and a Focus outside the window pulls it back.
	scroll int
}

func (s *Select) Update(msg tea.KeyMsg) (done bool, result any) {
	s.normalizeFocus()
	switch key := msg.String(); key {
	case "up":
		s.move(-1)
	case "down":
		s.move(1)
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
	case "enter":
		return true, SelectResult{Index: s.Focus}
	case "esc", "ctrl+c":
		return true, SelectResult{Index: -1, Canceled: true}
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

func (s *Select) View(width int) string {
	s.normalizeFocus()
	var head []string
	if s.Prompt != "" {
		head = append(head, bodyStyle.Render(clip(s.Prompt, width-cardFrameWidth)))
	}
	hint := s.Hint
	switch {
	case hint != "":
	case s.Unnumbered:
		// No numbers to offer, and j/k are text on a list that is typed into.
		hint = "↑↓ move · enter select · esc cancel"
	default:
		hint = fmt.Sprintf("↑↓/jk move · enter select · 1–%d jump · esc cancel", s.selectable())
	}
	// The query line and the key hints are pinned: the list scrolls under
	// them, so what the card spends on them comes off the list's budget
	// before the window is drawn (S-116).
	tail := hintRows([]string{hint}, width)
	rows := append(head, s.visibleRows(width, s.bodyBudget(len(head)+len(tail)), !s.Unnumbered)...)
	rows = append(rows, tail...)
	rows = boundRows(rows, s.MaxLines)
	return renderChromeCard(cardChrome{title: s.Title, chips: s.Chips}, rows, width)
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
			rows = append(rows, focusRowStyle.Render(clip("❯ "+label, inner)))
			if opt.Desc != "" {
				rows = append(rows, dimStyle.Render(clip("    "+opt.Desc, inner)))
			}
		case opt.Dim:
			rows = append(rows, dimmerStyle.Render(clip("  "+label, inner)))
		default:
			rows = append(rows, clip("  "+label, inner))
		}
	}
	return rows
}

// bodyBudget is how many rows the option list may spend on a card of this
// height: the total less its frame and less everything pinned above and below
// the list. It returns 0 — unbounded — for a card with no height bound, which
// is what a test or a surface that sizes itself gets.
func (s *Select) bodyBudget(pinned int) int {
	if s.MaxLines <= 0 {
		return 0
	}
	return max(s.MaxLines-2-pinned, 1)
}

// visibleRows renders the option list windowed to a body budget, with the
// overflow markers the window makes necessary. A card that wraps a Select
// renders the list itself — NoteSelect puts a note field under it (§4c) — and
// goes through here too, so the pointer is never clipped off the bottom.
func (s *Select) visibleRows(width, budget int, numbered bool) []string {
	s.normalizeFocus()
	lo, hi := s.optionWindow(budget)
	rows := s.optionRows(width, numbered, lo, hi)
	if lo > 0 {
		rows = append([]string{listOverflowRow("↑", s.hiddenOptions(0, lo), width)}, rows...)
	}
	if hi < len(s.Options) {
		rows = append(rows, listOverflowRow("↓", s.hiddenOptions(hi, len(s.Options)), width))
	}
	return rows
}

// optionWindow is the half-open range of Options the card shows for a body
// budget (S-116). The pointer above the window pulls it up to meet it and the
// pointer below pushes it down, one option at a time; inside it, the window
// does not move at all.
func (s *Select) optionWindow(budget int) (lo, hi int) {
	n := len(s.Options)
	if n == 0 {
		s.scroll = 0
		return 0, 0
	}
	if budget <= 0 || s.optionHeight(0, n) <= budget {
		// Everything fits: there is no window, and nothing to remember about
		// where one was.
		s.scroll = 0
		return 0, n
	}
	lo = min(max(s.scroll, 0), n-1)
	if s.Focus < lo {
		lo = s.Focus
	}
	for {
		hi = s.windowEnd(lo, budget)
		if hi > s.Focus || lo >= n-1 {
			break
		}
		lo++
	}
	s.scroll = lo
	return lo, hi
}

// windowEnd is the exclusive end of the run starting at lo that fits budget
// body rows, counting the overflow markers the run itself makes necessary: a
// window that starts past the top spends a row saying so, and one that stops
// short of the end spends another.
func (s *Select) windowEnd(lo, budget int) int {
	n := len(s.Options)
	avail := budget
	if lo > 0 {
		avail--
	}
	if s.optionHeight(lo, n) <= avail {
		return n
	}
	avail--
	hi, used := lo, 0
	for hi < n {
		h := s.optionHeight(hi, hi+1)
		if used+h > avail {
			break
		}
		used, hi = used+h, hi+1
	}
	// A budget too small for even one option still shows one: a card with no
	// rows on it is worse than a card that overruns by a line, and boundRows
	// is what holds the height contract in that corner.
	return max(hi, lo+1)
}

// optionHeight is how many rows Options[lo:hi) render to. Every option is one
// row except the focused one, which carries its description underneath.
func (s *Select) optionHeight(lo, hi int) int {
	rows := 0
	for i := lo; i < hi; i++ {
		rows++
		if i == s.Focus && !s.Options[i].Header && s.Options[i].Desc != "" {
			rows++
		}
	}
	return rows
}

// hiddenOptions counts the rows a key could have landed on in Options[lo:hi).
// Headers are labels for options rather than options, so they are not what
// the marker offers to scroll to.
func (s *Select) hiddenOptions(lo, hi int) int {
	n := 0
	for i := lo; i < hi; i++ {
		if !s.Options[i].Header {
			n++
		}
	}
	return n
}

// listOverflowRow is the marker on a windowed list's edge. It counts what it
// is hiding rather than only marking that something is (invariant 4) — the
// queue strip's own overflowRow says the same thing about a different list; a
// run that hid nothing selectable keeps the bare … it always had.
func listOverflowRow(arrow string, n, width int) string {
	label := "…"
	if n > 0 {
		label = fmt.Sprintf("%s %d more", arrow, n)
	}
	return hintStyle.Render(clip(label, width-cardFrameWidth))
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
	return append(rows[:keep:keep], hintStyle.Render("…"))
}
