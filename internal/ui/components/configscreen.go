package components

// The config screen (
// docs/interface/surfaces.md#the-supporting-screens,
// ui_kits/cockpit/Tools.html). `shhh config` shipped before the cockpit and
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
// options. And nothing reaches the file until `[w]`: every edit is staged,
// the header counts what is standing against the file, and `[esc]` discards
// the lot.
//
// It is a passive component like the rest of this package. It owns no config
// semantics: a change resolves to a ConfigChange the host applies to its own
// copy, and the host hands back fresh Rows. That is why the screen can render
// `⏵⏵ auto` without knowing what a permission mode is.

import (
	"fmt"
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
	// Key is the config key `[w]` would write and `[r]` would clear. The screen
	// only carries it back to the host.
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
	// with ` · `, longest-lived last.
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
// is `[r]`: the key goes back to its default rather than to a value.
type ConfigChange struct {
	Key   string
	Value string
	Reset bool
}

// ConfigResult is what a key answered with: how the screen closed — `[w]`
// confirmed, or nothing was written, and Canceled and Write are never both
// true because `[esc]` discards — and the edit a key made with the screen
// still up. nil is a key that changed no setting.
type ConfigResult struct {
	Write    bool
	Canceled bool
	Change   *ConfigChange
}

// ConfigScreen is `shhh config`: a takeover surface, full width, no inspector
// rail, owning the keyboard for as long as it is up.
type ConfigScreen struct {
	// Path is the file `[w]` writes, stated in the header.
	Path string
	// Rows are the settings in the order they are shown.
	Rows []ConfigRow
	// Focus is an index into Rows and survives the host rebuilding them.
	Focus int
	// Changed is how many edits are standing against the file. The header counts
	// them and `[w]` is not offered while it is zero — a key that cannot act is
	// not offered (invariant 5).
	Changed int
	// MaxLines bounds the screen height; everything pinned comes off the list's
	// budget before its window is drawn. 0 is unbounded, which is what a test or
	// a host that sizes itself gets.
	MaxLines int
	// Notice is the line a key left behind — what `[w]` wrote, what `[r]` reset.
	// The host clears it on the next keystroke.
	Notice string

	menu    Select
	shown   []int
	optRow  []int
	picker  *Select
	editRow int
	edit    *configEdit
	secret  *SecretPrompt
	confirm *Confirm
	keys    bool
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
// a picker, a field, a masked entry or the write confirm owns the letters
// while it is up (invariant 5) — and the settings list answers otherwise.
func (c *ConfigScreen) Update(msg tea.KeyPressMsg) (done bool, result ConfigResult) {
	c.sync()
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
	case pressed == "up":
		c.move(-1)
		return false, ConfigResult{}
	case pressed == "down":
		c.move(1)
		return false, ConfigResult{}
	case keys.Is(pressed, keys.Screen.Take):
		c.open()
		return false, ConfigResult{}
	case keys.Is(pressed, keys.Select.Cancel):
		// esc leaves and changes nothing, which on this screen is literal: nothing
		// has reached the file yet, so discarding the staged edits is what "change
		// nothing" means.
		return true, ConfigResult{Canceled: true}
	}
	// With the query line open the query line is the surface, so w, r and q are
	// letters rather than keys — the same reading every picker in the product
	// makes.
	if c.menu.Filtering {
		c.menu.editQuery(msg)
		if c.menu.QueryChanged() {
			c.refilter()
		}
		return false, ConfigResult{}
	}
	switch {
	case pressed == "k":
		c.move(-1)
	case pressed == "j":
		c.move(1)
	case keys.Is(pressed, keys.Screen.Filter):
		c.menu.Filtering = true
	case pressed == keys.Shown(keys.Screen.Quit):
		return true, ConfigResult{Canceled: true}
	case keys.Is(pressed, keys.Screen.List):
		c.keys = !c.keys
	case keys.Is(pressed, keys.Screen.Reset):
		if row := c.current(); row != nil {
			return false, ConfigResult{Change: &ConfigChange{Key: row.Key, Reset: true}}
		}
	case keys.Is(pressed, keys.Screen.Write):
		if c.Changed > 0 {
			c.confirm = &Confirm{Prompt: sty.Body.Render(fmt.Sprintf(
				"Write %s to %s?", plural(c.Changed, "change"), c.Path))}
		}
	}
	return false, ConfigResult{}
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
		c.edit = &configEdit{value: []rune(row.Value)}
	}
}

