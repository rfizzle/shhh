package components

import (
	"fmt"
	"strings"
	"testing"
)

// TestInspectorPlan_TheDriftRowIsPinnedNotCounted: the drift row is a warning
// about the plan, not a step of it. The rail folds every step before it, and
// the marker over the folded plan counts steps and nothing else.
func TestInspectorPlan_TheDriftRowIsPinnedNotCounted(t *testing.T) {
	r := InspectorRail{Plan: &InspectorPlan{
		Steps: []InspectorPlanStep{
			{Number: 1, Title: "Locate the round accounting", State: PlanStepDone, Elapsed: "6.2s"},
			{Number: 2, Title: "Add a RoundsExhausted sentinel", State: PlanStepDone, Elapsed: "38.1s"},
			{Number: 3, Title: "Return it from runRound", State: PlanStepRunning},
		},
		Done:  2,
		Drift: "1 off plan",
	}}
	assertFoldCounts(t, r, []string{
		"Locate the round accounting",
		"Add a RoundsExhausted sentinel",
		"Return it from runRound",
	}, 0, "⚠ 1 off plan")
}

// assertFoldCounts is what PLAN and TODO both promise about their list: under
// pressure the marker's count is the number of list rows actually off screen,
// the host's own `… N more` row joins it rather than standing beside it, and
// the rows in kept (a warning, neither a list row nor chrome) stay through
// every pressure and are never counted.
//
// more is the host's own count of list rows it left out, drawn as a row of
// its own: it is never a second marker, and once the block folds a row it is
// part of the one marker's count.
func assertFoldCounts(t *testing.T, r InspectorRail, items []string, more int, kept ...string) {
	t.Helper()
	full := len(r.Lines(InspectorMaxWidth, 0))
	gone := func(rows int) string {
		lines := r.Lines(InspectorMaxWidth, full-rows)
		for i, line := range lines {
			lines[i] = stripANSI(line)
		}
		return strings.Join(lines, "\n")
	}
	if v := gone(0); strings.Count(v, "… ") != countRows(more) ||
		(more > 0 && !strings.Contains(v, fmt.Sprintf("… %d more", more))) {
		t.Fatalf("nothing folded, no marker beyond the host's own:\n%s", v)
	}
	// The marker costs a row of its own, so the block buys back less than it
	// gives; what is pinned here is the count, not how much each row of
	// pressure bought.
	for pressure := 2; pressure < len(items); pressure++ {
		v := gone(pressure)
		off := 0
		for _, item := range items {
			if !strings.Contains(v, item) {
				off++
			}
		}
		if want := fmt.Sprintf("… %d more", off+more); !strings.Contains(v, want) {
			t.Fatalf("the marker counts the %d rows actually hidden, wanted %q:\n%s", off+more, want, v)
		}
		if n := strings.Count(v, "… "); n != 1 {
			t.Fatalf("one count under pressure %d, not %d stacked:\n%s", pressure, n, v)
		}
		for _, k := range kept {
			if !strings.Contains(v, k) {
				t.Fatalf("%q is kept, not folded, under pressure %d:\n%s", k, pressure, v)
			}
		}
	}
}

// countRows is how many count rows a block draws with nothing folded: the
// host's own, where it has one.
func countRows(more int) int {
	if more > 0 {
		return 1
	}
	return 0
}
