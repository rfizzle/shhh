package chat

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/meter"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// statsModel is a session wired with a ledger, billed by its own turns and a
// classifier on the session model, a summary on the cheap one, and two
// children — one on each model — with two turns closed and their costs on
// their close rows.
func statsModel(t *testing.T) (Model, *meter.Ledger) {
	t.Helper()
	m, ledger := spendModel(t)
	ledger.Record(meter.Origin{Source: meter.SourceAgent}, "gpt-4o", provider.Usage{PromptTokens: 12_000, CompletionTokens: 900, CachedTokens: 8_000})
	ledger.Record(meter.Origin{Source: meter.SourceClassifier}, "cheap", provider.Usage{PromptTokens: 3_000, CompletionTokens: 40})
	ledger.Record(meter.Origin{Source: meter.SourceSummary}, "cheap", provider.Usage{PromptTokens: 2_000, CompletionTokens: 120})
	ledger.Record(meter.Origin{Source: meter.SourceSubagent, Label: "researcher-1"}, "cheap", provider.Usage{PromptTokens: 400, CompletionTokens: 40})
	ledger.Record(meter.Origin{Source: meter.SourceSubagent, Label: "writer-1"}, "gpt-4o", provider.Usage{PromptTokens: 600, CompletionTokens: 60})
	ledger.Record(meter.Origin{Source: meter.SourceSubagent, Label: "writer-1"}, "cheap", provider.Usage{PromptTokens: 100, CompletionTokens: 10})
	ledger.Record(meter.Origin{Source: meter.SourceAgent}, "gpt-4o", provider.Usage{PromptTokens: 14_000, CompletionTokens: 600})
	m.turnCount = 2
	m.transcript = []entry{
		{kind: entryUser, text: "first", turn: 1},
		{kind: entryTurnClose, turn: 1, close: &components.TurnClose{Spend: "$0.1200", Elapsed: "12s"}},
		{kind: entryUser, text: "second", turn: 2},
		{kind: entryTurnClose, turn: 2, close: &components.TurnClose{Spend: "$0.3400", Elapsed: "1m 04s"}},
	}
	return m, ledger
}

// rowsOf is the screen's rows of one kind, in the order it lists them.
func rowsOf(s components.SpendScreen, kind components.SpendKind) []components.SpendRow {
	var out []components.SpendRow
	for _, r := range s.Rows {
		if r.Kind == kind {
			out = append(out, r)
		}
	}
	return out
}

// The screen is the block's own ledger: its total is the rail's session
// total, and its model rows are the rail's rows — the same model, the same
// own cost, the same kinds of request and the same children's share — so the
// screen and the block cannot disagree about one bill.
func TestSpendScreen_ReadsTheLedgerTheBlockReads(t *testing.T) {
	m, ledger := statsModel(t)
	s := m.spendScreenData()
	block := m.inspectorSpend()

	totals := rowsOf(s, components.SpendTotal)
	if len(totals) != 1 || s.Rows[0].Kind != components.SpendTotal {
		t.Fatalf("the list opens on the one session total: %+v", s.Rows)
	}
	if totals[0].Cost != block.Session || totals[0].Cost != formatCost(ledger.Total().Cost) {
		t.Fatalf("the screen's total %q is not the rail's %q", totals[0].Cost, block.Session)
	}
	models := rowsOf(s, components.SpendModel)
	if len(models) != len(block.Models) {
		t.Fatalf("the screen has %d models and the block %d", len(models), len(block.Models))
	}
	for i, row := range models {
		rail := block.Models[i]
		var words []string
		for _, p := range row.Parts {
			words = append(words, p.Word)
		}
		if row.Name != rail.Model || row.Cost != rail.Cost || row.Children != rail.Children ||
			strings.Join(words, " · ") != strings.Join(rail.Sources, " · ") {
			t.Errorf("model row %+v is not the rail's %+v", row, rail)
		}
	}
}

