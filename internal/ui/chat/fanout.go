package chat

// Fan-out lanes in the transcript (
// docs/interface/surfaces.md#the-agent-manager). A round that
// spawned one child keeps today's inline `◇ spawn` row; a round that spawned
// two or more turns those rows into a single block with a lane per child, so
// three agents read as three things rather than as one interleaved feed.
//
// The block stores nothing but the batch number. Everything it draws is read
// off the supervisor's live snapshot at render time, which is what lets the
// lanes update in place while the children run and re-render at any width
// after a resize — the same contract every other transcript entry keeps.

import (
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// fanoutBatch is the transcript's handle on one round's fan-out: the batch
// the supervisor stamped on the children that round spawned. It is a pointer
// on the entry so the block keeps its identity as the transcript is
// re-rendered.
type fanoutBatch struct{ batch int }

// beginSpawnBatch opens the supervisor's next spawn batch and forgets the
// last round's first spawn row. The parent front-end is the only thing that
// knows where a tool round begins, which is why the boundary is pushed down
// from here rather than guessed at in the supervisor.
func (m *Model) beginSpawnBatch() {
	m.spawnRow = 0
	if m.subagents != nil {
		m.subagents.BeginBatch()
	}
}

// appendSpawnEntry files the row a successful spawn produced. The first child
// of a round gets the ordinary activity row; the second turns that row into
// the round's fan-out block in place, and every child after it joins the
// block rather than adding a row of its own.
//
// The row is replaced rather than removed: transcript indices are what focus
// mode, the changeset and an approved plan's checklist all address entries
// by, so a fan-out must not shift them.
func (m *Model) appendSpawnEntry(e entry) {
	if m.subagents == nil {
		m.appendEntry(e)
		return
	}
	batch := m.subagents.Batch()
	if m.subagents.BatchSize(batch) < 2 {
		// Where the row landed, not where the feed ended: a spawn is a gated
		// call, so its row goes back among the round's own rows (queue.go).
		m.spawnRow = m.appendEntry(e) + 1
		return
	}
	if m.spawnRow > 0 {
		m.transcript[m.spawnRow-1] = entry{kind: entryFanout, fanout: &fanoutBatch{batch: batch}}
		m.spawnRow = 0
		// The replaced row may already be rendered, and the cache never
		// re-renders an entry it has seen.
		m.invalidateRenderCache()
	}
}

// fanoutStatuses is the batch's children, in spawn order.
func (m Model) fanoutStatuses(b *fanoutBatch) []subagent.Status {
	if b == nil || m.subagents == nil {
		return nil
	}
	var out []subagent.Status
	for _, st := range m.subagents.Snapshot() {
		if st.Batch == b.batch {
			out = append(out, st)
		}
	}
	return out
}

// fanoutOpens reports whether a fan-out block has anything for the reading
// key to open: the report of a child that has stopped. It is a Model question
// and not an entry one because the block stores only its batch number and
// everything it draws is read off the supervisor.
//
// A block whose children are all still running opens nothing, and the cursor
// does not stop on it. A key offered on a row that will not honour it is a key
// that reads as broken
// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
func (m Model) fanoutOpens(e entry) bool {
	for _, st := range m.fanoutStatuses(e.fanout) {
		if m.childReport(st) != "" {
			return true
		}
	}
	return false
}

// rowExpands is expandable plus the one row whose body is not on the entry
// at all: a fan-out block, whose reports live on the supervisor. Every
// surface that asks whether a row opens asks this, so the key, the pointer
// and the bar cannot disagree about which rows do.
func (m Model) rowExpands(e entry) bool { return expandable(e) || m.fanoutOpens(e) }

// fanoutReports is every settled child's report in the block, in the order
// the block nests its lanes, each as the lines its fold counts.
func (m Model) fanoutReports(e entry) (names []string, reports [][]string) {
	nested, _ := m.nestAgents(m.fanoutStatuses(e.fanout))
	for _, st := range nested {
		if report := m.childReport(st); report != "" {
			names = append(names, st.Name)
			reports = append(reports, strings.Split(report, "\n"))
		}
	}
	return names, reports
}

// fanoutOverflows reports whether any report in the block held lines back at
// the opened fold's bound, which is when the block has a third depth: the
// whole report on its own screen, the depth a tool body and a paste open
// into (docs/interface/surfaces.md#the-activity-row).
func (m Model) fanoutOverflows(e entry) bool {
	_, reports := m.fanoutReports(e)
	for _, r := range reports {
		if len(r) > maxExpandedResultLines {
			return true
		}
	}
	return false
}

// fanoutOutputView is the block's reports on their own screen, unbounded.
// Several are one view with each named where it starts, the shape several
// pastes on one message take (attachments.go), because the reader opened the
// block and not one lane of it.
func (m Model) fanoutOutputView(e entry) *components.OutputView {
	names, reports := m.fanoutReports(e)
	var lines []string
	for i, r := range reports {
		if len(reports) > 1 {
			if i > 0 {
				lines = append(lines, "")
			}
			lines = append(lines, strings.ToUpper(names[i]))
		}
		lines = append(lines, r...)
	}
	title := plural(len(reports), "report")
	if len(reports) == 1 {
		title = names[0] + " report"
	}
	return &components.OutputView{Title: title, Lines: lines}
}

// fanoutLive reports whether any child of the entry's batch is still working.
// A block with a live child can never be frozen into the render cache — its
// lanes have to keep moving.
func (m Model) fanoutLive(e entry) bool {
	for _, st := range m.fanoutStatuses(e.fanout) {
		switch st.State {
		case subagent.StateDone, subagent.StateFailed:
		default:
			return true
		}
	}
	return false
}

// childProgress is one child's live progress in the form both surfaces that
// draw a child read: the lane in the transcript and the row in the manager
// . One mapping from the supervisor's state means the two can never
// disagree about what a child is doing.
func (m Model) childProgress(st subagent.Status) components.AgentProgress {
	p := components.AgentProgress{
		Step:  st.Step,
		Steps: st.Steps,
		Tools: st.ToolCalls,
		Spend: m.childSpendLabel(st),
		Frame: m.spinFrame,
		// What it was handed of the parent's conversation, on the line that
		// says what it has cost (docs/capabilities/subagents.md#what-they-share).
		Inherited: st.Inheritance,
	}
	// A review is the one role whose last line is a word rather than prose:
	// its prompt makes it end on the verdict the task asked for, so the
	// verdict is a fact about the child the way its state is, and it is read
	// here so the lane and the manager row say it together
	// (docs/capabilities/subagents.md#what-comes-back-says-what-happened-to-it).
	if st.Role == subagent.RoleReviewer {
		p.ReportVerdict = reportVerdict(m.childReport(st))
	}
	// A held child is parked at its own round boundary waiting for the
	// session to let it go. It is read off the status rather than off the
	// lifecycle, which is still `running` — the child keeps its slot, its
	// worktree and its conversation — and it outranks that lifecycle here
	// because what the surfaces draw is where the child has stopped, not what
	// it is between (docs/capabilities/subagents.md#a-hold-reaches-the-whole-fan-out).
	//
	// It is its own state rather than idle's. Both are stopped and only one
	// of them was stopped on purpose: idle is a turn that was cancelled and
	// wants steering, and a park the reader asked of the whole fan-out drawn
	// that way is a child that reads as having lost its turn.
	if st.Held {
		p.State = components.FanoutHeld
		return p
	}
	switch st.State {
	case subagent.StateQueued:
		p.State = components.FanoutQueued
	case subagent.StateBlocked:
		p.State = components.FanoutBlocked
	case subagent.StateIdle:
		p.State = components.FanoutIdle
	case subagent.StateDone:
		p.State = components.FanoutDone
	case subagent.StateFailed:
		p.State = components.FanoutFailed
	default:
		p.State = components.FanoutRunning
	}
	return p
}

// childNote is the line under a child wherever it is drawn: what a blocked
// one is waiting for, why a failed one failed, what a finished one found.
// Nothing for a child still working — its progress already says it.
func childNote(st subagent.Status) string {
	switch st.State {
	case subagent.StateBlocked:
		return st.Detail
	case subagent.StateRunning, subagent.StateQueued:
		// A child answering a follow-up is working on something other than
		// the task its row names, and the row says which question.
		if st.FollowUp != "" {
			return "follow-up · " + st.FollowUp
		}
	case subagent.StateDone:
		return firstLine(st.Summary)
	case subagent.StateFailed:
		// The row already says "failed"; the note says why.
		return strings.TrimPrefix(st.Detail, "failed · ")
	}
	return ""
}

// childReport is a settled child's own final report, as it wrote it — the
// text a lane folds open under its detail line. It is read off the supervisor
// at render time rather than carried on the status: the status is snapshotted
// on every frame for every child, and a report is the largest thing a child
// produces.
//
// Only a child that has stopped has one to show. A report read mid-run would
// be whatever the child had said so far, which is not a report and is not
// what the first line under the lane is the first line of.
func (m Model) childReport(st subagent.Status) string {
	if m.subagents == nil {
		return ""
	}
	switch st.State {
	case subagent.StateDone, subagent.StateFailed:
	default:
		return ""
	}
	report, _, ok := m.subagents.FinalReport(st.Name)
	if !ok {
		return ""
	}
	return strings.TrimSpace(report)
}

// fanoutBlockFor builds the block for one entry from the live snapshot.
func (m Model) fanoutBlockFor(e entry) components.FanoutBlock {
	var block components.FanoutBlock
	var longest time.Duration
	// In tree order, so a child a child spawned is the lane under its
	// parent's and the corner it draws behind has a row to hang off.
	nested, depth := m.nestAgents(m.fanoutStatuses(e.fanout))
	for _, st := range nested {
		p := m.childProgress(st)
		lane := components.FanoutLane{
			State:      p.State,
			Name:       st.Name,
			Depth:      depth[st.Name],
			Under:      len(m.subagents.Under(st.Name)),
			Task:       firstLine(st.Task),
			Step:       p.Step,
			Steps:      p.Steps,
			Tools:      p.Tools,
			Spend:      p.Spend,
			Elapsed:    turnDuration(st.Elapsed),
			Seeded:     st.Seeded,
			Inherited:  p.Inherited,
			Steers:     st.Steers,
			Yours:      st.LaneSteers,
			FromParent: st.ParentSteers,
			Verdict:    st.Verdict,
			SteerFrom:  string(st.SteerFrom),
			Frame:      p.Frame,
		}
		if note := childNote(st); note != "" {
			if st.State == subagent.StateBlocked {
				lane.Waiting = note
			} else {
				lane.Summary = note
			}
		}
		// The child's own words, under the lane it ran in. One fold flag for
		// the whole block, which is the shape a message with several pastes
		// already has (attachments.go): the reader opened the block, not one
		// of the lanes in it.
		if report := m.childReport(st); report != "" {
			// What the child answered before each follow-up, folded above
			// the answer that replaced it and headed with its turn.
			for _, r := range m.subagents.EarlierReports(st.Name) {
				lane.Earlier = append(lane.Earlier, components.LaneReport{
					Turn: r.Turn, Lines: strings.Split(strings.TrimSpace(r.Text), "\n")})
			}
			lane.Report = strings.Split(report, "\n")
			lane.ReportOpen = e.expanded
			lane.MaxReport = maxExpandedResultLines
			lane.Assumptions = statedAssumptions(report)
			lane.ReportVerdict = p.ReportVerdict
			if st.State == subagent.StateDone {
				// The first line without the marker that says there is more
				// of it. The fold directly under this line says the same
				// thing and says how much, so the `…` is the second of two
				// signals for one fact — and the one that ends up in the
				// middle of the line as soon as a count follows it.
				lane.Summary = strings.TrimSpace(lane.Report[0])
			}
		}
		if st.Elapsed > longest {
			longest = st.Elapsed
		}
		block.Lanes = append(block.Lanes, lane)
	}
	// The batch's own span is its longest-lived child: a fan-out is over when
	// the last of them stops.
	block.Elapsed = turnDuration(longest)
	// The manager is where a blocked child is answered, and it opens mid-turn
	//; the answer happens in the list itself, without a
	// detour through the child's session.
	block.Keys = []components.TurnKey{{Key: keys.Bracket(keys.Draft.Agents), Label: "agents"}}
	return block
}

// Reading a report. Two facts about a finished child are in the child's own
// words and nowhere else: what it assumed instead of asking, and — for a
// review — what it concluded. Both are read off the text here rather than
// recorded as the child runs, because neither is a thing the supervisor
// watches happen; they are things the report turns out to say.

// verdictWords and verdictChars are how short a line has to be to be a
// verdict rather than a sentence. A verdict is a phrase from whatever
// vocabulary the task named — `approve`, `request changes`, `approve with
// changes` — and a report that ended on a paragraph ended on prose. Putting
// a clause of that beside `✓ done` would state a conclusion the child never
// drew.
const (
	verdictWords = 4
	verdictChars = 32
)

// reportVerdict is the word a review ended on, or empty where its last line
// is not a verdict at all. A reviewing child is told to end its report on a
// line of its own, `Verdict: <word>` (the reviewer's prompt and
// internal/subagent's review directive), so the label is read first and
// trusted: it is the shape the child was asked for. The unlabelled reading
// below is for a report written before the contract named the label, or by a
// child that did not follow it.
//
// The word is lower-cased. It stands in the outcome field beside `✓ done` and
// `⚠ needs you`, which is a field of lower-case words, and a verdict that
// arrived capitalised because the child began a sentence with it would be the
// one word in the column shouting (docs/interface/principles.md#one-grid).
func reportVerdict(report string) string {
	lines := strings.Split(report, "\n")
	i := len(lines) - 1
	for i >= 0 && strings.TrimSpace(lines[i]) == "" {
		i--
	}
	if i < 0 {
		return ""
	}
	line, labelled := unmarked(lines[i]), false
	if label, rest, ok := strings.Cut(line, ":"); ok {
		// A labelled verdict — `Verdict: approve with changes` — is the
		// label's own text, and the label is what says the line is one. Any
		// other colon is a line making two statements, which a verdict does
		// not do.
		if l := unmarked(label); l != "verdict" && l != "final verdict" {
			return ""
		}
		line, labelled = strings.TrimRight(unmarked(rest), "."), true
	}
	// Unlabelled, what has to be told apart is a verdict from a sentence the
	// report happened to end on, and two things separate them. A sentence
	// ends on a stop and a verdict does not; a sentence has a subject and a
	// verdict names a decision instead. So `Ship it.` and `The tests pass
	// now` are reports of what happened rather than the word the task asked
	// the reviewer to end on, and neither belongs beside `✓ done`.
	//
	// It costs the reviewer that ends on `Approve.` its word, which the lane
	// then draws as `done` alone — the same as any other report with nothing
	// a verdict can be read off. Reading a sentence as a verdict is the worse
	// of the two failures: one leaves the field empty and the other puts a
	// conclusion in it that the child never drew.
	if !labelled && (strings.ContainsAny(line, ".!?") || opensASentence(line)) {
		return ""
	}
	if line == "" || len(line) > verdictChars || len(strings.Fields(line)) > verdictWords {
		return ""
	}
	if strings.ContainsAny(line, ".!?") {
		return ""
	}
	return line
}

// sentenceOpeners are the words a sentence starts with and a verdict never
// does, because a verdict has no subject: it names what was decided.
var sentenceOpeners = map[string]bool{
	"a": true, "an": true, "the": true, "i": true, "we": true, "you": true,
	"they": true, "it": true, "this": true, "that": true, "there": true,
}

// opensASentence reports whether a line starts the way a sentence does.
func opensASentence(line string) bool {
	f := strings.Fields(line)
	return len(f) > 0 && sentenceOpeners[f[0]]
}

// statedAssumptions counts what a report lists under a heading of its own for
// assumptions. A child is never offered the tool that asks, and what it does
// instead is state the assumption it would have asked about
// (docs/capabilities/subagents.md#a-child-answers-to-the-session); the count
// on the lane is what says there is something in there to disagree with.
//
// A report with no such heading counts nothing rather than zero, and the two
// are the same on the lane: neither draws a field. What must not happen is a
// lane asserting that a child assumed nothing because it never wrote the
// section.
func statedAssumptions(report string) int {
	lines := strings.Split(report, "\n")
	at := -1
	for i, line := range lines {
		if unmarked(line) == "assumptions" {
			at = i
			break
		}
	}
	if at < 0 {
		return 0
	}
	n := 0
	for _, line := range lines[at+1:] {
		if sectionBreak(line) {
			break
		}
		if listItem(line) {
			n++
		}
	}
	return n
}

// unmarked is a line with its Markdown taken off and folded to lower case:
// the same heading whether the child wrote `## Assumptions`, `**Assumptions**`
// or `Assumptions:`. A report is prose a model wrote, so the shape it names a
// section with is the one thing about it that cannot be relied on.
func unmarked(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimLeft(s, "#")
	s = strings.Trim(s, " *_`")
	return strings.ToLower(strings.TrimSpace(strings.TrimSuffix(s, ":")))
}

// sectionBreak reports whether a line opens a section of its own, which is
// where the section above it stops.
func sectionBreak(line string) bool {
	t := strings.TrimSpace(line)
	switch {
	case t == "":
		return false
	case strings.HasPrefix(t, "#"):
		return true
	case strings.HasPrefix(t, "**") && strings.HasSuffix(t, "**"):
		return true
	}
	return strings.HasSuffix(t, ":") && !listItem(t)
}

// listItem reports whether a line is an item of a list, in either of the two
// shapes Markdown writes one in.
func listItem(line string) bool {
	t := strings.TrimSpace(line)
	for _, mark := range []string{"- ", "* ", "+ "} {
		if strings.HasPrefix(t, mark) {
			return true
		}
	}
	i := 0
	for i < len(t) && t[i] >= '0' && t[i] <= '9' {
		i++
	}
	return i > 0 && i+1 < len(t) && (t[i] == '.' || t[i] == ')') && t[i+1] == ' '
}

// childSpendLabel is what a child was billed: the total the child priced
// request by request as each answer came back, cache split and all.
//
// The two fallbacks are for a child nothing priced — no pricing table where
// it ran, or a model the table does not know. Then it is the fresh input rate
// on a bare pair, which overstates a child whose prompt prefix is re-sent
// every round, and it is charged at the child's own model rather than the
// session's: a fan-out is the one place where several models are billed at
// once, and the orchestrator's price is the wrong one for a child the model
// sent somewhere cheaper.
func (m Model) childSpendLabel(st subagent.Status) string {
	if st.Spend.In == 0 && st.Spend.Out == 0 {
		return ""
	}
	if st.Spend.Priced {
		return formatCost(st.Spend.Cost)
	}
	if m.prices != nil && st.Model != "" {
		if in, out, found := m.prices.Cost(st.Model, st.Spend.In, st.Spend.Out); found {
			return formatCost(in + out)
		}
	}
	return m.freshRateLabel(st.Spend.In, st.Spend.Out)
}

// liveFanoutBlock is the index of the earliest transcript block holding a
// fan-out whose children are still working. renderHistory freezes everything
// before the last block rows can land in; a live fan-out is the one thing
// that keeps changing without any row landing at all, so nothing from there
// on may be frozen.
func (m Model) liveFanoutBlock(blocks []transcriptBlock) int {
	for i, blk := range blocks {
		start, end := blk.start, blk.end
		for j := start; j < end && j < len(m.transcript); j++ {
			if m.transcript[j].kind == entryFanout && m.fanoutLive(m.transcript[j]) {
				return i
			}
		}
	}
	return len(blocks)
}
