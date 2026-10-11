package components

// Inspector rail (docs/interface/surfaces.md#the-inspector-rail). Past a
// 130-column terminal the transcript stops being the whole screen: a rail on
// the right answers the three standing questions — what is it doing, what has
// it changed, what is it costing — so the session stops being interrogated
// with /stats and /diff for what it already knows. It starts at 46 columns
// and widens with the terminal to a ceiling; every block is rendered against
// the width it is handed rather than against that floor.
//
// The rail is passive, like Cockpit: the host feeds it the session's numbers
// and renders View every frame. It owns no keys, no state and no goroutines,
// and the block order is fixed — SUMMARY, THIS TURN, ALERTS, PLAN, STEPS,
// TODO, CHANGES, AGENTS, TOOLS, CONTEXT, SPEND. A block with nothing to say is
// omitted rather than rendered empty.
//
// A rail that does not fit its height gives up the least alive thing first.
// Each block states its class from its own state every frame — asks and
// motion, live readings, standing, static — and a block collapses to its
// heading before a livelier block loses a row; only a standing or static
// block is dropped, and a dropped block is still counted on one foot line
// that is a door to the rail in full. Nothing moves: a block whose class
// rises takes its rows back where it stands
// (docs/interface/surfaces.md#the-inspector-rail).
//
// ALERTS sits directly under the turn because it is the one block that is
// about neither the turn nor the session but about what wants answering now:
// what this session has run that is still broken
// (docs/interface/surfaces.md#the-inspector-rail).
//
// THIS TURN is the turn. CHANGES, AGENTS, CONTEXT and SPEND are the session
//: a file edited in turn 2 is still on screen in turn 8,
// because "what has this session done to my machine" does not reset when the
// agent starts a new turn. The two blocks that can count files both say their
// scope in words — `3 files this turn` and `session · +96 −11` — which is the
// rule that stops the two numbers reading as a contradiction.

import (
	"fmt"
	"sort"
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
	//
	// The rung the design states is a 130-column *terminal*
	// (guidelines/layout-breakpoints), and what the ladder is handed
	// everywhere is a content width — the terminal less the two columns the
	// host surface insets on each side. This is the terminal rung in that
	// datum, so a 130-column terminal splits and a 129-column one does not.
	InspectorMinContentWidth = 126
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
	// Steps is the session's own working list. The host never sends it with
	// Plan: while an approved plan is being executed, the plan is the list.
	Steps   *InspectorSteps
	Todo    *InspectorTodo
	Alerts  InspectorAlerts
	Changes *InspectorChanges
	Agents  []InspectorAgent
	Tools   *InspectorTools
	Context *InspectorContext
	Spend   *InspectorSpend
	// Doors names the blocks, by their Rail* heading, that have a surface
	// behind them holding the whole of what the block bounds. Their heading
	// and fold marker point at the block; every other block's point at
	// nothing. Which blocks have one is the host's answer, because the
	// surfaces are the host's (docs/interface/surfaces.md#the-inspector-rail).
	Doors map[string]bool
}

// The blocks' names: the heading each one draws, and what a RailTargetBlock
// carries.
const (
	RailSummary = "SUMMARY"
	RailTurn    = "THIS TURN"
	RailAlerts  = "ALERTS"
	RailPlan    = "PLAN"
	RailSteps   = "STEPS"
	RailTodo    = "TODO"
	RailChanges = "CHANGES"
	RailAgents  = "AGENTS"
	RailTools   = "TOOLS"
	RailContext = "CONTEXT"
	RailSpend   = "SPEND"
	// RailWhole is not a block but the rail itself: what the foot line names
	// when the rail has dropped blocks, and the door that opens every block
	// unshed.
	RailWhole = "RAIL"
)

// railClass is how alive a block is, which is what decides the order the
// rail gives its rows up in when it runs short of height: the lowest class
// first. A block states its class from its own state every frame, so the
// same block is history at one moment and the thing that must not go at
// the next (docs/interface/surfaces.md#the-inspector-rail).
type railClass int

