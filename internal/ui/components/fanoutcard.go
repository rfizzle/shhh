package components

// The fan-out as a card (docs/interface/surfaces.md#the-agent-manager): the
// spawn is a step like any other, so it is drawn on the step's grammar — a
// header that is its receipt, the sentence that titled it as the body — and
// the children are the list in its footer, one row each, in the slots a
// card's header has: the glyph, the name in a slot of its own, what the
// child is doing, and how it stands on the right.

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// CardLine is what one line of a card with a list in its footer is, so a
// host can say which line a click landed on without working the layout out
// a second time.
type CardLine int

const (
	// CardLineText is the card's own text: a padding row, its body.
	CardLineText CardLine = iota
	// CardLineHeader is the header, which opens the card and closes it.
	CardLineHeader
	// CardLineChild is one entry of the list: a child's row, a plan's step.
	CardLineChild
	// CardLineNote is the line a child's row says under itself.
	CardLineNote
	// CardLineReport is a settled child's report fold and the report under it.
	CardLineReport
)

// fanoutVerb is the spawn's verb in the receipt's past tense, the way every
// card's header leads with what the step did.
const fanoutVerb = "spawned"

// laneNameSlot is the columns a child's name is given on its row, so the
// facts after it start in one column down the list. A longer name takes what
// it needs and one more: a name is never cut, because `researcher-1` and
// `researcher-2` cut short are the same name.
// See docs/interface/departures.md#a-fan-out-lane-sets-its-name-in-a-slot.
const laneNameSlot = 10

// laneFactsMin is how much of what a child is doing its row keeps before the
// costs on the right give way.
const laneFactsMin = 12

// laneDetailColumn is where the lines under a child's row start: its name's
// column, so what it reported reads as the child's.
const laneDetailColumn = CardBodyIndent + 2

// View renders one lane as a row of the card's footer, with whatever it has
// to say underneath it.
func (l FanoutLane) View(width int) string {
	lines, _ := l.cardLines(width, nil)
	return strings.Join(lines, "\n")
}

// cardLines is the lane's row, the line under it and its report fold, each
// on the band, with what each line is. offers are the keys a blocked child's
// row carries.
func (l FanoutLane) cardLines(width int, offers []TurnKey) ([]string, []CardLine) {
	inner := max(width-cardMargin, 1)
	lead := strings.Repeat(" ", CardBodyIndent)
	if l.Depth >= 2 {
		// The corner hard against the glyph, as the rail's map draws the
		// same child (AgentNesting).
		lead = strings.Repeat(" ", CardBodyIndent-1) + AgentNesting(2)
	}
	nameW := max(laneNameSlot, lipgloss.Width(l.Name)+1)
	head := lead + l.glyph() + " " + sty.Body.Render(l.Name) + strings.Repeat(" ", nameW-lipgloss.Width(l.Name))
	room := inner - lipgloss.Width(head)

	right := l.fittedRight(room, offers)
	factsRoom := room
	if right != "" {
		factsRoom -= lipgloss.Width(right) + 1
	}
	said := l.paintFacts(l.fittedFacts(max(factsRoom, 0)))
	gap := max(room-lipgloss.Width(said)-lipgloss.Width(right), 1)
	lines := []string{onBand(Clip(head+said+strings.Repeat(" ", gap)+right, inner), width)}
	roles := []CardLine{CardLineChild}
	if note := l.rowNote(); note != "" {
		lines = append(lines, onBand(indented(note, laneDetailColumn, inner), width))
		roles = append(roles, CardLineNote)
	}
	for _, r := range l.reportFold(inner) {
		lines = append(lines, onBand(r, width))
		roles = append(roles, CardLineReport)
	}
	return lines, roles
}

// facts is what the row says the child is doing, left of its state: what a
// blocked child is waiting for, what a settled one found, and otherwise what
// it was asked to do and the step of its own plan it is on.
func (l FanoutLane) facts() string {
	switch {
	case l.State == FanoutBlocked:
		if l.Waiting == "" {
			return laneBlocked
		}
		return laneBlocked + detailSep + l.Waiting
	case l.State.settled():
		if note := l.settledNote(); note != "" {
			return note
		}
	}
	facts := l.Task
	if title := l.stepTitle(); title != "" {
		if facts != "" {
			facts += detailSep
		}
		facts += title
	}
	return facts
}

// laneBlocked is the word a child waiting on you leads its row with, in del:
// it is the one thing in a fan-out that cannot wait.
const laneBlocked = "blocked"

