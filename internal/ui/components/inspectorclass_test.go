package components

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
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
		Spend: &InspectorSpend{Turn: "$0.14", Session: "$1.86"},
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

// TestInspectorRail_ShedsByClassThenLength walks the rail from its full
// height down to six rows, one row at a time, at four widths, and holds the
// rule at every step: the rail fits; what asks keeps its pinned rows while
// anything less alive is still on the rail; the least alive block gives
// first, the longest of a class first; and whatever is gone is named on the
// foot line, which is the last row
// (docs/interface/surfaces.md#the-inspector-rail).
func TestInspectorRail_ShedsByClassThenLength(t *testing.T) {
	r := classRail()
	for _, b := range r.blocks(InspectorWidth) {
		want, ok := classRailClasses[b.name]
		if !ok || b.class != want {
			t.Fatalf("%s states class %d, the table says %d", b.name, b.class, want)
		}
	}
	for _, width := range []int{46, 52, 60, 72} {
		full := r.blocks(width)
		rows0 := map[string]int{}
		length := map[string]int{}
		for _, b := range full {
			rows0[b.name] = len(b.rows)
			// Length decides among blocks that have rows to give; a block of
			// pinned rows is not in that contest.
			if givable(b) {
				length[b.name] = len(b.rows)
			}
		}
		firstLoss := map[string]int{}
		for height := len(r.Lines(width, 0)); height >= 6; height-- {
			rows := r.Rows(width, height)
			// (a) and (b): every row fits the width, and the rail its height.
			if len(rows) > height {
				t.Fatalf("w%d h%d: %d rows", width, height, len(rows))
			}
			for _, row := range rows {
				if w := lipgloss.Width(row.Text); w > width {
					t.Fatalf("w%d h%d: a row is %d wide: %q", width, height, w, stripANSI(row.Text))
				}
			}
			kept, dropped := fitBlocks(r.blocks(width), height)
			on := map[string]railBlock{}
			for _, b := range kept {
				on[b.name] = b
			}
			lostPinned := false
			for _, b := range full {
				k, present := on[b.name]
				if (!present || len(k.rows) < rows0[b.name]) && firstLoss[b.name] == 0 {
					firstLoss[b.name] = height
				}
				if b.class == railAsks && present && pinnedKept(k) < pinnedKept(b) {
					lostPinned = true
				}
			}
			// A live block or an ask gives a row only once every static and
			// standing block has left the rail.
			for _, k := range kept {
				if k.class < railLive || len(k.rows) >= rows0[k.name] {
					continue
				}
				for _, o := range kept {
					if o.class <= railStanding {
						t.Fatalf("w%d h%d: %s (class %d) gave a row while %s (class %d) is still on the rail:\n%s",
							width, height, k.name, k.class, o.name, o.class, stripANSI(r.View(width, height)))
					}
				}
			}
			// (c): what asks loses a pinned row only once nothing static or
			// standing is on the rail and every live block has collapsed.
			if lostPinned {
				for _, k := range kept {
					if k.class <= railStanding || k.class == railLive && !k.collapsed {
						t.Fatalf("w%d h%d: an ask lost a pinned row while %s (class %d) stands:\n%s",
							width, height, k.name, k.class, stripANSI(r.View(width, height)))
					}
				}
			}
			// (e) and (f): whatever is gone is counted on the foot line, and the
			// foot line is the last row.
			last := stripANSI(rows[len(rows)-1].Text)
			if len(dropped) == 0 {
				if strings.Contains(last, "↓") {
					t.Fatalf("w%d h%d: a foot line with nothing dropped", width, height)
				}
				continue
			}
			if !strings.Contains(last, fmt.Sprintf("↓ %d more", len(dropped))) {
				t.Fatalf("w%d h%d: the last row should count %d dropped blocks, got %q", width, height, len(dropped), last)
			}
			for _, d := range dropped {
				if !strings.Contains(last, strings.ToLower(d.name)) && !strings.HasSuffix(strings.TrimSpace(last), "…") {
					t.Fatalf("w%d h%d: %s is gone and not on the foot line %q", width, height, d.name, last)
				}
			}
		}
		// (d): the height at which a block first loses a row is monotone in
		// class, and within a class the longer block goes first. A block with
		// no row of its own (TOOLS with every source up) has no first loss to
		// order: it is only ever dropped whole.
		for _, a := range full {
			for _, b := range full {
				if rows0[a.name] == 0 || rows0[b.name] == 0 {
					continue
				}
				ha, hb := firstLoss[a.name], firstLoss[b.name]
				if a.class < b.class && ha < hb {
					t.Fatalf("w%d: %s (class %d) first lost a row at %d, after %s (class %d) at %d",
						width, a.name, a.class, ha, b.name, b.class, hb)
				}
				if a.class == b.class && length[b.name] > 0 && length[a.name] > length[b.name] && ha < hb {
					t.Fatalf("w%d: the longer %s first lost a row at %d, after the shorter %s at %d",
						width, a.name, ha, b.name, hb)
				}
			}
		}
	}
}

