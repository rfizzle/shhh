package chat

import (
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/provider"
)

func vitalsModel(t *testing.T) Model {
	t.Helper()
	table := pricing.NewTable(map[string]pricing.ModelPricing{
		"gpt-4o": {InputCostPerToken: 0.00001, OutputCostPerToken: 0.00002, MaxInputTokens: 200000},
	})
	return New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream).
		WithPricing(table, "gpt-4o")
}

func TestVitals_RingEvictsOldestKeepingTotals(t *testing.T) {
	var v vitals
	for i := 1; i <= vitalsHistory+5; i++ {
		v.startTurn()
		v.record("gpt-4o", provider.Usage{PromptTokens: i, CompletionTokens: 1, CachedTokens: 1}, 0.01, true)
		v.endTurn(time.Duration(i) * time.Second)
	}
	if got := len(v.turns); got != vitalsHistory {
		t.Fatalf("the ring is bounded to %d turns, got %d", vitalsHistory, got)
	}
	if v.evicted != 5 {
		t.Fatalf("eviction should be visible, got %d evicted", v.evicted)
	}
	// Eviction costs history, never the total.
	wantIn := int64((vitalsHistory + 5) * (vitalsHistory + 6) / 2)
	if v.totalIn != wantIn {
		t.Fatalf("session total should survive eviction: want ↑%d, got ↑%d", wantIn, v.totalIn)
	}
	if v.totalOut != int64(vitalsHistory+5) || v.totalCached != int64(vitalsHistory+5) {
		t.Fatalf("out/cached totals should survive eviction: ↓%d cached %d", v.totalOut, v.totalCached)
	}
	// The oldest kept turn is turn 6, the newest the last one recorded.
	if v.turns[0].In != 6 {
		t.Fatalf("oldest turns are evicted first, kept ↑%d", v.turns[0].In)
	}
	last, ok := v.lastTurn()
	if !ok || last.In != int64(vitalsHistory+5) || last.Elapsed != time.Duration(vitalsHistory+5)*time.Second {
		t.Fatalf("last turn should be the newest with its wall time, got %+v", last)
	}
}

func TestVitals_TurnAccumulatesEveryRound(t *testing.T) {
	m := vitalsModel(t)
	m.vitals.startTurn()
	m.accumulateUsage(&provider.Usage{PromptTokens: 1000, CompletionTokens: 100, CachedTokens: 400})
	m.accumulateUsage(&provider.Usage{PromptTokens: 2000, CompletionTokens: 200, CachedTokens: 0})
	m.vitals.endTurn(3 * time.Second)

	turn, ok := m.vitals.lastTurn()
	if !ok {
		t.Fatal("the closed turn should be in the ring")
	}
	if turn.In != 3000 || turn.Out != 300 || turn.Cached != 400 {
		t.Fatalf("turn should sum its rounds, got %+v", turn)
	}
	if !turn.Priced || turn.Cost <= 0 {
		t.Fatalf("a priced model should give the turn a cost, got %+v", turn)
	}
	// The model's own totals are read back from the same accounting.
	if m.TotalTokensIn != 3000 || m.TotalTokensOut != 300 {
		t.Fatalf("session totals should mirror the vitals: ↑%d ↓%d", m.TotalTokensIn, m.TotalTokensOut)
	}
	if m.turnTokensIn != 3000 || m.turnTokensOut != 300 {
		t.Fatalf("turn totals should mirror the vitals: ↑%d ↓%d", m.turnTokensIn, m.turnTokensOut)
	}
	// The context estimate is the latest round's prompt plus completion.
	if m.contextTokens != 2200 {
		t.Fatalf("context should follow the last round, got %d", m.contextTokens)
	}
}

func TestVitals_UnpricedModelReportsNoCost(t *testing.T) {
	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream)
	m.vitals.startTurn()
	m.accumulateUsage(&provider.Usage{PromptTokens: 1000, CompletionTokens: 100})
	if m.vitals.priced || m.vitals.totalCost != 0 {
		t.Fatalf("without a pricing table there is no cost to report, got %v", m.vitals.totalCost)
	}
	if out := m.statsReport(); strings.Contains(out, "$") {
		t.Fatalf("/stats should not invent a dollar figure:\n%s", out)
	}
}

