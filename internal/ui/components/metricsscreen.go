package components

// The metrics surface (S-129, DESIGN-TUI.md §19c, ui_kits/cockpit/Tools.html).
// `shhh metrics` printed a tabwriter table of fifteen unaligned columns and no
// Bubble Tea at all. It is re-cut here from parts that already exist: the §6a
// column grid applied to a table, the §10c sparkline for the shape of a
// trend, and the §10c block meter for every ratio — so the sparkline a reader
// already knows from the inspector rail means the same thing here.
//
// Three rules shape it and all three come from §19c. Numeric columns are
// fixed-width and right-aligned, so the reader scans one column rather than
// parsing rows. A sparkline is dimmer and never coloured, because a coloured
// sparkline would imply a threshold nobody set — the numbers beside it are
// the measurement and the sparkline is only the shape. And every ratio is a
// block meter with its number stated beside the bar, in the accent-coloured
// category tone rather than the context ladder, because none of these shares
// has a threshold to cross.
//
// It is a passive component like the rest of this package. It owns no metrics
// semantics: the host reads the store, prices the tokens, decides what a
// category is and formats every field. The screen aligns the columns, draws
// the bars, and gives ground as the terminal narrows.

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	// metricsIndent is the column the table and the blocks start at, the same
	// one the config screen's settings list uses (§19a).
	metricsIndent = 2
	// metricsGap is the gutter between two columns of the table. Two spaces
	// is what keeps a right-aligned number from touching the one before it.
	metricsGap = 2
	// metricsTrendCells is the sparkline's width: one cell per day for a
	// week. It is fixed rather than following the window, so a reader
	// comparing two runs of `shhh metrics` is comparing the same span.
	metricsTrendCells = 7
	// metricsMinLabel is the shortest run of a bar's label worth drawing. The
	// meter itself never shrinks (§10c allows four cell counts and no
	// others), so the label is what gives ground.
	metricsMinLabel = 8
)

// MetricsModel is one row of the model table, already resolved to what the
// screen draws. The host formats every field — `2.9M`, `$12.80`, `0.6s` —
// because those are readings of the store and this is a renderer.
type MetricsModel struct {
	// Name identifies the model, and is the one column that never drops.
	Name string
	// Requests, TokensIn, TokensOut, Spend, TTFT and P95 are the numeric
	// columns, right-aligned in their own fixed widths. A field the store
	// never recorded is NoDuration rather than a zero — a blank column would
	// read as "none" where the truth is "never measured".
	Requests  string
	TokensIn  string
	TokensOut string
	Spend     string
	TTFT      string
	P95       string
	// Trend is the token total for each of the last metricsTrendCells days,
	// oldest first, and the host supplies a full run with zeroes for the days
	// nothing ran — an eight-cell shape and a three-cell one side by side
	// would compare two different spans. Each row scales to its own maximum:
	// it is the shape of this model's week, not of the table's.
	Trend []float64
}

// MetricsBar is one row of a meter block: a share of a total, its bar, the
// number beside it, and a right-hand field that annotates the pair.
type MetricsBar struct {
	// Label is the category or the model the share belongs to. Where the
	// tone is the only thing separating two bars, the label carries a glyph
	// as well — invariant 1 holds here as everywhere.
	Label string
	// Pct is the fill, 0–100.
	Pct int
	// Text is the number stated beside the bar — `$9.94 · 54%`, `94%
	// answered`. Never empty: a bar without its number is a shape (§10c).
	Text string
	// Note is the right-hand field: what the share is made of. It drops
	// before anything else on the row does (§16).
	Note string
	// NoteTone reads the note. A note that says something the reader would
	// rather not have paid for is del, and its bar is too.
	NoteTone FieldTone
	// Tone is the meter's own colour: MeterCategory for an ordinary share,
	// MeterUnasked for a cost nobody asked for, MeterAgent for a sub-agent's.
	Tone MeterTone
}

