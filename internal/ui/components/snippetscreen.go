package components

// The snippet browser (docs/interface/surfaces.md#the-supporting-screens).
// `shhh snippets` was the last command still drawing a chrome of its own — a
// style table, a divider, a footer and a detail page nothing else in the
// product used, with the acts on a bar the reader walked with tab. It is
// re-cut here from the parts the family already shares: the shared chrome
// above and below it, the selector window with its markers and its filter
// row, the column grid for the command it is about to run, the one-line field
// for a rename, and the inline confirm in front of the one key that destroys
// something.
//
// It is a list and a preview, split like the history browser's
// (screenpanes.go), and for the same reason: the command is the thing a
// snippet is, so the pane that carries it is what the terminal spends its
// columns on. Nothing runs until `[enter]`, which the footer says in words.
//
// It is a passive component like the rest of this package. It owns no snippet
// semantics: `[c]`, `[r]` and `[x]` resolve to a SnippetCommand the host
// carries out against its own store, and the host hands back fresh Rows.

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

const (
	// snippetStackWidth is the width below which the two panes stack. It is
	// the history browser's, because the pane on the right carries the same
	// thing: a shell command, which is the one field here that runs to any
	// length.
	snippetStackWidth = 96
	// snippetListMin / snippetListMax bound the list's column. A snippet's row
	// is a name and a note about it, which is two fields rather than history's
	// four, so the list takes the smaller half and the command gets the rest.
	snippetListMin = 24
	snippetListMax = 44
	// snippetMinPreview is the smallest preview the stacked layout leaves
	// standing: the name, what it is for, the command and when it was saved.
	snippetMinPreview = 4
)

// SnippetRow is one saved command, already resolved to what the screen draws.
// The host formats every field — when it was saved is `4m ago` — because
// those are readings of the store and this is a renderer.
type SnippetRow struct {
	// ID is the host's own handle on the snippet, carried back on a command
	// and never drawn.
	ID string
	// Name is what the snippet is called: the row's label, what the filter
	// bolds its match in, and what `[r]` renames.
	Name string
	// Description is what it is for, in the row's dim continuation and in the
	// preview under the name.
	Description string
	// Command is what `[enter]` would run. It is the preview's whole subject.
	Command string
	// Saved is when it was last written, in the host's own words.
	Saved string
}

// SnippetAct is what a key asked the host to do to the snippet under the
// pointer. Running is not one of them: it takes the terminal, so it closes
// the screen instead (see SnippetResult).
type SnippetAct int

const (
	// SnippetCopy is `[c]`: the command to the clipboard.
	SnippetCopy SnippetAct = iota
	// SnippetRename is `[r]`, once the rename row has been committed.
	SnippetRename
	// SnippetDelete is `[x]`, and only after the inline confirm has been
	// answered — the screen never resolves a delete the reader has not said
	// yes to.
	SnippetDelete
)

// SnippetCommand is one act the host carries out while the screen stays up.
// The host does it, sets Notice, and hands back fresh Rows.
type SnippetCommand struct {
	Act SnippetAct
	ID  string
	// Name is what the rename row was left holding, and is read for
	// SnippetRename alone.
	Name string
}

// SnippetResult is how the screen closed: with a command to run, or with
// nothing. Run and Canceled are never both true.
type SnippetResult struct {
	Run bool
	// ID and Command are the snippet `[enter]` chose. The command travels
	// with the id so a host that has already closed its store can still run
	// it.
	ID       string
	Command  string
	Canceled bool
	// Do is the housekeeping a key asked for with the screen still up — a
	// copy, a rename, a delete already past its confirm. nil is a key that
	// asked for none.
	Do *SnippetCommand
}

