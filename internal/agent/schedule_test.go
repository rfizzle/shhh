package agent

import (
	"testing"
	"time"
)

// The three bounds the two surfaces used to keep a copy of each are exercised
// through those surfaces (summaryrun_test.go, and the session's own). What is
// tested here is the fourth, and the guarantees the other three now owe it.

const scheduleInterval = 10

// An interruption restarts the schedule the way the start of a turn does: the
// reading that says whether the steer took comes a few rounds after it rather
// than a whole interval after the reading that earned it.
func TestSummarySchedule_AnInterventionPullsTheNextReadingForward(t *testing.T) {
	var s SummarySchedule
	s.Read(10)
	s.Intervened(11)

	for round := 11; round < 11+FirstSummaryRound; round++ {
		if s.Due(round, scheduleInterval, 0) {
			t.Fatalf("a reading came due at round %d, %d rounds after the steer", round, round-11)
		}
	}
	if !s.Due(11+FirstSummaryRound, scheduleInterval, 0) {
		t.Fatalf("a reading is due %d rounds after a steer, well inside the interval of %d",
			FirstSummaryRound, scheduleInterval)
	}

	// The ordinary case is the interruption sharing the reading's round: the
	// verdict queues at round N and the boundary at the end of round N is
	// where it is delivered.
	var same SummarySchedule
	same.Read(10)
	same.Intervened(10)
	if !same.Due(10+FirstSummaryRound, scheduleInterval, 0) {
		t.Fatal("a steer delivered at the round that was read still restarts the schedule")
	}
}

// It is an extra way for a reading to fall due and never a later one. A
// schedule that counted only from the interruption would let one silence the
// rail for longer than the interval it replaced.
func TestSummarySchedule_AnInterventionNeverPushesAReadingBack(t *testing.T) {
	var s SummarySchedule
	s.Read(1)
	s.Intervened(11)
	if !s.Due(11, scheduleInterval, 0) {
		t.Fatal("the interval had come round; an interruption must not postpone it")
	}
}

// The wall-clock floor is not one of the rules an interruption restarts. It
// exists because rounds are not evenly spaced in time, and a burst of fast
// rounds after a steer is exactly the case it was written for.
func TestSummarySchedule_TheFloorHoldsUnderAnIntervention(t *testing.T) {
	var s SummarySchedule
	s.Read(10)
	s.Intervened(11)
	if s.Due(11+FirstSummaryRound, scheduleInterval, time.Hour) {
		t.Fatal("the floor holds a reading back however good the reason for it")
	}
}

// One interruption buys one early reading. Once that reading lands the
// interval is back in force, or a steered turn would be read every three
// rounds for the rest of it.
func TestSummarySchedule_TheInterventionIsSpentByTheReadingItEarned(t *testing.T) {
	var s SummarySchedule
	s.Read(10)
	s.Intervened(11)
	s.Read(14)
	if s.Due(14+FirstSummaryRound, scheduleInterval, 0) {
		t.Fatal("the steer earned one early reading, not a shorter interval")
	}
	if !s.Due(14+scheduleInterval, scheduleInterval, 0) {
		t.Fatal("the interval is back in force after the reading the steer earned")
	}
}

// Moved is the turn's close asking whether there is anything new to say. The
// round counter is the obvious half; an interruption is the other, because it
// is the one thing that changes the answer without moving the counter.
func TestSummarySchedule_MovedCountsAnInterventionAsSomethingHappening(t *testing.T) {
	var read SummarySchedule
	read.Read(6)
	if read.Moved(6) {
		t.Fatal("a turn read this round, uninterrupted, has nothing new to say")
	}
	if !read.Moved(7) {
		t.Fatal("a round taken since the reading is something new")
	}

	steered := read
	steered.Intervened(6)
	if !steered.Moved(6) {
		t.Fatal("a steer delivered at the round that was read is something new")
	}
}

// A reading that did not come back moves the clock and not the round: the
// block's age is measured against the last reading that actually said
// something.
func TestSummarySchedule_AMissedReadingMovesOnlyTheClock(t *testing.T) {
	var s SummarySchedule
	s.Read(10)
	s.Missed()
	if s.LastRound() != 10 {
		t.Fatalf("the round bound moved on a failure: %d", s.LastRound())
	}
	if s.Due(11, scheduleInterval, time.Hour) {
		t.Fatal("the floor is measured from the attempt, so a failing provider is not asked every round")
	}
}
