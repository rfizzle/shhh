package components

// The context surface (docs/interface/surfaces.md#the-context-surface, and
// ui_kits/cockpit/Context.html in the shhh Design System project).
//
// The occupancy breakdown on demand. The pressure card already draws this
// accounting, but only when the window is nearly full and only as a sentence
// about what to do next; this is the same reading with nothing to decide, so
// it is a takeover with no offers on it and a way out.
//
// Two panels, because there are two questions. The grid is the block meter
// wrapped to the rail's own width, ten rows deep, so the whole window is on
// screen at once and how much is left is a shape rather than a number to
// read; each category takes one unbroken run of it in its own tone. The
// legend beside it names those tones and states what each category cost.
//
// The five tones are not a new ramp — each is the colour the product already
// draws the thing that category is made of, which is what lets the grid be
// tinted at all: a run of cells means what its colour has always meant. See
// ContextTone, and the occupancy grid guideline.
//
// Pressure is deliberately not in the grid. It stays on the number, in the
// header and beside the total, because a grid that turned del at the alert
// threshold would stop saying what filled the window at exactly the moment
// that became the useful question.
//
// Under the two panels are the categories that are made of many things. They
// arrive folded, and a folded group states what it swallowed (invariant 4):
// `tool definitions · 24 tools · 22.3k` is an answer before it is opened, so
// opening one is a question the reader chose to ask.
//
// It is a passive component like the rest of this package. It owns no
// accounting: the host reads the session, decides what a category is, orders
// the rows and formats every number.

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

const (
	// contextGridRows is how deep the wrapped meter runs. Ten rows of
	// MeterCellsRail is 220 cells, so one cell is a little under half a
	// percent — fine enough that a category worth acting on is visible, and
	// coarse enough that the grid is a shape rather than a texture.
	contextGridRows = 10
	// contextGridGap separates the grid from the legend. Three columns,
	// because two would let a legend label touch the last empty cell of a
	// row and read as part of the meter.
	contextGridGap = 3
	// contextIndent is the column both panels start at, the same one the
	// metrics screen's table and blocks use.
	contextIndent = 2
	// contextLabelWidth is the legend's label column. It fits the longest
	// category name the host has ("tool definitions") with a space after it.
	contextLabelWidth = 18
	// contextTokensWidth and contextPctWidth are the legend's two numeric
	// columns, right-aligned so the reader scans a column rather than
	// parsing a row — the same rule the metrics table follows.
	contextTokensWidth = 9
	contextPctWidth    = 7
	// contextSwatchWidth is the legend's leading key: one cell of the grid's
	// own glyph, in the category's own tone, and a space.
	contextSwatchWidth = 2
	// contextItemIndent is where an opened group's items start: far enough
	// in that the run of them reads as belonging to the row above.
	contextItemIndent = 6
)

// ContextTone is which of the palette's existing jobs a category is drawn in.
// The six are not a new ramp: five of them are the colour the product already
// uses for the thing the category is made of, which is what lets the grid be
// tinted at all — a run of cells means what its colour has always meant.
type ContextTone int

const (
	// ContextPrompt is the system prompt: chrome, in the sense that matters
	// here — it is there whatever you do, and no key on any screen shrinks
	// it.
	ContextPrompt ContextTone = iota
	// ContextProject is what a file in the repository put into the prompt.
	// Info, the heading tone, because that is what it is: a document read in
	// whole, and the one category you shrink by editing something.
	ContextProject
	// ContextTools is the tool definitions. Accent is already the tool
	// colour — every tool glyph in the product is drawn in it — so the run of
	// cells the toolset occupies is the colour the toolset always is.
	ContextTools
	// ContextMessages is the conversation. Body, because the conversation is
	// the ordinary text this whole interface is made of, and because it is
	// the one category nobody should be encouraged to think of as waste.
	ContextMessages
	// ContextOutput is tool results. Dimmer is already what tool output is
	// drawn in wherever it appears, and it is also the category the window
	// trim elides first — the grid says which cells will go before it goes.
	ContextOutput
	// ContextFree is what is left. It is the empty cell's own grey and its
	// cells are `▱` rather than `▰`, so used and free stay apart under mono
	// where every tint above collapses into two shades.
	ContextFree
)

