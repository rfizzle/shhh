package agent

import (
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/agent/sidejobs"
)

// A run's own plan reaches the reading beside its changes, in the one
// spelling, and a run that named no plan sends no field rather than zero of
// zero.
func TestSummaryRun_TheOwnPlanReachesTheDigest(t *testing.T) {
	p := &slowProvider{}
	r, _ := testSummaryRun(t, p, "ship the parser")
	r.WithSteps(func() (int, int, string) { return 3, 7, "add the tests" })
	waitVerdict(t, r, sidejobs.FirstSummaryRound)
	sent := p.requests()[0]
	for _, want := range []string{`"own_plan_progress"`, "3 of 7 steps done · on: add the tests"} {
		if !strings.Contains(sent, want) {
			t.Errorf("the digest should carry %q:\n%s", want, sent)
		}
	}

	p = &slowProvider{}
	r, _ = testSummaryRun(t, p, "ship the parser")
	r.WithSteps(func() (int, int, string) { return 0, 0, "" })
	waitVerdict(t, r, sidejobs.FirstSummaryRound)
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
		if got := sidejobs.SummarySteps(tc.done, tc.total, tc.current); got != tc.want {
			t.Errorf("sidejobs.SummarySteps(%d, %d, %q) = %q, want %q", tc.done, tc.total, tc.current, got, tc.want)
		}
	}
}

// The session's check-in puts its own list in front of it, every step with
// its mark, says how many rounds ago the list was set, and asks whether it
// still holds and for it again with what is done marked — under the
// wording, on every route a check-in takes.
func TestCheckIn_CarriesTheListAndAsksWhetherItHolds(t *testing.T) {
	const list = "Working list, 2 of 4 done — on: wire the flag\n" +
		"  [x] 1. read the loop\n  [x] 2. add the field\n→ [ ] 3. wire the flag\n  [ ] 4. test it"
	a := New(nil, noStream)
	a.rounds = DefaultCheckInInterval
	a.SetCheckInSteps(func() StepReading {
		return StepReading{Done: 2, Total: 4, Current: "wire the flag", List: list, Age: 25}
	})
	prompt, ok := a.TakeCheckIn()
	if !ok {
		t.Fatal("the round check-in is due")
	}
	base := CheckInPrompt(DefaultCheckInInterval, FinishedInSession)
	if !strings.HasPrefix(prompt, base) {
		t.Errorf("the check-in should keep the surface's wording first:\n%s", prompt)
	}
	for _, want := range []string{
		"as you last sent it 25 tool rounds ago:\n" + list,
		"Say whether this list still holds",
		"send the list again — every step, with each one you have finished marked done",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the check-in should carry %q:\n%s", want, prompt)
		}
	}
	if got := a.CheckInMessage(); !strings.Contains(got, list) {
		t.Errorf("the cap's check-in should carry the list too:\n%s", got)
	}
	if strings.Contains(prompt, "progress line") {
		t.Errorf("the check-in still names the retired progress line:\n%s", prompt)
	}
}

// A turn with no list is asked exactly the wording, with no paragraph about
// one; a surface that keeps no age, or hands over a count alone, still gets
// the question.
func TestCheckIn_NoListNoParagraph(t *testing.T) {
	a := New(nil, noStream)
	a.rounds = DefaultCheckInInterval
	base := CheckInPrompt(DefaultCheckInInterval, FinishedInSession)
	a.SetCheckInSteps(func() StepReading { return StepReading{} })
	if prompt, ok := a.TakeCheckIn(); !ok || prompt != base {
		t.Errorf("a turn with no list is asked the wording alone (due %v):\n%s", ok, prompt)
	}

	for _, tc := range []struct {
		r    StepReading
		want string
	}{
		{StepReading{Done: 1, Total: 3, Current: "patch it", Age: -1}, "as you last sent it:\n1 of 3 steps done — on: patch it"},
		{StepReading{Done: 3, Total: 3, Age: 0}, "in the round just finished:\n3 of 3 steps done\n"},
		{StepReading{Done: 0, Total: 1, Current: "read", Age: 1}, "1 tool round ago:\n0 of 1 step done — on: read"},
	} {
		got := stepNote(tc.r)
		if !strings.Contains(got, tc.want) || !strings.Contains(got, "Say whether this list still holds") {
			t.Errorf("stepNote(%+v) = %q, want it to carry %q and the question", tc.r, got, tc.want)
		}
	}
}
