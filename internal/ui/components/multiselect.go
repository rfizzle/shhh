package components

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// multiSelectResult is the multi-select Update result: the checked indices in
// order, or Canceled.
type multiSelectResult struct {
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
	// KeyList says the host answers `?` over this card with its register and
	// the glyph legend, so the key row offers `[?] keys` in the last slot
	// before the way out. It is drawn and never answered here.
	KeyList bool
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
	Tone cardTone
	Lead []string
	// Chips ride the right end of the title border, the single-select's way:
	// a card whose title is what it is asking needs somewhere to say which of
	// several questions this one is.
	Chips []string
	// CancelLabel is what esc leaves, in the host's own words. Empty is the
	// family's — applying none of the boxes, which is the counterpart of the
	// `apply (N)` beside it (cancelOffer).
	CancelLabel string
	// Columns lays each row out as the label, the Value and the Desc in
	// columns of their own, the widest of each setting the next one's start,
	// for a list whose rows are only told apart by what they mean — the
	// profile drafter's tiers and tools, where `write` alone says nothing a
	// person can decide on. A card that leaves it off draws the label alone,
	// as every checkbox list did before this field existed.
	Columns bool
	// Fixed marks rows that are ticked and stay ticked, one per option like
	// Checked: a grant every answer includes, drawn in its place so the list
	// reads as the whole of what is granted. Space on one says why rather
	// than unticking it, and all-or-none leaves it alone.
	Fixed []bool
	// Warning is the host's standing refusal of the boxes as they are ticked
	// now, drawn under the list and kept until the host clears it; enter is
	// refused while it stands. It is the host's rather than the card's
	// because the rule is — a loader's, which the card cannot know — and the
	// host asks it again after every keystroke, so it is live rather than
	// said once at the take.
	Warning string
	// TakeVerb is the word enter is offered under, before the count, in the
	// host's own words. Empty is the family's `apply`; a question asked with
	// boxes says `choose`, because `apply` is the edit card's word for
	// writing a file.
	TakeVerb string
	// NotYetLive and Handover are the note-selector's, for the reason the
	// note field and the lead are: a question asked with boxes is the same
	// question asked with rows, and it lands beside a live draft the same way
	// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
	NotYetLive bool
	Handover   string
	notice     string
	// list is the shared pointer and window (list.go). A multi-select owns
	// its own Focus, which is why it did not come along when the movement and
	// the window went to the selector.
	list List[SelectOption]
}

// pointer aims the shared list at this card's rows. Every option is one row,
// and every one of them is something a key can land on but a Header, which
// labels or qualifies the rows around it and is stepped over.
func (s *MultiSelect) pointer() *List[SelectOption] {
	s.list.Items, s.list.Focus = s.Options, s.Focus
	s.list.skipFn = func(o SelectOption) bool { return o.Header }
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
		if !o.Dim && !o.Header {
			n++
		}
	}
	return n
}

// fixed reports a row that is ticked and stays ticked.
func (s *MultiSelect) fixed(i int) bool { return i < len(s.Fixed) && s.Fixed[i] }

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

func (s *MultiSelect) Update(msg tea.KeyPressMsg) (done bool, result multiSelectResult) {
	s.notice = ""
	pressed := msg.String()
	if s.Note != nil {
		s.Note.Settle()
		// Tab moves the keyboard between the list and the field, and while
		// the field has it every letter is text — the space that ticks a box
		// and the `a` that ticks all of them included.
		if keys.Is(pressed, keys.Select.Note) {
			s.Note.Toggle()
			return false, multiSelectResult{}
		}
		if s.Note.Focused && !keys.Is(pressed, keys.Select.Take) && !keys.Is(pressed, keys.Select.Cancel) {
			s.Note.Update(msg)
			return false, multiSelectResult{}
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
				return false, multiSelectResult{}
			}
			if s.fixed(s.Focus) {
				s.notice = s.Options[s.Focus].fixedNotice()
				return false, multiSelectResult{}
			}
			s.Checked[s.Focus] = !s.Checked[s.Focus]
		}
	case keys.Is(pressed, keys.Select.All):
		// All ↔ none: anything unchecked checks everything, else clears. A
		// row that cannot be ticked is never ticked by it.
		all := s.count() == s.checkable()
		for i := range s.Checked {
			if s.fixed(i) {
				continue
			}
			s.Checked[i] = !all && !s.Options[i].Dim && !s.Options[i].Header
		}
	case keys.Is(pressed, keys.Select.Take):
		if s.Warning != "" {
			// The refusal is already on the card; enter says it was read
			// rather than doing nothing (invariant 5).
			s.notice = "not taken — the line above says why"
			return false, multiSelectResult{}
		}
		if s.count() == 0 && !s.AllowNone {
			s.notice = "nothing selected — " + keys.Bracket(keys.Select.Toggle) +
				" toggles, " + keys.Bracket(keys.Select.Cancel) + " cancels"
			return false, multiSelectResult{}
		}
		var idx []int
		for i, c := range s.Checked {
			if c {
				idx = append(idx, i)
			}
		}
		return true, multiSelectResult{Indices: idx}
	case keys.Is(pressed, keys.Select.Cancel):
		return true, multiSelectResult{Canceled: true}
	}
	return false, multiSelectResult{}
}

