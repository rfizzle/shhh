package components

// The backlog screen (docs/interface/surfaces.md#the-backlog-screen,
// docs/capabilities/todo.md#the-backlog-is-in-view-and-the-file-is-still-the-item).
//
// The backlog was reachable two ways and neither of them answered "what is
// in here". The rail's block shows four rows of it; the command prints a
// listing and then, for anything beyond reading, asks for a slug typed
// back — `/todo block deps-in-rail` — which is a name the reader has just
// read off the screen and now has to copy. A picker over
// the slugs is right for "open the one I mean" and wrong for "what is in
// here": it shows one item at a time and it cannot change one.
//
// So this is the doctor/history shape over items and nothing new: the
// windowed list on the left, the item's own prose on the right, the shared
// chrome around both. What it removes is the composing, not the asking —
// blocking, archiving and dropping each still ask, and the drop still names
// what it loses.
//
// It is a passive component like the rest of this package. It owns no
// backlog semantics: a key resolves to a BacklogCommand the host carries out
// against the files, and the host hands back fresh rows. That is why the
// screen can draw `waits on parse-header` without knowing what a dependency
// is.

import (
	"fmt"
	"strings"

	"github.com/rfizzle/shhh/internal/ui/keys"
)

const (
	// backlogStackWidth is the width below which the body folds under the
	// list rather than sitting beside it. It is the history browser's own
	// threshold: the row carries four fields there and five here, and the
	// pane beside it is prose rather than one command, so a split narrower
	// than this leaves two columns that each say less than one would.
	backlogStackWidth = 96
	// backlogListMin / backlogListMax bound the list pane. It takes rather
	// less than half — the rows are short fields and the pane beside them is
	// a rendered document, which is the half that needs the room.
	backlogListMin = 34
	backlogListMax = 56
	// backlogMinBody is the smallest body the stacked layout leaves
	// standing: the header row, the dependency row, the rule and one line of
	// prose.
	backlogMinBody = 4
	// minBacklogTitle is the shortest run of a title worth putting on a row.
	// Below it the title has stopped identifying anything and the slug in
	// front of it is doing the whole job, so the row keeps its fields and the
	// pane beside it carries the title in full.
	minBacklogTitle = 12
)

// The tabs, in the order the tab key steps through them: the backlog, the
// sprint that scopes it where there is one, and the archive. The sprint sits
// between them because that is where it sits in the work — a subset of the
// backlog on its way to the archive — and a tab that moved depending on
// whether a sprint was open would be a key whose destination the reader has
// to guess.
const (
	backlogTabItems = iota
	backlogTabSprint
	backlogTabDone
	backlogTabs
)

// The status cycle, in the order its key steps through it. It opens on the
// empty stop, which is "every one of them", so a cycle comes back round to
// no filter rather than trapping the reader inside one. The header fields
// cycle the same way over the words the host declared for them.
var backlogStatuses = []string{"", "open", "in progress", "blocked"}

// BacklogField is one of the header fields an item carries, as the screen
// filters and letters it. Which fields there are and what each may say is
// the host's reading of the project, like every other word on a row.
type BacklogField struct {
	// Name is the header key. The footer names it beside the word a filter
	// stopped on, because "story" alone does not say what it narrowed.
	Name string
	// Values are the words the field may say, in the order the cycle steps
	// through them.
	Values []BacklogValue
}

// BacklogValue is one word a field may say and the letter a row draws it
// as. An empty glyph is a word no row letters, and a field whose words all
// carry none is a field the row leaves to the pane beside it.
type BacklogValue struct {
	Word, Glyph string
}

// stop is one position of a field cycle: which field and which of its
// words, with the zero value meaning no filter at all.
type stop struct {
	field, word string
}

// fieldStops is the flattened cycle the field-filter key steps through:
// every word of every field, in the order the fields were declared, behind
// one empty stop. One key rather than one key per field, because a profile
// may declare four fields and there are not four letters left.
func fieldStops(fields []BacklogField) []stop {
	stops := []stop{{}}
	for _, f := range fields {
		for _, v := range f.Values {
			stops = append(stops, stop{field: f.Name, word: v.Word})
		}
	}
	return stops
}

// BacklogState is what a row's glyph and its state field say about an item.
type BacklogState int

