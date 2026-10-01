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
	m.appendEntry(entry{kind: entryUser, text: planApprovedMessage})
	m.beginPlanRun(plan.Parse(planFixture))
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

	t.Run("a click on the header folds the card and gives it back", func(t *testing.T) {
		m := plannedRun(t, selectModel(t, &clip{}))
		m.viewport.SetLines(m.renderHistoryLines())
		m.viewport.GotoTop()
		m.atBottom = false
		at := -1
		for i, e := range m.transcript {
			if e.kind == entryPlan {
				at = i
			}
		}
		x, y := rowCell(t, m, "planned 4 steps")
		m = click(t, m, x, y)
		if got := (*m.entries())[at].stepFold; got != foldClosed {
			t.Fatalf("the header click left the fold at %v, want it folded", got)
		}
		if strings.Contains(strings.Join(contentLines(m), "\n"), "Add a RoundsExhausted sentinel") {
			t.Fatal("a folded plan card still draws its rows")
		}
		x, y = rowCell(t, m, "planned 4 steps")
		m = click(t, m, x, y)
		if got := (*m.entries())[at].stepFold; got == foldClosed {
			t.Fatal("a second click on the header should give the card back")
		}
		x, y = rowCell(t, m, "Add a RoundsExhausted sentinel")
		m = click(t, m, x, y)
		if got := (*m.entries())[at].stepFold; got == foldClosed {
			t.Fatal("a click on a step's row should do nothing")
		}
	})
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