// SnippetScreen is `shhh snippets`: a takeover surface, full width, no
// inspector rail, owning the keyboard for as long as it is up.
type SnippetScreen struct {
	// Rows are the snippets in the order the host read them.
	Rows []SnippetRow
	// Focus is an index into Rows and survives the host rebuilding them.
	Focus int
	// Subject is what the header says the screen is over — `12 snippets`. The
	// host counts it, because counting is a reading of the store.
	Subject string
	// MaxLines bounds the screen height; everything pinned comes off the
	// panes' budget before the window is drawn. 0 is unbounded.
	MaxLines int
	// Notice is the line a key left behind — what was copied, what was
	// deleted. The host clears it on the next keystroke.
	Notice string

	list    Select
	shown   []int
	confirm *Confirm
	rename  *lineEdit
	keys    bool
}

// Update is the screen's whole keyboard. The confirm and the rename row
// answer first while either is up — each holds the keyboard, so `y` is not a
// letter to one and `x` is text to the other (invariant 5).
func (s *SnippetScreen) Update(msg tea.KeyPressMsg) (done bool, result SnippetResult) {
	s.sync()
	if s.confirm != nil {
		return s.updateConfirm(msg)
	}
	if s.rename != nil {
		return s.updateRename(msg)
	}
	pressed := msg.String()
	switch {
	case s.moved(pressed):
		return false, SnippetResult{}
	case keys.Is(pressed, keys.Screen.Rerun):
		// The one key that leaves the screen with something to do. A list the
		// filter emptied has nothing for it to take (invariant 5).
		if row := s.current(); row != nil {
			return true, SnippetResult{Run: true, ID: row.ID, Command: row.Command}
		}
		return false, SnippetResult{}
	case keys.Is(pressed, keys.Select.Cancel):
		return true, SnippetResult{Canceled: true}
	}
	// With the query line open the query line is the surface, so c, r, x and q
	// are letters rather than keys — the reading every picker in the product
	// makes. ctrl+u clears it, and clearing a filter that is already empty
	// closes it, which is how the row keys are got back without leaving the
	// screen.
	if s.list.Filtering {
		if keys.Is(pressed, keys.Screen.ClearQ) && s.list.Query == "" {
			s.list.Filtering = false
			return false, SnippetResult{}
		}
		s.list.editQuery(msg)
		if s.list.QueryChanged() {
			s.refilter()
		}
		return false, SnippetResult{}
	}
	switch {
	case keys.Is(pressed, keys.Screen.Filter):
		s.list.Filtering = true
	case pressed == keys.Shown(keys.Screen.Quit):
		return true, SnippetResult{Canceled: true}
	case keys.Is(pressed, keys.Screen.List):
		s.keys = !s.keys
	case keys.Is(pressed, keys.Screen.Copy):
		if row := s.current(); row != nil {
			return false, SnippetResult{Do: &SnippetCommand{Act: SnippetCopy, ID: row.ID}}
		}
	case keys.Is(pressed, keys.Screen.Rename):
		if row := s.current(); row != nil {
			s.rename = &lineEdit{value: []rune(row.Name), lead: "rename", hint: "type a name"}
		}
	case keys.Is(pressed, keys.Screen.Delete):
		// The one key here that destroys something asks first, and the prompt
		// names what it would take rather than saying "this snippet". What is on
		// disk is untouched, which the question says because a reader deleting a
		// saved command has every reason to wonder.
		if row := s.current(); row != nil {
			s.confirm = &Confirm{Prompt: sty.Body.Render(
				"Delete the snippet " + quoted(row.Name) + "? Files on disk are untouched.")}
		}
	}
	return false, SnippetResult{}
}

// updateConfirm is the keyboard while the delete question is up. Declining
// leaves the list exactly as it was, and the row the answer acts on is the
// one under the pointer when it is answered.
func (s *SnippetScreen) updateConfirm(msg tea.KeyPressMsg) (bool, SnippetResult) {
	if answered, yes := confirmed(&s.confirm, msg); answered && yes {
		if row := s.current(); row != nil {
			return false, SnippetResult{Do: &SnippetCommand{Act: SnippetDelete, ID: row.ID}}
		}
	}
	return false, SnippetResult{}
}

