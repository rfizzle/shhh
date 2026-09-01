package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/eval"
)

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

// The defect this shape exists for: a report redirected to a file measures
// 80 columns, a row clips its target to fit, and the numbers were what got
// clipped — in the one output whose entire purpose is the numbers.
func TestEvalReportKeepsTheNumbersAtEightyColumns(t *testing.T) {
	sum := eval.Summary{Model: "claude-sonnet-4-5", Results: []eval.Result{{
		Case: eval.Case{
			Name:   "trace-the-cause",
			Prompt: "route.Match returns the wrong handler for paths with a trailing slash, and its test says so. The cause is not in the route package.",
		},
		Attempts: []eval.Attempt{
			{Passed: true, Rounds: 11, TokensIn: 240000, TokensOut: 4200, Cost: 0.94, Priced: true, Elapsed: 56 * time.Second},
			{Passed: true, Rounds: 9, TokensIn: 190000, TokensOut: 3800, Cost: 0.81, Priced: true, Elapsed: 45 * time.Second},
		},
	}}}

	out := evalReport(sum).Render(80)
	for _, want := range []string{"rounds", "$", "trace-the-cause", "passed"} {
		if !strings.Contains(out, want) {
			t.Errorf("a report at 80 columns lost %q:\n%s", want, out)
		}
	}
	for _, line := range strings.Split(out, "\n") {
		if len([]rune(line)) > 80 {
			t.Errorf("line overflows 80 columns (%d): %q", len([]rune(line)), line)
		}
	}
}

// The prompt is not in the row at all: it is a property of the suite, and the
// row's width is owed to the numbers instead.
func TestEvalReportDoesNotSpendTheRowOnThePrompt(t *testing.T) {
	sum := eval.Summary{Results: []eval.Result{{
		Case:     eval.Case{Name: "a-case", Prompt: "some long instruction nobody needs repeated here"},
		Attempts: []eval.Attempt{{Passed: true, Rounds: 4}},
	}}}
	if out := evalReport(sum).Render(120); strings.Contains(out, "nobody needs repeated") {
		t.Errorf("the prompt should not be in the row:\n%s", out)
	}
}

// A skipped case has no numbers, so its reason is what the row is for.
func TestEvalReportSkippedRowSaysWhy(t *testing.T) {
	sum := eval.Summary{Results: []eval.Result{{
		Case: eval.Case{Name: "rusty", Skip: "not on PATH: cargo"},
	}}}
	out := evalReport(sum).Render(80)
	if !strings.Contains(out, "cargo") {
		t.Errorf("a skipped row must name what is missing:\n%s", out)
	}
}
