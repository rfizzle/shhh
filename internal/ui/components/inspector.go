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

// InspectorSummary is the SUMMARY block: a cheap model's read of what
// the session is doing and whether it is still doing what was asked. It is
// the one block that is not a count — the numbers under it say how much has
// happened, and this says what.
type InspectorSummary struct {
	// Text is the reading itself, in the model's own words. It is wrapped to
	// the rail rather than clipped, because a sentence cut at 44 columns is a
	// sentence nobody can finish.
	Text string
	// State is the reading's judgement, drawn as its own row.
	State SummaryTone
	// Reason qualifies a state that is not on target, and is empty otherwise
	// — a departure the reader can see is worth a row, and "on target
	// because…" is the model narrating.
	Reason string
	// Round is the tool round the reading was taken at, which the heading
	// states. A summary without it is a claim about now that nobody can
	// check.
	Round int
	// Stale marks a reading the session has outrun — a refresh that failed,
	// or one still in flight past its interval. The heading says so rather
	// than letting an old sentence pass for a current one.
	Stale bool
}

// SummaryTone is the rail's rendering of a summary's state. It mirrors
// agent.SummaryState; the component keeps its own type for the same reason
// PlanStepState is not the transcript's own — the rail draws, it does not
// import the session's vocabulary.
type SummaryTone int

const (
	// SummaryUnclear is the reading that could not tell.
	SummaryUnclear SummaryTone = iota
	// SummaryOnTarget is the run still serving the instruction it started
	// from.
	SummaryOnTarget
	// SummaryOffTarget is the run that has departed from it.
	SummaryOffTarget
	// SummarySufficient is the run still on its instruction that has found
	// what it needs and has not started acting on it.
	SummarySufficient
)

// InspectorTurn is the THIS TURN block: how far through its steps the turn
// is, how many tools it has spent, and how long it has been running.
type InspectorTurn struct {
	// Step and Steps drive the progress meter and the "step 3 of 4" heading.
	// Steps == 0 means the turn declared none, so no ratio is fabricated —
	// the block states its tool count and elapsed time alone.
	Step, Steps int
	Tools       int
	Elapsed     time.Duration
	// Files and its counts are what this turn changed — the turn-scoped half of
	// the scoped pair, and the reason the row says "this turn" in words rather
	// than printing a bare count beside CHANGES' session total.
	Files          int
	Added, Removed int
	// Running says the turn is still in flight, which is what lights the
	// progress meter's current cell. The row states the clock without saying
	// whether it is still moving — the live turn status is what answers that
	//, and saying it twice cost the row its file count.
	Running bool
}

// PlanStepState is one checklist step's state in the PLAN block. It is the
// same four states the step outline draws, because an approved plan's
// step and the transcript's step are the same step.
type PlanStepState int

const (
	// PlanStepQueued is declared and not started; its duration is —.
	PlanStepQueued PlanStepState = iota
	// PlanStepRunning is the step in flight, the one the reader is looking
	// for, so it is the one the block emphasises.
	PlanStepRunning
	// PlanStepDone finished with nothing to report.
	PlanStepDone
	// PlanStepFailed finished and contained a failure.
	PlanStepFailed
)

// InspectorPlanStep is one step of the approved plan in the PLAN block.
type InspectorPlanStep struct {
	Number int
	Title  string
	State  PlanStepState
	// Elapsed is how long the step took; blank for one that has not finished,
	// so the column carries a number only where there is one.
	Elapsed string
}

// InspectorPlan is the PLAN block: an approved plan as a live checklist
// . An approved plan is not a message that scrolls away — it is the
// answer to "where are we", and the rail is where that answer belongs.
type InspectorPlan struct {
	Steps []InspectorPlanStep
	// Done counts the steps that finished, which is what the heading states.
	// A failed step finished.
	Done int
	// Drift is the one-line note when the run has departed from the plan —
	// work the plan never named, steps taken out of order, steps skipped.
	// Empty while the run is following it, because "no drift" is not news.
	Drift string
	// Hint is the row under the list naming how to read the whole plan.
	Hint string
}

// TodoRowState is what a backlog row's glyph says about it.
type TodoRowState int

const (
	// TodoReady can be started now.
	TodoReady TodoRowState = iota
	// TodoWaiting is open but has a dependency still outstanding.
	TodoWaiting
	// TodoBlocked needs a person before it can move.
	TodoBlocked
	// TodoRunning is being worked on in this session.
	TodoRunning
)

// InspectorTodoRow is one backlog item in the TODO block.
type InspectorTodoRow struct {
	Slug string
	// Priority and Size are one letter each — H/M/L and S/M/L — the two
	// facts that decide the order and the ceremony, and nothing else fits.
	Priority, Size string
	State          TodoRowState
	// Note is the right-hand column: what the row waits on, or the stage a
	// running one is at. Blank for a ready row, which has nothing to add.
	Note string
}

