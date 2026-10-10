package components

// The config screen (
// docs/interface/surfaces.md#the-supporting-screens,
// the config-screen golden). `shhh config` shipped before the cockpit and
// invented its own list, its own idea of a value and its own key words for
// them. It is re-cut here from parts that already exist: the selector window
// with its markers and its filter row, the grid row with a right-hand field,
// the frame's bracketed-key hint line, the masked entry an auth failure
// opens, and the inline confirm. Almost nothing here is new — the win is
// deletion, and the gain is that a reader who knows the cockpit already knows
// this screen.
//
// Two rules shape it (docs/interface/surfaces.md#the-supporting-screens). A
// value is a row, and changing one opens the picker *under* that row rather
// than over the screen, so the setting being changed stays visible above the
// options. And nothing reaches the file until `[ctrl+s]`: every edit is staged,
// the header counts what is standing against the file, and the way out
// discards the lot — which is why both directions go through the same
// one-line question.
//
// It is a passive component like the rest of this package. It owns no config
// semantics: a change resolves to a ConfigChange the host applies to its own
// copy, and the host hands back fresh Rows. That is why the screen can render
// `⏵⏵ auto` without knowing what a permission mode is.

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// menuIndent is the column the settings list starts at, and pickerIndent the
// one level further in that an open picker sits at ("indented one level").
// Two levels is all this screen has.
const (
	menuIndent   = 2
	pickerIndent = 4
)

// ConfigRow is one setting: what it is called, what it is set to, and where
// that answer came from. The host builds these from its config and rebuilds
// them after every change — the screen never computes a value.
type ConfigRow struct {
	// Group is the rail this row sits under — the file's own tables, PROVIDER
	// through TODO, so a reader who has scrolled the file has scrolled the
	// screen. Rails are labels rather than options: the pointer steps over
	// them and the markers do not count them. A row whose Group differs from
	// the row before it opens a new rail.
	Group string
	// Key is the config key `[ctrl+s]` would write and `[ctrl+r]` would clear. The
	// screen only carries it back to the host.
	Key string
	// Label is the setting's name, in the left column.
	Label string
	// Value is what it is set to, as it reads on the row: `⏵⏵ auto`, `⛨
	// workspace-write`, `25`. A masked secret is already masked here — see
	// MaskSecret.
	Value string
	// ValueTone reads the value: safe, a door left open, at risk, or an
	// unremarkable fact. The glyph beside it says the same thing, so the colour
	// never carries it alone (invariant 1).
	ValueTone FieldTone
	// Detail qualifies the value in dim text on the same row — `— reads fold,
	// mutations never do`. It is a note about the value, which is why it is
	// never toned.
	Detail string
	// Source is the right-hand field: where the answer came from (`default`,
	// `user`, `project` for a value the checkout's own file set, `user ·
	// ~/.config/shhh/config.toml`), or the reason the host cannot honour the
	// setting at all: "why is this on" is the only question a config screen is
	// ever asked, and a setting the machine cannot keep says so rather than
	// being hidden (invariant 4). A host with two answers to give joins them
	// with ` · `, longest-lived last — `unwritten · next session` for a value
	// staged in a session that only a session started after the write runs
	// on.
	Source     string
	SourceTone FieldTone
	// Options are what `[enter]` offers. A row with none opens a field to type
	// into instead.
	Options []SelectOption
	// Secret marks a value that must never be echoed: `[enter]` opens the masked
	// entry rather than a field showing what is already there.
	Secret bool
}

// ConfigChange is one staged edit, resolved to the host as it is made. Reset
// is `[ctrl+r]`: the key goes back to its default rather than to a value.
type ConfigChange struct {
	Key   string
	Value string
	Reset bool
}

// ConfigResult is what a key answered with: how the screen closed — left or
// discarded — and the edit a key made with the screen still up. nil is
// a key that changed no setting.
type ConfigResult struct {
	// Write is the save chord with something staged. The screen stays up: the
	// host writes, says what it wrote in Notice, and hands the rows back.
	Write    bool
	Canceled bool
	Change   *ConfigChange
	// Scope is the toggle between the checkout's file and the person's own:
	// the host moves the write, and hands back the Path, Yours and Rows that
	// describe the file it moved it to.
	Scope bool
}

