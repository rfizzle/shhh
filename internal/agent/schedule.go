package agent

// When a run's next reading is due.
//
// Two surfaces take readings of a running turn — the session, which draws
// them on a rail and acts on them, and the unattended run, which only acts on
// them — and both schedule against the same three bounds: the first reading
// of a turn comes early, the ones after it come on the interval, and a
// wall-clock floor holds under both because rounds are not evenly spaced in
// time. Those were two copies of one predicate, agreeing by inspection, and
// every rule added to one of them was a rule that could be forgotten in the
// other. This is the one copy.
//
// The rule that made a second copy untenable is the fourth bound here. An
// interruption is the machinery telling the model something, which is the one
// event that changes what a reading would say without the run having done
// anything, so it restarts the schedule the way the start of a turn does: the
// reading that says whether the steer took arrives a few rounds after it
// rather than a whole interval later. The interval is a cost; an interruption
// is a reason.
// See docs/capabilities/coding-agent.md#two-failures-two-interruptions.

import "time"

// SummarySchedule is when the next reading of one turn is due. The zero value
// is a turn that has never been read and has not been interrupted, which is
// what the start of a turn restores.
type SummarySchedule struct {
	// lastRound is the round the last reading was taken at, and zero when
	// there has been none. Zero is a safe "none": the first reading of a turn
	// is never taken before FirstSummaryRound.
	lastRound int
	// lastAt is the wall clock the floor is measured from.
	lastAt time.Time
	// intervenedRound is the round an interruption was last delivered at, and
	// zero when none has been — an interruption is only ever delivered at a
	// round boundary, which is reached after a round of tool results and so
	// never at round zero.
	intervenedRound int
}

// Due reports whether a reading should be taken now, against the interval and
// the wall-clock floor in force. The caller's own bounds stay with the caller:
// whether readings are configured at all, and whether one is already in
// flight, are things only it can answer.
func (s SummarySchedule) Due(rounds, interval int, gap time.Duration) bool {
	if !s.roundsDue(rounds, interval) {
		return false
	}
	// The floor is checked last because it is the one that catches a burst of
	// fast read-only rounds, which is the case the round interval cannot see.
	return time.Since(s.lastAt) >= gap
}

// roundsDue is the round half of the schedule: whichever of its counts comes
// round first.
func (s SummarySchedule) roundsDue(rounds, interval int) bool {
	if s.lastRound == 0 {
		// The first reading comes early, so a long turn has a verdict to act
		// on well before a whole interval has gone by.
		return rounds >= FirstSummaryRound
	}
	if rounds-s.lastRound >= interval {
		return true
	}
	// An interruption delivered since the last reading is counted from on the
	// same short interval a turn start uses, because the reading it would
	// otherwise replace is the verdict that earned the interruption and the
	// reader has been given something new to judge. It is an extra way for a
	// reading to fall due and never a later one: a schedule that could push a
	// reading back would let an interruption silence the rail.
	//
	// At or after, not after: the ordinary case is an interruption delivered
	// at the boundary of the very round the reading that earned it was taken
	// at, and a reading already in flight when it was delivered knows nothing
	// about it either way.
	return s.intervenedRound >= s.lastRound && rounds-s.intervenedRound >= FirstSummaryRound
}

// Read records a reading taken at this round: both bounds move.
func (s *SummarySchedule) Read(round int) {
	s.lastRound = round
	s.lastAt = time.Now()
}

// Missed records a reading that did not come back. Only the wall clock moves,
// so a provider that is refusing is asked again on the floor rather than on
// the next round, while the round bound stays with the last reading that
// actually said something — which is what a block's age is measured against.
func (s *SummarySchedule) Missed() { s.lastAt = time.Now() }

// Intervened records an interruption delivered at this round.
func (s *SummarySchedule) Intervened(round int) { s.intervenedRound = round }

// LastRound is the round of the last reading, and zero when there has been
// none.
func (s SummarySchedule) LastRound() int { return s.lastRound }

// Moved reports whether anything has happened since the last reading: the
// round counter went past it, or an interruption was delivered at or after
// it. A turn's close asks this before spending a reading on a turn with
// nothing new to say.
//
// Rounds are the only clock a reading has, and an interruption is the one
// thing that changes the answer without moving them: a steer delivered at the
// boundary after the reading that earned it, answered in words with no tool
// call, ends the turn at the round it started. Without the second half of
// this the verdict left on screen is the off-target one the steer was the
// answer to.
//
// A turn that has never been read has always moved, whatever its round: there
// is no reading on screen for it to have nothing new to say against.
func (s SummarySchedule) Moved(rounds int) bool {
	return rounds > s.lastRound || s.intervenedRound >= s.lastRound
}

// CloseDue reports whether the reading a turn ends on is worth taking. It
// ignores the interval, because the close is the reading that stands after
// the work has stopped and a turn that finished at round 7 with a reading
// from round 3 would be describing its own middle. What it does ask is that
// the turn was long enough to be worth reading at all
// (SummaryCloseMinRounds) and that something has happened since the last
// reading (Moved).
//
// Both surfaces close on this one predicate: the session, where the verdict
// is what sits on the rail while nothing else moves, and the unattended run,
// where it is the last thing the record says about the turn.
func (s SummarySchedule) CloseDue(rounds int) bool {
	return rounds >= SummaryCloseMinRounds && s.Moved(rounds)
}