// updateRename is the keyboard while the rename row is up: enter commits,
// esc keeps the name, and everything else is typed into the row. A name that
// was not changed asks the host for nothing.
func (s *SnippetScreen) updateRename(msg tea.KeyPressMsg) (bool, SnippetResult) {
	switch pressed := msg.String(); {
	case keys.Is(pressed, keys.Select.Cancel):
		s.rename = nil
		return false, SnippetResult{}
	case keys.Is(pressed, keys.Screen.Take):
		name := strings.TrimSpace(string(s.rename.value))
		s.rename = nil
		row := s.current()
		if row == nil || name == "" || name == row.Name {
			return false, SnippetResult{}
		}
		return false, SnippetResult{Do: &SnippetCommand{Act: SnippetRename, ID: row.ID, Name: name}}
	}
	s.rename.update(msg)
	return false, SnippetResult{}
}

// SetSize gives the screen the terminal's rectangle. It lays itself out from
// the width it is rendered at, so only the height is kept.
func (s *SnippetScreen) SetSize(_, height int) { s.MaxLines = height }

// View renders the screen: the shared chrome, with the two panes in the rows
// it leaves and the rename row, when one is open, under them.
func (s *SnippetScreen) View(width int) string {
	if width <= 0 {
		return ""
	}
	s.sync()
	panes := screenPanes{
		stackAt: snippetStackWidth, listMin: snippetListMin,
		listMax: snippetListMax, minPreview: snippetMinPreview,
		list:    s.listRows,
		preview: s.previewRows,
	}
	return ScreenChrome{
		Header:   s.header(),
		Foot:     s.footer(width).Rows(width),
		Notice:   s.Notice,
		MaxLines: s.MaxLines,
		Reserve:  len(s.renameRows(width)),
	}.View(width, func(budget int) []string {
		return append(panes.rows(width, budget), s.renameRows(width)...)
	})
}

// renameRows is the open rename row, or nothing. It sits under the panes
// rather than over them so the snippet being renamed stays on screen above
// its own new name, which is where the config screen puts the field it opens
// on a setting.
func (s *SnippetScreen) renameRows(width int) []string {
	if s.rename == nil {
		return nil
	}
	return []string{Clip(s.rename.view(), width)}
}

// listRows is the left pane: the filter row pinned above the selector window,
// the window itself with its markers, and — under it — what the filter hid
// and the key that clears it.
func (s *SnippetScreen) listRows(width, budget int) []string {
	head := s.list.queryRows(cardWidthFor(width))
	if len(head) > 0 {
		head = append(head, screenRule(width))
	}
	tail := s.hiddenRows(width)
	body, _ := s.list.visibleRows(cardWidthFor(width), listBudget(budget, len(head)+len(tail)), false)
	return append(append(head, body...), tail...)
}

// hiddenRows is the line under the list saying what the filter took out of
// it. It is only ever drawn while something is hidden — a filter that hid
// nothing has nothing to confess (invariant 4).
func (s *SnippetScreen) hiddenRows(width int) []string {
	if !s.list.Filtering {
		return nil
	}
	hidden := len(s.Rows) - len(s.shown)
	if hidden <= 0 {
		return nil
	}
	row := sty.Dim.Render(plural(hidden, "snippet")+" hidden by the filter · ") +
		sty.Info.Render(keys.Bracket(keys.Screen.ClearQ)) + sty.Dim.Render(" clear it")
	return []string{screenRule(width), Clip(row, width)}
}

// previewRows is the right pane: the snippet the pointer is on. The name
// leads it with when it was saved right-aligned, what it is for sits under
// that, and the command is a grid row — the same grid the history browser
// draws a recorded command on, because this is the same kind of thing and a
// second grammar for it would be a second thing to learn.
//
// It is a preview, not a second list: nothing in it is focusable and no key
// reaches it.
func (s *SnippetScreen) previewRows(width int) []string {
	row := s.current()
	if row == nil {
		return []string{sty.Dim.Render(Clip("no snippet selected", width))}
	}
	rows := []string{paneTitle(brightStyle().Render(row.Name), sty.Dim.Render(row.Saved), width)}
	if row.Description != "" {
		for _, line := range wrapSpans([]styledSpan{{row.Description, sty.Dim}}, max(width-2, 1)) {
			rows = append(rows, "  "+line)
		}
	}
	rows = append(rows, "")
	// A saved command has no outcome and no duration: it has not run. The kind
	// glyph stands, which is what says it is a shell command.
	return append(rows, commandGridRows(row.Command, "", "", ActivityDone, width)...)
}