// InspectorTodo is the TODO block: what the project still owes, so "what
// comes after this" is on screen next to "where are we". PLAN says through
// what this turn is going; TODO says what is queued behind it. It is the
// one block scoped wider than the session — the backlog is the project's —
// and its heading says so in words, like every other block says its scope.
// See docs/interface/surfaces.md#the-inspector-rail.
type InspectorTodo struct {
	// Open and Blocked are the counts the heading states over the whole
	// backlog, whatever Rows shows of it.
	Open, Blocked int
	Rows          []InspectorTodoRow
	// More is how many active items Rows left out.
	More int
	// Hint is the row under the list naming how to see the whole backlog.
	Hint string
}

// InspectorFile is one changed path in the CHANGES block: the session's net
// change to it, however many turns produced that.
type InspectorFile struct {
	Path           string
	Added, Removed int
	// Turns is how many turns edited this path. Above one it is stated as
	// `3t` beside the counts, because repeat edits collapse to one row and
	// the row should say that it did.
	Turns int
	// ThisTurn marks a path the running turn touched. Those rows are the last
	// the fold takes, so the turn in front of you keeps its rows while the
	// session's older ones go behind `… N more`.
	ThisTurn bool
}

// InspectorAlert is one thing the workspace is still wrong about: a command
// whose last run in this session came back broken, what it said, and the turn
// that ran it. Alerts outlive their turn and clear when the workspace is
// clean — a red row that clears itself because a new turn started is the
// exact failure this rail exists to prevent.
type InspectorAlert struct {
	Label string
	Note  string
	// Turn is the turn that ran it; zero prints no turn field.
	Turn int64
}

// InspectorChanges is the CHANGES block: what this session has written to the
// workspace, and what about it is still broken.
type InspectorChanges struct {
	Files          []InspectorFile
	Added, Removed int
	// Alerts are the failing commands still standing, oldest first. They are
	// drawn above the file rows and are the last thing truncation takes.
	Alerts []InspectorAlert
}

// InspectorAgent is one session in the AGENTS block — the orchestrator or one
// of its children. Steps is only set when the child declared a step count;
// without one the row shows its tool count rather than a fabricated ratio.
type InspectorAgent struct {
	Name   string
	Detail string
	Spend  string
	Tools  int
	// Step and Steps drive the five-cell lane meter. Steps == 0 means the
	// child declared no total, so the lane shows the spinner beside what it
	// is doing instead of a bar drawn against a denominator nobody supplied.
	Step, Steps int
	// State is the session's lifecycle state, in the same vocabulary a
	// fan-out lane and a manager row use, so one child cannot be drawn three
	// ways on one screen.
	State FanoutState
	// Outcome is the word a session that has stopped ends on. It is the
	// host's own word rather than one derived here: the block states how a
	// child ended, and the only authority on that is whatever ran it. Empty
	// for a session still moving, whose detail row already says what it is
	// doing.
	Outcome string
	// Focused marks the session the keyboard is in. It is the one thing the
	// rest of the rail cannot say for itself: every other block answers for
	// the session as a whole, so beside a child's transcript the mark is
	// what stops the numbers reading as the child's.
	Focused bool
	// Self marks the orchestrator's own row. It is not a child: it never
	// finishes, so it never folds, and it is left out of the tally the
	// heading states about what the children still owe you.
	Self bool
}

// InspectorContext is the CONTEXT block: occupancy of the model's window,
// the tokens behind it, and the per-round burn.
type InspectorContext struct {
	Pct              int
	Tokens, Window   int64
	Tokens1, Tokens2 string // the ↑in and ↓out labels
	// Burn is the per-round context series behind the sparkline, fed from the
	// session's vitals history. One sample is a dot, not a trend, so
	// the host sends nothing until it has two and the row says "estimated"
	// instead of drawing a flat line.
	Burn []float64
	// WarnPct/AlertPct override the meter's threshold colors (0 keeps the
	// defaults), so the rail matches the host's own trim warnings.
	WarnPct, AlertPct int
	// Estimated says the occupancy is the host's own estimate rather than a
	// provider-reported size, and the block says so in words — a
	// number nobody vouched for should not look like one that was.
	Estimated bool
}

// InspectorSpend is the SPEND block: this turn's cost, how it split between
// the orchestrator and its children, and the session total.
type InspectorSpend struct {
	Turn     string
	Main     string
	Children string
	Session  string
	Model    string
}

