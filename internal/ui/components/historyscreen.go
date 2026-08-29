package components

// The history browser (S-128, DESIGN-TUI.md §19b, ui_kits/cockpit/Tools.html).
// `shhh history` shipped on `internal/ui/browse`, which invented a list, a
// query line, a detail page and an action bar of its own. It is re-cut here
// from parts that already exist: the §4a window with its markers, its filter
// row and its two counts; the §6a grid for the entry it selects; and the §5
// inline confirm in front of the one key that destroys something.
//
// Two panes and one rule shape it, both from §19b. The search is on the left
// and the entry it selects on the right — the right pane is a preview, not a
// second list, and has no cursor of its own. And nothing is re-run until
// `[enter]`, which the hint line says in words, because a browser over past
// shell commands that runs one by accident is the worst thing this screen
// could do.
//
// It is a passive component like the rest of this package. It owns no history
// semantics: `[c]`, `[s]` and `[x]` resolve to a HistoryCommand the host
// carries out against its own store, and the host hands back fresh Rows.
// That is why the screen can draw `exit 3` in del without knowing what an
// exit code is.

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

const (
	// historyStackWidth is the width below which the two panes stack rather
	// than sitting side by side. It is above §16a's own threshold because
	// this list's rows carry four fields and the preview carries a shell
	// command: two columns of 40 would clip both.
	historyStackWidth = 96
	// historyListMin / historyListMax bound the search pane. History is the
	// longest list in the product (§19b) and its rows carry four fields, so
	// it takes very nearly half the terminal — the artboard's own split —
	// rather than review's two fifths. Below the floor the outcome starts
	// dropping off rows that exist to be read for it.
	historyListMin = 30
	historyListMax = 64
	// historyMinPreview is the smallest preview the stacked layout leaves
	// standing: the title, the prompt, the command and its outcome.
	historyMinPreview = 4
	// minHistoryLabel is the shortest run of a prompt worth putting on a
	// row. Below it the row has stopped identifying anything, so the grid's
	// own drop order is the better answer.
	minHistoryLabel = 24
	// minClosestPrefix is the shortest run of the query that naming a near
	// miss will stand on.
	minClosestPrefix = 3
)

// HistoryRow is one past generation, already resolved to what the screen
// draws. The host formats every field — how long ago is "4m ago", an exit
// code is "exit 0", a duration is "1.4s" — because those are readings of the
// store and this is a renderer.
type HistoryRow struct {
	// ID is the host's own handle on the entry, carried back on a command
	// and never drawn.
	ID string
	// Prompt is what was asked, and the row's target: the only field on the
	// §6a grid that grows, and the one the filter bolds its match in.
	Prompt string
	// Command is what came back. It is the preview's whole subject and the
	// thing `[enter]` would run.
	Command string
	// When is how long ago, in the row's own words — `4m ago`, `yesterday`.
	When string
	// Model is `openai/gpt-5.2`, stated in the preview rather than on the
	// row: it is a property of the answer, not a way of finding it.
	Model string
	// Action is what was done with the command at the time — `run`, `copy`,
	// `save`, `cancel`. The preview states it; the row states its outcome.
	Action string
	// Outcome is the closed §6d field: `exit 0`, `copied`, `not run`. It is
	// the reason to read the row, so it never clips.
	Outcome string
	// State picks the glyph the row and the preview both lead with, and the
	// colour the outcome takes. §6b's reading holds: a command that finished
	// keeps `$`, and only a break or a refusal overrides it.
	State ActivityState
	// Duration is the 6-column right-aligned field: how long the model took
	// to answer. Blank under half a second, like every other row's.
	Duration string
	// Counts is the preview's token line — `↑ 412 · ↓ 38`. Empty for an
	// entry recorded before the columns existed, which is most of them.
	Counts string
}

// HistoryAct is what a key asked the host to do to the entry under the
// pointer. Re-running is not one of them: it takes the terminal, so it closes
// the screen instead (see HistoryResult).
type HistoryAct int

const (
	// HistoryCopy is `[c]`: the command to the clipboard.
	HistoryCopy HistoryAct = iota
	// HistorySave is `[s]`: the command saved as a snippet.
	HistorySave
	// HistoryDelete is `[x]`, and only after the §5 confirm has been
	// answered — the screen never resolves a delete the reader has not said
	// yes to.
	HistoryDelete
)

