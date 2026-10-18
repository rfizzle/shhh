package components

import (
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/ui/golden"
)

// TestStateTable_EveryStateSpeaksOnce holds the table to what it promises: a
// state has a word of its own, so two states that share a mark are still told
// apart on a terminal with no colour, and a state with no mark is the one that
// keeps the kind's glyph.
func TestStateTable_EveryStateSpeaksOnce(t *testing.T) {
	seen := map[string]State{}
	for s := State(0); s < stateCount; s++ {
		w := s.Word()
		if w == "" {
			t.Errorf("state %d has no word", s)
		}
		if prev, dup := seen[w]; dup {
			t.Errorf("states %d and %d are both %q", prev, s, w)
		}
		seen[w] = s
		if s.Glyph() == "" && s != StateOK {
			t.Errorf("state %q has no glyph and is not the one that keeps the kind's", w)
		}
	}
}

// TestStateTable_OutcomeWordsAreTheTables holds the closed outcome
// vocabulary to the table, so a word is spelled in one place.
func TestStateTable_OutcomeWordsAreTheTables(t *testing.T) {
	for _, c := range []struct {
		got  string
		want State
	}{
		{OutcomeOK, StateOK},
		{OutcomeRunning, StateRunning},
		{OutcomeQueued, StateQueued},
		{OutcomeCancelled, StateCancelled},
		{OutcomeDenied, StateDenied},
	} {
		if c.got != c.want.Word() {
			t.Errorf("outcome %q is not state word %q", c.got, c.want.Word())
		}
	}
}

// TestStateTable_LegendNamesEveryMark holds the legend to the table: every
// mark a row can lead with is mapped under `?`.
func TestStateTable_LegendNamesEveryMark(t *testing.T) {
	legend := strings.Join(glyphLegend(60), "\n")
	for s := State(0); s < stateCount; s++ {
		if g := s.Glyph(); g != "" && !strings.Contains(legend, g) {
			t.Errorf("legend does not map %q (%s)", g, s.Word())
		}
	}
}

// TestGolden_StateTable captures every state as the table draws it, and the
// rows the first cancelled state rides on: a command the reader stopped, a
// turn they stopped, and the status line that says so.
func TestGolden_StateTable(t *testing.T) {
	captureGolden(t, "state-table", "one glyph, token and word per state", goldenWidths, func(width int) []golden.Panel {
		var table []string
		for s := State(0); s < stateCount; s++ {
			table = append(table, strings.TrimSpace(s.Mark()+" "+s.Painted()))
		}
		cmd := func(mut func(*ActivityRow)) string {
			r := ActivityRow{Kind: ActivityCommand, Verb: "run", Target: "go test ./... -run TestRoundLimit"}
			mut(&r)
			return r.View(width)
		}
		return []golden.Panel{
			{Label: "table · every state's mark and word", View: strings.Join(table, "\n")},
			{Label: "row · a command the reader cancelled", View: cmd(func(r *ActivityRow) {
				r.State, r.Outcome, r.Duration = ActivityDenied, OutcomeCancelled, "3.7s"
			})},
			{Label: "row · a command that returned", View: cmd(func(r *ActivityRow) {
				r.Outcome, r.Duration = OutcomeOK, "3.7s"
			})},
			{Label: "row · a command still running", View: cmd(func(r *ActivityRow) {
				r.State, r.Outcome, r.Duration = ActivityRunning, OutcomeRunning, "42s"
			})},
			{Label: "close · a turn the reader cancelled", View: TurnClose{
				State: TurnCancelled, Steps: 2, Tools: 4, Elapsed: "21.6s", Spend: "$0.05",
			}.View(width)},
			{Label: "status · the line a cancelled turn resolves into", View: TurnStatus{
				Done: true, Outcome: TurnCancelled,
			}.View(width)},
		}
	})
}