const (
	// BacklogReady is open with every dependency done: it can be started
	// now.
	BacklogReady BacklogState = iota
	// BacklogWaiting is open with a dependency still outstanding.
	BacklogWaiting
	// BacklogRunning is being worked on.
	BacklogRunning
	// BacklogBlocked needs a person before it can move.
	BacklogBlocked
	// BacklogArchived is in the archive: the done tab's rows.
	BacklogArchived
	// BacklogUnreadable is a file that would not parse as an item. It is a
	// row rather than a gap because the file is still there, and a list that
	// dropped it would be saying the work is gone.
	BacklogUnreadable
)

// BacklogRow is one item as the screen draws it. The host reads the header
// and hands over its words — "story", "high", "in progress" — because
// those are readings of the file and this is a renderer.
type BacklogRow struct {
	// Slug is the item's identity: the file's name, what a dependency names,
	// and what a command carried out on this row takes.
	Slug string
	// Path is where the file is, for the one row that has to name it: a
	// file that will not parse is on disk and on no list, and the reader
	// needs to know where to go.
	Path string
	// Title is the sentence the header names it with. It is the field the
	// row gives up first, because the pane beside the list carries it whole.
	Title string
	// Priority and Status are the two words this screen reads whatever the
	// project's vocabulary is: what orders the list, and where the item is
	// in its life.
	Priority, Status string
	// Values are the item's own header fields by name — what sort of work
	// it is, how big — which the field filters match and the row letters.
	// A field the file left unset is absent rather than empty.
	Values map[string]string
	// State picks the glyph and the state field.
	State BacklogState
	// Waits are the dependencies not done yet, in the order the header named
	// them. The row states the first and counts the rest, and `[w]` jumps to
	// it.
	Waits []string
	// Blocks are the active items whose dependencies name this one. They are
	// the other half of the same edge, and the half a listing has never
	// stated: the body's header row carries them.
	Blocks []string
	// Fields are the compact row above the body — the kind, the priority,
	// the size, when it was written. Already formatted, joined here.
	Fields []string
	// Body is the item's prose as Markdown: the sections for an active item,
	// and for an archived one the report of what was actually done.
	Body string
	// Reason is why an unreadable file would not load. It is the body of
	// that row, in the parser's own words.
	Reason string
	// InSprint reports that the open sprint names this item, which is what
	// `[S]` would add or drop.
	InSprint bool
	// Note replaces the row's computed state field with the host's own
	// words. The sprint tab fills it, because where a slug stands in a set
	// is a reading the host made and not one this row can compute.
	Note string
	// Warnings are what was odd about the file without stopping it loading —
	// a size off the scale, a dependency on a slug nothing holds.
	Warnings []string
}

// BacklogAct is what a key asked the host to do to the item under the
// pointer.
type BacklogAct int

const (
	// BacklogEdit is `[e]`: the file in the reader's own editor.
	BacklogEdit BacklogAct = iota
	// BacklogRun is `[R]`: the item worked through to a commit.
	BacklogRun
	// BacklogBlock is `[b]`, past its confirm.
	BacklogBlock
	// BacklogReopen is `[o]`: a blocked item back to open, or an archived
	// one back into the backlog.
	BacklogReopen
	// BacklogArchive is `[d]`, past its confirm.
	BacklogArchive
	// BacklogDrop is `[x]`, past the one confirm that names what it loses.
	BacklogDrop
	// BacklogNew is `[n]`: a new item, which is a card rather than anything
	// this screen can draw.
	BacklogNew
	// BacklogSprintAdd and BacklogSprintDrop are the two halves of `[S]`,
	// which reads the row it is standing on to decide which it is.
	BacklogSprintAdd
	BacklogSprintDrop
	// BacklogGroom is `[g]`: the item read against the tree as it stands.
	// Like a run it spends a turn, so the host closes the screen for it.
	BacklogGroom
	// BacklogSprintTake is the plan card's `[enter]`: write the sprint from
	// the slugs still in the set, in the order they are drawn.
	BacklogSprintTake
	// BacklogSprintGoal is the plan card's `[g]`: say what the set is for.
	// The goal is a sentence and the card has nowhere to type one, so the
	// host takes the keyboard back with the question already asked.
	BacklogSprintGoal
	// BacklogSprintCancel is the plan card's `[esc]`: nothing is written
	// and the proposal is dropped.
	BacklogSprintCancel
)