// ConfigScreen is `shhh config`: a takeover surface, full width, no inspector
// rail, owning the keyboard for as long as it is up.
type ConfigScreen struct {
	// Path is the file `[ctrl+s]` writes, stated in the header.
	Path string
	// Behind is that file read against the settings the table has now and
	// found wanting — keys added since it was written, a key that moved, a
	// wording with no file. The header says so in one word beside the path
	// and nothing more: the doctor's row is where the counts and the offer
	// are (docs/capabilities/configuration.md#an-older-file-is-brought-up-to-date).
	Behind bool
	// Scoped is a screen standing in a checkout, where a write has two files
	// it could reach: the checkout's own and the person's. It is what offers
	// the key that switches between them, and what makes the header say
	// whose file Path is — outside a checkout there is only the one, and
	// naming its owner would answer a question nobody asked. Yours is which
	// of the two the write reaches now
	// (docs/capabilities/configuration.md#two-files-one-resolution-order).
	Scoped bool
	Yours  bool
	// The pointer is an index into Rows and survives the host rebuilding
	// them; the rows showing are the ones the query left.
	listScreen[ConfigRow]
	// Rows are the settings in the order they are shown.
	Rows []ConfigRow
	// Changed is how many edits are standing against the file. The header counts
	// them and the write is not offered while it is zero — a key that cannot act is
	// not offered (invariant 5).
	Changed int
	// Held is how many of those a running session has taken for itself, so
	// leaving does not give them back: the question over them is where they
	// go, not whether they are thrown away. It is zero outside a session, and
	// for the settings a session reads only when it opens
	// (docs/interface/principles.md#esc-is-always-the-safe-answer).
	Held int
	// maxLines bounds the screen height; everything pinned comes off the list's
	// budget before its window is drawn. 0 is unbounded, which is what a test or
	// a host that sizes itself gets.
	maxLines int
	// Notice is the line a key left behind — what the write wrote, what `[r]`
	// reset. It is the screen's foot row, and the next keystroke clears it.
	Notice string
	// InSession is a host inside a chat rather than at a command line
	// (`/config`). The screen is the same screen; what differs is the two
	// fields that say where the reader is — what it is called, and the one
	// word its header ends with, which is `back` where a session is
	// underneath it and `quit` where a shell is
	// (docs/interface/surfaces.md#the-supporting-screens).
	InSession bool

	optRow  []int
	picker  *Select
	editRow int
	edit    *lineEdit
	secret  *SecretPrompt
	// confirm is the question standing in front of the one key this screen
	// cannot take back: the way out, which drops what has been typed. The
	// write is not asked about; it answers with a receipt instead.
	confirm *Confirm
	// pending is what answering that question yes does. It is armed with the
	// question and goes down with it, so a decline cannot hand it to whatever
	// is asked next.
	pending ConfigResult
	// keeping says the question up is the one that offers the running
	// session the edits and the file a write first: yes leaves them on the
	// session, the write chord writes them and leaves, and anything else
	// stays.
	keeping bool
}

// MaskSecret renders a secret the way the config screen asks for: the last
// four characters and nothing else. A key too short to have four is all dots,
// because a mask that reveals most of a short key has masked nothing.
func MaskSecret(s string) string {
	if s == "" {
		return ""
	}
	r := []rune(s)
	if len(r) <= 4 {
		return strings.Repeat("·", len(r)+3)
	}
	return "···" + string(r[len(r)-4:])
}

// Update is the screen's whole keyboard. The open sub-surface answers first —
// a picker, a field, a masked entry or an armed question owns the letters
// while it is up (invariant 5) — and the settings list answers otherwise.
func (c *ConfigScreen) Update(msg tea.KeyPressMsg) (done bool, result ConfigResult) {
	c.sync()
	// A notice is what the last key left behind, so the next key clears it;
	// the host sets its own after the key it answered.
	c.Notice = ""
	switch {
	case c.confirm != nil:
		return c.updateConfirm(msg)
	case c.secret != nil:
		return c.updateSecret(msg)
	case c.edit != nil:
		return c.updateEdit(msg)
	case c.picker != nil:
		return c.updatePicker(msg)
	}
	return c.updateMenu(msg)
}