// HistoryCommand is one act the host carries out while the screen stays up.
// The host does it, sets Notice, and hands back fresh Rows.
type HistoryCommand struct {
	Act HistoryAct
	ID  string
}

// HistoryResult is how the screen closed: with a command to run, or with
// nothing. Run and Canceled are never both true.
type HistoryResult struct {
	Run bool
	// ID and Command are the entry `[enter]` chose. The command is carried
	// out as well as the id so a host that has already closed its store can
	// still run it.
	ID       string
	Command  string
	Canceled bool
}

// HistoryScreen is `shhh history`: a takeover surface, full width, no
// inspector rail, owning the keyboard for as long as it is up (§19).
type HistoryScreen struct {
	// Rows are the entries newest first, as the host read them.
	Rows []HistoryRow
	// Focus is an index into Rows and survives the host rebuilding them.
	Focus int
	// Subject is what the header says the screen is over — `41 entries · 12
	// run`. The host counts it, because counting is a reading of the store.
	Subject string
	// MaxLines bounds the screen height; everything pinned comes off the
	// panes' budget before the window is drawn (§4a). 0 is unbounded.
	MaxLines int
	// Notice is the line a key left behind — what was copied, what was
	// deleted. The host clears it on the next keystroke.
	Notice string

	list    Select
	shown   []int
	confirm *Confirm
	keys    bool
}

// Update is the screen's whole keyboard. The confirm answers first while it
// is up — it holds the keyboard, and `y` is not a letter to it (invariant 5).
func (h *HistoryScreen) Update(msg tea.KeyMsg) (done bool, result any) {
	h.sync()
	if h.confirm != nil {
		return h.updateConfirm(msg)
	}
	pressed := msg.String()
	switch {
	case pressed == "up":
		h.move(-1)
		return false, nil
	case pressed == "down":
		h.move(1)
		return false, nil
	case keys.Is(pressed, keys.Screen.Rerun):
		// The one key that leaves the screen with something to do. A list the
		// filter emptied has nothing for it to take (invariant 5).
		if row := h.current(); row != nil {
			return true, HistoryResult{Run: true, ID: row.ID, Command: row.Command}
		}
		return false, nil
	case keys.Is(pressed, keys.Select.Cancel):
		return true, HistoryResult{Canceled: true}
	}
	// With the query line open the query line is the surface, so c, s, x and
	// q are letters rather than keys — the reading every picker in the
	// product makes (§4a). ctrl+u clears it, and clearing a filter that is
	// already empty closes it, which is how the row keys are got back
	// without leaving the screen.
	if h.list.Filtering {
		if keys.Is(pressed, keys.Screen.ClearQ) && h.list.Query == "" {
			h.list.Filtering = false
			return false, nil
		}
		h.list.editQuery(msg)
		if h.list.QueryChanged() {
			h.refilter()
		}
		return false, nil
	}
	switch {
	case pressed == "k":
		h.move(-1)
	case pressed == "j":
		h.move(1)
	case keys.Is(pressed, keys.Screen.Filter):
		h.list.Filtering = true
	case pressed == keys.Shown(keys.Screen.Quit):
		return true, HistoryResult{Canceled: true}
	case keys.Is(pressed, keys.Screen.List):
		h.keys = !h.keys
	case keys.Is(pressed, keys.Screen.Copy):
		if row := h.current(); row != nil {
			return false, HistoryCommand{Act: HistoryCopy, ID: row.ID}
		}
	case keys.Is(pressed, keys.Screen.Snippet):
		if row := h.current(); row != nil {
			return false, HistoryCommand{Act: HistorySave, ID: row.ID}
		}
	case keys.Is(pressed, keys.Screen.Delete):
		// §5: the one key here that destroys something asks first, and the
		// prompt names what it would take rather than saying "this entry".
		if row := h.current(); row != nil {
			h.confirm = &Confirm{Prompt: sty.Body.Render(
				"Delete the entry for " + quoted(row.Prompt) + "?")}
		}
	}
	return false, nil
}