// BacklogCommand is one act the host carries out. Three of them take the
// terminal or start work — the editor, a run, a new item's card — and the
// host closes the screen for those; the rest change a file and hand back
// fresh rows with the screen still up. Which is which is the host's to know,
// because it is a fact about the session rather than about this list.
type BacklogCommand struct {
	Act  BacklogAct
	Slug string
	// Slugs is the set an act names rather than the one row it stands on.
	// Only the plan card fills it, because it is the only key here whose
	// answer is about a set.
	Slugs []string
}

// BacklogResult is how a key was answered: the screen closed, or an act for
// the host to carry out.
type BacklogResult struct {
	Canceled bool
	// Do is the act a key asked for, once any confirm in front of it has
	// been answered. nil is a key that asked for none.
	Do *BacklogCommand
}

// BacklogScreen is the backlog as a surface: a takeover, full width, owning
// the keyboard for as long as it is up.
type BacklogScreen struct {
	// Rows are the active items in backlog order, and Done the archive, as
	// the host read them.
	Rows []BacklogRow
	Done []BacklogRow
	// Noun is what the project calls one item, singular — "item", "story",
	// "question", "task" — which is what the tally counts in. Empty is
	// "item". A screen that counted questions as items would be naming the
	// backlog by a word that appears nowhere in it.
	Noun string
	// Priority is the field that orders the list. It has a key of its own
	// because every backlog has it and it is what the list is sorted by,
	// so it is the one filter a reader reaches for without reading the
	// footer first.
	Priority BacklogField
	// Fields are the rest of the header's fields, in the order the project
	// declares them: what the field-filter key cycles, and what a row
	// letters after the priority.
	Fields []BacklogField
	// Sprint is the open sprint's name, or empty where the project is
	// working without one. `[S]` is offered only while there is a set to add
	// to.
	Sprint string
	// Board is the sprint tab. nil is a project with no sprint, and the tab
	// is absent rather than empty: a tab that opens on "there is no sprint"
	// is a place the reader learns to stop pressing.
	Board *SprintBoard
	// Plan is the proposal being answered. While it is set the sprint tab
	// draws the card instead of the board and the card holds the keyboard,
	// so none of the screen's own letters is live under it.
	Plan *SprintPlan
	// ReadOnly is a turn in flight. The screen still reads — that is the
	// whole reason it can be opened during one — but every key that would
	// change a file goes inert, because the model may be working from those
	// files and the two would be editing the same item at the same moment.
	ReadOnly bool
	// Why is what the footer says while ReadOnly holds. It is the host's
	// sentence: the session knows what it is doing and this does not.
	Why string
	// Prose lays an item's body out for the pane. It is injected rather than
	// called directly because the renderer this product lays prose out with
	// sits above this package — a component that reached up for it would be
	// an import cycle — and because the transcript's renderer is the one
	// that must draw an item's sections, so that a heading in an item and a
	// heading in an answer are the same heading.
	//
	// A host that supplies none gets the file's own lines, marks and all.
	// That is the honest fallback rather than a degraded one: what the pane
	// is showing then is the file, which is what the item is.
	Prose func(src string, width int) []string
	// MaxLines bounds the screen height. 0 is unbounded.
	MaxLines int
	// Notice is the line a key left behind. The screen clears it on the next
	// keystroke.
	Notice string

	// tab is which of the three the screen is on and focus the pointer per
	// tab, so moving between them keeps every place.
	tab   int
	focus [backlogTabs]int
	// query and filtering are the text filter; the three indices are the
	// cycles' stops and ready is the toggle.
	query     string
	filtering bool
	status    int
	priority  int
	field     int
	ready     bool
	// reading is the body holding the keys, scrolled through pager.
	reading bool
	pager   Pager
	// list is the shared pointer and window over the positions the filters
	// left showing (list.go).
	list  List[int]
	shown []int
	// body is the last markdown render and the row, width and palette it was
	// made for. The screen redraws on every keystroke and a document is
	// parsed rather than formatted, so the render outlives the frame.
	body    []string
	bodyKey string

	confirm *Confirm
	pending *BacklogCommand
	keys    bool
}

// SetSize gives the screen the terminal's rectangle. It lays itself out from
// the width it is rendered at, so only the height is kept.
func (b *BacklogScreen) SetSize(_, height int) { b.MaxLines = height }