func (c *ConfigScreen) updateMenu(msg tea.KeyPressMsg) (bool, ConfigResult) {
	pressed := msg.String()
	switch {
	case c.walked(pressed):
		return false, ConfigResult{}
	case keys.Is(pressed, keys.Screen.Take):
		c.open()
		return false, ConfigResult{}
	case keys.Is(pressed, keys.Screen.Write):
		return c.write()
	}
	// With the query line open the query line is the surface, so a letter is
	// text rather than a key — the same reading every picker in the product
	// makes — and esc backs out of the line before it leaves the screen: it
	// clears what was typed, then closes the line.
	if c.list.Filtering {
		if keys.Is(pressed, keys.Screen.Quit) {
			if c.list.Query == "" {
				c.list.Filtering = false
				return false, ConfigResult{}
			}
			c.list.Query, c.list.queryEdited = "", true
		} else {
			c.list.editQuery(msg)
		}
		if c.list.QueryChanged() {
			c.refilter()
		}
		return false, ConfigResult{}
	}
	switch {
	case keys.Is(pressed, keys.Screen.Filter):
		c.list.Filtering = true
	case keys.Is(pressed, keys.Screen.Quit):
		return c.leave()
	case keys.Is(pressed, keys.Screen.List):
		c.keys = !c.keys
	case keys.Is(pressed, keys.Screen.Reset):
		if row := c.current(); row != nil {
			return false, ConfigResult{Change: &ConfigChange{Key: row.Key, Reset: true}}
		}
	case keys.Is(pressed, keys.Screen.Scope):
		if c.Scoped {
			return false, ConfigResult{Scope: true}
		}
	}
	return false, ConfigResult{}
}

// write is the save chord. It writes at once: the screen's one question is
// the discard, because that is the act that cannot be taken back, and a
// write is what the person came to do. The host answers with a receipt. With
// nothing staged the key says so and writes nothing.
func (c *ConfigScreen) write() (bool, ConfigResult) {
	if c.Changed == 0 {
		c.Notice = "nothing staged to write"
		return false, ConfigResult{}
	}
	return false, ConfigResult{Write: true}
}

// leave is the way out, and what it costs. Staged edits are typed work, and
// Escape never abandons work without putting it back
// (docs/interface/principles.md#esc-is-always-the-safe-answer): with
// something standing against the file the question comes up first, over the
// same count the header is carrying. With nothing staged there is nothing to
// lose and the press closes the screen.
func (c *ConfigScreen) leave() (bool, ConfigResult) {
	if c.Changed == 0 {
		return true, ConfigResult{Canceled: true}
	}
	if c.Held > 0 {
		c.askKeep()
		return false, ConfigResult{}
	}
	c.ask("Discard "+plural(c.Changed, "change")+"?", ConfigResult{Canceled: true})
	return false, ConfigResult{}
}

// askKeep is the question over edits a running session has taken: they stay
// on the session whichever way the screen is left, so declining to write
// them costs the reader nothing they cannot take back with the row's reset,
// and the answers are where they go. A change the session cannot take is
// dropped with the screen and the question says so by its count.
func (c *ConfigScreen) askKeep() {
	prompt := "Leave " + plural(c.Held, "change") + " on this session only?"
	if dropped := c.Changed - c.Held; dropped > 0 {
		prompt = "Leave " + plural(c.Held, "change") + " on this session and drop " + plural(dropped, "change") + "?"
	}
	c.ask(prompt, ConfigResult{Canceled: true})
	c.keeping = true
	c.confirm.Answers = sty.dim.Render(keys.Bracket(keys.Confirm.Yes) + " this session only  " +
		keys.Bracket(keys.Screen.Write) + " write first  " + keys.Bracket(keys.Screen.Quit) + " stay")
}

// ask arms the inline confirm in front of a key, with what saying yes to it
// does. Both questions this screen asks are one line and count the same
// edits, because they are the two answers to the same situation.
func (c *ConfigScreen) ask(prompt string, then ConfigResult) {
	c.confirm = &Confirm{Prompt: sty.body.Render(prompt)}
	c.pending = then
}

// open is what `[enter]` does to the row under the pointer: a picker for a
// setting with answers, the masked entry for a secret, a field for anything
// else. All three open under the row rather than over the screen.
func (c *ConfigScreen) open() {
	row := c.current()
	if row == nil {
		return
	}
	c.editRow = c.Focus
	switch {
	case len(row.Options) > 0:
		p := &Select{
			Options: append([]SelectOption(nil), row.Options...),
			Total:   len(row.Options), Filterable: true, Unnumbered: true,
			QueryHint: "type to filter",
		}
		for i, o := range row.Options {
			if o.Label == row.Value {
				p.Focus = i
			}
		}
		c.picker = p
	case row.Secret:
		c.secret = &SecretPrompt{
			Prompt: "Paste a value for " + row.Label, Replace: lastFour(row.Value),
			Hint: row.Key,
		}
	default:
		c.edit = &lineEdit{value: []rune(row.Value), hint: "type a value"}
	}
}