// header names the command and what it is over.
func (s *SnippetScreen) header() ScreenHeader {
	head := ScreenHeader{Left: []RailSegment{screenTitle("shhh snippets")}, Keys: screenHeaderKeys()}
	if s.Subject != "" {
		head.Left = append(head.Left, screenField(s.Subject))
	}
	// The query is stated up here as well as on the row it is typed into,
	// because the header is what says what the count under it is a count of:
	// `4 of 12` on the query row is a reading of a list this row has already
	// said is filtered.
	if query := strings.TrimSpace(s.list.Query); query != "" {
		head.Left = append(head.Left, screenField("filtered by "+strconv.Quote(query)))
	}
	return head
}

// footer is the keys the screen offers and the field that annotates them.
// Which keys those are depends on the field, so it is read once here.
func (s *SnippetScreen) footer(width int) KeyFooter {
	field := s.footField()
	f := KeyFooter{Offers: s.offers(width, field), Register: s.keyList(),
		Showing: s.keys, Field: field}
	if s.confirm != nil {
		f.Taken = s.confirm.View(width)
	}
	return f
}

// offers is the key row for whichever surface holds the keyboard. While the
// query line or the rename row is open the list's letters are text, so they
// are not offered: a key that cannot act is not an offer (invariant 5).
//
// The row gives ground for the field beside it in the history browser's own
// order, because it is the same trade: the movement reminder goes first,
// since `[?]` carries it in full and every list in the product moves the same
// way; then the copy, which is the one offer here that is about somewhere
// else; then the filter. Nothing is ever truncated to make room (invariant
// 4) — a segment goes whole or it stays whole.
func (s *SnippetScreen) offers(width int, field string) []KeyOffer {
	if s.rename != nil {
		return []KeyOffer{
			keyOfferAs(keys.Screen.Take, "rename it"),
			keyOfferAs(keys.Screen.ClearQ, "clear the row"),
			keyOfferAs(keys.Screen.Keep, "keep the name"),
		}
	}
	move := keyOffer(keys.Screen.Move)
	var acts []KeyOffer
	if s.current() != nil {
		acts = append(acts, keyOfferAs(keys.Screen.Rerun, "run it"))
	}
	if s.list.Filtering {
		acts = append(acts, keyOfferAs(keys.Screen.ClearQ, "clear the filter, then close it"))
	} else {
		if s.current() != nil {
			acts = append(acts,
				keyOffer(keys.Screen.Copy),
				keyOffer(keys.Screen.Rename),
				keyOffer(keys.Screen.Delete))
		}
		acts = append(acts, keyOffer(keys.Screen.Filter))
	}
	acts = append(acts, keyOfferAs(keys.Select.Cancel, "back to the shell"))

	rungs := [][]KeyOffer{
		append([]KeyOffer{move}, acts...),
		acts,
		without(acts, keys.Bracket(keys.Screen.Copy)),
		without(acts, keys.Bracket(keys.Screen.Copy), keys.Bracket(keys.Screen.Filter)),
	}
	if field == "" {
		return rungs[0]
	}
	for _, rung := range rungs {
		if fitsBeside(rung, field, width) {
			return rung
		}
	}
	// Nothing fits beside the field, so the field goes — and with nothing left
	// to buy, the row keeps every offer it had and wraps.
	return rungs[0]
}

