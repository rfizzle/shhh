package chat

// Step outline (docs/interface/surfaces.md#the-step): consecutive tool
// calls are grouped into numbered steps, each drawn as one card (card.go),
// so a forty-tool turn reads as an outline instead of a scrolling feed. The
// grouping is a layer over the entry list —
// the agent already emits ordered tool results, and inventing a step protocol
// on the wire would couple every provider to the UI. Plan mode is the
// one place a step list is authoritative: once a plan is approved its
// steps are the transcript's steps — numbered as the plan numbered them,
// including the ones not started — and work the plan never named is marked as
// off it rather than renumbered into it. Without a plan every step is
// inferred from the assistant prose immediately preceding a batch of calls,
// and a run of calls no prose titled is a card too, with no body, that the
// outline does not number.

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/rfizzle/shhh/internal/plan"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// stepState is a step's state. It follows its rows: running while any
// call is in flight, failed once one broke, done otherwise. Queued is the
// declared-but-not-started state a plan's steps arrive in.
type stepState int

const (
	stepQueued  stepState = iota // · declared, not started; duration —
	stepRunning                  // ▸ a call is in flight
	stepDone                     // ✓ finished with nothing to report
	stepFailed                   // ✗ finished, contained a failure
)

// foldState is your answer to how much of a card is drawn, over the
// density rung's. It lives on the entry the card is kept on, so cards hold no
// layout state and re-render from stored raw entries on resize (card.go).
type foldState int

const (
	foldAuto   foldState = iota // as the density rung draws it; never folded by finishing
	foldOpen                    // you opened it onto its calls
	foldClosed                  // you folded it to its header
	// foldSearch is a fold the transcript search opened to reach a match it
	// had counted behind it (search.go). It draws exactly like foldOpen and
	// is a value of its own for one reason: clearing the query puts it back,
	// and a card the reader opened themselves stays open. Nothing but the
	// search writes it, so "who opened this" is answered by the entry rather
	// than by a list somebody has to keep in step with the entries.
	foldSearch
	// foldCard is the card itself — header, body and footer — where the
	// rung would draw its header alone: the reader unfolded a card at low.
	foldCard
)

// stepOrdinalWidth keeps titles on one column for the first 99 steps.
const stepOrdinalWidth = 2

// stepGroup is one titled run of consecutive activity entries: the assistant
// entry at titleIdx heads it, and members [start,end) are the calls it made.
// A step an approved plan declared but the run has not reached yet has no
// entries at all — start == end and titleIdx is stepNoTitle — and renders as
// its header alone.
type stepGroup struct {
	ordinal  int
	titleIdx int
	title    string
	start    int
	end      int
	// offPlan marks a step the running plan never declared. It carries no
	// ordinal, because the numbers belong to the plan.
	offPlan bool
}

// stepNoTitle is the titleIdx of a declared step no entry heads.
const stepNoTitle = -1

// queued reports whether the group is a declared step that has not started.
func (g *stepGroup) queued() bool { return g.end <= g.start }

// transcriptBlock is one renderable unit of history: a step, or a lone entry
// that belongs to no step. Blocks tile the transcript in order.
type transcriptBlock struct {
	start int
	end   int
	step  *stepGroup
	// last marks the block a turn can still add to. It is usually the final
	// one, and it is not whenever notices stand after it: a notice is
	// provisional (stepBlocks), so the block still open is the one under it.
	last bool
}

// members is the range of entries a block renders as activity rows — a step's
// calls, or the lone entry itself.
func (b transcriptBlock) members() (int, int) {
	if b.step != nil {
		return b.step.start, b.step.end
	}
	return b.start, b.end
}

