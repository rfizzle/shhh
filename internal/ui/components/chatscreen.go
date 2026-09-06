package components

// The saved-chat browser (docs/interface/surfaces.md#the-supporting-screens,
// docs/capabilities/sessions-and-memory.md#housekeeping). `shhh chats` and
// `--resume` opened a browser of their own — its own list, its own detail
// page, its own footer — and it was the last surface in the product a reader
// could not arrive at knowing the keys already.
//
// It is the history browser's shape over conversations rather than over
// commands: the list on the left, the conversation the pointer is on to the
// right of it, the same filter row, the same window and markers, and the same
// inline confirm in front of the delete. What it has that history does not is
// the rename row, because a conversation is named by whoever saved it and a
// slot called `2026-09-06-1` is one nobody can find again.
//
// A row another running session is autosaving into is listed, filtered, read,
// renamed and deleted like any other, and the one thing it will not do is
// open (fold, never hide — docs/interface/principles.md#fold-never-hide). The
// sentence saying why is the host's: this screen is a renderer, and what a
// held slot means belongs to whoever holds it.

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

const (
	// chatStackWidth is the width below which the two panes stack. It is the
	// history browser's: this list's rows carry a name, a title and a count,
	// and the pane beside them carries the same fields again in full.
	chatStackWidth = 96
	// chatListMin / chatListMax bound the list's column. A conversation is
	// found by its name and its title, which are the two fields that grow
	// here, so the list takes very nearly half the terminal the way history's
	// does.
	chatListMin = 30
	chatListMax = 64
	// chatMinPreview is the smallest preview the stacked layout leaves
	// standing: the name, the title, how many turns and when.
	chatMinPreview = 4
)

// ChatRow is one saved conversation, already resolved to what the screen
// draws. The host formats every field — how many turns, when it was last
// written — because those are readings of the store and this is a renderer.
type ChatRow struct {
	// ID is the slot the host opens, deletes and renames by. It is the name
	// as the store holds it and is never drawn.
	ID string
	// Name is what the slot is called: the row's label and what `[r]`
	// renames.
	Name string
	// Title is what the conversation was about, in the row's continuation and
	// under the name in the preview. Empty for a slot nothing has titled.
	Title string
	// Turns is how long the conversation is, in words — `12 turns`.
	Turns string
	// When is when it was last written, in the row's own words — `Jan 2
	// 15:04` on the row, and the host's longer spelling in the preview
	// through Updated.
	When    string
	Updated string
	// Mark is the short field at the end of the row: why this conversation
	// cannot be opened, in the host's own words. It is a word rather than a
	// colour, because the row beside it is otherwise identical (invariant 1).
	// Empty is a row that opens.
	Mark string
	// Refused is the whole sentence the screen says back when `[enter]` is
	// pressed on a row it cannot open. A refused row keeps its place and
	// everything else it could do: the host writes the sentence, so the
	// screen needs to know nothing about what holds the slot.
	Refused string
	// Deleting is what deleting this conversation also takes, in words — `and
	// its 2 branches` — so the confirm can name it. Empty when nothing else
	// goes.
	Deleting string
}

// ChatAct is what a key asked the host to do to the conversation under the
// pointer. Opening is not one of them: it hands the terminal to a session, so
// it closes the screen instead (see ChatResult).
type ChatAct int

const (
	// ChatRename is `[r]`, once the rename row has been committed.
	ChatRename ChatAct = iota
	// ChatDelete is `[x]`, and only after the inline confirm has been
	// answered — the screen never resolves a delete the reader has not said
	// yes to.
	ChatDelete
)

// ChatCommand is one act the host carries out while the screen stays up. The
// host does it, sets Notice, and hands back fresh Rows.
type ChatCommand struct {
	Act ChatAct
	ID  string
	// Name is what the rename row was left holding, and is read for
	// ChatRename alone.
	Name string
}

