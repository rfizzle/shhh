package agent

import (
	"strconv"
	"strings"
	"testing"
)

// The interval widens as a turn goes on: often enough early to catch one
// working on the wrong thing, rare enough later to stay out of the way of one
// that is committed.
func TestTakeCheckIn_WidensAsTheTurnGoesOn(t *testing.T) {
	a := New(nil, noStream)
	at := checkInRounds(a, 600)
	want := []int{40, 120, 280, 440, 600} // 40, then +80, +160, and +160 on
	if len(at) != len(want) {
		t.Fatalf("check-ins at %v, want %v", at, want)
	}
	for i := range want {
		if at[i] != want[i] {
			t.Fatalf("check-ins at %v, want %v", at, want)
		}
	}
}

// The widening is bounded, or a turn that survives a few check-ins is never
// questioned again — the same failure on a longer timescale.
func TestTakeCheckIn_TheWideningIsBounded(t *testing.T) {
	a := New(nil, noStream)
	at := checkInRounds(a, 4000)
	if len(at) < 6 {
		t.Fatalf("a long run should keep being asked, got %v", at)
	}
	last := at[len(at)-1] - at[len(at)-2]
	ceiling := DefaultCheckInInterval << maxCheckInDoublings
	if last > ceiling {
		t.Errorf("the gap grew to %d rounds, past the ceiling of %d", last, ceiling)
	}
	// And it really does stop growing rather than creeping.
	if prev := at[len(at)-2] - at[len(at)-3]; prev != last {
		t.Errorf("gaps %d then %d — the widening should have levelled off", prev, last)
	}
}

// A surface with less watching it than a session sets its own, shorter.
func TestSetCheckInInterval_IsPerSurface(t *testing.T) {
	a := New(nil, noStream)
	a.SetCheckInInterval(25)
	at := checkInRounds(a, 200)
	want := []int{25, 75, 175} // 25, then +50, then +100
	for i := range want {
		if i >= len(at) || at[i] != want[i] {
			t.Fatalf("check-ins at %v, want them to start %v", at, want)
		}
	}

	a = New(nil, noStream)
	a.SetCheckInInterval(0)
	if got := a.checkInInterval(); got != DefaultCheckInInterval {
		t.Errorf("zero should restore the default, got %d", got)
	}
}

// checkInRounds plays a turn out to n rounds and returns the rounds a check-in
// fell on.
func checkInRounds(a *Agent, n int) []int {
	var at []int
	for r := 1; r <= n; r++ {
		a.rounds = r
		if _, ok := a.TakeCheckIn(); ok {
			at = append(at, r)
		}
	}
	return at
}

func TestTakeCheckIn_NotAtZeroRounds(t *testing.T) {
	a := New(nil, noStream)
	if _, ok := a.TakeCheckIn(); ok {
		t.Error("a turn that has run no rounds has nothing to take stock of")
	}
}

// The interval runs from the last intervention, not from the turn's start, so
// a steer pushes the next check-in out rather than letting both land together.
func TestTakeCheckIn_CountsFromTheLastIntervention(t *testing.T) {
	a := New(nil, noStream)
	a.rounds = DefaultCheckInInterval - 1
	a.NoteIntervention() // a steer, one round short of the boundary

	a.rounds = DefaultCheckInInterval
	if _, ok := a.TakeCheckIn(); ok {
		t.Error("a turn steered one round ago must not also be asked to check in")
	}
	a.rounds = DefaultCheckInInterval*2 - 2
	if _, ok := a.TakeCheckIn(); ok {
		t.Error("check-in came early: the interval runs from the intervention")
	}
	a.rounds = DefaultCheckInInterval*2 - 1
	if _, ok := a.TakeCheckIn(); !ok {
		t.Error("a full interval after the steer, the check-in is due again")
	}
}

// The user's own message is the most direct stock-take there is, and it puts
// the counter back to zero.
func TestResetRounds_ClearsTheInterventionMark(t *testing.T) {
	a := New(nil, noStream)
	a.rounds = DefaultCheckInInterval
	a.NoteIntervention()
	a.ResetRounds()

	a.rounds = DefaultCheckInInterval
	if _, ok := a.TakeCheckIn(); !ok {
		t.Error("after a user message the interval restarts from zero")
	}
}

