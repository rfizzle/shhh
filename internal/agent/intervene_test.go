package agent

import (
	"strings"
	"testing"
)

// working is what a front-end answers about its own turn; these tests are
// about a turn that is running.
const running = true

func driftVerdict(round int) SummaryVerdict {
	return SummaryVerdict{State: SummaryOffTarget, Round: round, Reason: "editing files outside the exporter"}
}

func enoughVerdict(round int) SummaryVerdict {
	return SummaryVerdict{State: SummarySufficient, Round: round, Reason: "has named the file and the line"}
}

func TestConsiderVerdict_DriftEarnsASteer(t *testing.T) {
	a := New(nil, noStream)
	a.rounds = 5
	a.ConsiderVerdict(driftVerdict(5), running)

	iv, ok := a.NextIntervention("build the exporter")
	if !ok {
		t.Fatal("a drifting reading should earn an interruption")
	}
	if iv.Kind != InterveneSteer {
		t.Fatalf("kind = %v, want InterveneSteer", iv.Kind)
	}
	if !strings.Contains(iv.Message, "build the exporter") ||
		!strings.Contains(iv.Message, "editing files outside the exporter") {
		t.Errorf("the steer should carry the instruction and the reason:\n%s", iv.Message)
	}
	if !strings.Contains(iv.Notice, "Steered") {
		t.Errorf("notice = %q", iv.Notice)
	}
	if iv.Kind.Signal() != "steer" {
		t.Errorf("signal = %q", iv.Kind.Signal())
	}
}

// Sufficiency asks the ordinary question early. There is nothing to accuse
// the turn of, so the message must not read like an accusation.
func TestConsiderVerdict_SufficiencyEarnsAnEarlyCheckIn(t *testing.T) {
	a := New(nil, noStream)
	a.rounds = 5
	a.ConsiderVerdict(enoughVerdict(5), running)

	iv, ok := a.NextIntervention("build the exporter")
	if !ok || iv.Kind != InterveneEnough {
		t.Fatalf("kind = %v ok = %v, want InterveneEnough", iv.Kind, ok)
	}
	if !strings.Contains(iv.Message, "routine check-in") {
		t.Errorf("expected the ordinary check-in message:\n%s", iv.Message)
	}
	if strings.Contains(iv.Message, "moved away") {
		t.Error("sufficiency must not accuse the turn of drifting")
	}
	if !strings.Contains(iv.Notice, "take stock early") {
		t.Errorf("notice = %q", iv.Notice)
	}
}

func TestConsiderVerdict_OnTargetAndUnclearEarnNothing(t *testing.T) {
	for _, state := range []SummaryState{SummaryOnTarget, SummaryUncertain} {
		a := New(nil, noStream)
		a.rounds = 5
		a.ConsiderVerdict(SummaryVerdict{State: state, Round: 5}, running)
		if _, ok := a.NextIntervention("x"); ok {
			t.Errorf("%v should not interrupt the turn", state)
		}
	}
}

// A failed reading is not a reading. The last verdict stands and nothing acts
// on the failure.
func TestConsiderVerdict_AFailedReadingActsOnNothing(t *testing.T) {
	a := New(nil, noStream)
	a.rounds = 5
	a.ConsiderVerdict(SummaryVerdict{State: SummaryOffTarget, Round: 5, Failed: true}, running)
	if _, ok := a.NextIntervention("x"); ok {
		t.Fatal("a failed reading must not steer")
	}
}

// A closing reading arrives after the turn has stopped; there is nothing left
// to interrupt.
func TestConsiderVerdict_AnIdleTurnIsNotInterrupted(t *testing.T) {
	a := New(nil, noStream)
	a.rounds = 5
	a.ConsiderVerdict(driftVerdict(5), false)
	if _, ok := a.NextIntervention("x"); ok {
		t.Fatal("a finished turn must not be steered")
	}
}

// A reading stands for several rounds. It gets one say, not one per round.
func TestNextIntervention_OneReadingActsOnce(t *testing.T) {
	a := New(nil, noStream)
	a.rounds = 5
	a.ConsiderVerdict(driftVerdict(5), running)
	if _, ok := a.NextIntervention("x"); !ok {
		t.Fatal("setup: expected the first steer")
	}
	a.ConsiderVerdict(driftVerdict(5), running) // the same reading again
	if _, ok := a.NextIntervention("x"); ok {
		t.Fatal("a reading that has already acted must not act again")
	}
}

