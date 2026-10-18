package eval

import (
	"strings"
	"testing"
)

func skippedResult(name, why string) Result {
	return Result{Case: Case{Name: name, Kind: KindWorkspace, Skip: why}}
}

func standingOf(rows ...CaseBaseline) Baseline {
	return Baseline{Version: BaselineVersion, Cases: rows}
}

// A skip where a measured row stood is refused, each case named with its reason.
func TestRefreshBaselineRefusesASkipWhereAMeasuredRowStood(t *testing.T) {
	standing := standingOf(
		CaseBaseline{Name: "alpha", Kind: KindWorkspace, Verdict: "passed"},
		CaseBaseline{Name: "beta", Kind: KindWorkspace, Verdict: "failed"})
	sum := Summary{Results: []Result{
		skippedResult("alpha", "no model to ask: SHHH_API_KEY"),
		workspaceResult("beta", []int{3}, true),
	}}
	_, _, err := sum.RefreshBaseline(standing)
	if err == nil {
		t.Fatal("a refresh that skipped a measured case was allowed")
	}
	if !strings.Contains(err.Error(), "alpha: no model to ask") || strings.Contains(err.Error(), "beta") {
		t.Errorf("the refusal should name the skipped case and its reason alone: %v", err)
	}
}

// A new case that was skipped is named and left out, never written as a skip.
func TestRefreshBaselineLeavesOutANewCaseThatWasSkipped(t *testing.T) {
	standing := standingOf(CaseBaseline{Name: "alpha", Kind: KindWorkspace, Verdict: "passed"})
	sum := Summary{Results: []Result{
		workspaceResult("alpha", []int{3}, true),
		skippedResult("fresh", "needs go"),
	}}
	got, left, err := sum.RefreshBaseline(standing)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Cases) != 1 || got.Cases[0].Name != "alpha" {
		t.Errorf("the file should hold the measured case alone: %+v", got.Cases)
	}
	if len(left) != 1 || !strings.Contains(left[0], "fresh") || !strings.Contains(left[0], "needs go") {
		t.Errorf("the case left out should be named with its reason: %v", left)
	}
}

// A run that measured everything writes what a plain Baseline would, and a
// row that was already a skip stays one.
func TestRefreshBaselineWritesAFullyMeasuredRun(t *testing.T) {
	standing := standingOf(CaseBaseline{Name: "old", Kind: KindWorkspace, Verdict: "skipped"})
	sum := Summary{Model: "m", Results: []Result{
		workspaceResult("alpha", []int{3}, true),
		skippedResult("old", "needs go"),
	}}
	got, left, err := sum.RefreshBaseline(standing)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 || len(got.Cases) != 2 || got.Model != "m" {
		t.Errorf("a run with nothing newly lost should write as it always did: %+v, left %v", got, left)
	}
}

// A run that measured nothing has nothing to write.
func TestRefreshBaselineRefusesARunThatMeasuredNothing(t *testing.T) {
	sum := Summary{Results: []Result{skippedResult("fresh", "needs go")}}
	if _, _, err := sum.RefreshBaseline(Baseline{}); err == nil {
		t.Fatal("an empty baseline was offered for writing")
	}
}
