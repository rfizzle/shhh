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

import "fmt"

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

// CheckInPrompt is the built-in turn handed to a session that has reached a
// check-in.
//
// It asks about the work rather than announcing a budget, on purpose: told it
// is running out, a model apologises and stops; asked what is left, it says
// so and carries on. The last line is the one that matters for a turn that
// has quietly finished — it gives it somewhere to go other than more reading.
// The closing line is the caller's because a session and a sub-agent finish
// differently: one reports to the person in front of it, the other has a
// final report that is its whole deliverable.
func CheckInPrompt(used int, whenFinished string) string {
	return fmt.Sprintf(`You have used %d tool rounds. This is a routine check-in, not a stop — nothing has gone wrong and nothing is running out.

Briefly take stock:
- what you have established or changed so far
- what is still left to do
- what you are doing next

Then carry on with the task. If you already know enough to act, stop looking and start work — more reading is not more progress. %s`, used, whenFinished)
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
	if a.rounds <= 0 || a.rounds-a.lastIntervention < a.checkInInterval() {
		return "", false
	}
	a.NoteIntervention()
	// Only the clock's own check-ins widen the interval. A steer is a
	// different question with a reason behind it, and one turn's worth of
	// them should not make the generic question rarer.
	a.checkIns++
	return a.steering.checkInPrompt(a.rounds, FinishedInSession), true
}

// ForceCheckIn returns the check-in unconditionally and marks it taken. It is
// for a caller holding a reason the interval cannot see — a reading that says
// the session already has what it needs — and it is additional to the clock,
// never a replacement for it: TakeCheckIn still fires on schedule for every
// session that has no reading to go on.
func (a *Agent) ForceCheckIn() string {
	a.NoteIntervention()
	return a.steering.checkInPrompt(a.rounds, FinishedInSession)
}

// CheckInInterval is the rounds owed before the next check-in, for a caller
// that needs to state what it configured — a status line, or a test in the
// package that set it.
func (a *Agent) CheckInInterval() int { return a.checkInInterval() }

// CheckInMessage is the check-in this agent would ask at the round it has
// reached, closing with whenFinished. It is for a caller holding a reason of
// its own — a sub-agent's round cap, which is a check-in rather than a stop —
// and it marks nothing: TakeCheckIn and ForceCheckIn are the two that do.
//
// It goes through the agent rather than through CheckInPrompt so a caller
// that has its own reason still asks in the surface's own wording. A second
// route to the same message is how one of them ends up saying something the
// operator replaced everywhere else.
func (a *Agent) CheckInMessage(whenFinished string) string {
	return a.steering.checkInPrompt(a.rounds, whenFinished)
}

// NoteIntervention records that something has just asked the turn to take
// stock, so the next check-in is counted from here.
//
// The interval is measured from the last intervention rather than from the
// start of the turn, because a steer is a check-in with better evidence: a
// turn that has just been asked what it is doing does not need asking again
// forty rounds after some earlier boundary. It also means a skipped round can
// never skip a check-in, which a modulo would.
func (a *Agent) NoteIntervention() { a.lastIntervention = a.rounds }