// Style is the token a category's cells and its legend row are drawn in.
func (t ContextTone) Style() lipgloss.Style {
	switch t {
	case ContextProject:
		return sty.Info
	case ContextTools:
		return sty.Accent
	case ContextMessages:
		return sty.Body
	case ContextOutput:
		return sty.Dimmer
	case ContextFree:
		return sty.Dim
	default:
		return sty.Status
	}
}

// ContextCategory is one row of the legend: a share of the window, already
// formatted, and the run of cells in the grid that is the same share drawn.
type ContextCategory struct {
	// Label names the category in the surface's own words ("tool
	// definitions"), not in the accounting's field names.
	Label string
	// Tokens and Pct are the two numeric columns, formatted by the host —
	// `22.3k`, `2.2%` — because those are readings of the session and this
	// is a renderer.
	Tokens string
	Pct    string
	// Share is the category's share of the window, 0–100, which is what
	// decides how much of the grid it takes. The host states the same number
	// in Pct; this is the one the grid measures with, so a rounded label and
	// a drawn run cannot come from two different figures.
	Share float64
	// Tone is which of the palette's jobs the run and the row are drawn in.
	Tone ContextTone
}

// ContextItem is one line of an opened group — one tool definition, one
// turn's messages.
type ContextItem struct {
	Label string
	// Share is the item's share of its own group, 0–100, drawn as a meter.
	// It is of the group and not of the window because a tool that is a
	// fifth of the tool budget is the thing worth seeing, and a fifth of the
	// tool budget is a rounding error against the window.
	Share  int
	Tokens string
	Pct    string
}

// ContextGroup is a category that is made of many things: the folded row that
// counts them, and the items under it once it is opened.
type ContextGroup struct {
	// Label names the group with the same words its legend row uses, so the
	// reader can see which category they just opened.
	Label string
	// Summary is what the folded row states besides its label — `24 tools ·
	// 22.3k`. Never empty: a fold that does not count what it swallowed is a
	// hide (invariant 4).
	Summary string
	// Items are the group's parts, largest first; the host drops the ones
	// too small to name and says so in More.
	Items []ContextItem
	// More is the clause under the last item for the parts that were not
	// named — `18 more · 6.9k together`. Empty when every part is listed.
	More string
	// Open is whether the group is unfolded. The host owns it so a surface
	// re-opened in the same session comes back the way it was left.
	Open bool
}

// ContextScreen is `/context`: a takeover surface, full width, no inspector
// rail, owning the keyboard for as long as it is up.
type ContextScreen struct {
	// Model and Provider are the two lines beside the grid: the model the
	// window belongs to, and who is serving it. Both are stated because the
	// same model name behind two gateways has two prices and, often enough,
	// two window sizes.
	Model, Provider string
	// Window is the context size as words (`1m`), Tokens what is in it
	// (`~312.4k`), and Pct the occupancy the grid draws.
	Window, Tokens string
	Pct            int
	// Warn and Alert are the host's own thresholds, passed to the meter so
	// the grid, the rails and the pressure card turn colour at the same two
	// numbers.
	Warn, Alert int
	// Source says where the total came from — `provider-reported`,
	// `estimated`. It is stated rather than implied because a guess and a
	// measurement are different facts about the same number.
	Source string
	// Categories are the legend, in the host's order, with the free-space
	// row last.
	Categories []ContextCategory
	// Groups are the folds under the two panels, in the host's order.
	Groups []ContextGroup
	// Cursor is which group the reading cursor is on. The host owns it for
	// the same reason it owns Open.
	Cursor int
	// ShowKeys is whether `?` has swapped the compact key row for the whole
	// register, in place — the idiom every takeover in the product shares.
	ShowKeys bool
	// MaxLines bounds the screen height; the header and its rule come off
	// the body's budget before anything is drawn. 0 is unbounded, which is
	// what a test or a host that sizes itself gets.
	MaxLines int
}

