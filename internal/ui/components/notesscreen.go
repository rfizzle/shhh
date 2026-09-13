package components

// The notes screen (docs/interface/surfaces.md#the-supporting-screens): the
// session's shared notebook, as the person reads and corrects it.
//
// It is re-cut from the parts the sources screen is cut from — the selector
// window with its headers grouping the rows, the preview pane beside it, the
// shared chrome around both, and the inline confirm the saved-chat browser
// puts in front of a delete. The list is grouped by the agent that wrote each
// note, because a fan-out's notes arrive interleaved and the question
// somebody asks their own session is who found what.
//
// No row carries a mark. Writing a note changed nothing about the machine and
// reading one is not an act the glyph column names, which is the same reason
// the line a turn closes with leaves its gutter empty: an empty column is
// what says so (docs/interface/principles.md#weight-tracks-risk).
//
// It is a passive component. It owns no notebook semantics: the host formats
// every field and carries out every drop, because who wrote a note and when
// are readings of the session, and this is a renderer.

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

const (
	// notesStackWidth is the width below which the panes stack. A row carries
	// a title and an id and the preview carries a paragraph, so the two
	// columns want the room the other list-and-preview screens want.
	notesStackWidth = 96
	// notesListMin / notesListMax bound the list column. A row is a title
	// with its number after it — fewer fields than the sources row and the
	// same shape as the saved-chat browser's — so it takes very nearly half
	// the terminal and no more: past the ceiling the extra column is worth
	// more to the note being read.
	notesListMin = 30
	notesListMax = 64
	// notesMinPreview is the smallest preview the stacked layout leaves
	// standing: the title, who wrote it, its number and turn, and the first
	// line of the note.
	notesMinPreview = 4
)

// NotesRow is one note, already resolved to what the screen draws. Every
// field arrives as its words — `n3`, `turn 4` — because those are readings of
// the notebook.
type NotesRow struct {
	// ID is the host's own handle on the row, carried back on a result and
	// never drawn on its own.
	ID string
	// Group is the header the row is filed under: the agent that wrote it.
	Group string
	// Label is the note's title, which is the row.
	Label string
	// Number is the note's number as the notebook prints it — `n3` — so a
	// reader can drop it by name from the prompt as well as by key.
	Number string
	// Turn is the turn it was written in, in words — `turn 4`. Empty for a
	// note no surface had said a turn for.
	//
	// It is the whole of when a note happened. The notebook stamps a wall
	// clock as well and no surface has ever printed it: what a reader asks
	// of a note is which round of the work it came out of, and the turn is
	// that.
	Turn string
	// Body is the note itself, a line per paragraph as the host split it.
	Body []string
}

// NotesResult is how a keystroke resolved.
type NotesResult struct {
	// Read is `[enter]` on a note: the host opens ID's body full screen.
	Read bool
	// ID is the note Read names.
	ID string
	// Dropped is the notes the reader has confirmed the loss of — the one
	// under the pointer for `[d]`, every note for a clear. The screen stays
	// up while the host deletes them and hands back fresh Rows, so this
	// arrives with done false.
	Dropped []string
	// Canceled is the way out.
	Canceled bool
}

// NotesScreen is `/notes`: a takeover in the chat, full width, owning the
// keyboard for as long as it is up.
type NotesScreen struct {
	// Rows are the notes in the order the host read them out of the
	// notebook, oldest first.
	Rows []NotesRow
	// Focus is an index into Rows and survives the host rebuilding them.
	Focus int
	// Subject is what the header says the screen is over — `7 notes · 3
	// agents`. The host counts it.
	Subject string
	// MaxLines bounds the screen height; everything pinned comes off the
	// panes' budget. 0 is unbounded.
	MaxLines int
	// Notice is the line a key left behind — what was dropped. The host
	// clears it on the next keystroke.
	Notice string

	list    Select
	order   []int
	optAt   map[int]int
	confirm *Confirm
	// clearing says the armed confirm is over the whole notebook rather than
	// over the note under the pointer, which is the one thing the two
	// questions differ by once either has been asked.
	clearing bool
	keys     bool
}

// AskClear arms the confirm over the whole notebook, which is what `/notes
// clear` opens the screen holding. The question is asked here rather than at
// the prompt so the notes it would take are on the screen while it is
// answered: a count is easier to say yes to than a list.
func (s *NotesScreen) AskClear() {
	s.sync()
	if len(s.Rows) == 0 {
		return
	}
	s.clearing = true
	s.confirm = &Confirm{Prompt: sty.Body.Render(
		"Drop " + plural(len(s.Rows), "note") + "? Nothing else in the session is touched.")}
}