// View renders the screen: the shared chrome, with the two panes in the rows
// it leaves.
func (b *BacklogScreen) View(width int) string {
	if width <= 0 {
		return ""
	}
	b.sync()
	return ScreenChrome{
		Header:   b.header(),
		Foot:     b.footRows(width),
		Notice:   b.Notice,
		MaxLines: b.MaxLines,
	}.View(width, func(budget int) []string { return b.paneRows(width, budget) })
}

// header names the surface, which tab it is on, what the filters left and
// how much of the list that is.
func (b *BacklogScreen) header() ScreenHeader {
	h := ScreenHeader{Left: []RailSegment{screenTitle("backlog")}, Keys: b.headerKeys()}
	switch {
	case b.archived():
		h.Left = append(h.Left, screenField("done"))
	case b.planning():
		// The budget is in the header because it is the whole account of
		// why these items and not others: a proposal whose filter is not
		// stated is a recommendation, and this is not one.
		h.Left = append(h.Left, screenField("planning"))
		if b.Plan.Budget != "" {
			h.Left = append(h.Left, screenField(b.Plan.Budget))
		}
	case b.sprinting() && b.Board != nil:
		h.Left = append(h.Left, screenField("sprint"), screenField(b.Board.Name))
	}
	if words := b.filterWords(); words != "" && !b.planning() {
		h.Left = append(h.Left, screenField(words))
	}
	if b.Sprint != "" && b.tab == backlogTabItems {
		h.Left = append(h.Left, screenField(b.Sprint))
	}
	h.Tally = sty.Dim.Render(b.count())
	return h
}

// headerKeys is the pair a takeover ends its header with: the whole register
// and the way out. A surface holding the keyboard with nothing saying how to
// give it back is the thing invariant 5 exists to stop.
func (b *BacklogScreen) headerKeys() string {
	list := keys.Bracket(keys.Backlog.List) + " " + keys.Words(keys.Backlog.List)
	if b.keys {
		list = keys.Bracket(keys.Backlog.List) + " hide the keys"
	}
	// One word, the way every screen in the family ends its header: what the
	// key is for. What it will do is the foot's to say
	// (docs/interface/surfaces.md#the-supporting-screens).
	return list + " · " + words(keys.Backlog.Back, "back")
}

// filterWords is what the header says the filters are, in words rather than
// as a state the reader has to remember pressing into. A list that is
// shorter than the backlog must say why on the screen that shortened it
// (docs/interface/principles.md#fold-never-hide).
func (b *BacklogScreen) filterWords() string {
	var parts []string
	if q := strings.TrimSpace(b.query); q != "" {
		parts = append(parts, "matching "+q)
	}
	if s := backlogStatuses[b.status]; s != "" {
		parts = append(parts, s)
	}
	if p := b.priorityStop(); p != "" {
		parts = append(parts, p+" priority")
	}
	if f := b.fieldStop(); f.field != "" {
		parts = append(parts, f.field+" "+f.word)
	}
	if b.ready {
		parts = append(parts, "ready")
	}
	return strings.Join(parts, " · ")
}

// count is the tally: how many rows the filters left, out of how many there
// are. It states both, because "6 items" on a filtered list is a reading of
// a list that is not the backlog.
func (b *BacklogScreen) count() string {
	if b.planning() {
		return fmt.Sprintf("%d of %s kept", len(b.Plan.Kept()), plural(len(b.Plan.Rows), b.noun()))
	}
	total := len(b.rows())
	if len(b.shown) == total {
		return plural(total, b.noun())
	}
	return fmt.Sprintf("%d of %s", len(b.shown), plural(total, b.noun()))
}

// noun is what one row is called, with the fallback a host that named none
// gets.
func (b *BacklogScreen) noun() string {
	if b.Noun == "" {
		return "item"
	}
	return b.Noun
}

// paneRows is the body, which of the three tabs is up decides what: a
// proposal has the surface to itself, a sprint puts its head over the two
// panes, and everything else is the two panes alone.
func (b *BacklogScreen) paneRows(width, budget int) []string {
	if b.planning() {
		// The proposal is one question about a set, so it has the surface
		// to itself: a list of the backlog beside it would be answering a
		// question nobody asked while one is open.
		return b.planRows(width, budget)
	}
	if b.sprinting() {
		return b.sprintRows(width, budget)
	}
	return b.panes(width, budget)
}

