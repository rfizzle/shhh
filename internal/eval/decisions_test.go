package eval

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/provider"
)

// fakeDecisions answers the Decisions API from the evidence, the way fakeAux
// answers a completion, and fails the test if a completion is asked.
type fakeDecisions struct {
	t       *testing.T
	decided int
}

func (f *fakeDecisions) Name() string { return "fake" }

func (f *fakeDecisions) StreamCompletion(context.Context, []provider.Message, provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
	f.t.Fatal("the decisions backend asked a completion")
	return nil, nil
}

func (f *fakeDecisions) OffersDecisions(string) bool { return true }

func (f *fakeDecisions) Decide(_ context.Context, req provider.DecisionRequest) (provider.DecisionResult, error) {
	f.decided++
	p := 0.95
	if strings.Contains(req.Input, "answers-deny") {
		p = 0.1
	}
	return provider.DecisionResult{
		Answers: []provider.DecisionAnswer{{Type: provider.AnswerPredicate, Name: req.Questions[0].Name, Probability: p}},
		Usage:   provider.Usage{PromptTokens: 1000},
	}, nil
}

// The classifier table runs on either backend, and a run on the decisions
// backend is written down as one: its baseline names the backend, and keeps
// the false allows and false denies apart beside the median time and the cost
// of one verdict, so two baselines can be read against each other.
func TestAClassifierTableRunsOnTheDecisionsBackend(t *testing.T) {
	p := &fakeDecisions{t: t}
	c := decisionCase(
		classifierRow("let through", LabelDeny, "answers-allow"),
		classifierRow("refused", LabelAllow, "answers-deny"),
		classifierRow("right", LabelAllow, "answers-allow-too"),
	)
	sum, err := Run(context.Background(), []Case{c}, Options{
		Provider: p, Model: "gpt-6-luna", ClassifierBackend: agent.BackendDecisions,
		Price: func(_ string, in, out int) (float64, bool) { return float64(in+out) / 1e6, true },
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.decided != 3 {
		t.Fatalf("decided %d rows, want 3", p.decided)
	}
	score, _ := sum.Results[0].Score()
	if score.FalseAllow() != 1 || score.FalseDeny() != 1 || score.Correct() != 1 {
		t.Fatalf("score: %d false allow, %d false deny, %d correct", score.FalseAllow(), score.FalseDeny(), score.Correct())
	}

	b := sum.Baseline()
	if b.ClassifierBackend != agent.BackendDecisions {
		t.Fatalf("baseline backend = %q", b.ClassifierBackend)
	}
	table := b.Cases[0].Table
	if table.FalseAllow != 1 || table.FalseDeny != 1 {
		t.Fatalf("table = %+v", *table)
	}
	if want := 3000.0 / 1e6 / 3; table.CostPerVerdict != want {
		t.Fatalf("cost per verdict = %v, want %v", table.CostPerVerdict, want)
	}
	path := filepath.Join(t.TempDir(), "decisions.json")
	if err := WriteBaseline(path, b); err != nil {
		t.Fatal(err)
	}
	back, err := ReadBaseline(path)
	if err != nil || back.ClassifierBackend != agent.BackendDecisions || back.Cases[0].Table.CostPerVerdict != table.CostPerVerdict {
		t.Fatalf("round trip = %+v, %v", back, err)
	}
	if Narrow(back, []string{"decisions"}).ClassifierBackend != agent.BackendDecisions {
		t.Fatal("narrowing a baseline lost its backend")
	}
}

// A verdict's time is the row's own, and the median is taken over rows.
func TestMedianVerdictIsOverRows(t *testing.T) {
	s := Score{Answers: []Answer{{Elapsed: 3 * time.Second}, {Elapsed: time.Second}, {Elapsed: 2 * time.Second}}}
	if got := s.MedianVerdict(); got != 2*time.Second {
		t.Fatalf("median = %v", got)
	}
	s.Answers = append(s.Answers, Answer{Elapsed: 4 * time.Second})
	if got := s.MedianVerdict(); got != 2500*time.Millisecond {
		t.Fatalf("even median = %v", got)
	}
	if (Score{}).MedianVerdict() != 0 {
		t.Fatal("an empty table has no verdict time")
	}
}