// Update is the screen's whole keyboard. The confirm answers first while it
// is up, because it holds the keyboard and `d` is not a key to it
// (invariant 5).
func (s *NotesScreen) Update(msg tea.KeyPressMsg) (done bool, result NotesResult) {
	s.sync()
	if s.confirm != nil {
		return false, s.answerConfirm(msg)
	}
	pressed := msg.String()
	switch {
	case s.moved(pressed):
		return false, NotesResult{}
	case keys.Is(pressed, keys.Notes.Read):
		// The one key that leaves with something to do. A row is a note and
		// every note has a body, so there is no row it cannot act on.
		if row := s.current(); row != nil {
			return true, NotesResult{Read: true, ID: row.ID}
		}
		return false, NotesResult{}
	case keys.Is(pressed, keys.Notes.Drop):
		// The one key here that destroys something asks first, and the
		// prompt names the note rather than saying "this note".
		if row := s.current(); row != nil {
			s.clearing = false
			s.confirm = &Confirm{Prompt: sty.Body.Render("Drop " + quoted(row.Label) + "?")}
		}
	case keys.Is(pressed, keys.Notes.List):
		s.keys = !s.keys
	case keys.Is(pressed, keys.Notes.Back):
		return true, NotesResult{Canceled: true}
	}
	return false, NotesResult{}
}

// answerConfirm is the keyboard while the drop question is up. Declining
// leaves the notebook exactly as it was, and what a yes takes is read when it
// is answered rather than when it was armed.
func (s *NotesScreen) answerConfirm(msg tea.KeyPressMsg) NotesResult {
	clearing := s.clearing
	answered, yes := confirmed(&s.confirm, msg)
	if !answered {
		return NotesResult{}
	}
	s.clearing = false
	if !yes {
		return NotesResult{}
	}
	if clearing {
		ids := make([]string, 0, len(s.Rows))
		for _, row := range s.Rows {
			ids = append(ids, row.ID)
		}
		return NotesResult{Dropped: ids}
	}
	if row := s.current(); row != nil {
		return NotesResult{Dropped: []string{row.ID}}
	}
	return NotesResult{}
}

// SetSize gives the screen the terminal's rectangle. It lays itself out from
// the width it is rendered at, so only the height is kept.
func (s *NotesScreen) SetSize(_, height int) { s.MaxLines = height }

// View renders the screen: the shared chrome, with the two panes in the rows
// it leaves.
func (s *NotesScreen) View(width int) string {
	if width <= 0 {
		return ""
	}
	s.sync()
	return ScreenChrome{
		Header:   s.header(),
		Foot:     s.footer(width).Rows(width),
		Notice:   s.Notice,
		MaxLines: s.MaxLines,
	}.View(width, func(budget int) []string { return s.panes().rows(width, budget) })
}

// panes is the body, split the way every screen with a list and a preview
// splits it (screenpanes.go).
func (s *NotesScreen) panes() screenPanes {
	return screenPanes{
		stackAt: notesStackWidth, listMin: notesListMin,
		listMax: notesListMax, minPreview: notesMinPreview,
		list:    s.listRows,
		preview: s.previewRows,
	}
}

// listRows is the left pane: the notebook grouped under the agents that
// wrote it.
func (s *NotesScreen) listRows(width, budget int) []string {
	if len(s.Rows) == 0 {
		return []string{sty.Dim.Render(Clip("the notebook is empty", width))}
	}
	body, _ := s.list.visibleRows(cardWidthFor(width), budget, false)
	return body
}

// previewRows is the right pane: the note the pointer is on, whole where it
// fits and opened by `[enter]` where it does not.
func (s *NotesScreen) previewRows(width int) []string {
	row := s.current()
	if row == nil {
		return []string{sty.Dim.Render(Clip("no note selected", width))}
	}
	rows := []string{paneTitle(brightStyle().Render(oneLine(row.Label)),
		sty.Dim.Render(row.Group), width)}
	if field := notesStamp(*row); field != "" {
		rows = append(rows, "  "+sty.Dimmer.Render(Clip(field, max(width-2, 1))))
	}
	rows = append(rows, "")
	for _, para := range row.Body {
		for _, line := range wrapSpans([]styledSpan{{para, sty.Dim}}, max(width-2, 1)) {
			rows = append(rows, "  "+line)
		}
	}
	return rows
}

// footField annotates the key row. What it says is the thing this screen
// exists to say: the notebook is a store the agents write to without asking,
// and taking something out of it is the person's alone.
func (s *NotesScreen) footField() string {
	if len(s.Rows) == 0 {
		return ""
	}
	return "written by the agents; dropping one is yours"
}

// notesStamp is the preview's second line: the note's number and the turn it
// belongs to, joined the way every field run in the product is joined.
func notesStamp(row NotesRow) string {
	fields := make([]string, 0, 2)
	for _, f := range []string{row.Number, row.Turn} {
		if f != "" {
			fields = append(fields, f)
		}
	}
	return strings.Join(fields, " · ")
}

// header names the surface and what it is over.
func (s *NotesScreen) header() ScreenHeader {
	h := ScreenHeader{Left: []RailSegment{screenTitle("/notes")}, Keys: s.headerKeys()}
	if s.Subject != "" {
		h.Left = append(h.Left, screenField(s.Subject))
	}
	return h
}