func TestContextAccounting_CategoriesSumToReportedContext(t *testing.T) {
	m := New([]provider.Message{
		{Role: provider.RoleSystem, Content: strings.Repeat("s", 4000)},
		{Role: provider.RoleUser, Content: strings.Repeat("u", 2000)},
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "c1", Name: "read_file", Arguments: `{"path":"x"}`}}},
		{Role: provider.RoleTool, Content: strings.Repeat("t", 8000), ToolCallID: "c1"},
	}, mockStream).
		WithToolTokenEstimate(500).
		WithProjectContextTokens(300)

	est := m.contextAccounting()
	if est.Reported {
		t.Fatal("with no usage reported the accounting is an estimate")
	}
	if est.Project != 300 {
		t.Fatalf("the project context should be split out of the system prompt, got %d", est.Project)
	}
	if est.System != 1000-300 {
		t.Fatalf("the system prompt should shrink by the project context, got %d", est.System)
	}
	if est.total() != m.estimatedContextTokens() {
		t.Fatalf("the estimate and the total are one number: %d vs %d", est.total(), m.estimatedContextTokens())
	}

	// A provider report rescales the same shares onto the reported total.
	m.contextTokens = 12345
	b := m.contextAccounting()
	if !b.Reported {
		t.Fatal("a reported context should be marked as reported")
	}
	if b.total() != 12345 {
		t.Fatalf("categories must sum to the reported context, got %d", b.total())
	}
	for _, c := range []struct {
		name  string
		value int64
	}{{"system", b.System}, {"project", b.Project}, {"tools", b.Tools}, {"messages", b.Messages}, {"tool results", b.ToolResults}} {
		if c.value <= 0 {
			t.Fatalf("%s should keep a share of the reported context, got %d", c.name, c.value)
		}
	}
	// The shares stay proportional: tool output is the biggest category here.
	if b.ToolResults <= b.Messages {
		t.Fatalf("scaling should preserve the shares: results %d, messages %d", b.ToolResults, b.Messages)
	}
}

func TestContextAccounting_FallsBackToEstimateAndSaysSo(t *testing.T) {
	m := vitalsModel(t)
	m.agent.Append(provider.Message{Role: provider.RoleUser, Content: strings.Repeat("u", 4000)})

	b := m.contextAccounting()
	if b.Reported || b.total() <= 0 {
		t.Fatalf("with no usage the accounting estimates from the messages, got %+v", b)
	}
	if out := m.statsReport(); !strings.Contains(out, "estimated") {
		t.Fatalf("/stats should say the occupancy is estimated:\n%s", out)
	}
	rail := m.inspectorContext()
	if rail == nil || !rail.Estimated {
		t.Fatalf("the rail's CONTEXT block should be marked estimated, got %+v", rail)
	}

	// Once the provider reports, both surfaces stop hedging.
	m.accumulateUsage(&provider.Usage{PromptTokens: 5000, CompletionTokens: 200})
	if out := m.statsReport(); !strings.Contains(out, "provider-reported") {
		t.Fatalf("/stats should name the provider report:\n%s", out)
	}
	if rail := m.inspectorContext(); rail == nil || rail.Estimated {
		t.Fatalf("a reported context is not an estimate, got %+v", rail)
	}
}