func (c *ConfigScreen) updatePicker(msg tea.KeyPressMsg) (bool, ConfigResult) {
	switch pressed := msg.String(); {
	case keys.Is(pressed, keys.Select.Cancel):
		// esc keeps the current value — it is the one key on this screen that is
		// guaranteed to change nothing.
		c.picker = nil
		return false, ConfigResult{}
	case keys.Is(pressed, keys.Screen.Take):
		opts := c.picker.Options
		if len(opts) == 0 {
			return false, ConfigResult{}
		}
		chosen := opts[min(max(c.picker.Focus, 0), len(opts)-1)]
		c.picker = nil
		if row := c.rowAt(c.editRow); row != nil {
			return false, ConfigResult{Change: &ConfigChange{Key: row.Key, Value: chosen.Label}}
		}
		return false, ConfigResult{}
	}
	if c.picker.Filtering {
		switch msg.String() {
		case "up":
			c.picker.move(-1)
		case "down":
			c.picker.move(1)
		default:
			c.picker.editQuery(msg)
			if c.picker.QueryChanged() {
				c.refilterPicker()
			}
		}
		return false, ConfigResult{}
	}
	switch pressed := msg.String(); {
	case pressed == "up", pressed == "k":
		c.picker.move(-1)
	case pressed == "down", pressed == "j":
		c.picker.move(1)
	case keys.Is(pressed, keys.Screen.Filter):
		c.picker.Filtering = true
	}
	return false, ConfigResult{}
}

func (c *ConfigScreen) updateEdit(msg tea.KeyPressMsg) (bool, ConfigResult) {
	switch pressed := msg.String(); {
	case keys.Is(pressed, keys.Select.Cancel):
		c.edit = nil
		return false, ConfigResult{}
	case keys.Is(pressed, keys.Screen.Take):
		value := strings.TrimSpace(string(c.edit.value))
		c.edit = nil
		if row := c.rowAt(c.editRow); row != nil {
			return false, ConfigResult{Change: &ConfigChange{Key: row.Key, Value: value}}
		}
		return false, ConfigResult{}
	}
	c.edit.update(msg)
	return false, ConfigResult{}
}

func (c *ConfigScreen) updateSecret(msg tea.KeyPressMsg) (bool, ConfigResult) {
	done, result := c.secret.Update(msg)
	if !done {
		return false, ConfigResult{}
	}
	value, _ := result.(string)
	c.secret = nil
	// The masked entry resolves to "" on esc, which leaves the key that was
	// already there in place — esc never destroys.
	if value == "" {
		return false, ConfigResult{}
	}
	if row := c.rowAt(c.editRow); row != nil {
		return false, ConfigResult{Change: &ConfigChange{Key: row.Key, Value: value}}
	}
	return false, ConfigResult{}
}

func (c *ConfigScreen) updateConfirm(msg tea.KeyPressMsg) (bool, ConfigResult) {
	done, result := c.confirm.Update(msg)
	if !done {
		return false, ConfigResult{}
	}
	c.confirm = nil
	if yes, _ := result.(bool); yes {
		return true, ConfigResult{Write: true}
	}
	return false, ConfigResult{}
}

// SetSize gives the screen the terminal's rectangle. It lays itself out from
// the width it is rendered at, so only the height is kept.
func (c *ConfigScreen) SetSize(_, height int) { c.MaxLines = height }

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
	for _, row := range c.menu.queryRows(cardWidthFor(width - menuIndent)) {
		head = append(head, indentBy(row, menuIndent, width))
	}
	return ScreenChrome{
		Header:   c.header(),
		Head:     head,
		Foot:     c.footer(width).Rows(width),
		Notice:   c.Notice,
		MaxLines: c.MaxLines,
		Reserve:  len(inline),
	}.View(width, func(budget int) []string { return c.bodyRows(width, budget, inline) })
}