// A queued verdict is a claim about a turn, and a fresher reading can
// withdraw it. Delivering it after that would steer a session against
// evidence the session already has.
func TestConsiderVerdict_ALaterReadingRetiresAQueuedSteer(t *testing.T) {
	a := New(nil, noStream)
	a.rounds = 5
	a.ConsiderVerdict(driftVerdict(5), running)
	a.rounds = 8
	a.ConsiderVerdict(SummaryVerdict{State: SummaryOnTarget, Round: 8}, running)
	if _, ok := a.NextIntervention("x"); ok {
		t.Fatal("a steer whose case was withdrawn by a later reading must not be delivered")
	}

	// A reading that could not judge retires one too: the queue is a claim
	// the evidence no longer supports, and an unclear reading is the evidence
	// having changed. An intervention on a shrug is worse than none, and so
	// is one on a shrug's predecessor.
	b := New(nil, noStream)
	b.rounds = 5
	b.ConsiderVerdict(driftVerdict(5), running)
	b.rounds = 8
	b.ConsiderVerdict(SummaryVerdict{State: SummaryUncertain, Round: 8}, running)
	if _, ok := b.NextIntervention("x"); ok {
		t.Fatal("an unclear reading after a drift one leaves nothing to deliver")
	}
}

// Only a fresher one, though. Readings come back out of order — a slow
// request asked at round 5 can land after a fast one asked at round 8 — and
// an older on-target reading knows nothing about the departure.
func TestConsiderVerdict_AnOlderReadingLeavesTheQueueAlone(t *testing.T) {
	a := New(nil, noStream)
	a.rounds = 8
	a.ConsiderVerdict(driftVerdict(8), running)
	a.ConsiderVerdict(SummaryVerdict{State: SummaryOnTarget, Round: 5}, running)
	if iv, ok := a.NextIntervention("x"); !ok || iv.Kind != InterveneSteer {
		t.Fatal("a reading older than the queued one must not retire it")
	}
}

// A failed reading is not evidence of anything, so it withdraws nothing.
func TestConsiderVerdict_AFailedReadingRetiresNothing(t *testing.T) {
	a := New(nil, noStream)
	a.rounds = 5
	a.ConsiderVerdict(driftVerdict(5), running)
	a.rounds = 8
	a.ConsiderVerdict(SummaryVerdict{State: SummaryOnTarget, Round: 8, Failed: true}, running)
	if _, ok := a.NextIntervention("x"); !ok {
		t.Fatal("a reading that did not happen must not retire the queued steer")
	}
}

// What the next reading is told about an interruption: the round, a word from
// a closed set, and the earlier reading's own reason. Nothing a tool wrote is
// anywhere near it.
func TestIntervention_RowIsTheRoundTheKindAndTheReason(t *testing.T) {
	steer := Intervention{Kind: InterveneSteer, Reason: "editing files outside the exporter"}
	if got := steer.Row(14); got != "round 14 · steered · editing files outside the exporter" {
		t.Errorf("steer row = %q", got)
	}
	early := Intervention{Kind: InterveneEnough, Reason: "has named the file and the line"}
	if got := early.Row(20); got != "round 20 · check-in · has named the file and the line" {
		t.Errorf("early check-in row = %q", got)
	}
	// The interval's own is owed for no reason but the clock, and a row that
	// invented one would be the digest saying something the turn was not told.
	if got := (Intervention{Kind: InterveneCheckIn}).Row(30); got != "round 30 · check-in" {
		t.Errorf("clock check-in row = %q", got)
	}
}

// The reason travels through delivery, because the row is built from the
// delivered interruption rather than from the verdict a front-end would have
// to keep beside it.
func TestNextIntervention_CarriesTheReadingsReason(t *testing.T) {
	a := New(nil, noStream)
	a.rounds = 5
	a.ConsiderVerdict(driftVerdict(5), running)
	iv, ok := a.NextIntervention("build the exporter")
	if !ok {
		t.Fatal("setup: expected the steer")
	}
	if iv.Reason != "editing files outside the exporter" {
		t.Fatalf("reason = %q", iv.Reason)
	}
	if got := iv.Row(5); got != "round 5 · steered · editing files outside the exporter" {
		t.Fatalf("row = %q", got)
	}
}