// holds reports whether idx is one of the entries this block renders — its
// own range, the rows a step groups, or the entry that titles it. It is what
// the gutter's cache asks before it reuses a block: the unit under the
// reading cursor renders differently from the same unit anywhere else
// (focus.go), so the block holding the cursor is the one block that cannot
// be taken from a cache built with no cursor in it.
func (b transcriptBlock) holds(idx int) bool {
	if idx < 0 {
		return false
	}
	if idx >= b.start && idx < b.end {
		return true
	}
	if b.step == nil {
		return false
	}
	if idx == b.step.titleIdx {
		return true
	}
	start, end := b.members()
	return idx >= start && idx < end
}

// isActivityEntry reports whether an entry is one of the calls a step groups.
func isActivityEntry(e entry) bool {
	switch e.kind {
	case entryTool, entryCommand, entryDiff:
		return true
	}
	return false
}

// isStepMember reports whether an entry belongs inside a step. One-line
// notices (an approval, an auto-allow) are part of the batch they sit in;
// anything that reads as a standalone block ends the step.
//
// A think row is a member, not a stop. The model thinking between two rounds
// of the same step is still that step's work, so the calls after it stay
// under the title it announced, and folding the step folds the thought with
// them. The header's count is its calls and does not count the thought: it
// counts acts, and a thought ran, read and changed nothing
// (docs/interface/surfaces.md#the-step). A think row trailing the step's last
// call is trimmed off like a notice, and taken back when the next call lands.
func isStepMember(e entry) bool {
	return isActivityEntry(e) || (!entryIsBlock(e) && e.kind != entryAssistant)
}

// callRun measures the run of consecutive calls starting at i, which is the
// block a batch of calls nothing titled renders as: one card, however many
// calls and of whatever kind (card.go). It is at least one row: everything
// else on the transcript stands alone.
//
// Only calls, because a call can never title a step — a title is assistant
// prose — so a run can be taken here without a second look at what the scan
// above it already claimed. A reading that lands straight after the run is
// taken into it as well, and ends it: where the model said nothing about the
// calls, the reading's sentence is what the card has for a body
// (docs/interface/surfaces.md#the-step).
func callRun(es []entry, i int) int {
	// A call that was refused is a card of its own: it is a call a step
	// asked for and nothing it would have done happened, so a card counting
	// what ran around it would read as its answer.
	// See docs/interface/departures.md#a-run-nothing-titled-and-a-refused-call-are-cards.
	if !isActivityEntry(es[i]) || refusedCall(es[i]) {
		return 1
	}
	n := 0
	for i+n < len(es) && isActivityEntry(es[i+n]) && !refusedCall(es[i+n]) {
		n++
	}
	if i+n < len(es) && es[i+n].kind == entrySummary {
		n++
	}
	return n
}

// refusedCall reports whether an entry is a call that never ran: a person
// or a rule refused it, the queue skipped it, or the reader cancelled it.
func refusedCall(e entry) bool {
	return e.kind == entryTool && (e.deniedBy != "" || e.skipped != "" || e.toolResult == cancelledToolResult)
}

// stepTitle reports the title an assistant entry offers a step, if any: one
// line of prose, however long. The card has a body to put the whole of it in
// (docs/interface/surfaces.md#the-step), so a long sentence titles its step
// rather than standing on its own above it; prose of several lines is a
// passage, keeps its own block, and the calls after it are a card nothing
// titled.
func stepTitle(e entry) (string, bool) {
	if e.kind != entryAssistant {
		return "", false
	}
	t := strings.TrimSpace(e.text)
	if t == "" || strings.Contains(t, "\n") {
		return "", false
	}
	// A progress checkpoint is the session's own note, bounded and folded
	// where it stands (docs/interface/surfaces.md#the-progress-checkpoint),
	// so one longer than a line keeps its own block rather than becoming a
	// card's body; one short enough to title the calls after it does.
	if e.checkpoint && len([]rune(t)) > checkpointTitleMaxRunes {
		return "", false
	}
	return t, true
}

// checkpointTitleMaxRunes is the longest checkpoint that titles a step: one
// line at the body column of a wide pane. Past it the note is the bounded
// block the checkpoint is drawn as.
const checkpointTitleMaxRunes = 120