// InspectorTools is the TOOLS block: where this session's tools came from
// and which of those sources answered. A tool that silently failed to
// register is otherwise indistinguishable from one the model simply did not
// call, and the difference is the whole reason the block exists.
// See docs/interface/surfaces.md#the-inspector-rail.
type InspectorTools struct {
	Sources []InspectorToolSource
	// Up is how many sources answered, over every source the session has and
	// not only the rows that fit — the heading states it against the same
	// total, and a fold that changed the numerator would make the ratio a lie.
	Up int
	// More is how many sources Sources left out.
	More int
	// MemoryOmitted is how many durable memories the recall budget kept out
	// of the system prompt. It is on this block because it is the same
	// question the block already answers — what did this session actually get
	// — and a memory the session never saw is otherwise indistinguishable
	// from one that was never written.
	MemoryOmitted int
}

// InspectorToolSource is one place tools came from: the built-in toolset, or
// a server the session was told to reach.
type InspectorToolSource struct {
	Name  string
	State ToolSourceState
	// Note is what the state amounts to in the host's own words — a tool
	// count for a source that answered, the reason for one that did not.
	Note string
}

// ToolSourceState is a source's standing, and the block draws it as a glyph
// and a word so a monochrome terminal reads the same as a colour one. It is
// four states rather than the transport's own vocabulary because the reader's
// question is whether the tools are there, and the note beside it says why
// when they are not.
type ToolSourceState int

const (
	// ToolSourceUp: it answered and its tools are in the toolset.
	ToolSourceUp ToolSourceState = iota
	// ToolSourceBlocked: it is configured and something is in the way that
	// only a person can move.
	ToolSourceBlocked
	// ToolSourceOff: it was left out on purpose.
	ToolSourceOff
	// ToolSourceFailed: it was tried and did not answer.
	ToolSourceFailed
)

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

// summaryLines is how many wrapped rows the reading may take. Three is what
// two short sentences need at this width; a model that wrote more has written
// more than the block asked for, and the rest folds.
const summaryLines = 3

// summaryReasonLines is how many rows a departure's reason may take under the
// state row. Two, because a reason that needs three is not a reason, it is a
// second summary.
const summaryReasonLines = 2

// summaryBlock is the rail's one prose block. It sits first because
// it is the answer the rest of the rail is the detail of: SUMMARY says what
// is happening, THIS TURN says how far through, CHANGES says what it cost the
// workspace.
//
// The state row is drawn in every state, including on target. PLAN's drift
// line is not — "no drift is not news" — and the difference is where the two
// come from: PLAN's drift is computed from the plan and the steps taken, so
// its absence is a fact, while this is a model's judgement, and a block that
// went quiet when the judgement was "fine" would be indistinguishable from
// one whose reading failed.
func (r InspectorRail) summaryBlock(width int) (railBlock, bool) {
	s := r.Summary
	if s == nil || strings.TrimSpace(s.Text) == "" {
		return railBlock{}, false
	}
	var fields []string
	if s.Round > 0 {
		fields = append(fields, fmt.Sprintf("as of round %d", s.Round))
	}
	metaStyle := sty.Dim
	if s.Stale {
		// An old reading is still the best reading there is — it is just not
		// a current one, and the heading is where that is said.
		fields, metaStyle = append(fields, "stale"), sty.Accent
	}
	meta := strings.Join(fields, " · ")
	b := railBlock{heading: railHeading("SUMMARY", meta, metaStyle, width)}

	lines := wrapPlain(s.Text, width-inspectorIndent)
	if len(lines) > summaryLines {
		lines = lines[:summaryLines]
		lines[summaryLines-1] = clip(lines[summaryLines-1], width-inspectorIndent-1) + "…"
	}
	for i, line := range lines {
		// The first line is pinned: a block truncated to its heading would
		// leave the rail with a word and no sentence, and the sentence is the
		// whole block. The rest fold from the bottom like any other rows.
		row := indentRow(sty.Body.Render(line), width)
		if i == 0 {
			b.pin(row)
			continue
		}
		b.add(row)
	}

	b.pin(indentRow(SummaryLabel(s.State), width))
	// The reason gets its own rows rather than a suffix on the state row: it
	// is the whole content of a departure, and at 46 columns a suffix is a
	// reason clipped mid-word. It follows its state the way an alert's note
	// follows its alert in CHANGES — one indent further in, dim, bounded.
	for i, line := range wrapPlain(s.Reason, width-inspectorIndent-2) {
		if s.Reason == "" || i >= summaryReasonLines {
			break
		}
		b.pin(railRow(sty.Dim.Render(line), "", width, inspectorIndent+2))
	}
	return b, true
}

