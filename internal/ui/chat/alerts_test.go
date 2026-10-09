package chat

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/quality"
)

// alertsModel is a session four turns in with every way an alert can stand
// or be answered: the tests failing in turns 1 and 3 and never answered, a
// formatter that broke and came back clean in turn 2, and a linter the
// quality gate answered in turn 4 — the last with its output kept under an
// evidence id, one reduced and one trimmed.
func alertsModel(t *testing.T) Model {
	t.Helper()
	return alertsModelAt(t, 160, 50)
}

// alertsModelAt is alertsModel on a terminal of the given size.
func alertsModelAt(t *testing.T, width, height int) Model {
	t.Helper()
	m := inspectorModel(t, width, height)
	m.transcript = []entry{
		{kind: entryUser, text: "fix the loop", turn: 1},
		{kind: entryCommand, text: "go test ./...", exitCode: 1, duration: 4200 * time.Millisecond, turn: 1,
			toolResult: "FAIL\n[output reduced; the original is ev-0123456789abcdef]"},
		{kind: entryCommand, text: "golangci-lint run ./...", exitCode: 1, duration: 9 * time.Second, turn: 1},
		{kind: entryUser, text: "and format it", turn: 2},
		{kind: entryCommand, text: "gofmt -l internal", exitCode: 1, turn: 2},
		{kind: entryCommand, text: "gofmt -l internal", exitCode: 0, turn: 2},
		{kind: entryUser, text: "again", turn: 3},
		{kind: entryCommand, text: "go test ./internal/agent", exitCode: 2, turn: 3,
			elided: &elidedRow{evidence: "ev-fedcba9876543210"}},
		{kind: entryUser, text: "run the suite", turn: 4},
		{kind: entryTool, toolName: quality.ToolName, toolResult: gateResult("PASS", 5, 5), turn: 4},
		{kind: entryCommand, text: "go test ./...", exitCode: 1, turn: 4},
	}
	m.turnCount = 4
	return m
}

// The screen is the block's own walk: the same alerts in the same number,
// standing first and newest first, and the standing ones are exactly the
// rail's live ones — never a second reading of the transcript.
func TestAlertsScreen_ReadsTheEpisodesTheBlockReads(t *testing.T) {
	m := alertsModel(t)
	s := m.alertsScreenData()
	block := m.inspectorAlerts()
	if len(s.Alerts) != len(block) {
		t.Fatalf("the screen has %d alerts and the block %d", len(s.Alerts), len(block))
	}
	for _, item := range s.Alerts {
		found := false
		for _, a := range block {
			found = found || a == item.Alert
		}
		if !found {
			t.Errorf("the screen's %+v is not one of the block's alerts %+v", item.Alert, block)
		}
	}
	var got []string
	for _, item := range s.Alerts {
		got = append(got, item.Alert.Label)
	}
	// The go test episode was answered by the gate and broke again after it,
	// so it stands as a new episode beside the one the gate answered.
	want := []string{"go test", "go test", "gofmt", "golangci-lint run"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("the order is %v, want %v", got, want)
	}
	live := block.Live()
	if len(live) != 1 || s.Alerts[0].Alert.Superseded || !s.Alerts[1].Alert.Superseded {
		t.Fatalf("standing must lead and match the rail's live alerts %+v: %+v", live, s.Alerts)
	}
}

