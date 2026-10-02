package chat

// The approved plan as a card where it was approved, and the flat line each
// step the run finishes leaves under its own card.

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/plan"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// plannedRun is a session that approved planFixture, with its card filed
// under the approving message, that has finished the plan's first step and
// is on its third.
func plannedRun(t *testing.T, m Model) Model {
	t.Helper()
	return plannedRunOf(t, m, planFixture)
}

// longPlanFixture is planFixture with three more steps, past the card's
// ceiling, so the closed card counts the last of them rather than drawing
// them.
const longPlanFixture = planFixture + `5. Survey the callers
   files: internal/agent/run.go
   action: read
6. Run the agent tests
   action: run
7. Read the result back
   files: internal/agent/loop.go
   action: read
`

// plannedRunOf is plannedRun with the plan the session approved.
func plannedRunOf(t *testing.T, m Model, doc string) Model {
	t.Helper()
	m.appendEntry(entry{kind: entryUser, text: planApprovedMessage})
	m.beginPlanRun(plan.Parse(doc))
	if m.planRun == nil {
		t.Fatal("the fixture plan should parse into steps")
	}
	announce(t, &m, "Locate the round accounting", time.Second, false)
	announce(t, &m, "Return it from runRound", 2*time.Second, false)
	m.state = stateStreaming
	m.invalidateRenderCache()
	return m
}

// indexOfLine is the index of the first line holding want, or -1.
func indexOfLine(lines []string, want string) int {
	for i, l := range lines {
		if strings.Contains(l, want) {
			return i
		}
	}
	return -1
}

func TestCard_APlanIsACardWithAStepPerRow(t *testing.T) {
	m := plannedRun(t, frameModel(t, 110, 40))
	lines := strings.Split(ansi.Strip(m.renderHistory()), "\n")
	head := indexOfLine(lines, "▸ planned 4 steps · 3 files")
	if head < 0 {
		t.Fatalf("no plan card under the approving message:\n%s", strings.Join(lines, "\n"))
	}
	if !strings.Contains(lines[head], "3 writes") {
		t.Errorf("the header should count the plan's writes: %q", lines[head])
	}
	if got := strings.TrimSpace(lines[head+1]); got != "make the round limit recoverable" {
		t.Errorf("the body = %q, want the plan's own sentence", got)
	}
	for _, row := range []struct{ step, mark string }{
		{"✓ 1  Locate the round accounting", "read only"},
		{"· 2  Add a RoundsExhausted sentinel", "✎ errors.go"},
		{"3  Return it from runRound", "✎ loop.go"},
		{"· 4  Offer more rounds in the chat model", "✎ model.go"},
	} {
		at := indexOfLine(lines, row.step)
		if at <= head {
			t.Errorf("no row %q under the card's header:\n%s", row.step, strings.Join(lines, "\n"))
			continue
		}
		if !strings.Contains(lines[at], row.mark) {
			t.Errorf("the row %q should say %q", lines[at], row.mark)
		}
	}
	// The card stands before the work it lists, and the steps the run has
	// not reached are its rows rather than outline rows after the work.
	if first := indexOfLine(lines, "⚙ read a.go"); first < head {
		t.Errorf("the plan's card should stand before the first step's card")
	}
	if strings.Contains(strings.Join(lines, "\n"), components.OutcomeQueued) {
		t.Errorf("a step not reached should be a row of the card, not an outline row:\n%s", strings.Join(lines, "\n"))
	}

}

// planIndex is the entry the plan's card is kept on.
func planIndex(t *testing.T, m Model) int {
	t.Helper()
	for i, e := range m.transcript {
		if e.kind == entryPlan {
			return i
		}
	}
	t.Fatal("no plan card was filed")
	return -1
}