// pinnedKept counts a block's pinned rows still on the rail, leaving out the
// ones its collapsed heading states.
func pinnedKept(b railBlock) int {
	n := 0
	for _, r := range b.rows {
		if r.pinned && !r.stated {
			n++
		}
	}
	return n
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

// TestInspectorRail_NeverEndsSilently: eight rows for eleven blocks' worth of
// rail, and the last row is the foot line counting everything that is not
// there.
func TestInspectorRail_NeverEndsSilently(t *testing.T) {
	r := classRail()
	r.Steps = nil
	r.Doors = map[string]bool{RailWhole: true}
	for _, width := range []int{InspectorWidth, InspectorMaxWidth} {
		rows := r.Rows(width, 8)
		if len(rows) != 8 {
			t.Fatalf("w%d: the rail should take its 8 rows, got %d", width, len(rows))
		}
		view := stripANSI(r.View(width, 8))
		missing := 0
		for _, name := range railOrder {
			if !strings.Contains(view, "  "+name) {
				missing++
			}
		}
		missing-- // STEPS is never on this rail: the plan stands in its place
		last := rows[len(rows)-1]
		if got := stripANSI(last.Text); !strings.Contains(got, fmt.Sprintf("↓ %d more", missing)) {
			t.Fatalf("w%d: the last row should count the %d blocks that are not there, got %q:\n%s", width, missing, got, view)
		}
		if last.Target != (RailTarget{Kind: RailTargetBlock, Name: RailWhole}) {
			t.Fatalf("w%d: the foot line is the door to the whole rail, got %+v", width, last.Target)
		}
	}
	// A rail with nothing dropped draws no foot line, and one with no door
	// for it keeps the line inert.
	r.Doors = nil
	if last := r.Rows(InspectorWidth, 8); last[len(last)-1].Target != (RailTarget{}) {
		t.Fatalf("a foot line with no door behind it points at nothing, got %+v", last[len(last)-1].Target)
	}
	for _, line := range r.Lines(InspectorWidth, 0) {
		if strings.HasPrefix(stripANSI(line), "  ↓ ") {
			t.Fatal("a rail with room for everything draws no foot line")
		}
	}
}

// TestInspectorRail_PromotionUncollapsesInPlace: a map of settled children is
// standing, so a short rail collapses it; one child blocking makes it an ask,
// and it takes its rows back where it stands while the static blocks below it
// pay.
func TestInspectorRail_PromotionUncollapsesInPlace(t *testing.T) {
	r := InspectorRail{
		Alerts: InspectorAlerts{{Label: "go test", Note: OutcomeExit(1), Turn: 3}},
		Agents: []InspectorAgent{
			{Name: "orchestrator", Detail: "round 3 · streaming…", Self: true, State: FanoutRunning},
			{Name: "writer-1", Detail: "wrote the loop", Outcome: "done", State: FanoutDone},
			{Name: "writer-2", Detail: "wrote the docs", Outcome: "done", State: FanoutDone},
		},
		Context: &InspectorContext{Pct: 30, Tokens: 60000, Window: 200000,
			Tokens1: "↑41.2k", Tokens2: "↓9.8k", Burn: []float64{1, 2, 3, 4}},
		Spend: &InspectorSpend{Turn: "$0.14", Session: "$1.86"},
	}
	const width = InspectorWidth
	// The height where the static blocks have collapsed and the settled map
	// has given every row it can.
	height := 0
	for h := len(r.Lines(width, 0)); h > 0; h-- {
		kept, dropped := fitBlocks(r.blocks(width), h)
		if len(dropped) > 0 {
			break
		}
		for _, b := range kept {
			if b.name == RailAgents && b.collapsed {
				height = h
			}
		}
		if height > 0 {
			break
		}
	}
	if height == 0 {
		t.Fatalf("no height collapses the settled map before a block is dropped:\n%s", stripANSI(r.View(width, 0)))
	}
	headingAt := func(rail InspectorRail) int {
		for i, row := range rail.Lines(width, height) {
			if strings.HasPrefix(stripANSI(row), "  AGENTS") {
				return i
			}
		}
		t.Fatalf("no AGENTS heading at height %d:\n%s", height, stripANSI(rail.View(width, height)))
		return -1
	}
	before := headingAt(r)
	collapsedBefore := stripANSI(r.View(width, height))

	asks := r
	asks.Agents = append([]InspectorAgent(nil), r.Agents...)
	asks.Agents[2].State, asks.Agents[2].Outcome, asks.Agents[2].Detail = FanoutBlocked, "", "waiting approval: apply patch"
	view := stripANSI(asks.View(width, height))
	if after := headingAt(asks); after != before {
		t.Fatalf("AGENTS moved from row %d to %d when it rose:\n%s\n---\n%s", before, after, collapsedBefore, view)
	}
	if !strings.Contains(view, "writer-2") || !strings.Contains(view, "waiting approval") {
		t.Fatalf("the blocked child's rows are back under the map:\n%s", view)
	}
	static := func(rail InspectorRail) int {
		kept, _ := fitBlocks(rail.blocks(width), height)
		n := 0
		for _, b := range kept {
			if b.class == railStatic {
				n += b.height() + 1
			}
		}
		return n
	}
	if static(asks) >= static(r) {
		t.Fatalf("a static block gives its rows up to pay for the ones the map took back:\n%s\n---\n%s", collapsedBefore, view)
	}
	// Nothing reorders: every heading that is on both rails is in the same
	// order on each.
	order := func(v string) []string {
		var names []string
		for _, line := range strings.Split(v, "\n") {
			for _, name := range railOrder {
				if strings.HasPrefix(line, "  "+name) {
					names = append(names, name)
				}
			}
		}
		return names
	}
	common := func(names []string, in []string) string {
		var out []string
		for _, n := range names {
			for _, m := range in {
				if n == m {
					out = append(out, n)
				}
			}
		}
		return strings.Join(out, ",")
	}
	if a, b := common(order(collapsedBefore), order(view)), common(order(view), order(collapsedBefore)); a != b {
		t.Fatalf("the blocks reordered: %s became %s", a, b)
	}
}
