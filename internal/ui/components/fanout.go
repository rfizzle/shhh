package components

// Fan-out lanes (docs/interface/surfaces.md#the-agent-manager). Three
// children streaming their rows into one transcript reads as one confused
// feed, so a spawn of two or more collapses into a single block with a lane
// per child: its name, what it was asked to do, how far it has got, what it
// has cost, and what state it is in.
//
// Three rules the block enforces rather than documents. A blocked lane sorts
// to the top and says `⚠ needs you` in words in its outcome field, because
// the only thing a fan-out can need from you is an answer and it must never
// be the thing you scroll past. A lane draws a progress bar only when the
// spawn declared a step count — without one it gets the spinner, never a
// ratio nobody supplied. And a finished lane's bar stops measuring and starts
// stating: full, with `✓ 5/5` beside it, over the first line of what the
// child found.
//
// This is a passive renderer. The block re-renders from the supervisor's live
// snapshot at whatever width the host has, so a resize costs nothing and no
// layout state lives here.

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// FanoutState is one lane's lifecycle state. It mirrors the supervisor's
// child states; the block keeps its own so nothing in components imports the
// orchestration package.
type FanoutState int

const (
	FanoutQueued  FanoutState = iota // accepted, waiting for a slot
	FanoutRunning                    // working
	FanoutBlocked                    // waiting on an answer from you
	FanoutIdle                       // turn cancelled, waiting for steering
	FanoutDone                       // finished
	FanoutFailed                     // broke
)

// settled reports whether the lane has stopped moving, which is when its
// progress stops being worth drawing.
func (s FanoutState) settled() bool { return s == FanoutDone || s == FanoutFailed }

// AgentProgress is what one child reports about how it is doing: its state,
// how far it has got against a declared step count, how many calls it has
// made and what it has cost. A child is drawn in two places — a lane in the
// transcript and a row in the manager — and both render it
// through these methods, so what a lane says and what a row says about the
// same child can never drift apart.
type AgentProgress struct {
	State       FanoutState
	Step, Steps int
	Tools       int
	Spend       string
	// Frame is the spinner frame for a child with no declared step count;
	// the host ticks it.
	Frame int
}

// FanoutLane is one child of the batch.
type FanoutLane struct {
	State FanoutState
	// Name is the child's name; it takes the verb column, so a lane lines up
	// with the rows around it.
	Name string
	// Depth is how far under the session the child sits — 1 for a child the
	// session spawned, 2 for that child's own child — in the numbering the
	// rail's map uses for the same child. A depth past 1 draws the lane
	// behind a corner, so a batch that delegated reads as the tree it is
	// rather than as a row of siblings
	// (docs/capabilities/subagents.md#a-child-may-delegate-to-a-configured-depth).
	Depth int
	// Under is how many agents are live below this one. Zero says nothing:
	// almost every child delegates nothing, and a lane that reported it
	// would be reporting a zero on every fan-out there has ever been.
	Under int
	// Task is the one-line label of what it was asked to do — the only field
	// that grows, and the only one that clips.
	Task string
	// Step and Steps are progress against a declared step count. Steps of
	// zero means none was declared and the lane spins instead.
	Step, Steps int
	// Tools is the call count so far; Spend is pre-formatted by the host
	// (dollars where the pricing table knows the child's model, tokens
	// otherwise); Elapsed is the 6-column duration field.
	Tools   int
	Spend   string
	Elapsed string
	// Summary is the first line of a finished child's report.
	Summary string
	// Waiting names what a blocked child is waiting for, stated under the
	// lane: "needs you" without saying what for sends you looking.
	Waiting string
	// Seeded is how many of your uncommitted files the child's own copy of
	// the repository was started from. Zero says nothing — a child that
	// started from the last commit has nothing to explain.
	Seeded int
	// Steers is how many times this turn the child has been told the check
	// reads its work as off its task. Zero says nothing — a child nobody has
	// had to steer is almost every child.
	//
	// It is under the lane rather than beside the tool count because the
	// right-hand field is what the child's name gives way to: a name clipped
	// to an ellipsis is a lane the reader cannot tell from the one under it,
	// and the count is worth a line of its own before it is worth that.
	Steers int
	// Verdict is the last reading of the child's work, in the reader's own
	// closed vocabulary. It is stated beside the count rather than inferred
	// from it: the count is what has happened this turn and the reading is
	// where the work stands now, and a child steered twice and back on task
	// is the outcome the whole mechanism is for.
	Verdict string
	// SteerFrom is where the last steer came from, in the supervisor's own
	// closed vocabulary. Empty says nothing — a child nobody and nothing has
	// redirected is almost every child.
	//
	// It is stated because a steer now has more than one author: the check
	// reading the child's own work, you at this lane, and the orchestrator
	// that wrote the task. A count with no author leaves the one question
	// worth asking of it unanswered.
	SteerFrom string
	// Frame is the spinner frame for a lane with no declared step count; the
	// host ticks it.
	Frame int
}