// MetricsBlock is one titled run of bars — `where the money went`, `how the
// answers came back`. A block with no bars is left out by the host rather
// than drawn empty (§19c), which is also how a reading the store has nothing
// for disappears instead of showing as a row of zeroes.
type MetricsBlock struct {
	// Title names the block, in the sentence case the artboard uses: these
	// are readings, not rails.
	Title string
	// Field is the right-hand annotation — `last 30 days`, `241 requests`.
	Field string
	Bars  []MetricsBar
}

// MetricsScreen is `shhh metrics`: a takeover surface, full width, no
// inspector rail, owning the keyboard for as long as it is up (§19).
type MetricsScreen struct {
	// Subject is what the header says the screen is over — `last 30 days ·
	// 241 requests · 4 models`. The host counts it.
	Subject string
	// Spend is the total the header states on the right, ahead of the keys.
	Spend string
	// Models are the table's rows, in the order the host read them.
	Models []MetricsModel
	// Blocks are the meter blocks under the table, in the order they are
	// drawn.
	Blocks []MetricsBlock
	// MaxLines bounds the screen height; the header and its rule come off the
	// body's budget before anything is drawn. 0 is unbounded, which is what a
	// test or a host that sizes itself gets.
	MaxLines int
}

// Update is the screen's whole keyboard, and it is one key. §19c's header
// offers `[q] quit` and nothing else: there is no pointer to move, nothing to
// choose and nothing to change, so there is no key list to open either — a
// `[?]` over a single key would be a row explaining the row above it.
func (m *MetricsScreen) Update(msg tea.KeyMsg) (done bool, result any) {
	switch msg.String() {
	case "q", "esc", "ctrl+c":
		return true, nil
	}
	return false, nil
}

// View renders the screen: the §17c header and its rule, the model table, and
// the meter blocks under it.
func (m *MetricsScreen) View(width int) string {
	if width <= 0 {
		return ""
	}
	head := []string{m.headerRow(width), reviewRule(width), ""}
	rows := append(head, m.bodyRows(width, m.budget(len(head)))...)
	return strings.Join(rows, "\n")
}

// budget is how many rows the body may spend: the screen's height less the
// header and its rule. An unbounded screen drops nothing.
func (m *MetricsScreen) budget(pinned int) int {
	if m.MaxLines <= 0 {
		return 0
	}
	return max(m.MaxLines-pinned, 1)
}

// headerRow is the §17c header: the command, what it is over, the total spend
// and the one key the screen has. The spend sits with the key rather than in
// the subject because it is the answer the reader came for, and §19c puts it
// where the eye already goes for the state of a surface.
//
// It is the subject that gives ground here, not the right-hand pair — the
// other two screens of §19 clip the left and let their keys go, because both
// have a foot key row to fall back on. This one has neither a foot row nor a
// second key, so dropping `[q]` would leave a takeover surface with no stated
// way out of it (invariant 5).
func (m *MetricsScreen) headerRow(width int) string {
	right := dimStyle.Render("[q] quit")
	if m.Spend != "" {
		right = bodyStyle.Render(m.Spend) + dimStyle.Render(" · [q] quit")
	}
	left := brightStyle().Render("shhh metrics")
	room := width - lipgloss.Width(right) - 2
	if m.Subject != "" && room > lipgloss.Width(left) {
		left = clip(left+dimStyle.Render(" · "+m.Subject), room)
	}
	if pad := width - lipgloss.Width(left) - lipgloss.Width(right); pad >= 2 {
		return left + strings.Repeat(" ", pad) + right
	}
	return clip(right, width)
}