// stepBlocks tiles the entries into blocks: a step wherever a one-line
// assistant title is followed by at least one call, a lone entry everywhere
// else. The scan is left to right, so a block a call has landed after can
// never change — which is what lets renderHistory keep caching. A notice is
// the one entry that can land after a block and still be taken back into it,
// and `last` below is where that is accounted for.
//
// declared is the approved plan's step list, or nil. With one, a group takes
// the number stamped on its title entry rather than the running count, the
// steps nobody has reached are appended as headers with no rows, and a group
// the plan never declared is marked off it.
func stepBlocks(es []entry, declared []plan.Step) []transcriptBlock {
	var blocks []transcriptBlock
	ordinal := 0
	claimed := map[int]bool{}
	for i := 0; i < len(es); {
		if title, ok := stepTitle(es[i]); ok {
			j := i + 1
			for j < len(es) && isStepMember(es[j]) {
				j++
			}
			// Trailing notices belong to whatever comes next, not to this
			// step: a step ends on its last call.
			for j > i+1 && !isActivityEntry(es[j-1]) {
				j--
			}
			if j > i+1 {
				g := &stepGroup{titleIdx: i, title: title, start: i + 1, end: j}
				switch n := es[i].planStep; {
				case n > 0:
					// The plan named this step, so the plan numbers and titles
					// it: the outline mirrors the list that was approved.
					g.ordinal = n
					claimed[n] = true
					if s, ok := stepByNumber(declared, n); ok {
						g.title = s.Title
					}
				case n == offPlanStep:
					g.offPlan = true
				default:
					ordinal++
					g.ordinal = ordinal
				}
				blocks = append(blocks, transcriptBlock{start: i, end: j, step: g})
				i = j
				continue
			}
		}
		// A run of consecutive calls no prose titled is one block rather than
		// one block each, because a card is a property of a run of calls and
		// not of the outline above it: a turn that reads thirty files before
		// it has anything to say about them is the deepest burial there is,
		// and it is exactly the turn with no step to stand them under
		// (card.go).
		run := callRun(es, i)
		blocks = append(blocks, transcriptBlock{start: i, end: i + run})
		i += run
	}
	// The last block a turn can still add to is the last one with entries in
	// it; a declared step nobody has started is not somewhere rows can land,
	// and neither is a trailing notice. A notice is trimmed off the step
	// above rather than given a step of its own, so the next batch of calls
	// to land takes it and the step's next rows back into that step — which
	// makes the step, not the notice standing after it, the block still open.
	// Everything before this one is frozen and may never change again
	// (render.go, focus.go), and a step frozen while a notice stood after it
	// drew its whole block a second time when the batch arrived: one header,
	// one ordinal, twice.
	tail := len(es)
	for tail > 0 && isStepMember(es[tail-1]) && !isActivityEntry(es[tail-1]) {
		tail--
	}
	for k := len(blocks) - 1; k >= 0; k-- {
		if blocks[k].end > blocks[k].start && blocks[k].start < tail {
			blocks[k].last = true
			break
		}
	}
	for _, s := range declared {
		if claimed[s.Number] {
			continue
		}
		blocks = append(blocks, transcriptBlock{start: len(es), end: len(es), step: &stepGroup{
			ordinal: s.Number, titleIdx: stepNoTitle, title: s.Title,
			start: len(es), end: len(es),
		}})
	}
	return blocks
}

// stepByNumber finds a declared step by the number the plan gave it — which
// is the model's own numbering, not an index (internal/plan).
func stepByNumber(declared []plan.Step, n int) (plan.Step, bool) {
	for _, s := range declared {
		if s.Number == n {
			return s, true
		}
	}
	return plan.Step{}, false
}

// stepBlockAt returns the card kept on the entry at idx: the step it titles,
// or the run of calls it begins where nothing titled them (card.go).
func (m Model) stepBlockAt(es []entry, idx int) (transcriptBlock, bool) {
	return m.cardBlockAt(es, idx)
}

