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

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
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

// Update is the screen's whole keyboard. The confirm answers first while it
// is up — it holds the keyboard, and `y` is not a letter to it
// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard,
// invariant 5).
func (b *BacklogScreen) Update(msg tea.KeyPressMsg) (done bool, result BacklogResult) {
	b.Notice = ""
	b.sync()
	if b.confirm != nil {
		return b.updateConfirm(msg)
	}
	pressed := msg.String()
	// The plan card answers every keystroke while it is up, including the
	// way out: a card that is asking for one answer and a screen underneath
	// it that closes on `q` would lose the proposal to a letter.
	if b.planning() {
		return b.updatePlan(pressed)
	}
	// With the query line open the query line is the surface, so every
	// selector letter is a letter — the reading every list in the product
	// makes. ctrl+u clears it, and clearing a filter that is already empty
	// closes it, which is how the row keys are got back without leaving.
	if b.filtering {
		return b.editQuery(msg, pressed)
	}
	if b.reading {
		return b.updateReading(pressed)
	}
	if keys.Is(pressed, keys.Backlog.Back) {
		return true, BacklogResult{Canceled: true}
	}
	if b.readKey(pressed) {
		return false, BacklogResult{}
	}
	return b.stateKey(pressed)
}

// readKey answers the keys that change nothing outside this screen — the
// pointer, the tab, the filters, the register — and reports whether it did.
// They are separated from the keys that change a file because that is the
// line a running turn is drawn along: everything here stays live while the
// model works, and nothing here is offered twice.
func (b *BacklogScreen) readKey(pressed string) bool {
	switch {
	case keys.Is(pressed, keys.Backlog.Move):
		b.move(moveDelta(pressed))
	case keys.Is(pressed, keys.Backlog.Read):
		if b.current() != nil {
			b.reading, b.pager.Offset = true, 0
		}
	case keys.Is(pressed, keys.Backlog.Tab):
		b.swapTab()
	case keys.Is(pressed, keys.Backlog.Filter):
		b.filtering = true
	case keys.Is(pressed, keys.Backlog.Status) && !b.archived():
		b.status = (b.status + 1) % len(backlogStatuses)
		b.refilter()
	case keys.Is(pressed, keys.Backlog.Priority):
		b.priority = (b.priority + 1) % len(b.priorityStops())
		b.refilter()
	case keys.Is(pressed, keys.Backlog.Kind):
		b.field = (b.field + 1) % len(fieldStops(b.Fields))
		b.refilter()
	case keys.Is(pressed, keys.Backlog.Ready) && !b.archived():
		b.ready = !b.ready
		b.refilter()
	case keys.Is(pressed, keys.Backlog.Depends):
		b.jumpToDependency()
	case keys.Is(pressed, keys.Backlog.List):
		b.keys = !b.keys
	default:
		return false
	}
	return true
}

// stateKey answers the keys that change a file. Every one of them is inert
// while the session's own turn is running: the model may be reading these
// files this second, and a key that changed one under it would leave the
// turn working from a header that no longer exists. The keys go grey and the
// footer says why, rather than the surface accepting the press and refusing
// it afterwards — a refusal after the fact is a key that looked live
// (invariant 5).
func (b *BacklogScreen) stateKey(pressed string) (bool, BacklogResult) {
	if b.ReadOnly {
		return false, BacklogResult{}
	}
	// Starting an item is about the backlog rather than about a row, so it
	// answers with the pointer on nothing — which is the list it is most
	// needed on. An empty backlog offering a key that did nothing would be
	// the one screen where the offer is the only thing on it.
	if keys.Is(pressed, keys.Backlog.New) {
		return false, BacklogResult{Do: &BacklogCommand{Act: BacklogNew}}
	}
	row := b.current()
	if row == nil {
		return false, BacklogResult{}
	}
	switch {
	case row.State == BacklogUnreadable:
		// Nothing below this line can be done to a file that will not parse:
		// the verbs are line edits on a header this one does not have. The
		// row is still here, and the way to act on it is the editor.
		if keys.Is(pressed, keys.Backlog.Edit) {
			return false, b.act(BacklogEdit, row.Slug)
		}
	case keys.Is(pressed, keys.Backlog.Edit):
		return false, b.act(BacklogEdit, row.Slug)
	case keys.Is(pressed, keys.Backlog.Run) && !b.archived():
		return false, b.act(BacklogRun, row.Slug)
	case keys.Is(pressed, keys.Backlog.Reopen):
		return false, b.act(BacklogReopen, row.Slug)
	case keys.Is(pressed, keys.Backlog.Block) && !b.archived():
		b.ask(BacklogBlock, row.Slug, "Block "+row.Slug+"?")
	case keys.Is(pressed, keys.Backlog.Archive) && !b.archived():
		b.ask(BacklogArchive, row.Slug, "Archive "+row.Slug+"?")
	case keys.Is(pressed, keys.Backlog.Drop) && !b.archived():
		// The one key here that loses information says so in the question,
		// because the answer to "archive or drop" depends entirely on which
		// of the two this is.
		b.ask(BacklogDrop, row.Slug, "Drop "+row.Slug+"? The file is deleted, not archived.")
	case keys.Is(pressed, keys.Backlog.Groom) && !b.archived():
		return false, b.act(BacklogGroom, row.Slug)
	case keys.Is(pressed, keys.Backlog.Sprint) && b.Sprint != "" && !b.archived():
		if row.InSprint {
			return false, b.act(BacklogSprintDrop, row.Slug)
		}
		return false, b.act(BacklogSprintAdd, row.Slug)
	}
	return false, BacklogResult{}
}