// TestCard_APlanHasTwoDepths: a plan longer than the card's ceiling counts
// the steps past it, and enter or a click on the header opens the card onto
// every step and closes it back to the counted card; nothing draws it as its
// header alone or puts a fold mark in its pointer column. A plan the ceiling
// already draws whole has nothing more to show, so it is no stop and its
// header no target, and a click on a step's row does nothing, as enter has
// no act on one.
func TestCard_APlanHasTwoDepths(t *testing.T) {
	const header, last, counted = "planned 7 steps", "7  Read the result back", "… 3 more"
	closedCard := func(t *testing.T, m Model) {
		t.Helper()
		lines := contentLines(m)
		out := strings.Join(lines, "\n")
		if !strings.Contains(out, counted) || strings.Contains(out, last) {
			t.Fatalf("the closed card should count the steps past its ceiling:\n%s", out)
		}
		if h := lines[indexOfLine(lines, header)]; !strings.HasPrefix(h, "  ▸ planned") {
			t.Errorf("the header %q should keep a blank pointer column and the plan's mark", h)
		}
		if indexOfLine(lines, "Locate the round accounting") < 0 {
			t.Errorf("the closed card is the card, not its header alone:\n%s", out)
		}
	}
	openCard := func(t *testing.T, m Model) {
		t.Helper()
		out := strings.Join(contentLines(m), "\n")
		if !strings.Contains(out, last) || strings.Contains(out, counted) {
			t.Fatalf("the open card should draw every step:\n%s", out)
		}
	}
	for _, tc := range []struct {
		name  string
		press func(t *testing.T, m Model) Model
	}{
		{"enter", func(t *testing.T, m Model) Model {
			m.focusIdx = planIndex(t, m)
			next, _ := m.openCursorRow(stateFocus)
			return next.(Model)
		}},
		{"a click on the header", func(t *testing.T, m Model) Model {
			x, y := rowCell(t, m, header)
			return click(t, m, x, y)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := plannedRunOf(t, selectModel(t, &clip{}), longPlanFixture)
			m.viewport.SetLines(m.renderHistoryLines())
			m.viewport.GotoTop()
			m.atBottom = false
			if !m.selectableRow(m.transcript[planIndex(t, m)]) {
				t.Fatal("a plan past the ceiling should be a stop for the cursor")
			}
			closedCard(t, m)
			m = tc.press(t, m)
			openCard(t, m)
			m = tc.press(t, m)
			closedCard(t, m)
		})
	}

	t.Run("a plan the ceiling draws whole and a step's row do nothing", func(t *testing.T) {
		m := plannedRun(t, selectModel(t, &clip{}))
		m.viewport.SetLines(m.renderHistoryLines())
		m.viewport.GotoTop()
		m.atBottom = false
		at := planIndex(t, m)
		if m.selectableRow(m.transcript[at]) {
			t.Error("a plan with nothing past the ceiling should be no stop")
		}
		before := strings.Join(contentLines(m), "\n")
		for _, row := range []string{"planned 4 steps", "Add a RoundsExhausted sentinel"} {
			x, y := rowCell(t, m, row)
			m = click(t, m, x, y)
			if e := (*m.entries())[at]; e.expanded || e.stepFold != foldAuto {
				t.Fatalf("a click on %q changed the card", row)
			}
		}
		if after := strings.Join(contentLines(m), "\n"); after != before {
			t.Errorf("the clicks moved the card:\n%s", after)
		}
	})
}

// TestDensity_LowOpensAFanOutAndAPlan: at low a fan-out's card and a plan's
// are each their header alone on one band row, inset as a normal header is,
// with a blank either side; enter or a click on that row opens the card whole,
// and the same again gives the row back.
func TestDensity_LowOpensAFanOutAndAPlan(t *testing.T) {
	for _, tc := range []struct {
		name, header, inside string
		model                func(t *testing.T) (Model, int)
	}{
		{"a plan", "planned 4 steps", "Add a RoundsExhausted sentinel", func(t *testing.T) (Model, int) {
			m := plannedRun(t, selectModel(t, &clip{}))
			return m, planIndex(t, m)
		}},
		{"a fan-out still running", "spawned 2 agents", "◇ researcher-1", func(t *testing.T) (Model, int) {
			m := spawnedFanout(t, blockingEnv(), false)
			return m, fanoutIndex(t, m)
		}},
	} {
		for _, press := range []string{"enter", "click"} {
			t.Run(tc.name+" by "+press, func(t *testing.T) {
				m, at := tc.model(t)
				m.verbosity = verbosityLow
				m.invalidateRenderCache()
				m.viewport.SetLines(m.renderHistoryLines())
				m.viewport.GotoTop()
				m.atBottom = false
				row := func(m Model) []string {
					lines := contentLines(m)
					h := indexOfLine(lines, tc.header)
					if h < 0 || !strings.HasPrefix(lines[h], "  ") || strings.Contains(lines[h], "▸ ▸") {
						t.Fatalf("no inset header row for %q:\n%s", tc.header, strings.Join(lines, "\n"))
					}
					// The first line of the pane has nothing above it to be blank.
					above := ""
					if h > 0 {
						above = lines[h-1]
					}
					return append([]string{above}, lines[h:min(h+2, len(lines))]...)
				}
				low := row(m)
				if strings.TrimSpace(low[0]) != "" || (len(low) > 2 && strings.TrimSpace(low[2]) != "") {
					t.Fatalf("at low the card is one row with a blank either side:\n%s", strings.Join(low, "\n"))
				}
				if lineAt := indexOfLine(contentLines(m), tc.inside); lineAt >= 0 {
					t.Fatalf("at low the closed card should not draw %q", tc.inside)
				}
				if !m.selectableRow(m.transcript[at]) {
					t.Fatal("at low the card opens, so it is a stop")
				}
				toggle := func(m Model) Model {
					if press == "click" {
						x, y := rowCell(t, m, tc.header)
						return click(t, m, x, y)
					}
					m.focusIdx = at
					next, _ := m.openCursorRow(stateFocus)
					return next.(Model)
				}
				m = toggle(m)
				if indexOfLine(contentLines(m), tc.inside) < 0 {
					t.Fatalf("%s at low should open the card whole:\n%s", press, strings.Join(contentLines(m), "\n"))
				}
				m = toggle(m)
				if indexOfLine(contentLines(m), tc.inside) >= 0 {
					t.Fatalf("%s again should give the one row back:\n%s", press, strings.Join(contentLines(m), "\n"))
				}
			})
		}
	}
}