// ContextResult is what a key that changed something reports back: which
// group the cursor left on, and whether it was toggled.
type ContextResult struct {
	Cursor int
	Toggle bool
	Keys   bool
}

// Update is the surface's keyboard. It moves the cursor, folds and unfolds,
// swaps the key row for the register, and leaves — and it changes nothing
// about the session, which is why there is no key here that asks a question.
func (c *ContextScreen) Update(msg tea.KeyPressMsg) (done bool, result any) {
	switch pressed := msg.String(); {
	case keys.Is(pressed, keys.Context.Back):
		return true, nil
	case keys.Is(pressed, keys.Context.List):
		c.ShowKeys = !c.ShowKeys
		return false, ContextResult{Cursor: c.Cursor, Keys: true}
	case keys.Is(pressed, keys.Context.Move):
		c.move(pressed)
		return false, ContextResult{Cursor: c.Cursor}
	case keys.Is(pressed, keys.Context.Expand):
		if len(c.Groups) == 0 {
			return false, nil
		}
		c.Groups[c.cursor()].Open = !c.Groups[c.cursor()].Open
		return false, ContextResult{Cursor: c.Cursor, Toggle: true}
	}
	return false, nil
}

// move walks the cursor over the groups. It stops at both ends rather than
// wrapping: a list this short has no scroll to lose your place in, and a
// cursor that reappears at the top reads as a key that did nothing.
func (c *ContextScreen) move(pressed string) {
	if len(c.Groups) == 0 {
		return
	}
	switch pressed {
	case "up", "k":
		c.Cursor = max(c.cursor()-1, 0)
	default:
		c.Cursor = min(c.cursor()+1, len(c.Groups)-1)
	}
}

// cursor is Cursor clamped to the groups that exist, so a host that dropped a
// group between renders cannot index past the end.
func (c ContextScreen) cursor() int {
	if len(c.Groups) == 0 {
		return 0
	}
	return min(max(c.Cursor, 0), len(c.Groups)-1)
}

// View renders the surface: the header and its rule, the two panels, and the
// folds under them.
func (c *ContextScreen) View(width int) string {
	if width <= 0 {
		return ""
	}
	head := []string{c.headerRow(width), reviewRule(width), ""}
	rows := append(head, c.bodyRows(width, c.budget(len(head)))...)
	return strings.Join(rows, "\n")
}

// budget is how many rows the body may spend: the screen's height less the
// header and its rule. An unbounded screen drops nothing.
func (c *ContextScreen) budget(pinned int) int {
	if c.MaxLines <= 0 {
		return 0
	}
	return max(c.MaxLines-pinned, 1)
}

// headerRow names the surface, what it is a reading of, and the way out. The
// occupancy goes on the right beside the keys because it is the answer the
// reader came for, and this is where the eye already goes for the state of a
// surface.
func (c *ContextScreen) headerRow(width int) string {
	right := sty.Dim.Render(c.headerKeys())
	if c.Tokens != "" {
		right = c.pctStyle().Render(c.Tokens) + sty.Dim.Render(" · "+c.headerKeys())
	}
	name := brightStyle().Render("/context")
	left := name
	if c.Window != "" {
		left += sty.Dim.Render(" · this session · " + c.Window + " window")
	}
	// The subject drops whole rather than clipping mid-word: `/context · thi…`
	// says less than `/context` does and costs the same three columns.
	if room := width - lipgloss.Width(right) - 2; lipgloss.Width(left) > room {
		left = clip(name, max(room, 0))
	}
	if pad := width - lipgloss.Width(left) - lipgloss.Width(right); pad >= 2 {
		return left + strings.Repeat(" ", pad) + right
	}
	return clip(right, width)
}