// FanoutBlock is the whole batch — the header stating how many children are
// running and how many need you, then one lane each.
type FanoutBlock struct {
	Lanes []FanoutLane
	// Elapsed is the batch's own duration field: the longest-lived lane's.
	Elapsed string
	// Keys are the offers the block makes while a child is waiting on you.
	// They render once, under the lanes, and wrap rather than clip on a
	// narrow terminal (packOffers).
	Keys []TurnKey
}

// fanoutLead is the gutter a lane shares with an activity row: the pointer
// column, the mutation rail (a child's progress is a report, never an act),
// the state glyph, and the verb. The verb is `agent` — the vocabulary's name
// for a child's mirrored row in the parent transcript — because a lane is
// that row, one per child. The child's own name goes in the target field,
// which is the only field that grows: a name is not a word from a closed
// vocabulary and must never be clipped to eight columns, where `researcher-1`
// and `researcher-2` become the same string.
func fanoutLead(glyph string, depth int) string {
	return laneNesting(depth) + glyph + " " + verbField("agent")
}

// laneNesting is the lane's gutter: blank for a child of the session, and the
// corner hard against the glyph for one a child spawned — the same corner in
// the same place as the rail's map draws for the same child. It goes in the
// pointer column and the mutation rail, the two columns a lane never uses: a
// lane is a report and never an act, and nothing points at it.
//
// The gutter is three columns and the corner takes the last of them, so every
// depth past the first draws in that one column. The lane is a row on the
// grid and the columns past the gutter are the grid's — a lane that indented
// into the verb field would move the edge the whole transcript is read down
// (docs/interface/principles.md#one-grid). What the corner says here is that
// the lane is under something; how far under, the rail's map counts in full.
func laneNesting(depth int) string {
	if depth < 2 {
		return strings.Repeat(" ", ptrWidth+railWidth)
	}
	return strings.Repeat(" ", ptrWidth+railWidth-1) + agentNesting(2)
}

// agentNesting is the column a child spawned by another child is drawn in
// behind: one space per level below the first, then the corner. The corner is
// the frame's own, so a nested row borrows a mark the reader has already
// learned rather than adding one to the set
// (docs/interface/principles.md#closed-vocabularies). Depth counts the
// session as 0, so nothing under 2 is nested and nothing under 2 is drawn.
//
// A lane, a manager row and the rail's map are the same child through the
// same renderer, and this is what keeps the three of them indenting it by the
// same rule (docs/interface/surfaces.md#the-agent-manager).
func agentNesting(depth int) string {
	if depth < 2 {
		return ""
	}
	return strings.Repeat(" ", depth-2) + sty.Dimmer.Render("└")
}

// headerLead is the same gutter one level out — the block heads its lanes the
// way a step header heads its rows, so the nesting is visible without a rule.
// It still fills leadWidth, so the header's target and duration land in the
// same columns as its lanes': only the gutter moves.
//
// The mark takes the marker gutter's own first column, where a step header's
// fold caret and a sent message's ❯ go, rather than landing one column into
// it. That gutter is the edge the whole transcript is read down, and a mark
// half inside it lines up with nothing above or below the block
// (docs/interface/surfaces.md#the-leading-columns).
func headerLead() string {
	return sty.Info.Render("◇") + strings.Repeat(" ", ptrWidth-1) +
		verbField("fan-out") + strings.Repeat(" ", railWidth+glyphWidth)
}

