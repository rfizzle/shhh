package chat

// Step outline (docs/interface/surfaces.md#the-step): consecutive tool
// calls fold under numbered steps, so a forty-tool turn reads as an outline
// instead of a scrolling feed. The grouping is a layer over the entry list —
// the agent already emits ordered tool results, and inventing a step protocol
// on the wire would couple every provider to the UI. Plan mode is the
// one place a step list is authoritative: once a plan is approved its
// steps are the transcript's steps — numbered as the plan numbered them,
// including the ones not started — and work the plan never named is marked as
// off it rather than renumbered into it. Without a plan every step is
// inferred from the assistant prose immediately preceding a batch of calls,
// and a turn with no discernible steps renders exactly as it did before — a
// flat list, no empty group chrome.

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

// foldState is your override of a step's automatic folding. It lives on the
// entry that titles the step, so steps themselves hold no layout state and
// re-render from stored raw entries on resize.
type foldState int

const (
	foldAuto   foldState = iota // open while running or broken, folded once done
	foldOpen                    // you unfolded it
	foldClosed                  // you folded it
)

// stepTitleMaxRunes bounds what counts as a title: one short line of prose.
// Longer prose is an explanation, not a heading, and keeps its own block —
// the outline titles a step, it never swallows text (invariant 4).
const stepTitleMaxRunes = 120

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
	// last marks the final block, the only one a turn can still add to.
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
func isStepMember(e entry) bool {
	return isActivityEntry(e) || (!entryIsBlock(e) && e.kind != entryAssistant)
}

// stepTitle reports the title an assistant entry offers a step, if any.
func stepTitle(e entry) (string, bool) {
	if e.kind != entryAssistant {
		return "", false
	}
	t := strings.TrimSpace(e.text)
	if t == "" || strings.Contains(t, "\n") || len([]rune(t)) > stepTitleMaxRunes {
		return "", false
	}
	return t, true
}

