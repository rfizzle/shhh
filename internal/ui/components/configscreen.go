package components

// The config screen (S-127, DESIGN-TUI.md §19a, ui_kits/cockpit/Tools.html).
// `shhh config` shipped before the cockpit and invented its own list, its own
// idea of a value and its own key words for them. It is re-cut here from
// parts that already exist: the §4a window with its markers and its filter
// row, the §6a row with a right-hand field, the §12a bracketed-key hint line,
// the masked entry an auth failure opens, and the §5 inline confirm. Almost
// nothing here is new — the win is deletion, and the gain is that a reader
// who knows the cockpit already knows this screen.
//
// Two rules shape it and both come from §19a. A value is a row, and changing
// one opens the picker *under* that row rather than over the screen, so the
// setting being changed stays visible above the options. And nothing reaches
// the file until `[w]`: every edit is staged, the header counts what is
// standing against the file, and `[esc]` discards the lot.
//
// It is a passive component like the rest of this package. It owns no config
// semantics: a change resolves to a ConfigChange the host applies to its own
// copy, and the host hands back fresh Rows. That is why the screen can render
// `⏵⏵ auto` without knowing what a permission mode is.

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// menuIndent is the column the settings list starts at, and pickerIndent the
// one level further in that an open picker sits at (§19a: "indented one
// level"). Two levels is all this screen has.
const (
	menuIndent   = 2
	pickerIndent = 4
)

// ConfigRow is one setting: what it is called, what it is set to, and where
// that answer came from. The host builds these from its config and rebuilds
// them after every change — the screen never computes a value.
type ConfigRow struct {
	// Group is the rail this row sits under — SESSION, MODEL, WORKSPACE.
	// Rails are labels rather than options (§4a): the pointer steps over
	// them and the markers do not count them. A row whose Group differs from
	// the row before it opens a new rail.
	Group string
	// Key is the config key `[w]` would write and `[r]` would clear. The
	// screen only carries it back to the host.
	Key string
	// Label is the setting's name, in the left column.
	Label string
	// Value is what it is set to, as it reads on the row: `⏵⏵ auto`,
	// `⛨ workspace-write`, `25`. A masked secret is already masked here —
	// see MaskSecret.
	Value string
	// ValueTone reads the value: safe, a door left open, at risk, or an
	// unremarkable fact. The glyph beside it says the same thing, so the
	// colour never carries it alone (invariant 1).
	ValueTone FieldTone
	// Detail qualifies the value in dim text on the same row — `— reads
	// fold, mutations never do`. It is a note about the value, which is why
	// it is never toned.
	Detail string
	// Source is the right-hand field: where the answer came from
	// (`default`, `user`, `user · ~/.config/shhh/config.toml`), or the
	// reason the host cannot honour the setting at all. §19a: "why is this
	// on" is the only question a config screen is ever asked, and a setting
	// the machine cannot keep says so rather than being hidden (invariant 4).
	Source     string
	SourceTone FieldTone
	// Options are what `[enter]` offers. A row with none opens a field to
	// type into instead.
	Options []SelectOption
	// Secret marks a value that must never be echoed: `[enter]` opens the
	// masked entry rather than a field showing what is already there.
	Secret bool
}

// ConfigChange is one staged edit, resolved to the host as it is made. Reset
// is `[r]`: the key goes back to its default rather than to a value.
type ConfigChange struct {
	Key   string
	Value string
	Reset bool
}

// ConfigResult is how the screen closed: `[w]` confirmed, or nothing was
// written. Canceled and Write are never both true — `[esc]` discards.
type ConfigResult struct {
	Write    bool
	Canceled bool
}

