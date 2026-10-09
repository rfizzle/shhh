package meter

import (
	"context"
	"testing"

	"github.com/rfizzle/shhh/internal/provider"
)

// stubDecider answers every question at one probability and reports the
// usage it was given, output included, so the test can see what is billed.
type stubDecider struct {
	*stubProvider
	usage provider.Usage
}

func (s *stubDecider) OffersDecisions(model string) bool { return model == "small" }

func (s *stubDecider) Decide(_ context.Context, req provider.DecisionRequest) (provider.DecisionResult, error) {
	return provider.DecisionResult{
		Answers: []provider.DecisionAnswer{{Type: provider.AnswerPredicate, Name: "q", Probability: 0.9}},
		Usage:   s.usage,
	}, nil
}

type stubListerDecider struct{ *stubDecider }

func (s *stubListerDecider) ListModels(context.Context) ([]string, error) { return []string{"a"}, nil }

// The gate answers the Decider question the way its inner provider does —
// as it does ModelLister — and bills a decisions request to the gate's own
// source at its input tokens alone.
func TestLedger_KeepsTheDecider(t *testing.T) {
	l := New(testPrices())
	if _, ok := l.For(&stubProvider{}, SourceClassifier).(provider.Decider); ok {
		t.Fatal("a provider that cannot decide must not appear to")
	}

	inner := &stubDecider{stubProvider: &stubProvider{}, usage: provider.Usage{PromptTokens: 1000, CompletionTokens: 50}}
	p := l.For(inner, SourceClassifier)
	d, ok := p.(provider.Decider)
	if !ok {
		t.Fatal("a provider that can decide must still say so through the gate")
	}
	if _, ok := p.(provider.ModelLister); ok {
		t.Fatal("the gate must not invent a catalog query either")
	}
	if !d.OffersDecisions("small") || d.OffersDecisions("big") {
		t.Fatal("which models offer it is the inner provider's answer")
	}
	res, err := d.Decide(context.Background(), provider.DecisionRequest{Model: "small"})
	if err != nil || len(res.Answers) != 1 {
		t.Fatalf("decide should pass through: %+v %v", res, err)
	}
	got := l.Entries()
	if len(got) != 1 || got[0].Origin.Source != SourceClassifier || got[0].Model != "small" {
		t.Fatalf("billed to %+v, want the classifier on its model", got)
	}
	if got[0].In != 1000 || got[0].Out != 0 || got[0].Requests != 1 {
		t.Fatalf("billed %+v, want 1000 input tokens and nothing else", got[0])
	}

	both := l.For(&stubListerDecider{stubDecider: inner}, SourceClassifier)
	if _, ok := both.(provider.Decider); !ok {
		t.Fatal("a lister that decides keeps the capability")
	}
	if _, ok := both.(provider.ModelLister); !ok {
		t.Fatal("a lister that decides keeps the catalog")
	}
	if _, ok := WithFallbackModel(both, "small").(provider.Decider); !ok {
		t.Fatal("a fallback model must not cost the capability")
	}
}
