package chat

import (
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/meter"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/provider"
)

// spendModel is a session wired the way the CLI wires one: a ledger behind
// the provider gate, and a pricing table that knows the session's model and a
// cheaper one for the background to run on.
func spendModel(t *testing.T) (Model, *meter.Ledger) {
	t.Helper()
	table := pricing.NewTable(map[string]pricing.ModelPricing{
		"gpt-4o": {InputCostPerToken: 0.00001, OutputCostPerToken: 0.00002, MaxInputTokens: 200000},
		"cheap":  {InputCostPerToken: 0.0000001, OutputCostPerToken: 0.0000002},
	})
	ledger := meter.New(table)
	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream).
		WithPricing(table, "gpt-4o").
		WithLedger(ledger)
	return m, ledger
}

// Background spend used to be added straight to the session totals, which the
// next agent round then overwrote from its own accounting. Billing at the
// gate means there is only one place the number comes from, so a round cannot
// erase what the classifier and the summary spent before it.
func TestSpend_BackgroundSpendSurvivesTheNextRound(t *testing.T) {
	m, ledger := spendModel(t)
	m.vitals.startTurn()

	ledger.Record(meter.Origin{Source: meter.SourceSummary}, "cheap", provider.Usage{PromptTokens: 800, CompletionTokens: 30})
	ledger.Record(meter.Origin{Source: meter.SourceClassifier}, "cheap", provider.Usage{PromptTokens: 500, CompletionTokens: 20})
	ledger.Record(meter.Origin{Source: meter.SourceAgent}, "gpt-4o", provider.Usage{PromptTokens: 1000, CompletionTokens: 100})
	m.accumulateUsage(&provider.Usage{PromptTokens: 1000, CompletionTokens: 100})

	if got := m.sessionSpend(); got.In != 2300 || got.Out != 150 {
		t.Fatalf("the session keeps every request it paid for: want ↑2300 ↓150, got ↑%d ↓%d", got.In, got.Out)
	}
	// The agent's own figure stays the agent's own — the rail distinguishes
	// "what this turn is costing" from "what this session is costing".
	if m.TotalTokensIn != 1000 || m.TotalTokensOut != 100 {
		t.Fatalf("the agent's own spend is its turns alone, got ↑%d ↓%d", m.TotalTokensIn, m.TotalTokensOut)
	}
}

// Background work runs on a cheaper model, and pricing it at the session's
// rate is how a session overstates what it cost.
func TestSpend_EachSourceIsPricedAtItsOwnModel(t *testing.T) {
	_, ledger := spendModel(t)
	ledger.Record(meter.Origin{Source: meter.SourceAgent}, "gpt-4o", provider.Usage{PromptTokens: 1000, CompletionTokens: 0})
	ledger.Record(meter.Origin{Source: meter.SourceSummary}, "cheap", provider.Usage{PromptTokens: 1000, CompletionTokens: 0})

	agentCost := ledger.SourceTotal(meter.SourceAgent).Cost
	summaryCost := ledger.SourceTotal(meter.SourceSummary).Cost
	if agentCost <= summaryCost*10 {
		t.Fatalf("the same tokens on a cheaper model cost less: agent %v, summary %v", agentCost, summaryCost)
	}
}

// /stats names what the session's money went on, down to the individual
// sub-agent — "sub-agents cost this much" is not an answer to which of them
// did.
func TestSpend_StatsNamesEverySource(t *testing.T) {
	m, ledger := spendModel(t)
	ledger.Record(meter.Origin{Source: meter.SourceAgent}, "gpt-4o", provider.Usage{PromptTokens: 1000, CompletionTokens: 100})
	ledger.Record(meter.Origin{Source: meter.SourceSummary}, "cheap", provider.Usage{PromptTokens: 800, CompletionTokens: 30})
	ledger.Record(meter.Origin{Source: meter.SourceSubagent, Label: "researcher-1"}, "cheap", provider.Usage{PromptTokens: 400, CompletionTokens: 40})
	ledger.Record(meter.Origin{Source: meter.SourceSubagent, Label: "writer-1"}, "gpt-4o", provider.Usage{PromptTokens: 600, CompletionTokens: 60})

	report := m.statsReport()
	for _, want := range []string{"By source:", "agent", "summary", "sub-agent", "researcher-1", "writer-1", "By model:", "cheap", "gpt-4o"} {
		if !strings.Contains(report, want) {
			t.Fatalf("/stats should name %q:\n%s", want, report)
		}
	}
	// The session line is the whole bill, not the agent's share of it.
	if !strings.Contains(report, "↑2.8k") {
		t.Fatalf("session spend should total every source:\n%s", report)
	}
}

// A single sub-agent is already named by its class row; repeating it under
// itself is noise.
func TestSpend_StatsDoesNotRepeatALoneChild(t *testing.T) {
	m, ledger := spendModel(t)
	ledger.Record(meter.Origin{Source: meter.SourceAgent}, "gpt-4o", provider.Usage{PromptTokens: 1000, CompletionTokens: 100})
	ledger.Record(meter.Origin{Source: meter.SourceSubagent, Label: "writer-1"}, "gpt-4o", provider.Usage{PromptTokens: 600, CompletionTokens: 60})

	if report := m.statsReport(); strings.Contains(report, "writer-1") {
		t.Fatalf("one child of a class is the class row:\n%s", report)
	}
}

