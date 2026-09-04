package eval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// workspaceResult is a run of one workspace case, as the harness hands one
// over.
func workspaceResult(name string, rounds []int, passed ...bool) Result {
	res := Result{Case: Case{Name: name, Kind: KindWorkspace}}
	for i, p := range passed {
		res.Attempts = append(res.Attempts, Attempt{
			Passed:  p,
			Rounds:  rounds[i],
			Cost:    0.5,
			Priced:  true,
			Elapsed: 40 * time.Second,
		})
	}
	return res
}

// tableCaseResult is a scored table with the given outcomes: rows the model
// got right, then one row wanted deny and answered allow for each false
// allow.
func tableCaseResult(name string, correct, falseAllow int) Result {
	score := Score{Kind: KindClassifier}
	for i := 0; i < correct; i++ {
		score.Answers = append(score.Answers, Answer{
			Row:   Row{Name: "right", Expect: []string{LabelDeny}},
			Label: LabelDeny,
		})
	}
	for i := 0; i < falseAllow; i++ {
		score.Answers = append(score.Answers, Answer{
			Row:   Row{Name: "let through", Expect: []string{LabelDeny}},
			Label: LabelAllow,
		})
	}
	return Result{
		Case:     Case{Name: name, Kind: KindClassifier},
		Attempts: []Attempt{{Passed: falseAllow == 0, Score: &score, Elapsed: time.Minute}},
	}
}

// The file is what the report showed, so a reader can check one against the
// other. A table case's rates are rows of it and not a pass count: a verdict
// that stayed "failed" says nothing about a control that opened further.
func TestBaselineKeepsATableCasesRatesApart(t *testing.T) {
	sum := Summary{Model: "a-model", Results: []Result{tableCaseResult("classifier-decisions", 17, 3)}}
	b := sum.Baseline()
	if len(b.Cases) != 1 {
		t.Fatalf("cases = %v", b.Cases)
	}
	c := b.Cases[0]
	if c.Table == nil {
		t.Fatal("a table case's rates are what it is compared on")
	}
	if c.Table.Rows != 20 || c.Table.Correct != 17 || c.Table.FalseAllow != 3 {
		t.Errorf("table = %+v", *c.Table)
	}
	if c.Table.FalseDeny != 0 {
		t.Errorf("a false allow must not be counted as a false deny: %+v", *c.Table)
	}
	if c.Verdict != "failed" {
		t.Errorf("verdict = %q", c.Verdict)
	}
}

func TestBaselineRoundTripsThroughAFile(t *testing.T) {
	sum := Summary{Model: "a-model", Results: []Result{
		workspaceResult("fix-failing-test", []int{9, 11, 10}, true, true, true),
		tableCaseResult("classifier-decisions", 18, 2),
	}}
	path := filepath.Join(t.TempDir(), "nested", "baseline.json")
	if err := WriteBaseline(path, sum.Baseline()); err != nil {
		t.Fatal(err)
	}
	got, err := ReadBaseline(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Model != "a-model" || len(got.Cases) != 2 {
		t.Fatalf("read back %+v", got)
	}
	if got.Cases[0].Rounds != 10 {
		t.Errorf("median rounds = %v, want the run's 10", got.Cases[0].Rounds)
	}
	if got.Cases[1].Table == nil || got.Cases[1].Table.FalseAllow != 2 {
		t.Errorf("table = %+v", got.Cases[1].Table)
	}
}

// A file from a different shhh decodes into zeroes, which would read as a run
// in which everything suddenly got cheaper.
func TestReadBaselineRefusesAFormatItDoesNotKnow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "baseline.json")
	if err := os.WriteFile(path, []byte(`{"version":99,"cases":[{"name":"a"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadBaseline(path); err == nil {
		t.Fatal("a baseline in an unknown format must be refused")
	}
}

func TestReadBaselineRefusesAVerdictItCannotRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "baseline.json")
	body := `{"version":1,"cases":[{"name":"a","verdict":"mostly fine"}]}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ReadBaseline(path)
	if err == nil || !strings.Contains(err.Error(), "mostly fine") {
		t.Fatalf("err = %v, want one naming the word it could not read", err)
	}
}

// Two runs over different suites have totals that are not comparable, and
// dropping the odd case out would hide exactly the reason they are not.
func TestCompareRefusesTwoRunsOverDifferentCases(t *testing.T) {
	before := Summary{Results: []Result{
		workspaceResult("fix-failing-test", []int{9}, true),
		workspaceResult("gone-since", []int{4}, true),
	}}.Baseline()
	after := Summary{Results: []Result{
		workspaceResult("fix-failing-test", []int{9}, true),
		workspaceResult("ts-fix-failing-test", []int{6}, true),
	}}.Baseline()

	_, err := Compare(before, after)
	if err == nil {
		t.Fatal("a baseline from a different case set must be refused")
	}
	for _, want := range []string{"gone-since", "ts-fix-failing-test"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal should name which cases differ, got: %v", err)
		}
	}
}

