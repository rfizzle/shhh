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
// and the block order is fixed — THIS TURN, ALERTS, PLAN, CHANGES, AGENTS,
// CONTEXT, SPEND. A block with nothing to say is omitted rather than rendered
// empty, and a rail that does not fit its height takes its rows off the
// longest block that has one to give and says how many it swallowed.
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
	Todo    *InspectorTodo
	Alerts  InspectorAlerts
	Changes *InspectorChanges
	Agents  []InspectorAgent
	// AgentsHint is the trailer under the map: how to reach the sessions it
	// draws. It is beside the slice rather than on a block struct of its own
	// because the map's rows are the block — and it is the host's sentence
	// rather than a constant here, since the letters in it are whatever the
	// key register currently binds
	// (docs/interface/surfaces.md#the-inspector-rail).
	AgentsHint string
	// AgentsOption says this trailer is where the session first offers a
	// chord, so it is the one that carries what alt costs on a stock macOS
	// terminal. Every clause of the trailer is a chord, which makes the whole
	// row a false offer on a terminal that composes a character for one, and
	// a map is often on screen long before a transcript row offers anything
	// (docs/interface/reserved-keys.md#the-draft-spends-chords-only).
	AgentsOption bool
	Tools        *InspectorTools
	Context      *InspectorContext
	Spend        *InspectorSpend
	// Frame is the host's spinner frame index, for the lanes of children that
	// declared no step count. The rail stays passive: it animates nothing, it
	// just draws the frame it is handed.
	Frame int
}

// Empty reports whether every block is omitted, so the host can skip the
// split rather than draw an empty column.
func (r InspectorRail) Empty() bool {
	return r.Summary == nil && r.Turn == nil && r.Plan == nil && r.Todo == nil &&
		len(r.Alerts.Live()) == 0 && r.Changes == nil &&
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
	// a trailer naming keys is not an item on a list. Folding one would spend
	// the row it just saved on the marker, leaving the rail no shorter and
	// the loop below taking a second row of real content for the same one row
	// of pressure — and the marker would then report something hidden that is
	// still on screen.
	shed bool
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

// shedRow appends a row truncation removes outright rather than folding it
// behind the block's marker (railLine.shed): chrome standing for nothing the
// marker could count, such as a trailer naming a command.
func (b *railBlock) shedRow(text string) { b.rows = append(b.rows, railLine{text: text, shed: true}) }

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
		n := 0
		for _, h := range b.hidden {
			n += max(h.more, 1)
		}
		out = append(out, RailRow{Text: indentRow(sty.Hint.Render(fmt.Sprintf("… %d more", n)), width)})
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
		r.summaryBlock, r.turnBlock, r.alertsBlock, r.planBlock, r.todoBlock, r.changesBlock,
		r.agentsBlock, r.toolsBlock, r.contextBlock, r.spendBlock,
	} {
		if blk, ok := b(width); ok {
			blocks = append(blocks, blk)
		}
	}
	return blocks
}

// fitBlocks truncates the rail into height rows, taking rows off the block
// with the most of them to give. A truncated block keeps its heading and says
// how many rows it is hiding, so the rail never ends silently; CHANGES and
// ALERTS fold rather than truncate, and their markers carry what they took.
func fitBlocks(blocks []railBlock, height int) []railBlock {
	for total(blocks) > height {
		block, row, ok := nextToHide(blocks)
		if !ok {
			// Every block is down to its heading: nothing left to give.
			break
		}
		b := &blocks[block]
		taken := b.rows[row]
		b.rows = append(b.rows[:row], b.rows[row+1:]...)
		if taken.shed {
			continue
		}
		b.hidden = append([]railLine{taken}, b.hidden...)
		// The marker this fold draws is the block's only count from here on,
		// so a host's count row goes behind it in the same step rather than
		// standing over it (railLine.more).
		for j := len(b.rows) - 1; j >= 0; j-- {
			if b.rows[j].more > 0 {
				b.hidden = append(b.hidden, b.rows[j])
				b.rows = append(b.rows[:j], b.rows[j+1:]...)
			}
		}
	}
	return blocks
}

// nextToHide is the row truncation takes next: the most expendable unpinned
// row of the longest block that still has one, and — once every block is down
// to pinned rows — the bottom-most row of the longest block, because a rail
// that cannot fit what it must keep still has to end somewhere. Which of a
// block's rows is the most expendable is the block's own answer, in give;
// a block that gives no answer loses its bottom row first.
//
// Two blocks of the same length are a tie the lower one loses, in both
// passes. The rail is read downwards and its order runs from the turn out to
// the session, so the block nearer the top is the one nearer what is
// happening now: without this the turn's own block hands over a row while a
// block of session history keeps all of its
// (docs/interface/surfaces.md#the-inspector-rail).
//
// A block with nothing but pinned rows is not a candidate while any other
// block has a row to give. That is what keeps ALERTS whole: it is short, its
// live rows are pinned, and picking by length alone would take the news off a
// rail that could have folded a file row instead
// (docs/interface/surfaces.md#the-inspector-rail).
func nextToHide(blocks []railBlock) (block, row int, ok bool) {
	block, rows := -1, 0
	for i, b := range blocks {
		if len(b.rows) < rows || !givable(b) {
			continue
		}
		block, rows = i, len(b.rows)
	}
	if block >= 0 {
		row = -1
		for j := len(blocks[block].rows) - 1; j >= 0; j-- {
			if r := blocks[block].rows[j]; r.pinned || r.more > 0 {
				continue
			}
			if row < 0 || blocks[block].rows[j].give < blocks[block].rows[row].give {
				row = j
			}
		}
		if row >= 0 {
			return block, row, true
		}
	}
	longest := -1
	for i, b := range blocks {
		if longest < 0 || len(b.rows) >= len(blocks[longest].rows) {
			longest = i
		}
	}
	if longest < 0 || len(blocks[longest].rows) == 0 {
		return 0, 0, false
	}
	return longest, len(blocks[longest].rows) - 1, true
}

// givable reports whether a block has a row truncation may take before it
// starts taking pinned ones.
func givable(b railBlock) bool {
	for _, r := range b.rows {
		if !r.pinned && r.more == 0 {
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
	return railRow(sty.Headline.Render(label), meta, width, inspectorIndent)
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