// SummaryLabel is a reading's judgement as one clause: the glyph, then the
// words. The rail draws it as a row of its own and the input frame's status
// row leads a line with it, so a terminal too narrow for the rail reads the
// same verdict in the same marks
// (docs/interface/surfaces.md#the-inspector-rail).
func SummaryLabel(s SummaryTone) string {
	glyph, label, style := summaryTone(s)
	return glyph + " " + style.Render(label)
}

// summaryTone is the state row's glyph, its words and its weight. The glyph
// carries the distinction so a monochrome terminal reads the same as a colour
// one: ▸ for a run still on its instruction, ◆ for one that has what it needs
// and is still looking, ⚠ for one that has left the instruction, · for a
// reading that could not tell.
//
// Only the departure is drawn in the accent: a run that has found what it
// needs is not a warning, it is news, so it takes the reading weight and the
// healthy glyph colour.
func summaryTone(s SummaryTone) (string, string, lipgloss.Style) {
	glyph, word := SummaryGlyph(s), SummaryWord(s)
	switch s {
	case SummaryOnTarget:
		return sty.SpinText.Render(glyph), word, sty.Dim
	case SummarySufficient:
		return sty.SpinText.Render(glyph), word, sty.Body
	case SummaryOffTarget:
		return sty.Accent.Render(glyph), word, sty.Body
	}
	return sty.Dim.Render(glyph), word, sty.Dim
}

// SummaryGlyph and SummaryWord are the same verdict unpainted, for the
// callers that render it into a field of their own — the transcript's summary
// row states its verdict in the outcome column, and a colour set there would
// be a second wrapper around a string the row is about to paint itself
// (activityrow.go). The glyph goes first wherever both are used: it is what
// carries the distinction on a terminal with no colour at all.
func SummaryGlyph(s SummaryTone) string {
	switch s {
	case SummaryOnTarget:
		return "▸"
	case SummarySufficient:
		return "◆"
	case SummaryOffTarget:
		return "⚠"
	}
	return "·"
}

func SummaryWord(s SummaryTone) string {
	switch s {
	case SummaryOnTarget:
		return "on target"
	case SummarySufficient:
		return "has enough"
	case SummaryOffTarget:
		return "off target"
	}
	return "target unclear"
}

func (r InspectorRail) turnBlock(width int) (railBlock, bool) {
	t := r.Turn
	if t == nil {
		return railBlock{}, false
	}
	meta := ""
	if t.Steps <= 0 && t.Step > 0 {
		// Steps observed, none declared: the ordinal is true, the ratio would
		// not be, so no denominator and no meter.
		meta = fmt.Sprintf("step %d", t.Step)
	}
	b := railBlock{heading: railHeading("THIS TURN", meta, sty.Dim, width)}
	if m, ok := StepMeter(t.Step, t.Steps, railCells(MeterCellsRail, width), t.Running); ok {
		// The count sits beside the bar rather than in the heading, because a
		// bar is never the only carrier of its value.
		b.add(indentRow(m.View(), width))
	}
	// "3 files this turn" rather than "3 files": CHANGES counts files too, and
	// the two are different questions, so both say their scope in words
	//. A turn that wrote nothing still says so — that is the fact.
	files := sty.Dim.Render(plural(t.Files, "file") + " this turn")
	if t.Files > 0 {
		files += " " + DiffStat(t.Added, t.Removed)
	}
	b.add(indentRow(strings.Join([]string{
		files,
		sty.Dim.Render(plural(t.Tools, "tool")),
		sty.Dim.Render(FormatElapsed(t.Elapsed)),
	}, sty.Dim.Render(" · ")), width))
	return b, true
}

// planBlock is the PLAN checklist. It sits under THIS TURN because it
// is that block's detail: THIS TURN says how far through, PLAN says through
// what. The keys it prints are the host's, like [v] and [u] on CHANGES.
func (r InspectorRail) planBlock(width int) (railBlock, bool) {
	p := r.Plan
	if p == nil || len(p.Steps) == 0 {
		return railBlock{}, false
	}
	b := railBlock{heading: railHeading("PLAN",
		fmt.Sprintf("%d of %d done", p.Done, len(p.Steps)), sty.Dim, width)}
	for _, s := range p.Steps {
		glyph, style := planStepTone(s.State)
		// A step with no duration yet gets no right-hand field at all, so the
		// title has the whole row rather than a column reserved for nothing.
		elapsed := ""
		if s.Elapsed != "" {
			elapsed = sty.Dim.Render(s.Elapsed)
		}
		b.add(railRow(glyph+" "+style.Render(s.Title), elapsed, width, inspectorIndent))
	}
	if p.Drift != "" {
		b.add(railRow(sty.Accent.Render("⚠")+" "+sty.Dim.Render(p.Drift),
			"", width, inspectorIndent))
	}
	if p.Hint != "" {
		b.add(indentRow(sty.Hint.Render(p.Hint), width))
	}
	return b, true
}