func (c *ConfigScreen) updatePicker(msg tea.KeyPressMsg) (bool, ConfigResult) {
	switch pressed := msg.String(); {
	case keys.Is(pressed, keys.Select.Cancel):
		// esc keeps the current value — it is the one key on this screen that
		// is guaranteed to change nothing — one level at a time: a query typed
		// into the picker's row is cleared, an empty row closes, and then the
		// picker goes.
		switch {
		case c.picker.Filtering && c.picker.Query != "":
			c.picker.Query, c.picker.queryEdited = "", true
			if c.picker.QueryChanged() {
				c.refilterPicker()
			}
		case c.picker.Filtering:
			c.picker.Filtering = false
		default:
			c.picker = nil
		}
		return false, ConfigResult{}
	case keys.Is(pressed, keys.Screen.Take):
		return false, c.takeChosen()
	case keys.Is(pressed, keys.Screen.Write):
		// The write takes what is in the picker first, so the choice under the
		// pointer is staged and then written rather than swallowed.
		return false, c.writing(c.takeChosen())
	}
	pressed := msg.String()
	if c.picker.moved(pressed) {
		return false, ConfigResult{}
	}
	if c.picker.Filtering {
		c.picker.editQuery(msg)
		if c.picker.QueryChanged() {
			c.refilterPicker()
		}
		return false, ConfigResult{}
	}
	if keys.Is(pressed, keys.Screen.Filter) {
		c.picker.Filtering = true
	}
	return false, ConfigResult{}
}

// takeChosen answers the picker with the option under its pointer, staged.
func (c *ConfigScreen) takeChosen() ConfigResult {
	opts := c.picker.Options
	if len(opts) == 0 {
		return ConfigResult{}
	}
	chosen := opts[min(max(c.picker.Focus, 0), len(opts)-1)]
	c.picker = nil
	if row := c.rowAt(c.editRow); row != nil {
		return ConfigResult{Change: &ConfigChange{Key: row.Key, Value: chosen.Label}}
	}
	return ConfigResult{}
}

// writing is a result that also asks for the write, for the save chord pressed
// with a field or a picker open: what is in it is staged first, so the write
// has it to write.
func (c *ConfigScreen) writing(r ConfigResult) ConfigResult {
	r.Write = true
	return r
}

func (c *ConfigScreen) updateEdit(msg tea.KeyPressMsg) (bool, ConfigResult) {
	switch pressed := msg.String(); {
	case keys.Is(pressed, keys.Select.Cancel):
		c.edit = nil
		return false, ConfigResult{}
	case keys.Is(pressed, keys.Screen.Take):
		return false, c.takeTyped()
	case keys.Is(pressed, keys.Screen.Write):
		return false, c.writing(c.takeTyped())
	}
	c.edit.update(msg)
	return false, ConfigResult{}
}

// takeTyped closes the field and answers with what was typed into it, staged.
func (c *ConfigScreen) takeTyped() ConfigResult {
	value := strings.TrimSpace(string(c.edit.value))
	c.edit = nil
	if row := c.rowAt(c.editRow); row != nil {
		return ConfigResult{Change: &ConfigChange{Key: row.Key, Value: value}}
	}
	return ConfigResult{}
}

func (c *ConfigScreen) updateSecret(msg tea.KeyPressMsg) (bool, ConfigResult) {
	done, result := c.secret.Update(msg)
	if !done {
		return false, ConfigResult{}
	}
	c.secret = nil
	// The masked entry resolves to an empty value on esc, which leaves the
	// key that was already there in place — esc never destroys.
	if result.Value == "" {
		return false, ConfigResult{}
	}
	if row := c.rowAt(c.editRow); row != nil {
		return false, ConfigResult{Change: &ConfigChange{Key: row.Key, Value: result.Value}}
	}
	return false, ConfigResult{}
}

// updateConfirm is the keyboard while a question is up. Answering it takes it
// down; only yes carries the act it was armed for, and either act closes the
// screen because there is nothing left for it to say. Declining — n, enter or
// esc — leaves the screen and every staged edit exactly as they were.
func (c *ConfigScreen) updateConfirm(msg tea.KeyPressMsg) (bool, ConfigResult) {
	if c.keeping && keys.Is(msg.String(), keys.Screen.Write) {
		c.confirm, c.pending, c.keeping = nil, ConfigResult{}, false
		return true, ConfigResult{Write: true, Canceled: true}
	}
	answered, yes := confirmed(&c.confirm, msg)
	if !answered {
		return false, ConfigResult{}
	}
	c.keeping = false
	// The armed act goes down with the question either way: it was armed for
	// this question, and a decline that left it behind would hand it to
	// whatever is asked next.
	act := c.pending
	c.pending = ConfigResult{}
	if !yes {
		return false, ConfigResult{}
	}
	return true, act
}