func (s *MultiSelect) View(width int) string {
	inner := width - cardFrameWidth
	// The notice and the key hints are pinned: the list scrolls under them,
	// so what the card spends on them comes off the list's budget before the
	// window is drawn.
	var tail []string
	if s.Warning != "" {
		// Wrapped rather than clipped: the refusal is the loader's sentence,
		// and the part a clip would cut is the part that says what to change.
		for i, line := range wrapPlain(s.Warning, max(inner-2, 8)) {
			mark := "  "
			if i == 0 {
				mark = "⚠ "
			}
			tail = append(tail, sty.warn.Render(Clip(mark+line, inner)))
		}
	}
	if s.notice != "" {
		tail = append(tail, sty.warn.Render(Clip(s.notice, inner)))
	}
	if s.Note != nil {
		tail = append(tail, s.Note.RowsLive(inner, !s.NotYetLive)...)
	}
	tail = append(tail, s.hintRowsFor(width)...)
	head := leadRows(s.Lead, width)
	rows := append(head, s.visibleRows(width, bodyBudget(s.MaxLines, len(tail)+len(head)))...)
	rows = append(rows, tail...)
	rows = boundRows(rows, s.MaxLines)
	return Card{Title: s.Title, chips: s.Chips, tone: s.Tone}.Render(rows, width)
}

// takeVerb is enter's word on the key row: the host's, or `apply`.
func (s *MultiSelect) takeVerb() string {
	if s.TakeVerb != "" {
		return s.TakeVerb
	}
	return "apply"
}

// hintRowsFor is the card's key row, or the handover alone while the draft
// still holds the keyboard — the note-selector's rule, on the same terms: the
// boxes toggle on a bare letter, and a bare letter drawn beside a live draft
// is a letter of the sentence being typed
// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
func (s *MultiSelect) hintRowsFor(width int) []string {
	if s.NotYetLive {
		return notYetLiveRows(s.Handover, width)
	}
	var segs []KeyOffer
	if s.Note != nil {
		// The field's own key leads, because it is the one this card has
		// that the plain checkbox list does not.
		segs = append(segs, keyOfferAs(keys.Select.Note, "note or list"))
	}
	segs = append(segs,
		keyOffer(keys.Select.Toggle),
		keyOfferAs(keys.Select.All, "all/none"),
	)
	segs = append(segs, s.Actions...)
	segs = append(segs,
		keyOfferAs(keys.Select.Take, fmt.Sprintf("%s (%d)", s.takeVerb(), s.count())),
		cancelOffer(s.CancelLabel, applyNone))
	if s.KeyList {
		segs = withKeyListOffer(segs)
	}
	// Handed over as segments and never pre-joined: a row too wide for the
	// terminal takes another row, and a joined one could only be cut in the
	// middle of a clause (docs/interface/principles.md#fold-never-hide).
	return hintRows(segs, width)
}