// todoBlock is the TODO block. It sits under PLAN because it is the same
// question one step further out — PLAN is this turn's list, TODO is the
// project's — and above CHANGES because it is about work, not about files.
// A backlog with nothing active is no block at all.
func (r InspectorRail) todoBlock(width int) (railBlock, bool) {
	t := r.Todo
	if t == nil || (t.Open == 0 && t.Blocked == 0 && len(t.Rows) == 0) {
		return railBlock{}, false
	}
	meta := fmt.Sprintf("project · %d open", t.Open)
	if t.Blocked > 0 {
		meta += fmt.Sprintf(" · %d blocked", t.Blocked)
	}
	b := railBlock{heading: railHeading("TODO", meta, sty.Dim, width)}
	for _, row := range t.Rows {
		glyph, style := todoRowTone(row.State)
		left := glyph + " " + sty.Dim.Render(row.Priority+" "+row.Size) + " " + style.Render(row.Slug)
		note := ""
		if row.Note != "" {
			note = sty.Dim.Render(row.Note)
		}
		b.add(railRow(left, note, width, inspectorIndent))
	}
	if t.More > 0 {
		b.add(indentRow(sty.Dim.Render(fmt.Sprintf("… %d more", t.More)), width))
	}
	if t.Hint != "" {
		b.add(indentRow(sty.Hint.Render(t.Hint), width))
	}
	return b, true
}

// todoRowTone is a backlog row's glyph and the weight its slug carries. The
// running one is bright for the same reason the running plan step is; a
// blocked one carries the error mark because it is waiting on a person.
func todoRowTone(s TodoRowState) (string, lipgloss.Style) {
	switch s {
	case TodoRunning:
		return sty.SpinText.Render("▸"), brightStyle()
	case TodoBlocked:
		return sty.Err.Render("!"), sty.Body
	case TodoWaiting:
		return sty.Dim.Render("·"), sty.Dim
	}
	return sty.Dim.Render("·"), sty.Body
}

// planStepTone is a checklist step's glyph and the weight its title carries.
// The running step is the one being looked for, so it is the bright one; a
// finished step recedes to chrome, having nothing left to ask of anyone.
func planStepTone(s PlanStepState) (string, lipgloss.Style) {
	switch s {
	case PlanStepRunning:
		return sty.SpinText.Render("▸"), brightStyle()
	case PlanStepDone:
		return sty.Add.Render("✓"), sty.Dim
	case PlanStepFailed:
		return sty.Err.Render("✗"), sty.Body
	}
	return sty.Dim.Render("·"), sty.Dim
}

// changesBlock is the session's own diff: every path it has
// touched since it opened, one row each, with the alerts still standing above
// them. The heading says "session" in words because THIS TURN counts files
// too, and a rail that printed two bare counts would read as a contradiction.
func (r InspectorRail) changesBlock(width int) (railBlock, bool) {
	c := r.Changes
	if c == nil || (len(c.Files) == 0 && len(c.Alerts) == 0) {
		return railBlock{}, false
	}
	meta := ""
	if len(c.Files) > 0 {
		meta = sty.Dim.Render("session · ") + DiffStat(c.Added, c.Removed)
	}
	b := railBlock{heading: railHeading("CHANGES", meta, sty.Dim, width)}
	// The alerts come first and are pinned: they are what the block exists to
	// keep on screen, and the turn that caused one is part of the fact.
	for _, a := range c.Alerts {
		turn := ""
		if a.Turn > 0 {
			turn = sty.Dim.Render(fmt.Sprintf("turn %d", a.Turn))
		}
		b.pin(railRow(" "+sty.Err.Render("✗")+" "+sty.Body.Render(a.Label), turn, width, inspectorIndent))
		if a.Note != "" {
			b.pin(railRow(sty.Dim.Render(a.Note), "", width, inspectorIndent+2))
		}
	}
	for _, f := range c.Files {
		// The changed-file row carries the mutation rail and the edit glyph,
		// so the close of a turn looks like the rows that produced it.
		lead := sty.Accent.Render("▎") + sty.Accent.Render("✎") + " "
		stats := DiffStat(f.Added, f.Removed)
		if f.Turns > 1 {
			// Repeat edits collapsed to one row, so the row says how many
			// turns are behind its counts.
			stats += " " + sty.Dim.Render(fmt.Sprintf("%dt", f.Turns))
		}
		b.rows = append(b.rows, railLine{
			text:    railRow(lead+sty.Body.Render(f.Path), stats, width, inspectorIndent),
			pinned:  f.ThisTurn,
			counted: true,
			added:   f.Added,
			removed: f.Removed,
			// The row already knows the path it is about, so a host answering
			// a pointer never has to read it back out of the styled text —
			// which carries a clipped path and a stats field besides.
			target: RailTarget{Kind: RailTargetFile, Name: f.Path},
		})
	}
	b.fold = func(hidden []railLine) string { return changesFold(hidden, width) }
	return b, true
}