// headerKeys is the pair the header ends with: the key that shows the whole
// register, and the way back. A takeover that did not state its way out would
// be a surface holding the keyboard with nothing saying how to give it back
// (invariant 5).
func (c *ContextScreen) headerKeys() string {
	list := keys.Bracket(keys.Context.List) + " " + keys.Words(keys.Context.List)
	if c.ShowKeys {
		list = keys.Bracket(keys.Context.List) + " hide the keys"
	}
	return list + " · " + keys.Bracket(keys.Context.Back) + " " + keys.Words(keys.Context.Back)
}

// bodyRows is the two panels and the folds under them, trimmed to the budget.
// The folds give ground first: a screen too short for them can still say
// where the window went, and the reverse is not true.
func (c *ContextScreen) bodyRows(width, budget int) []string {
	rows := c.panelRows(width)
	if folds := c.foldRows(width); len(folds) > 0 {
		rows = append(rows, "")
		rows = append(rows, folds...)
	}
	rows = append(rows, "")
	rows = append(rows, c.keyRows(width)...)
	if budget <= 0 || len(rows) <= budget {
		return rows
	}
	return rows[:budget]
}

// panelRows is the grid and the legend side by side, or one above the other
// when the terminal cannot hold both. The legend is what decides: a legend
// clipped into its numeric columns has lost the thing it exists to state.
func (c *ContextScreen) panelRows(width int) []string {
	grid := c.gridRows()
	legend := c.legendRows()
	inner := width - contextIndent
	if inner < c.sideBySideWidth() {
		return c.stackedRows(grid, legend, width)
	}
	pad := strings.Repeat(" ", contextIndent)
	rows := make([]string, 0, len(grid))
	for i, cells := range grid {
		row := pad + cells
		if i < len(legend) && lipgloss.Width(legend[i]) > 0 {
			row += strings.Repeat(" ", contextGridGap) + legend[i]
		}
		rows = append(rows, clip(row, width))
	}
	// A legend longer than the grid is deep keeps its remaining rows under
	// the grid rather than losing them; the grid's own height is fixed.
	for i := len(grid); i < len(legend); i++ {
		rows = append(rows, clip(pad+strings.Repeat(" ", MeterCellsRail+contextGridGap)+legend[i], width))
	}
	return rows
}

// sideBySideWidth is the narrowest inner width the two panels fit in beside
// each other.
func (c *ContextScreen) sideBySideWidth() int {
	return MeterCellsRail + contextGridGap + contextSwatchWidth + contextLabelWidth + contextTokensWidth + contextPctWidth
}

// stackedRows is the narrow layout: the grid, then what the header could not
// hold, then the legend under it. Stacking rather than truncating is the rule
// every component in this package follows.
func (c *ContextScreen) stackedRows(grid, legend []string, width int) []string {
	pad := strings.Repeat(" ", contextIndent)
	rows := make([]string, 0, len(grid)+len(legend)+2)
	for _, cells := range grid {
		rows = append(rows, clip(pad+cells, width))
	}
	rows = append(rows, "")
	for _, line := range legend {
		if lipgloss.Width(line) == 0 {
			rows = append(rows, "")
			continue
		}
		rows = append(rows, clip(pad+line, width))
	}
	return rows
}

// gridRows is the wrapped meter: MeterCellsRail cells a row, contextGridRows
// deep, laid out in reading order and cut into rows afterwards. It is one
// meter rather than ten, so a category's run ends where its share says it
// does and not at the end of whichever row it fell in.
func (c *ContextScreen) gridRows() []string {
	cells := c.gridCells()
	rows := make([]string, 0, contextGridRows)
	for r := range contextGridRows {
		rows = append(rows, paintRun(cells[r*MeterCellsRail:(r+1)*MeterCellsRail]))
	}
	return rows
}