// Each superseded episode names what answered it and the turn it did; each
// run carries its turn, its ending, its time and the evidence id its output
// was kept under, whether the reduction named it or the trim kept it.
func TestAlertsScreen_EachEpisodeSaysWhatAnsweredItAndWhatEachRunCameTo(t *testing.T) {
	s := alertsModel(t).alertsScreenData()
	standing, gated, gofmt, lint := s.Alerts[0], s.Alerts[1], s.Alerts[2], s.Alerts[3]
	if standing.Answer != nil || len(standing.Runs) != 1 || standing.Runs[0].Turn != 4 {
		t.Errorf("the failure after the gate should stand alone: %+v", standing)
	}
	if gated.Answer == nil || gated.Answer.What != "quality gate default passed" || gated.Answer.Turn != 4 {
		t.Errorf("the tests before the gate should be answered by it: %+v", gated.Answer)
	}
	if len(gated.Runs) != 2 || gated.Runs[0].Evidence != "ev-0123456789abcdef" || gated.Runs[1].Evidence != "ev-fedcba9876543210" {
		t.Errorf("each run's evidence id, reduced and trimmed: %+v", gated.Runs)
	}
	if r := gated.Runs[0]; r.Turn != 1 || r.Outcome != "exit 1" || r.Duration != "4.2s" || r.Line != "go test ./..." {
		t.Errorf("the first run: %+v", r)
	}
	if lint.Answer == nil || lint.Answer.What != "quality gate default passed" {
		t.Errorf("the linter was answered by the gate: %+v", lint.Answer)
	}
	if gofmt.Answer == nil || gofmt.Answer.What != "gofmt -l internal came back clean" || gofmt.Answer.Turn != 2 {
		t.Errorf("the formatter was answered by its own clean run: %+v", gofmt.Answer)
	}
}

// The standing count the screen draws is the one the rail's heading and the
// turn's close agree on: the close for the turn the gate ran in reads green
// up to the gate, and the one failure after it is the one alert standing.
func TestAlertsScreen_AgreesWithTheTurnsClose(t *testing.T) {
	m := alertsModel(t)
	turn4 := m.transcript[8:]
	checks := resolveChecks(turn4)
	if got := len(checks.standing()); got != 2 {
		t.Fatalf("the close reads %d standing attempts, want the gate and the failure after it", got)
	}
	standing := 0
	for _, a := range m.alertsScreenData().Alerts {
		if !a.Alert.Superseded {
			standing++
		}
	}
	if standing != len(m.inspectorAlerts().Live()) || standing != 1 {
		t.Fatalf("the screen stands %d, the rail %d", standing, len(m.inspectorAlerts().Live()))
	}
}

