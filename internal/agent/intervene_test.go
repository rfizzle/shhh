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
