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
	var rows []string
	if s.Prompt != "" {
		rows = append(rows, bodyStyle.Render(clip(s.Prompt, width-cardFrameWidth)))
	}
	rows = append(rows, s.optionRows(width, !s.Unnumbered)...)
	hint := s.Hint
	switch {
	case hint != "":
	case s.Unnumbered:
		// No numbers to offer, and j/k are text on a list that is typed into.
		hint = "↑↓ move · enter select · esc cancel"
	default:
		hint = fmt.Sprintf("↑↓/jk move · enter select · 1–%d jump · esc cancel", s.selectable())
	}
	rows = append(rows, hintRows([]string{hint}, width)...)
	rows = boundRows(rows, s.MaxLines)
	return renderChromeCard(cardChrome{title: s.Title, chips: s.Chips}, rows, width)
}

// optionRows renders the option list with the ❯ pointer on the focused row
// and the focused option's description beneath it.
func (s *Select) optionRows(width int, numbered bool) []string {
	inner := width - cardFrameWidth
	var rows []string
	n := 0
	for i, opt := range s.Options {
		if opt.Header {
			rows = append(rows, dimStyle.Render(clip(opt.Label, inner)))
			continue
		}
		n++
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