const (
	// railStatic is a block that reads the same from send to send: the
	// bill, a window under its warning, every tool source up.
	railStatic railClass = iota
	// railStanding changes between turns and is read before the next send:
	// the changeset, the backlog, an idle plan, a map of settled children.
	railStanding
	// railLive changes this turn and is read mid-turn: the turn's figures,
	// a reading on target, a window past its warning, a running plan step.
	railLive
	// railAsks wants the reader now or is moving now: a standing alert, a
	// child waiting or working, a running turn, a reading off target.
	railAsks
)

// Empty reports whether every block is omitted, so the host can skip the
// split rather than draw an empty column.
func (r InspectorRail) Empty() bool {
	return r.Summary == nil && r.Turn == nil && r.Plan == nil && r.Steps == nil && r.Todo == nil &&
		len(r.Alerts.Live()) == 0 && r.Changes == nil &&
		len(r.Agents) == 0 && r.Tools == nil && r.Context == nil && r.Spend == nil
}

// railTargetKind says what a row on the rail points at. Most of the rail
// points at nothing, and that is the default on purpose: a meter and a
// sentence are readings rather than doors. A block's heading and its fold
// marker are the exception where the block has a surface behind it: they
// open the surface that holds the whole of what the block bounds, and the
// same cell closes it again.
type railTargetKind int

const (
	// railTargetNone: the row is something to read.
	railTargetNone railTargetKind = iota
	// RailTargetFile: the row names a path the session has changed.
	RailTargetFile
	// RailTargetSession: the row names a session in the map.
	RailTargetSession
	// RailTargetBlock: the row is a block's heading or its fold marker, and
	// names the block.
	RailTargetBlock
)

// RailTarget is what a row points at: the kind of thing it names and the name
// itself — a workspace path for a file, a session's name for a session,
// which is empty for the session the rail belongs to, and a block's Rail*
// name for a block.
type RailTarget struct {
	Kind railTargetKind
	Name string
}

// railTargetRow is one rendered row of the rail beside what it points at. The rail
// assembles its rows from the session's own values, so a row already knows
// what it is; handing that out with the text is what lets a host resolve a
// cell to a target instead of parsing back the styled string it drew
// (docs/interface/surfaces.md#the-inspector-rail).
type railTargetRow struct {
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
	// give is the order this block's rows are taken in when the rail runs
	// short of height: the lowest number goes first, and rows sharing a
	// number go bottom-most first, which is what a block that numbers
	// nothing gets throughout. A block whose rows are not equally
	// expendable numbers them — the map can afford to lose a finished
	// session before a queued one, and neither is at the bottom
	// (docs/interface/surfaces.md#the-inspector-rail).
	give int
	// shed marks a row truncation removes outright rather than folding behind
	// the block's marker: chrome standing for nothing a marker could count —
	// a warning is not an item on a list. Folding one would spend
	// the row it just saved on the marker, leaving the rail no shorter and
	// the loop below taking a second row of real content for the same one row
	// of pressure — and the marker would then report something hidden that is
	// still on screen.
	shed bool
	// stated marks a row whose figure the block's collapsed heading states,
	// so once the block has collapsed the row is not one the marker has to
	// count: it is on screen, in the heading. A pinned stated row moves into
	// the heading when the block collapses.
	stated bool
	// more marks a row that is itself a count of list rows the host left out
	// — TODO's `… N more` — and holds that count. It is never taken on its
	// own: the first list row the block folds takes it with it, and the
	// block's marker states both, because a host's count and the block's
	// stacked one above the other are two numbers for one question.
	more int
}

// railBlock is one headed block under assembly: its heading line, its rows,
// and the rows truncation has taken.
type railBlock struct {
	heading string
	rows    []railLine
	// hidden holds what truncation took. Nothing reads its order — every fold
	// marker counts or sums it — which is what lets a block name a taking
	// order of its own rather than always losing its bottom row (give).
	hidden []railLine
	// fold renders the marker for the hidden rows. Nil prints the bare
	// "… N more" every block but CHANGES uses.
	fold func([]railLine) string
	// door is what the heading and the fold marker point at: the block
	// itself where it has a surface behind it, nothing where it has none.
	door RailTarget
	// name is the block's Rail* name, which the foot line says when the
	// block is dropped.
	name string
	// class is how alive the block is (railClass), stated by its builder
	// from its own state.
	class railClass
	// collapsedHeading is the heading a collapsed block draws: the same
	// label with the one figure its rows would have stated. Empty keeps the
	// heading as it is.
	collapsedHeading string
	// collapsed reports that truncation has taken every row the block could
	// give, so it is down to its heading, what it must keep, and a marker.
	collapsed bool
}