// stepStats reads a step's state, tool count and duration off its rows. The
// duration is the sum of what the calls took — entries carry no wall clock,
// and the sum is the honest number for "how long this step cost".
func (m Model) stepStats(g *stepGroup, es []entry) (state stepState, tools int, d time.Duration) {
	if g.end <= g.start {
		return stepQueued, 0, 0
	}
	var running, failed bool
	for _, e := range es[g.start:g.end] {
		if !isActivityEntry(e) {
			continue
		}
		tools++
		d += e.duration
		if e.kind == entryDiff {
			continue
		}
		row := m.activityRowFor(e)
		switch {
		case row.State == components.ActivityRunning:
			running = true
		case row.Failed():
			failed = true
		}
	}
	switch {
	case running:
		return stepRunning, tools, d
	case failed:
		return stepFailed, tools, d
	}
	return stepDone, tools, d
}

// toggleStepFold flips a card between its header alone and the card,
// recording the choice on the entry the card is kept on (card.go).
func (m *Model) toggleStepFold(idx int) {
	m.toggleCardFold(idx)
}

// stepHeader is what a step's outline states about it: its ordinal and
// title, how it stands, its calls and what they took, and whether it is
// drawn as its header alone. The transcript draws a step that has begun as a
// card (card.go); this header's own line is drawn for a step an approved
// plan declared and the run has not reached, which has no calls to make a
// card of, and the plan's checklist and the rail read its state.
type stepHeader struct {
	Ordinal  int
	Title    string
	State    stepState
	Tools    int
	Duration time.Duration
	Folded   bool
	// Detail marks a step you opened the detail of yourself.
	// It is your answer, not the resolved state: at high verbosity every step
	// is open, and a word repeated on every header says nothing about any of
	// them. What the marker is for is the one step that is taller than the
	// setting would have made it.
	Detail bool
	// OffPlan marks a step the running plan never declared: it takes the
	// ordinal column's width but not a number, because the numbers are the
	// plan's.
	OffPlan bool
	// Matches is how many occurrences of a live transcript search sit behind
	// this header's fold (search.go). A fold states what it swallowed
	// (invariant 4), and while a search is up what it swallowed includes
	// answers to the question the reader is asking.
	Matches int
	// OutOfWindow marks a step a compaction folded into a summary: the rows
	// are still here and still searchable, and the model no longer remembers
	// them firsthand (context.go). The header says so in words, because a
	// step that read as current would have the reader asking the model about
	// work it can no longer see.
	OutOfWindow bool
}

// tones are the header's per-state colors, following the design system's
// StepGroup component: the pointer and the duration go spin while the step
// runs and the title brightens with it, a queued step is dim throughout, and
// a finished step is ordinary body text under a faint rule.
func (h stepHeader) tones() (ptr, title, dur lipgloss.Style) {
	switch {
	case h.OutOfWindow:
		// Past work, and the title goes with the rest of the line: the eye
		// running up a transcript should find where the window starts
		// without reading a word (context.go).
		return sty.Step.Dim, sty.Step.Dim, sty.Step.Dim
	case h.State == stepRunning:
		return sty.Step.Run, sty.Step.LiveTitle, sty.Step.Run
	case h.State == stepQueued:
		return sty.Step.Dim, sty.Step.Dim, sty.Step.Dim
	}
	return sty.Step.Dim, sty.Step.Title, sty.Step.Stats
}

// glyph is the state glyph and its color.
func (h stepHeader) glyph() string {
	switch h.State {
	case stepRunning:
		return sty.Step.Run.Render("▸")
	case stepFailed:
		return sty.Step.Fail.Render("✗")
	case stepQueued:
		return sty.Step.Dim.Render("·")
	}
	return sty.Step.Done.Render("✓")
}