// bodyRows is the settings list with the open sub-surface spliced in under
// the row it belongs to. That splice is the whole shape of the screen: the
// picker is not a modal over the screen, so the setting being changed stays
// on screen above its own options.
func (c *ConfigScreen) bodyRows(width, budget int, inline []string) []string {
	rows, _, at := c.menu.visibleRowsFocus(cardWidthFor(width-menuIndent), budget, false)
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
		// The masked entry's own key row is dropped: the screen already has one at
		// its foot, and the two would offer the same two keys twice.
		lines := strings.Split(c.secret.View(inner), "\n")
		rows = append(rows, lines[:max(len(lines)-1, 1)]...)
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
	if c.MaxLines > 0 {
		return max(min(pickerRows, c.MaxLines/2)-pinned, 1)
	}
	return max(pickerRows-pinned, 1)
}

// header names the command, which file it is over, and what is standing
// against that file. The path goes before the count of unwritten changes: a
// change that has not reached the file yet is the one thing on this row a
// reader cannot afford to lose sight of.
func (c *ConfigScreen) header() ScreenHeader {
	h := ScreenHeader{Left: []RailSegment{screenTitle("shhh config")}, Keys: screenHeaderKeys()}
	if c.Path != "" {
		h.Left = append(h.Left, screenField(c.Path))
	}
	if c.Changed > 0 {
		h.Left = append(h.Left, RailSegment{
			Text: sty.Accent.Render(" · " + plural(c.Changed, "change") + " unwritten"),
			Drop: RailVital,
		})
	}
	return h
}

// footer is the keys the screen offers and the field that annotates them.
func (c *ConfigScreen) footer(width int) KeyFooter {
	f := KeyFooter{Offers: c.offers(), Register: c.keyList(), Showing: c.keys, Field: c.footField()}
	if c.confirm != nil {
		f.Taken = c.confirm.View(width)
	}
	return f
}

// offers is the key row for whichever surface holds the keyboard.
func (c *ConfigScreen) offers() []KeyOffer {
	keep := keyOffer(keys.Screen.Keep)
	if row := c.rowAt(c.editRow); row != nil && row.Value != "" && !row.Secret {
		keep.Label = "keep " + row.Value
	}
	switch {
	case c.picker != nil:
		offers := []KeyOffer{keyOffer(keys.Select.Move)}
		if c.picker.Filtering {
			offers = append(offers, keyOffer(keys.Screen.ClearQ))
		} else {
			offers = append(offers, keyOffer(keys.Screen.Filter))
		}
		return append(offers, keyOffer(keys.Screen.Take), keep)
	case c.secret != nil:
		return []KeyOffer{
			keyOfferAs(keys.Wait.UseKey, "use it"),
			keyOffer(keys.Wait.KeepKey),
		}
	case c.edit != nil:
		return []KeyOffer{
			keyOfferAs(keys.Screen.Take, "set it"),
			keyOfferAs(keys.Screen.ClearQ, "clear the field"),
			keep,
		}
	}
	offers := []KeyOffer{keyOffer(keys.Select.Move), keyOfferAs(keys.Screen.Take, "change")}
	if c.menu.Filtering {
		offers = append(offers, keyOffer(keys.Screen.ClearQ))
	} else {
		offers = append(offers, keyOffer(keys.Screen.Filter), keyOffer(keys.Screen.Reset))
	}
	if c.Changed > 0 {
		offers = append(offers, keyOffer(keys.Screen.Write))
	}
	return append(offers, keyOfferAs(keys.Select.Cancel, "discard"))
}

// keyList is every key the screen has, for `[?]`. It says what the compact
// row cannot: which keys belong to a picker rather than to the list.
func (c *ConfigScreen) keyList() []KeyOffer {
	return []KeyOffer{
		keyOfferAs(keys.Screen.Move, "move between settings"),
		keyOfferAs(keys.Screen.Take, "change the setting under the pointer"),
		keyOfferAs(keys.Screen.Filter, "filter the settings by name"),
		keyOfferAs(keys.Screen.ClearQ, "clear the filter, or the field being typed into"),
		keyOfferAs(keys.Screen.Reset, "reset this setting to its default"),
		keyOfferAs(keys.Screen.Write, "write every staged change to "+c.Path),
		keyOfferAs(keys.Select.Cancel, "leave the picker, or leave the screen writing nothing"),
		keyOfferAs(keys.Screen.Quit, "leave the screen writing nothing"),
	}
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
	case c.menu.Filtering:
		return ""
	}
	return plural(len(c.Rows), "setting")
}

