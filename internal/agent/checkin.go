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

// CheckInInterval is how many tool rounds pass before a turn is asked to take
// stock. It sits well under DefaultMaxToolRounds because the cap is the
// checkpoint for the human and this is the one the turn does for itself; a
// check-in that only ever arrived with the cap would be the pause the person
// is already there for.
//
// Forty is about the length of a thorough investigation that is going
// somewhere. Ordinary work finishes inside one and never sees the question.
const CheckInInterval = 40

// CheckInPrompt is the turn handed to a session that has reached a check-in.
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
	if a.rounds <= 0 || a.rounds-a.lastIntervention < CheckInInterval {
		return "", false
	}
	a.NoteIntervention()
	return CheckInPrompt(a.rounds, FinishedInSession), true
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