// ConfigScreen is `shhh config`: a takeover surface, full width, no inspector
// rail, owning the keyboard for as long as it is up (§19).
type ConfigScreen struct {
	// Path is the file `[w]` writes, stated in the header.
	Path string
	// Rows are the settings in the order they are shown.
	Rows []ConfigRow
	// Focus is an index into Rows and survives the host rebuilding them.
	Focus int
	// Changed is how many edits are standing against the file. The header
	// counts them and `[w]` is not offered while it is zero — a key that
	// cannot act is not offered (invariant 5).
	Changed int
	// MaxLines bounds the screen height; everything pinned comes off the
	// list's budget before its window is drawn (§4a). 0 is unbounded, which
	// is what a test or a host that sizes itself gets.
	MaxLines int
	// Notice is the line a key left behind — what `[w]` wrote, what `[r]`
	// reset. The host clears it on the next keystroke.
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

// MaskSecret renders a secret the way §19a asks for: the last four characters
// and nothing else. A key too short to have four is all dots, because a mask
// that reveals most of a short key has masked nothing.
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
func (c *ConfigScreen) Update(msg tea.KeyMsg) (done bool, result any) {
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

func (c *ConfigScreen) updateMenu(msg tea.KeyMsg) (bool, any) {
	key := msg.String()
	switch key {
	case "up":
		c.move(-1)
		return false, nil
	case "down":
		c.move(1)
		return false, nil
	case "enter":
		c.open()
		return false, nil
	case "esc", "ctrl+c":
		// esc leaves and changes nothing, which on this screen is literal:
		// nothing has reached the file yet, so discarding the staged edits is
		// what "change nothing" means (§19a).
		return true, ConfigResult{Canceled: true}
	}
	// With the query line open the query line is the surface, so w, r and q
	// are letters rather than keys — the same reading every picker in the
	// product makes (§4a).
	if c.menu.Filtering {
		c.menu.editQuery(msg)
		if c.menu.QueryChanged() {
			c.refilter()
		}
		return false, nil
	}
	switch key {
	case "k":
		c.move(-1)
	case "j":
		c.move(1)
	case "/":
		c.menu.Filtering = true
	case "q":
		return true, ConfigResult{Canceled: true}
	case "?":
		c.keys = !c.keys
	case "r":
		if row := c.current(); row != nil {
			return false, ConfigChange{Key: row.Key, Reset: true}
		}
	case "w":
		if c.Changed > 0 {
			c.confirm = &Confirm{Prompt: bodyStyle.Render(fmt.Sprintf(
				"Write %s to %s?", plural(c.Changed, "change"), c.Path))}
		}
	}
	return false, nil
}

// open is what `[enter]` does to the row under the pointer: a picker for a
// setting with answers, the masked entry for a secret, a field for anything
// else. All three open under the row rather than over the screen (§19a).
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

func (c *ConfigScreen) updatePicker(msg tea.KeyMsg) (bool, any) {
	switch msg.String() {
	case "esc", "ctrl+c":
		// esc keeps the current value — it is the one key on this screen
		// that is guaranteed to change nothing (§19a).
		c.picker = nil
		return false, nil
	case "enter":
		opts := c.picker.Options
		if len(opts) == 0 {
			return false, nil
		}
		chosen := opts[min(max(c.picker.Focus, 0), len(opts)-1)]
		c.picker = nil
		if row := c.rowAt(c.editRow); row != nil {
			return false, ConfigChange{Key: row.Key, Value: chosen.Label}
		}
		return false, nil
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
		return false, nil
	}
	switch msg.String() {
	case "up", "k":
		c.picker.move(-1)
	case "down", "j":
		c.picker.move(1)
	case "/":
		c.picker.Filtering = true
	}
	return false, nil
}

func (c *ConfigScreen) updateEdit(msg tea.KeyMsg) (bool, any) {
	switch msg.String() {
	case "esc", "ctrl+c":
		c.edit = nil
		return false, nil
	case "enter":
		value := strings.TrimSpace(string(c.edit.value))
		c.edit = nil
		if row := c.rowAt(c.editRow); row != nil {
			return false, ConfigChange{Key: row.Key, Value: value}
		}
		return false, nil
	}
	c.edit.update(msg)
	return false, nil
}

func (c *ConfigScreen) updateSecret(msg tea.KeyMsg) (bool, any) {
	done, result := c.secret.Update(msg)
	if !done {
		return false, nil
	}
	value, _ := result.(string)
	c.secret = nil
	// The masked entry resolves to "" on esc, which leaves the key that was
	// already there in place — esc never destroys.
	if value == "" {
		return false, nil
	}
	if row := c.rowAt(c.editRow); row != nil {
		return false, ConfigChange{Key: row.Key, Value: value}
	}
	return false, nil
}

func (c *ConfigScreen) updateConfirm(msg tea.KeyMsg) (bool, any) {
	done, result := c.confirm.Update(msg)
	if !done {
		return false, nil
	}
	c.confirm = nil
	if yes, _ := result.(bool); yes {
		return true, ConfigResult{Write: true}
	}
	return false, nil
}

// View renders the screen: the §17c header and its rule, the settings list
// with whatever is open under the row being changed, and one hint line at the
// foot.
func (c *ConfigScreen) View(width int) string {
	if width <= 0 {
		return ""
	}
	c.sync()
	foot := c.footRows(width)
	inline := c.inlineRows(width)
	head := []string{c.headerRow(width), reviewRule(width), ""}
	// The settings' own filter row is pinned above the list the way it is on
	// every card (§4a), so what it spends comes off the list's budget before
	// the window is drawn.
	for _, row := range c.menu.queryRows(cardWidthFor(width - menuIndent)) {
		head = append(head, indentBy(row, menuIndent, width))
	}

	pinned := len(head) + 1 + len(foot)
	if c.Notice != "" {
		pinned++
	}
	rows := append(head, c.bodyRows(width, c.budget(pinned+len(inline)), inline)...)
	rows = append(rows, "")
	rows = append(rows, foot...)
	if c.Notice != "" {
		rows = append(rows, dimStyle.Render(clip(c.Notice, width)))
	}
	return strings.Join(rows, "\n")
}

// budget is how many rows the settings list may spend: the screen's height
// less everything pinned around it. An unbounded screen windows nothing.
func (c *ConfigScreen) budget(pinned int) int {
	if c.MaxLines <= 0 {
		return 0
	}
	return max(c.MaxLines-pinned, 1)
}

// bodyRows is the settings list with the open sub-surface spliced in under
// the row it belongs to. That splice is the whole shape of §19a: the picker
// is not a modal over the screen, so the setting being changed stays on
// screen above its own options.
func (c *ConfigScreen) bodyRows(width, budget int, inline []string) []string {
	rows, _, at := c.menu.visibleRowsFocus(cardWidthFor(width-menuIndent), budget, false)
	out := make([]string, 0, len(rows)+len(inline))
	for i, row := range rows {
		out = append(out, indentBy(row, menuIndent, width))
		if i == at {
			out = append(out, inline...)
		}
	}
	// A focus the window does not hold — a filter that matched nothing —
	// leaves the sub-surface with no row to sit under, so it goes at the
	// foot of the list rather than nowhere.
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
		// The masked entry's own key row is dropped: the screen already has
		// one at its foot, and the two would offer the same two keys twice.
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
// is what the artboard draws and what the §4a window is sized for.
func (c *ConfigScreen) pickerBudget(pinned int) int {
	const pickerRows = 8
	if c.MaxLines > 0 {
		return max(min(pickerRows, c.MaxLines/2)-pinned, 1)
	}
	return max(pickerRows-pinned, 1)
}

// headerRow is the §17c header: the command, its subject, what is standing
// against the file, and the two keys every one of these screens offers. The
// right-hand keys drop before the subject does — they annotate the line, and
// an annotation goes first (§16).
func (c *ConfigScreen) headerRow(width int) string {
	left := brightStyle().Render("shhh config")
	if c.Path != "" {
		left += dimStyle.Render(" · " + c.Path)
	}
	if c.Changed > 0 {
		left += accentStyle.Render(" · " + plural(c.Changed, "change") + " unwritten")
	}
	right := dimStyle.Render("[?] keys · [q] quit")
	if pad := width - lipgloss.Width(left) - lipgloss.Width(right); pad >= 2 {
		return left + strings.Repeat(" ", pad) + right
	}
	return clip(left, width)
}

// footRows are the keys the screen offers and the field that annotates them.
// The field drops first (§16); the offers never truncate, they wrap
// (invariant 4).
func (c *ConfigScreen) footRows(width int) []string {
	if c.confirm != nil {
		return []string{clip(c.confirm.View(width), width)}
	}
	if c.keys {
		rows := make([]string, 0, len(c.keyList())+1)
		for _, offer := range c.keyList() {
			rows = append(rows, clip(keyOffers([]KeyOffer{offer}), width))
		}
		return append(rows, clip(keyOffers([]KeyOffer{{Key: "[?]", Label: "hide the keys"}}), width))
	}
	rows := wrapOffers(c.offers(), width)
	if len(rows) == 0 {
		return rows
	}
	field := c.footField()
	if field == "" {
		return rows
	}
	painted := dimStyle.Render(field)
	if pad := width - lipgloss.Width(rows[0]) - lipgloss.Width(painted); pad >= 2 {
		rows[0] += strings.Repeat(" ", pad) + painted
	}
	return rows
}

// offers is the key row for whichever surface holds the keyboard.
func (c *ConfigScreen) offers() []KeyOffer {
	keep := KeyOffer{Key: "[esc]", Label: "keep the current value"}
	if row := c.rowAt(c.editRow); row != nil && row.Value != "" && !row.Secret {
		keep.Label = "keep " + row.Value
	}
	switch {
	case c.picker != nil:
		offers := []KeyOffer{{Key: "[↑↓]", Label: "move"}}
		if c.picker.Filtering {
			offers = append(offers, KeyOffer{Key: "[ctrl+u]", Label: "clear the filter"})
		} else {
			offers = append(offers, KeyOffer{Key: "[/]", Label: "filter"})
		}
		return append(offers, KeyOffer{Key: "[enter]", Label: "take it"}, keep)
	case c.secret != nil:
		return []KeyOffer{
			{Key: "[enter]", Label: "use it"},
			{Key: "[esc]", Label: "keep the current key"},
		}
	case c.edit != nil:
		return []KeyOffer{
			{Key: "[enter]", Label: "set it"},
			{Key: "[ctrl+u]", Label: "clear the field"},
			keep,
		}
	}
	offers := []KeyOffer{{Key: "[↑↓]", Label: "move"}, {Key: "[enter]", Label: "change"}}
	if c.menu.Filtering {
		offers = append(offers, KeyOffer{Key: "[ctrl+u]", Label: "clear the filter"})
	} else {
		offers = append(offers, KeyOffer{Key: "[/]", Label: "filter"},
			KeyOffer{Key: "[r]", Label: "reset to default"})
	}
	if c.Changed > 0 {
		offers = append(offers, KeyOffer{Key: "[w]", Label: "write the file"})
	}
	return append(offers, KeyOffer{Key: "[esc]", Label: "discard"})
}

// keyList is every key the screen has, for `[?]`. It says what the compact
// row cannot: which keys belong to a picker rather than to the list.
func (c *ConfigScreen) keyList() []KeyOffer {
	return []KeyOffer{
		{Key: "[↑↓/jk]", Label: "move between settings"},
		{Key: "[enter]", Label: "change the setting under the pointer"},
		{Key: "[/]", Label: "filter the settings by name"},
		{Key: "[ctrl+u]", Label: "clear the filter, or the field being typed into"},
		{Key: "[r]", Label: "reset this setting to its default"},
		{Key: "[w]", Label: "write every staged change to " + c.Path},
		{Key: "[esc]", Label: "leave the picker, or leave the screen writing nothing"},
		{Key: "[q]", Label: "leave the screen writing nothing"},
	}
}

// footField annotates the key row. It is the count of settings until
// something is staged, and then it is the sentence §19a wants the reader to
// have read before they walk away.
func (c *ConfigScreen) footField() string {
	switch {
	case c.picker != nil || c.edit != nil || c.secret != nil:
		return ""
	case c.Changed > 0:
		return "nothing is written until [w]"
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

// qualifier is how a note about a value joins it: an em-dash, because
// `normal — reads fold, mutations never do` reads as one clause and
// `normal reads fold` reads as a sentence that is not true. A host that
// already wrote the dash keeps it.
func qualifier(detail string) string {
	if detail == "" || strings.HasPrefix(detail, "—") {
		return detail
	}
	return "— " + detail
}

// match is the settings the query left showing. The rule lives here rather
// than in the card because the card never filters (§4a): a setting is matched
// by its name or by the config key behind it, so a reader who knows the key
// can type it.
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
// the rows that were there a moment ago (S-112).
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
// opens under itself. It is the filter row's own `▸ text█` grammar (§4a),
// because the reader has met that row on every picker in the product and a
// second idea of "a line you type into" is exactly what this story deletes.
type configEdit struct{ value []rune }

func (e *configEdit) update(msg tea.KeyMsg) {
	switch msg.String() {
	case "ctrl+u":
		e.value = nil
	case "backspace":
		if len(e.value) > 0 {
			e.value = e.value[:len(e.value)-1]
		}
	default:
		e.value = append(e.value, []rune(typedRunes(msg))...)
	}
}

func (e *configEdit) view() string {
	row := infoStyle.Render("▸ ") + queryTextStyle.Render(string(e.value)+queryCursor)
	if len(e.value) == 0 {
		row += dimStyle.Render(" type a value")
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
// The screen is a takeover and draws no frame (§19), but the rows inside it
// are the card's rows and the card measures its columns against its own inner
// width.
func cardWidthFor(inner int) int { return inner + cardFrameWidth }

// indentBy moves one already-rendered row in by n columns without disturbing
// what is painted on it.
func indentBy(row string, n, width int) string {
	// A row with nothing painted on it is the blank between two rails, and
	// indenting nothing leaves trailing spaces on an empty line.
	if lipgloss.Width(row) == 0 {
		return ""
	}
	return clip(strings.Repeat(" ", n)+row, width)
}