// SetSize gives the screen the terminal's rectangle. It lays itself out from
// the width it is rendered at, so only the height is kept.
func (c *ConfigScreen) SetSize(_, height int) { c.maxLines = height }

// View renders the screen: the shared chrome, with the settings list in the
// rows it leaves and whatever is open spliced in under the row being changed.
func (c *ConfigScreen) View(width int) string {
	if width <= 0 {
		return ""
	}
	c.sync()
	inline := c.inlineRows(width)
	// The settings' own filter row is pinned above the list the way it is on
	// every card, so what it spends comes off the list's budget before the
	// window is drawn.
	var head []string
	for _, row := range c.list.queryRows(cardWidthFor(width - menuIndent)) {
		head = append(head, indentBy(row, menuIndent, width))
	}
	return screenChrome{
		header:   c.header(),
		head:     head,
		foot:     c.footer(width).rows(width),
		notice:   c.Notice,
		maxLines: c.maxLines,
		reserve:  len(inline),
	}.view(width, func(budget int) []string { return c.bodyRows(width, budget, inline) })
}

// bodyRows is the settings list with the open sub-surface spliced in under
// the row it belongs to. That splice is the whole shape of the screen: the
// picker is not a modal over the screen, so the setting being changed stays
// on screen above its own options.
func (c *ConfigScreen) bodyRows(width, budget int, inline []string) []string {
	rows, _, at := c.list.visibleRowsFocus(cardWidthFor(width-menuIndent), budget, false)
	out := make([]string, 0, len(rows)+len(inline))
	for i, row := range rows {
		out = append(out, indentBy(row, menuIndent, width))
		if i == at {
			out = append(out, inline...)
		}
	}
	// A focus the window does not hold — a filter that matched nothing — leaves
	// the sub-surface with no row to sit under, so it goes at the foot of the
	// list rather than nowhere.
	if at < 0 {
		out = append(out, inline...)
	}
	return out
}

// inlineRows are what is open under the focused row, already indented one
// level in. The order is the artboard's: the picker's own filter row above
// its window, its markers around it, and its keys under it.
func (c *ConfigScreen) inlineRows(width int) []string {
	inner := width - pickerIndent
	if inner < minDescWidth {
		return nil
	}
	var rows []string
	switch {
	case c.picker != nil:
		rows = append(rows, c.picker.queryRows(cardWidthFor(inner))...)
		body, _ := c.picker.visibleRows(cardWidthFor(inner), c.pickerBudget(len(rows)), false)
		rows = append(rows, body...)
	case c.edit != nil:
		rows = append(rows, c.edit.view())
	case c.secret != nil:
		// The masked entry's own key rows are dropped — the prompt and the mask
		// are its first two — because the screen already has a key row at its
		// foot, and the two would offer the same two keys twice.
		lines := strings.Split(c.secret.View(inner), "\n")
		rows = append(rows, lines[:min(len(lines), 2)]...)
	default:
		return nil
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, indentBy(row, pickerIndent, width))
	}
	return out
}

// pickerBudget bounds the picker so that opening one over a two-dozen-entry
// catalog does not push the settings under it off the screen. Eight options
// is what the artboard draws and what the selector window is sized for.
func (c *ConfigScreen) pickerBudget(pinned int) int {
	const pickerRows = 8
	if c.maxLines > 0 {
		return max(min(pickerRows, c.maxLines/2)-pinned, 1)
	}
	return max(pickerRows-pinned, 1)
}

// header names the command, which file it is over, and what is standing
// against that file. The path goes before the count of unwritten changes: a
// change that has not reached the file yet is the one thing on this row a
// reader cannot afford to lose sight of.
func (c *ConfigScreen) header() screenHeader {
	title, headerKeys := "shhh config", screenHeaderKeys()
	if c.InSession {
		title, headerKeys = "/config", screenBackKeys()
	}
	h := screenHeader{left: []RailSegment{screenTitle(title)}, keys: headerKeys}
	if c.Scoped {
		// Whose file it is outlives the path: a long path is the first field
		// this header gives up, and which of the two files the write reaches
		// is the thing the scope key changes.
		h.left = append(h.left, RailSegment{Text: sty.dim.Render(" · " + c.owner() + "file"), Drop: RailNormal})
	}
	if c.Path != "" {
		h.left = append(h.left, screenField(c.Path))
	}
	if c.Behind {
		// Kept longer than the path it qualifies: a long path is the first
		// field to go, and the word is the one thing here the reader would
		// not otherwise learn.
		h.left = append(h.left, RailSegment{Text: sty.warn.Render(" · outdated"), Drop: RailNormal})
	}
	if c.Changed > 0 {
		h.left = append(h.left, RailSegment{
			Text: sty.accent.Render(" · " + plural(c.Changed, "change") + " unwritten"),
			Drop: RailVital,
		})
	}
	return h
}