// SetQuery opens the filter row with a query already in it, for a host that
// was given one on the command line. It is the same state `[/]` and a run of
// keystrokes would have left behind, so `[ctrl+u]` clears it and the query
// row states both counts exactly as it does when the reader typed it.
func (h *HistoryScreen) SetQuery(query string) {
	h.list.Filtering = true
	h.list.Query = query
	h.refilter()
}

func (h *HistoryScreen) updateConfirm(msg tea.KeyMsg) (bool, any) {
	done, result := h.confirm.Update(msg)
	if !done {
		return false, nil
	}
	h.confirm = nil
	if yes, _ := result.(bool); yes {
		if row := h.current(); row != nil {
			return false, HistoryCommand{Act: HistoryDelete, ID: row.ID}
		}
	}
	return false, nil
}

// View renders the screen: the §17c header and its rule, the two panes, and
// one hint line at the foot.
func (h *HistoryScreen) View(width int) string {
	if width <= 0 {
		return ""
	}
	h.sync()
	foot := h.footRows(width)
	head := []string{h.headerRow(width), reviewRule(width), ""}

	pinned := len(head) + 1 + len(foot)
	if h.Notice != "" {
		pinned++
	}
	rows := append(head, h.paneRows(width, h.budget(pinned))...)
	rows = append(rows, "")
	rows = append(rows, foot...)
	if h.Notice != "" {
		rows = append(rows, sty.Dim.Render(clip(h.Notice, width)))
	}
	return strings.Join(rows, "\n")
}

// budget is how many rows the panes may spend: the screen's height less
// everything pinned around them. An unbounded screen windows nothing.
func (h *HistoryScreen) budget(pinned int) int {
	if h.MaxLines <= 0 {
		return 0
	}
	return max(h.MaxLines-pinned, 1)
}

// paneRows is the body: the search and the preview side by side where the
// terminal can carry two columns, and stacked where it cannot. Stacked, the
// preview keeps a floor and the list gives way to it — a browser that cannot
// show the command it is about to run is not a browser.
func (h *HistoryScreen) paneRows(width, budget int) []string {
	if width < historyStackWidth {
		return h.stackedRows(width, budget)
	}
	listWidth := min(max(width/2, historyListMin), historyListMax)
	paneWidth := max(width-listWidth-lipgloss.Width(reviewDivider), 8)
	list := h.listRows(listWidth, budget)
	pane := h.previewRows(paneWidth)
	rows := max(len(list), len(pane))
	if budget > 0 {
		rows = min(rows, budget)
	}
	return joinReviewPanes(list, pane, listWidth, rows)
}

// stackedRows is the narrow layout: the search above, the preview below,
// nothing truncated sideways.
func (h *HistoryScreen) stackedRows(width, budget int) []string {
	pane := h.previewRows(width)
	if budget <= 0 {
		return append(append(h.listRows(width, 0), reviewRule(width)), pane...)
	}
	// The rule between the panes costs a row.
	avail := budget - 1
	if avail < historyMinPreview+2 {
		// No room for both: the list wins, because a screen that cannot
		// preview an entry can still say which entries there are.
		return truncRows(h.listRows(width, budget), budget, width)
	}
	keep := min(len(pane), max(avail/2, historyMinPreview))
	rows := h.listRows(width, avail-keep)
	rows = append(rows, reviewRule(width))
	return append(rows, truncRows(pane, keep, width)...)
}

// listRows is the left pane: the filter row pinned above the §4a window, the
// window itself with its markers, and — under it — what the filter hid and
// the key that clears it. Both counts are stated (§19b): the query row says
// `6 of 41 match` and the line under the list says what became of the other
// 35.
func (h *HistoryScreen) listRows(width, budget int) []string {
	h.clipLabels(width)
	head := h.list.queryRows(cardWidthFor(width))
	if len(head) > 0 {
		head = append(head, reviewRule(width))
	}
	tail := h.hiddenRows(width)
	body, _ := h.list.visibleRows(cardWidthFor(width), listBudget(budget, len(head)+len(tail)), false)
	rows := append(head, body...)
	return append(rows, tail...)
}