// A steer restarts the interval but does not widen it: it is a different
// question with a reason behind it, and a turn that drifted twice should not
// be asked the generic question less often for it.
func TestTakeSteer_DoesNotWidenTheInterval(t *testing.T) {
	a := New(nil, noStream)
	for i := 0; i < 3; i++ {
		a.rounds = (i + 1) * 10
		a.TakeSteer("ship it", "wandering")
	}
	if got := a.checkInInterval(); got != DefaultCheckInInterval {
		t.Errorf("three steers widened the interval to %d", got)
	}
	a.rounds = 30 + DefaultCheckInInterval
	if _, ok := a.TakeCheckIn(); !ok {
		t.Fatal("a full interval after the last steer, the check-in is due")
	}
}

// A steer is a check-in with better evidence, so it counts as one.
func TestTakeSteer_CountsAsAnIntervention(t *testing.T) {
	a := New(nil, noStream)
	a.rounds = DefaultCheckInInterval
	got := a.TakeSteer("ship the parser", "has been reading unrelated files")
	if !strings.Contains(got, "ship the parser") {
		t.Error("the steer should quote the instruction it was judged against")
	}
	if _, ok := a.TakeCheckIn(); ok {
		t.Error("a steer must postpone the check-in, not arrive alongside it")
	}
}

// The judge is a cheap model reading a digest. A steer that asserts the work
// has gone wrong derails a session that was in fact on task.
func TestSteerPrompt_AsksRatherThanAccuses(t *testing.T) {
	p := SteerPrompt("build the exporter", "editing files outside the exporter")
	for _, want := range []string{
		"may have moved away",
		"build the exporter",
		"editing files outside the exporter",
		"can be wrong",
		"Do not restart work you have already finished",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("missing %q from:\n%s", want, p)
		}
	}
}

func TestSteerPrompt_SurvivesAnEmptyReason(t *testing.T) {
	p := SteerPrompt("build the exporter", "")
	if strings.Contains(p, "What the check noticed") {
		t.Error("an empty reason should not leave a dangling label")
	}
	if !strings.Contains(p, "build the exporter") {
		t.Error("the instruction is still quoted")
	}
}

func TestSteerPrompt_BoundsTheInstruction(t *testing.T) {
	long := strings.Repeat("x", maxSteerTarget*2)
	p := SteerPrompt(long, "")
	if strings.Contains(p, long) {
		t.Error("the anchor went in whole; it has no length limit and the steer does")
	}
	if !strings.Contains(p, strings.Repeat("x", maxSteerTarget-1)) {
		t.Error("the anchor was clamped far shorter than the bound")
	}
}

// The check-in has to arrive well before the cap: at the cap the session
// stops and asks the person, which is the intervention it exists to spare
// them.
func TestCheckInInterval_ComesWellBeforeTheCap(t *testing.T) {
	if DefaultCheckInInterval >= DefaultMaxToolRounds {
		t.Fatalf("check-in interval %d must fall inside the round cap %d", DefaultCheckInInterval, DefaultMaxToolRounds)
	}
	if got := DefaultMaxToolRounds / DefaultCheckInInterval; got < 2 {
		t.Errorf("a capped turn should take stock more than once; got %d check-ins", got)
	}
}

func TestCheckInPrompt_AsksForStockNotForMoreWork(t *testing.T) {
	p := CheckInPrompt(40, FinishedInSession)
	if !strings.Contains(p, strconv.Itoa(40)) {
		t.Error("the prompt should say how many rounds have gone")
	}
	for _, want := range []string{"not a stop", "what is still left", "stop looking and start work"} {
		if !strings.Contains(p, want) {
			t.Errorf("missing %q from:\n%s", want, p)
		}
	}
	if strings.Contains(p, FinishedAsSubAgent) {
		t.Error("a session has a person to tell, not a report to file")
	}
	if !strings.Contains(CheckInPrompt(1, FinishedAsSubAgent), "final report") {
		t.Error("a sub-agent's check-in should point at its deliverable")
	}
}