// One source and one model say nothing the session total does not.
func TestSpend_StatsStaysQuietForASingleSource(t *testing.T) {
	m, ledger := spendModel(t)
	ledger.Record(meter.Origin{Source: meter.SourceAgent}, "gpt-4o", provider.Usage{PromptTokens: 1000, CompletionTokens: 100})

	report := m.statsReport()
	if strings.Contains(report, "By source:") || strings.Contains(report, "By model:") {
		t.Fatalf("a breakdown of one is not a breakdown:\n%s", report)
	}
}

// The rail's session line is the whole bill; its main line is the agent's own.
func TestSpend_InspectorSeparatesTheAgentFromTheSession(t *testing.T) {
	m, ledger := spendModel(t)
	m.vitals.startTurn()
	ledger.Record(meter.Origin{Source: meter.SourceAgent}, "gpt-4o", provider.Usage{PromptTokens: 1000, CompletionTokens: 100})
	m.accumulateUsage(&provider.Usage{PromptTokens: 1000, CompletionTokens: 100})
	ledger.Record(meter.Origin{Source: meter.SourceSubagent, Label: "writer-1"}, "gpt-4o", provider.Usage{PromptTokens: 5000, CompletionTokens: 500})

	s := m.inspectorSpend()
	if s == nil {
		t.Fatal("a session that has spent something has a spend block")
	}
	if s.Main == s.Session {
		t.Fatalf("children are part of the session and not part of the agent: main %q, session %q", s.Main, s.Session)
	}
	if s.Children == "" {
		t.Fatal("a session with children says what they cost")
	}
}

// The observer is what persists a session's cost, so it has to be told what
// the whole session spent — and at what price, since the session is a mixture
// of models the recorder cannot price for itself.
func TestSpend_ObserverReportsThePricedSessionTotal(t *testing.T) {
	var gotIn, gotOut int64
	var gotCost float64
	var gotPriced bool
	m, ledger := spendModel(t)
	m = m.WithObserver(observe.Observer{Usage: func(_, in, out int64, cost float64, priced bool) {
		gotIn, gotOut, gotCost, gotPriced = in, out, cost, priced
	}})
	m.vitals.startTurn()

	ledger.Record(meter.Origin{Source: meter.SourceSubagent, Label: "writer-1"}, "cheap", provider.Usage{PromptTokens: 2000, CompletionTokens: 200})
	ledger.Record(meter.Origin{Source: meter.SourceAgent}, "gpt-4o", provider.Usage{PromptTokens: 1000, CompletionTokens: 100})
	m.accumulateUsage(&provider.Usage{PromptTokens: 1000, CompletionTokens: 100})

	if gotIn != 3000 || gotOut != 300 {
		t.Fatalf("the recorded session is every request it made, got ↑%d ↓%d", gotIn, gotOut)
	}
	if !gotPriced || gotCost <= 0 {
		t.Fatalf("the cost travels with the tokens, got %v priced=%v", gotCost, gotPriced)
	}
	if want := ledger.Total().Cost; gotCost != want {
		t.Fatalf("the recorded cost is the ledger's: want %v, got %v", want, gotCost)
	}
}

// /clear starts the accounting over, spend included.
func TestSpend_ClearResetsTheLedger(t *testing.T) {
	m, ledger := spendModel(t)
	ledger.Record(meter.Origin{Source: meter.SourceAgent}, "gpt-4o", provider.Usage{PromptTokens: 1000, CompletionTokens: 100})
	m.clearConversation()

	if got := m.sessionSpend(); got.In != 0 || got.Out != 0 || got.Cost != 0 {
		t.Fatalf("a cleared session has spent nothing, got %+v", got)
	}
}

// A session assembled without a ledger still reports what the agent spent,
// rather than reporting nothing.
func TestSpend_NoLedgerFallsBackToTheAgentsOwnAccounting(t *testing.T) {
	m := vitalsModel(t)
	m.vitals.startTurn()
	m.accumulateUsage(&provider.Usage{PromptTokens: 1000, CompletionTokens: 100})

	if got := m.sessionSpend(); got.In != 1000 || got.Out != 100 || !got.Priced {
		t.Fatalf("without a ledger the agent's own accounting answers, got %+v", got)
	}
}

// A summary reading is spend, and it is the summary's spend — not a mystery
// increase in what the agent's turns cost.
func TestSpend_SummaryIsAttributedToTheSummary(t *testing.T) {
	m := summaryModel(t, &readingProvider{text: "Reading the loop."})
	m = applyReading(t, m)

	if got := m.ledger.SourceTotal(meter.SourceSummary); got.In != 800 || got.Out != 30 {
		t.Fatalf("the reading is billed to the summary, got %+v", got)
	}
	if got := m.ledger.SourceTotal(meter.SourceAgent); got.In != 0 {
		t.Fatalf("and not to the agent, got %+v", got)
	}
}
