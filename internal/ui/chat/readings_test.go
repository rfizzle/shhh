package chat

// The readings history and the screen over it
// (docs/interface/surfaces.md#the-supporting-screens).

import (
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// A quiet reading earns no transcript row, and the history is where it is
// kept instead; the turn's start, which clears the rail's reading, leaves the
// history alone.
func TestReadings_AQuietReadingIsKeptAcrossTurns(t *testing.T) {
	m := summaryModel(t, &readingProvider{text: "Reading the loop."})
	before := len(m.transcript)
	landReading(&m, agent.SummaryVerdict{Text: "Reading the loop.", State: agent.SummaryOnTarget, Round: 3})
	if len(m.transcript) != before {
		t.Fatalf("a quiet reading should leave no row, got %d new entries", len(m.transcript)-before)
	}
	m.summary.startTurn()
	landReading(&m, agent.SummaryVerdict{Text: "Rewriting the README.", State: agent.SummaryOffTarget, Round: 4})
	if got := len(m.summary.readings); got != 2 {
		t.Fatalf("the history should hold both readings across the turn boundary, got %d", got)
	}
	if r := m.summary.readings[0]; r.verdict.Text != "Reading the loop." || r.target != "make the round limit a checkpoint" {
		t.Errorf("the quiet reading was not kept with its target: %+v", r)
	}
}

// The history is the session's: a new session starts with none.
func TestReadings_ANewSessionStartsWithNone(t *testing.T) {
	m := summaryModel(t, &readingProvider{text: "Reading the loop."})
	landReading(&m, agent.SummaryVerdict{Text: "Reading the loop.", State: agent.SummaryOnTarget, Round: 3})
	m.startNewSession()
	if len(m.summary.readings) != 0 || m.summary.dropped != 0 {
		t.Fatalf("a new session inherited %d readings", len(m.summary.readings))
	}
}

// Past the bound the oldest go, and the count of them is kept for the screen
// to say.
func TestReadings_TheHistoryIsBounded(t *testing.T) {
	m := summaryModel(t, &readingProvider{text: "Reading."})
	for i := range summaryHistoryMax + 3 {
		landReading(&m, agent.SummaryVerdict{Text: "Reading.", State: agent.SummaryOnTarget, Round: i + 1})
	}
	if got := len(m.summary.readings); got != summaryHistoryMax {
		t.Fatalf("the history holds %d readings, want %d", got, summaryHistoryMax)
	}
	if m.summary.dropped != 3 || m.summary.readings[0].verdict.Round != 4 {
		t.Fatalf("the oldest three should have gone: dropped %d, first round %d",
			m.summary.dropped, m.summary.readings[0].verdict.Round)
	}
	data := m.readingsScreenData()
	if data.Dropped != 3 || data.Kept != summaryHistoryMax {
		t.Errorf("the screen was not told what was dropped: %d of %d", data.Dropped, data.Kept)
	}
}

// A reading that earned a delivered steer says so, and says so again once the
// reader has taken the steer back.
func TestReadings_ASteerAndItsWithdrawalAreTheReadingsOutcome(t *testing.T) {
	m := steeredModel(t)
	last := m.summary.readings[len(m.summary.readings)-1]
	if last.steer == "" {
		t.Fatal("the reading that earned the steer should carry it")
	}
	m.focusIdx = steerNoticeIndex(t, m)
	updated, _, _ := m.withdrawSteer(keys.Shown(keys.Row.Undo))
	next := updated.(Model)
	data := next.readingsScreenData()
	if top := data.Readings[0]; !top.Steered || !top.Withdrawn {
		t.Fatalf("the newest reading should read steered and withdrawn, got %+v", top)
	}
}

// `/readings` opens the screen, newest first, and its preview is the
// transcript's opened summary row — the reading whole, its reason and the
// instruction it was judged against.
func TestReadings_TheCommandOpensTheReadingWhole(t *testing.T) {
	m := summaryModel(t, &readingProvider{text: "Reading."})
	landReading(&m, agent.SummaryVerdict{Text: "Reading the loop.", State: agent.SummaryOnTarget, Round: 3})
	landReading(&m, agent.SummaryVerdict{Text: "Rewriting the README.", State: agent.SummaryOffTarget,
		Reason: "docs were not asked for", Round: 9})
	next, _ := m.runCommand("/readings", "/readings")
	opened := next.(Model)
	if opened.state != stateReadings || opened.readingsScreen == nil {
		t.Fatalf("/readings should open the screen, got state %d", opened.state)
	}
	view := stripANSI(opened.readingsScreen.View(130))
	for _, want := range []string{"/readings", "2 readings", "r 9 · off target", "r 3 · on target",
		"Rewriting the README.", "docs were not asked for", "read against: make the round limit a checkpoint"} {
		if !strings.Contains(view, want) {
			t.Errorf("the screen is missing %q:\n%s", want, view)
		}
	}
	if strings.Index(view, "r 9 ·") > strings.Index(view, "r 3 ·") {
		t.Errorf("the newest reading should come first:\n%s", view)
	}
	closed, _ := opened.updateReadings(keyPress('q'))
	if got := closed.(Model); got.state == stateReadings || got.readingsScreen != nil {
		t.Fatalf("the way out should close the screen, got state %d", got.state)
	}
}

// With nothing to show the command answers in a line.
func TestReadings_NoReadingsSaysSo(t *testing.T) {
	m := summaryModel(t, &readingProvider{text: "Reading."})
	next, _ := m.runCommand("/readings", "/readings")
	if got := next.(Model); got.state == stateReadings {
		t.Fatal("a session with no readings should not open an empty screen")
	}
}

// A steer is filed against the off-target reading that earned it, not a
// newer quiet one, even when neither gave a reason; and taking it back marks
// that one reading, not an earlier turn's steer whose digest row reads the
// same because rounds restart every turn.
func TestReadings_ASteerIsFiledAgainstTheReadingThatEarnedIt(t *testing.T) {
	var s summaryState
	iv := agent.Intervention{Kind: agent.InterveneSteer}
	s.keepReading(summaryReading{verdict: agent.SummaryVerdict{State: agent.SummaryOffTarget, Round: 3}, turn: 1})
	s.markSteered(iv, "round 3 · steered")
	s.keepReading(summaryReading{verdict: agent.SummaryVerdict{State: agent.SummaryOffTarget, Round: 3}, turn: 2})
	s.keepReading(summaryReading{verdict: agent.SummaryVerdict{State: agent.SummaryOnTarget, Round: 4}, turn: 2})
	s.markSteered(iv, "round 3 · steered")
	if s.readings[1].steer == "" || s.readings[2].steer != "" {
		t.Fatalf("the steer went to the wrong reading: %+v", s.readings)
	}
	s.steers = 1
	s.dropIntervention(iv, "round 3 · steered")
	if !s.readings[1].withdrawn || s.readings[0].withdrawn {
		t.Fatalf("the withdrawal marked the wrong reading: %+v", s.readings)
	}
}