// sync rebuilds the list from Rows. It runs before every Update and every
// View because the host replaces Rows after each change, and the window and
// the query the list is showing have to survive that.
func (c *ConfigScreen) sync() {
	c.shown = c.match()
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
			Label: row.Label, Value: row.Value, ValueTone: row.ValueTone,
			Desc: qualifier(row.Detail), Meta: row.Source, MetaTone: row.SourceTone,
		})
		c.optRow = append(c.optRow, i)
	}
	c.menu.Options = opts
	c.menu.Total = len(c.Rows)
	c.menu.Filterable = true
	c.menu.Unnumbered = true
	c.menu.QueryHint = "type to filter the settings"
	c.menu.Focus = c.optIndex(c.Focus)
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

// match is the settings the query left showing. The rule lives here rather
// than in the card because the card never filters: a setting is matched by
// its name or by the config key behind it, so a reader who knows the key can
// type it.
func (c *ConfigScreen) match() []int {
	query := strings.ToLower(strings.TrimSpace(c.menu.Query))
	out := make([]int, 0, len(c.Rows))
	for i, row := range c.Rows {
		if query == "" ||
			strings.Contains(strings.ToLower(row.Label), query) ||
			strings.Contains(strings.ToLower(row.Key), query) {
			out = append(out, i)
		}
	}
	return out
}

// refilter re-runs the match after a keystroke changed the query, and puts
// the pointer on the first row that survived it — the rows under it are not
// the rows that were there a moment ago.
func (c *ConfigScreen) refilter() {
	c.picker, c.edit, c.secret = nil, nil, nil
	if shown := c.match(); len(shown) > 0 {
		c.Focus = shown[0]
	}
	c.sync()
}

// refilterPicker is the same for the picker's own query, over the options of
// the row being changed.
func (c *ConfigScreen) refilterPicker() {
	row := c.rowAt(c.editRow)
	if row == nil {
		return
	}
	query := strings.ToLower(strings.TrimSpace(c.picker.Query))
	matches := make([]SelectOption, 0, len(row.Options))
	for _, o := range row.Options {
		if query == "" || strings.Contains(strings.ToLower(o.Label), query) {
			matches = append(matches, o)
		}
	}
	c.picker.Options = matches
	c.picker.Focus = 0
}

// move steps the pointer to the next setting the filter left showing,
// stopping at either end rather than wrapping.
func (c *ConfigScreen) move(delta int) {
	if len(c.shown) == 0 {
		return
	}
	at := 0
	for i, row := range c.shown {
		if row == c.Focus {
			at = i
			break
		}
	}
	c.Focus = c.shown[min(max(at+delta, 0), len(c.shown)-1)]
	c.picker, c.edit, c.secret = nil, nil, nil
	c.sync()
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

// configEdit is the one-line field a setting with no answers to choose from
// opens under itself. It is the filter row's own `▸ text█` grammar,
// because the reader has met that row on every picker in the product and a
// second idea of "a line you type into" is exactly what this story deletes.
type configEdit struct{ value []rune }

func (e *configEdit) update(msg tea.KeyPressMsg) {
	switch pressed := msg.String(); {
	case keys.Is(pressed, keys.Screen.ClearQ):
		e.value = nil
	case pressed == "backspace":
		if len(e.value) > 0 {
			e.value = e.value[:len(e.value)-1]
		}
	default:
		e.value = append(e.value, []rune(typedRunes(msg))...)
	}
}

func (e *configEdit) view() string {
	row := sty.Info.Render("▸ ") + sty.QueryText.Render(string(e.value)+queryCursor)
	if len(e.value) == 0 {
		row += sty.Dim.Render(" type a value")
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