// countLabel names what the step holds, in words, so the glyph never carries
// the state alone (invariant 1).
func (h stepHeader) countLabel() string {
	var label string
	switch {
	case h.State == stepQueued:
		return components.OutcomeQueued
	case h.Tools == 1:
		label = "1 tool"
	default:
		label = fmt.Sprintf("%d tools", h.Tools)
	}
	if h.Detail {
		// What is open is said in a word rather than left to the reader to
		// infer from how tall the step got (invariant 1).
		label += " · detail"
	}
	if h.Matches > 0 {
		label += " · " + matchesInside(h.Matches)
	}
	if h.OutOfWindow {
		// Last, because it is the standing fact about the step rather than
		// something that happened in it, and a reader scanning the column
		// reads the counts first.
		label += " · " + outOfWindowLabel
	}
	return label
}

// outOfWindowLabel is what a step a compaction folded away says about itself.
// The words carry it and nothing else does (invariant 1): the rows are still
// on the transcript, still searchable and still openable, so the only thing
// that has changed is what the model can be asked about
// (docs/interface/principles.md#fold-never-hide).
const outOfWindowLabel = "out of the window"

// durationText is the header's duration: blank under 0.5s like every other
// row, — for a step that never ran.
func (h stepHeader) durationText() string {
	if h.State == stepQueued {
		return components.NoDuration
	}
	return activityDuration(h.Duration)
}

// View renders the header at the given width, on the column grid: the title
// starts in the verb column and the duration is the same right-aligned
// 6-column field the rows use, behind the same reserved gap, so the outline
// and the feed share one edge and a full duration never abuts the count.
func (h stepHeader) View(width int) string {
	fold := "▾"
	switch {
	case h.State == stepQueued:
		fold = "·"
	case h.Folded:
		fold = "▸"
	}
	ord := strconv.Itoa(h.Ordinal)
	if h.OffPlan {
		// Off the plan: the eye still finds the column, and finds no number
		// there, which is exactly what happened.
		ord = "+"
	}
	if len(ord) < stepOrdinalWidth {
		ord += strings.Repeat(" ", stepOrdinalWidth-len(ord))
	}
	ptrStyle, titleStyle, durStyle := h.tones()
	leadW := components.GridPointerWidth + len(ord) + 1
	lead := ptrStyle.Render(fold) + " " + titleStyle.Render(ord) + " "

	label := h.countLabel()
	stats := h.glyph() + " " + sty.Step.Stats.Render(label)
	statsW := lipgloss.Width(label) + 2
	// The rule takes what the title leaves; the title clips before the rule
	// disappears, because the stats are the reason to read the header.
	fixed := leadW + statsW + components.GridDurationGap + components.GridDurationWidth + 3
	title := clipRow(h.Title, width-fixed)
	rule := width - leadW - lipgloss.Width(title) - statsW - components.GridDurationGap - components.GridDurationWidth - 2
	if rule < 1 {
		rule = 1
	}
	line := lead + titleStyle.Render(title) + " " +
		sty.Step.Rule.Render(strings.Repeat("─", rule)) + " " +
		stats + strings.Repeat(" ", components.GridDurationGap) +
		stepDurationField(h.durationText(), durStyle)
	return strings.TrimRight(line, " ")
}

// stepDurationField right-aligns the duration in the grid's 6 columns; the
// field is reserved even when blank so headers and rows line up.
func stepDurationField(d string, style lipgloss.Style) string {
	w := components.GridDurationWidth
	if d == "" {
		return strings.Repeat(" ", w)
	}
	d = clipRow(d, w)
	return strings.Repeat(" ", w-lipgloss.Width(d)) + style.Render(d)
}

// stepStateFor is the state a step's header draws, and it is separate from
// the header because knowing whether a step is folded is a much cheaper
// question than building one: a header counts what a live search has found
// behind its fold, and the walk that count is for has to ask which steps are
// folded before it can ask anything else (search.go).
//
// The live step — the last block while the turn is still working — is running
// even though every row in it has landed: a call joins the transcript only
// once it finishes, so the rows alone never say "busy".
func (m Model) stepStateFor(blk transcriptBlock, es []entry) stepState {
	state, _, _ := m.stepStats(blk.step, es)
	if state == stepDone && blk.last && m.turnState() != stateInput {
		return stepRunning
	}
	return state
}