func TestStatsReport_ReadsTheSameNumbersAsTheRail(t *testing.T) {
	m := vitalsModel(t)
	m.vitals.startTurn()
	m.accumulateUsage(&provider.Usage{PromptTokens: 41200, CompletionTokens: 9800, CachedTokens: 2000})
	m.vitals.endTurn(64 * time.Second)

	rail := m.inspectorContext()
	if rail == nil {
		t.Fatal("the CONTEXT block should be present once usage has arrived")
	}
	b := m.contextAccounting()
	if rail.Tokens != b.total() {
		t.Fatalf("the rail and the accounting disagree: %d vs %d", rail.Tokens, b.total())
	}
	out := m.statsReport()
	for _, want := range []string{
		"Context occupancy", "system prompt", "tool definitions", "messages", "tool results",
		"2.0k cached", "last turn", "1m 04s",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("/stats missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "project context") {
		t.Fatalf("a session with no project context should not print the row:\n%s", out)
	}
	if !strings.Contains(out, formatTokenCount(b.total())) {
		t.Fatalf("/stats should quote the accounting's total:\n%s", out)
	}
}

func TestVitals_TurnLifecycleThroughTheModel(t *testing.T) {
	m := vitalsModel(t)
	m = sendText(t, m, "do the thing")
	if !m.vitals.open {
		t.Fatal("sending a message opens the turn's accounting")
	}
	m.accumulateUsage(&provider.Usage{PromptTokens: 100, CompletionTokens: 10})
	m.setTurnState(stateInput)
	if m.vitals.open {
		t.Fatal("a turn going idle closes its accounting")
	}
	if turn, ok := m.vitals.lastTurn(); !ok || turn.In != 100 || turn.Elapsed <= 0 {
		t.Fatalf("the closed turn should carry its usage and wall time, got %+v", turn)
	}
}

// The two rails are one account: what the session has spent is what the
// earlier turns cost plus what the running one is costing, composed rather
// than counted twice.
func TestVitals_SessionTotalIsTheTurnsAccountPlusTheEarlierTurns(t *testing.T) {
	m := statusModel(t)
	m.vitals.startTurn()
	m.accumulateUsage(&provider.Usage{PromptTokens: 1000, CompletionTokens: 400})
	m.vitals.endTurn(time.Second)
	earlierIn, earlierOut := m.TotalTokensIn, m.TotalTokensOut

	m.vitals.startTurn()
	m.accumulateUsage(&provider.Usage{PromptTokens: 2000, CompletionTokens: 700})
	m.streaming = strings.Repeat("token ", 400)
	settleCounts(&m)

	turnIn, turnOut := m.easedTurnTokens()
	sessionIn, sessionOut := m.liveSessionTokens()
	if sessionIn != earlierIn+turnIn || sessionOut != earlierOut+turnOut {
		t.Fatalf("the rail's total ↑%d ↓%d is not the turn's ↑%d ↓%d plus the earlier ↑%d ↓%d",
			sessionIn, sessionOut, turnIn, turnOut, earlierIn, earlierOut)
	}
	// The round's own report is in the turn's figure already: adding the
	// estimate on top of it would count this round's prompt twice.
	if sessionIn != earlierIn+2000 {
		t.Fatalf("the reported prompt should replace the estimate, got ↑%d", sessionIn)
	}
}

// Closing a turn moves what it spent out of the estimate and into the totals
// in the same update, so the session figure does not jump at the boundary.
func TestVitals_SessionTotalHoldsAcrossTheTurnBoundary(t *testing.T) {
	m := statusModel(t)
	m.vitals.startTurn()
	m.accumulateUsage(&provider.Usage{PromptTokens: 2000, CompletionTokens: 700})
	settleCounts(&m)
	beforeIn, beforeOut := m.liveSessionTokens()

	m.state = stateInput
	m.vitals.endTurn(time.Second)
	settleCounts(&m)
	afterIn, afterOut := m.liveSessionTokens()
	if afterIn != beforeIn || afterOut != beforeOut {
		t.Fatalf("the session figure moved at the boundary: ↑%d ↓%d -> ↑%d ↓%d",
			beforeIn, beforeOut, afterIn, afterOut)
	}
	if m.countsEasing() {
		t.Fatal("a turn closing is a reset, not a climb, and must not keep the tick alive")
	}
}

// The resolution follows the moment: every digit while a turn is spending
// them, and the shape a finished total is read in once nothing is moving.
func TestVitals_RailCountsChangeResolutionWithTheTurn(t *testing.T) {
	m := statusModel(t)
	// A session whose only spend is this turn's, so the rail's figure is one
	// the assertion can name.
	m.vitals.reset()
	m.vitals.startTurn()
	m.accumulateUsage(&provider.Usage{PromptTokens: 41200, CompletionTokens: 9800})
	settleCounts(&m)
	if bar := stripANSI(m.renderStatusBar(160)); !strings.Contains(bar, "↑41,200 ↓9,800") {
		t.Fatalf("a working turn should print every digit, got %q", bar)
	}

	m.state = stateInput
	m.vitals.endTurn(time.Second)
	settleCounts(&m)
	if bar := stripANSI(m.renderStatusBar(160)); !strings.Contains(bar, "↑41.2k ↓9.8k") {
		t.Fatalf("a rested total keeps its own shape, got %q", bar)
	}
}

// A turn granted more rounds after a round-limit pause is put back on the
// books with nothing spent: what it cost moves out of the closed totals and
// into the open turn again. Neither rail has anything to move — a session
// total that falls would be a lie about what has been spent, and a turn's
// account that climbs back to a figure it was already showing is movement
// nothing measured.
func TestVitals_NeitherRailMovesWhenATurnGoesBackOnTheBooks(t *testing.T) {
	m := statusModel(t)
	m.vitals.startTurn()
	m.accumulateUsage(&provider.Usage{PromptTokens: 2000, CompletionTokens: 700})
	settleCounts(&m)
	turnIn, turnOut := m.easedTurnTokens()

	// The ceiling: the turn is closed with everything it spent.
	m.state = stateInput
	m.vitals.endTurn(time.Second)
	settleCounts(&m)
	sessionIn, sessionOut := m.liveSessionTokens()

	// The grant: the closed turn becomes the open one again.
	m.vitals.reopenTurn()
	m.state = stateStreaming
	m.spinFrame++
	m.easeCounts()
	if in, out := m.liveSessionTokens(); in != sessionIn || out != sessionOut {
		t.Fatalf("the session's total moved on a turn that spent nothing: ↑%d ↓%d -> ↑%d ↓%d",
			sessionIn, sessionOut, in, out)
	}
	if in, out := m.easedTurnTokens(); in != turnIn || out != turnOut {
		t.Fatalf("the turn's account climbed again on a grant that spent nothing: ↑%d ↓%d -> ↑%d ↓%d",
			turnIn, turnOut, in, out)
	}
	if m.countsEasing() {
		t.Fatal("a grant that spent nothing has nothing to animate")
	}
}