// The same run against itself moved nothing, and a comparison that reported a
// direction there would be reporting noise.
func TestCompareOfARunAgainstItselfIsUnchanged(t *testing.T) {
	b := Summary{Model: "a-model", Results: []Result{
		workspaceResult("fix-failing-test", []int{9, 11, 10}, true, true, true),
		tableCaseResult("classifier-decisions", 18, 2),
	}}.Baseline()

	cmp, err := Compare(b, b)
	if err != nil {
		t.Fatal(err)
	}
	improved, regressed, unchanged, incomparable := cmp.Tally()
	if improved != 0 || regressed != 0 || incomparable != 0 || unchanged != 2 {
		t.Errorf("tally = %d improved, %d regressed, %d unchanged, %d not comparable",
			improved, regressed, unchanged, incomparable)
	}
}

// A verdict that moved is the finding, whatever the numbers did — a case that
// now fails is not redeemed by having failed in fewer rounds.
func TestCompareReadsAVerdictBeforeTheNumbers(t *testing.T) {
	before := Summary{Results: []Result{workspaceResult("a", []int{20}, true)}}.Baseline()
	after := Summary{Results: []Result{workspaceResult("a", []int{4}, false)}}.Baseline()
	cmp, err := Compare(before, after)
	if err != nil {
		t.Fatal(err)
	}
	if cmp.Cases[0].Change != Regressed {
		t.Errorf("change = %v, want Regressed", cmp.Cases[0].Change)
	}
	// The pair of words is in the row already; the line under it is what the
	// move means.
	if !strings.Contains(cmp.Cases[0].Why, "finished before and now does not") {
		t.Errorf("why = %q", cmp.Cases[0].Why)
	}
}

// The same verdict for twice the work is the regression nothing that only
// counts passes can see.
func TestCompareReadsADoubledRoundMedianAsARegression(t *testing.T) {
	before := Summary{Results: []Result{workspaceResult("a", []int{9}, true)}}.Baseline()
	after := Summary{Results: []Result{workspaceResult("a", []int{18}, true)}}.Baseline()
	cmp, err := Compare(before, after)
	if err != nil {
		t.Fatal(err)
	}
	if cmp.Cases[0].Change != Regressed {
		t.Fatalf("change = %v, want Regressed", cmp.Cases[0].Change)
	}
	if !strings.Contains(cmp.Cases[0].Why, "9 → 18 rounds") {
		t.Errorf("why = %q", cmp.Cases[0].Why)
	}
}

// A median off a handful of attempts wobbles by a round, and a suite that
// cried regression at that would cry it every run.
func TestCompareIgnoresARoundMedianThatBarelyMoved(t *testing.T) {
	before := Summary{Results: []Result{workspaceResult("a", []int{10}, true)}}.Baseline()
	after := Summary{Results: []Result{workspaceResult("a", []int{11}, true)}}.Baseline()
	cmp, err := Compare(before, after)
	if err != nil {
		t.Fatal(err)
	}
	if cmp.Cases[0].Change != Unchanged {
		t.Errorf("change = %v, want Unchanged", cmp.Cases[0].Change)
	}
}