// changesFold is the marker the file list folds behind when the rail is
// shorter than it. It carries its own counts, so the rows it swallowed are
// still accounted for (invariant 4); rows with no counts of their own — a
// truncated alert — fold behind a bare marker rather than a fabricated zero.
func changesFold(hidden []railLine, width int) string {
	var added, removed, counted int
	for _, h := range hidden {
		if !h.counted {
			continue
		}
		counted++
		added += h.added
		removed += h.removed
	}
	left := sty.Hint.Render(fmt.Sprintf("… %d more", len(hidden)))
	if counted == 0 {
		return indentRow(left, width)
	}
	return railRow(left, DiffStat(added, removed), width, inspectorIndent)
}

// agentsBlock is the session map: the orchestrator, then every child in spawn
// order, running or finished, with the row the keyboard is in marked. Every
// other block on the rail answers for the session as a whole, which is what
// makes the mark load-bearing rather than decorative — beside a child's
// transcript it is the only thing saying which session those numbers are
// about (docs/interface/surfaces.md#the-inspector-rail).
//
// A map of one session is not a map, so the host sends nothing where there
// are no children and the block is omitted like any other with nothing to
// say.
func (r InspectorRail) agentsBlock(width int) (railBlock, bool) {
	if len(r.Agents) == 0 {
		return railBlock{}, false
	}
	b := railBlock{heading: railHeading("AGENTS", r.childTally(), sty.Dim, width)}
	shown, folded := r.mappedAgents()
	for _, a := range shown {
		b.rows = append(b.rows, a.railLines(r.Frame, width)...)
	}
	for _, a := range folded {
		b.hidden = append(b.hidden, a.railLines(r.Frame, width)...)
	}
	b.fold = func(hidden []railLine) string { return agentsFold(hidden, width) }
	return b, true
}

// inspectorAgentsSettled is how many finished children the map keeps on
// screen before the rest fold behind their count. The number is what the
// arithmetic allows rather than a taste: a finished child costs two rows —
// its own and the line saying what it found — and the marker costs one, so
// folding only starts saving room at the third. Below that the fold would
// spend a row to hide a row, and above it the block would grow into a log of
// everything the session ever started, which is the agent manager's job and
// not a standing overview's.
const inspectorAgentsSettled = 2

// mappedAgents splits the map into the rows it draws and the rows it folds.
// What folds is the surplus of finished children, earliest first: an outcome
// you have not read yet is the one that just landed, so the budget is spent
// from the newest backwards. A failure folds like anything else, because a
// child that wants something from you is blocked rather than failed, and a
// blocked child is never what folds. The orchestrator never folds because it
// never finishes, and the focused session never folds because the mark on it
// is the reason the rest of the rail can be read at all.
func (r InspectorRail) mappedAgents() (shown, folded []InspectorAgent) {
	drop := make(map[int]bool)
	budget := inspectorAgentsSettled
	for i := len(r.Agents) - 1; i >= 0; i-- {
		a := r.Agents[i]
		if a.Self || a.Focused || !a.State.settled() {
			continue
		}
		if budget > 0 {
			budget--
			continue
		}
		drop[i] = true
	}
	for i, a := range r.Agents {
		if drop[i] {
			folded = append(folded, a)
		} else {
			shown = append(shown, a)
		}
	}
	return shown, folded
}

// childTally is the heading's own sentence: what the children still owe you,
// in the words the fan-out header and the manager's title rail state about
// the same children. The orchestrator is not a child and is left out of it,
// or a session with nothing running would head its map with "1 running".
func (r InspectorRail) childTally() string {
	var states []FanoutState
	for _, a := range r.Agents {
		if !a.Self {
			states = append(states, a.State)
		}
	}
	return stateTally(states)
}

// agentsFold is the marker the map folds behind: a count of sessions, which
// is what the counted rows are.
func agentsFold(hidden []railLine, width int) string {
	n := 0
	for _, h := range hidden {
		if h.counted {
			n++
		}
	}
	if n == 0 {
		// Height truncation can take a session's detail row on its own,
		// leaving hidden rows that belong to sessions still on screen. They
		// are not sessions the marker is hiding, and they are not nothing
		// either, so the marker falls back to counting what it has.
		n = len(hidden)
	}
	return indentRow(sty.Hint.Render(fmt.Sprintf("… %d more", n)), width)
}