// clipLabels is what keeps the outcome on the row. §4a's grid gives a label
// wider than half the card the whole row and drops the fields after it — but
// on this list the outcome is the reason to read the row, and a prompt is the
// one field here that runs to any length. So the prompt gives way to it
// instead, and the preview beside it carries the prompt in full, which is
// what makes the trade a fold rather than a loss (invariant 4).
func (h *HistoryScreen) clipLabels(width int) {
	room := 2
	for _, opt := range h.list.Options {
		field := 0
		if opt.Value != "" {
			field += lipgloss.Width(opt.Value) + 2
		}
		if opt.Meta != "" {
			field += lipgloss.Width(opt.Meta) + 2
		}
		room = max(room, field+2)
	}
	// A pane too narrow for a prompt and its fields both keeps a readable
	// run of the prompt; below that the grid's own drop order takes over.
	label := max(width-room, minHistoryLabel)
	for i, opt := range h.list.Options {
		h.list.Options[i].Label = clip(opt.Label, label)
	}
}

// listBudget is what the window may spend once the filter row and the hidden
// line have taken their share. An unbounded pane windows nothing.
func listBudget(budget, pinned int) int {
	if budget <= 0 {
		return 0
	}
	return max(budget-pinned, 1)
}

// hiddenRows is the line under the list saying what the filter took out of
// it. It is only ever drawn while something is hidden — a filter that hid
// nothing has nothing to confess, and invariant 4 is about what a surface
// swallowed, not about the row that says so.
func (h *HistoryScreen) hiddenRows(width int) []string {
	if !h.list.Filtering {
		return nil
	}
	hidden := len(h.Rows) - len(h.shown)
	if hidden <= 0 {
		return nil
	}
	row := sty.Dim.Render(entries(hidden)+" hidden by the filter · ") +
		sty.Info.Render("[ctrl+u]") + sty.Dim.Render(" clear it")
	return []string{reviewRule(width), clip(row, width)}
}

// previewRows is the right pane: the entry the pointer is on, in the grammar
// it was recorded in (§19b). The title says when and by which model, the
// prompt is the opening instruction, the command is a §6a row with its
// outcome and its duration, and the token line closes it.
//
// It is a preview, not a second list: nothing in it is focusable and no key
// reaches it.
func (h *HistoryScreen) previewRows(width int) []string {
	row := h.current()
	if row == nil {
		return []string{sty.Dim.Render(clip("no entry selected", width))}
	}
	rows := []string{h.previewTitle(*row, width)}
	if row.Prompt != "" {
		for _, line := range wrapSpans([]styledSpan{{row.Prompt, sty.Dim}}, max(width-2, 1)) {
			rows = append(rows, "  "+line)
		}
	}
	rows = append(rows, "")
	rows = append(rows, h.commandRows(*row, width)...)
	if row.Counts != "" {
		rows = append(rows, "  "+sty.Dimmer.Render(clip(row.Counts, max(width-2, 1))))
	}
	return rows
}

// previewTitle is the preview's header: when the entry was made, which model
// made it, and — right-aligned — what was done with the command at the time.
func (h *HistoryScreen) previewTitle(row HistoryRow, width int) string {
	left := brightStyle().Render(row.When)
	if row.Model != "" {
		left += sty.Dim.Render(" · " + row.Model)
	}
	right := ""
	if row.Action != "" {
		right = sty.Dim.Render(row.Action)
	}
	if pad := width - lipgloss.Width(left) - lipgloss.Width(right); pad >= 2 && right != "" {
		return left + strings.Repeat(" ", pad) + right
	}
	return clip(left, width)
}

// commandRows is the command on the §6a grid: the `$` glyph, the `run` verb,
// the command itself as the target, the outcome and the duration. A command
// too long for the pane keeps going on the detail lines under it rather than
// being clipped away — this is the thing `[enter]` would run, so invariant 4
// is not negotiable here.
func (h *HistoryScreen) commandRows(row HistoryRow, width int) []string {
	command := strings.Join(strings.Fields(row.Command), " ")
	if command == "" {
		return []string{sty.Dim.Render(clip("  no command was recorded", width))}
	}
	act := ActivityRow{
		Kind: ActivityCommand, State: row.State, Verb: "run",
		Outcome: row.Outcome, Duration: row.Duration, Expanded: true,
	}
	// The target column is what is left of the pane once the fixed fields
	// have taken theirs; the continuation lines only give up the detail
	// indent, so the wrap is measured against the narrower of the two. It is
	// wrapped as plain text rather than through wrapSpans because the grid
	// does its own painting, and a styled run it clipped would leave half an
	// escape sequence behind.
	head := max(width-leadWidth-durWidth-lipgloss.Width(row.Outcome)-2, 8)
	rest := max(width-detailIndent, 8)
	lines := wrapPlain(command, min(head, rest))
	act.Target = lines[0]
	act.Detail = lines[1:]
	return strings.Split(act.View(width), "\n")
}