// Each model names the kinds of request that spent on it with what each
// cost, and those add up to the model's own figure — a second model on the
// bill is explained where the bill is read.
func TestSpendScreen_EachModelNamesItsKindsAndWhatEachCost(t *testing.T) {
	m, ledger := statsModel(t)
	models := rowsOf(m.spendScreenData(), components.SpendModel)
	cheap := models[1]
	if cheap.Name != "cheap" || len(cheap.Parts) != 2 || cheap.Parts[0].Word != "classifier" || cheap.Parts[1].Word != "summary" {
		t.Fatalf("the small model names the classifier and the summary: %+v", cheap)
	}
	if want := formatCost(ledger.SourceTotal(meter.SourceClassifier).Cost); cheap.Parts[0].Cost != want {
		t.Fatalf("the classifier's line is what it cost, %q, got %q", want, cheap.Parts[0].Cost)
	}
	main := models[0]
	if len(main.Parts) != 1 || main.Parts[0].Word != "main" || main.Parts[0].Requests != 2 || main.Parts[0].Cost != main.Cost {
		t.Fatalf("the session's turns are one kind of request, called main, over two requests: %+v", main)
	}
	if !strings.Contains(main.Tokens, "8.0k cached") {
		t.Fatalf("the model says how much of its input came from the cache: %q", main.Tokens)
	}
}

// Each child has a share of its own, by name, across every model it ran on,
// and the children's shares add up to what the block puts after the ◇ — so
// a child is counted once among the children and once on its model's row,
// never twice in either, and never left out.
func TestSpendScreen_EachChildHasItsOwnShare(t *testing.T) {
	m, ledger := statsModel(t)
	s := m.spendScreenData()
	children := rowsOf(s, components.SpendChild)
	if len(children) != 2 || children[0].Name != "researcher-1" || children[1].Name != "writer-1" {
		t.Fatalf("both children, in the order they first billed: %+v", children)
	}
	writer := children[1]
	if strings.Join(writer.Models, " · ") != "gpt-4o · cheap" {
		t.Fatalf("a child that ran on two models names both: %v", writer.Models)
	}
	want := ledger.OriginTotal(meter.Origin{Source: meter.SourceSubagent, Label: "writer-1"})
	if writer.Cost != formatCost(want.Cost) || writer.Requests != 2 {
		t.Fatalf("the writer's share is its whole cost on both models, %q over 2 requests: %+v", formatCost(want.Cost), writer)
	}
	var byChild, onModels float64
	for _, c := range m.childShares() {
		byChild += c.spend.Cost
	}
	for _, sh := range m.spendShares() {
		onModels += sh.children.Cost
	}
	all := ledger.SourceTotal(meter.SourceSubagent).Cost
	if d := byChild - all; d > 1e-12 || d < -1e-12 {
		t.Fatalf("the children's shares cost %v and the children %v", byChild, all)
	}
	if d := onModels - all; d > 1e-12 || d < -1e-12 {
		t.Fatalf("the block's ◇ shares cost %v and the children %v", onModels, all)
	}
}

// Each turn is on the bill at the cost its close row states, newest first,
// and a turn whose figures were not kept is left off rather than drawn as a
// zero.
func TestSpendScreen_EachTurnAtItsClosesCost(t *testing.T) {
	m, _ := statsModel(t)
	m.turnCount = 3
	turns := rowsOf(m.spendScreenData(), components.SpendTurn)
	if len(turns) != 2 || turns[0].Turn.N != 2 || turns[0].Cost != "$0.3400" || turns[1].Turn.N != 1 || turns[1].Cost != "$0.1200" {
		t.Fatalf("the two closed turns, newest first, at their close's cost: %+v", turns)
	}
}