// Both verdict kinds share one cooldown, because both spend a round on the
// same interruption.
func TestNextIntervention_OneCooldownAcrossBothKinds(t *testing.T) {
	a := New(nil, noStream)
	a.SetInterveneCooldown(20)
	a.rounds = 5
	a.ConsiderVerdict(driftVerdict(5), running)
	if _, ok := a.NextIntervention("x"); !ok {
		t.Fatal("setup: expected the first steer")
	}

	a.rounds = 24 // 19 rounds on, one short
	a.ConsiderVerdict(enoughVerdict(24), running)
	if _, ok := a.NextIntervention("x"); ok {
		t.Fatal("a sufficiency reading inside the cooldown of a steer")
	}

	a.rounds = 25
	a.ConsiderVerdict(enoughVerdict(25), running)
	if _, ok := a.NextIntervention("x"); !ok {
		t.Fatal("past the cooldown the next reading acts")
	}
}

// The clock is the backstop and stays one: a turn with no reading to go on is
// still asked, which is the whole reason the check-in exists.
func TestNextIntervention_ClockFiresWithNoVerdictAtAll(t *testing.T) {
	a := New(nil, noStream)
	a.rounds = DefaultCheckInInterval
	iv, ok := a.NextIntervention("x")
	if !ok || iv.Kind != InterveneCheckIn {
		t.Fatalf("kind = %v ok = %v, want InterveneCheckIn", iv.Kind, ok)
	}
	if !strings.Contains(iv.Message, "routine check-in") {
		t.Errorf("message:\n%s", iv.Message)
	}
	if iv.Kind.Signal() != "check-in" {
		t.Errorf("signal = %q", iv.Kind.Signal())
	}
}

// The exit a check-in offers belongs to the surface, not to whichever route
// asked. A child's clock check-in and the one a sufficient reading brings
// forward both point at the final report that ends its turn; a session's
// point at the person in front of it. Only the round cap ever named the
// report before, so a child asked by its clock was told to say so to nobody.
func TestNextIntervention_TheFinishIsTheSurfaces(t *testing.T) {
	for _, tc := range []struct {
		name     string
		finished string
		want     string
		notWant  string
	}{
		{"a session", "", FinishedInSession, FinishedAsSubAgent},
		{"a child", FinishedAsSubAgent, FinishedAsSubAgent, FinishedInSession},
	} {
		check := func(route, msg string) {
			t.Helper()
			if !strings.Contains(msg, tc.want) {
				t.Errorf("%s, %s: missing %q from:\n%s", tc.name, route, tc.want, msg)
			}
			if strings.Contains(msg, tc.notWant) {
				t.Errorf("%s, %s: carries the other surface's exit %q", tc.name, route, tc.notWant)
			}
		}

		clock := New(nil, noStream)
		clock.SetFinished(tc.finished)
		clock.rounds = DefaultCheckInInterval
		iv, ok := clock.NextIntervention("build the exporter")
		if !ok || iv.Kind != InterveneCheckIn {
			t.Fatalf("%s: kind = %v ok = %v, want InterveneCheckIn", tc.name, iv.Kind, ok)
		}
		check("the clock", iv.Message)

		reading := New(nil, noStream)
		reading.SetFinished(tc.finished)
		reading.rounds = 5
		reading.ConsiderVerdict(enoughVerdict(5), running)
		iv, ok = reading.NextIntervention("build the exporter")
		if !ok || iv.Kind != InterveneEnough {
			t.Fatalf("%s: kind = %v ok = %v, want InterveneEnough", tc.name, iv.Kind, ok)
		}
		check("a sufficient reading", iv.Message)

		capped := New(nil, noStream)
		capped.SetFinished(tc.finished)
		capped.rounds = 25
		check("a caller with its own reason", capped.CheckInMessage())
		check("a forced check-in", capped.ForceCheckIn())
	}
}

// A reading wins over the clock: it is the same question asked for a reason,
// and asking both in one round is asking twice.
func TestNextIntervention_AReadingWinsOverTheClock(t *testing.T) {
	a := New(nil, noStream)
	a.rounds = DefaultCheckInInterval
	a.ConsiderVerdict(driftVerdict(DefaultCheckInInterval), running)

	iv, ok := a.NextIntervention("build the exporter")
	if !ok || iv.Kind != InterveneSteer {
		t.Fatalf("kind = %v, want InterveneSteer", iv.Kind)
	}
	// And the check-in is postponed rather than dropped.
	if _, ok := a.NextIntervention("x"); ok {
		t.Fatal("both interventions arrived in one round")
	}
	a.rounds += DefaultCheckInInterval
	if iv, ok := a.NextIntervention("x"); !ok || iv.Kind != InterveneCheckIn {
		t.Fatal("the check-in should return an interval after the steer")
	}
}

