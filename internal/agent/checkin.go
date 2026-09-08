package agent

// Asking a turn what it has got.
//
// A long investigation has no way to notice it is finished. From inside a
// turn every round looks like progress — one more file to read, one more
// pattern to try — and the signal that enough is known is not a tool result
// but a judgement, which nothing in the loop ever asks for. So sessions ran a
// hundred and fifty rounds of reading and searching and stopped only when the
// person watching asked whether they had enough, at which point they said yes
// and started work. The question was the whole intervention: the model
// already had what it needed and had not been prompted to say so.
//
// Sub-agents were given that prompt and the parent was not, because the
// parent has a human in front of it and the round cap hands control back to
// them. That made the person the check-in mechanism — a job they should not
// have, and cannot do while looking away. The interval below asks the
// question on its own, long before the cap.
//
// RepeatDetector (repeat.go) is the other half of the same problem: it
// catches a turn asking one question twice, this catches a turn that has
// stopped asking anything new.
// See docs/capabilities/coding-agent.md#a-long-turn-is-asked-what-it-has-got.
//
// The interval is rounds, and a turn that dies does not die of rounds. A
// sub-agent's life ends on a token budget, and one making few large calls
// spends the whole of it long before the round interval comes round: the
// observed child died on its budget at round 27 having been asked exactly one
// question, at round 25. So the same machinery runs on a second clock — a
// check-in every share of the budget, widening the way the round interval
// widens — and that one asks the question the rounds cannot, which is what
// the spending bought.
// See docs/capabilities/coding-agent.md#a-childs-other-clock-is-its-budget.

import (
	"fmt"
	"strconv"
	"strings"
)

// DefaultCheckInInterval is how many tool rounds pass before a session is
// asked to take stock. It sits well under DefaultMaxToolRounds because the cap
// is the checkpoint for the human and this is the one the turn does for
// itself; a check-in that only ever arrived with the cap would be the pause
// the person is already there for.
//
// It is the interval for a turn that has plenty else watching it — a reading
// every few rounds that can ask sooner, a round cap, and a person. A surface
// with less than that sets its own, shorter, because this number is
// calibrated for the best-supervised case and would be the wrong default for
// the least: see SetCheckInInterval.
const DefaultCheckInInterval = 40

// The interval widens as a turn goes on: often enough early to catch a turn
// working on the wrong thing, rare enough later to stay out of the way of one
// that is committed and going somewhere. It is the shape the sub-agent's own
// budget check-ins already escalate in.
//
// The growth is bounded so a long run never stops being asked. Doubling
// without a ceiling means a turn that survives a few check-ins is effectively
// never questioned again, which is the failure this mechanism exists for
// wearing a longer timescale.
//
// Two doublings is what the sub-agent's own worked example describes — 25,
// then 50, then 100 — and it is as far as the widening should go: a fourfold
// interval on a child's 25 is a hundred rounds, which is already the outer
// edge of "rare enough to stay out of the way". Steering.CheckInDoublings is
// the count a surface can put in its place; the doubling itself is not a
// setting.
const (
	checkInGrowth = 2
	// DefaultCheckInDoublings is how many times the interval may double
	// before it stops widening.
	DefaultCheckInDoublings = 2
)

// checkInBudgetShare is the share of a turn's token budget that passes before
// it is asked to take stock: a quarter, widening from there off the same
// count the round interval widens off.
//
// It is a share rather than a number of tokens because the budget it divides
// is the caller's, and every surface that has one sets a different one. A
// quarter is what the observed failure asks for: a child with a 200,000-token
// budget spent all of it in 27 rounds of reading, so the first question falls
// around 50,000 tokens whatever shape the rounds take, and the widening puts
// the second at three quarters, still ahead of the end. Smaller and a child
// making a handful of large calls is interrupted before it has read anything;
// larger and the first question arrives with too little budget left to act on
// the answer.
const checkInBudgetShare = 4

// maxCheckInWritten bounds the files a budget check-in names back. It is a
// question and not an inventory: a turn that has written thirty files is not
// the one this exists for, and a list that long buries the question under it.
const maxCheckInWritten = 8

// SetCheckInInterval overrides how many rounds pass between check-ins. Zero or
// less restores the default.
//
// It is per-surface because the interval is only ever the last thing watching
// a turn, and how much else is watching differs enormously. A session has a
// reading that asks sooner, a round cap, and a person; a sub-agent runs
// uncapped, takes no readings unless asked to, and has nobody in front of it,
// so its check-in is the only question it will ever be put.
// See docs/capabilities/coding-agent.md#the-interval-is-the-last-thing-watching.
func (a *Agent) SetCheckInInterval(n int) {
	if n <= 0 {
		n = DefaultCheckInInterval
	}
	a.steering.CheckInInterval = n
}

