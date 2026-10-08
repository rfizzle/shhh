package observe

import (
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/agent"
)

// A connect is filed under its own status, a failure that was the wait
// running out under a word of its own, and a status this build does not know
// under other rather than as whatever string arrived.
func TestServerOutcome_TellsATimeoutFromAFailure(t *testing.T) {
	cases := []struct {
		status   string
		timedOut bool
		want     string
	}{
		{"connected", false, ServerConnected},
		{"failed", false, ServerFailed},
		{"failed", true, ServerTimeout},
		{"disabled", false, ServerDisabled},
		{"untrusted", false, ServerUntrusted},
		{"missing-env", false, ServerMissingEnv},
		{"excluded", false, ServerExcluded},
		{"something new", false, ServerOther},
	}
	for _, c := range cases {
		if got := ServerOutcome(c.status, c.timedOut); got != c.want {
			t.Errorf("ServerOutcome(%q, %v) = %q, want %q", c.status, c.timedOut, got, c.want)
		}
	}
}

// Rows filed before the record is attached are written at the attach, in
// order; rows after it go straight through; and only the first attach takes
// them.
func TestStartup_HoldsRowsUntilAttached(t *testing.T) {
	var s Startup
	s.Add(StartupRow{Phase: PhaseConfig})
	s.Add(StartupRow{Phase: PhaseStore})
	var first, second []string
	s.Attach(func(r StartupRow) { first = append(first, r.Phase) })
	s.Attach(func(r StartupRow) { second = append(second, r.Phase) })
	s.Add(StartupRow{Phase: PhaseFirstPaint})
	if len(first) != 3 || first[0] != PhaseConfig || first[1] != PhaseStore || first[2] != PhaseFirstPaint {
		t.Fatalf("first attach took %v", first)
	}
	if len(second) != 0 {
		t.Fatalf("second attach took %v", second)
	}
	var none *Startup
	none.Add(StartupRow{Phase: PhaseLSP}) // a nil holder records nothing
}

// The milliseconds a split is written in add up to the turn's, however the
// fractions fall.
func TestTurnMillis_AddUpToTheTurn(t *testing.T) {
	s := agent.TurnSplit{ModelFirst: 999 * time.Microsecond, ModelStream: 999 * time.Microsecond,
		Tool: 999 * time.Microsecond, Person: 3 * time.Microsecond}
	ms := TurnMillis(s.Total(), s)
	if sum := ms[0] + ms[1] + ms[2] + ms[3]; sum != s.Total().Milliseconds() {
		t.Fatalf("millis %v sum to %d, want %d", ms, sum, s.Total().Milliseconds())
	}
}