// bodyRows is the table and the blocks under it, trimmed to the budget. The
// table gives ground last: a screen that cannot show how the money split can
// still say which models were used, and the reverse is not true.
func (m *MetricsScreen) bodyRows(width, budget int) []string {
	sections := make([][]string, 0, len(m.Blocks)+1)
	sections = append(sections, m.tableRows(width, 0))
	for _, block := range m.Blocks {
		sections = append(sections, append([]string{""}, m.blockRows(block, width)...))
	}
	if budget <= 0 {
		return flatten(sections)
	}

	// Whole blocks go first, from the bottom, and the row that replaces them
	// names what went rather than only marking that something did
	// (invariant 4).
	kept, dropped := sections, 0
	for len(kept) > 1 && rowCount(kept)+markerRows(dropped) > budget {
		kept, dropped = kept[:len(kept)-1], dropped+1
	}
	if dropped == 0 {
		// Everything fits, or the table alone still does not and windows
		// itself against the whole budget.
		if len(kept) > 1 {
			return flatten(kept)
		}
		return m.tableRows(width, budget)
	}
	// The marker is what keeps the dropped blocks from being dropped
	// silently, so it takes its row before the table windows against what is
	// left (invariant 4).
	marker := indentBy(m.droppedRow(dropped, width-metricsIndent), metricsIndent, width)
	if len(kept) > 1 {
		return append(flatten(kept), marker)
	}
	return append(m.tableRows(width, budget-1), marker)
}

// markerRows is the row the dropped-blocks marker will cost once anything has
// been dropped at all.
func markerRows(dropped int) int {
	if dropped == 0 {
		return 0
	}
	return 1
}

// droppedRow names the blocks that did not fit. A marker that only said
// "2 more" would leave the reader guessing which two readings the screen is
// sitting on.
func (m *MetricsScreen) droppedRow(dropped, width int) string {
	titles := make([]string, 0, dropped)
	for _, block := range m.Blocks[len(m.Blocks)-dropped:] {
		titles = append(titles, block.Title)
	}
	return dimStyle.Render(clip(
		fmt.Sprintf("↓ %d more · %s", dropped, strings.Join(titles, " · ")), width))
}

// tableRows is the model table: the §6a grid applied to a table, with a
// heading rail over it. budget bounds it; a table that cannot show every
// model says how many it is holding back.
func (m *MetricsScreen) tableRows(width, budget int) []string {
	cols := m.columns(width)
	if len(cols) == 0 {
		return nil
	}
	rows := []string{indentBy(headlineStyle.Render(m.headingRow(cols)), metricsIndent, width)}
	if len(m.Models) == 0 {
		// A heading over nothing is a table that lost its rows. The host
		// keeps the screen closed when the store is empty, so this is the
		// window having taken everything, and it says so.
		return append(rows, indentBy(dimStyle.Render("no models to show"), metricsIndent, width))
	}

	shown := m.Models
	// The heading and the marker both come off the budget before the models
	// do: the window may never buy itself a row (§4a).
	if budget > 0 && len(shown) > budget-1 {
		shown = shown[:max(budget-2, 1)]
	}
	for _, model := range shown {
		rows = append(rows, indentBy(m.modelRow(model, cols), metricsIndent, width))
	}
	if hidden := len(m.Models) - len(shown); hidden > 0 {
		rows = append(rows, indentBy(dimStyle.Render(
			fmt.Sprintf("↓ %d more %s", hidden, nounFor(hidden, "model"))), metricsIndent, width))
	}
	return rows
}

// metricsColumn is one column of the table: its heading, whether its cells are
// numbers, and how to read one out of a row.
type metricsColumn struct {
	head    string
	numeric bool
	value   func(MetricsModel) string
	// width is the column's own fixed width, computed once from the heading
	// and every cell under it.
	width int
	// trend marks the sparkline column, which is drawn rather than printed:
	// it is dimmer where the numbers are body, and it is a shape rather than
	// a value (§10c).
	trend bool
}