// SetFinished overrides the line this surface's check-ins close on. Empty
// restores FinishedInSession.
//
// It is per-surface for the reason the interval is, and it is the same one
// line: a check-in exists to give a turn that is quietly already done
// somewhere to go other than more reading, and where that is depends on what
// ends the turn. A session says so to the person in front of it; a child's
// final report is its whole deliverable, and one told to say so instead says
// so into a transcript nobody reads and carries on.
// See docs/capabilities/coding-agent.md#the-interval-is-the-last-thing-watching.
func (a *Agent) SetFinished(line string) { a.steering.Finished = line }

// SetCheckInBudget gives the turn its second clock: spend answers what it has
// taken in and what it is allowed, and written what it has changed. Nil spend
// stops that clock, which is every surface running against no budget at all.
//
// The two are one call because a budget clock with nothing to say about the
// writes is a check-in that names the wrong thing. The whole reason to ask on
// spend rather than on rounds is that spending is not progress, and the fact
// that says which it was is what the turn has written — a child that has used
// a quarter of its attention and touched nothing is the failure, and its
// round check-in, which asks about rounds, cannot see it.
//
// Both are read from the round boundary the check-in is asked at, on the
// loop's own goroutine, and each guards its own state; neither is called
// anywhere else.
// See docs/capabilities/coding-agent.md#a-childs-other-clock-is-its-budget.
func (a *Agent) SetCheckInBudget(spend func() (spent, budget int64), written func() []string) {
	a.spend, a.written = spend, written
	a.markSpend()
}

// checkInInterval is the number of rounds owed before the next check-in,
// widened by how many this turn has already had.
func (a *Agent) checkInInterval() int {
	base := a.steering.CheckInInterval
	if base <= 0 {
		base = DefaultCheckInInterval
	}
	for i := 0; i < a.checkIns && i < a.steering.doublings(); i++ {
		base *= checkInGrowth
	}
	return base
}

// checkInSpend is the spend owed before the next check-in, widened by how
// many this turn has already had.
//
// It widens off the same count the round interval does, so the two clocks
// are one escalation rather than two: a turn already asked twice by its
// rounds is committed to something, and the budget is no reason to start
// asking it at the narrow interval again. Zero is a turn with no budget,
// which is the clock stopped.
func (a *Agent) checkInSpend(budget int64) int64 {
	if budget <= 0 {
		return 0
	}
	share := budget / checkInBudgetShare
	for i := 0; i < a.checkIns && i < a.steering.doublings(); i++ {
		share *= checkInGrowth
	}
	return share
}

// spendDue reports whether the turn has taken in another share of its budget
// since something last asked it to take stock.
func (a *Agent) spendDue() bool {
	if a.spend == nil {
		return false
	}
	spent, budget := a.spend()
	share := a.checkInSpend(budget)
	return share > 0 && spent-a.lastSpend >= share
}

// markSpend puts the spend clock's mark where the turn stands now, which is
// how the interval comes to be measured from the last intervention rather
// than from the start of the turn. A surface with no budget has nothing to
// mark and marks nothing.
func (a *Agent) markSpend() {
	if a.spend == nil {
		return
	}
	a.lastSpend, _ = a.spend()
}

// CheckInPrompt is the built-in turn handed to a session that has reached a
// check-in.
//
// It asks about the work rather than announcing a budget, on purpose: told it
// is running out, a model apologises and stops; asked what is left, it says
// so and carries on. The last line is the one that matters for a turn that
// has quietly finished — it gives it somewhere to go other than more reading.
// The closing line is a parameter because a session and a sub-agent finish
// differently: one reports to the person in front of it, the other has a
// final report that is its whole deliverable. Which of the two a turn is
// asked with is Steering.Finished, not the call site.
func CheckInPrompt(used int, whenFinished string) string {
	return buildCheckIn(strconv.Itoa(used), whenFinished)
}

// CheckInWording is the built-in check-in with its substitutions left
// standing, which is the same text a file replacing it would hold. It is
// what a scaffold writes to start from, and what a wording is compared
// against to decide whether it replaced anything.
func CheckInWording() string {
	return buildCheckIn(PlaceholderRounds, PlaceholderFinished)
}

// buildCheckIn assembles the wording from parts that are already text, so
// the prompt a session sends and the wording a file starts from are one
// sentence rather than two that can drift.
func buildCheckIn(rounds, whenFinished string) string {
	return fmt.Sprintf(`You have used %s tool rounds. This is a routine check-in, not a stop — nothing has gone wrong and nothing is running out.

Briefly take stock:
- what you have established or changed so far
- what is still left to do
- what you are doing next

Then carry on with the task. If you already know enough to act, stop looking and start work — more reading is not more progress. %s`, rounds, whenFinished)
}

