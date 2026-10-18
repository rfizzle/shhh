package cli

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/eval"
)

// repeated is a workspace case run n times with the given number of passes.
func repeated(name string, n, passes int) eval.Result {
	r := eval.Result{Case: eval.Case{Name: name, Kind: eval.KindWorkspace}}
	for i := 0; i < n; i++ {
		r.Attempts = append(r.Attempts, eval.Attempt{Passed: i < passes, Rounds: 3, Elapsed: time.Second})
	}
	return r
}

func TestRegressionErrorNamesEveryCaseThatFailedEveryAttempt(t *testing.T) {
	before := baselineOf("m", repeated("a", 3, 3), repeated("b", 3, 3), repeated("c", 3, 3))
	after := baselineOf("m", repeated("a", 3, 0), repeated("b", 3, 0), repeated("c", 3, 3))
	err := regressionError(compareOf(t, before, after).Regressions())
	if err == nil || !strings.HasSuffix(err.Error(), ": a, b") {
		t.Fatalf("both regressed cases and only those should be named: %v", err)
	}
}

func TestRegressionErrorIsNilWhenNothingRegressed(t *testing.T) {
	run := baselineOf("m", repeated("a", 3, 3))
	if err := regressionError(compareOf(t, run, run).Regressions()); err != nil {
		t.Errorf("an unchanged run failed the gate: %v", err)
	}
}

func TestAFlakyCaseIsNotARegression(t *testing.T) {
	before := baselineOf("m", repeated("a", 3, 3))
	after := baselineOf("m", repeated("a", 3, 1))
	if got := compareOf(t, before, after).Regressions(); len(got) != 0 {
		t.Errorf("passed to flaky is a finding for the report, not the gate: %v", got)
	}
}

func TestTwoAttemptsCannotRegressTheGate(t *testing.T) {
	before := baselineOf("m", repeated("a", 3, 3))
	after := baselineOf("m", repeated("a", 2, 0))
	if got := compareOf(t, before, after).Regressions(); len(got) != 0 {
		t.Errorf("two samples cannot tell flaky from broken: %v", got)
	}
}

func TestEvalFailOnRegressionNeedsEnoughAttempts(t *testing.T) {
	suite := skippableSuite(t, "alpha")
	_, err := runEval(t, suite, "--fail-on-regression", "--repeat", "2")
	if err == nil || !strings.Contains(err.Error(), "--repeat of at least 3") {
		t.Fatalf("a gate over too few attempts should be refused with the reason: %v", err)
	}
}

func TestEvalFailOnRegressionNeedsABaseline(t *testing.T) {
	suite := skippableSuite(t, "alpha")
	_, err := runEval(t, suite, "--fail-on-regression", "--repeat", "3")
	if err == nil || !strings.Contains(err.Error(), "no baseline to compare against") {
		t.Fatalf("a gate with nothing to read the run against must refuse, not pass: %v", err)
	}
}

func TestEvalFailOnRegressionPassesAnUnchangedRun(t *testing.T) {
	suite := skippableSuite(t, "alpha")
	path := filepath.Join(t.TempDir(), "baseline.json")
	if _, err := runEval(t, suite, "--baseline", path); err != nil {
		t.Fatal(err)
	}
	if _, err := runEval(t, suite, "--compare", path, "--repeat", "3", "--fail-on-regression"); err != nil {
		t.Errorf("a run with nothing regressed failed the gate: %v", err)
	}
}
