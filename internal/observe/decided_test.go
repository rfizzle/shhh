package observe

import (
	"testing"
	"time"
)

// A timed verdict goes to DecisionTimed where the observer takes one, and to
// Decision otherwise — never to both, so a verdict is one row either way.
func TestDecided_ARowIsWrittenOnceAndTimedWhereItCanBe(t *testing.T) {
	var plain, timed int
	var took time.Duration
	both := Observer{
		Decision:      func(Pos, string, string) { plain++ },
		DecisionTimed: func(_ Pos, _, _ string, d time.Duration) { timed++; took = d },
	}
	both.Decided(Pos{}, DecisionDeny, ReasonClassifier, 2*time.Second)
	both.Decided(Pos{}, DecisionAllow, ReasonUser, 0)
	if plain != 1 || timed != 1 || took != 2*time.Second {
		t.Fatalf("plain %d, timed %d, took %v", plain, timed, took)
	}

	plain = 0
	untimed := Observer{Decision: func(Pos, string, string) { plain++ }}
	untimed.Decided(Pos{}, DecisionDeny, ReasonClassifier, time.Second)
	if plain != 1 {
		t.Fatal("an observer with no timed callback should still get the row")
	}
	Observer{}.Decided(Pos{}, DecisionDeny, ReasonClassifier, time.Second)
}