// A control that lets one more action through has failed further open, and it
// says so even though the verdict on the case never moved.
func TestCompareReadsAFalseAllowUnderAnUnchangedVerdict(t *testing.T) {
	before := Summary{Results: []Result{tableCaseResult("classifier", 18, 2)}}.Baseline()
	after := Summary{Results: []Result{tableCaseResult("classifier", 16, 4)}}.Baseline()
	cmp, err := Compare(before, after)
	if err != nil {
		t.Fatal(err)
	}
	if cmp.Cases[0].Change != Regressed {
		t.Fatalf("change = %v, want Regressed", cmp.Cases[0].Change)
	}
	if !strings.Contains(cmp.Cases[0].Why, "2 → 4 false allow") {
		t.Errorf("why = %q", cmp.Cases[0].Why)
	}
}

// A case the machine could not run has no numbers behind it, and calling that
// an improvement because the failures stopped is the worst reading available.
func TestCompareWillNotReadASkippedCaseAsAnImprovement(t *testing.T) {
	before := Summary{Results: []Result{workspaceResult("ts-fix-failing-test", []int{9}, false)}}.Baseline()
	after := Summary{Results: []Result{{Case: Case{Name: "ts-fix-failing-test", Skip: "not on PATH: node"}}}}.Baseline()
	cmp, err := Compare(before, after)
	if err != nil {
		t.Fatal(err)
	}
	if cmp.Cases[0].Change != Incomparable {
		t.Errorf("change = %v, want Incomparable", cmp.Cases[0].Change)
	}
}

// The small-n rule: the samples are attempts for a workspace case and rows
// for a table one, because that is where each shape's independent draws are.
func TestSamplesAreAttemptsForAWorkspaceCaseAndRowsForATable(t *testing.T) {
	three := Summary{Results: []Result{workspaceResult("a", []int{9, 9, 9}, true, true, true)}}.Baseline()
	cmp, err := Compare(three, three)
	if err != nil {
		t.Fatal(err)
	}
	if cmp.Cases[0].Samples() != 3 || cmp.Cases[0].ReadableRate() {
		t.Errorf("three attempts = %d samples, readable %v", cmp.Cases[0].Samples(), cmp.Cases[0].ReadableRate())
	}

	table := Summary{Results: []Result{tableCaseResult("t", 18, 2)}}.Baseline()
	cmp, err = Compare(table, table)
	if err != nil {
		t.Fatal(err)
	}
	if cmp.Cases[0].Samples() != 20 || !cmp.Cases[0].ReadableRate() {
		t.Errorf("a twenty-row table put once = %d samples, readable %v",
			cmp.Cases[0].Samples(), cmp.Cases[0].ReadableRate())
	}
}

// A median over an even number of attempts lands on a half. Printed to no
// decimal places, two and a half rounds and two rounds are the same word, and
// the row would claim a regression beside two identical numbers.
func TestADeltaOnAHalfRoundMedianShowsTheHalf(t *testing.T) {
	before := Summary{Results: []Result{workspaceResult("a", []int{2, 2}, true, true)}}.Baseline()
	after := Summary{Results: []Result{workspaceResult("a", []int{2, 3}, true, true)}}.Baseline()
	cmp, err := Compare(before, after)
	if err != nil {
		t.Fatal(err)
	}
	if cmp.Cases[0].Change != Regressed {
		t.Fatalf("change = %v, want Regressed", cmp.Cases[0].Change)
	}
	if !strings.Contains(cmp.Cases[0].Why, "2 → 2.5 rounds") {
		t.Errorf("why = %q, want the half it regressed by", cmp.Cases[0].Why)
	}
}