// glyph is the kind glyph, and on a lane it is the kind glyph in every state:
// a lane is one child from the moment it is queued until it stops being one,
// and ◇ is what a child is. The state is said in the outcome field beside it,
// in words behind the state's own mark — `⚠ needs you`, `✓ 5/5`, `✗ failed` —
// so a monochrome terminal reads the same lane a colour one does
// (invariant 1) and the colour here only reinforces it.
//
// This is the one place the outcome table does not override the kind glyph,
// and the reason is that a lane is not a row. A row is one act and its
// outcome is the whole of what became of it; a lane is a child that will be
// many acts before it is anything. A manager row *is* a row and does override
// — see AgentRow.stateGlyph
// (docs/interface/departures.md#a-fan-out-lane-keeps-its-kind-glyph-and-a-manager-row-does-not).
func (p AgentProgress) glyph() string { return p.kindTone().Render("◇") }

// rowGlyph is the same child on a row rather than in a lane, and a row keeps
// the outcome table's rule: the states that ask something of the reader take
// the glyph column from the kind, and the ones that do not leave it alone. A
// blocked child leads with ⚠, a broken one with ✗, one still waiting for a
// slot with · and one waiting to be steered with ⊘; a child running or
// finished keeps ◇ and says how it went in the field on the right.
//
// The manager, the rail's AGENTS block and a lane are the same child through
// the same renderer, and this is the one place they differ — see glyph above
// for why a lane is not a row
// (docs/interface/departures.md#a-fan-out-lane-keeps-its-kind-glyph-and-a-manager-row-does-not).
func (p AgentProgress) rowGlyph() string {
	switch p.State {
	case FanoutBlocked:
		return sty.Err.Render("⚠")
	case FanoutFailed:
		return sty.Err.Render("✗")
	case FanoutQueued:
		return sty.Dim.Render("·")
	case FanoutIdle:
		return sty.Dim.Render("⊘")
	}
	return p.glyph()
}

// kindTone is the colour the kind glyph carries: info while the child is
// going, add once it has answered, del while it is stopped on something. A
// queued or idle child is none of those and takes the grey everything waiting
// takes.
func (p AgentProgress) kindTone() lipgloss.Style {
	switch p.State {
	case FanoutBlocked, FanoutFailed:
		return sty.Err
	case FanoutDone:
		return sty.Add
	case FanoutQueued, FanoutIdle:
		return sty.Dim
	default:
		return sty.Info
	}
}

// progress is the left-hand status field: the meter when the spawn declared a
// step count, the spinner when it did not, and the state's own mark and word
// once the child has stopped moving. The mark stands here rather than in the
// glyph column, which says what the child is; this says how it is doing.
//
// A child that finished against a declared step count keeps its meter, full,
// with `✓ 5/5` as the meter's text. The bar is no longer measuring anything —
// it states that the whole of the declared work was covered, which is what a
// reader who watched it climb was waiting to see, in the shape they were
// watching. A child that declared no count has no such shape and says `✓
// done`.
func (p AgentProgress) progress() string {
	switch p.State {
	case FanoutBlocked:
		return sty.Err.Render("⚠ needs you")
	case FanoutQueued:
		return sty.Dim.Render("queued")
	case FanoutIdle:
		return sty.Dim.Render("idle")
	case FanoutFailed:
		return sty.Err.Render("✗ failed")
	}
	if m, ok := AgentMeter(p.Step, p.Steps); ok {
		m.Text = fmt.Sprintf("%d/%d", min(max(p.Step, 0), p.Steps), p.Steps)
		if p.State == FanoutDone {
			// Full, and in the add every finished thing wears rather than the
			// info a lane climbs in: the run is over and the bar is now a
			// statement about it.
			m.Pct, m.Tone = 100, MeterProgress
			m.Text = fmt.Sprintf("✓ %d/%d", p.Steps, p.Steps)
		}
		return m.View()
	}
	if p.State == FanoutDone {
		return sty.Add.Render("✓ done")
	}
	return Spinner{Frame: p.Frame, Label: "working"}.View()
}

// stats is what the child cost so far: the calls it made and the money it
// spent, each left out when there is nothing to report rather than stated as
// a zero.
func (p AgentProgress) stats() string {
	var parts []string
	if p.Tools > 0 {
		parts = append(parts, plural(p.Tools, "tool"))
	}
	if p.Spend != "" {
		parts = append(parts, p.Spend)
	}
	if len(parts) == 0 {
		return ""
	}
	return sty.Dimmer.Render(strings.Join(parts, " · "))
}

// outcomeField joins the progress and the stats into the one right-aligned
// field, the way an activity row joins outcome and counts.
func (p AgentProgress) outcomeField() string {
	progress, stats := p.progress(), p.stats()
	switch {
	case progress == "":
		return stats
	case stats == "":
		return progress
	}
	return progress + sty.Dim.Render(" · ") + stats
}