// gridCells assigns every cell of the grid to a category by walking the
// categories in order and giving each the cells its share covers. Walking
// cumulatively rather than rounding each share on its own is what keeps the
// runs contiguous and stops the rounding error accumulating: every boundary
// is measured from the start of the grid, not from the run before it.
//
// A category with a real cost but less than a cell's worth of it still gets
// one cell, which is the rule meterFill already applies to every other meter
// in the product and for the same reason — a category named in the legend
// with nothing of it anywhere in the grid reads as one that is not in the
// window. It borrows the cell from the free space, so a window with five
// rounding-error categories in it draws at most five cells fuller than it is,
// against a total stated exactly twice on the same screen.
func (c *ContextScreen) gridCells() []ContextTone {
	total := MeterCellsRail * contextGridRows
	cells := make([]ContextTone, total)
	for i := range cells {
		cells[i] = ContextFree
	}
	var at int
	var running float64
	for _, cat := range c.Categories {
		if cat.Tone == ContextFree || cat.Share <= 0 {
			continue
		}
		running += cat.Share
		end := min(int(running*float64(total)/100), total)
		if end <= at {
			end = min(at+1, total)
		}
		for ; at < end; at++ {
			cells[at] = cat.Tone
		}
	}
	return cells
}

// paintRun draws one row of cells, one styled run per stretch of a single
// tone, so a row of twenty-two cells costs at most six escape sequences
// rather than twenty-two.
func paintRun(cells []ContextTone) string {
	var b strings.Builder
	for i := 0; i < len(cells); {
		j := i
		for j < len(cells) && cells[j] == cells[i] {
			j++
		}
		glyph := "▰"
		if cells[i] == ContextFree {
			glyph = "▱"
		}
		b.WriteString(cells[i].Style().Render(strings.Repeat(glyph, j-i)))
		i = j
	}
	return b.String()
}

// pctStyle is the pressure ladder the grid and the occupancy in the header
// both read, so the shape and the number cannot disagree about how full the
// window is.
func (c *ContextScreen) pctStyle() lipgloss.Style {
	return ctxStyle(c.Pct, c.Warn, c.Alert)
}

// legendRows is the panel beside the grid: the model, the occupancy, then a
// row per category. The blank rows in it are deliberate — they are what puts
// the category heading level with the fifth row of the grid rather than
// crowding the model lines above it.
func (c *ContextScreen) legendRows() []string {
	var rows []string
	if c.Model != "" {
		rows = append(rows, brightStyle().Render(c.Model))
	}
	if c.Provider != "" {
		rows = append(rows, sty.Dim.Render(c.Provider))
	}
	rows = append(rows, c.occupancyRow(), "", sty.Info.Bold(true).Render("OCCUPANCY BY CATEGORY"))
	for _, cat := range c.Categories {
		rows = append(rows, c.categoryRow(cat))
	}
	return rows
}

// occupancyRow is the sentence the grid is a picture of. It names the source
// because a total the provider reported and one we estimated are different
// facts, and the surface that itemises the estimate is the one place that
// distinction is actionable.
func (c *ContextScreen) occupancyRow() string {
	row := sty.Body.Render(c.Tokens) + sty.Dim.Render(" of "+c.Window+" · ")
	row += c.pctStyle().Render(fmt.Sprintf("%d%%", min(max(c.Pct, 0), 100))) + sty.Dim.Render(" full")
	if c.Source != "" {
		row += sty.Dim.Render(" · " + c.Source)
	}
	return row
}

// categoryRow is one legend row: the swatch that keys the grid, the label,
// and the two right-aligned numbers. The swatch and the label carry the same
// tone, which is the pairing invariant 1 asks for — the colour in the grid is
// never the only thing saying which category a run belongs to, because the
// word is right here in the same colour.
func (c *ContextScreen) categoryRow(cat ContextCategory) string {
	glyph := "▰"
	if cat.Tone == ContextFree {
		glyph = "▱"
	}
	style := cat.Tone.Style()
	return style.Render(glyph+" ") +
		style.Render(padRight(cat.Label, contextLabelWidth)) +
		style.Render(padLeft(cat.Tokens, contextTokensWidth)) +
		sty.Dim.Render(padLeft(cat.Pct, contextPctWidth))
}

