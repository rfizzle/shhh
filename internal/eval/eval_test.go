package eval

import (
	"errors"
	"testing"
	"time"
)

func attempts(passed ...bool) []Attempt {
	out := make([]Attempt, len(passed))
	for i, p := range passed {
		out[i] = Attempt{Passed: p, Rounds: 4 + i}
	}
	return out
}

// Flaky is a verdict of its own, not a rounded pass rate: a task the session
// can lose is the one worth reading, and an average hides it.
func TestVerdictSeparatesFlakyFromPassingAndFailing(t *testing.T) {
	for name, tc := range map[string]struct {
		result Result
		want   Verdict
	}{
		"every attempt passed":      {Result{Attempts: attempts(true, true, true)}, Passed},
		"no attempt passed":         {Result{Attempts: attempts(false, false)}, Failed},
		"one of three passed":       {Result{Attempts: attempts(true, false, false)}, Flaky},
		"one of three failed":       {Result{Attempts: attempts(true, true, false)}, Flaky},
		"the case was not run here": {Result{Case: Case{Skip: "no go"}}, Skipped},
		"nothing was attempted":     {Result{}, Errored},
	} {
		if got := tc.result.Verdict(); got != tc.want {
			t.Errorf("%s: verdict = %v, want %v", name, got, tc.want)
		}
	}
}

// A session that never finished says nothing about the task, and must not be
// reported as the task having failed — that is the difference between "the
// agent could not do it" and "the run broke".
func TestVerdictDoesNotBlameTheTaskForARunThatBroke(t *testing.T) {
	res := Result{Attempts: []Attempt{
		{Err: errors.New("the session failed: no provider")},
		{Err: errors.New("the session failed: no provider")},
	}}
	if got := res.Verdict(); got != Errored {
		t.Fatalf("verdict = %v, want Errored", got)
	}
}

// One attempt that spent three times the rounds is the reason this is a
// median: it must not drag the number the comparison is read on.
func TestMedianIgnoresAnOutlier(t *testing.T) {
	res := Result{Attempts: []Attempt{{Rounds: 4}, {Rounds: 5}, {Rounds: 60}}}
	if got := res.MedianRounds(); got != 5 {
		t.Errorf("median rounds = %v, want 5", got)
	}
}

func TestMedianOfAnEvenCountAveragesTheMiddleTwo(t *testing.T) {
	res := Result{Attempts: []Attempt{{Rounds: 4}, {Rounds: 6}}}
	if got := res.MedianRounds(); got != 5 {
		t.Errorf("median rounds = %v, want 5", got)
	}
}

// An attempt that never ran contributes no numbers, or the medians describe
// the failures rather than the work.
func TestMedianSkipsAttemptsThatNeverRan(t *testing.T) {
	res := Result{Attempts: []Attempt{
		{Rounds: 10},
		{Rounds: 0, Err: errors.New("broke")},
		{Rounds: 12},
	}}
	if got := res.MedianRounds(); got != 11 {
		t.Errorf("median rounds = %v, want 11", got)
	}
}

// An unpriced run reports no cost rather than a cost of zero: the two look
// identical in a total and mean opposite things.
func TestCostIsUnpricedRatherThanZeroWhenTheTableDidNotKnowTheModel(t *testing.T) {
	sum := Summary{Results: []Result{{Attempts: []Attempt{{Cost: 0, Priced: false}}}}}
	if _, priced := sum.Cost(); priced {
		t.Error("a run nothing could price must not report a price")
	}

	sum = Summary{Results: []Result{{Attempts: []Attempt{{Cost: 0.5, Priced: true}}}}}
	total, priced := sum.Cost()
	if !priced || total != 0.5 {
		t.Errorf("total = %v priced = %v, want 0.5 true", total, priced)
	}
}

func TestTallyCountsEveryVerdict(t *testing.T) {
	sum := Summary{Results: []Result{
		{Attempts: attempts(true)},
		{Attempts: attempts(true, false)},
		{Attempts: attempts(false)},
		{Case: Case{Skip: "no toolchain"}},
		{},
	}}
	passed, flaky, failed, skipped, errored := sum.Tally()
	if passed != 1 || flaky != 1 || failed != 1 || skipped != 1 || errored != 1 {
		t.Errorf("tally = %d/%d/%d/%d/%d, want one of each", passed, flaky, failed, skipped, errored)
	}
}

func TestElapsedIsTheWholeRun(t *testing.T) {
	sum := Summary{Results: []Result{
		{Attempts: []Attempt{{Elapsed: time.Second}, {Elapsed: 2 * time.Second}}},
		{Attempts: []Attempt{{Elapsed: 3 * time.Second}}},
	}}
	if got := sum.Elapsed(); got != 6*time.Second {
		t.Errorf("elapsed = %v, want 6s", got)
	}
}
