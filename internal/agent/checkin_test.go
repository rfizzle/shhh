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
	ceiling := DefaultCheckInInterval << DefaultCheckInDoublings
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
	long := strings.Repeat("x", DefaultSteerTargetChars*2)
	p := SteerPrompt(long, "")
	if strings.Contains(p, long) {
		t.Error("the anchor went in whole; it has no length limit and the steer does")
	}
	if !strings.Contains(p, strings.Repeat("x", DefaultSteerTargetChars-1)) {
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

// A turn's rounds and its budget are different clocks, and the one that ends
// a sub-agent is the budget. A child making few large calls reaches the end
// of its budget long before the round interval comes round, so the spend asks
// on its own.
func TestTakeCheckIn_AsksOnTheBudgetAsWellAsTheRounds(t *testing.T) {
	a := New(nil, noStream)
	a.SetCheckInInterval(25)
	var spent int64
	a.SetCheckInBudget(func() (int64, int64) { return spent, 200_000 }, nil)

	// Five rounds, each taking in a tenth of the budget: nothing the round
	// clock can see, and half the child's life.
	var at []int
	for r := 1; r <= 5; r++ {
		a.rounds = r
		spent += 20_000
		if _, ok := a.TakeCheckIn(); ok {
			at = append(at, r)
		}
	}
	if len(at) != 1 || at[0] != 3 {
		t.Fatalf("budget check-ins at rounds %v, want one at round 3 — a quarter of the budget in", at)
	}
}

// The budget clock widens off the same count the round clock does, so a turn
// already asked twice is not asked at the narrow interval by the other one.
func TestTakeCheckIn_TheBudgetClockWidensWithTheRounds(t *testing.T) {
	a := New(nil, noStream)
	var spent int64
	a.SetCheckInBudget(func() (int64, int64) { return spent, 400 }, nil)

	var at []int64
	for r := 1; r <= 40; r++ {
		a.rounds = r
		spent += 10
		if _, ok := a.TakeCheckIn(); ok {
			at = append(at, spent)
		}
	}
	// A quarter, then a half; the third would fall past the whole budget,
	// which is a child that has already died of it.
	want := []int64{100, 300}
	if len(at) != len(want) {
		t.Fatalf("check-ins at %v tokens, want %v", at, want)
	}
	for i := range want {
		if at[i] != want[i] {
			t.Fatalf("check-ins at %v tokens, want %v", at, want)
		}
	}
}

// A turn that has spent a share of its budget and written nothing is asked
// about that specifically: the whole reason to ask on spend is that spending
// is not progress.
func TestTakeCheckIn_TheBudgetQuestionNamesTheWrites(t *testing.T) {
	a := New(nil, noStream)
	written := []string{}
	var spent int64
	a.SetCheckInBudget(func() (int64, int64) { return spent, 200 }, func() []string { return written })

	spent = 100
	a.rounds = 1
	prompt, ok := a.TakeCheckIn()
	if !ok {
		t.Fatal("half the budget in, the check-in is due")
	}
	if !strings.Contains(prompt, "not written to any file") {
		t.Errorf("a turn that has written nothing should be asked about it:\n%s", prompt)
	}
	if !strings.Contains(prompt, "take stock") {
		t.Errorf("the budget note goes under the surface's own wording, not in place of it:\n%s", prompt)
	}

	written = []string{"internal/agent/agent.go", "internal/agent/checkin.go"}
	spent = 300
	a.rounds = 2
	prompt, ok = a.TakeCheckIn()
	if !ok {
		t.Fatal("another share of the budget in, the check-in is due again")
	}
	for _, want := range []string{"2 files", "internal/agent/agent.go", "internal/agent/checkin.go"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("missing %q from:\n%s", want, prompt)
		}
	}
}

// A check-in the round clock asked says nothing about writes. There is no
// budget behind it to make the writes the point, and a session asked what it
// has changed every forty rounds is a different mechanism from this one.
func TestTakeCheckIn_TheRoundQuestionIsUnchanged(t *testing.T) {
	a := New(nil, noStream)
	a.rounds = DefaultCheckInInterval
	prompt, ok := a.TakeCheckIn()
	if !ok {
		t.Fatal("the round check-in is due")
	}
	if prompt != CheckInPrompt(DefaultCheckInInterval, FinishedInSession) {
		t.Errorf("a turn with no budget should be asked the built-in wording:\n%s", prompt)
	}
}

// A steer is a check-in with better evidence on either clock: a turn that has
// just been asked what it is doing must not be asked again because answering
// spent another share of its budget.
func TestTakeCheckIn_ASteerPostponesTheBudgetClock(t *testing.T) {
	a := New(nil, noStream)
	spent := int64(40)
	a.SetCheckInBudget(func() (int64, int64) { return spent, 200 }, nil)

	a.rounds = 1
	a.TakeSteer("ship the parser", "reading unrelated files")
	spent = 80
	a.rounds = 2
	if _, ok := a.TakeCheckIn(); ok {
		t.Error("a turn steered one round ago must not also be asked a budget check-in")
	}
	spent = 130
	a.rounds = 3
	if _, ok := a.TakeCheckIn(); !ok {
		t.Error("a full share past the steer, the budget check-in is due again")
	}
}

// A child's budget is the whole of its life and its second turn opens on
// whatever the first one left, so the mark moves with the turn rather than
// back to zero — or every turn after the first opens on a check-in about the
// turn before it.
func TestStartTurn_CarriesTheSpendMark(t *testing.T) {
	a := New(nil, noStream)
	spent := int64(150)
	a.SetCheckInBudget(func() (int64, int64) { return spent, 200 }, nil)

	a.StartTurn("carry on")
	a.rounds = 1
	if _, ok := a.TakeCheckIn(); ok {
		t.Error("a fresh turn must not be asked a budget check-in for what the turn before it spent")
	}
	spent = 200
	if _, ok := a.TakeCheckIn(); !ok {
		t.Error("a share spent inside this turn is due a check-in")
	}
}

// A surface with no budget runs on the round clock alone, which is every
// session: nothing here may fire on a turn that was never given one.
func TestTakeCheckIn_NoBudgetIsNoSecondClock(t *testing.T) {
	a := New(nil, noStream)
	a.SetCheckInBudget(func() (int64, int64) { return 1 << 40, 0 }, nil)
	at := checkInRounds(a, 120)
	want := []int{40, 120}
	if len(at) != len(want) || at[0] != want[0] || at[1] != want[1] {
		t.Fatalf("check-ins at %v, want the round clock's %v", at, want)
	}
}
