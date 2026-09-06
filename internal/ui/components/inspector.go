package components

// Inspector rail (docs/interface/surfaces.md#the-inspector-rail). Past
// 130 content columns the transcript stops being the whole screen: a rail on
// the right answers the three standing questions — what is it doing, what has
// it changed, what is it costing — so the session stops being interrogated
// with /stats and /diff for what it already knows. It starts at 46 columns
// and widens with the terminal to a ceiling; every block is rendered against
// the width it is handed rather than against that floor.
//
// The rail is passive, like Cockpit: the host feeds it the session's numbers
// and renders View every frame. It owns no keys, no state and no goroutines,
// and the block order is fixed — THIS TURN, PLAN, CHANGES, AGENTS, CONTEXT,
// SPEND. A block with nothing to say is omitted rather than rendered empty
//, and a rail that does not fit its height truncates its longest block
// first and says how many rows it swallowed.
//
// THIS TURN is the turn. CHANGES, AGENTS, CONTEXT and SPEND are the session
//: a file edited in turn 2 is still on screen in turn 8,
// because "what has this session done to my machine" does not reset when the
// agent starts a new turn. The two blocks that can count files both say their
// scope in words — `3 files this turn` and `session · +96 −11` — which is the
// rule that stops the two numbers reading as a contradiction.

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
)

const (
	// InspectorWidth is the rail at its narrowest — what it takes on the
	// first rung of the width ladder, and the floor every wider rail is
	// measured up from.
	InspectorWidth = 46
	// InspectorMaxWidth is the rail's ceiling. The blocks run out of things
	// to say with the columns long before the terminal runs out of columns:
	// past this a file path is already whole, a meter is already a bar rather
	// than a shape, and the rest is gap.
	InspectorMaxWidth = 72
	// InspectorMinContentWidth is the top rung of the width ladder: at
	// or above it the surface splits into transcript pane + rail, below it the
	// rail is dropped entirely.
	InspectorMinContentWidth = 130
	// inspectorGrowthColumns is how many content columns buy the rail one:
	// about one in four, so the transcript keeps the larger share of
	// everything the terminal gains
	// (docs/interface/surfaces.md#the-inspector-rail).
	inspectorGrowthColumns = 4

	// inspectorIndent is the two columns every block heading and row starts
	// at; a changed-file row spends the third on the mutation rail.
	inspectorIndent = 2
)

// RailWidthAuto is the value that hands the rail's width back to the ladder:
// what an unset setting reads as, and what the word means.
const RailWidthAuto = "auto"

// ParseRailWidth reads the rail's width setting: `auto` — or nothing — for
// the ladder, otherwise a column count. The count is not judged against the
// rail's floor and ceiling here, because it is not refused by them: a number
// outside the range is held to it when the layout is resolved, and a person
// who asked for 40 on a terminal that allows 62 is better served by the
// narrowest rail there is than by an error
// (docs/interface/surfaces.md#the-inspector-rail).
func ParseRailWidth(s string) (int, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" || s == RailWidthAuto {
		return 0, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("unknown rail width %q (valid: auto, or a column count)", s)
	}
	return n, nil
}

// RailWidthOrAuto is the column count a stored setting names, or the ladder's
// own where the value is not one this rail can read. Nothing unreadable
// reaches the layout: every surface that writes the key refuses anything else
// first, so a value that arrives anyway was hand-edited into the file, and a
// session that widens its own rail is a better answer than one that will not
// open.
func RailWidthOrAuto(s string) int {
	n, err := ParseRailWidth(s)
	if err != nil {
		return 0
	}
	return n
}

// InspectorWidthFor is the rail's column count at a content width — the
// ladder itself, and the only place it is written down. Below the rung it is
// the floor rather than nothing: whether there is a rail at all is the
// caller's question, and answering it here as a zero width would make every
// caller check for one.
func InspectorWidthFor(content int) int {
	grown := InspectorWidth + max(content-InspectorMinContentWidth, 0)/inspectorGrowthColumns
	return min(grown, InspectorMaxWidth)
}

// InspectorRail is the whole rail. A nil block pointer (or an empty agent
// list) is a block with nothing to say, and is omitted.
type InspectorRail struct {
	Summary *InspectorSummary
	Turn    *InspectorTurn
	Plan    *InspectorPlan
	Todo    *InspectorTodo
	Changes *InspectorChanges
	Agents  []InspectorAgent
	Tools   *InspectorTools
	Context *InspectorContext
	Spend   *InspectorSpend
	// Frame is the host's spinner frame index, for the lanes of children that
	// declared no step count. The rail stays passive: it animates nothing, it
	// just draws the frame it is handed.
	Frame int
}