// act is a key that asked for something with nothing to confirm.
func (b *BacklogScreen) act(a BacklogAct, slug string) BacklogResult {
	return BacklogResult{Do: &BacklogCommand{Act: a, Slug: slug}}
}

// ask arms the inline confirm in front of a key that changes a file. The
// prompt names the item rather than saying "this item": the row moves under
// the reader as the filters narrow, and a question that does not say what it
// is about is one that gets answered yes by reflex.
func (b *BacklogScreen) ask(a BacklogAct, slug, prompt string) {
	b.confirm = &Confirm{Prompt: sty.Body.Render(prompt)}
	b.pending = &BacklogCommand{Act: a, Slug: slug}
}

// updateConfirm resolves the armed question. Declining leaves the screen
// exactly as it was, which is what esc promises everywhere else
// (docs/interface/principles.md#esc-is-always-the-safe-answer).
func (b *BacklogScreen) updateConfirm(msg tea.KeyPressMsg) (bool, BacklogResult) {
	done, result := b.confirm.Update(msg)
	if !done {
		return false, BacklogResult{}
	}
	cmd := b.pending
	b.confirm, b.pending = nil, nil
	if yes, _ := result.(bool); yes && cmd != nil {
		return false, BacklogResult{Do: cmd}
	}
	return false, BacklogResult{}
}

// editQuery is the keyboard while the filter row is open. Every letter is a
// letter here, the arrows still move the pointer, and the two keys that are
// not letters close the row.
func (b *BacklogScreen) editQuery(msg tea.KeyPressMsg, pressed string) (bool, BacklogResult) {
	switch {
	case keys.Is(pressed, keys.Backlog.Move):
		// The list under the row is still a list, which is why the movement
		// binding is the arrows and not j/k: a query being typed into has
		// no letters to spare, and this screen would have had to break the
		// pair here as well as on the list.
		b.move(moveDelta(pressed))
		return false, BacklogResult{}
	case keys.Is(pressed, keys.Backlog.ClearQ):
		// An empty filter has nothing left to clear, so the same key closes
		// the row and hands the letters back — the rule every selector in
		// the product answers to.
		if b.query == "" {
			b.filtering = false
			return false, BacklogResult{}
		}
		b.query = ""
	case keys.Is(pressed, keys.Backlog.Back) && pressed != keys.Shown(keys.Backlog.Back):
		// The way out answers to three keystrokes and one of them is `q`.
		// Here `q` is a letter, so only the two that no sentence produces
		// close the row.
		b.filtering, b.query = false, ""
	case pressed == "backspace":
		if r := []rune(b.query); len(r) > 0 {
			b.query = string(r[:len(r)-1])
		}
	default:
		b.query += typedRunes(msg)
	}
	b.refilter()
	return false, BacklogResult{}
}

// updateReading is the keyboard while the body has it: the pager, and the
// way back to the list. Back goes to the list rather than out of the screen,
// because the reader is one level in and esc is a step back rather than an
// exit.
func (b *BacklogScreen) updateReading(pressed string) (bool, BacklogResult) {
	switch {
	case keys.Is(pressed, keys.Backlog.Move):
		b.pager.Offset += moveDelta(pressed)
	case keys.Is(pressed, keys.Backlog.Page):
		b.pager.Offset += pageDelta(pressed) * max(b.pager.Height, 1)
	case keys.Is(pressed, keys.Backlog.Read), keys.Is(pressed, keys.Backlog.Back):
		b.reading = false
	case keys.Is(pressed, keys.Backlog.List):
		b.keys = !b.keys
	}
	return false, BacklogResult{}
}

// moveDelta reads which end of a movement binding was pressed.
func moveDelta(pressed string) int {
	if pressed == "up" {
		return -1
	}
	return 1
}

// pageDelta reads which end of the paging binding was pressed.
func pageDelta(pressed string) int {
	if pressed == "pgup" {
		return -1
	}
	return 1
}