// progressOf is the lane's child-progress view of itself, so the lane and
// the manager row for the same child draw from one renderer.
func (l FanoutLane) progressOf() AgentProgress {
	return AgentProgress{State: l.State, Step: l.Step, Steps: l.Steps,
		Tools: l.Tools, Spend: l.Spend, Frame: l.Frame}
}

func (l FanoutLane) glyph() string        { return l.progressOf().glyph() }
func (l FanoutLane) outcomeField() string { return l.progressOf().outcomeField() }

// target is the lane's growing field: the child's name, then what it was
// asked to do, joined with the separator every row in the product joins two
// facts with (docs/interface/principles.md#one-grid). Two spaces read as a
// column that is not there — the tasks under them never line up, because the
// names are not one width.
func (l FanoutLane) target() string {
	if l.Task == "" {
		return l.Name
	}
	return l.Name + detailSep + l.Task
}

// paintTarget leads the field with the name in body text and dims the task
// behind it. A field too narrow to hold the name whole goes dim entirely
// rather than emphasising half a name — the same rule the recovery rows keep.
func (l FanoutLane) paintTarget(s string) string {
	if l.Name != "" && strings.HasPrefix(s, l.Name) {
		return sty.Body.Render(l.Name) + sty.Dim.Render(strings.TrimPrefix(s, l.Name))
	}
	return sty.Dim.Render(s)
}

// View renders one lane plus whatever it has to say underneath: what a
// blocked child is waiting for, or the first line of a finished child's
// report.
func (l FanoutLane) View(width int) string {
	lines := []string{gridLineWith(fanoutLead(l.glyph(), l.Depth), l.target(), l.paintTarget,
		l.outcomeField(), l.Elapsed, width)}
	if note := l.note(); note != "" {
		lines = append(lines, indented(note, detailIndent, width))
	}
	return strings.Join(lines, "\n")
}

// note is the line under the lane: a blocked child's reason, a finished
// child's result, or — for a child still working, which has nothing else to
// add — how often it has been steered and by whom, and failing that what its
// copy of the repository was started from. Which of your uncommitted files a writer can
// see is a question you have while it runs and not after it has answered, so
// the line gives way to the outcome.
//
// A steer outranks the seed line while both are true, because one is news and
// the other is context: a child that has been told twice that its work reads
// as off its task is one to look at now, and where its files came from will
// still be there to ask about afterwards.
func (l FanoutLane) note() string {
	if l.State == FanoutBlocked {
		return l.Waiting
	}
	if l.State.settled() {
		return l.Summary
	}
	if note := l.steerNote(); note != "" {
		return note
	}
	if l.Under > 0 {
		// A lane with agents under it is quiet for a reason, and this is the
		// reason: the work it is waiting on is somewhere else on the block.
		// It outranks the seed line for the same reason a steer does — one is
		// what the child is doing now and the other is where its files came
		// from, which will still be there to ask about afterwards
		// (docs/capabilities/subagents.md#a-child-may-delegate-to-a-configured-depth).
		return plural(l.Under, "agent") + " under it"
	}
	if l.Seeded > 0 {
		return "started from " + plural(l.Seeded, "uncommitted file") + " in your tree"
	}
	return ""
}

// steerNote is the steering half of the line: how often the child has been
// steered this turn, who spoke to it last, and what the last reading made of
// its work. The source stands alone where the count is zero, which is what a
// redirect the child has already taken up looks like — the count it answered
// went back to zero when the child took the message, and the source is then
// the only thing left saying anyone had spoken to it.
func (l FanoutLane) steerNote() string {
	var note string
	switch {
	case l.Steers > 0 && l.SteerFrom != "":
		note = plural(l.Steers, "steer") + " · from " + l.SteerFrom
	case l.Steers > 0:
		note = plural(l.Steers, "steer")
	case l.SteerFrom != "":
		note = "steered from " + l.SteerFrom
	default:
		return ""
	}
	if l.Verdict != "" {
		note += " · last read " + l.Verdict
	}
	return note
}