// Empty reports whether every block is omitted, so the host can skip the
// split rather than draw an empty column.
func (r InspectorRail) Empty() bool {
	return r.Summary == nil && r.Turn == nil && r.Plan == nil && r.Todo == nil && r.Changes == nil &&
		len(r.Agents) == 0 && r.Tools == nil && r.Context == nil && r.Spend == nil
}

// RailTargetKind says what a row on the rail points at. Most of the rail
// points at nothing, and that is the default on purpose: a heading, a meter,
// a sentence and a fold marker are readings rather than doors, and a row that
// answered a click by opening a whole surface would be a target the same
// click could not leave.
type RailTargetKind int

const (
	// RailTargetNone: the row is something to read.
	RailTargetNone RailTargetKind = iota
	// RailTargetFile: the row names a path the session has changed.
	RailTargetFile
	// RailTargetSession: the row names a session in the map.
	RailTargetSession
)

// RailTarget is what a row points at: the kind of thing it names and the name
// itself — a workspace path for a file, and a session's name for a session,
// which is empty for the session the rail belongs to.
type RailTarget struct {
	Kind RailTargetKind
	Name string
}

// RailRow is one rendered row of the rail beside what it points at. The rail
// assembles its rows from the session's own values, so a row already knows
// what it is; handing that out with the text is what lets a host resolve a
// cell to a target instead of parsing back the styled string it drew
// (docs/interface/surfaces.md#the-inspector-rail).
type RailRow struct {
	Text   string
	Target RailTarget
}

// railLine is one assembled row, what truncation is allowed to do with it,
// and what it points at. A pinned row is the last one taken — an alert, or a
// file the running turn wrote — and a counted row is one the fold marker
// counts, carrying its numbers where it has any, so a marker states what it
// swallowed rather than only how many rows went behind it (invariant 4). Two
// rows of one thing are one counted row: the map's sessions take two each,
// and a marker counting rows would report three folded children as six.
type railLine struct {
	text           string
	pinned         bool
	counted        bool
	added, removed int
	target         RailTarget
}

// railBlock is one headed block under assembly: its heading line, its rows,
// and the rows truncation has taken.
type railBlock struct {
	heading string
	rows    []railLine
	// hidden holds what truncation took, in the order it stood in.
	hidden []railLine
	// fold renders the marker for the hidden rows. Nil prints the bare
	// "… N more" every block but CHANGES uses.
	fold func([]railLine) string
}

// add appends an ordinary row: truncation may take it, and it carries no
// counts of its own.
func (b *railBlock) add(text string) { b.rows = append(b.rows, railLine{text: text}) }

// pin appends a row truncation takes only when nothing else is left.
func (b *railBlock) pin(text string) { b.rows = append(b.rows, railLine{text: text, pinned: true}) }

func (b railBlock) height() int {
	h := 1 + len(b.rows)
	if len(b.hidden) > 0 {
		h++
	}
	return h
}

// render draws the block at the rail's width, each row beside what it points
// at. The width is a parameter and not the rail's floor: a fold marker drawn
// at 46 columns inside a 62-column rail is a row that ends where nothing else
// on the rail ends.
//
// The heading and the fold marker point at nothing. The heading names a block
// rather than a thing in it, and the marker stands for rows that are not on
// screen — a click on either would have to guess which of several things the
// reader meant.
func (b railBlock) render(width int) []RailRow {
	out := make([]RailRow, 0, b.height())
	out = append(out, RailRow{Text: b.heading})
	for _, r := range b.rows {
		out = append(out, RailRow{Text: r.text, Target: r.target})
	}
	switch {
	case len(b.hidden) == 0:
	case b.fold != nil:
		out = append(out, RailRow{Text: b.fold(b.hidden)})
	default:
		out = append(out, RailRow{Text: indentRow(sty.Hint.Render(fmt.Sprintf("… %d more", len(b.hidden))), width)})
	}
	return out
}

// View renders the rail at width columns and, when height > 0, within height
// rows. Blocks are separated by one blank row; the rail never scrolls.
func (r InspectorRail) View(width, height int) string {
	return strings.Join(r.Lines(width, height), "\n")
}

// Lines is View split into rows, for hosts joining the rail beside a
// transcript pane line by line. It is Rows without the targets, for the draw,
// which has no use for them.
func (r InspectorRail) Lines(width, height int) []string {
	rows := r.Rows(width, height)
	if len(rows) == 0 {
		return nil
	}
	out := make([]string, len(rows))
	for i, row := range rows {
		out[i] = row.Text
	}
	return out
}