// visibleRows renders the checkbox list windowed to a body budget, with the
// markers the window makes necessary.
func (s *MultiSelect) visibleRows(width, budget int) []string {
	inner := width - cardFrameWidth
	n := len(s.Options)
	lo, hi := s.pointer().Range(budget)
	var rows []string
	if lo > 0 {
		rows = append(rows, listOverflowRow("↑", lo, s.checkedNote(0, lo), width-cardFrameWidth))
	}
	for i := lo; i < hi; i++ {
		if s.Options[i].Header {
			rows = append(rows, s.headerRow(s.Options[i], s.columns(i), inner))
			continue
		}
		rows = append(rows, s.optionRow(i, inner))
	}
	if hi < n {
		rows = append(rows, listOverflowRow("↓", n-hi, s.checkedNote(hi, n), width-cardFrameWidth))
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
	box := sty.dim.Render("[ ]")
	switch {
	case opt.Dim:
		// A row that cannot be ticked draws no box: an empty box is an
		// offer, and the ⊘ the label carries is what says this row is not
		// one (invariant 1).
		box = "   "
	case i < len(s.Checked) && s.Checked[i]:
		box = sty.add.Render("[x]")
	}
	if s.fixed(i) {
		// Ticked and not a choice: the box is the grant's, in the tone that
		// says it is not the reader's to change.
		box = sty.dim.Render("[x]")
	}
	body := inner - 2
	label := opt.labelText()
	if opt.Dim {
		label = sty.dimmer.Render(label)
	}
	if s.Columns {
		label = s.columnsText(opt, s.columns(i))
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
		return sty.focusPointer.Render("❯") + " " +
			litRowKeeping(row, 0, lipgloss.Width(box)+1, max(body, 0))
	}
	return PointerColumn() + row
}

// multiColumns is the width of the label and the value columns, read off the
// rows a key can land on.
type multiColumns struct{ label, value int }

// columns is where each column of row i starts on a Columns card. The rows
// between two rails are one group and are measured together, so a group of
// short words is not spaced out to the widest name in the group under it; a
// note inside or after a group is laid in that group's columns.
func (s *MultiSelect) columns(i int) multiColumns {
	var c multiColumns
	if !s.Columns || i < 0 || i >= len(s.Options) {
		return c
	}
	rail := func(o SelectOption) bool { return o.Header && o.Desc == "" }
	lo := i
	for lo > 0 && !rail(s.Options[lo]) {
		lo--
	}
	for j := lo; j < len(s.Options); j++ {
		o := s.Options[j]
		if j > lo && rail(o) {
			break
		}
		if o.Header {
			continue
		}
		c.label = max(c.label, lipgloss.Width(o.Label))
		c.value = max(c.value, lipgloss.Width(o.Value))
	}
	return c
}

// columnsText is a row's label, value and description laid in their columns:
// the label as the list's own, the value in its tone and the description dim,
// all three Dimmer on a row that cannot be ticked. That row's ⊘ leads its
// description rather than its label, so the columns stand where they stood
// when a tick elsewhere makes the row one that cannot be ticked.
func (s *MultiSelect) columnsText(opt SelectOption, c multiColumns) string {
	label, value, desc := sty.body, opt.valueTone.style(), sty.dim
	why := opt.Desc
	if opt.Dim {
		label, value, desc = sty.dimmer, sty.dimmer, sty.dimmer
		why = strings.TrimSpace("⊘ " + why)
	}
	text := label.Render(padRight(opt.Label, c.label))
	if c.value > 0 {
		text += "  " + value.Render(padRight(opt.Value, c.value))
	}
	if why != "" {
		text += "  " + desc.Render(why)
	}
	return text
}

// headerRow is a row no key lands on. With no description it is a rail over
// the rows under it; with one it is a note in the list's columns — a fact
// about the rows around it, like the grants every answer carries — drawn
// where a row would be and without a box, because a box is an offer.
func (s *MultiSelect) headerRow(opt SelectOption, c multiColumns, inner int) string {
	if opt.Desc == "" {
		return Clip(" "+sty.dim.Render(opt.Label), inner)
	}
	if opt.Label == "" {
		return Clip(PointerColumn()+sty.dim.Render(opt.Desc), inner)
	}
	text := sty.dim.Render(padRight(opt.Label, c.label))
	if c.value > 0 {
		text += "  " + strings.Repeat(" ", c.value)
	}
	return Clip(PointerColumn()+"    "+text+"  "+sty.dim.Render(opt.Desc), inner)
}

// fixedNotice is what space on a row that stays ticked says: that it does,
// and the reason the row already carries.
func (opt SelectOption) fixedNotice() string {
	reason := opt.Meta
	if reason == "" {
		reason = opt.Desc
	}
	if reason == "" {
		return opt.Label + " stays ticked"
	}
	return opt.Label + " stays ticked — " + reason
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
		tone := opt.metaTone.style()
		if opt.Dim {
			tone = sty.dimmer
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