// Nothing the pricing table knew means no dollar figure anywhere on the
// screen: a share is its tokens rather than a cost of nothing.
func TestSpendScreen_UnpricedSaysTokensNotDollars(t *testing.T) {
	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream)
	m.vitals.startTurn()
	m.accumulateUsage(&provider.Usage{PromptTokens: 1000, CompletionTokens: 100})
	s := m.spendScreenData()
	s.MaxLines = 20
	if view := stripANSI(s.View(130)); strings.Contains(view, "$") || !strings.Contains(view, "session total") {
		t.Fatalf("/stats should not invent a dollar figure:\n%s", view)
	}
}

// /stats opens the screen over the rail without touching the draft, and esc
// leaves with the draft as it was.
func TestSpendScreen_OpensLeavesAndKeepsTheDraft(t *testing.T) {
	m, _ := statsModel(t)
	m.input.SetValue("half a sentence")
	next, _ := m.runCommand("/stats", "/stats")
	m = next.(Model)
	if m.state != stateSpend || m.screens.spend() == nil || !m.inspectorHidden() {
		t.Fatalf("/stats should put the screen up over the rail, state %d", m.state)
	}
	view := stripANSI(strings.Join(overlays()[stateSpend].lines(m, 160, 40), "\n"))
	for _, want := range []string{"/stats", "session total", "by model", "by child", "by turn", "◇ writer-1", "turn 2"} {
		if !strings.Contains(view, want) {
			t.Errorf("the screen is missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Context occupancy") {
		t.Errorf("the occupancy is /context's, not this screen's:\n%s", view)
	}
	next, _ = m.updateStats(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = next.(Model)
	if m.state == stateSpend || m.screens.spend() != nil {
		t.Fatalf("esc should leave the screen, state %d", m.state)
	}
	if got := m.input.Value(); got != "half a sentence" {
		t.Fatalf("the screen took the draft: %q", got)
	}
}

// Enter on a turn opens the turns screen with that turn under the pointer;
// enter anywhere else does nothing.
func TestSpendScreen_EnterOpensTheTurnOnTheTurnsScreen(t *testing.T) {
	m, _ := statsModel(t)
	next, _ := m.openStats()
	m = next.(Model)
	next, _ = m.updateStats(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	if m.state != stateSpend {
		t.Fatalf("enter on the total is not an offer, state %d", m.state)
	}
	screen := m.screens.spend()
	for screen.Rows[screen.Focus].Kind != components.SpendTurn || screen.Rows[screen.Focus].Turn.N != 1 {
		before := screen.Focus
		next, _ = m.updateStats(tea.KeyPressMsg{Code: tea.KeyDown})
		m = next.(Model)
		if screen.Focus == before {
			t.Fatalf("the pointer never reached turn 1: %+v", screen.Rows)
		}
	}
	next, _ = m.updateStats(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	turns := m.screens.turns()
	if m.state != stateTurns || turns == nil || m.screens.spend() != nil {
		t.Fatalf("enter on a turn opens the turns screen in place of this one, state %d", m.state)
	}
	if got := turns.Turns[turns.Focus].N; got != 1 {
		t.Fatalf("the turns screen should open on turn 1, got turn %d", got)
	}
}

// A session that has spent nothing still opens the screen: the block is gone
// then, so the command is the only door, and nothing spent is its answer.
func TestSpendScreen_NothingSpentOpensOnASentence(t *testing.T) {
	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream)
	next, _ := m.openStats()
	m = next.(Model)
	if m.state != stateSpend {
		t.Fatalf("/stats over a session that spent nothing should still open, state %d", m.state)
	}
	view := stripANSI(strings.Join(overlays()[stateSpend].lines(m, 130, 20), "\n"))
	if !strings.Contains(view, "the session has not been billed for anything yet") {
		t.Errorf("the empty screen:\n%s", view)
	}
}

func TestHelp_ListsStats(t *testing.T) {
	m := frameModel(t, 80, 30)
	if !strings.Contains(helpText(&m), "/stats") {
		t.Fatal("help should list /stats")
	}
}
