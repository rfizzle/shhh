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
	// AllowNone makes an empty answer an answer. A card that is choosing
	// what to do with a list — which proposals to write, which hunks to
	// stage — has nothing to do with none of them, so confirming with
	// nothing checked is a slip and gets the notice. A card that is *setting*
	// a list is the other case: clearing an item's dependencies is exactly
	// what checking nothing means, and refusing it would leave no way to say
	// it.
	AllowNone bool
	// Actions are keys the host answers on the focused row beyond checking
	// it — the proposals card's way into one proposal's header. They are
	// offered on the key row already worded, unlike the single-select's,
	// because a key that means one thing where it is declared can mean a
	// near thing here and the row has to say which: the same `e` opens an
	// item's file in the editor on the backlog screen and one proposal's
	// header on this card. They are offers and are never dropped.
	Actions []KeyOffer
	// Note is the one-line note field under the list, for a card whose
	// answer can carry the reader's own words beside the boxes. Nil is a
	// card with no note, which is every card that had one before this field
	// existed.
	Note *NoteBox
	// Severities rate the rows, one per option, for a list whose rows are
	// decisions rather than choices — the approval queue answered as a list
	// (docs/interface/surfaces.md#the-approval-card). The chip is drawn at
	// the end of the row after the short field, in the words and the tone
	// the queue strip prints the same rating in (severityChip). It is
	// parallel to the options the way Checked is, because it is a fact the
	// caller knows about the row rather than one the row carries: a list of
	// choices has nothing to be rated about, and one that sets none renders
	// exactly as it did before this field existed.
	Severities []Severity
	// Tone is the frame's colour and Lead the sentence the boxes answer,
	// pinned above them — both the single-select's, for the reason the note
	// field is the same field on both cards: a question asked with boxes is
	// the same question asked with rows.
	Tone CardTone
	Lead []string
	// Chips ride the right end of the title border, the single-select's way:
	// a card whose title is what it is asking needs somewhere to say which of
	// several questions this one is.
	Chips  []string
	notice string
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