// foldRows is the run of groups under the two panels: the folded row for each,
// and the items under the ones that are open.
func (c *ContextScreen) foldRows(width int) []string {
	var rows []string
	for i, g := range c.Groups {
		rows = append(rows, c.groupRow(g, i == c.cursor(), width))
		if !g.Open {
			continue
		}
		for _, item := range g.Items {
			rows = append(rows, c.itemRow(item, width))
		}
		if g.More != "" {
			rows = append(rows, clip(strings.Repeat(" ", contextItemIndent)+sty.Dim.Render(g.More), width))
		}
	}
	return rows
}

// groupRow is a fold's own row: the pointer, the fold glyph, the label, and
// the count of what it swallowed. The row the cursor is on is lit and carries
// the key that opens it; the others carry no key, because a key written on a
// row the cursor is not on is an offer nothing answers.
func (c *ContextScreen) groupRow(g ContextGroup, lit bool, width int) string {
	glyph := "▸"
	if g.Open {
		glyph = "▾"
	}
	pad := strings.Repeat(" ", contextIndent)
	pointer := "  "
	if lit {
		pointer = sty.FocusPointer.Render("❯ ")
	}
	body := sty.Dim.Render(glyph+" ") + sty.Body.Render(g.Label) + sty.Dim.Render(" · "+g.Summary)
	if !lit {
		return clip(pad+pointer+body, width)
	}
	key := keys.Bracket(keys.Context.Expand) + " " + foldVerb(g.Open)
	room := width - contextIndent - 2
	line := padRight(body, max(room-lipgloss.Width(key)-1, 0)) + key
	return clip(pad+pointer+LitRow(line, 0, max(room, 0)), width)
}

// foldVerb is what the key on the lit row promises. The binding's own words
// cover both directions ("expand or fold"); a row that is already open says
// only the half of that which is still available.
func foldVerb(open bool) string {
	if open {
		return "fold"
	}
	return "expand"
}

// itemRow is one part of an opened group: the name, its share of the group as
// a meter, and its two numbers. The meter is the category tone rather than the
// pressure ladder, because a tool taking a fifth of the tool budget has
// crossed no threshold anybody set.
func (c *ContextScreen) itemRow(item ContextItem, width int) string {
	pad := strings.Repeat(" ", contextItemIndent)
	label := sty.Body.Render(padRight(clip(item.Label, contextLabelWidth), contextLabelWidth))
	numbers := sty.Body.Render(padLeft(item.Tokens, contextTokensWidth)) +
		sty.Dim.Render(padLeft(item.Pct, contextPctWidth))
	bar := Meter{Pct: item.Share, Cells: MeterCellsRail, Tone: MeterCategory}.Bar()
	// The bar is the first thing to go when the terminal cannot hold the
	// row: a number that has lost its bar is still a number.
	if contextItemIndent+contextLabelWidth+MeterCellsRail+contextTokensWidth+contextPctWidth > width {
		return clip(pad+label+numbers, width)
	}
	return clip(pad+label+bar+numbers, width)
}

// keyRows is the foot of the screen: the compact key row, or the whole
// register in its place once `?` has been pressed.
func (c *ContextScreen) keyRows(width int) []string {
	if !c.ShowKeys {
		return []string{clip(sty.Dim.Render(contextKeyRow(width)), width)}
	}
	rows := make([]string, 0, len(keys.Context.All()))
	for _, b := range keys.Context.All() {
		rows = append(rows, clip(sty.Dim.Render("  "+offer(b)), width))
	}
	return rows
}

// contextKeyRow is the surface's keys as one line, in the order the register
// declares them, giving ground from the left as the terminal narrows. The way
// out is the last thing on it and so the last thing to go: a takeover that
// clipped its own exit would be holding the keyboard with nothing saying how
// to give it back (invariant 5).
func contextKeyRow(width int) string {
	all := keys.Context.All()
	parts := make([]string, 0, len(all))
	for _, b := range all {
		parts = append(parts, keys.Bracket(b)+" "+keys.Words(b))
	}
	for len(parts) > 1 {
		if lipgloss.Width(strings.Join(parts, " · ")) <= width {
			break
		}
		parts = parts[1:]
	}
	return strings.Join(parts, " · ")
}