// metricsColumns is every column the table can carry, in the order it draws
// them.
func (m *MetricsScreen) allColumns() []metricsColumn {
	return []metricsColumn{
		{head: "MODEL", value: func(r MetricsModel) string { return r.Name }},
		{head: "REQUESTS", numeric: true, value: func(r MetricsModel) string { return r.Requests }},
		{head: "↑ TOK", numeric: true, value: func(r MetricsModel) string { return r.TokensIn }},
		{head: "↓ TOK", numeric: true, value: func(r MetricsModel) string { return r.TokensOut }},
		{head: "SPEND", numeric: true, value: func(r MetricsModel) string { return r.Spend }},
		{head: "TTFT", numeric: true, value: func(r MetricsModel) string { return r.TTFT }},
		{head: "P95", numeric: true, value: func(r MetricsModel) string { return r.P95 }},
		{head: "TOK · 7d", trend: true},
	}
}

// metricsDropOrder is which columns give ground as the terminal narrows, in
// the order they go (§8b's rule, over a table). The sparkline goes first
// because §19c says what it is: the shape, not the measurement — the
// measurement is the column beside it. The latencies follow, then the token
// totals from the smaller of the pair, and REQUESTS last, because a row with
// no count on it has stopped being a summary.
//
// MODEL and SPEND are not in the list: the name is what the row is about and
// the spend is what the header has been totalling, so neither ever drops.
var metricsDropOrder = []string{"TOK · 7d", "P95", "TTFT", "↓ TOK", "↑ TOK", "REQUESTS"}

// columns is the set of columns that fits, with each one's width already
// measured against its own cells.
func (m *MetricsScreen) columns(width int) []metricsColumn {
	cols := m.measure(m.allColumns())
	for _, drop := range metricsDropOrder {
		if tableWidth(cols) <= width-metricsIndent {
			break
		}
		cols = withoutColumn(cols, drop)
	}
	return cols
}

// measure sets each column's width from its heading and every cell under it,
// which is what makes the numbers line up down the column rather than after
// the longest row (§19c).
func (m *MetricsScreen) measure(cols []metricsColumn) []metricsColumn {
	for i := range cols {
		cols[i].width = lipgloss.Width(cols[i].head)
		if cols[i].trend {
			cols[i].width = max(cols[i].width, metricsTrendCells)
			continue
		}
		for _, model := range m.Models {
			cols[i].width = max(cols[i].width, lipgloss.Width(cols[i].value(model)))
		}
	}
	return cols
}

// tableWidth is what a set of columns costs, gutters included.
func tableWidth(cols []metricsColumn) int {
	total := 0
	for i, col := range cols {
		if i > 0 {
			total += metricsGap
		}
		total += col.width
	}
	return total
}

// withoutColumn is the column set with one column shed. Shedding is whole-column:
// a half-drawn heading over half a number is worse than the column not being
// there (invariant 4).
func withoutColumn(cols []metricsColumn, head string) []metricsColumn {
	out := make([]metricsColumn, 0, len(cols))
	for _, col := range cols {
		if col.head != head {
			out = append(out, col)
		}
	}
	return out
}

// headingRow is the table's heading, aligned exactly as its cells are. It is
// a rail rather than a row of labels — `c-info b`, the same as every group
// rail in the product (§4a).
func (m *MetricsScreen) headingRow(cols []metricsColumn) string {
	cells := make([]string, 0, len(cols))
	for _, col := range cols {
		cells = append(cells, align(col.head, col.width, col.numeric))
	}
	return strings.TrimRight(strings.Join(cells, strings.Repeat(" ", metricsGap)), " ")
}

// modelRow is one model's row: the numbers in body, and the sparkline in
// dimmer at the end of it. The two are painted separately because they are
// two different kinds of thing — one is measured, one is only shaped.
func (m *MetricsScreen) modelRow(model MetricsModel, cols []metricsColumn) string {
	var (
		cells []string
		trend string
	)
	for _, col := range cols {
		if col.trend {
			trend = Sparkline{Values: model.Trend, Cells: metricsTrendCells}.View()
			continue
		}
		cells = append(cells, align(col.value(model), col.width, col.numeric))
	}
	row := bodyStyle.Render(strings.Join(cells, strings.Repeat(" ", metricsGap)))
	if trend == "" {
		return strings.TrimRight(row, " ")
	}
	return row + strings.Repeat(" ", metricsGap) + trend
}

