package components

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// MultiSelectResult is the multi-select Update result: the checked indices in
// order, or Canceled.
type MultiSelectResult struct {
	Indices  []int
	Canceled bool
}

// MultiSelect is the checkbox selector
// (docs/interface/surfaces.md#selectors): space toggles, a flips all ↔ none,
// enter applies, esc cancels. Confirming with nothing selected is a no-op
// with a one-line notice, not a confirm.
//
// A multi-select that is an ordinary list of choices — `/memory forget`, the
// quality gate's checks — windows like any other list once it outgrows its
// card. Staging is the exception the design names and keeps:
// there you are accounting for every hunk, so hiding four of them behind `↓ 4
// more` would be a trap rather than a fold.
type MultiSelect struct {
	Title    string
	Options  []SelectOption
	Checked  []bool
	Focus    int
	MaxLines int
	notice   string
	// list is the shared pointer and window (list.go). A multi-select owns
	// its own Focus, which is why it did not come along when the movement and
	// the window went to the selector.
	list List[SelectOption]
}

// pointer aims the shared list at this card's rows. Every option is one row —
// a multi-select shows no descriptions — and every one of them is something a
// key can land on, so nothing is skipped and the markers count rows and
// options alike.
func (s *MultiSelect) pointer() *List[SelectOption] {
	s.list.Items, s.list.Focus = s.Options, s.Focus
	return &s.list
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

// step moves the pointer one row, stopping at either end.
func (s *MultiSelect) step(delta int) {
	l := s.pointer()
	l.Move(delta)
	s.Focus = l.Focus
}

func (s *MultiSelect) Update(msg tea.KeyPressMsg) (done bool, result MultiSelectResult) {
	s.notice = ""
	switch pressed := msg.String(); {
	case pressed == "up", pressed == "k":
		s.step(-1)
	case pressed == "down", pressed == "j":
		s.step(1)
	case keys.Is(pressed, keys.Select.Toggle):
		if s.Focus < len(s.Checked) {
			s.Checked[s.Focus] = !s.Checked[s.Focus]
		}
	case keys.Is(pressed, keys.Select.All):
		// All ↔ none: anything unchecked checks everything, else clears.
		all := s.count() == len(s.Checked)
		for i := range s.Checked {
			s.Checked[i] = !all
		}
	case keys.Is(pressed, keys.Select.Take):
		if s.count() == 0 {
			s.notice = "nothing selected — " + keys.Shown(keys.Select.Toggle) +
				" toggles, " + keys.Shown(keys.Select.Cancel) + " cancels"
			return false, MultiSelectResult{}
		}
		var idx []int
		for i, c := range s.Checked {
			if c {
				idx = append(idx, i)
			}
		}
		return true, MultiSelectResult{Indices: idx}
	case keys.Is(pressed, keys.Select.Cancel):
		return true, MultiSelectResult{Canceled: true}
	}
	return false, MultiSelectResult{}
}

func (s *MultiSelect) View(width int) string {
	inner := width - cardFrameWidth
	// The notice and the key hints are pinned: the list scrolls under them,
	// so what the card spends on them comes off the list's budget before the
	// window is drawn.
	var tail []string
	if s.notice != "" {
		tail = append(tail, sty.Warn.Render(Clip(s.notice, inner)))
	}
	hint := strings.Join([]string{
		offer(keys.Select.Toggle),
		words(keys.Select.All, "all/none"),
		words(keys.Select.Take, fmt.Sprintf("apply (%d)", s.count())),
		offer(keys.Select.Cancel),
	}, " · ")
	tail = append(tail, hintRows([]string{hint}, width)...)
	rows := append(s.visibleRows(width, bodyBudget(s.MaxLines, len(tail))), tail...)
	rows = boundRows(rows, s.MaxLines)
	return Card{Title: s.Title}.Render(rows, width)
}

// visibleRows renders the checkbox list windowed to a body budget, with the
// markers the window makes necessary.
func (s *MultiSelect) visibleRows(width, budget int) []string {
	inner := width - cardFrameWidth
	n := len(s.Options)
	lo, hi := s.pointer().Range(budget)
	var rows []string
	if lo > 0 {
		rows = append(rows, ListOverflowRow("↑", lo, s.checkedNote(0, lo), width-cardFrameWidth))
	}
	for i := lo; i < hi; i++ {
		rows = append(rows, s.optionRow(i, inner))
	}
	if hi < n {
		rows = append(rows, ListOverflowRow("↓", n-hi, s.checkedNote(hi, n), width-cardFrameWidth))
	}
	return rows
}

// optionRow lays one checkbox row across the card: the box, the label, and —
// where the caller has one — the short field right-aligned at the end of the
// row. That field is where a staging list states `+34 −6`: the counts
// are what you are deciding about, so they belong on the row rather than in a
// summary underneath it.
func (s *MultiSelect) optionRow(i, inner int) string {
	opt := s.Options[i]
	box := sty.Dim.Render("[ ]")
	if i < len(s.Checked) && s.Checked[i] {
		box = sty.Add.Render("[x]")
	}
	body := inner - 2
	row := box + " " + opt.Label
	if opt.Meta != "" && body-lipgloss.Width(row) >= lipgloss.Width(opt.Meta)+2 {
		row = padRight(row, body-lipgloss.Width(opt.Meta)) + opt.MetaTone.style().Render(opt.Meta)
	}
	row = Clip(row, max(body, 0))
	if i == s.Focus {
		return sty.FocusRow.Render(Clip("❯ ", inner)) + row
	}
	return "  " + row
}

// checkedNote is what a marker adds about the run it is hiding: how many of
// those rows are ticked. A single-select loses nothing by scrolling, but a
// multi-select can scroll the user's own answer off the card, and a count
// that is out of sight is a count that has to be taken on trust. The
// key row states the total the same way it always has — `enter apply (3)` —
// so the two together say how many are checked and where they went.
func (s *MultiSelect) checkedNote(lo, hi int) string {
	n := 0
	for i := lo; i < hi && i < len(s.Checked); i++ {
		if s.Checked[i] {
			n++
		}
	}
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("%d checked", n)
}
