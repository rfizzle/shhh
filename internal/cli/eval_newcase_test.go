package cli

import (
	"strings"
	"testing"
	"time"
)

// A case with no baseline row has nothing to have regressed from: the gate
// reads the cases both runs hold and never counts the rest either way.
func TestGateIgnoresACaseTheBaselineDoesNotHold(t *testing.T) {
	before := baselineOf("m", repeated("a", 3, 3))
	after := baselineOf("m", repeated("a", 3, 3), repeated("new", 3, 0))
	cmp := compareOf(t, before, after)
	if got := cmp.Regressions(); len(got) != 0 {
		t.Errorf("a case added since the baseline failed the gate: %v", got)
	}
	if len(cmp.NotInBaseline) != 1 || cmp.NotInBaseline[0] != "new" {
		t.Errorf("the new case should be named, not compared: %v", cmp.NotInBaseline)
	}
	after = baselineOf("m", repeated("a", 3, 0), repeated("new", 3, 3))
	if got := compareOf(t, before, after).Regressions(); len(got) != 1 || got[0].Name != "a" {
		t.Errorf("a regression among the shared cases must still stop the gate: %v", got)
	}
}

func TestCompareReportSaysItsTotalsLeaveOutACaseNotInTheBaseline(t *testing.T) {
	before := baselineOf("m", repeated("a", 3, 3))
	after := baselineOf("m", repeated("a", 3, 3), repeated("new", 3, 3))
	out := compareReport(compareOf(t, before, after), time.Now()).Render(100)
	for _, want := range []string{"not in the baseline", "new", "both runs hold"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report should say %q:\n%s", want, out)
		}
	}
}
