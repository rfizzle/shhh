package components

import (
	"fmt"
	"strings"
	"testing"
)

// TestInspectorPlan_TheHintIsShedNotFolded: the row naming the command that
// prints the whole list is chrome, not a step, so a rail short of height
// takes it first and takes it outright. Folding it would spend the row the
// fold just saved on the marker, leaving the rail no shorter, and the marker
// would then count a step hidden that is still on screen.
func TestInspectorPlan_TheHintIsShedNotFolded(t *testing.T) {
	r := InspectorRail{Plan: &InspectorPlan{
		Steps: []InspectorPlanStep{
			{Number: 1, Title: "Locate the round accounting", State: PlanStepDone, Elapsed: "6.2s"},
			{Number: 2, Title: "Add a RoundsExhausted sentinel", State: PlanStepDone, Elapsed: "38.1s"},
			{Number: 3, Title: "Return it from runRound", State: PlanStepRunning},
		},
		Done: 2,
		Hint: "/plan for the whole list",
	}}
	assertHintIsShed(t, r, "/plan for the whole list", []string{
		"Locate the round accounting",
		"Add a RoundsExhausted sentinel",
		"Return it from runRound",
	}, 0)
}

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
		Hint:  "/plan for the whole list",
	}}
	assertHintIsShed(t, r, "/plan for the whole list", []string{
		"Locate the round accounting",
		"Add a RoundsExhausted sentinel",
		"Return it from runRound",
	}, 0, "⚠ 1 off plan")
}

// assertHintIsShed is what PLAN and TODO both promise about the hint row
// under their list: one row of pressure costs the hint and nothing else, no
// marker stands over it, and under every deeper pressure the marker's count
// is the number of list rows actually off screen — the hint is never one of
// them, because it never went behind the marker.
//
// The first check compares the whole rail rather than looking for the hint's
// absence, because the failure it is here for takes some other row while
// leaving the hint: every containment check would still pass.
//
// more is the host's own count of list rows it left out, drawn as a row of
// its own: it is never a second marker, and once the block folds a row it is
// part of the one marker's count. kept are rows that are neither list rows
// nor chrome — a warning — which stay through every pressure and are never
// counted.
func assertHintIsShed(t *testing.T, r InspectorRail, hint string, items []string, more int, kept ...string) {
	t.Helper()
	full := len(r.Lines(InspectorMaxWidth, 0))
	gone := func(rows int) string {
		lines := r.Lines(InspectorMaxWidth, full-rows)
		for i, line := range lines {
			lines[i] = stripANSI(line)
		}
		return strings.Join(lines, "\n")
	}
	whole := strings.Split(gone(0), "\n")
	if !strings.Contains(whole[len(whole)-1], hint) {
		t.Fatalf("the hint is the block's last row:\n%s", strings.Join(whole, "\n"))
	}
	if got, want := gone(1), strings.Join(whole[:len(whole)-1], "\n"); got != want {
		t.Fatalf("one row of pressure is the hint and nothing else:\ngot\n%s\nwant\n%s", got, want)
	}
	// And it goes rather than folding: a hint is not an item, so nothing is
	// behind a marker yet and the rail is a row shorter for having lost it.
	// The only count left is the host's own, where it drew one.
	if v, want := gone(1), countRows(more); strings.Count(v, "… ") != want ||
		(more > 0 && !strings.Contains(v, fmt.Sprintf("… %d more", more))) {
		t.Fatalf("the hint is shed, not folded behind a count:\n%s", v)
	}
	// Past that the block folds list rows, and the marker states how many —
	// which stays a count of rows that left the screen. The marker costs a
	// row of its own, so the block buys back less than it gives; what is
	// pinned here is the count, not how much each row of pressure bought.
	for pressure := 2; pressure <= len(items); pressure++ {
		v := gone(pressure)
		if strings.Contains(v, hint) {
			t.Fatalf("the shed hint does not come back under pressure %d:\n%s", pressure, v)
		}
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