// budgetNote is the sentence a check-in the spend clock asked carries under
// the surface's own wording, and the only thing that tells one from a
// check-in the round clock asked.
//
// It names what the turn has written rather than what it has spent, which is
// the same choice the check-in itself makes: a turn told it is running out
// apologises and stops, where one asked what it has to show for the work says
// so and carries on. The answer that matters is none. A child that has spent
// a quarter of its budget reading has nothing to show for it and is asked
// about that specifically, because nothing else it is ever asked names it —
// the round check-in asks about rounds, and the drift reading is a judgement
// the child never sees.
//
// It goes under the surface's wording rather than into it, the way a steer's
// repeat count does, so an operator who replaced the words did not also
// replace this.
func budgetNote(written []string) string {
	if len(written) == 0 {
		return "You have not written to any file yet. Reading and searching spend the same attention that changing something does, and leave nothing behind: if you already know enough to make the change, make it now rather than reading further."
	}
	names, more := written, ""
	if len(names) > maxCheckInWritten {
		more = fmt.Sprintf(" and %d more", len(names)-maxCheckInWritten)
		names = names[:maxCheckInWritten]
	}
	return fmt.Sprintf("So far you have written %d %s: %s%s. Say which of those you consider finished and what is still to be written.",
		len(written), plural(len(written), "file"), strings.Join(names, ", "), more)
}

// FinishedInSession and FinishedAsSubAgent are the closing lines for the two
// kinds of turn that take stock.
const (
	FinishedInSession  = "If the work is in fact finished, say so instead."
	FinishedAsSubAgent = "If the work is in fact finished, give your final report instead."
)

// TakeCheckIn returns the check-in the turn is due, and marks it taken.
//
// It is one call rather than a predicate and a setter because the two must
// not come apart: a caller that asks and forgets to mark gets a check-in
// every round for the rest of the turn, which is the opposite of the
// mechanism. Not due returns ok=false and changes nothing.
func (a *Agent) TakeCheckIn() (prompt string, ok bool) {
	if a.rounds <= 0 {
		return "", false
	}
	// Either clock being due is a check-in due, and the spend clock is asked
	// first because it is what decides the wording: a boundary both clocks
	// fall on is still a budget check-in, and the round it landed on is no
	// reason to leave out the one fact the spend can add.
	onSpend := a.spendDue()
	if !onSpend && a.rounds-a.lastIntervention < a.checkInInterval() {
		return "", false
	}
	a.NoteIntervention()
	// Only the clock's own check-ins widen the interval. A steer is a
	// different question with a reason behind it, and one turn's worth of
	// them should not make the generic question rarer.
	a.checkIns++
	prompt = a.steering.checkInPrompt(a.rounds)
	if onSpend {
		prompt += "\n\n" + budgetNote(supplied(a.written))
	}
	return prompt, true
}

// ForceCheckIn returns the check-in unconditionally and marks it taken. It is
// for a caller holding a reason the interval cannot see — a reading that says
// the session already has what it needs — and it is additional to the clock,
// never a replacement for it: TakeCheckIn still fires on schedule for every
// session that has no reading to go on.
func (a *Agent) ForceCheckIn() string {
	a.NoteIntervention()
	return a.steering.checkInPrompt(a.rounds)
}

// CheckInInterval is the rounds owed before the next check-in, for a caller
// that needs to state what it configured — a status line, or a test in the
// package that set it.
func (a *Agent) CheckInInterval() int { return a.checkInInterval() }

// CheckInMessage is the check-in this agent would ask at the round it has
// reached. It is for a caller holding a reason of its own — a sub-agent's
// round cap, which is a check-in rather than a stop — and it marks nothing:
// TakeCheckIn and ForceCheckIn are the two that do.
//
// It goes through the agent rather than through CheckInPrompt so a caller
// that has its own reason still asks in the surface's own wording, closing on
// the surface's own exit. A second route to the same message is how one of
// them ends up saying something the operator replaced everywhere else — and
// the closing line is not the caller's to choose for the same reason: the
// round cap was the only route that named the report, so a child asked by its
// clock was told to say so to nobody.
func (a *Agent) CheckInMessage() string {
	return a.steering.checkInPrompt(a.rounds)
}

// NoteIntervention records that something has just asked the turn to take
// stock, so the next check-in is counted from here.
//
// The interval is measured from the last intervention rather than from the
// start of the turn, because a steer is a check-in with better evidence: a
// turn that has just been asked what it is doing does not need asking again
// forty rounds after some earlier boundary. It also means a skipped round can
// never skip a check-in, which a modulo would.
//
// Both clocks are marked, for that reason and not only for tidiness: a turn
// asked what it is doing does not need asking again because it has since
// spent another share of its budget on answering.
func (a *Agent) NoteIntervention() {
	a.lastIntervention = a.rounds
	a.markSpend()
}
