package meter

import (
	"context"
	"sync"
	"testing"

	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/provider"
)

func testPrices() *pricing.Table {
	return pricing.NewTable(map[string]pricing.ModelPricing{
		"big":   {InputCostPerToken: 0.00001, OutputCostPerToken: 0.00002},
		"small": {InputCostPerToken: 0.0000001, OutputCostPerToken: 0.0000002},
	})
}

// stubProvider emits one usage report on the terminal event, the way every
// real provider does.
type stubProvider struct {
	usage  provider.Usage
	mu     sync.Mutex
	models []string
}

func (s *stubProvider) Name() string { return "stub" }

func (s *stubProvider) StreamCompletion(ctx context.Context, _ []provider.Message, opts provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
	s.mu.Lock()
	s.models = append(s.models, opts.Model)
	s.mu.Unlock()
	ch := make(chan provider.StreamEvent, 2)
	ch <- provider.StreamEvent{Token: "hi"}
	u := s.usage
	ch <- provider.StreamEvent{Usage: &u, Done: true}
	close(ch)
	return ch, nil
}

type stubLister struct{ *stubProvider }

func (s *stubLister) ListModels(context.Context) ([]string, error) { return []string{"a"}, nil }

func drain(t *testing.T, p provider.Provider, model string) {
	t.Helper()
	ev, err := p.StreamCompletion(context.Background(), nil, provider.CompletionOpts{Model: model})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	for range ev {
	}
}

// The gate is the choke point: a feature holding a gated provider bills the
// ledger without knowing the ledger exists.
func TestGate_BillsWhatPassesThrough(t *testing.T) {
	l := New(testPrices())
	p := l.For(&stubProvider{usage: provider.Usage{PromptTokens: 1000, CompletionTokens: 100}}, SourceAgent)
	drain(t, p, "big")
	drain(t, p, "big")

	got := l.Total()
	if got.In != 2000 || got.Out != 200 || got.Requests != 2 {
		t.Fatalf("two requests should bill twice, got %+v", got)
	}
	if !got.Priced || got.Cost <= 0 {
		t.Fatalf("a priced model should give the session a cost, got %+v", got)
	}
}

// Each requester is priced against the model that actually answered, not
// against whichever model the session is on.
func TestGate_PricesEachOriginAgainstItsOwnModel(t *testing.T) {
	l := New(testPrices())
	stub := &stubProvider{usage: provider.Usage{PromptTokens: 1000, CompletionTokens: 100}}
	drain(t, l.For(stub, SourceAgent), "big")
	drain(t, l.For(stub, SourceSummary), "small")

	big := l.SourceTotal(SourceAgent)
	small := l.SourceTotal(SourceSummary)
	if big.Cost <= small.Cost*10 {
		t.Fatalf("the cheap model should cost far less: agent %v, summary %v", big.Cost, small.Cost)
	}
	if total := l.Total(); total.Cost != big.Cost+small.Cost {
		t.Fatalf("the total should be the sum of its parts: %v vs %v", total.Cost, big.Cost+small.Cost)
	}
}

// A fan-out bills several children at once, and "sub-agents cost X" is not an
// answer to "which of them cost that".
func TestGate_NamesIndividualSubagents(t *testing.T) {
	l := New(testPrices())
	stub := &stubProvider{usage: provider.Usage{PromptTokens: 500, CompletionTokens: 50}}
	drain(t, l.ForOrigin(stub, Origin{Source: SourceSubagent, Label: "scout"}), "small")
	drain(t, l.ForOrigin(stub, Origin{Source: SourceSubagent, Label: "scribe"}), "small")
	drain(t, l.ForOrigin(stub, Origin{Source: SourceSubagent, Label: "scribe"}), "small")

	if got := l.SourceTotal(SourceSubagent); got.In != 1500 {
		t.Fatalf("the class total covers every child, got %+v", got)
	}
	scout := l.OriginTotal(Origin{Source: SourceSubagent, Label: "scout"})
	scribe := l.OriginTotal(Origin{Source: SourceSubagent, Label: "scribe"})
	if scout.In != 500 || scribe.In != 1000 {
		t.Fatalf("each child is named separately: scout %+v, scribe %+v", scout, scribe)
	}
	if got := len(l.ByOrigin()); got != 2 {
		t.Fatalf("two children, two rows, got %d", got)
	}
}

// A gated request that declares nothing is visible rather than lost: the
// whole point of the gate is that a feature added later cannot under-report.
func TestGate_UndeclaredSpendIsVisible(t *testing.T) {
	l := New(testPrices())
	drain(t, l.For(&stubProvider{usage: provider.Usage{PromptTokens: 10, CompletionTokens: 1}}, ""), "big")

	if got := l.SourceTotal(SourceUnattributed); got.In != 10 {
		t.Fatalf("undeclared spend lands in its own bucket, got %+v", got)
	}
	if got := l.Total(); got.In != 10 {
		t.Fatalf("and it still counts toward the session, got %+v", got)
	}
}

// Wrapping must not cost the session a capability it had.
func TestGate_ForwardsModelListingOnlyWhereItExists(t *testing.T) {
	l := New(nil)
	plain := l.For(&stubProvider{}, SourceAgent)
	if _, ok := plain.(provider.ModelLister); ok {
		t.Fatal("a provider that cannot enumerate must not appear to")
	}
	rich := l.For(&stubLister{stubProvider: &stubProvider{}}, SourceAgent)
	lister, ok := rich.(provider.ModelLister)
	if !ok {
		t.Fatal("a provider that can enumerate must still say so through the gate")
	}
	if models, err := lister.ListModels(context.Background()); err != nil || len(models) != 1 {
		t.Fatalf("listing should pass through: %v %v", models, err)
	}
}

// The ledger is read from the render loop while children write to it from
// their own goroutines.
func TestLedger_ConcurrentWriters(t *testing.T) {
	l := New(testPrices())
	stub := &stubProvider{usage: provider.Usage{PromptTokens: 100, CompletionTokens: 10}}
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p := l.ForOrigin(stub, Origin{Source: SourceSubagent, Label: string(rune('a' + i))})
			for range 25 {
				ev, _ := p.StreamCompletion(context.Background(), nil, provider.CompletionOpts{Model: "small"})
				for range ev {
				}
			}
		}()
	}
	wg.Wait()
	if got := l.Total(); got.In != 8*25*100 || got.Requests != 200 {
		t.Fatalf("every write should land, got %+v", got)
	}
}

// A stream nobody finishes reading was still paid for.
func TestGate_AbandonedStreamIsStillBilled(t *testing.T) {
	l := New(testPrices())
	p := l.For(&stubProvider{usage: provider.Usage{PromptTokens: 700, CompletionTokens: 7}}, SourceAgent)
	ctx, cancel := context.WithCancel(context.Background())
	ev, err := p.StreamCompletion(ctx, nil, provider.CompletionOpts{Model: "big"})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	<-ev // take the first token, then walk away
	cancel()
	for range ev {
	}
	if got := l.Total(); got.In != 700 {
		t.Fatalf("an abandoned stream is spend the session made, got %+v", got)
	}
}
