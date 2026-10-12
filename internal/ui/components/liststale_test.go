package components

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/ui/golden"
)

// staleRail is the rail with a STEPS block over a list whose age is given,
// under THIS TURN and above CHANGES, as the STEPS golden draws it.
func staleRail(steps *InspectorSteps) InspectorRail {
	return InspectorRail{
		Turn:  &InspectorTurn{Tools: 7, Running: true, Files: 1, Added: 12, Removed: 2},
		Steps: steps,
		Changes: &InspectorChanges{
			Files: []InspectorFile{{Path: "internal/agent/loop.go", Added: 12, Removed: 2, ThisTurn: true}},
			Added: 12, Removed: 2,
		},
	}
}

// Under the threshold the block is exactly what it was before it knew an age;
// at it, the age is said after the count in the block's own words.
func TestInspectorSteps_TheAgeShowsOnlyFromTheThreshold(t *testing.T) {
	w := InspectorWidthFor(130)
	for _, tc := range []struct {
		age  int
		want string
	}{
		{0, ""},
		{StaleListRounds - 1, ""},
		{StaleListRounds, "unchanged 20 rounds"},
		{25, "unchanged 25 rounds"},
	} {
		fresh := staleRail(&InspectorSteps{Done: 2, Total: 7, Current: "Wire the flag"}).View(w, 14)
		aged := staleRail(&InspectorSteps{Done: 2, Total: 7, Current: "Wire the flag", Age: tc.age}).View(w, 14)
		if tc.want == "" {
			if aged != fresh {
				t.Errorf("age %d below the threshold changed the block:\n%s", tc.age, ansi.Strip(aged))
			}
			continue
		}
		if !strings.Contains(ansi.Strip(aged), tc.want) {
			t.Errorf("age %d: the block should say %q:\n%s", tc.age, tc.want, ansi.Strip(aged))
		}
	}
}

// The close says so only where the turn ends with steps open on a list past
// the threshold: a fresh list, a finished one and no list add no row.
func TestTurnClose_AStaleOpenListIsOneRow(t *testing.T) {
	base := TurnClose{Steps: 2, Tools: 30, Elapsed: "4m 10s"}
	plain := base.View(110)
	for _, tc := range []struct {
		name string
		list *TurnList
		want string
	}{
		{"no list", nil, ""},
		{"fresh", &TurnList{Done: 2, Total: 7, Age: StaleListRounds - 1}, ""},
		{"complete", &TurnList{Done: 7, Total: 7, Age: 40}, ""},
		{"stale and open", &TurnList{Done: 2, Total: 7, Age: 25}, "steps 2 of 7 done · list unchanged 25 rounds"},
	} {
		c := base
		c.List = tc.list
		got := c.View(110)
		if tc.want == "" {
			if got != plain {
				t.Errorf("%s: the close gained a row:\n%s", tc.name, ansi.Strip(got))
			}
			continue
		}
		if !strings.Contains(ansi.Strip(got), tc.want) || !strings.Contains(c.Summary(), tc.want) {
			t.Errorf("%s: the close and its summary should say %q:\n%s\n%s", tc.name, tc.want, ansi.Strip(got), c.Summary())
		}
	}
}

// TestGolden_InspectorStepsStale captures the STEPS block over a list that has
// stood unchanged past the threshold: open, with the age after the count or
// on a row of its own where the step's title leaves no room, and collapsed,
// where the age joins the count in the heading while it fits
// (docs/interface/surfaces.md#the-inspector-rail).
func TestGolden_InspectorStepsStale(t *testing.T) {
	captureGolden(t, "inspector-steps-stale", "inspector rail · steps, a list gone quiet", goldenWidths, func(width int) []golden.Panel {
		w := InspectorWidthFor(width)
		short := &InspectorSteps{Done: 2, Total: 7, Current: "Add the flag", Age: 25}
		long := &InspectorSteps{Done: 2, Total: 7, Current: "Return it from runRound and handle it in Run", Age: 25}
		return []golden.Panel{
			{Label: "open · the age after the count and the step it is on", View: staleRail(short).View(w, 14)},
			{Label: "open · a long step title, and the age on a row of its own", View: staleRail(long).View(w, 14)},
			{Label: "collapsed · the rail too short for the rows, the age in the heading", View: staleRail(short).View(w, 7)},
		}
	})
}

// TestGolden_TurnCloseStaleList captures the close of a turn that ended with
// steps open on a list gone quiet: one dim row under the others, in the
// notes row's empty gutter.
func TestGolden_TurnCloseStaleList(t *testing.T) {
	captureGolden(t, "turn-close-stale-list", "turn close rows · a list gone quiet", goldenWidths, func(width int) []golden.Panel {
		c := TurnClose{
			Steps: 2, Tools: 30, Elapsed: "4m 10s", Spend: "$0.41",
			Changes: &TurnChanges{
				Files: 5, Added: 210, Removed: 12,
				Keys: []TurnKey{reviewTurnOffer(), commitTurnOffer()},
				Note: "all tracked in git",
			},
			List: &TurnList{Done: 2, Total: 7, Age: 25},
		}
		return []golden.Panel{
			{Label: "done · steps open on a list unchanged past the threshold", View: c.View(width)},
		}
	})
}

// TestGolden_StepsScreenStale captures `/steps` over a list gone quiet: the
// age follows the count in the header, in the rail block's words.
func TestGolden_StepsScreenStale(t *testing.T) {
	captureGolden(t, "steps-screen-stale", "the steps screen · a list gone quiet", goldenWidths, func(width int) []golden.Panel {
		s := stepsScreen(1)
		s.Age = 25
		return []golden.Panel{
			{Label: "the header · the count, then how long the list has stood", View: s.View(width)},
		}
	})
}
