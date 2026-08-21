package components

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

// MultiSelectResult is the multi-select Update result: the checked indices in
// order, or Canceled.
type MultiSelectResult struct {
	Indices  []int
	Canceled bool
}

// MultiSelect is the checkbox selector (DESIGN-TUI.md §4b): space toggles,
// a flips all ↔ none, enter applies, esc cancels. Confirming with nothing
// selected is a no-op with a one-line notice, not a confirm.
type MultiSelect struct {
	Title    string
	Options  []SelectOption
	Checked  []bool
	Focus    int
	MaxLines int
	notice   string
}

// NewMultiSelect builds a multi-select with nothing checked.
func NewMultiSelect(title string, options []SelectOption) *MultiSelect {
	return &MultiSelect{Title: title, Options: options, Checked: make([]bool, len(options))}
}

func (s *MultiSelect) count() int {
	n := 0
	for _, c := range s.Checked {
		if c {
			n++
		}
	}
	return n
}

func (s *MultiSelect) Update(msg tea.KeyMsg) (done bool, result any) {
	s.notice = ""
	switch msg.String() {
	case "up", "k":
		if s.Focus > 0 {
			s.Focus--
		}
	case "down", "j":
		if s.Focus < len(s.Options)-1 {
			s.Focus++
		}
	case " ", "space":
		if s.Focus < len(s.Checked) {
			s.Checked[s.Focus] = !s.Checked[s.Focus]
		}
	case "a":
		// All ↔ none: anything unchecked checks everything, else clears.
		all := s.count() == len(s.Checked)
		for i := range s.Checked {
			s.Checked[i] = !all
		}
	case "enter":
		if s.count() == 0 {
			s.notice = "nothing selected — space toggles, esc cancels"
			return false, nil
		}
		var idx []int
		for i, c := range s.Checked {
			if c {
				idx = append(idx, i)
			}
		}
		return true, MultiSelectResult{Indices: idx}
	case "esc", "ctrl+c":
		return true, MultiSelectResult{Canceled: true}
	}
	return false, nil
}

func (s *MultiSelect) View(width int) string {
	inner := width - cardFrameWidth
	var rows []string
	for i, opt := range s.Options {
		box := dimStyle.Render("[ ]")
		if i < len(s.Checked) && s.Checked[i] {
			box = addStyle.Render("[x]")
		}
		row := box + " " + opt.Label
		if i == s.Focus {
			rows = append(rows, focusRowStyle.Render(clip("❯ ", inner))+clip(row, max(inner-2, 0)))
		} else {
			rows = append(rows, "  "+clip(row, max(inner-2, 0)))
		}
	}
	if s.notice != "" {
		rows = append(rows, warnStyle.Render(clip(s.notice, inner)))
	}
	hint := fmt.Sprintf("space toggle · a all/none · enter apply (%d) · esc cancel", s.count())
	rows = append(rows, hintRows([]string{hint}, width)...)
	rows = boundRows(rows, s.MaxLines)
	return renderCard(s.Title, rows, width)
}