// railLines is one session's rows: who it is, how it is and what it has
// spent, and under that what it is doing or what it found.
func (a InspectorAgent) railLines(frame, width int) []railLine {
	// The mark sits in the indent every other row spends on nothing, so a
	// marked row starts in the same column as an unmarked one. It is a mark
	// and not a cursor: the manager's ❯ says where the selection is, and this
	// says where the keyboard is, which are different questions.
	lead := strings.Repeat(" ", inspectorIndent)
	if a.Focused {
		lead = sty.FocusRow.Render("▸") + " "
	}
	// Both of a session's rows point at that session. They are one thing
	// drawn on two lines — the name and what it is doing — and a pointer that
	// answered on the first and not the second would make the target half a
	// row tall for no reason the reader can see.
	target := RailTarget{Kind: RailTargetSession, Name: a.Name}
	if a.Self {
		// The rail's own session has no name to attach to: it is where the
		// keyboard goes back to, which every host spells as no name at all.
		target.Name = ""
	}
	rows := []railLine{{
		text: railRow(lead+AgentProgress{State: a.State}.glyph()+" "+sty.Body.Render(a.Name),
			a.rightField(), width, 0),
		// The orchestrator, the session the keyboard is in and a child
		// waiting on an answer are the rows the map exists to keep on
		// screen; truncation takes them only when nothing else is left.
		pinned: a.Self || a.Focused || a.State == FanoutBlocked,
		// The fold counts sessions, and this is the row that is one.
		counted: true,
		target:  target,
	}}
	if detail := a.detailRow(frame, width); detail != "" {
		rows = append(rows, railLine{text: detail, target: target})
	}
	return rows
}

// rightField is what a session's row reports: the word it ended on where it
// has ended, and the spend, which it has whatever it is doing.
func (a InspectorAgent) rightField() string {
	spend := ""
	if a.Spend != "" {
		spend = sty.Dim.Render(a.Spend)
	}
	if a.Outcome == "" {
		return spend
	}
	word := outcomeStyle(a.State).Render(a.Outcome)
	if spend == "" {
		return word
	}
	return word + "  " + spend
}

// outcomeStyle is the weight the word a session ended on carries: a failure
// is the only one of them that asks anything of the reader.
func outcomeStyle(s FanoutState) lipgloss.Style {
	switch s {
	case FanoutFailed:
		return sty.Err
	case FanoutDone:
		return sty.Add
	}
	return sty.Dim
}

// detailRow is the line under a session. A session that has stopped moving
// gets neither a bar nor a spinner: a bar against a finished child measures
// nothing, and motion beside one is motion where there is none.
func (a InspectorAgent) detailRow(frame, width int) string {
	var parts []string
	switch m, ok := AgentMeter(a.Step, a.Steps); {
	case a.State == FanoutDone || a.State == FanoutFailed:
		if a.Detail != "" {
			parts = append(parts, sty.Dimmer.Render(a.Detail))
		}
	case ok:
		// A declared step count earns a bar; the lane is info whatever
		// the child's health, and states its count beside it.
		parts = append(parts, m.View())
		if a.Detail != "" {
			parts = append(parts, sty.Dimmer.Render(a.Detail))
		}
	case a.Detail == "":
	case a.State != FanoutRunning:
		// Waiting on an answer, waiting for a slot, or waiting to be
		// steered: none of them is running, so none of them gets motion.
		parts = append(parts, sty.Dimmer.Render(a.Detail))
	default:
		// No declared total: motion beside the word naming what is
		// running, never a fabricated ratio.
		parts = append(parts, Spinner{Frame: frame, Label: a.Detail}.View())
	}
	if a.Tools > 0 {
		parts = append(parts, sty.Dimmer.Render(plural(a.Tools, "tool")))
	}
	if len(parts) == 0 {
		return ""
	}
	return railRow(strings.Join(parts, sty.Dimmer.Render(" · ")), "", width, inspectorIndent+2)
}

