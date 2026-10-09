package cli

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/eval"
)

// classifierRun is a classifier table whose rows each took took and whose
// attempt cost cost, with one false allow and one false deny among them.
func classifierRun(took time.Duration, cost float64) eval.Result {
	score := eval.Score{Kind: eval.KindClassifier}
	for _, a := range []struct{ want, got string }{
		{eval.LabelDeny, eval.LabelAllow}, {eval.LabelAllow, eval.LabelDeny},
		{eval.LabelDeny, eval.LabelDeny}, {eval.LabelAllow, eval.LabelAllow},
	} {
		score.Answers = append(score.Answers, eval.Answer{
			Row: eval.Row{Name: "r", Expect: []string{a.want}}, Label: a.got, Elapsed: took,
		})
	}
	return eval.Result{
		Case:     eval.Case{Name: "classifier-decisions", Kind: eval.KindClassifier},
		Attempts: []eval.Attempt{{Score: &score, Cost: cost, Priced: true, Elapsed: 4 * took}},
	}
}

// Two baselines on the two backends are read against each other: the report
// says the backend moved, keeps false allows and false denies apart, and
// gives each side's median time and cost for one verdict.
func TestCompareReport_SetsOneClassifierBackendAgainstTheOther(t *testing.T) {
	before := eval.Summary{Model: "gpt-6-luna", Results: []eval.Result{classifierRun(2*time.Second, 0.04)}}.Baseline()
	after := eval.Summary{Model: "gpt-6-luna", ClassifierBackend: "decisions",
		Results: []eval.Result{classifierRun(200*time.Millisecond, 0.0004)}}.Baseline()

	r := compareReport(compareOf(t, before, after), time.Now())
	var noted bool
	for _, n := range r.Notes {
		noted = noted || strings.Contains(n.Text, "on completion and this run on decisions")
	}
	if !noted {
		t.Fatalf("the backend change is not noted: %+v", r.Notes)
	}
	detail := r.Sections[0].Rows[0].Detail
	for _, want := range []string{"1 → 1 false allow", "1 → 1 false deny", "2.0s → 200ms a verdict", "$0.01 → $0.00010 a verdict"} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail %q lacks %q", detail, want)
		}
	}

	out := evalReport(eval.Summary{Model: "gpt-6-luna", ClassifierBackend: "decisions",
		Results: []eval.Result{classifierRun(200*time.Millisecond, 0.0004)}}, "")
	if !strings.Contains(out.Subject, "classifier on decisions") {
		t.Errorf("subject = %q", out.Subject)
	}
	if row := out.Sections[0].Rows[0]; !strings.Contains(row.Subject, "200ms a verdict") ||
		!strings.Contains(row.Consequence, "1 false allow · 1 false deny") {
		t.Errorf("row = %+v", row)
	}
}

// A backend the classifier does not have is refused before anything is
// asked.
func TestEval_RefusesAnUnknownClassifierBackend(t *testing.T) {
	cmd := newEvalCmd()
	cmd.SetArgs([]string{t.TempDir(), "--classifier-backend", "oracle"})
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--classifier-backend") {
		t.Fatalf("err = %v", err)
	}
}

// Every row of a table asked for probabilities is printed with its verdict,
// the ones that were right beside the ones that were not.
func TestEval_ClassifierRowsShowTheirProbability(t *testing.T) {
	score := eval.Score{Kind: eval.KindClassifier}
	for _, a := range []struct {
		name, want, got string
		p               float64
	}{{"reads a file", eval.LabelAllow, eval.LabelAllow, 0.93}, {"wipes the disk", eval.LabelDeny, eval.LabelAllow, 0.8125}} {
		score.Answers = append(score.Answers, eval.Answer{
			Row: eval.Row{Name: a.name, Expect: []string{a.want}}, Label: a.got, Probability: a.p, Probed: true,
		})
	}
	res := eval.Result{
		Case:     eval.Case{Name: "classifier-decisions", Kind: eval.KindClassifier},
		Attempts: []eval.Attempt{{Score: &score}},
	}
	out := evalReport(eval.Summary{Model: "m", ClassifierBackend: "decisions", ClassifierThreshold: 70, Results: []eval.Result{res}}, "")
	if !strings.Contains(out.Subject, "allowed at 70%") {
		t.Errorf("subject = %q", out.Subject)
	}
	body := strings.Join(out.Sections[0].Rows[0].Body, "\n")
	for _, want := range []string{"reads a file — allow at 93%", "wipes the disk — allow at 81% (wanted deny)"} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q:\n%s", want, body)
		}
	}

	cmd := newEvalCmd()
	cmd.SetArgs([]string{t.TempDir(), "--classifier-threshold", "101"})
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "--classifier-threshold") {
		t.Errorf("err = %v", err)
	}
}

// A request that came back with nothing took however long it took to give
// up, and the median is over the verdicts that came back; the report says
// how many did not.
func TestEval_MedianVerdictSkipsFailures(t *testing.T) {
	score := eval.Score{Kind: eval.KindClassifier}
	for i, took := range []time.Duration{100 * time.Millisecond, 300 * time.Millisecond, 30 * time.Second, 30 * time.Second, 30 * time.Second} {
		a := eval.Answer{Row: eval.Row{Name: fmt.Sprint(i), Expect: []string{eval.LabelAllow}}, Elapsed: took}
		if i < 2 {
			a.Label = eval.LabelAllow
		} else {
			a.Err = "timed out"
		}
		score.Answers = append(score.Answers, a)
	}
	if got := score.MedianVerdict(); got != 200*time.Millisecond {
		t.Fatalf("median = %v, want the answered rows' 200ms", got)
	}
	res := eval.Result{
		Case:     eval.Case{Name: "classifier-decisions", Kind: eval.KindClassifier},
		Attempts: []eval.Attempt{{Score: &score}},
	}
	if detail := evalDetail(res); !strings.Contains(detail, "200ms a verdict (3 failed)") {
		t.Errorf("detail = %q", detail)
	}
}
