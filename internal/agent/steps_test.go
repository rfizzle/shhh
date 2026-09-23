package agent

import (
	"strings"
	"testing"
)

// A run's own plan reaches the reading beside its changes, in the one
// spelling, and a run that named no plan sends no field rather than zero of
// zero.
func TestSummaryRun_TheOwnPlanReachesTheDigest(t *testing.T) {
	p := &slowProvider{}
	r, _ := testSummaryRun(t, p, "ship the parser")
	r.WithSteps(func() (int, int, string) { return 3, 7, "add the tests" })
	waitVerdict(t, r, FirstSummaryRound)
	sent := p.requests()[0]
	for _, want := range []string{`"own_plan_progress"`, "3 of 7 steps done · on: add the tests"} {
		if !strings.Contains(sent, want) {
			t.Errorf("the digest should carry %q:\n%s", want, sent)
		}
	}

	p = &slowProvider{}
	r, _ = testSummaryRun(t, p, "ship the parser")
	r.WithSteps(func() (int, int, string) { return 0, 0, "" })
	waitVerdict(t, r, FirstSummaryRound)
	if sent := p.requests()[0]; strings.Contains(sent, `"own_plan_progress"`) {
		t.Errorf("a run with no plan sends no plan progress:\n%s", sent)
	}
}

func TestSummarySteps(t *testing.T) {
	for _, tc := range []struct {
		done, total int
		current     string
		want        string
	}{
		{0, 0, "", ""},
		{0, 1, "read it", "0 of 1 step done · on: read it"},
		{7, 7, "", "7 of 7 steps done"},
		{9, 7, "", "7 of 7 steps done"},
	} {
		if got := SummarySteps(tc.done, tc.total, tc.current); got != tc.want {
			t.Errorf("SummarySteps(%d, %d, %q) = %q, want %q", tc.done, tc.total, tc.current, got, tc.want)
		}
	}
}

// Every check-in names the step the turn said it was on, under the wording,
// and a turn with no plan is asked exactly what it was asked before.
func TestCheckIn_NamesTheStepTheTurnIsOn(t *testing.T) {
	a := New(nil, noStream)
	a.rounds = DefaultCheckInInterval
	a.SetCheckInSteps(func() (int, int, string) { return 2, 5, "wire the flag" })
	prompt, ok := a.TakeCheckIn()
	if !ok {
		t.Fatal("the round check-in is due")
	}
	base := CheckInPrompt(DefaultCheckInInterval, FinishedInSession)
	if !strings.HasPrefix(prompt, base) || !strings.Contains(prompt, "2 of 5 steps done and are on: wire the flag") {
		t.Errorf("the check-in should keep the wording and name the step:\n%s", prompt)
	}
	if got := a.CheckInMessage(); !strings.Contains(got, "on: wire the flag") {
		t.Errorf("the cap's check-in should name the step too:\n%s", got)
	}

	a.SetCheckInSteps(func() (int, int, string) { return 0, 0, "" })
	if got := a.ForceCheckIn(); got != base {
		t.Errorf("a turn with no plan is asked the wording alone:\n%s", got)
	}
}
