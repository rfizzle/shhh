package agent

import (
	"strconv"
	"strings"
	"testing"
)

func TestDueForCheckIn_FiresOncePerInterval(t *testing.T) {
	a := New(nil, noStream)
	due := 0
	for r := 1; r <= CheckInInterval*3; r++ {
		a.rounds = r
		if a.DueForCheckIn() {
			due++
			if r%CheckInInterval != 0 {
				t.Fatalf("check-in due at round %d, which is not a boundary", r)
			}
		}
	}
	if due != 3 {
		t.Errorf("expected one check-in per interval, got %d in %d rounds", due, CheckInInterval*3)
	}
}

func TestDueForCheckIn_NotAtZeroRounds(t *testing.T) {
	a := New(nil, noStream)
	if a.DueForCheckIn() {
		t.Error("a turn that has run no rounds has nothing to take stock of")
	}
}

// The check-in has to arrive well before the cap: at the cap the session
// stops and asks the person, which is the intervention it exists to spare
// them.
func TestCheckInInterval_ComesWellBeforeTheCap(t *testing.T) {
	if CheckInInterval >= DefaultMaxToolRounds {
		t.Fatalf("check-in interval %d must fall inside the round cap %d", CheckInInterval, DefaultMaxToolRounds)
	}
	if got := DefaultMaxToolRounds / CheckInInterval; got < 2 {
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