func TestPlan_ATickedStepIsAFlatLine(t *testing.T) {
	cases := []struct {
		name   string
		settle bool
		failed bool
		want   []string
		absent []string
	}{
		{name: "the first step finished, the third running",
			want:   []string{"  ✓ plan · 1 of 4 · Locate the round accounting · 3 writes left"},
			absent: []string{"plan · 3 of 4"}},
		{name: "the turn over, the third finished too", settle: true,
			want: []string{"  ✓ plan · 1 of 4 · Locate the round accounting · 3 writes left",
				"  ✓ plan · 3 of 4 · Return it from runRound · 2 writes left"}},
		{name: "a step that broke ticks in its own mark", settle: true, failed: true,
			want: []string{"  ✗ plan · 4 of 4 · Offer more rounds in the chat model · 1 write left"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := plannedRun(t, frameModel(t, 110, 40))
			if tc.failed {
				announce(t, &m, "Offer more rounds in the chat model", time.Second, true)
			}
			if tc.settle {
				m.state = stateInput
			}
			m.invalidateRenderCache()
			out := ansi.Strip(m.renderHistory())
			for _, w := range tc.want {
				if !strings.Contains(out, w+"\n") {
					t.Errorf("no flat line %q:\n%s", w, out)
				}
			}
			for _, a := range tc.absent {
				if strings.Contains(out, a) {
					t.Errorf("a step still running ticked %q:\n%s", a, out)
				}
			}
			// The line stands under its step's card, before the next card.
			lines := strings.Split(out, "\n")
			body := func(s string) int {
				for i, l := range lines {
					if strings.TrimSpace(l) == s {
						return i
					}
				}
				return -1
			}
			tick := indexOfLine(lines, "plan · 1 of 4")
			card, next := body("Locate the round accounting"), body("Return it from runRound")
			if card >= tick || tick >= next {
				t.Errorf("the line should stand between its step's card (%d) and the next (%d), at %d", card, next, tick)
			}
		})
	}
}

// A plan's card keeps its own run's states after a later plan is approved:
// the later plan numbers its steps from one too, and read as the earlier
// plan's they would undo its ticks.
func TestCard_APlanKeepsItsRowsAfterALaterPlan(t *testing.T) {
	m := plannedRun(t, frameModel(t, 110, 40))
	first := m.planRun
	m.appendEntry(entry{kind: entryUser, text: planApprovedMessage})
	m.beginPlanRun(plan.Parse(planFixture))
	if m.planRun == first {
		t.Fatal("the second approval should start a run of its own")
	}
	announce(t, &m, "Locate the round accounting", time.Second, true)
	got := m.planChecklistOf(first)
	if got[0].State != components.PlanStepDone {
		t.Fatalf("the first plan's step 1 = %v after a later plan, want it done", got[0].State)
	}
	if later := m.planChecklistOf(m.planRun); later[0].State == components.PlanStepDone {
		t.Fatalf("the later plan's step 1 = %v, want its own state", later[0].State)
	}
}