// stepHeaderOnly reports whether a step's card is drawn as its header alone:
// the reader folded it, or the rung draws headers only. A declared step
// nobody has started is its header and nothing else, and is not folded.
func (m Model) stepHeaderOnly(blk transcriptBlock, es []entry) bool {
	if blk.step != nil && blk.step.queued() {
		return false
	}
	d, folded := m.cardShape(blk, es)
	return folded || d == components.CardLow
}

// headerFor builds the header for a step from its rows.
func (m Model) headerFor(blk transcriptBlock, es []entry) stepHeader {
	g := blk.step
	state := m.stepStateFor(blk, es)
	_, tools, d := m.stepStats(g, es)
	h := stepHeader{
		Ordinal:  g.ordinal,
		Title:    g.title,
		State:    state,
		Tools:    tools,
		Duration: d,
		Folded:   m.stepHeaderOnly(blk, es),
		Detail:   g.titleIdx != stepNoTitle && es[g.titleIdx].detailFold == foldOpen,
		OffPlan:  g.offPlan,
		// Read off the rows rather than off the title, so a step whose title
		// entry a plan supplied still says which side of the window it is on.
		OutOfWindow: g.end > g.start && es[g.start].outOfWindow,
	}
	if m.cardHidesRows(blk, es) {
		h.Matches = m.searchMatchesIn(es, g.start, g.end)
	}
	return h
}

// unit is one addressable piece of rendered history: a step header, or a
// single entry's block. Focus mode selects units, so the plain, focus and
// attached renderers all walk the same list.
type unit struct {
	// idx is the transcript index the unit is anchored to — the entry it
	// renders, or the entry that titles the step it heads.
	idx int
	// sepBefore and sepAfter decide spacing on each side (separatorBefore).
	// They differ for a header: it takes a block's air above and a feed row's
	// tightness below, so its rows sit directly under it.
	sepBefore entry
	sepAfter  entry
	text      string
	// cardHead marks the lines a card draws itself, as against the rows of
	// its calls. A run nothing titled is kept on its first call, so open,
	// the card's own lines and that call's row share idx, and a click has
	// to say which of the two it landed on (click.go).
	cardHead bool
	// group marks an open card's group line, kept on the group's first
	// call, and strip its strip, kept on the card's anchor: each shares its
	// idx with a row of its own, and a click has to say which it landed on
	// (cardopen.go).
	group, strip bool
	// shadow marks a unit that shares its idx with the unit the reading
	// cursor stands on for that entry and is not it: an open card's own
	// lines where nothing titled it, its strip, a call row under the line
	// of its group. The cursor's line and a row's place are read off the
	// other one.
	shadow bool
}

// blockUnits renders one block. In focus mode selectable units carry the
// gutter, with the pointer on the selected one. A unit already on the grid
// keeps its width and its columns — the cursor goes in the column it already
// holds back — and one that is not renders two columns narrower to make room
// for it (gutterPrefix).
func (m Model) blockUnits(blk transcriptBlock, es []entry, width int, focus bool, focusIdx int) []unit {
	if isCardBlock(blk, es) {
		return m.cardUnits(blk, es, width, focus, focusIdx)
	}
	var units []unit
	if blk.step != nil {
		// A declared step nobody has started is the plan's outline row and
		// nothing else: no calls to make a card of, so nothing for focus mode
		// to select either.
		header := m.headerFor(blk, es)
		return append(units, unit{idx: blk.step.titleIdx, sepBefore: entry{kind: entryAssistant},
			sepAfter: entry{kind: entryTool}, text: header.View(width) + "\n"})
	}
	for i := blk.start; i < blk.end; i++ {
		e := es[i]
		text, selectable, grid := m.entryUnitText(i, es, width, focus, focusIdx, false)
		if text == "" {
			continue
		}
		if focus && selectable {
			text = gutterPrefix(text, i == focusIdx, grid, gutterWidth(width, grid))
		}
		units = append(units, unit{idx: i, sepBefore: e, sepAfter: e, text: text})
	}
	return units
}