// owner is whose file the header's path is, where there are two it could
// be, as the word in front of it.
func (c *ConfigScreen) owner() string {
	switch {
	case !c.Scoped:
		return ""
	case c.Yours:
		return "global "
	}
	return "project "
}

// writeTarget is where the write line says the staged changes go: the path,
// and in a checkout the file's name for it ahead of the path.
func (c *ConfigScreen) writeTarget() string {
	if o := c.owner(); o != "" {
		return "the " + o + "file, " + c.Path
	}
	return c.Path
}

// scopeOffer is the switch between the two files, worded as where it would
// send the write rather than where the write is now — the header already
// says that.
func (c *ConfigScreen) scopeOffer() KeyOffer {
	if c.Yours {
		return keyOfferAs(keys.Screen.Scope, "write project")
	}
	return keyOfferAs(keys.Screen.Scope, "write global")
}

// footer is the keys the screen offers and the field that annotates them.
func (c *ConfigScreen) footer(width int) keyFooter {
	f := keyFooter{offers: c.offers(), register: c.keyList(), showing: c.keys, field: c.footField(), keepField: c.Changed > 0}
	if c.confirm != nil {
		f.taken = c.confirm.View(width)
		// The answers are what may never be clipped away: where the question
		// and its three answers do not share a row, they take two.
		if c.keeping && lipgloss.Width(c.confirm.Prompt)+2+lipgloss.Width(c.confirm.Answers) > width {
			f.taken = Clip(c.confirm.Prompt, width) + "\n" + Clip(c.confirm.Answers, width)
		}
	}
	return f
}

// offers is the key row for whichever surface holds the keyboard.
func (c *ConfigScreen) offers() []KeyOffer {
	keep := keyOffer(keys.Screen.Quit)
	if row := c.rowAt(c.editRow); row != nil && row.Value != "" && !row.Secret {
		keep.Label = "keep " + row.Value
	}
	switch {
	case c.picker != nil:
		offers := []KeyOffer{keyOffer(keys.Select.Move)}
		if c.picker.Filtering {
			// esc steps out of the row before it keeps the value, so while
			// the row is open that is what it offers.
			keep = keyOfferAs(keys.Screen.Quit, "clear the filter, then close it")
		} else {
			offers = append(offers, keyOffer(keys.Screen.Filter))
		}
		return append(offers, keyOfferAs(keys.Screen.Take, "set it"), keep)
	case c.secret != nil:
		return []KeyOffer{
			keyOfferAs(keys.Wait.UseKey, "use it"),
			keyOffer(keys.Wait.KeepKey),
		}
	case c.edit != nil:
		return []KeyOffer{
			keyOfferAs(keys.Screen.Take, "set it"),
			keep,
		}
	}
	offers := []KeyOffer{keyOffer(keys.Screen.Move), keyOfferAs(keys.Screen.Take, "change")}
	if c.list.Filtering {
		return append(offers, keyOfferAs(keys.Screen.Quit, "clear the filter, then close it"))
	} else {
		offers = append(offers, keyOffer(keys.Screen.Filter), keyOffer(keys.Screen.Reset))
		if c.Scoped {
			offers = append(offers, c.scopeOffer())
		}
	}
	if c.Changed > 0 {
		// With something staged the way out is a discard, and it says so with
		// the ask in the same breath: a row that promised only "discard" would
		// be describing the old key, and one that promised only "leave" would be
		// hiding what leaving costs.
		way := "discard, after asking"
		switch {
		case c.Held == c.Changed:
			way = "keep on this session, after asking"
		case c.Held > 0:
			way = "leave, after asking"
		}
		return append(offers, keyOfferAs(keys.Screen.Write, "write "+plural(c.Changed, "change")), wayOut(way))
	}
	return append(offers, wayOut("leave"))
}