// panes is the two-column body every tab shares: the list beside the item
// where the terminal carries two columns, stacked where it cannot. The body
// never sits beside a list too narrow to read — a pane of prose two columns
// wide is a pane that says nothing.
func (b *BacklogScreen) panes(width, budget int) []string {
	if b.reading {
		// The reader asked for the body, so the body gets the whole surface
		// and the list steps out of the way.
		return b.readingRows(width, budget)
	}
	if width < backlogStackWidth {
		return b.stackedRows(width, budget)
	}
	return b.splitRows(width, budget)
}

// sync rebuilds the window from the rows. It runs before every Update and
// every View because the host replaces the rows after each command, and the
// pointer and the filter have to survive that.
func (b *BacklogScreen) sync() {
	// A proposal opens the tab it is drawn on, and a sprint that closed
	// under the reader steps them back to the backlog rather than leaving
	// them on a tab that is no longer there.
	switch {
	case b.planning():
		b.tab = backlogTabSprint
	case b.sprinting() && !b.sprintTab():
		b.tab = backlogTabItems
	}
	b.shown = b.match()
	b.list.Items = b.shown
	b.list.Focus = b.optIndex(b.focus[b.tab])
	b.list.Normalize()
}

// match is the positions the filters left showing.
func (b *BacklogScreen) match() []int {
	rows := b.rows()
	out := make([]int, 0, len(rows))
	for i, row := range rows {
		if b.matches(row) {
			out = append(out, i)
		}
	}
	return out
}

// matches is the filter rule over one row. A file that will not parse
// answers none of the field filters — it has no fields — and it survives
// them rather than being hidden by one: the row is the only thing on screen
// saying the file is there, and a filter that swallowed it would be hiding
// exactly the item the reader has to go and fix.
func (b *BacklogScreen) matches(row BacklogRow) bool {
	if !Matches(strings.TrimSpace(b.query), row.Slug, row.Title) {
		return false
	}
	if row.State == BacklogUnreadable {
		return true
	}
	if s := backlogStatuses[b.status]; s != "" && s != row.Status {
		return false
	}
	if p := b.priorityStop(); p != "" && p != row.Priority {
		return false
	}
	if f := b.fieldStop(); f.field != "" && f.word != row.Values[f.field] {
		return false
	}
	return !b.ready || row.State == BacklogReady
}

// refilter re-runs the match after a key changed a filter, and puts the
// pointer on the first row that survived it — the rows under it are not the
// rows that were there a moment ago.
func (b *BacklogScreen) refilter() {
	b.confirm, b.pending = nil, nil
	b.reading, b.pager.Offset = false, 0
	if shown := b.match(); len(shown) > 0 {
		b.focus[b.tab] = shown[0]
	}
	b.sync()
}

// moved steps the pointer to the next row the filters left showing, stopping
// at either end rather than wrapping, and reports whether the keystroke was
// the screen's movement key.
func (b *BacklogScreen) moved(pressed string) bool {
	return b.after(b.list.Move(pressed, keys.Backlog.Move))
}

// movedTyping is moved with the filter row open, where a letter is a letter.
func (b *BacklogScreen) movedTyping(pressed string) bool {
	return b.after(b.list.MoveTyping(pressed, keys.Backlog.Move))
}

// after carries a move through to the pointer the tabs remember, and drops
// any confirm the last key armed: the row it was about is no longer the row
// under the pointer.
func (b *BacklogScreen) after(moved bool) bool {
	if !moved || len(b.shown) == 0 {
		return moved
	}
	b.focus[b.tab] = b.shown[min(max(b.list.Focus, 0), len(b.shown)-1)]
	b.confirm, b.pending = nil, nil
	return true
}

// current is the item under the pointer, or nil where the filters left none.
func (b *BacklogScreen) current() *BacklogRow {
	rows := b.rows()
	for _, i := range b.shown {
		if i == b.focus[b.tab] {
			return &rows[i]
		}
	}
	return nil
}

// showing reports whether position i survived the filters.
func (b *BacklogScreen) showing(i int) bool {
	for _, at := range b.shown {
		if at == i {
			return true
		}
	}
	return false
}

// optIndex maps a position in the tab's rows to its place in what is
// showing. A row the filter hid takes the first one that is not.
func (b *BacklogScreen) optIndex(row int) int {
	for i, at := range b.shown {
		if at == row {
			return i
		}
	}
	return 0
}