// add appends an ordinary row: truncation may take it, and it carries no
// counts of its own.
func (b *railBlock) add(text string) { b.rows = append(b.rows, railLine{text: text}) }

// pin appends a row truncation takes only when nothing else is left.
func (b *railBlock) pin(text string) { b.rows = append(b.rows, railLine{text: text, pinned: true}) }

// moreRow appends the host's own count of list rows it left out
// (railLine.more).
func (b *railBlock) moreRow(text string, n int) {
	b.rows = append(b.rows, railLine{text: text, more: n})
}

func (b railBlock) height() int {
	h := 1 + len(b.rows)
	if len(b.folded()) > 0 {
		h++
	}
	return h
}

// folded is what the block's marker counts: every hidden row but, once the
// block has collapsed, the ones its heading states.
func (b railBlock) folded() []railLine {
	if !b.collapsed {
		return b.hidden
	}
	var out []railLine
	for _, h := range b.hidden {
		if !h.stated {
			out = append(out, h)
		}
	}
	return out
}

// collapse puts the block down to its heading: the heading states the
// block's figure from here on, and a pinned row whose figure that is moves
// into it.
func (b *railBlock) collapse() {
	b.collapsed = true
	kept := make([]railLine, 0, len(b.rows))
	for _, r := range b.rows {
		if r.stated {
			b.hidden = append(b.hidden, r)
			continue
		}
		kept = append(kept, r)
	}
	b.rows = kept
}

// rowClass is the class a row is given up at: the block's, except for chrome
// a block sheds — a warning that is not a step of its list goes with the
// static blocks.
func (b railBlock) rowClass(row int) railClass {
	if b.rows[row].shed {
		return railStatic
	}
	return b.class
}

