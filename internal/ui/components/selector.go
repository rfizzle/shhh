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
}

func (s *Select) Update(msg tea.KeyMsg) (done bool, result any) {
	switch key := msg.String(); key {
	case "up", "k":
		if s.Focus > 0 {
			s.Focus--
		}
	case "down", "j":
		if s.Focus < len(s.Options)-1 {
			s.Focus++
		}
	case "enter":
		return true, SelectResult{Index: s.Focus}
	case "esc", "ctrl+c":
		return true, SelectResult{Index: -1, Canceled: true}
	default:
		if idx := digitIndex(key, len(s.Options)); idx >= 0 {
			s.Focus = idx
			return true, SelectResult{Index: idx}
		}
	}
	return false, nil
}

func (s *Select) View(width int) string {
	rows := s.optionRows(width, true)
	hint := fmt.Sprintf("↑↓/jk move · enter select · 1–%d jump · esc cancel", len(s.Options))
	rows = append(rows, hintRows([]string{hint}, width)...)
	rows = boundRows(rows, s.MaxLines)
	return renderCard(s.Title, rows, width)
}

// optionRows renders the option list with the ❯ pointer on the focused row
// and the focused option's description beneath it.
func (s *Select) optionRows(width int, numbered bool) []string {
	inner := width - cardFrameWidth
	var rows []string
	for i, opt := range s.Options {
		label := opt.Label
		if numbered {
			label = fmt.Sprintf("%d. %s", i+1, label)
		}
		if i == s.Focus {
			rows = append(rows, focusRowStyle.Render(clip("❯ "+label, inner)))
			if opt.Desc != "" {
				rows = append(rows, dimStyle.Render(clip("    "+opt.Desc, inner)))
			}
		} else {
			rows = append(rows, clip("  "+label, inner))
		}
	}
	return rows
}

// digitIndex maps a number key to an option index, or -1.
func digitIndex(key string, n int) int {
	if len(key) != 1 || key[0] < '1' || key[0] > '9' {
		return -1
	}
	if idx := int(key[0] - '1'); idx < n {
		return idx
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