// stepBlocks tiles the entries into blocks: a step wherever a one-line
// assistant title is followed by at least one call, a lone entry everywhere
// else. The scan is left to right, so a block that already has a successor
// can never change — which is what lets renderHistory keep caching.
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
		blocks = append(blocks, transcriptBlock{start: i, end: i + 1})
		i++
	}
	// The last block a turn can still add to is the last one with entries in
	// it; a declared step nobody has started is not somewhere rows can land.
	for k := len(blocks) - 1; k >= 0; k-- {
		if blocks[k].end > blocks[k].start {
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

// stepBlockAt returns the block whose step is titled by the entry at idx.
func (m Model) stepBlockAt(es []entry, idx int) (transcriptBlock, bool) {
	for _, blk := range m.blocksOf(es) {
		if blk.step != nil && blk.step.titleIdx == idx {
			return blk, true
		}
	}
	return transcriptBlock{}, false
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

// stepFolded decides whether a step shows only its header. A step is open
// while it runs and collapses when it finishes — except one that contains a
// failure, because a failure you have to scroll to find is a failure you will
// miss. Your own fold overrides both.
func (m Model) stepFolded(g *stepGroup, es []entry, state stepState) bool {
	if g.titleIdx == stepNoTitle {
		// A declared step nobody has started has no rows to fold and no entry
		// to record a fold on.
		return false
	}
	switch es[g.titleIdx].stepFold {
	case foldOpen:
		return false
	case foldClosed:
		return true
	}
	switch m.verbosity {
	case verbosityHigh:
		return false
	case verbosityLow:
		// Headers only: at low every step is folded, a broken one
		// included — you asked for the outline, and the ✗ is on the header.
		return true
	}
	return state == stepDone
}

// toggleStepFold flips a step between folded and open, recording the choice
// on the entry that titles it.
func (m *Model) toggleStepFold(idx int) {
	es := *m.entries()
	blk, ok := m.stepBlockAt(es, idx)
	if !ok {
		return
	}
	if m.headerFor(blk, es).Folded {
		es[idx].stepFold = foldOpen
	} else {
		es[idx].stepFold = foldClosed
	}
}

// stepHeader is one step's line: fold state, ordinal, title, a faint rule
// stretching to the stats, state glyph, tool count and duration.
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
}

// tones are the header's per-state colors, following the design system's
// StepGroup component: the pointer and the duration go spin while the step
// runs and the title brightens with it, a queued step is dim throughout, and
// a finished step is ordinary body text under a faint rule.
func (h stepHeader) tones() (ptr, title, dur lipgloss.Style) {
	switch h.State {
	case stepRunning:
		return sty.Step.Run, sty.Step.LiveTitle, sty.Step.Run
	case stepQueued:
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
	return label
}

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
// 6-column field the rows use, so the outline and the feed share one edge.
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
	fixed := leadW + statsW + components.GridDurationWidth + 3
	title := clipRow(h.Title, width-fixed)
	rule := width - leadW - lipgloss.Width(title) - statsW - components.GridDurationWidth - 2
	if rule < 1 {
		rule = 1
	}
	line := lead + titleStyle.Render(title) + " " +
		sty.Step.Rule.Render(strings.Repeat("─", rule)) + " " +
		stats + stepDurationField(h.durationText(), durStyle)
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

// headerFor builds the header for a step from its rows.
func (m Model) headerFor(blk transcriptBlock, es []entry) stepHeader {
	g := blk.step
	state, tools, d := m.stepStats(g, es)
	// The live step — the last block while the turn is still working — is
	// running even though every row in it has landed: a call joins the
	// transcript only once it finishes, so the rows alone never say "busy".
	if state == stepDone && blk.last && m.turnState() != stateInput {
		state = stepRunning
	}
	return stepHeader{
		Ordinal:  g.ordinal,
		Title:    g.title,
		State:    state,
		Tools:    tools,
		Duration: d,
		Folded:   m.stepFolded(g, es, state),
		Detail:   g.titleIdx != stepNoTitle && es[g.titleIdx].detailFold == foldOpen,
		OffPlan:  g.offPlan,
	}
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
}

// blockUnits renders one block. In focus mode selectable units render two
// columns narrower and carry the gutter, with the pointer on the selected
// one.
func (m Model) blockUnits(blk transcriptBlock, es []entry, width int, focus bool, focusIdx int) []unit {
	var units []unit
	add := func(idx int, sepBefore, sepAfter entry, text string, selectable bool) {
		if text == "" {
			return
		}
		if focus && selectable {
			text = gutterPrefix(text, idx == focusIdx, width-components.GridPointerWidth)
		}
		units = append(units, unit{idx: idx, sepBefore: sepBefore, sepAfter: sepAfter, text: text})
	}
	entryWidth := func(e entry) int {
		if focus && selectable(e) {
			return width - components.GridPointerWidth
		}
		return width
	}
	addEntry := func(i int, detail bool) {
		e := es[i]
		// A row's own keys are live only under reading mode's cursor;
		// anywhere else they render beside the key that hands the keyboard
		// to the transcript.
		add(i, e, e, m.renderEntryDetail(e, entryWidth(e), focus && i == focusIdx, detail), selectable(e))
	}

	if blk.step == nil {
		// A row outside a step has no step to be opened by, so ctrl+o never
		// reaches it and [enter] is the only thing that opens it.
		addEntry(blk.start, false)
		return units
	}
	g := blk.step
	headerWidth := width
	if focus {
		headerWidth -= components.GridPointerWidth
	}
	header := m.headerFor(blk, es)
	// A declared step nobody has started is its header and nothing else: no
	// rows to expand, so nothing for focus mode to select either.
	add(g.titleIdx, entry{kind: entryAssistant}, entry{kind: entryTool},
		header.View(headerWidth)+"\n", !g.queued())
	if header.Folded || g.queued() {
		return units
	}
	// A step's rows render through its slots, so a folded run of read-only
	// calls arrives as one counted group row — unless the step
	// has its detail open, which gives the run back.
	for _, sl := range m.stepSlots(es, g) {
		if !sl.group {
			addEntry(sl.idx, header.Detail)
			continue
		}
		e := es[sl.idx]
		add(sl.idx, e, es[sl.idx+sl.span-1], m.groupRowFor(es, sl).View(entryWidth(e))+"\n", true)
	}
	return units
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