// keyList is every key the screen has, for `[?]`.
func (s *SnippetScreen) keyList() []KeyOffer {
	return []KeyOffer{
		keyOfferAs(keys.Screen.Move, "move between snippets"),
		keyOfferAs(keys.Screen.Rerun, "run the snippet under the pointer"),
		keyOfferAs(keys.Screen.Copy, "copy its command to the clipboard"),
		keyOfferAs(keys.Screen.Rename, "rename it, in a row under the list"),
		keyOfferAs(keys.Screen.Delete, "delete it, after confirming it"),
		keyOfferAs(keys.Screen.Filter, "filter by name or by what the command is"),
		keyOfferAs(keys.Screen.ClearQ, "clear the filter or the rename row"),
		keyOfferAs(keys.Query.Rub, "take a rune back out of either"),
		keyOfferAs(keys.Screen.Keep, "keep the name, or leave the screen"),
		keyOfferAs(keys.Screen.Quit, "back to the shell, running nothing"),
	}
}

// footField annotates the key row with the sentence this screen asks the
// reader to have read before they walk away: nothing here runs by itself.
func (s *SnippetScreen) footField() string {
	if s.current() == nil || s.rename != nil {
		return ""
	}
	return "nothing is run until " + keys.Bracket(keys.Screen.Rerun)
}

// sync rebuilds the list from Rows. It runs before every Update and every
// View because the host replaces Rows after each command, and the window and
// the query the list is showing have to survive that.
func (s *SnippetScreen) sync() {
	s.shown = s.match()
	opts := make([]SelectOption, 0, len(s.shown))
	for _, i := range s.shown {
		row := s.Rows[i]
		opts = append(opts, SelectOption{
			Label: row.Name, Desc: oneLine(row.Description), Meta: row.Saved,
		})
	}
	s.list.Options = opts
	s.list.Total = len(s.Rows)
	s.list.Filterable = true
	s.list.Unnumbered = true
	s.list.QueryHint = "type to filter by name or by command"
	s.list.Focus = s.optIndex(s.Focus)
}

// match is the snippets the query left showing. A snippet is found by its
// name, by what it is for or by the command itself, so a reader who
// remembers any of the three can find it.
func (s *SnippetScreen) match() []int {
	query := strings.ToLower(strings.TrimSpace(s.list.Query))
	out := make([]int, 0, len(s.Rows))
	for i, row := range s.Rows {
		if Matches(query, row.Name, row.Description, row.Command) {
			out = append(out, i)
		}
	}
	return out
}

// refilter re-runs the match after a keystroke changed the query, and puts
// the pointer on the first snippet that survived it — the rows under it are
// not the rows that were there a moment ago.
func (s *SnippetScreen) refilter() {
	s.confirm = nil
	if shown := s.match(); len(shown) > 0 {
		s.Focus = shown[0]
	}
	s.sync()
}

// moved walks the pointer over the snippets the filter left showing and
// reports whether the keystroke was the screen's own movement key. The
// pointer is the snippet's place in the whole list rather than in the
// filtered one, so what moves is a List over what is showing (list.go).
func (s *SnippetScreen) moved(pressed string) bool {
	if len(s.shown) == 0 {
		return false
	}
	l := List[int]{Items: s.shown, Focus: s.at()}
	moved := false
	if s.list.Filtering {
		moved = l.MoveTyping(pressed, keys.Screen.Move)
	} else {
		moved = l.Move(pressed, keys.Screen.Move)
	}
	if !moved {
		return false
	}
	s.Focus = s.shown[l.Focus]
	s.confirm, s.rename = nil, nil
	s.sync()
	return true
}

// at is where the pointer is among the snippets the filter left showing.
func (s *SnippetScreen) at() int {
	for i, row := range s.shown {
		if row == s.Focus {
			return i
		}
	}
	return 0
}

// current is the snippet under the pointer, or nil when the filter left none.
func (s *SnippetScreen) current() *SnippetRow {
	for _, i := range s.shown {
		if i == s.Focus {
			return &s.Rows[i]
		}
	}
	return nil
}

// optIndex maps a row index to its place in the list the card is drawing. A
// row the filter hid takes the first one showing.
func (s *SnippetScreen) optIndex(row int) int {
	for i, at := range s.shown {
		if at == row {
			return i
		}
	}
	return 0
}