// swapTab steps to the next tab there is. The sprint tab is skipped where
// the project has no sprint, so the key never lands on a tab with nothing
// on it.
//
// The status and ready filters come off on the archive: every archived item
// has the same status, so a status filter carried in there would empty a
// list the reader had just asked to see.
func (b *BacklogScreen) swapTab() {
	for range backlogTabs {
		b.tab = (b.tab + 1) % backlogTabs
		if b.tab != backlogTabSprint || b.sprintTab() {
			break
		}
	}
	b.reading, b.pager.Offset = false, 0
	b.confirm, b.pending = nil, nil
	if b.archived() {
		b.status, b.ready = 0, false
	}
	// The pointer is kept per tab rather than reset, so coming back lands
	// where the reader left off. Dropping the two filters above can only
	// widen what is showing, so the row it was on is still one of them.
	b.sync()
}

// jumpToDependency puts the pointer on the first item the row is waiting on,
// which is what makes the edge something you can follow rather than a slug
// to remember. A dependency the backlog does not hold is the case the row's
// own warning is about, and this says so rather than moving nowhere.
func (b *BacklogScreen) jumpToDependency() {
	row := b.current()
	if row == nil || len(row.Waits) == 0 {
		return
	}
	want := row.Waits[0]
	for i, r := range b.rows() {
		if r.Slug != want {
			continue
		}
		// The pointer may be moving to a row the filters are hiding, so they
		// come off: a jump that landed on nothing would be the filter
		// swallowing the answer to the key that was just pressed.
		if !b.showing(i) {
			b.clearFilters()
		}
		b.focus[b.tab] = i
		b.reading, b.pager.Offset = false, 0
		b.sync()
		return
	}
	b.Notice = "the backlog has no item named " + want
}

// clearFilters puts every filter back to showing everything.
func (b *BacklogScreen) clearFilters() {
	b.query, b.filtering = "", false
	b.status, b.priority, b.field, b.ready = 0, 0, 0, false
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
	return list + " · " + keys.Bracket(keys.Backlog.Back) + " " + keys.Words(keys.Backlog.Back)
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
		return fmt.Sprintf("%d of %s kept", len(b.Plan.Kept()), plural(len(b.Plan.Rows), "item"))
	}
	total := len(b.rows())
	if len(b.shown) == total {
		return plural(total, "item")
	}
	return fmt.Sprintf("%d of %s", len(b.shown), plural(total, "item"))
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

// splitRows is the wide layout: the list on the left and the item beside it.
func (b *BacklogScreen) splitRows(width, budget int) []string {
	listWidth := min(max(width*2/5, backlogListMin), backlogListMax)
	paneWidth := max(width-listWidth-lipgloss.Width(reviewDivider), 8)
	list := b.listRows(listWidth, budget)
	pane := b.itemRows(paneWidth)
	rows := max(len(list), len(pane))
	if budget > 0 {
		// The pane says what it could not fit rather than ending mid-item:
		// a body is longer than a preview, and `[enter]` is what the row
		// naming the rest is pointing at (invariant 4).
		pane = truncRows(pane, budget, paneWidth)
		rows = min(rows, budget)
	}
	return joinReviewPanes(list, pane, listWidth, rows)
}