// headerKeys is the pair the header ends with: the key that shows the whole
// register, and the way back in the one word a header field is
// (docs/interface/surfaces.md#the-supporting-screens).
func (s *NotesScreen) headerKeys() string {
	list := keys.Bracket(keys.Notes.List) + " " + keys.Words(keys.Notes.List)
	if s.keys {
		list = keys.Bracket(keys.Notes.List) + " hide the keys"
	}
	return list + " · " + words(keys.Notes.Back, "back")
}

// footer is the keys the screen offers, the field that annotates them, and
// the confirm where one is armed.
func (s *NotesScreen) footer(width int) KeyFooter {
	field := s.footField()
	f := KeyFooter{Offers: s.offers(width, field), Register: s.keyList(),
		Showing: s.keys, Field: field}
	if s.confirm != nil {
		f.Taken = s.confirm.View(width)
	}
	return f
}

// offers is the key row. An empty notebook has no note to read and none to
// drop, so it is offered neither: a key that cannot act is not an offer
// (invariant 5).
func (s *NotesScreen) offers(width int, field string) []KeyOffer {
	var acts []KeyOffer
	if s.current() != nil {
		acts = append(acts, keyOffer(keys.Notes.Read), keyOffer(keys.Notes.Drop))
	}
	acts = append(acts, wayOut(backToPrompt))
	rungs := [][]KeyOffer{append([]KeyOffer{keyOffer(keys.Notes.Move)}, acts...), acts}
	if field == "" {
		return rungs[0]
	}
	for _, rung := range rungs {
		if fitsBeside(rung, field, width) {
			return rung
		}
	}
	return rungs[0]
}

// keyList is every key the screen has, for `[?]`.
func (s *NotesScreen) keyList() []KeyOffer {
	return []KeyOffer{
		keyOfferAs(keys.Notes.Move, "move between notes"),
		keyOfferAs(keys.Notes.Read, "read the whole note"),
		keyOfferAs(keys.Notes.Drop, "drop it, after confirming it"),
		wayOut(backToPrompt),
		keyOfferAs(keys.Notes.Back, backToPrompt),
	}
}

// sync rebuilds the list from Rows. It runs before every Update and every
// View because the host replaces Rows after every drop, and the pointer has
// to survive that.
func (s *NotesScreen) sync() {
	s.order = s.grouped()
	s.optAt = make(map[int]int, len(s.order))
	opts := make([]SelectOption, 0, len(s.order))
	group := ""
	for _, i := range s.order {
		row := s.Rows[i]
		if row.Group != group {
			group = row.Group
			opts = append(opts, SelectOption{Label: group, Header: true})
		}
		s.optAt[i] = len(opts)
		opts = append(opts, SelectOption{
			Label: oneLine(row.Label), Value: row.Number, Meta: row.Turn,
		})
	}
	s.list.Options = opts
	// The count the window's marker states is of notes, not of options: the
	// author headers are rows on the screen and not things anybody wrote.
	s.list.Total = len(s.order)
	s.list.Unnumbered = true
	s.list.Focus = s.optIndex(s.Focus)
}

// grouped is the display order: the authors in the order they first wrote,
// and each author's notes oldest first under them. It is the order the
// notebook's own listing has always used, and it is the order a signature
// carrying a child's lineage will group by when notes are signed with one —
// the grouping follows the signature rather than being a second reading of
// who wrote what.
func (s *NotesScreen) grouped() []int {
	var groups []string
	byGroup := map[string][]int{}
	for i, row := range s.Rows {
		if _, seen := byGroup[row.Group]; !seen {
			groups = append(groups, row.Group)
		}
		byGroup[row.Group] = append(byGroup[row.Group], i)
	}
	out := make([]int, 0, len(s.Rows))
	for _, g := range groups {
		out = append(out, byGroup[g]...)
	}
	return out
}

// moved walks the pointer between notes, stepping over the headers the way
// every list in the product does. It takes any armed confirm down with it:
// the note it was about is no longer the note under the pointer.
func (s *NotesScreen) moved(pressed string) bool {
	if len(s.order) == 0 {
		return false
	}
	l := List[int]{Items: s.order, Focus: s.at()}
	if !l.Move(pressed, keys.Notes.Move) {
		return false
	}
	s.Focus = s.order[l.Focus]
	s.confirm, s.clearing = nil, false
	s.sync()
	return true
}

// at is where the pointer is among the notes in display order.
func (s *NotesScreen) at() int {
	for i, row := range s.order {
		if row == s.Focus {
			return i
		}
	}
	return 0
}

// current is the note under the pointer, or nil for an empty notebook.
func (s *NotesScreen) current() *NotesRow {
	for _, i := range s.order {
		if i == s.Focus {
			return &s.Rows[i]
		}
	}
	return nil
}

// optIndex maps a row index to its place in the list, which carries a header
// per author as well as the rows.
func (s *NotesScreen) optIndex(row int) int {
	if at, ok := s.optAt[row]; ok {
		return at
	}
	if len(s.order) > 0 {
		return s.optAt[s.order[0]]
	}
	return 0
}
