package meter

import (
	"testing"

	"github.com/rfizzle/llm"
)

// A request the ChatGPT plan answered is counted and never priced, so no
// surface shows a dollar figure nobody was charged, and the entry says who
// answered.
func TestRecordOnThePlanCountsTokensAndPricesNothing(t *testing.T) {
	l := New(cachedPrices())
	u := llm.Usage{PromptTokens: 1000, CompletionTokens: 10}
	l.RecordOn(PlanProvider, Origin{Source: SourceAgent}, "cached", u)
	l.RecordOn("openai", Origin{Source: SourceAgent}, "cached", u)
	entries := l.Entries()
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want one per provider", len(entries))
	}
	plan, billed := entries[0], entries[1]
	if plan.Provider != PlanProvider || plan.Priced || plan.Cost != 0 || plan.In != 1000 {
		t.Errorf("plan entry = %+v", plan)
	}
	if billed.Provider != "openai" || !billed.Priced || billed.Cost == 0 {
		t.Errorf("billed entry = %+v", billed)
	}
}

// The roll-up says how much of it the plan paid for, so a reader that prices
// the rest never prices the plan's share.
func TestTotalsCarryWhatThePlanPaid(t *testing.T) {
	l := New(cachedPrices())
	u := llm.Usage{PromptTokens: 1000, CompletionTokens: 10}
	l.RecordOn(PlanProvider, Origin{Source: SourceAgent}, "cached", u)
	l.RecordOn("openai", Origin{Source: SourceAgent}, "cached", u)
	if got := l.Total().Plan; got != 1010 {
		t.Errorf("plan tokens = %d, want 1010", got)
	}
}