// sorted returns the lanes in render order: blocked first, in the order they
// were spawned, then everything else in that same order. A child that needs
// an answer is the only thing in a fan-out that cannot wait, so it is never
// below one that can.
//
// What floats is the group and not the lane. A nested lane hangs off the lane
// above it, so a request lifted out on its own would leave a corner under
// nothing and its parent pointing at a lane that has moved
// (docs/capabilities/subagents.md#a-child-may-delegate-to-a-configured-depth).
// A group floats when anything in it is blocked, which is the same rule one
// level up: a parent whose delegate is waiting on you is a parent waiting on
// you.
func (b FanoutBlock) sorted() []FanoutLane {
	var blocked, rest []FanoutLane
	depths := make([]int, len(b.Lanes))
	for i, l := range b.Lanes {
		depths[i] = l.Depth
	}
	for _, g := range depthGroups(depths) {
		waiting := false
		for _, i := range g {
			waiting = waiting || b.Lanes[i].State == FanoutBlocked
		}
		for _, i := range g {
			if waiting {
				blocked = append(blocked, b.Lanes[i])
			} else {
				rest = append(rest, b.Lanes[i])
			}
		}
	}
	return append(blocked, rest...)
}

// depthGroups splits a run of agents into what moves together: an agent at
// the top level and every deeper agent drawn under it. It takes the depths
// and answers in positions, because the surfaces that need it hold different
// row types and the grouping is the same fact about all of them.
//
// A host hands its agents over in tree order, so a group is a run of the list
// rather than a lookup — which is also what makes an agent with nothing above
// it to nest under (a fixture, or a child whose parent is in an earlier
// block) a group of its own.
func depthGroups(depths []int) [][]int {
	var groups [][]int
	for i, d := range depths {
		if d > 1 && len(groups) > 0 {
			groups[len(groups)-1] = append(groups[len(groups)-1], i)
			continue
		}
		groups = append(groups, []int{i})
	}
	return groups
}

// tallyStates counts a set of children by state, for the one line that heads
// them.
func tallyStates(states []FanoutState) (running, blocked, done, failed int) {
	for _, st := range states {
		switch st {
		case FanoutBlocked:
			blocked++
		case FanoutDone:
			done++
		case FanoutFailed:
			failed++
		default:
			running++
		}
	}
	return running, blocked, done, failed
}

// stateTally states what a set of children still owes you. Whoever needs an
// answer is said first and in del, because it is the only part of the line
// that asks anything of you; the tally of finished children is left to the
// rows until nothing is running, when it becomes the whole story. The field
// never clips, so it says two things at most. The fan-out header and
// the manager's title rail are the same sentence about the same children, so
// they are the same function.
func stateTally(states []FanoutState) string {
	running, blocked, done, failed := tallyStates(states)
	var parts []string
	if blocked > 0 {
		parts = append(parts, sty.Err.Render(fmt.Sprintf("%d needs you", blocked)))
	}
	if running > 0 {
		parts = append(parts, sty.SpinText.Render(fmt.Sprintf("%d running", running)))
	}
	if len(parts) == 0 {
		if done > 0 {
			parts = append(parts, sty.Add.Render(fmt.Sprintf("%d done", done)))
		}
		if failed > 0 {
			parts = append(parts, sty.Err.Render(fmt.Sprintf("%d failed", failed)))
		}
	}
	return strings.Join(parts, sty.Dim.Render(" · "))
}

// states is the batch's lane states, in lane order.
func (b FanoutBlock) states() []FanoutState {
	out := make([]FanoutState, len(b.Lanes))
	for i, l := range b.Lanes {
		out[i] = l.State
	}
	return out
}

// counts tallies the batch for its header.
func (b FanoutBlock) counts() (running, blocked, done, failed int) {
	return tallyStates(b.states())
}

// headerOutcome states what the batch still owes you.
func (b FanoutBlock) headerOutcome() string { return stateTally(b.states()) }

// View renders the block at the given width: the header, then every lane in
// sort order, then the offers a blocked lane makes.
func (b FanoutBlock) View(width int) string {
	if len(b.Lanes) == 0 {
		return ""
	}
	lanes := b.sorted()
	lines := []string{gridLine(
		headerLead(),
		plural(len(lanes), "agent"),
		b.headerOutcome(), b.Elapsed, width)}
	for _, l := range lanes {
		lines = append(lines, l.View(width))
	}
	if _, blocked, _, _ := b.counts(); blocked > 0 {
		for _, keys := range packOffers(b.Keys, max(width-detailIndent, 1)) {
			lines = append(lines, detailLine(keys, width))
		}
	}
	return strings.Join(lines, "\n")
}