// toolsBlock is the TOOLS block. It sits under AGENTS because the two answer
// the same question about the session's machinery — who else is working, and
// what this session can reach — and above CONTEXT because those are what the
// work is costing rather than what it is made of.
func (r InspectorRail) toolsBlock(width int) (railBlock, bool) {
	t := r.Tools
	if t == nil || (len(t.Sources) == 0 && t.MemoryOmitted == 0) {
		return railBlock{}, false
	}
	// The ratio is over every source the session has, and a session with none
	// gets no ratio at all: "0 of 0 up" is a fabricated zero, and the block is
	// on screen for the memory row underneath.
	meta := ""
	if total := len(t.Sources) + t.More; total > 0 {
		meta = fmt.Sprintf("%d of %d up", t.Up, total)
	}
	b := railBlock{heading: railHeading("TOOLS", meta, sty.Dim, width)}
	for _, s := range t.Sources {
		glyph, word, style := toolSourceTone(s.State)
		right := style.Render(word)
		if s.Note != "" {
			right += sty.Dim.Render(" · " + s.Note)
		}
		b.add(railRow(glyph+" "+sty.Body.Render(s.Name), right, width, inspectorIndent))
	}
	if t.More > 0 {
		b.add(indentRow(sty.Dim.Render(fmt.Sprintf("… %d more", t.More)), width))
	}
	if t.MemoryOmitted > 0 {
		// A source row in everything but name: the memory the prompt carries
		// is a place this session's knowledge came from, and this says how
		// much of it did not arrive. The mark is the one the rail already
		// uses for something only a person can move — the way out is to
		// shorten an entry.
		b.add(railRow(sty.Accent.Render("⚠")+" "+sty.Body.Render("memory"),
			sty.Dim.Render(fmt.Sprintf("%d did not fit", t.MemoryOmitted)),
			width, inspectorIndent))
	}
	return b, true
}

// ToolSourceWord is a source's state in the one word the block's glyph stands
// for, so a surface that prints the block in words rather than drawing it says
// the same thing.
func ToolSourceWord(s ToolSourceState) string {
	_, word, _ := toolSourceTone(s)
	return word
}

// toolSourceTone is a source's glyph, its word and the weight the word
// carries. The glyph is the distinction a monochrome terminal reads: the
// same four marks every other surface uses for done, waiting on a person,
// left out and failed.
func toolSourceTone(s ToolSourceState) (string, string, lipgloss.Style) {
	switch s {
	case ToolSourceUp:
		return sty.Add.Render("✓"), "up", sty.Dim
	case ToolSourceBlocked:
		return sty.Accent.Render("⚠"), "blocked", sty.Body
	case ToolSourceOff:
		return sty.Dim.Render("⊘"), "off", sty.Dim
	}
	return sty.Err.Render("✗"), "error", sty.Body
}

func (r InspectorRail) contextBlock(width int) (railBlock, bool) {
	c := r.Context
	if c == nil || c.Window <= 0 {
		return railBlock{}, false
	}
	pct := min(max(c.Pct, 0), 100)
	meter := Meter{Pct: pct, Cells: railCells(MeterCellsRail, width), Tone: MeterPressure,
		Warn: c.WarnPct, Alert: c.AlertPct}
	style := meter.Style()
	b := railBlock{heading: railHeading("CONTEXT",
		style.Render(fmt.Sprintf("%d%% of %s", pct, formatTokens(c.Window))), style, width)}
	count := formatTokens(c.Tokens)
	if c.Estimated {
		count = "~" + count
	}
	// The bar's number is the token count at the rail's right edge, in the
	// meter's own colour — the bar never carries the value alone.
	b.add(railRow(meter.Bar(), style.Render(count), width, inspectorIndent))
	tokens := strings.TrimSpace(c.Tokens1 + " " + c.Tokens2)
	lead := ""
	switch {
	case len(c.Burn) > 0:
		lead = Sparkline{Values: c.Burn, Cells: railCells(SparkCells, width)}.View() + " " + sty.Dim.Render("per round")
	case c.Estimated:
		// No series yet and no reported size: the block still has to say
		// where its number came from.
		lead = sty.Dim.Render("estimated")
	}
	if lead != "" || tokens != "" {
		b.add(railRow(lead, sty.Dim.Render(tokens), width, inspectorIndent))
	}
	return b, true
}

func (r InspectorRail) spendBlock(width int) (railBlock, bool) {
	s := r.Spend
	if s == nil || (s.Turn == "" && s.Main == "" && s.Session == "") {
		return railBlock{}, false
	}
	b := railBlock{heading: railHeading("SPEND", sty.Body.Render(s.Turn), sty.Body, width)}
	var split []string
	if s.Model != "" {
		split = append(split, s.Model)
	}
	if s.Main != "" {
		split = append(split, s.Main+" main")
	}
	if s.Children != "" {
		split = append(split, s.Children+" ◇")
	}
	if len(split) > 0 {
		b.add(railRow(sty.Dim.Render(strings.Join(split, " · ")), "", width, inspectorIndent))
	}
	if s.Session != "" {
		b.add(railRow(sty.Dim.Render("session total "+s.Session), "", width, inspectorIndent))
	}
	return b, true
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
	left = clip(left, max(room, 0))
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
