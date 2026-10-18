package eval

import (
	"encoding/json"
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
	if got.Cases[0].Behaviour == nil {
		t.Fatal("a workspace baseline dropped its action metrics")
	}
	if got.Cases[0].Behaviour.CallsBeforeFirstMutation != 0 {
		t.Errorf("default behaviour = %+v", got.Cases[0].Behaviour)
	}
}

func TestBaselineKeepsWorkspaceBehaviourMetrics(t *testing.T) {
	res := Result{Case: Case{Name: "analysis-only", Kind: KindWorkspace, AnalysisOnly: true}, Attempts: []Attempt{{
		Passed: true,
		Behaviour: Behaviour{
			MutationsAttempted:       0,
			CallsBeforeFirstMutation: 3,
			ValidationAttempts:       1,
			UnintendedMutations:      0,
		},
	}}}
	got := Summary{Results: []Result{res}}.Baseline().Cases[0].Behaviour
	want := &BehaviourBaseline{CallsBeforeFirstMutation: 3, ValidationAttempts: 1}
	if got == nil || *got != *want {
		t.Errorf("behaviour = %+v, want %+v", got, want)
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

// A suite that gained a case is behind its baseline by that case alone, and
// the rest of the comparison is still worth having.
func TestCompare_ANewCaseDoesNotRefuseTheRest(t *testing.T) {
	before := Summary{Results: []Result{
		workspaceResult("fix-failing-test", []int{9}, true),
	}}.Baseline()
	after := Summary{Results: []Result{
		workspaceResult("fix-failing-test", []int{9}, true),
		workspaceResult("process-choice", []int{6}, true),
	}}.Baseline()

	cmp, err := Compare(before, after)
	if err != nil {
		t.Fatalf("a case added since the baseline refused the comparison: %v", err)
	}
	if len(cmp.Cases) != 1 || cmp.Cases[0].Name != "fix-failing-test" {
		t.Errorf("only the case both runs hold is compared: %+v", cmp.Cases)
	}
	if len(cmp.NotInBaseline) != 1 || cmp.NotInBaseline[0] != "process-choice" {
		t.Errorf("the case with nothing to read against should be named: %v", cmp.NotInBaseline)
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

// researchBaseline is one research case as a run wrote it down.
func researchBaseline(cited, quoted, facts Rate) CaseBaseline {
	score := ResearchScore{Cited: cited, Quoted: quoted, Facts: facts}
	res := Result{
		Case:     Case{Name: "research-version", Kind: KindResearch},
		Attempts: []Attempt{{Passed: score.OK(), Rounds: 6, Research: &score}},
	}
	return Summary{Results: []Result{res}}.Baseline().Cases[0]
}

// A run is only worth keeping if what it found comes back. The three rates
// are the whole of what a research case found, and a baseline that dropped
// them would compare two runs on their verdicts alone.
func TestABaselineKeepsTheThreeResearchRates(t *testing.T) {
	c := researchBaseline(Rate{Got: 1, Of: 2}, Rate{Got: 2, Of: 2}, Rate{Got: 2, Of: 2})
	if c.Research == nil {
		t.Fatal("a research case's rates must be written down")
	}
	if c.Research.Cited != (Rate{Got: 1, Of: 2}) || c.Research.Citations != 1 {
		t.Errorf("cited = %v, cited-not-read = %d", c.Research.Cited, c.Research.Citations)
	}
	if c.Table != nil {
		t.Errorf("a research case has no table: %+v", c.Table)
	}

	path := filepath.Join(t.TempDir(), "baseline.json")
	if err := WriteBaseline(path, Baseline{Version: BaselineVersion, Cases: []CaseBaseline{c}}); err != nil {
		t.Fatal(err)
	}
	back, err := ReadBaseline(path)
	if err != nil {
		t.Fatal(err)
	}
	if back.Cases[0].Research == nil || *back.Cases[0].Research != *c.Research {
		t.Errorf("the rates did not survive the file: %+v", back.Cases[0].Research)
	}
}

// A write-up that started citing pages the run never fetched has got worse in
// a way the verdict does not move: both runs failed, and the comparison has
// to say what changed anyway.
func TestACompareReadsAWorseCitationRateAsARegression(t *testing.T) {
	before := researchBaseline(Rate{Got: 2, Of: 2}, Rate{Got: 1, Of: 1}, Rate{Got: 1, Of: 2})
	after := researchBaseline(Rate{Got: 1, Of: 3}, Rate{Got: 1, Of: 1}, Rate{Got: 1, Of: 2})
	d := delta(before, after)
	if d.Change != Regressed {
		t.Errorf("change = %v, want a regression", d.Change)
	}
	if !strings.Contains(d.Why, "cited, not read") {
		t.Errorf("why = %q", d.Why)
	}
	// And a fact the write-up stopped saying is the next reading down.
	fewer := researchBaseline(Rate{Got: 2, Of: 2}, Rate{Got: 1, Of: 1}, Rate{Got: 0, Of: 2})
	if d := delta(before, fewer); d.Change != Regressed || !strings.Contains(d.Why, "facts") {
		t.Errorf("change = %v, why = %q", d.Change, d.Why)
	}
	// A run that read every page it cited and said everything it did before
	// moved nothing, whatever the rounds did inside the band.
	if d := delta(before, before); d.Change != Unchanged {
		t.Errorf("change = %v, why = %q", d.Change, d.Why)
	}
}

// A reader who named the cases they wanted has said which comparison they
// want, and the rest of the file is not a case that vanished.
func TestNarrowKeepsTheNamedCasesInTheFilesOwnOrder(t *testing.T) {
	before := Baseline{Version: BaselineVersion, Cases: []CaseBaseline{
		{Name: "alpha", Verdict: "passed"}, {Name: "beta", Verdict: "failed"}, {Name: "gamma", Verdict: "passed"},
	}}
	got := Narrow(before, []string{"gamma", "alpha"})
	if len(got.Cases) != 2 || got.Cases[0].Name != "alpha" || got.Cases[1].Name != "gamma" {
		t.Fatalf("narrowed to %+v", got.Cases)
	}
	if got.Version != before.Version {
		t.Error("a narrowed baseline is the same file, read in part")
	}
	if len(Narrow(before, nil).Cases) != 0 {
		t.Error("narrowing to nothing is nothing, not everything")
	}
}

// withDigest is a result whose case was loaded holding the given version.
func withDigest(res Result, digest string) Result {
	res.Case.Digest = digest
	return res
}

// A refreshed file is reviewed as a diff, so each row's digest sits under its
// case's name where that diff shows which case changed.
func TestBaselineWritesEachCasesDigestUnderItsName(t *testing.T) {
	sum := Summary{Results: []Result{withDigest(workspaceResult("fix-it", []int{9}, true), "d1")}}
	b := sum.Baseline()
	if b.Version != BaselineVersion || b.Cases[0].Digest != "d1" {
		t.Fatalf("baseline = version %d, digest %q", b.Version, b.Cases[0].Digest)
	}
	path := filepath.Join(t.TempDir(), "baseline.json")
	if err := WriteBaseline(path, b); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := "\"name\": \"fix-it\",\n      \"kind\": \"workspace\",\n      \"digest\": \"d1\","; !strings.Contains(string(data), want) {
		t.Errorf("the digest is not under the case's name:\n%s", data)
	}
	got, err := ReadBaseline(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Cases[0].Digest != "d1" {
		t.Errorf("read back digest %q", got.Cases[0].Digest)
	}
}

// An edited case is a different test, so neither direction can be read off
// it, and the gate never counts it.
func TestCompare_AChangedCaseIsNotComparable(t *testing.T) {
	for _, tc := range []struct {
		name          string
		before, after Result
	}{
		{"passed then failed every attempt",
			workspaceResult("a", []int{9, 9, 9}, true, true, true), workspaceResult("a", []int{9, 9, 9}, false, false, false)},
		{"failed then passed",
			workspaceResult("a", []int{9, 9, 9}, false, false, false), workspaceResult("a", []int{9, 9, 9}, true, true, true)},
		{"a table that let more through",
			tableCaseResult("a", 18, 2), tableCaseResult("a", 15, 5)},
		{"the same result", workspaceResult("a", []int{9}, true), workspaceResult("a", []int{9}, true)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := Summary{Results: []Result{withDigest(tc.before, "old")}}.Baseline()
			after := Summary{Results: []Result{withDigest(tc.after, "new")}}.Baseline()
			cmp, err := Compare(before, after)
			if err != nil {
				t.Fatal(err)
			}
			d := cmp.Cases[0]
			if d.Change != Incomparable {
				t.Errorf("change = %v, want Incomparable", d.Change)
			}
			if !strings.Contains(d.Why, "the case changed since the baseline was recorded") {
				t.Errorf("why = %q", d.Why)
			}
			if regs := cmp.Regressions(); len(regs) != 0 {
				t.Errorf("a changed case stopped the gate: %+v", regs)
			}
			improved, regressed, unchanged, incomparable := cmp.Tally()
			if improved != 0 || regressed != 0 || unchanged != 0 || incomparable != 1 {
				t.Errorf("tally = %d improved, %d regressed, %d unchanged, %d not comparable",
					improved, regressed, unchanged, incomparable)
			}
		})
	}
}

// The same version of a case either side is compared on its numbers.
func TestCompare_AnUnchangedDigestComparesTheNumbers(t *testing.T) {
	before := Summary{Results: []Result{withDigest(workspaceResult("a", []int{9, 9, 9}, true, true, true), "same")}}.Baseline()
	after := Summary{Results: []Result{withDigest(workspaceResult("a", []int{9, 9, 9}, false, false, false), "same")}}.Baseline()
	cmp, err := Compare(before, after)
	if err != nil {
		t.Fatal(err)
	}
	if cmp.Cases[0].Change != Regressed || len(cmp.Regressions()) != 1 {
		t.Errorf("change = %v, regressions = %d", cmp.Cases[0].Change, len(cmp.Regressions()))
	}
	if cmp.Undigested {
		t.Error("a baseline that records digests was read as one that does not")
	}
}

// A file written before rows had a digest is still read, and its rows compare
// as they always did rather than every one refusing until it is refreshed.
func TestCompare_ABaselineWithoutDigestsComparesAsBefore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "baseline.json")
	body := `{"version":1,"cases":[{"name":"a","kind":"workspace","verdict":"passed","attempts":3,"passes":3,"median_rounds":9}]}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := ReadBaseline(path)
	if err != nil {
		t.Fatalf("a format 1 baseline was refused: %v", err)
	}
	if before.Cases[0].Digest != "" {
		t.Errorf("digest = %q, want none", before.Cases[0].Digest)
	}
	after := Summary{Results: []Result{withDigest(workspaceResult("a", []int{9, 9, 9}, false, false, false), "now")}}.Baseline()
	cmp, err := Compare(before, after)
	if err != nil {
		t.Fatal(err)
	}
	if !cmp.Undigested {
		t.Error("the comparison should say the baseline records no digests")
	}
	if cmp.Cases[0].Change != Regressed || len(cmp.Regressions()) != 1 {
		t.Errorf("change = %v, regressions = %d — a row with no digest compares as it always did",
			cmp.Cases[0].Change, len(cmp.Regressions()))
	}
}

// assertedResult is a workspace case that declares two assertions, one attempt
// per entry of broken, each breaking that many.
func assertedResult(name string, broken ...int) Result {
	res := Result{Case: Case{Name: name, Kind: KindWorkspace, Assertions: []Assertion{{Word: wordNoShell}, {Word: wordNoRepeat}}}}
	for _, n := range broken {
		var fs []Finding
		for i := 0; i < 2; i++ {
			fs = append(fs, Finding{Assertion: res.Case.Assertions[i], Held: i >= n})
		}
		res.Attempts = append(res.Attempts, Attempt{Passed: n == 0, CheckPassed: true, Findings: fs, Rounds: 4, Elapsed: time.Minute})
	}
	return res
}

// The count a case declares and the median that broke are kept apart, and a
// case that declares none carries neither.
func TestBaselineKeepsDeclaredAndBrokenApart(t *testing.T) {
	b := Summary{Results: []Result{assertedResult("asserted", 1, 1, 2), workspaceResult("plain", []int{3}, true)}}.Baseline()
	got := b.Cases[0].Assertions
	if got == nil || got.Declared != 2 || got.MedianBroken != 1 {
		t.Errorf("assertions = %+v, want 2 declared and a median of 1 broken", got)
	}
	if b.Cases[1].Assertions != nil {
		t.Errorf("a case that declares none carries no assertions: %+v", b.Cases[1].Assertions)
	}
	if b.Version != BaselineVersion {
		t.Errorf("the format version moved to %d", b.Version)
	}
	data, err := json.Marshal(b.Cases[1])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "assertions") {
		t.Errorf("a row without assertions should not write the key: %s", data)
	}
}

// More broken under a held verdict is a control opening further, even though
// the case failed both times.
func TestMoreBrokenAssertionsUnderAHeldVerdictIsAControlOpening(t *testing.T) {
	before := Summary{Results: []Result{assertedResult("asserted", 1)}}.Baseline()
	after := Summary{Results: []Result{assertedResult("asserted", 2)}}.Baseline()
	cmp, err := Compare(before, after)
	if err != nil {
		t.Fatal(err)
	}
	d := cmp.Cases[0]
	if d.Change != Regressed || !strings.Contains(d.Why, "further open") || !strings.Contains(d.Why, "1 → 2") {
		t.Errorf("delta = %v %q", d.Change, d.Why)
	}
	back, err := Compare(after, before)
	if err != nil {
		t.Fatal(err)
	}
	if back.Cases[0].Change != Improved {
		t.Errorf("fewer broken should read as improved: %v", back.Cases[0].Change)
	}
}