// keyList is every key the screen has, for `[?]`. It says what the compact
// row cannot: which keys belong to a picker rather than to the list.
//
// The two ways out are read from what is staged, the way the compact row is:
// a register that promised a question with nothing staged would be describing
// a screen the reader is not on.
func (c *ConfigScreen) keyList() []KeyOffer {
	out, quit := "leave the picker, or leave the screen writing nothing", "leave the screen writing nothing"
	if c.Changed > 0 {
		out, quit = "leave the picker, or ask before discarding the lot",
			"ask before discarding the lot and leaving"
		if c.Held > 0 {
			out, quit = "leave the picker, or ask where the changes go",
				"ask whether to write the changes or leave them on this session, and leave"
		}
	}
	list := []KeyOffer{
		keyOfferAs(keys.Screen.Move, "move between settings"),
		keyOfferAs(keys.Screen.Take, "open the setting under the pointer, and take what is in it"),
		keyOfferAs(keys.Screen.Filter, "filter the settings by name"),
		keyOfferAs(keys.Query.Rub, "delete a character from the filter or the field being typed into"),
		keyOfferAs(keys.Screen.Reset, "reset this setting to its default"),
		keyOfferAs(keys.Screen.Write, "write every staged change to "+c.writeTarget()),
	}
	if c.Scoped {
		list = append(list, keyOfferAs(keys.Screen.Scope, "switch the write between the project file and the global one"))
	}
	return append(list, keyOfferAs(keys.Screen.Quit, "clear the filter, then close it; "+out), keyOfferAs(keys.Screen.Quit, quit))
}

// footField annotates the key row. It is the count of settings until
// something is staged, and then it is the sentence the screen wants the
// reader to have read before they walk away.
func (c *ConfigScreen) footField() string {
	switch {
	case c.picker != nil || c.edit != nil || c.secret != nil:
		return ""
	case c.Changed > 0:
		return "nothing is written until " + keys.Bracket(keys.Screen.Write)
	case c.list.Filtering:
		return ""
	}
	return plural(len(c.Rows), "setting")
}

// sync rebuilds the list from Rows. It runs before every Update and every
// View because the host replaces Rows after each change, and the window and
// the query the list is showing have to survive that.
func (c *ConfigScreen) sync() {
	c.shown = c.match(c.Rows, configFields)
	opts := make([]SelectOption, 0, len(c.shown)+6)
	c.optRow = c.optRow[:0]
	rail := func(label string) {
		opts = append(opts, SelectOption{Label: label, Header: true})
		c.optRow = append(c.optRow, -1)
	}
	group := ""
	for _, i := range c.shown {
		row := c.Rows[i]
		if row.Group != "" && row.Group != group {
			if group != "" {
				rail("")
			}
			group = row.Group
			rail(group)
		}
		opts = append(opts, SelectOption{
			Label: row.Label, Value: row.Value, valueTone: row.ValueTone,
			Desc: qualifier(row.Detail), Meta: row.Source, metaTone: row.SourceTone,
		})
		c.optRow = append(c.optRow, i)
	}
	c.show(opts, len(c.Rows), c.optIndex(c.Focus))
	c.list.Filterable = true
	c.list.QueryHint = "type to filter the settings"
}

// qualifier is how a note about a value joins it: an em-dash, because `normal
// — reads fold, mutations never do` reads as one clause and `normal reads
// fold` reads as a sentence that is not true. A host that already wrote the
// dash keeps it.
func qualifier(detail string) string {
	if detail == "" || strings.HasPrefix(detail, "—") {
		return detail
	}
	return "— " + detail
}

// configFields are what a setting is matched by: its name or the config key
// behind it, so a reader who knows the key can type it.
func configFields(row ConfigRow) []string { return []string{row.Label, row.Key} }

// refilter re-runs the match after a keystroke changed the query: the
// pointer goes to the first row that survived it, and whatever was open on
// the row under it goes.
func (c *ConfigScreen) refilter() {
	c.picker, c.edit, c.secret = nil, nil, nil
	c.refocus(c.Rows, configFields)
	c.sync()
}

// refilterPicker is the same for the picker's own query, over the options of
// the row being changed.
func (c *ConfigScreen) refilterPicker() {
	row := c.rowAt(c.editRow)
	if row == nil {
		return
	}
	shown := Filter(row.Options, strings.TrimSpace(c.picker.Query), func(o SelectOption) []string {
		return []string{o.Label}
	})
	matches := make([]SelectOption, 0, len(shown))
	for _, i := range shown {
		matches = append(matches, row.Options[i])
	}
	c.picker.Options = matches
	c.picker.Focus = 0
}

// walked walks the pointer over the rows the filter left showing, and puts
// away whatever was open on the row it left.
func (c *ConfigScreen) walked(pressed string) bool {
	if !c.movedShown(pressed, keys.Screen.Move) {
		return false
	}
	c.picker, c.edit, c.secret = nil, nil, nil
	c.sync()
	return true
}