// align places a cell in its column: numbers to the right so the digits line
// up, everything else to the left.
func align(cell string, width int, numeric bool) string {
	if numeric {
		return padLeft(cell, width)
	}
	return padRight(cell, width)
}

// blockRows is one meter block: its title with a right-hand field, and a bar
// per share under it.
func (m *MetricsScreen) blockRows(block MetricsBlock, width int) []string {
	inner := width - metricsIndent
	rows := []string{indentBy(titleRow(block, inner), metricsIndent, width)}
	label, notes := m.barGeometry(block, inner)
	for _, bar := range block.Bars {
		rows = append(rows, indentBy(barRow(bar, label, notes, inner), metricsIndent, width))
	}
	return rows
}

// labelWidth is the column every bar on the screen starts its meter after. It
// is one width for every block rather than one per block: the blocks under
// the table are readings of the same models, and three columns a character
// apart would read as three different tables.
func (m *MetricsScreen) labelWidth() int {
	label := 0
	for _, block := range m.Blocks {
		for _, bar := range block.Bars {
			label = max(label, lipgloss.Width(bar.Label))
		}
	}
	return label
}

// titleRow is the block's heading: what the block is reading, and what it is
// reading it over.
func titleRow(block MetricsBlock, width int) string {
	left := dimStyle.Render(block.Title)
	right := dimStyle.Render(block.Field)
	if pad := width - lipgloss.Width(left) - lipgloss.Width(right); pad >= 2 && block.Field != "" {
		return left + strings.Repeat(" ", pad) + right
	}
	return clip(left, width)
}

// barGeometry is the column every bar in a block starts its meter after, and
// whether the block's notes fit beside them. Both are one answer for the
// whole block rather than per row: bars that started in different columns
// would not be a column, and a note that appeared on the one short row and
// not on the three beside it would read as a fact about that row.
//
// The label is what gives ground when the terminal narrows, and the notes go
// before it does (§16). The meter itself never gives ground, because §10c
// allows four cell counts and no others.
func (m *MetricsScreen) barGeometry(block MetricsBlock, width int) (label int, notes bool) {
	var text, note int
	for _, bar := range block.Bars {
		text = max(text, lipgloss.Width(bar.Text))
		note = max(note, lipgloss.Width(bar.Note))
	}
	label = max(min(m.labelWidth(), width-MeterCellsRail-text-4), metricsMinLabel)
	notes = note > 0 && label+2+MeterCellsRail+1+text+2+note <= width
	return label, notes
}

// barRow is one share: its label, the §10c meter with its number beside it,
// and the note that annotates the pair. The note drops first (§16) and the
// meter never does.
func barRow(bar MetricsBar, label int, notes bool, width int) string {
	meter := Meter{Pct: bar.Pct, Cells: MeterCellsRail, Tone: bar.Tone, Text: bar.Text}
	left := dimStyle.Render(padRight(clip(bar.Label, label), label)) + "  " + meter.View()
	if bar.Note == "" || !notes {
		return clip(left, width)
	}
	note := bar.NoteTone.style().Render(bar.Note)
	if pad := width - lipgloss.Width(left) - lipgloss.Width(note); pad >= 2 {
		return left + strings.Repeat(" ", pad) + note
	}
	return clip(left, width)
}

// flatten runs the sections together into the rows they render to.
func flatten(sections [][]string) []string {
	var rows []string
	for _, section := range sections {
		rows = append(rows, section...)
	}
	return rows
}

// rowCount is how many rows a set of sections renders to.
func rowCount(sections [][]string) int {
	n := 0
	for _, section := range sections {
		n += len(section)
	}
	return n
}

// nounFor pluralises a noun whose plural is the singular plus an s.
func nounFor(n int, noun string) string {
	if n == 1 {
		return noun
	}
	return noun + "s"
}
