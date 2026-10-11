package components

import (
	"strings"
	"testing"
)

// classRail is a rail with a member of every class: two live alerts, a child
// waiting and one working, and a running turn ask; a reading on target, a plan
// with a step in flight and a window past its warning read live; the backlog
// and the changeset stand; every tool source up and the bill are static.
func classRail() InspectorRail {
	return InspectorRail{
		Summary: &InspectorSummary{Text: "Wiring the round limit into the chat model; tests not run yet.",
			State: SummaryOnTarget, Round: 7},
		Turn: &InspectorTurn{Step: 3, Steps: 4, Tools: 18, Running: true, Files: 2, Added: 27, Removed: 4},
		Alerts: InspectorAlerts{
			{Label: "go test", Note: OutcomeExit(1), Turn: 6},
			{Label: "go vet", Note: OutcomeExit(1), Turn: 7},
		},
		Plan: &InspectorPlan{Done: 2, Steps: []InspectorPlanStep{
			{Number: 1, Title: "Locate the round accounting", State: PlanStepDone, Elapsed: "6.2s"},
			{Number: 2, Title: "Add the sentinel", State: PlanStepDone, Elapsed: "38s"},
			{Number: 3, Title: "Wire the handler", State: PlanStepRunning},
			{Number: 4, Title: "Offer more rounds", State: PlanStepQueued},
			{Number: 5, Title: "Document the pause", State: PlanStepQueued},
		}},
		Todo: &InspectorTodo{Open: 12, Sprint: "rail", SprintDone: 1, SprintTotal: 4,
			SprintItem: "rail-ladder", SprintStage: "review",
			Rows: []InspectorTodoRow{
				{Slug: "rail-ladder", Priority: "H", Grade: "L", State: TodoRunning},
				{Slug: "rail-wheel", Priority: "L", Grade: "M", State: TodoReady},
			}},
		Changes: &InspectorChanges{Files: []InspectorFile{
			{Path: "internal/agent/loop.go", Added: 18, Removed: 3},
			{Path: "internal/agent/round.go", Added: 9, Removed: 1},
			{Path: "docs/loop.md", Added: 12},
		}, Added: 39, Removed: 4},
		Agents: []InspectorAgent{
			{Name: "orchestrator", Detail: "round 3 · streaming…", Self: true, State: FanoutRunning},
			{Name: "reviewer-1", Detail: "waiting approval: apply patch", State: FanoutBlocked},
			{Name: "writer-2", Detail: "docs/loop.md", Tools: 4, State: FanoutRunning},
		},
		Tools: &InspectorTools{Up: 2, Sources: []InspectorToolSource{
			{Name: "built-in", State: ToolSourceUp, Note: "18 tools"},
			{Name: "docs", State: ToolSourceUp, Note: "9 tools"},
		}},
		Context: &InspectorContext{Pct: 65, Tokens: 130000, Window: 200000, WarnPct: 60,
			Tokens1: "↑41.2k", Tokens2: "↓9.8k", Burn: []float64{1, 2, 3, 4}},
		Spend: &InspectorSpend{Turn: "$0.14", Session: "$1.86", Models: []InspectorSpendModel{
			{Model: "gpt-5.2", Cost: "$1.70", Sources: []string{"main"}},
			{Model: "gpt-5-mini", Cost: "$0.16", Sources: []string{"classifier"}},
		}},
	}
}

// The written order: the class each block of classRail takes from its state.
// A change to the rule is a change to this table.
var classRailClasses = map[string]railClass{
	RailSummary: railLive,
	RailTurn:    railAsks,
	RailAlerts:  railAsks,
	RailPlan:    railLive,
	RailTodo:    railStanding,
	RailChanges: railStanding,
	RailAgents:  railAsks,
	RailTools:   railStatic,
	RailContext: railLive,
	RailSpend:   railStatic,
}

// TestInspectorRail_CollapsedHeadingStatesTheFigure: a block down to its
// heading says on it the one figure its rows would have stated, in mono so no
// colour carries any of it.
func TestInspectorRail_CollapsedHeadingStatesTheFigure(t *testing.T) {
	r := classRail()
	r.Steps = &InspectorSteps{Done: 2, Total: 4, Current: "wire the handler"}
	stepsOnly := InspectorRail{Steps: r.Steps}
	idleTurn := r
	idleTurn.Turn = &InspectorTurn{Step: 3, Steps: 4, Files: 2, Added: 27, Removed: 4}
	settled := InspectorRail{Agents: []InspectorAgent{
		{Name: "orchestrator", Self: true, State: FanoutRunning},
		{Name: "writer-1", Detail: "wrote two files", Outcome: "done", State: FanoutDone},
		{Name: "writer-2", Detail: "wrote one file", Outcome: "done", State: FanoutDone},
		{Name: "reader-3", Detail: "read 9 files", Outcome: "done", State: FanoutDone},
	}}
	for _, tc := range []struct {
		rail  InspectorRail
		block string
		want  string
	}{
		{r, RailSummary, "▸ on target · round 7"},
		{idleTurn, RailTurn, "step 3 of 4 · 2 files +27 −4"},
		{r, RailAlerts, "2 standing"},
		{r, RailPlan, "2 of 5 done · ▸ Wire the handler"},
		{stepsOnly, RailSteps, "2 of 4"},
		{r, RailTodo, "project · 12 open · on rail-ladder"},
		{r, RailChanges, "session · 3 files +39 −4"},
		{settled, RailAgents, "3 · 3 done"},
		{r, RailTools, "2 of 2 up"},
		{r, RailContext, "65% of 200k"},
		{r, RailSpend, "$0.14 · session $1.86"},
	} {
		blocks := tc.rail.blocks(InspectorMaxWidth)
		found := false
		for _, b := range blocks {
			if b.name != tc.block {
				continue
			}
			found = true
			for givable(b) {
				_, row, _ := nextToHide([]railBlock{b})
				take(&b, row)
			}
			if !b.collapsed {
				b.collapse()
			}
			heading := stripANSI(b.render(InspectorMaxWidth)[0].Text)
			if !strings.HasPrefix(strings.TrimSpace(heading), tc.block) || !strings.Contains(heading, tc.want) {
				t.Errorf("collapsed %s reads %q, want %q on it", tc.block, heading, tc.want)
			}
			if b.name == RailSpend || b.name == RailContext || b.name == RailTools || b.name == RailSteps || b.name == RailTurn {
				if h := b.height(); h != 1 {
					t.Errorf("collapsed %s is %d rows; its heading states every row it had", tc.block, h)
				}
			}
		}
		if !found {
			t.Fatalf("no %s block on the fixture", tc.block)
		}
	}
}