// entryUnitText is one entry's own unit, drawn at the width its unit takes
// and dressed for the cursor's three states, before the gutter is added.
func (m Model) entryUnitText(i int, es []entry, width int, focus bool, focusIdx int, detail bool) (text string, selectable, grid bool) {
	e := es[i]
	// A row's own offers are live only under reading mode's cursor. Under
	// the pointer lit from the prompt every letter is text, so the row
	// draws them grey beside the handover that reaches it, and every other
	// row beside the key that hands the keyboard to the transcript.
	sel := rowUnselected
	if focus && i == focusIdx {
		sel = rowPointed
		if m.state == stateFocus {
			sel = rowUnderCursor
		}
	}
	return m.renderEntryDetail(e, m.unitWidth(e, width, focus), sel, detail), m.selectableRow(e), onGrid(e)
}

// cardUnits renders a card. A card is one unit — the reading cursor stops
// on it as a whole — until it is open, when its calls are rows of their own
// on the card's band so the cursor can stand on each.
func (m Model) cardUnits(blk transcriptBlock, es []entry, width int, focus bool, focusIdx int) []unit {
	anchor := cardAnchor(blk)
	// A run nothing titled is kept on its first call, which is a row of its own
	// once the card is open: the cursor there is on the row, not the card.
	_, onStrip := m.stripCallOn(anchor)
	onCard := focus && focusIdx == anchor && (blk.step != nil || !m.cardOpen(blk, es)) && !onStrip
	card := m.stepCardFor(blk, es, width, onCard)
	// One blank either side of a card, and between two cards one blank
	// rather than two: the separator rule reads the card as a block
	// (separatorBefore).
	block := entry{kind: entryAssistant}
	if card.Folded || card.Density != components.CardHigh {
		return []unit{{idx: anchor, sepBefore: block, sepAfter: block, text: card.View(width) + "\n", cardHead: true}}
	}
	return m.openCardUnits(blk, es, width, focus, focusIdx, card)
}

// unitWidth is the width an entry's own unit is rendered at in a pane of
// width: the pane's, less the cursor's two columns where reading mode's
// gutter takes them from a selectable row that is not on the grid.
func (m Model) unitWidth(e entry, width int, focus bool) int {
	if focus && m.selectableRow(e) {
		return gutterWidth(width, onGrid(e))
	}
	return width
}

// gutterWidth is what a selectable unit renders at once reading mode's cursor
// is on the list. A unit on the grid renders at the pane's full width: its
// first two columns are the cursor's already, so nothing has to move to make
// room. One that is not gives up those two columns to it.
func gutterWidth(width int, grid bool) int {
	if grid {
		return width
	}
	return width - components.GridPointerWidth
}

// transcriptUnits renders every block of a transcript in order.
func (m Model) transcriptUnits(es []entry, width int, focus bool, focusIdx int) []unit {
	var units []unit
	for _, blk := range m.blocksOf(es) {
		units = append(units, m.blockUnits(blk, es, width, focus, focusIdx)...)
	}
	return units
}

// joinUnits concatenates units with the spacing rhythm of separatorBefore,
// continuing from prev when the caller has already emitted something.
func joinUnits(units []unit, prev entry, havePrev bool) (string, entry, bool) {
	var b strings.Builder
	for _, u := range units {
		if havePrev {
			b.WriteString(separatorBefore(prev, u.sepBefore))
		}
		b.WriteString(u.text)
		prev, havePrev = u.sepAfter, true
	}
	return b.String(), prev, havePrev
}