// paintFacts dims the facts, leading a blocked child's with the word that
// says so in del.
func (l FanoutLane) paintFacts(s string) string {
	if l.State == FanoutBlocked && strings.HasPrefix(s, laneBlocked) {
		return sty.Err.Render(laneBlocked) + sty.Dim.Render(strings.TrimPrefix(s, laneBlocked))
	}
	return sty.Dim.Render(s)
}

// rowNote is the line under a child still working, which has nothing else to
// say on its row: how it has been steered, what its copy started from, what
// is under it. A blocked child says what it waits for on the row itself, and
// a settled one what it found.
func (l FanoutLane) rowNote() string {
	if l.State == FanoutBlocked || l.State.settled() {
		return ""
	}
	return l.note()
}

// fittedFacts is as much of the facts as room holds. The step a child with
// its own plan is on is what the row keeps once the costs have gone, so
// where the task and the step will not both fit, the task clips between the
// two; with no room left for any of the task it goes whole, and the step
// clips only after that.
func (l FanoutLane) fittedFacts(room int) string {
	facts, title := l.facts(), l.stepTitle()
	if lipgloss.Width(facts) <= room || title == "" || l.Task == "" || l.State == FanoutBlocked || l.State.settled() {
		return Clip(facts, room)
	}
	tail := detailSep + title
	if taskRoom := room - lipgloss.Width(tail); taskRoom > 1 {
		return Clip(l.Task, taskRoom) + tail
	}
	return Clip(title, room)
}

// fittedRight is as much of the right-hand run as room leaves the facts:
// how the child stands, what it cost and how long it has run. The costs give
// way until the facts hold whole — what the child was asked to do is read
// before what it has spent — in the order the manager's row keeps them: what
// it inherited, the tokens, the budget's share, the tool count. A review's
// verdict goes after them, and only where the facts would otherwise be cut
// under a name's worth of room; the clock goes last of all. A blocked
// child's run is the key that answers it, the one key the card carries.
// See docs/interface/departures.md#a-fan-outs-rows-keep-their-words-and-their-costs.
func (l FanoutLane) fittedRight(room int, offers []TurnKey) string {
	withClock := func(s string) string {
		switch {
		case l.Elapsed == "":
			return s
		case s == "":
			return sty.Dim.Render(l.Elapsed)
		}
		return s + sty.Dim.Render(detailSep+l.Elapsed)
	}
	leaves := func(r string) int { return room - lipgloss.Width(r) - 1 }
	if l.State == FanoutBlocked && len(offers) > 0 {
		k := offerRun(offers)
		if leaves(withClock(k)) >= laneFactsMin {
			return withClock(k)
		}
		return k
	}
	whole := lipgloss.Width(l.facts())
	p := l.progressOf()
	for _, drop := range []func(){
		func() {},
		func() { p.Inherited = 0 },
		func() { p.Spend = "" },
		func() { p.BudgetPct = 0 },
		func() { p.Tools = 0 },
	} {
		drop()
		if r := withClock(p.outcomeField()); leaves(r) >= whole {
			return r
		}
	}
	if r := withClock(p.outcomeField()); p.ReportVerdict == "" || leaves(r) >= laneFactsMin {
		return r
	}
	p.ReportVerdict = ""
	if r := withClock(p.outcomeField()); leaves(r) >= laneFactsMin {
		return r
	}
	return p.outcomeField()
}

// offerRun is a row's keys: the key in the key colour, since it is live — the
// chord reaches the manager from anywhere — and what it does dim.
func offerRun(offers []TurnKey) string {
	parts := make([]string, 0, len(offers))
	for _, o := range offers {
		parts = append(parts, sty.Key.Render(o.Key)+sty.Dim.Render(" "+o.Label))
	}
	return strings.Join(parts, sty.Dim.Render(detailSep))
}