// /alerts opens the screen over the rail without touching the draft, enter
// opens the pointer's runs, and esc leaves with the draft as it was.
func TestAlertsScreen_OpensLeavesAndKeepsTheDraft(t *testing.T) {
	m := alertsModel(t)
	m.input.SetValue("half a sentence")
	next, _ := m.runCommand("/alerts", "/alerts")
	m = next.(Model)
	if m.state != stateAlerts || m.screens.alerts() == nil || !m.inspectorHidden() {
		t.Fatalf("/alerts should put the screen up over the rail, state %d", m.state)
	}
	next, _ = m.updateAlerts(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	if !m.screens.alerts().Open {
		t.Fatal("enter should show the episode's runs")
	}
	if view := stripANSI(strings.Join(overlays()[stateAlerts].lines(m, 160, 40), "\n")); !strings.Contains(view, "each run") {
		t.Errorf("the open episode does not draw its runs:\n%s", view)
	}
	next, _ = m.updateAlerts(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = next.(Model)
	if m.state == stateAlerts || m.screens.alerts() != nil {
		t.Fatalf("esc should leave the screen, state %d", m.state)
	}
	if got := m.input.Value(); got != "half a sentence" {
		t.Fatalf("the screen took the draft: %q", got)
	}
}

// A session that has broken nothing still opens the screen: nothing broken
// is the answer the reader asked for.
func TestAlertsScreen_NothingBrokenOpensOnASentence(t *testing.T) {
	m := inspectorModel(t, 160, 50)
	m.transcript = nil
	next, _ := m.openAlerts()
	m = next.(Model)
	if m.state != stateAlerts {
		t.Fatalf("/alerts over a clean session should still open, state %d", m.state)
	}
	view := stripANSI(strings.Join(overlays()[stateAlerts].lines(m, 160, 40), "\n"))
	if !strings.Contains(view, "nothing this session ran has come back broken") {
		t.Errorf("the empty screen:\n%s", view)
	}
}

// alertsKeptOutput is a store holding the reduced run's output and nothing
// else: the trimmed run's entry has been purged since.
func alertsKeptOutput(m Model) Model {
	m.wiring.Evidence = Evidence{Read: func(id string, limit int) (string, bool) {
		if id != "ev-0123456789abcdef" {
			return "", false
		}
		return "--- FAIL: TestLoop (0.00s)\n    loop_test.go:42: the loop ran 151 rounds\nFAIL\nexit status 1\n", true
	}}
	m.agent.StoreElided(m.wiring.Evidence.Keep)
	return m
}

// alertsPress sends one key through the whole session, the way a reader's
// keystroke arrives.
func alertsPress(t *testing.T, m Model, code rune) Model {
	t.Helper()
	next, _ := m.Update(tea.KeyPressMsg{Code: code})
	return next.(Model)
}

// On an opened episode, enter on a run whose output was kept opens it in the
// full-screen viewer, and esc comes back to the alerts screen with the runs
// still out rather than to the prompt.
func TestAlertsScreen_EnterOnAKeptRunOpensItsOutputAndComesBack(t *testing.T) {
	m := alertsKeptOutput(alertsModel(t))
	next, _ := m.runCommand("/alerts", "/alerts")
	m = next.(Model)
	m = alertsPress(t, m, tea.KeyDown)  // the episode the gate answered
	m = alertsPress(t, m, tea.KeyEnter) // its runs
	m = alertsPress(t, m, tea.KeyEnter) // the first, whose output was reduced
	if m.state != stateOutputFull || m.fullOutput == nil || m.outputReturn != stateAlerts {
		t.Fatalf("enter on a kept run should open its output over the screen: state %d, return %d", m.state, m.outputReturn)
	}
	view := stripANSI(strings.Join(overlays()[stateOutputFull].lines(m, 160, 40), "\n"))
	for _, want := range []string{"$ go test ./...", "the loop ran 151 rounds"} {
		if !strings.Contains(view, want) {
			t.Errorf("the opened output is missing %q:\n%s", want, view)
		}
	}
	if hint := stripANSI(m.renderOutputFullHint()); !strings.Contains(hint, "back to the alerts") {
		t.Errorf("the viewer should say where esc goes: %q", hint)
	}
	m = alertsPress(t, m, tea.KeyEscape)
	if m.state != stateAlerts || m.screens.alerts() == nil || !m.screens.alerts().Open {
		t.Fatalf("esc should come back to the screen with the runs out, state %d", m.state)
	}
}

// A run whose kept output the store has since let go of says so on the
// screen, and a run whose output was never cut opens nothing and says that.
func TestAlertsScreen_ARunWithNothingToOpenSaysSo(t *testing.T) {
	m := alertsKeptOutput(alertsModel(t))
	next, _ := m.runCommand("/alerts", "/alerts")
	m = next.(Model)
	m = alertsPress(t, m, tea.KeyDown)
	m = alertsPress(t, m, tea.KeyEnter)
	m = alertsPress(t, m, tea.KeyDown) // the trimmed run, purged since
	m = alertsPress(t, m, tea.KeyEnter)
	if m.state != stateAlerts {
		t.Fatalf("a purged entry should open nothing, state %d", m.state)
	}
	if got := m.screens.alerts().Notice; !strings.Contains(got, "ev-fedcba9876543210 is no longer in the evidence store") {
		t.Errorf("the purged entry's notice: %q", got)
	}
	m = alertsPress(t, m, tea.KeyUp)
	m = alertsPress(t, m, tea.KeyUp) // back to the standing episode, whose run was never cut
	m = alertsPress(t, m, tea.KeyEnter)
	m = alertsPress(t, m, tea.KeyEnter)
	if m.state != stateAlerts {
		t.Fatalf("a run nothing was kept of should open nothing, state %d", m.state)
	}
	if view := stripANSI(strings.Join(overlays()[stateAlerts].lines(m, 160, 40), "\n")); !strings.Contains(view, "never cut, so nothing was kept") {
		t.Errorf("a run nothing was kept of did not say so:\n%s", view)
	}
}