// render draws the block at the rail's width, each row beside what it points
// at. The width is a parameter and not the rail's floor: a fold marker drawn
// at 46 columns inside a 62-column rail is a row that ends where nothing else
// on the rail ends.
//
// The heading and the fold marker are the block's door. A heading names the
// block rather than a thing in it, and a marker stands for rows that are not
// on screen, so neither can point at one of the block's things — but both
// can point at the block, and where the block has a surface holding the whole
// of it, that is what they open. A host's own count of rows it left out
// (TODO's `… N more`) is a fold marker in the host's words and opens the same
// door. A block with no surface behind it keeps all of them inert.
func (b railBlock) render(width int) []railTargetRow {
	out := make([]railTargetRow, 0, b.height())
	heading := b.heading
	if b.collapsed && b.collapsedHeading != "" {
		heading = b.collapsedHeading
	}
	out = append(out, railTargetRow{Text: heading, Target: b.door})
	for _, r := range b.rows {
		target := r.target
		if r.more > 0 {
			target = b.door
		}
		out = append(out, railTargetRow{Text: r.text, Target: target})
	}
	switch hidden := b.folded(); {
	case len(hidden) == 0:
	case b.fold != nil:
		out = append(out, railTargetRow{Text: b.fold(hidden), Target: b.door})
	default:
		n := 0
		for _, h := range hidden {
			n += max(h.more, 1)
		}
		out = append(out, railTargetRow{Text: indentRow(sty.hint.Render(fmt.Sprintf("… %d more", n)), width), Target: b.door})
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
//
// A rail that had to drop blocks to fit ends on the foot line, on its last
// row, naming them: the rail never ends silently.
func (r InspectorRail) Rows(width, height int) []railTargetRow {
	blocks := r.blocks(width)
	if len(blocks) == 0 {
		return nil
	}
	var dropped []railBlock
	if height > 0 {
		blocks, dropped = fitBlocks(blocks, height)
	}
	var out []railTargetRow
	for i, b := range blocks {
		if i > 0 {
			out = append(out, railTargetRow{})
		}
		out = append(out, b.render(width)...)
	}
	if len(dropped) > 0 {
		for len(out) < height-1 {
			out = append(out, railTargetRow{})
		}
		foot := railTargetRow{Text: footRow(dropped, width)}
		if r.Doors[RailWhole] {
			foot.Target = RailTarget{Kind: RailTargetBlock, Name: RailWhole}
		}
		out = append(out, foot)
	}
	return out
}

// footRow is the rail's last row once it has dropped blocks: how many, and
// their names in rail order as far as they fit. It is the rail's own fold
// marker, so a dropped block is counted rather than gone
// (docs/interface/principles.md#fold-never-hide).
func footRow(dropped []railBlock, width int) string {
	room := railRoom(width, "", inspectorIndent)
	text := fmt.Sprintf("↓ %d more", len(dropped))
	for i, b := range dropped {
		next := text + " · " + strings.ToLower(b.name)
		rest := ""
		if i < len(dropped)-1 {
			rest = " · …"
		}
		if lipgloss.Width(next+rest) > room {
			if lipgloss.Width(text+" · …") <= room {
				text += " · …"
			}
			break
		}
		text = next
	}
	return indentRow(sty.hint.Render(text), width)
}

// blocks assembles the present blocks in their fixed order, each carrying
// its door where the host named one.
func (r InspectorRail) blocks(width int) []railBlock {
	var blocks []railBlock
	for _, b := range []struct {
		name  string
		build func(int) (railBlock, bool)
	}{
		{RailSummary, r.summaryBlock}, {RailTurn, r.turnBlock}, {RailAlerts, r.alertsBlock},
		{RailPlan, r.planBlock}, {RailSteps, r.stepsBlock}, {RailTodo, r.todoBlock},
		{RailChanges, r.changesBlock}, {RailAgents, r.agentsBlock}, {RailTools, r.toolsBlock},
		{RailContext, r.contextBlock}, {RailSpend, r.spendBlock},
	} {
		if blk, ok := b.build(width); ok {
			blk.name = b.name
			if r.Doors[b.name] {
				blk.door = RailTarget{Kind: RailTargetBlock, Name: b.name}
			}
			blocks = append(blocks, blk)
		}
	}
	return blocks
}

// fitBlocks truncates the rail into height rows, giving up the least alive
// thing first: a block collapses to its heading before a livelier block loses
// a row, only a block nobody is waiting on is dropped, and a dropped block is
// still counted on the foot line (docs/interface/surfaces.md#the-inspector-rail).
//
// Each pass asks its questions in order, the way the title row sheds its
// facts, and takes the first that has an answer: collapse a block below the
// class of the next row that would go; take that row while it is a static or
// standing one; drop a block — static before standing, the lowest first,
// never one that asks or reads live — before a live block loses a row; else
// take the row. So the static and standing blocks collapse, then leave, and
// only then do the live blocks and the asks collapse. Once nothing has a row
// to give, pinned rows go bottom-up, and once every block is down to its
// heading the lowest goes to the foot line, so a rail too short for what it
// must keep still says what it left out. Nothing reorders: a block that stays
// keeps its place, which is what lets a block whose class rises take its rows
// back where it stands.
func fitBlocks(blocks []railBlock, height int) (kept, dropped []railBlock) {
	for railHeight(blocks, len(dropped) > 0) > height {
		block, row, ok := nextToHide(blocks)
		next := railAsks + 1
		if ok {
			next = blocks[block].rowClass(row)
		}
		if i := toCollapse(blocks, next); i >= 0 {
			blocks[i].collapse()
			continue
		}
		if ok && next <= railStanding {
			take(&blocks[block], row)
			continue
		}
		if i := toDrop(blocks); i >= 0 {
			dropped = append(dropped, blocks[i])
			blocks = append(blocks[:i:i], blocks[i+1:]...)
			continue
		}
		if ok {
			take(&blocks[block], row)
			continue
		}
		if block, row, ok = lastToHide(blocks); ok {
			take(&blocks[block], row)
			continue
		}
		last := len(blocks) - 1
		if last < 0 {
			break
		}
		dropped = append(dropped, blocks[last])
		blocks = blocks[:last]
	}
	sortByRail(dropped)
	return blocks, dropped
}

// railHeight is the rows the blocks take with a blank between each, and the
// foot line when there is one. The foot line costs its own row and no more:
// it stands on the rail's last row, so whatever the blocks leave between it
// and them is blank, and where they leave nothing it is marked apart by its
// arrow.
func railHeight(blocks []railBlock, foot bool) int {
	n := 0
	if len(blocks) > 0 {
		n = total(blocks)
	}
	if foot {
		n++
	}
	return n
}

// take moves one row of the block behind its marker, or off the rail where it
// is chrome, and collapses the block once it has no row left to give.
func take(b *railBlock, row int) {
	taken := b.rows[row]
	b.rows = append(b.rows[:row:row], b.rows[row+1:]...)
	if !taken.shed {
		b.hidden = append([]railLine{taken}, b.hidden...)
		// The marker this fold draws is the block's only count from here on,
		// so a host's count row goes behind it in the same step rather than
		// standing over it (railLine.more).
		for j := len(b.rows) - 1; j >= 0; j-- {
			if b.rows[j].more > 0 {
				b.hidden = append(b.hidden, b.rows[j])
				b.rows = append(b.rows[:j:j], b.rows[j+1:]...)
			}
		}
	}
	if !b.collapsed && !givable(*b) {
		b.collapse()
	}
}

// toCollapse is the lowest block below class next with no row left to give
// that would still be shorter collapsed — a pinned row its heading states —
// or -1.
func toCollapse(blocks []railBlock, next railClass) int {
	for i := len(blocks) - 1; i >= 0; i-- {
		b := blocks[i]
		if b.class >= next || b.collapsed || givable(b) {
			continue
		}
		shut := b
		shut.rows = append([]railLine(nil), b.rows...)
		shut.hidden = append([]railLine(nil), b.hidden...)
		shut.collapse()
		if shut.height() < b.height() {
			return i
		}
	}
	return -1
}

// toDrop is the block the rail drops once every block has collapsed: the
// lowest static block, then the lowest standing one, and never a block that
// asks or reads live. -1 is none.
func toDrop(blocks []railBlock) int {
	for class := railStatic; class <= railStanding; class++ {
		for i := len(blocks) - 1; i >= 0; i-- {
			if blocks[i].class == class {
				return i
			}
		}
	}
	return -1
}

// railOrder is every block's name in the order the rail draws them.
var railOrder = []string{RailSummary, RailTurn, RailAlerts, RailPlan, RailSteps, RailTodo,
	RailChanges, RailAgents, RailTools, RailContext, RailSpend}

// sortByRail puts the dropped blocks back in the rail's own order, the order
// the foot line names them in.
func sortByRail(blocks []railBlock) {
	at := make(map[string]int, len(railOrder))
	for i, name := range railOrder {
		at[name] = i
	}
	sort.SliceStable(blocks, func(a, b int) bool { return at[blocks[a].name] < at[blocks[b].name] })
}

// nextToHide is the row truncation takes next among the rows a block may
// give: the lowest class first, then the longest block with a row of it, then
// the lower of two the same length. Which of a block's rows is the most
// expendable is the block's own answer, in give; a block that gives no answer
// loses its bottom row first.
//
// Two blocks of the same class and length are a tie the lower one loses. The
// rail is read downwards and its order runs from the turn out to the session,
// so the block nearer the top is the one nearer what is happening now
// (docs/interface/surfaces.md#the-inspector-rail).
//
// A pinned row is never a candidate here. That is what keeps ALERTS whole:
// its live rows are pinned, and a rail that took them while it could have
// folded a file row would take the news off to keep the history
// (docs/interface/surfaces.md#the-inspector-rail).
func nextToHide(blocks []railBlock) (block, row int, ok bool) {
	class := railAsks + 1
	for _, b := range blocks {
		for j, r := range b.rows {
			if takeable(r) && b.rowClass(j) < class {
				class = b.rowClass(j)
			}
		}
	}
	if class > railAsks {
		return 0, 0, false
	}
	block, rows := -1, 0
	for i, b := range blocks {
		if len(b.rows) < rows || !b.gives(class) {
			continue
		}
		block, rows = i, len(b.rows)
	}
	b := blocks[block]
	row = -1
	for j := len(b.rows) - 1; j >= 0; j-- {
		if !takeable(b.rows[j]) || b.rowClass(j) != class {
			continue
		}
		if row < 0 || b.rows[j].give < b.rows[row].give {
			row = j
		}
	}
	return block, row, true
}

// gives reports whether the block has a row of that class to give.
func (b railBlock) gives(class railClass) bool {
	for j, r := range b.rows {
		if takeable(r) && b.rowClass(j) == class {
			return true
		}
	}
	return false
}

// lastToHide is the row taken once no block has one to give: the bottom-most
// row of the least alive block that has rows left, pinned or not, the longest
// of a class first and the lower of two the same length — because a rail
// that cannot fit what it must keep still has to end somewhere, and what it
// keeps longest is what is asking for the reader.
func lastToHide(blocks []railBlock) (block, row int, ok bool) {
	longest := -1
	for i, b := range blocks {
		if len(b.rows) == 0 {
			continue
		}
		if longest < 0 || b.class < blocks[longest].class ||
			b.class == blocks[longest].class && len(b.rows) >= len(blocks[longest].rows) {
			longest = i
		}
	}
	if longest < 0 {
		return 0, 0, false
	}
	return longest, len(blocks[longest].rows) - 1, true
}

// takeable reports whether truncation may take a row before it starts on the
// pinned ones. A host's count row is never taken on its own (railLine.more).
func takeable(r railLine) bool { return !r.pinned && r.more == 0 }

// givable reports whether a block has a row truncation may take before it
// starts taking pinned ones.
func givable(b railBlock) bool {
	for _, r := range b.rows {
		if takeable(r) {
			return true
		}
	}
	return false
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
	return railRow(sty.headline.Render(label), meta, width, inspectorIndent)
}

// railRow lays one row out: indent, left field, right field against the
// rail's right edge. The left field clips when the two would collide — the
// right field is the number, and a clipped number is a wrong number.
func railRow(left, right string, width, indent int) string {
	left = Clip(left, max(railRoom(width, right, indent), 0))
	gap := width - indent - lipgloss.Width(left) - lipgloss.Width(right)
	return strings.Repeat(" ", indent) + left + strings.Repeat(" ", max(gap, 0)) + right
}

// railRoom is the columns railRow leaves the left field: the rail less the
// indent, the right field and the space between them. A block deciding
// whether something fits before it renders it asks this rather than measuring
// the row afterwards, so the two cannot come to disagree about where the left
// field ends.
func railRoom(width int, right string, indent int) int {
	room := width - indent - lipgloss.Width(right)
	if right != "" {
		room-- // at least one space between the fields
	}
	return room
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
	plus, minus := diffStatParts(added, removed)
	return plus.Tone.style().Render(plus.Text) + " " + minus.Tone.style().Render(minus.Text)
}

// DiffStatSpans is that same line count as the two tokens it is made of, for
// a surface that has to measure it before painting it, or paint it twice — a
// list row, which is drawn once in its own tones and once bright on the focus
// background. It is shaped by the same function DiffStat is, for the reason
// DiffStat exists: an edit stated two ways is a chance to disagree.
func DiffStatSpans(added, removed int) []DetailSpan {
	plus, minus := diffStatParts(added, removed)
	return []DetailSpan{plus, {Text: " ", Tone: ToneQuiet}, minus}
}

// diffStatParts is what the two halves say and how each is read.
func diffStatParts(added, removed int) (plus, minus DetailSpan) {
	return DetailSpan{Text: fmt.Sprintf("+%d", added), Tone: ToneSafe},
		DetailSpan{Text: fmt.Sprintf("−%d", removed), Tone: ToneRisk}
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
