package cli

import (
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