// current is the row under the pointer, or nil when the filter left none.
func (c *ConfigScreen) current() *ConfigRow { return c.rowAt(c.Focus) }

func (c *ConfigScreen) rowAt(i int) *ConfigRow {
	if i < 0 || i >= len(c.Rows) {
		return nil
	}
	return &c.Rows[i]
}

// optIndex maps a row index to its place in the list the card is drawing,
// which the rails shift. A row the filter hid takes the nearest one showing.
func (c *ConfigScreen) optIndex(row int) int {
	first := 0
	for i, at := range c.optRow {
		if at == row {
			return i
		}
		if at >= 0 && first == 0 {
			first = i
		}
	}
	return first
}

// lineEdit is the one-line field a screen opens over the row under its
// pointer: a setting with no answers to choose from, the name a snippet or a
// saved chat is being renamed to. It is the filter row's own `▸ text█`
// grammar, because the reader has met that row on every picker in the product
// and a second idea of "a line you type into" is one more thing to learn.
type lineEdit struct {
	value []rune
	// lead names what is being typed where the row above it does not — a
	// rename row opens holding a name that is already there, and `rename ▸`
	// is what says the field is not the filter. Empty is a field the row it
	// opened under has already named.
	lead string
	// hint is what the row says while nothing has been typed into it.
	hint string
}

func (e *lineEdit) update(msg tea.KeyPressMsg) {
	switch pressed := msg.String(); {
	case keys.Is(pressed, keys.Query.Rub):

		if len(e.value) > 0 {
			e.value = e.value[:len(e.value)-1]
		}
	default:
		e.value = append(e.value, []rune(typedRunes(msg))...)
	}
}

// renameKey answers a key while a browser's rename row is up: enter commits,
// esc keeps the name, and everything else is typed into the row. Either of
// the first two closes the row. It reports the name enter committed, trimmed;
// the caller asks its host for nothing where the name is empty or unchanged.
func renameKey(edit **lineEdit, msg tea.KeyPressMsg) (string, bool) {
	switch pressed := msg.String(); {
	case keys.Is(pressed, keys.Select.Cancel):
		*edit = nil
		return "", false
	case keys.Is(pressed, keys.Screen.Take):
		name := strings.TrimSpace(string((*edit).value))
		*edit = nil
		return name, true
	}
	(*edit).update(msg)
	return "", false
}

func (e *lineEdit) view() string {
	row := ""
	if e.lead != "" {
		row = sty.dim.Render(e.lead + " ")
	}
	row += sty.info.Render("▸ ") + sty.queryText.Render(string(e.value)+queryCursor)
	if len(e.value) == 0 && e.hint != "" {
		row += sty.dim.Render(" " + e.hint)
	}
	return row
}

// lastFour is the tail of a secret the masked entry says it is replacing. It
// takes an already-masked value as readily as a raw one, because the row
// hands it whichever it is holding.
func lastFour(s string) string {
	r := []rune(strings.TrimLeft(s, "·"))
	if len(r) <= 4 {
		return string(r)
	}
	return string(r[len(r)-4:])
}

// cardWidthFor turns a usable width into the width a card primitive expects.
// The screen is a takeover and draws no frame, but the rows inside it are the
// card's rows and the card measures its columns against its own inner width.
func cardWidthFor(inner int) int { return inner + cardFrameWidth }

// indentBy moves one already-rendered row in by n columns without disturbing
// what is painted on it.
func indentBy(row string, n, width int) string {
	// A row with nothing painted on it is the blank between two rails, and
	// indenting nothing leaves trailing spaces on an empty line.
	if lipgloss.Width(row) == 0 {
		return ""
	}
	return Clip(strings.Repeat(" ", n)+row, width)
}

// WriteReceipt is the sentence every write answers with, on the screen that
// did it and in the transcript: what was written, to which file, and, when
// there is more to name than the file, what changed. It is one function so
// the settings screen, the model picker's default and the profile drafter say
// it in one shape: `wrote 2 changes to .shhh/config.toml · provider.model,
// behavior.command_timeout_seconds`.
func WriteReceipt(what, file string, changed ...string) string {
	receipt := "wrote " + what + " to " + file
	if len(changed) > 0 {
		receipt += " · " + strings.Join(changed, ", ")
	}
	return receipt
}

// Changes counts settings in the receipt's words: `1 change`, `2 changes`.
func Changes(n int) string { return plural(n, "change") }