// sprintRows is the sprint tab: the board's head, then the two panes over
// the set's own items. The head is pinned rather than scrolled with the
// list — what the set is for and how far through it is are the two facts
// the tab exists to state, and a head that scrolled away would leave a
// list of slugs indistinguishable from the backlog's.
func (b *BacklogScreen) sprintRows(width, budget int) []string {
	head := b.boardRows(width)
	if len(head) > 0 {
		head = append(head, screenRule(width))
	}
	if budget <= 0 {
		return append(head, b.panes(width, 0)...)
	}
	// A head taller than the tab leaves no list at all, so it gives ground
	// first: the rows under it are the set, and a board with no set on it
	// is a paragraph.
	if len(head) >= budget-backlogMinBody {
		head = truncRows(head, max(budget-backlogMinBody, 1), width)
	}
	return append(head, b.panes(width, budget-len(head))...)
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

// stackedRows is the narrow layout: the list above, the item below, nothing
// truncated sideways (invariant 4).
func (b *BacklogScreen) stackedRows(width, budget int) []string {
	pane := b.itemRows(width)
	if budget <= 0 {
		return append(append(b.listRows(width, 0), screenRule(width)), pane...)
	}
	// The rule between the panes costs a row.
	avail := budget - 1
	if avail < backlogMinBody+2 {
		// No room for both: the list wins, because a screen that cannot show
		// an item can still say which items there are.
		return truncRows(b.listRows(width, budget), budget, width)
	}
	keep := min(len(pane), max(avail/2, backlogMinBody))
	rows := b.listRows(width, avail-keep)
	rows = append(rows, screenRule(width))
	return append(rows, truncRows(pane, keep, width)...)
}

// readingRows is the body with the surface to itself, scrolled through the
// pager and counted at both ends: a fold that does not say how much it
// folded is a fold nobody can act on.
func (b *BacklogScreen) readingRows(width, budget int) []string {
	rows := b.itemRows(width)
	if budget <= 0 {
		return rows
	}
	if len(rows) <= budget {
		// The whole item is on screen, so no row is spent saying what was
		// folded: the marker is a fold's own account of itself, and a blank
		// line where it would be costs a line of the item for nothing.
		b.pager.Height, b.pager.Total = budget, len(rows)
		return rows
	}
	b.pager.Height = max(budget-1, 1)
	shown := b.pager.Window(rows)
	return append(shown, sty.Dim.Render(Clip(b.scrollNote(), width)))
}

// scrollNote is the row under a folded body saying what is off each end. It
// is only asked for once something has been folded, which is why none of its
// three answers is a blank.
func (b *BacklogScreen) scrollNote() string {
	above, below := b.pager.Above(), b.pager.Below()
	switch {
	case above == 0:
		return fmt.Sprintf("%d more rows below", below)
	case below == 0:
		return fmt.Sprintf("%d rows above", above)
	}
	return fmt.Sprintf("%d rows above · %d below", above, below)
}

// listRows is the left pane: the filter row where it is open, the window of
// items, and the count of what the filter took out from under it.
func (b *BacklogScreen) listRows(width, budget int) []string {
	head := b.queryRows(width)
	if len(head) > 0 {
		head = append(head, screenRule(width))
	}
	tail := b.hiddenRows(width)
	rows := append(head, b.windowRows(width, listBudget(budget, len(head)+len(tail)))...)
	return append(rows, tail...)
}

// queryRows is the filter row: what has been typed, and where the next
// character goes.
func (b *BacklogScreen) queryRows(width int) []string {
	if !b.filtering {
		return nil
	}
	typed := sty.Info.Render(queryPrompt) + sty.QueryText.Render(b.query+queryCursor)
	if b.query == "" {
		typed += sty.Dim.Render(" type to filter by slug or title")
	}
	return []string{Clip(typed, width)}
}

// hiddenRows is the line under the list saying what the filters took out of
// it, and the key that puts them back. It is drawn only while something is
// hidden: a filter that hid nothing has nothing to confess.
func (b *BacklogScreen) hiddenRows(width int) []string {
	hidden := len(b.rows()) - len(b.shown)
	if hidden <= 0 {
		return nil
	}
	row := sty.Dim.Render(fmt.Sprintf("%d hidden · ", hidden)) +
		sty.Info.Render(keys.Bracket(keys.Backlog.ClearQ)) + sty.Dim.Render(" clear it")
	return []string{screenRule(width), Clip(row, width)}
}

// windowRows is the run of items the budget shows, with the pointer inside
// it. An empty list says which of the two empties it is: a backlog with
// nothing in it and a filter that matched nothing are different answers, and
// only one of them is fixed by clearing something.
func (b *BacklogScreen) windowRows(width, budget int) []string {
	if len(b.shown) == 0 {
		if len(b.rows()) == 0 {
			return []string{sty.Dim.Render(Clip(b.emptyWords(), width))}
		}
		return []string{sty.Dim.Render(Clip("nothing matches the filter", width))}
	}
	lo, hi := b.list.Range(budget)
	rows := make([]string, 0, hi-lo)
	for i := lo; i < hi; i++ {
		rows = append(rows, b.itemRow(b.rows()[b.shown[i]], i == b.list.Focus, width))
	}
	if above := lo; above > 0 {
		rows = append([]string{sty.Dim.Render(Clip(fmt.Sprintf("↑ %d above", above), width))}, rows...)
	}
	if below := len(b.shown) - hi; below > 0 {
		rows = append(rows, sty.Dim.Render(Clip(fmt.Sprintf("↓ %d below", below), width)))
	}
	return rows
}

// emptyWords is what an empty tab says. The backlog's own emptiness names
// the key that ends it; the archive's is a statement of fact and offers
// nothing, because nothing here can archive an item that does not exist.
func (b *BacklogScreen) emptyWords() string {
	switch {
	case b.archived():
		return "nothing archived yet"
	case b.sprinting() && b.Board != nil && b.Board.Closed:
		// A closed sprint is a record and its items are back in the
		// backlog's own tabs. Offering a verb that adds one to a set
		// nothing can be added to would be a key that cannot act.
		return "this sprint is closed; its items are in the backlog and the archive"
	case b.sprinting():
		// The set is empty rather than the backlog, and the file is where
		// that is fixed: the sprint's own list is the one thing on this
		// screen no key here writes.
		return "the sprint names no items · /todo sprint add <slug> puts one in"
	}
	return "no items yet · " + keys.Bracket(keys.Backlog.New) + " starts one"
}

// itemRow is one item on the list. The fields go in the order they
// identify it — the slug, the two grade letters, the state — and the title
// takes what is left of the row. The title is the field that gives ground
// because the pane beside the list carries it in full, which is what makes
// the trade a fold rather than a loss (invariant 4).
func (b *BacklogScreen) itemRow(row BacklogRow, focused bool, width int) string {
	glyph, name := b.rowTone(row)
	pointer := "  "
	if focused {
		pointer, name = sty.FocusPointer.Render("❯ "), brightStyle()
	}
	lead := glyph + " " + name.Render(row.Slug)
	if grade := b.grade(row); grade != "" {
		lead += "  " + sty.Dim.Render(grade)
	}
	inner := max(width-2, 1)
	room := inner - lipgloss.Width(lead)
	if room < 4 {
		return pointer + Clip(lead, inner)
	}
	// The state is the field that clips and the title the field that goes.
	// The order is the row's whole argument: what an item is called and where
	// it stands are why the list is on screen, and the title is a sentence
	// the pane beside it carries in full (invariant 4).
	state := Clip("  "+b.stateWords(row), room)
	rest := room - lipgloss.Width(state)
	if row.Title == "" || rest < minBacklogTitle+2 {
		return pointer + lead + sty.Dim.Render(state)
	}
	return pointer + lead + sty.Dim.Render(state+"  "+Clip(row.Title, rest-2))
}

// rowTone is the row's glyph and the weight its slug carries. The four
// states an active item can be in are the rail's, drawn the same way, so a
// row means the same thing in both places; the two this screen adds are the
// archive's tick and the warning on a file that will not parse.
func (b *BacklogScreen) rowTone(row BacklogRow) (string, lipgloss.Style) {
	switch row.State {
	case BacklogUnreadable:
		return sty.Warn.Render("⚠"), sty.Warn
	case BacklogArchived:
		return sty.Add.Render("✓"), sty.Dim
	case BacklogRunning:
		return todoRowTone(TodoRunning)
	case BacklogBlocked:
		return todoRowTone(TodoBlocked)
	case BacklogWaiting:
		return todoRowTone(TodoWaiting)
	}
	return todoRowTone(TodoReady)
}

// grade is the letters that decide the order and the ceremony: the
// priority's, then one for each field the project gave letters to. A field
// the file left unset draws a hyphen rather than a blank, because a blank
// column reads as a field missing from the row and this one is missing from
// the file.
func (b *BacklogScreen) grade(row BacklogRow) string {
	if row.State == BacklogUnreadable {
		// A file that would not parse has no header to read a grade off,
		// and hyphens where the letters go would be this screen claiming
		// it did.
		return ""
	}
	out := b.Priority.glyph(row.Priority)
	for _, f := range b.Fields {
		if f.lettered() {
			out += f.glyph(row.Values[f.Name])
		}
	}
	return out
}

// glyph is the one letter the field draws a word as, and a hyphen for a
// word it does not hold — which is what an unset field and a misspelt one
// both are.
func (f BacklogField) glyph(word string) string {
	for _, v := range f.Values {
		if v.Word == word && v.Glyph != "" {
			return v.Glyph
		}
	}
	return "-"
}

// lettered reports the field carrying letters at all.
func (f BacklogField) lettered() bool {
	for _, v := range f.Values {
		if v.Glyph != "" {
			return true
		}
	}
	return false
}

// priorityStops is the priority cycle: its words behind the empty stop.
func (b *BacklogScreen) priorityStops() []string {
	stops := make([]string, 0, len(b.Priority.Values)+1)
	stops = append(stops, "")
	for _, v := range b.Priority.Values {
		stops = append(stops, v.Word)
	}
	return stops
}

// priorityStop is the word the priority cycle is standing on, and "" for
// the stop that shows everything.
func (b *BacklogScreen) priorityStop() string {
	stops := b.priorityStops()
	if b.priority >= len(stops) {
		return ""
	}
	return stops[b.priority]
}

// fieldStop is where the field cycle is standing.
func (b *BacklogScreen) fieldStop() stop {
	stops := fieldStops(b.Fields)
	if b.field >= len(stops) {
		return stop{}
	}
	return stops[b.field]
}

// stateWords is the row's state field. A waiting item states what it is
// waiting on rather than only that it is waiting: the slug is the reason,
// and `[w]` goes to it.
func (b *BacklogScreen) stateWords(row BacklogRow) string {
	// A row the host gave its own words to says those. It is how the sprint
	// tab draws where a slug stands in the set — finished, waiting, dropped
	// out of the backlog, or the stage the one in flight is at — which is a
	// different reading from the item's status in the backlog.
	if row.Note != "" {
		return row.Note
	}
	switch row.State {
	case BacklogUnreadable:
		return "will not load"
	case BacklogArchived:
		return "done"
	case BacklogRunning:
		return "in progress"
	case BacklogBlocked:
		return "blocked"
	case BacklogWaiting:
		words := "waits on " + row.Waits[0]
		if rest := len(row.Waits) - 1; rest > 0 {
			words += fmt.Sprintf(" +%d", rest)
		}
		return words
	}
	return "ready"
}

// itemRows is the right pane: the item under the pointer, drawn as the file
// it is — the header's fields as one compact row, the edges under them, and
// the sections through the renderer the transcript lays prose out with.
//
// It is a preview, not a second list: nothing in it is focusable, and the
// keys reach it only once `[enter]` has said so.
func (b *BacklogScreen) itemRows(width int) []string {
	row := b.current()
	if row == nil {
		return []string{sty.Dim.Render(Clip("no item selected", width))}
	}
	rows := []string{brightStyle().Render(Clip(row.Slug, width))}
	if row.Title != "" {
		rows = append(rows, wrapDim(row.Title, width)...)
	}
	if fields := b.fieldRow(*row); fields != "" {
		rows = append(rows, sty.Dim.Render(Clip(fields, width)))
	}
	if edges := b.edgeRow(*row); edges != "" {
		rows = append(rows, sty.Dim.Render(Clip(edges, width)))
	}
	for _, w := range row.Warnings {
		rows = append(rows, sty.Warn.Render(Clip("⚠ "+w, width)))
	}
	rows = append(rows, screenRule(width), "")
	return append(rows, b.bodyRows(*row, width)...)
}

// fieldRow is the header's own fields on one line: what sort of work it is,
// how soon, how big, where it stands. The file has them one per line and
// that is right for a file; on a pane beside a list they are a row.
func (b *BacklogScreen) fieldRow(row BacklogRow) string {
	fields := append([]string(nil), row.Fields...)
	if row.InSprint {
		fields = append(fields, "in "+b.Sprint)
	}
	return strings.Join(fields, " · ")
}

// edgeRow is the item's place in the graph, both ways round. A listing has
// always said what an item waits on; what waits on *it* is the half that
// decides whether finishing it is worth anything, and it has never been on
// screen anywhere.
func (b *BacklogScreen) edgeRow(row BacklogRow) string {
	var parts []string
	if len(row.Waits) > 0 {
		parts = append(parts, "waits on "+strings.Join(row.Waits, ", ")+
			" · "+keys.Bracket(keys.Backlog.Depends)+" goes there")
	}
	if len(row.Blocks) > 0 {
		parts = append(parts, plural(len(row.Blocks), "item")+" waits on this: "+
			strings.Join(row.Blocks, ", "))
	}
	return strings.Join(parts, "   ")
}

// bodyRows is the item's prose. An unreadable file has none, and what goes
// there instead is the reason it would not load — which is the one thing
// that row exists to say.
func (b *BacklogScreen) bodyRows(row BacklogRow, width int) []string {
	if row.State == BacklogUnreadable {
		return append(wrapWarn(row.Reason, width),
			sty.Dim.Render(Clip(row.Path+" is still on disk; "+
				keys.Bracket(keys.Backlog.Edit)+" opens it", width)))
	}
	body := strings.TrimSpace(row.Body)
	if body == "" {
		if row.State == BacklogArchived {
			return []string{sty.Dim.Render(Clip("archived without a report", width))}
		}
		return []string{sty.Dim.Render(Clip("nothing written under the header yet", width))}
	}
	key := fmt.Sprintf("%s\x00%d\x00%d\x00%t", row.Slug, b.tab, width, Mono())
	if key != b.bodyKey {
		// A markdown body is parsed rather than formatted, and the screen
		// redraws on every keystroke, so the render is kept until the row,
		// the width or the palette moves under it.
		b.bodyKey = key
		b.body = b.prose(body, width)
	}
	return b.body
}

// prose is the body laid out, through the host's renderer where there is one
// and as the file's own lines where there is not.
func (b *BacklogScreen) prose(body string, width int) []string {
	if b.Prose != nil {
		return b.Prose(body, width)
	}
	var rows []string
	for _, line := range strings.Split(body, "\n") {
		if strings.TrimSpace(line) == "" {
			rows = append(rows, "")
			continue
		}
		rows = append(rows, wrapDim(line, width)...)
	}
	return rows
}

// wrapDim and wrapWarn are a run of prose laid out in the pane's width, in
// the one treatment each is drawn in.
func wrapDim(text string, width int) []string {
	return wrapSpans([]styledSpan{{text, sty.Dim}}, max(width, 1))
}

func wrapWarn(text string, width int) []string {
	return wrapSpans([]styledSpan{{text, sty.Warn}}, max(width, 1))
}

// footRows is the key row and, while a turn is running, the run of keys that
// is not live and the sentence saying why.
func (b *BacklogScreen) footRows(width int) []string {
	f := KeyFooter{
		Offers:   b.offers(),
		Register: b.keyList(),
		Showing:  b.keys,
	}
	if b.confirm != nil {
		f.Taken = b.confirm.View(width)
	}
	rows := f.Rows(width)
	if b.confirm != nil || b.keys || !b.ReadOnly {
		return rows
	}
	// The keys that change a file, in the treatment a surface that cannot
	// press them draws: grey, with their words, under the sentence saying
	// why. It is the approval card's not-yet-live row over a whole key set.
	//
	// The sentence is a row of its own rather than the annotation beside the
	// offers, which is what a footer's field is. A field gives ground to the
	// keys as the terminal narrows, and this one may not: a row of grey keys
	// with nothing left saying why they are grey is a surface that looks
	// broken (invariant 5).
	rows = append(rows, sty.Dim.Render(Clip(b.whyInert(), width)))
	return append(rows, packOffersIn(b.stateOffers(), width, false)...)
}

// whyInert is the sentence over the grey keys. The host's is used where
// there is one, because the session knows what it is doing and this does
// not.
func (b *BacklogScreen) whyInert() string {
	if b.Why != "" {
		return b.Why
	}
	return "a turn is running; these change files it may be working from"
}

// offers is the key row for whichever surface holds the keyboard. While the
// query line is open the row keys are letters, so they are not offered: a
// key that cannot act is not an offer (invariant 5).
func (b *BacklogScreen) offers() []KeyOffer {
	if b.planning() {
		return sprintOffers(b.Plan)
	}
	if b.filtering {
		return []KeyOffer{
			keyOffer(keys.Backlog.Move),
			keyOfferAs(keys.Backlog.ClearQ, "clear the filter, then close it"),
			keyOfferAs(keys.Backlog.Back, "close it"),
		}
	}
	if b.reading {
		return []KeyOffer{
			keyOfferAs(keys.Backlog.Move, "scroll"),
			keyOffer(keys.Backlog.Page),
			keyOfferAs(keys.Backlog.Back, "back to the list"),
		}
	}
	out := []KeyOffer{keyOffer(keys.Backlog.Move)}
	if b.current() != nil {
		out = append(out, keyOffer(keys.Backlog.Read))
	}
	out = append(out, keyOffer(keys.Backlog.Filter), b.narrowOffer(), b.tabOffer())
	if !b.ReadOnly {
		out = append(out, b.stateOffers()...)
	}
	return append(out, keyOfferAs(keys.Backlog.Back, "back to the prompt"))
}

// tabOffer names the tab the key would go to rather than the tab it is on,
// because a key is named for what it does.
func (b *BacklogScreen) tabOffer() KeyOffer {
	switch {
	case b.archived():
		return keyOfferAs(keys.Backlog.Tab, "the backlog")
	case b.sprinting():
		return keyOfferAs(keys.Backlog.Tab, "what shipped")
	case b.sprintTab():
		return keyOfferAs(keys.Backlog.Tab, "the sprint")
	}
	return keyOfferAs(keys.Backlog.Tab, "what shipped")
}

// stateOffers are the keys that change a file: the run the footer greys out
// while a turn is working, and offers live otherwise. They are one list so
// the two treatments cannot come to disagree about which keys they are.
func (b *BacklogScreen) stateOffers() []KeyOffer {
	row := b.current()
	if row == nil {
		// A list with nothing on it still has one act: starting the item
		// that would fill it.
		return []KeyOffer{keyOffer(keys.Backlog.New)}
	}
	var out []KeyOffer
	switch {
	case row.State == BacklogUnreadable:
		// None of the verbs is a line edit this file's header could take;
		// the way to act on it is the editor.
		out = []KeyOffer{keyOfferAs(keys.Backlog.Edit, "fix the header")}
	case b.archived():
		out = []KeyOffer{
			keyOfferAs(keys.Backlog.Reopen, "put it back in the backlog"),
			keyOffer(keys.Backlog.Edit),
		}
	default:
		out = []KeyOffer{keyOffer(keys.Backlog.Edit), keyOffer(keys.Backlog.Run), keyOffer(keys.Backlog.Groom)}
		if row.State == BacklogBlocked {
			out = append(out, keyOffer(keys.Backlog.Reopen))
		} else {
			out = append(out, keyOffer(keys.Backlog.Block))
		}
		out = append(out, keyOffer(keys.Backlog.Archive), keyOffer(keys.Backlog.Drop))
		if b.Sprint != "" {
			out = append(out, b.sprintOffer(*row))
		}
	}
	// Starting an item is about the backlog rather than about the row, so it
	// is offered wherever the pointer is standing — including on the archive
	// and on a file that will not load, which is where a reader who has just
	// found something missing is.
	return append(out, keyOffer(keys.Backlog.New))
}

// sprintOffer is the one key here whose words depend on the row: the same
// act reads as adding or as dropping according to whether the set already
// names this item.
func (b *BacklogScreen) sprintOffer(row BacklogRow) KeyOffer {
	if row.InSprint {
		return keyOfferAs(keys.Backlog.Sprint, "drop it from "+b.Sprint)
	}
	return keyOfferAs(keys.Backlog.Sprint, "add it to "+b.Sprint)
}

// narrowOffer is the four cycle keys as one offer. They are one segment
// because four offers reading "cycle the … filter" would be most of the key
// row for four keys that do one thing, and they are an offer rather than the
// footer's annotation because an annotation gives ground as the terminal
// narrows: a screen whose filters are only findable behind `[?]` is a screen
// whose filters nobody finds.
//
// The archive drops two of them. Every item there has the same status and
// none of them is ready, so those two keys would narrow a list to nothing —
// and a key that cannot act is not an offer (invariant 5).
func (b *BacklogScreen) narrowOffer() KeyOffer {
	shown := []string{keys.Shown(keys.Backlog.Priority)}
	if !b.archived() {
		shown = []string{keys.Shown(keys.Backlog.Status), keys.Shown(keys.Backlog.Priority)}
	}
	// A project whose items carry nothing but a priority has no field
	// cycle, and a key that cannot narrow anything is not an offer
	// (invariant 5).
	if len(b.Fields) > 0 {
		shown = append(shown, keys.Shown(keys.Backlog.Kind))
	}
	if !b.archived() {
		shown = append(shown, keys.Shown(keys.Backlog.Ready))
	}
	return KeyOffer{Key: "[" + strings.Join(shown, "/") + "]", Label: "narrow it"}
}

// keyList is every key the screen has, for `[?]`. While the plan card holds
// the keyboard it is the card's keys and only those: a register listing keys
// the surface in front of the reader does not answer is worse than no
// register.
func (b *BacklogScreen) keyList() []KeyOffer {
	if b.planning() {
		return sprintOffers(b.Plan)
	}
	out := []KeyOffer{
		keyOfferAs(keys.Backlog.Move, "move between items"),
		keyOfferAs(keys.Backlog.Read, "read the body in the pane"),
		keyOfferAs(keys.Backlog.Page, "page the body while reading it"),
		keyOfferAs(keys.Backlog.Tab, "the backlog, or what shipped"),
		keyOfferAs(keys.Backlog.Filter, "filter by slug or title"),
		keyOfferAs(keys.Backlog.ClearQ, "clear the filter; clear it again to close it"),
		keyOfferAs(keys.Backlog.Status, "cycle the status filter"),
		keyOfferAs(keys.Backlog.Priority, "cycle the priority filter"),
	}
	if len(b.Fields) > 0 {
		out = append(out, keyOfferAs(keys.Backlog.Kind, "cycle the next field filter"))
	}
	out = append(out, []KeyOffer{
		keyOfferAs(keys.Backlog.Ready, "only what can be started now"),
		keyOfferAs(keys.Backlog.Depends, "jump to what this one waits on"),
		keyOfferAs(keys.Backlog.Edit, "open the file in your editor"),
		keyOfferAs(keys.Backlog.Run, "work it through to a commit"),
		keyOfferAs(keys.Backlog.Block, "mark it blocked, after confirming it"),
		keyOfferAs(keys.Backlog.Reopen, "reopen it, from the archive as well"),
		keyOfferAs(keys.Backlog.Archive, "archive it, after confirming it"),
		keyOfferAs(keys.Backlog.Drop, "delete the file, after confirming it"),
		keyOfferAs(keys.Backlog.New, "start a new item"),
	}...)
	if b.Sprint != "" {
		out = append(out, keyOfferAs(keys.Backlog.Sprint, "add it to "+b.Sprint+", or drop it"))
	}
	return append(out, keyOfferAs(keys.Backlog.Back, "back to the prompt"))
}

// archived, sprinting and planning are which tab the screen is on and
// whether the proposal is what that tab is showing.
func (b *BacklogScreen) archived() bool  { return b.tab == backlogTabDone }
func (b *BacklogScreen) sprinting() bool { return b.tab == backlogTabSprint }
func (b *BacklogScreen) planning() bool  { return b.Plan != nil }

// sprintTab reports that there is a sprint tab to step onto: a board to
// draw, or a proposal to answer.
func (b *BacklogScreen) sprintTab() bool { return b.Board != nil || b.Plan != nil }

// rows is the tab's own items.
func (b *BacklogScreen) rows() []BacklogRow {
	switch {
	case b.archived():
		return b.Done
	case b.sprinting() && b.Board != nil:
		return b.Board.Rows
	case b.sprinting():
		return nil
	}
	return b.Rows
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

// move steps the pointer to the next row the filters left showing, stopping
// at either end rather than wrapping.
func (b *BacklogScreen) move(delta int) {
	if len(b.shown) == 0 {
		return
	}
	b.list.Move(delta)
	b.focus[b.tab] = b.shown[min(max(b.list.Focus, 0), len(b.shown)-1)]
	b.confirm, b.pending = nil, nil
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
