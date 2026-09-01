package cli

import (
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/eval"
)

// A prompt that names a package or a function is full of dots that end
// nothing, and cutting at the first of them leaves a subject saying "route".
func TestFirstSentenceBreaksOnASentenceNotOnADot(t *testing.T) {
	got := firstSentence("route.Match returns the wrong handler. Track it down.")
	if got != "route.Match returns the wrong handler" {
		t.Errorf("got %q", got)
	}
}

func TestFirstSentenceKeepsAOneSentencePrompt(t *testing.T) {
	if got := firstSentence("make the tests pass."); got != "make the tests pass" {
		t.Errorf("got %q", got)
	}
}

func TestSelectCasesRefusesANameThatMatchesNothing(t *testing.T) {
	cases := []eval.Case{{Name: "one"}, {Name: "two"}}
	if _, err := selectCases(cases, []string{"three"}); err == nil {
		t.Fatal("a run must not silently measure less than was asked for")
	}
	got, err := selectCases(cases, []string{"two"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "two" {
		t.Fatalf("got %v", got)
	}
}

func TestSelectCasesWithNoNamesIsTheWholeSuite(t *testing.T) {
	cases := []eval.Case{{Name: "one"}, {Name: "two"}}
	got, err := selectCases(cases, nil)
	if err != nil || len(got) != 2 {
		t.Fatalf("got %v %v", got, err)
	}
}

// A run nothing could price says nothing about cost rather than "$0.0000",
// which reads as free.
func TestEvalReportOmitsCostWhenNothingWasPriced(t *testing.T) {
	sum := eval.Summary{Model: "mystery", Results: []eval.Result{
		{Case: eval.Case{Name: "a", Prompt: "do it"}, Attempts: []eval.Attempt{{Passed: true}}},
	}}
	if strings.Contains(evalReport(sum).Tally, "$") {
		t.Errorf("tally = %q", evalReport(sum).Tally)
	}
}

// A flaky case is named as flaky, with the count that says how flaky.
func TestEvalReportNamesAFlakyCase(t *testing.T) {
	sum := eval.Summary{Results: []eval.Result{{
		Case:     eval.Case{Name: "wobbly", Prompt: "do it"},
		Attempts: []eval.Attempt{{Passed: true}, {Passed: false}, {Passed: true}},
	}}}
	r := evalReport(sum)
	row := r.Sections[0].Rows[0]
	if row.Outcome != "flaky" {
		t.Errorf("outcome = %q", row.Outcome)
	}
	if !strings.Contains(row.Consequence, "2 of 3") {
		t.Errorf("consequence = %q", row.Consequence)
	}
	if !strings.Contains(r.Tally, "1 flaky") {
		t.Errorf("tally = %q", r.Tally)
	}
}