// ChatResult is how the screen closed: with a conversation to open, or with
// nothing. Open and Canceled are never both true.
type ChatResult struct {
	Open bool
	// ID is the slot `[enter]` chose.
	ID       string
	Canceled bool
	// Do is the housekeeping a key asked for with the screen still up — a
	// rename, a delete already past its confirm. nil is a key that asked for
	// none.
	Do *ChatCommand
}

// ChatScreen is `shhh chats`: a takeover surface, full width, no inspector
// rail, owning the keyboard for as long as it is up.
type ChatScreen struct {
	// Rows are the conversations in the order the host read them.
	Rows []ChatRow
	// Focus is an index into Rows and survives the host rebuilding them.
	Focus int
	// Subject is what the header says the screen is over — `9 conversations`.
	// The host counts it, because counting is a reading of the store.
	Subject string
	// MaxLines bounds the screen height; everything pinned comes off the
	// panes' budget before the window is drawn. 0 is unbounded.
	MaxLines int
	// Notice is the line a key left behind — what was renamed, what was
	// deleted, why a row would not open. Both write it: the host after a
	// command it carried out, the screen when a key of its own was answered
	// where it stood. It clears on the next keystroke (see Update).
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
func (c *ChatScreen) Update(msg tea.KeyPressMsg) (done bool, result ChatResult) {
	c.sync()
	// The notice is cleared here rather than by the host, which is where the
	// other screens clear theirs: this is the one screen that writes its own —
	// the refusal a row answers with — and a host clearing it after Update had
	// run would wipe the sentence the keystroke just produced.
	c.Notice = ""
	if c.confirm != nil {
		return c.updateConfirm(msg)
	}
	if c.rename != nil {
		return c.updateRename(msg)
	}
	pressed := msg.String()
	switch {
	case c.moved(pressed):
		return false, ChatResult{}
	case keys.Is(pressed, keys.Screen.Take):
		return c.open()
	case keys.Is(pressed, keys.Select.Cancel):
		return true, ChatResult{Canceled: true}
	}
	// With the query line open the query line is the surface, so r, x and q
	// are letters rather than keys — the reading every picker in the product
	// makes. ctrl+u clears it, and clearing a filter that is already empty
	// closes it, which is how the row keys are got back without leaving the
	// screen.
	if c.list.Filtering {
		if keys.Is(pressed, keys.Screen.ClearQ) && c.list.Query == "" {
			c.list.Filtering = false
			return false, ChatResult{}
		}
		c.list.editQuery(msg)
		if c.list.QueryChanged() {
			c.refilter()
		}
		return false, ChatResult{}
	}
	switch {
	case keys.Is(pressed, keys.Screen.Filter):
		c.list.Filtering = true
	case pressed == keys.Shown(keys.Screen.Quit):
		return true, ChatResult{Canceled: true}
	case keys.Is(pressed, keys.Screen.List):
		c.keys = !c.keys
	case keys.Is(pressed, keys.Screen.Rename):
		if row := c.current(); row != nil {
			c.rename = &lineEdit{value: []rune(row.Name), lead: "rename", hint: "type a name"}
		}
	case keys.Is(pressed, keys.Screen.Delete):
		// The one key here that destroys something asks first, and the prompt
		// names what it would take — the conversation and whatever branches go
		// with it — rather than saying "this chat".
		if row := c.current(); row != nil {
			with := ""
			if row.Deleting != "" {
				with = " " + row.Deleting
			}
			c.confirm = &Confirm{Prompt: sty.Body.Render(
				"Delete " + quoted(row.Name) + with + "? Files on disk are untouched.")}
		}
	}
	return false, ChatResult{}
}

// open is `[enter]` on the conversation under the pointer. A row the host
// refused says so where it stands rather than closing the screen: the reader
// is on a row they can still rename or delete, and leaving to report the
// refusal would take that row off the screen along with every other one.
func (c *ChatScreen) open() (bool, ChatResult) {
	row := c.current()
	switch {
	case row == nil:
		return false, ChatResult{}
	case row.Refused != "":
		c.Notice = row.Refused
		return false, ChatResult{}
	}
	return true, ChatResult{Open: true, ID: row.ID}
}

// updateConfirm is the keyboard while the delete question is up. Declining
// leaves the list exactly as it was, and the row the answer acts on is the
// one under the pointer when it is answered.
func (c *ChatScreen) updateConfirm(msg tea.KeyPressMsg) (bool, ChatResult) {
	if answered, yes := confirmed(&c.confirm, msg); answered && yes {
		if row := c.current(); row != nil {
			return false, ChatResult{Do: &ChatCommand{Act: ChatDelete, ID: row.ID}}
		}
	}
	return false, ChatResult{}
}

// updateRename is the keyboard while the rename row is up: enter commits,
// esc keeps the name, and everything else is typed into the row. A name that
// was not changed asks the host for nothing.
func (c *ChatScreen) updateRename(msg tea.KeyPressMsg) (bool, ChatResult) {
	switch pressed := msg.String(); {
	case keys.Is(pressed, keys.Select.Cancel):
		c.rename = nil
		return false, ChatResult{}
	case keys.Is(pressed, keys.Screen.Take):
		name := strings.TrimSpace(string(c.rename.value))
		c.rename = nil
		row := c.current()
		if row == nil || name == "" || name == row.Name {
			return false, ChatResult{}
		}
		return false, ChatResult{Do: &ChatCommand{Act: ChatRename, ID: row.ID, Name: name}}
	}
	c.rename.update(msg)
	return false, ChatResult{}
}

// SetSize gives the screen the terminal's rectangle. It lays itself out from
// the width it is rendered at, so only the height is kept.
func (c *ChatScreen) SetSize(_, height int) { c.MaxLines = height }

// View renders the screen: the shared chrome, with the two panes in the rows
// it leaves and the rename row, when one is open, under them.
func (c *ChatScreen) View(width int) string {
	if width <= 0 {
		return ""
	}
	c.sync()
	panes := screenPanes{
		stackAt: chatStackWidth, listMin: chatListMin,
		listMax: chatListMax, minPreview: chatMinPreview,
		list:    c.listRows,
		preview: c.previewRows,
	}
	return ScreenChrome{
		Header:   c.header(),
		Foot:     c.footer(width).Rows(width),
		Notice:   c.Notice,
		MaxLines: c.MaxLines,
		Reserve:  len(c.renameRows(width)),
	}.View(width, func(budget int) []string {
		return append(panes.rows(width, budget), c.renameRows(width)...)
	})
}

// renameRows is the open rename row, or nothing. It sits under the panes so
// the conversation being renamed stays on screen above its own new name.
func (c *ChatScreen) renameRows(width int) []string {
	if c.rename == nil {
		return nil
	}
	return []string{Clip(c.rename.view(), width)}
}

// listRows is the left pane: the filter row pinned above the selector window,
// the window itself with its markers, and — under it — what the filter hid
// and the key that clears it.
func (c *ChatScreen) listRows(width, budget int) []string {
	head := c.list.queryRows(cardWidthFor(width))
	if len(head) > 0 {
		head = append(head, screenRule(width))
	}
	tail := c.hiddenRows(width)
	body, _ := c.list.visibleRows(cardWidthFor(width), listBudget(budget, len(head)+len(tail)), false)
	return append(append(head, body...), tail...)
}

// hiddenRows is the line under the list saying what the filter took out of
// it. It is only ever drawn while something is hidden — a filter that hid
// nothing has nothing to confess (invariant 4).
func (c *ChatScreen) hiddenRows(width int) []string {
	if !c.list.Filtering {
		return nil
	}
	hidden := len(c.Rows) - len(c.shown)
	if hidden <= 0 {
		return nil
	}
	row := sty.Dim.Render(plural(hidden, "conversation")+" hidden by the filter · ") +
		sty.Info.Render(keys.Bracket(keys.Screen.ClearQ)) + sty.Dim.Render(" clear it")
	return []string{screenRule(width), Clip(row, width)}
}

// previewRows is the right pane: the conversation the pointer is on. The name
// leads it with when it was last written right-aligned, the title sits under
// that, and the last line is how long it is — and, where the host refused it,
// why it will not open.
//
// It is a preview, not a second list: nothing in it is focusable and no key
// reaches it.
func (c *ChatScreen) previewRows(width int) []string {
	row := c.current()
	if row == nil {
		return []string{sty.Dim.Render(Clip("no conversation selected", width))}
	}
	rows := []string{paneTitle(brightStyle().Render(row.Name), sty.Dim.Render(row.Updated), width)}
	if row.Title != "" {
		for _, line := range wrapSpans([]styledSpan{{row.Title, sty.Dim}}, max(width-2, 1)) {
			rows = append(rows, "  "+line)
		}
	}
	rows = append(rows, "", "  "+sty.Body.Render(Clip(row.Turns, max(width-2, 1))))
	if row.Refused != "" {
		rows = append(rows, "")
		for _, line := range wrapSpans([]styledSpan{{row.Refused, sty.Dim}}, max(width-2, 1)) {
			rows = append(rows, "  "+line)
		}
	}
	return rows
}

// header names the command and what it is over.
func (c *ChatScreen) header() ScreenHeader {
	head := ScreenHeader{Left: []RailSegment{screenTitle("shhh chats")}, Keys: screenHeaderKeys()}
	if c.Subject != "" {
		head.Left = append(head.Left, screenField(c.Subject))
	}
	if query := strings.TrimSpace(c.list.Query); query != "" {
		head.Left = append(head.Left, screenField("filtered by "+strconv.Quote(query)))
	}
	return head
}

// footer is the keys the screen offers and the field that annotates them.
func (c *ChatScreen) footer(width int) KeyFooter {
	f := KeyFooter{Offers: c.offers(), Register: c.keyList(), Showing: c.keys,
		Field: c.footField()}
	if c.confirm != nil {
		f.Taken = c.confirm.View(width)
	}
	return f
}

// offers is the key row for whichever surface holds the keyboard. While the
// query line or the rename row is open the list's letters are text, so they
// are not offered: a key that cannot act is not an offer (invariant 5).
func (c *ChatScreen) offers() []KeyOffer {
	if c.rename != nil {
		return []KeyOffer{
			keyOfferAs(keys.Screen.Take, "rename it"),
			keyOfferAs(keys.Screen.ClearQ, "clear the row"),
			keyOfferAs(keys.Screen.Keep, "keep the name"),
		}
	}
	offers := []KeyOffer{keyOffer(keys.Screen.Move)}
	// A conversation another session holds is not opened, so the key that
	// would open it is not offered on that row (invariant 5). Everything else
	// on the row still is.
	if row := c.current(); row != nil && row.Refused == "" {
		offers = append(offers, keyOfferAs(keys.Screen.Take, "open it"))
	}
	if c.list.Filtering {
		offers = append(offers, keyOfferAs(keys.Screen.ClearQ, "clear the filter, then close it"))
	} else {
		if c.current() != nil {
			offers = append(offers, keyOffer(keys.Screen.Rename), keyOffer(keys.Screen.Delete))
		}
		offers = append(offers, keyOffer(keys.Screen.Filter))
	}
	return append(offers, wayOut(backToShell))
}

// keyList is every key the screen has, for `[?]`.
func (c *ChatScreen) keyList() []KeyOffer {
	return []KeyOffer{
		keyOfferAs(keys.Screen.Move, "move between conversations"),
		keyOfferAs(keys.Screen.Take, "open the one under the pointer"),
		keyOfferAs(keys.Screen.Rename, "rename it, in a row under the list"),
		keyOfferAs(keys.Screen.Delete, "delete it and its branches, after confirming it"),
		keyOfferAs(keys.Screen.Filter, "filter by name or by what it was about"),
		keyOfferAs(keys.Screen.ClearQ, "clear the filter or the rename row"),
		keyOfferAs(keys.Query.Rub, "take a rune back out of either"),
		keyOfferAs(keys.Screen.Keep, "keep the name, or "+backToShell),
		keyOfferAs(keys.Screen.Quit, backToShell),
	}
}

// footField annotates the key row with why the row under the pointer will not
// open, which is the one thing about this screen a reader cannot work out
// from the row itself.
func (c *ChatScreen) footField() string {
	if row := c.current(); row != nil && row.Mark != "" && c.rename == nil {
		return row.Mark
	}
	return ""
}

// sync rebuilds the list from Rows. It runs before every Update and every
// View because the host replaces Rows after each command, and the window and
// the query the list is showing have to survive that.
func (c *ChatScreen) sync() {
	c.shown = c.match()
	opts := make([]SelectOption, 0, len(c.shown))
	for _, i := range c.shown {
		row := c.Rows[i]
		opts = append(opts, SelectOption{
			Label: row.Name, Desc: chatDesc(row), Meta: row.Mark,
		})
	}
	c.list.Options = opts
	c.list.Total = len(c.Rows)
	c.list.Filterable = true
	c.list.Unnumbered = true
	c.list.QueryHint = "type to filter by name or by what it was about"
	c.list.Focus = c.optIndex(c.Focus)
}

// chatDesc is the row's continuation: what the conversation was about, how
// long it is and when it was last written, in that order — the title is what
// a reader scans for and the two readings qualify it.
func chatDesc(row ChatRow) string {
	fields := make([]string, 0, 3)
	for _, field := range []string{oneLine(row.Title), row.Turns, row.When} {
		if field != "" {
			fields = append(fields, field)
		}
	}
	return strings.Join(fields, " · ")
}

// match is the conversations the query left showing. One is found by its name
// or by what it was about, which is the pair a reader looking for a
// conversation they half remember has to work with.
func (c *ChatScreen) match() []int {
	query := strings.ToLower(strings.TrimSpace(c.list.Query))
	out := make([]int, 0, len(c.Rows))
	for i, row := range c.Rows {
		if Matches(query, row.Name, row.Title) {
			out = append(out, i)
		}
	}
	return out
}

// refilter re-runs the match after a keystroke changed the query, and puts
// the pointer on the first conversation that survived it — the rows under it
// are not the rows that were there a moment ago.
func (c *ChatScreen) refilter() {
	c.confirm = nil
	if shown := c.match(); len(shown) > 0 {
		c.Focus = shown[0]
	}
	c.sync()
}

// moved walks the pointer over the conversations the filter left showing and
// reports whether the keystroke was the screen's own movement key. The
// pointer is the conversation's place in the whole list rather than in the
// filtered one, so what moves is a List over what is showing (list.go).
func (c *ChatScreen) moved(pressed string) bool {
	if len(c.shown) == 0 {
		return false
	}
	l := List[int]{Items: c.shown, Focus: c.at()}
	moved := false
	if c.list.Filtering {
		moved = l.MoveTyping(pressed, keys.Screen.Move)
	} else {
		moved = l.Move(pressed, keys.Screen.Move)
	}
	if !moved {
		return false
	}
	c.Focus = c.shown[l.Focus]
	c.confirm, c.rename = nil, nil
	c.sync()
	return true
}

// at is where the pointer is among the conversations the filter left showing.
func (c *ChatScreen) at() int {
	for i, row := range c.shown {
		if row == c.Focus {
			return i
		}
	}
	return 0
}

// current is the conversation under the pointer, or nil when the filter left
// none.
func (c *ChatScreen) current() *ChatRow {
	for _, i := range c.shown {
		if i == c.Focus {
			return &c.Rows[i]
		}
	}
	return nil
}

// optIndex maps a row index to its place in the list the card is drawing. A
// row the filter hid takes the first one showing.
func (c *ChatScreen) optIndex(row int) int {
	for i, at := range c.shown {
		if at == row {
			return i
		}
	}
	return 0
}