// A verdict about the last instruction must never be delivered against the
// next one.
func TestStartTurn_RetiresAQueuedVerdict(t *testing.T) {
	a := New(nil, noStream)
	a.rounds = 5
	a.ConsiderVerdict(driftVerdict(5), running)
	a.StartTurn("something else entirely")
	if _, ok := a.NextIntervention("x"); ok {
		t.Fatal("a new turn retires the queued verdict")
	}
}

// Sufficiency is a refinement of on target, never a departure.
func TestSummaryState_SufficiencyIsNotDrift(t *testing.T) {
	if SummarySufficient.Drifting() {
		t.Error("a session that has what it needs has not left its instruction")
	}
	if !SummarySufficient.Sufficient() {
		t.Error("Sufficient() should recognise its own state")
	}
	for _, s := range []SummaryState{SummaryOnTarget, SummaryOffTarget, SummaryUncertain} {
		if s.Sufficient() {
			t.Errorf("%v is not a sufficiency reading", s)
		}
	}
}

// A person's steer joins what was already asked, in the order they asked it.
// A steer is usually a refinement, and one that replaced the instruction would
// make the work the turn was asked for first read as a departure.
func TestExtendTarget_AddsTheSteerToWhatWasAlreadyAsked(t *testing.T) {
	const asked = "make the round limit a checkpoint"
	got := ExtendTarget(asked, "also check the tests")
	if want := asked + "\n\nalso check the tests"; got != want {
		t.Fatalf("extended target = %q, want %q", got, want)
	}
	if got := ExtendTarget(got, "and skip the docs"); !strings.HasSuffix(got, "and skip the docs") ||
		!strings.Contains(got, "also check the tests") || !strings.HasPrefix(got, asked) {
		t.Fatalf("a second steer goes on the end, got %q", got)
	}
	if got := ExtendTarget(asked, "   \n "); got != asked {
		t.Fatalf("an empty steer moves nothing, got %q", got)
	}
	if got := ExtendTarget("", "start here"); got != "start here" {
		t.Fatalf("a steer with no anchor is the target, got %q", got)
	}
}

// The bound on what a steer quotes back takes the tail, and the tail of an
// extended target is what the person said last. Each part gets its own share
// instead, so a long instruction cannot bury the steer that followed it — the
// bug this pair exists to end, arriving by way of the bound.
func TestSteerPrompt_TheNewestInstructionSurvivesTheBound(t *testing.T) {
	long := strings.Repeat("ship the parser. ", 60) // well past the bound
	target := ExtendTarget(long, "actually, fix the lexer first")

	a := New(nil, noStream)
	a.rounds = 5
	a.ConsiderVerdict(driftVerdict(5), running)
	iv, ok := a.NextIntervention(target)
	if !ok {
		t.Fatal("a drifting reading should earn an interruption")
	}
	if !strings.Contains(iv.Message, "actually, fix the lexer first") {
		t.Fatalf("the steer quoted the instruction and dropped what the person said last:\n%s", iv.Message)
	}
	if !strings.Contains(iv.Message, "ship the parser") {
		t.Fatalf("the anchor should still be quoted:\n%s", iv.Message)
	}
	if n := len([]rune(SteerPrompt(target, "a reason"))) - len([]rune(SteerWording())); n > DefaultSteerTargetChars {
		t.Fatalf("the quoted target ran to %d runes, past the bound of %d", n, DefaultSteerTargetChars)
	}
}

// The line a surface quotes shows every part of the target. Taking the first
// line of the whole thing would show a steer as though it had never been
// typed, which is the failure ExtendTarget exists to end.
func TestTargetLine_ShowsEveryThingThatWasAsked(t *testing.T) {
	target := ExtendTarget("make the round limit a checkpoint\nand say so", "also check the tests")
	want := "make the round limit a checkpoint … · also check the tests"
	if got := TargetLine(target); got != want {
		t.Fatalf("target line = %q, want %q", got, want)
	}
	if got := TargetLine(""); got != "" {
		t.Fatalf("an empty target has no line, got %q", got)
	}
}