// wrapPlain breaks text into unstyled lines no wider than width, on word
// boundaries. A word wider than the line is left whole for the grid to clip,
// the same reading wrapSpans makes: a break inside one would be a hyphen the
// interface invented.
func wrapPlain(text string, width int) []string {
	if width <= 0 {
		return []string{text}
	}
	var lines []string
	line := ""
	for _, word := range strings.Fields(text) {
		switch {
		case line == "":
			line = word
		case lipgloss.Width(line)+1+lipgloss.Width(word) <= width:
			line += " " + word
		default:
			lines = append(lines, line)
			line = word
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

// headerRow is the §17c header: the command, what it is over, and the two
// keys every one of these screens offers. The right-hand keys drop before the
// subject does — they annotate the line, and an annotation goes first (§16).
func (h *HistoryScreen) headerRow(width int) string {
	left := brightStyle().Render("shhh history")
	if h.Subject != "" {
		left += sty.Dim.Render(" · " + h.Subject)
	}
	right := sty.Dim.Render(screenHeaderKeys())
	if pad := width - lipgloss.Width(left) - lipgloss.Width(right); pad >= 2 {
		return left + strings.Repeat(" ", pad) + right
	}
	return clip(left, width)
}

// footRows are the keys the screen offers and the field that annotates them.
// The field drops first (§16); the offers never truncate, they wrap
// (invariant 4).
func (h *HistoryScreen) footRows(width int) []string {
	if h.confirm != nil {
		return []string{clip(h.confirm.View(width), width)}
	}
	if h.keys {
		rows := make([]string, 0, len(h.keyList())+1)
		for _, offer := range h.keyList() {
			rows = append(rows, clip(keyOffers([]KeyOffer{offer}), width))
		}
		return append(rows, clip(keyOffers([]KeyOffer{hideKeysOffer()}), width))
	}
	field := h.footField()
	rows := wrapOffers(h.offers(width, field), width)
	if len(rows) == 0 || field == "" {
		return rows
	}
	painted := sty.Dim.Render(field)
	if pad := width - lipgloss.Width(rows[0]) - lipgloss.Width(painted); pad >= 2 {
		rows[0] += strings.Repeat(" ", pad) + painted
	}
	return rows
}

// fits reports whether a run of offers leaves room beside it for the field
// that annotates them.
func fitsBeside(offers []KeyOffer, field string, width int) bool {
	return lipgloss.Width(keyOffers(offers))+lipgloss.Width(field)+2 <= width
}

// offers is the key row for whichever surface holds the keyboard. While the
// query line is open the row keys are letters, so they are not offered: a key
// that cannot act is not an offer (invariant 5).
//
// The row also gives ground for the field beside it, and the movement
// reminder is what it gives first — §19b's `nothing is re-run until [enter]`
// is the sentence a reader has to have read before they walk away, and `[↑↓]`
// is the one segment `[?]` still carries in full. Nothing is ever truncated
// to make room (invariant 4); the segment goes whole or it stays whole.
func (h *HistoryScreen) offers(width int, field string) []KeyOffer {
	move := keyOffer(keys.Select.Move)
	var acts []KeyOffer
	if h.current() != nil {
		acts = append(acts, keyOffer(keys.Screen.Rerun))
	}
	if h.list.Filtering {
		acts = append(acts, keyOfferAs(keys.Screen.ClearQ, "clear the filter, then close it"))
	} else {
		if h.current() != nil {
			acts = append(acts,
				keyOffer(keys.Screen.Copy),
				keyOffer(keys.Screen.Snippet),
				keyOffer(keys.Screen.Delete))
		}
		acts = append(acts, keyOffer(keys.Screen.Filter))
	}
	acts = append(acts, keyOfferAs(keys.Select.Cancel, "back to the shell"))

	// The rungs, in the order the row gives ground: the movement reminder
	// first, because `[?]` still carries it in full and every list in the
	// product moves the same way; then saving a snippet, which is the one
	// offer here that is about somewhere else. `[/]` is the last thing shed
	// — it is what this screen is for. A row that still does not fit keeps
	// the last rung and lets the field go, which is §16's own order.
	rungs := [][]KeyOffer{
		append([]KeyOffer{move}, acts...),
		acts,
		without(acts, keys.Bracket(keys.Screen.Snippet)),
		without(acts, keys.Bracket(keys.Screen.Snippet), keys.Bracket(keys.Screen.Filter)),
	}
	if field == "" {
		return rungs[0]
	}
	for _, rung := range rungs {
		if fitsBeside(rung, field, width) {
			return rung
		}
	}
	// Nothing fits beside the field, so the field goes (§16) — and with
	// nothing left to buy, the row keeps every offer it had and wraps.
	return rungs[0]
}

// without is a rung with some offers shed. Shedding is whole-segment: nothing
// on a key row is ever truncated (invariant 4).
func without(offers []KeyOffer, shed ...string) []KeyOffer {
	drop := map[string]bool{}
	for _, k := range shed {
		drop[k] = true
	}
	out := make([]KeyOffer, 0, len(offers))
	for _, o := range offers {
		if !drop[o.Key] {
			out = append(out, o)
		}
	}
	return out
}

// keyList is every key the screen has, for `[?]`.
func (h *HistoryScreen) keyList() []KeyOffer {
	return []KeyOffer{
		keyOfferAs(keys.Screen.Move, "move between entries"),
		keyOfferAs(keys.Screen.Rerun, "run the command under the pointer again"),
		keyOfferAs(keys.Screen.Copy, "copy the command to the clipboard"),
		keyOfferAs(keys.Screen.Snippet, "save the command as a snippet"),
		keyOfferAs(keys.Screen.Delete, "delete the entry, after confirming it"),
		keyOfferAs(keys.Screen.Filter, "filter by what was asked or by what came back"),
		keyOfferAs(keys.Screen.ClearQ, "clear the filter; clear it again to close it"),
		keyOfferAs(keys.Select.Cancel, "back to the shell, running nothing"),
		keyOfferAs(keys.Screen.Quit, "back to the shell, running nothing"),
	}
}

// footField annotates the key row. §19b asks that the reader has read the
// sentence before they walk away: nothing on this screen runs by itself.
func (h *HistoryScreen) footField() string {
	if h.current() == nil {
		return ""
	}
	return "nothing is re-run until [enter]"
}

// sync rebuilds the list from Rows. It runs before every Update and every
// View because the host replaces Rows after each command, and the window and
// the query the list is showing have to survive that.
func (h *HistoryScreen) sync() {
	h.shown = h.match()
	opts := make([]SelectOption, 0, len(h.shown))
	for _, i := range h.shown {
		row := h.Rows[i]
		opts = append(opts, SelectOption{
			Label:     historyGlyph(row.State) + " " + oneLine(row.Prompt),
			Value:     row.Outcome,
			ValueTone: historyTone(row.State),
			Desc:      historyWhen(row.When),
			Meta:      row.Duration,
		})
	}
	h.list.Options = opts
	h.list.Total = len(h.Rows)
	h.list.Filterable = true
	h.list.Unnumbered = true
	h.list.QueryHint = "type to filter what was asked or what came back"
	h.list.Focus = h.optIndex(h.Focus)
	h.list.Closest = ""
	if h.list.Filtering && len(h.shown) == 0 {
		h.list.Closest = h.closest()
	}
}

// historyGlyph is the row's leading glyph, plain rather than painted: the
// label runs through the card's own match emphasis and its focus paint, and
// an escape sequence inside either would be cut in half. Invariant 1 wants
// the glyph, and the glyph is what this is — the colour beside it is carried
// by the outcome, which is a word.
func historyGlyph(state ActivityState) string {
	switch state {
	case ActivityFailed:
		return "✗"
	case ActivityDenied:
		return "⊘"
	case ActivityQueued:
		return "·"
	}
	return "$"
}

// historyTone reads the outcome the way a card field is read (§2): a command
// that broke is del, one that never ran is an unremarkable fact, and one that
// exited clean is the reassuring answer.
func historyTone(state ActivityState) FieldTone {
	switch state {
	case ActivityFailed:
		return ToneRisk
	case ActivityDenied, ActivityQueued:
		return ToneNeutral
	}
	return ToneSafe
}

// historyWhen is the `· 4m ago` continuation. The separator is the card's
// rather than the host's so every row carries the same one.
func historyWhen(when string) string {
	if when == "" {
		return ""
	}
	return "· " + when
}

// match is the entries the query left showing. The rule lives here rather
// than in the card because the card never filters (§4a): an entry is found by
// what was asked or by what came back, which is the pair a reader looking for
// a command they half remember has to work with.
func (h *HistoryScreen) match() []int {
	query := strings.ToLower(strings.TrimSpace(h.list.Query))
	out := make([]int, 0, len(h.Rows))
	for i, row := range h.Rows {
		if matchesQuery(row, query) {
			out = append(out, i)
		}
	}
	return out
}

// refilter re-runs the match after a keystroke changed the query, and puts
// the pointer on the first entry that survived it — the rows under it are not
// the rows that were there a moment ago (S-112).
func (h *HistoryScreen) refilter() {
	h.confirm = nil
	if shown := h.match(); len(shown) > 0 {
		h.Focus = shown[0]
	}
	h.sync()
}

// closest names the nearest entry that does exist, for the card that matched
// nothing (§4a). It is found by taking the query's last character back one at
// a time until something matches, so what it names really is the nearest
// thing to what was typed rather than whatever happens to be newest. It stops
// at minClosestPrefix: a match on one or two letters is not a near miss, it
// is a coincidence, and naming one would be worse than saying nothing.
func (h *HistoryScreen) closest() string {
	for r := []rune(strings.TrimSpace(h.list.Query)); len(r) > minClosestPrefix; r = r[:len(r)-1] {
		for _, row := range h.Rows {
			if matchesQuery(row, strings.ToLower(string(r[:len(r)-1]))) {
				return oneLine(row.Prompt)
			}
		}
	}
	return ""
}

// matchesQuery is the match rule, over one row.
func matchesQuery(row HistoryRow, query string) bool {
	return query == "" ||
		strings.Contains(strings.ToLower(row.Prompt), query) ||
		strings.Contains(strings.ToLower(row.Command), query)
}

// move steps the pointer to the next entry the filter left showing, stopping
// at either end rather than wrapping.
func (h *HistoryScreen) move(delta int) {
	if len(h.shown) == 0 {
		return
	}
	at := 0
	for i, row := range h.shown {
		if row == h.Focus {
			at = i
			break
		}
	}
	h.Focus = h.shown[min(max(at+delta, 0), len(h.shown)-1)]
	h.confirm = nil
	h.sync()
}

// current is the entry under the pointer, or nil when the filter left none.
func (h *HistoryScreen) current() *HistoryRow {
	for _, i := range h.shown {
		if i == h.Focus {
			return &h.Rows[i]
		}
	}
	return nil
}

// optIndex maps a row index to its place in the list the card is drawing. A
// row the filter hid takes the first one showing.
func (h *HistoryScreen) optIndex(row int) int {
	for i, at := range h.shown {
		if at == row {
			return i
		}
	}
	return 0
}

// oneLine flattens a prompt onto the single row it gets. A prompt typed over
// three lines is one statement, and the row is where it is read as one.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// quoted is how the confirm names the entry it would delete. It is clipped
// short — the prompt identifies the row, and a confirm that wraps to three
// lines has stopped being the §5 one-liner.
func quoted(s string) string {
	const shown = 48
	s = oneLine(s)
	if s == "" {
		return "this entry"
	}
	return `"` + clip(s, shown) + `"`
}

// entries counts entries, which "entry" plus an s does not.
func entries(n int) string {
	if n == 1 {
		return "1 entry"
	}
	return fmt.Sprintf("%d entries", n)
}