// Rows is the rail as it will be drawn — every row in order, including the
// blank ones between blocks — each beside what it points at. A host that
// answers a pointer asks for these and indexes the row the pointer is on: the
// rail is laid out from the top of its rectangle, so the row is the offset
// and nothing has to be measured or re-read.
func (r InspectorRail) Rows(width, height int) []RailRow {
	blocks := r.blocks(width)
	if len(blocks) == 0 {
		return nil
	}
	if height > 0 {
		blocks = fitBlocks(blocks, height)
	}
	var out []RailRow
	for i, b := range blocks {
		if i > 0 {
			out = append(out, RailRow{})
		}
		out = append(out, b.render(width)...)
	}
	if height > 0 && len(out) > height {
		out = out[:height]
	}
	return out
}

// blocks assembles the present blocks in their fixed order.
func (r InspectorRail) blocks(width int) []railBlock {
	var blocks []railBlock
	for _, b := range []func(int) (railBlock, bool){
		r.summaryBlock, r.turnBlock, r.planBlock, r.todoBlock, r.changesBlock,
		r.agentsBlock, r.toolsBlock, r.contextBlock, r.spendBlock,
	} {
		if blk, ok := b(width); ok {
			blocks = append(blocks, blk)
		}
	}
	return blocks
}

// fitBlocks truncates the rail into height rows, taking rows off the longest
// block first. A truncated block keeps its heading and says how many
// rows it is hiding, so the rail never ends silently; CHANGES folds rather
// than truncates, and its marker carries the counts it took with it.
func fitBlocks(blocks []railBlock, height int) []railBlock {
	for total(blocks) > height {
		longest, rows := -1, 0
		for i, b := range blocks {
			if len(b.rows) > rows {
				longest, rows = i, len(b.rows)
			}
		}
		if longest < 0 {
			// Every block is down to its heading: nothing left to give.
			break
		}
		b := &blocks[longest]
		// The last row truncation is allowed to take: a pinned row — an
		// alert, or a file the running turn wrote — goes only when there is
		// nothing else left to give.
		i := len(b.rows) - 1
		for j := i; j >= 0; j-- {
			if !b.rows[j].pinned {
				i = j
				break
			}
		}
		b.hidden = append([]railLine{b.rows[i]}, b.hidden...)
		b.rows = append(b.rows[:i], b.rows[i+1:]...)
	}
	return blocks
}

func total(blocks []railBlock) int {
	n := len(blocks) - 1 // one blank row between blocks
	for _, b := range blocks {
		n += b.height()
	}
	return n
}

// railHeading is a block heading: the label in info, its count or value
// right-aligned at the rail's edge.
func railHeading(label, meta string, metaStyle lipgloss.Style, width int) string {
	if meta != "" && !strings.Contains(meta, "\x1b") {
		meta = metaStyle.Render(meta)
	}
	return railRow(sty.Headline.Render(label), meta, width, inspectorIndent)
}

// railRow lays one row out: indent, left field, right field against the
// rail's right edge. The left field clips when the two would collide — the
// right field is the number, and a clipped number is a wrong number.
func railRow(left, right string, width, indent int) string {
	room := width - indent - lipgloss.Width(right)
	if right != "" {
		room-- // at least one space between the fields
	}
	left = Clip(left, max(room, 0))
	gap := width - indent - lipgloss.Width(left) - lipgloss.Width(right)
	return strings.Repeat(" ", indent) + left + strings.Repeat(" ", max(gap, 0)) + right
}

// indentRow is railRow with nothing on the right.
func indentRow(s string, width int) string {
	return railRow(s, "", width, inspectorIndent)
}

// DiffStat is the shared line count: what an edit added and what it removed,
// in the two registers every surface prints them in. One implementation, so a
// transcript row, a rail block and a status row cannot state the same edit
// three ways — and the signs carry the distinction, so a monochrome terminal
// reads it too.
func DiffStat(added, removed int) string {
	return sty.Add.Render(fmt.Sprintf("+%d", added)) + " " + sty.Del.Render(fmt.Sprintf("−%d", removed))
}

// FormatElapsed is the shared wall-clock format: seconds under a minute,
// "1m 04s" above it. One implementation, so the rail and /stats cannot
// report the same duration two ways.
func FormatElapsed(d time.Duration) string {
	if d < time.Minute {
		if d < 10*time.Second {
			return fmt.Sprintf("%.1fs", d.Seconds())
		}
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm %02ds", int(d.Minutes()), int(d.Seconds())%60)
}

// formatTokens is the rail's token count: 124k, 200k, 1.2M.
func formatTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 10_000:
		return fmt.Sprintf("%dk", n/1000)
	case n >= 1000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}