// reportFold is the child's own words under a settled row: the line that says
// how much there is of them, and the report itself once the reader has
// opened it. It is the transcript's own fold grammar — `▸ report · 14 lines`
// — and prints no key, because the hint bar names what enter does under the
// cursor (docs/interface/principles.md#fold-never-hide,
// docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
func (l FanoutLane) reportFold(width int) []string {
	if !l.State.settled() || len(l.Report) == 0 {
		return nil
	}
	pad := strings.Repeat(" ", laneDetailColumn)
	room := max(width-laneDetailColumn, 1)
	mark := "▸"
	if l.ReportOpen {
		mark = "▾"
	}
	var lines []string
	for _, r := range l.Earlier {
		// Folded, and only folded: the answer the reader acts on is the one
		// under them, and an earlier one is a heading to remember it by.
		lines = append(lines, pad+sty.Dimmer.Render(Clip(
			fmt.Sprintf("▸ turn %d report%s%s", r.Turn, detailSep, plural(len(r.Lines), "line")), room)))
	}
	lines = append(lines, pad+sty.Dimmer.Render(Clip(mark+" report"+detailSep+plural(len(l.Report), "line"), room)))
	if !l.ReportOpen {
		return lines
	}
	body, dropped := l.Report, 0
	if l.MaxReport > 0 && len(body) > l.MaxReport {
		body, dropped = body[:l.MaxReport], len(body)-l.MaxReport
	}
	for _, line := range body {
		lines = append(lines, indented(line, laneDetailColumn, width))
	}
	if dropped > 0 {
		// The bound is a fold like any other, so it counts what it swallowed,
		// and names the key that opens the whole of it on its own screen, the
		// way a paste's bound does.
		lines = append(lines, pad+sty.Dim.Render(Clip(countedTail(dropped)+", "+
			keys.Bracket(keys.Reading.Expand)+" opens the whole of it", room)))
	}
	return lines
}

// View renders the batch as its card at the given width.
func (b FanoutBlock) View(width int) string {
	lines, _ := b.cardLines(width)
	return strings.Join(lines, "\n")
}

// Lines is what each line View draws is, in order.
func (b FanoutBlock) Lines(width int) []CardLine {
	_, roles := b.cardLines(width)
	return roles
}

// cardLines is the card and what each of its lines is.
func (b FanoutBlock) cardLines(width int) ([]string, []CardLine) {
	if len(b.Lanes) == 0 {
		return nil, nil
	}
	lanes := b.sorted()
	count := plural(len(lanes), "agent")
	rollup := count
	// The session's spawn count rides the header only while the batch is
	// live: that is when the next spawn is being planned, and a finished
	// batch would otherwise carry a number that has since moved. It is the
	// first thing the header gives up. It is the same count the manager
	// states, said without its verb: the header's verb already says it.
	if b.live() && b.SpawnLimit > 0 {
		rollup += detailSep + fmt.Sprintf("%d of %d in the session", b.Spawned, b.SpawnLimit)
	}
	card := StepCard{
		Kind:           ActivitySubagent,
		Verb:           fanoutVerb,
		Rollup:         rollup,
		Bare:           count,
		Outcome:        b.headerOutcome(),
		OutcomePainted: true,
		Duration:       b.Elapsed,
		Body:           b.Body,
		Selected:       b.Selected,
	}
	if b.Low {
		card.Density = CardLow
		return []string{card.View(width)}, []CardLine{CardLineHeader}
	}
	roles := []CardLine{CardLineText, CardLineHeader}
	for range card.bodyLines(width) {
		roles = append(roles, CardLineText)
	}
	for _, l := range lanes {
		var offers []TurnKey
		if l.State == FanoutBlocked {
			offers = b.Keys
		}
		rows, rr := l.cardLines(width, offers)
		card.Rows = append(card.Rows, rows...)
		roles = append(roles, rr...)
	}
	roles = append(roles, CardLineText)
	return strings.Split(card.View(width), "\n"), roles
}

// live reports whether any child is still working.
func (b FanoutBlock) live() bool {
	running, blocked, held, _, _ := b.counts()
	return running+blocked+held > 0
}

// headerOutcome is how the batch stands, on the header's right. While every
// child is simply working the card says they are working together, which is
// the one fact a count of them would repeat; a hold or a wait for a check
// slot is counted, because it is what the reader is watching land; and once
// nothing runs it is the tally of how they ended. A child waiting on you is
// said on its own row — the one row with a key — and not here, except on the
// low rung's header alone, which has no rows
// (docs/interface/departures.md#the-childrens-tally-says-who-needs-you-first).
func (b FanoutBlock) headerOutcome() string {
	_, blocked, held, _, _ := b.counts()
	if b.Low && blocked > 0 {
		// The low rung's card is its header alone, so the row that says a
		// child is waiting on you is not drawn; the header says it instead,
		// first, because an answer cannot wait behind a rung.
		return waitingTally(b.states(), b.slotWaits(), true)
	}
	if b.live() && held == 0 && b.slotWaits() == 0 {
		return sty.Dim.Render("in parallel")
	}
	return waitingTally(b.states(), b.slotWaits(), false)
}