// checkable is how many rows a key can put a tick in: the whole list less
// the rows that say they cannot be taken.
func (s *MultiSelect) checkable() int {
	n := 0
	for _, o := range s.Options {
		if !o.Dim {
			n++
		}
	}
	return n
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

// moved applies the family's movement keys, and reports whether the
// keystroke was one of them. The card carries no query line, so it is the
// whole pair the register puts on a selector: the arrows and j/k.
func (s *MultiSelect) moved(pressed string) bool {
	l := s.pointer()
	moved := l.Move(pressed, keys.Select.MoveJK)
	s.Focus = l.Focus
	return moved
}

func (s *MultiSelect) Update(msg tea.KeyPressMsg) (done bool, result MultiSelectResult) {
	s.notice = ""
	pressed := msg.String()
	if s.Note != nil {
		s.Note.Settle()
		// Tab moves the keyboard between the list and the field, and while
		// the field has it every letter is text — the space that ticks a box
		// and the `a` that ticks all of them included.
		if keys.Is(pressed, keys.Select.Note) {
			s.Note.Toggle()
			return false, MultiSelectResult{}
		}
		if s.Note.Focused && !keys.Is(pressed, keys.Select.Take) && !keys.Is(pressed, keys.Select.Cancel) {
			s.Note.Update(msg)
			return false, MultiSelectResult{}
		}
	}
	switch {
	case s.moved(pressed):
	case keys.Is(pressed, keys.Select.Toggle):
		if s.Focus < len(s.Checked) {
			if s.Options[s.Focus].Dim {
				// A row that cannot be ticked says why again rather than
				// doing nothing: a key that looks ignored is a key the
				// reader presses harder (invariant 5).
				s.notice = s.Options[s.Focus].UnavailableNotice()
				return false, MultiSelectResult{}
			}
			s.Checked[s.Focus] = !s.Checked[s.Focus]
		}
	case keys.Is(pressed, keys.Select.All):
		// All ↔ none: anything unchecked checks everything, else clears. A
		// row that cannot be ticked is never ticked by it.
		all := s.count() == s.checkable()
		for i := range s.Checked {
			s.Checked[i] = !all && !s.Options[i].Dim
		}
	case keys.Is(pressed, keys.Select.Take):
		if s.count() == 0 && !s.AllowNone {
			s.notice = "nothing selected — " + keys.Bracket(keys.Select.Toggle) +
				" toggles, " + keys.Bracket(keys.Select.Cancel) + " cancels"
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
	if s.Note != nil {
		tail = append(tail, s.Note.Rows(inner)...)
	}
	var segs []KeyOffer
	if s.Note != nil {
		// The field's own key leads, because it is the one this card has
		// that the plain checkbox list does not.
		segs = append(segs, keyOfferAs(keys.Select.Note, "note/options"))
	}
	segs = append(segs,
		keyOffer(keys.Select.Toggle),
		keyOfferAs(keys.Select.All, "all/none"),
	)
	segs = append(segs, s.Actions...)
	segs = append(segs,
		keyOfferAs(keys.Select.Take, fmt.Sprintf("apply (%d)", s.count())),
		keyOffer(keys.Select.Cancel))
	// Handed over as segments and never pre-joined: a row too wide for the
	// terminal takes another row, and a joined one could only be cut in the
	// middle of a clause (docs/interface/principles.md#fold-never-hide).
	tail = append(tail, hintRows(segs, width)...)
	head := leadRows(s.Lead, width)
	rows := append(head, s.visibleRows(width, bodyBudget(s.MaxLines, len(tail)+len(head)))...)
	rows = append(rows, tail...)
	rows = boundRows(rows, s.MaxLines)
	return Card{Title: s.Title, Chips: s.Chips, Tone: s.Tone}.Render(rows, width)
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
	switch {
	case opt.Dim:
		// A row that cannot be ticked draws no box: an empty box is an
		// offer, and the ⊘ the label carries is what says this row is not
		// one (invariant 1).
		box = "   "
	case i < len(s.Checked) && s.Checked[i]:
		box = sty.Add.Render("[x]")
	}
	body := inner - 2
	label := opt.labelText()
	if opt.Dim {
		label = sty.Dimmer.Render(label)
	}
	row := box + " " + label
	// The right-hand run is placed first and the label clipped to what is
	// left, the way the queue strip lays the same two columns out: the label
	// is the only part that can be shortened and still say something, so it
	// is the part that gives up width. A row too narrow to hold both keeps
	// the label whole — below that width the run would be all marker and no
	// fact.
	if right := s.rightRun(i, opt); right != "" {
		if room := body - lipgloss.Width(box) - 1 - lipgloss.Width(right) - 2; room > 0 {
			row = box + " " + Clip(label, room)
			row = padRight(row, body-lipgloss.Width(right)) + right
		}
	}
	row = Clip(row, max(body, 0))
	if i == s.Focus {
		// The pointer sits outside the highlight and the row is lit inside it,
		// which is the pair every list draws: a pointer on the focus ground
		// with an unlit row beside it says the highlight is the mark, and the
		// mark is the pointer (LitRow).
		return sty.FocusPointer.Render("❯") + " " +
			LitRowKeeping(row, 0, lipgloss.Width(box)+1, max(body, 0))
	}
	return PointerColumn() + row
}

// rightRun is the row's right-aligned block: the short field, and after it
// the severity chip where the caller rated the rows. They are built as one
// run and placed once — a row that dropped the field but kept the chip would
// put the rating in the column the reader reads the field in, and a row that
// placed them separately would have to agree with itself twice about where
// the label ends.
func (s *MultiSelect) rightRun(i int, opt SelectOption) string {
	var b strings.Builder
	if meta := opt.metaText(); meta != "" {
		tone := opt.MetaTone.style()
		if opt.Dim {
			tone = sty.Dimmer
		}
		b.WriteString(tone.Render(meta))
	}
	if i < len(s.Severities) {
		// Every row here is a decision the reader is about to answer, so
		// every row is painted — the strip's dimming is for the rows behind
		// the one card, and on this list there is no row behind anything.
		if chip := severityChip(s.Severities[i], true); chip != "" {
			if b.Len() > 0 {
				b.WriteString("  ")
			}
			b.WriteString(chip)
		}
	}
	return b.String()
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
