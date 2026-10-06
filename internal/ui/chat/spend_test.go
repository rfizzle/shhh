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

func TestSpend_CockpitNamesTheConfiguredWarning(t *testing.T) {
	m, ledger := spendModel(t)
	ledger.SetBudget(meter.Budget{WarningCents: 1})
	ledger.Record(meter.Origin{Source: meter.SourceAgent}, "gpt-4o", provider.Usage{PromptTokens: 1_000})

	if extra := strings.Join(m.cockpitData(true).Extra, " "); !strings.Contains(extra, "spend warning") {
		t.Fatalf("cockpit extras = %q, want spend warning", extra)
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
	if len(s.Models) != 1 {
		t.Fatalf("everything ran on one model, so there is one row: %+v", s.Models)
	}
	if row := s.Models[0]; row.Cost == s.Session || row.Children == "" {
		t.Fatalf("children are part of the session and not part of the agent, and say what they cost: row %+v, session %q", row, s.Session)
	}
}

// The model rows are shares of one bill: every model's row, its own requests
// and its children's, adds up to the session total under them, all of it the
// figure priced as it went (docs/interface/surfaces.md#the-inspector-rail).
func TestInspectorRail_SpendRowsSumToTheSessionTotal(t *testing.T) {
	m, ledger := spendModel(t)
	ledger.Record(meter.Origin{Source: meter.SourceAgent}, "gpt-4o", provider.Usage{PromptTokens: 12_000, CompletionTokens: 900, CachedTokens: 8_000})
	ledger.Record(meter.Origin{Source: meter.SourceClassifier}, "cheap", provider.Usage{PromptTokens: 3_000, CompletionTokens: 40})
	ledger.Record(meter.Origin{Source: meter.SourceSummary}, "cheap", provider.Usage{PromptTokens: 2_000, CompletionTokens: 120})
	ledger.Record(meter.Origin{Source: meter.SourceAgent}, "gpt-4o", provider.Usage{PromptTokens: 14_000, CompletionTokens: 600})

	shares := m.spendShares()
	if len(shares) != 2 {
		t.Fatalf("three sources on two models are two rows: %+v", shares)
	}
	var sum float64
	for _, s := range shares {
		sum += s.own.Cost + s.children.Cost
	}
	total := ledger.Total().Cost
	if diff := sum - total; diff > 1e-12 || diff < -1e-12 {
		t.Fatalf("the rows cost %v and the session %v", sum, total)
	}

	rail := m.inspectorSpend()
	main, aux := rail.Models[0], rail.Models[1]
	if main.Model != "gpt-4o" || strings.Join(main.Sources, " · ") != "main" {
		t.Fatalf("the session's own model is the first row, its turns called main: %+v", main)
	}
	if aux.Model != "cheap" || strings.Join(aux.Sources, " · ") != "classifier · summary" {
		t.Fatalf("the small model's row names both kinds of request it answered: %+v", aux)
	}
	if rail.Session != formatCost(total) || main.Cost != formatCost(shares[0].own.Cost) || aux.Cost != formatCost(shares[1].own.Cost) {
		t.Fatalf("each row states the cost it was billed: %+v, session %q", rail.Models, rail.Session)
	}
}

// A child that ran on a model nothing else did has a row of its own, and a
// source that spent nothing is not named.
func TestInspectorRail_AFanOutOnAnotherModelIsARowOfItsOwn(t *testing.T) {
	m, ledger := spendModel(t)
	ledger.Record(meter.Origin{Source: meter.SourceAgent}, "gpt-4o", provider.Usage{PromptTokens: 1_000, CompletionTokens: 100})
	ledger.Record(meter.Origin{Source: meter.SourceClassifier}, "gpt-4o", provider.Usage{})
	ledger.Record(meter.Origin{Source: meter.SourceSubagent, Label: "writer-1"}, "cheap", provider.Usage{PromptTokens: 5_000, CompletionTokens: 500})

	rail := m.inspectorSpend()
	if len(rail.Models) != 2 {
		t.Fatalf("two models billed: %+v", rail.Models)
	}
	if got := rail.Models[0].Sources; len(got) != 1 || got[0] != "main" {
		t.Fatalf("a classifier that spent nothing is not named: %v", got)
	}
	if child := rail.Models[1]; child.Model != "cheap" || child.Cost != "" || child.Children == "" {
		t.Fatalf("the child's model carries the child's share after its ◇ and nothing else: %+v", child)
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
	m.startNewSession()

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

// The heading's turn figure is the turn's cost, and a turn that has closed
// still cost what its close row says: the heading keeps that figure until
// the next turn opens, rather than going blank the moment the turn's books
// are closed into the history (docs/interface/surfaces.md#the-inspector-rail).
func TestSpend_TheTurnFigureSurvivesTheClose(t *testing.T) {
	m := turnModel(t)
	m = sendText(t, m, "raise the cap")
	m.accumulateUsage(&provider.Usage{PromptTokens: 1200, CompletionTokens: 80})
	running := m.inspectorSpend()
	if running == nil || running.Turn == "" {
		t.Fatalf("a running turn that has spent states it on the heading: %+v", running)
	}

	m = finishTurn(t, m)
	if m.turnOpen || m.vitals.open {
		t.Fatalf("the turn should have closed: open %v, books open %v", m.turnOpen, m.vitals.open)
	}
	closed := m.inspectorSpend()
	if closed == nil || closed.Turn != running.Turn {
		t.Fatalf("the closed turn's figure went from the heading: running %q, closed %+v", running.Turn, closed)
	}
	if c := lastClose(t, m); c.Spend != closed.Turn {
		t.Fatalf("the heading and the close row state the turn two ways: heading %q, close %q", closed.Turn, c.Spend)
	}

	// The next turn opening is what retires it: the heading is that turn's
	// own from there, and it has spent nothing yet.
	m.vitals.startTurn()
	if next := m.inspectorSpend(); next == nil || next.Turn != "" {
		t.Fatalf("the next turn's heading carried the last turn's figure: %+v", next)
	}
}
