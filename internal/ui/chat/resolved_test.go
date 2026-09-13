package chat

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// gateResult is a formatted quality-gate result the way the runner writes one,
// so the reading under test is the same parse every surface makes.
func gateResult(verdict string, passed, total int) string {
	return fmt.Sprintf("Quality gate %q: %s — %d/%d checks passed (1.4s)\nTree: clean",
		"default", verdict, passed, total)
}

func failedTest() entry {
	return entry{kind: entryCommand, text: "go test ./internal/agent/...",
		exitCode: 1, duration: 4200 * time.Millisecond}
}

func passingGate() entry {
	return entry{kind: entryTool, toolName: quality.ToolName, toolResult: gateResult("PASS", 5, 5)}
}

// A turn that watched the tests fail, fixed the code and ran the repository's
// own suite again closes on the suite's answer. The failure is counted, not
// argued with.
func TestResolvedChecks_FailThenPass(t *testing.T) {
	es := []entry{failedTest(), passingGate()}
	c := turnChecksRow(es, false)
	if c == nil || c.Failed {
		t.Fatalf("an applicable pass settles the turn, got %+v", c)
	}
	if c.Label != "quality gate default" || !strings.Contains(c.Counts, "5/5 checks") {
		t.Fatalf("the row is the verification that settled it, got %+v", c)
	}
	if c.Superseded != 1 {
		t.Fatalf("the earlier failure is counted on the row, got %+v", c)
	}
	// The attempt itself is untouched: its row keeps the outcome and the
	// time it had, which is what makes it still readable in the transcript.
	if es[0].exitCode != 1 || es[0].duration != 4200*time.Millisecond {
		t.Fatalf("a superseded attempt keeps its own record, got %+v", es[0])
	}
}

// Nothing answered the failure, so the turn says so.
func TestResolvedChecks_FailWithNoRetry(t *testing.T) {
	c := turnChecksRow([]entry{failedTest()}, false)
	if c == nil || !c.Failed {
		t.Fatalf("an unanswered failure is the turn's verdict, got %+v", c)
	}
	if c.Superseded != 0 {
		t.Fatalf("nothing superseded it, got %+v", c)
	}
	if !strings.Contains(c.Counts, "exit 1") {
		t.Fatalf("the row carries the exit code, got %+v", c)
	}
}

// A run nobody let finish reached no verdict, so it is neither a failure the
// turn reports nor a failure a later pass gets credit for answering.
func TestResolvedChecks_CancelledThenPass(t *testing.T) {
	stopped := entry{kind: entryCommand, text: "go test ./...", exitCode: -1,
		end: commandEnd{outcome: components.OutcomeStopped}}
	c := turnChecksRow([]entry{stopped, passingGate()}, false)
	if c == nil || c.Failed {
		t.Fatalf("the suite's pass is the verdict, got %+v", c)
	}
	if c.Superseded != 0 {
		t.Fatalf("a stopped run is not a failure to supersede, got %+v", c)
	}
	if c.Label != "quality gate default" {
		t.Fatalf("the row is the gate's, got %+v", c)
	}
	// And on its own it is no verdict at all: the turn's close already says
	// the reader stopped it.
	if got := turnChecksRow([]entry{stopped}, false); got != nil {
		t.Fatalf("a stopped run alone reports nothing, got %+v", got)
	}
	// The ceiling and a signal from outside are not the reader's decision.
	// Nobody is waiting to be told the code is fine, so those are failures,
	// and the row says what ended the run rather than a status it never had.
	for _, outcome := range []string{components.OutcomeTimedOut, components.OutcomeKilled} {
		e := entry{kind: entryCommand, text: "go test ./...", exitCode: -1,
			end: commandEnd{outcome: outcome}}
		c := turnChecksRow([]entry{e}, false)
		if c == nil || !c.Failed {
			t.Fatalf("%s is a check that did not come back clean, got %+v", outcome, c)
		}
		if !strings.Contains(c.Counts, outcome) {
			t.Fatalf("the row says what ended it, got %+v", c)
		}
	}
}

// A command that has nothing to do with the code is not a verdict about it,
// and a failing one beside a passing suite cannot make the turn read failed.
func TestResolvedChecks_UnrelatedFailureBesideAPassingGate(t *testing.T) {
	es := []entry{
		{kind: entryCommand, text: "curl -sf https://example.invalid", exitCode: 6, turn: 1},
		passingGate(),
	}
	c := turnChecksRow(es, false)
	if c == nil || c.Failed || c.Superseded != 0 {
		t.Fatalf("an unrelated command was never a check, got %+v", c)
	}
	if c.Label != "quality gate default" {
		t.Fatalf("the gate is the whole verdict, got %+v", c)
	}
	// The rail still stops calling it standing bad news: the suite has since
	// come back clean over the tree it failed on.
	m := inspectorModel(t, 144, 40)
	m.transcript = es
	if live := m.inspectorAlerts().Live(); len(live) != 0 {
		t.Fatalf("a passing suite answers the alerts older than it, got %+v", live)
	}
}

// The suite speaks only for the tree it ran over, so a failure after it is
// still standing.
func TestResolvedChecks_AFailureAfterTheGateStands(t *testing.T) {
	es := []entry{failedTest(), passingGate(), failedTest()}
	c := turnChecksRow(es, false)
	if c == nil || !c.Failed {
		t.Fatalf("a failure the suite never saw is unanswered, got %+v", c)
	}
	if c.Counts != "1 of 2 passing" {
		t.Fatalf("the standing attempts are the tally, got %q", c.Counts)
	}
	if c.Superseded != 1 {
		t.Fatalf("only the failure before the pass was answered, got %+v", c)
	}
}

// A stale pass disowned the tree it ran over, so it answers nothing.
func TestResolvedChecks_AStalePassSupersedesNothing(t *testing.T) {
	stale := entry{kind: entryTool, toolName: quality.ToolName,
		toolResult: gateResult("PASS", 5, 5) + "\nSTALE: the tree has changed since this run"}
	c := turnChecksRow([]entry{failedTest(), stale}, false)
	if c == nil || !c.Failed {
		t.Fatalf("a stale pass is not a pass, got %+v", c)
	}
	if c.Superseded != 0 {
		t.Fatalf("a stale pass supersedes nothing, got %+v", c)
	}
	if c.Counts != "0 of 2 passing" {
		t.Fatalf("both attempts are still standing, got %q", c.Counts)
	}
}

// A trim takes the gate's output out of the conversation and out of the row
// under it. The verdict is not output, and the close row that read it was
// drawn once and cannot be redrawn — so a trim must not hand the rail back a
// failure the turn already answered.
func TestResolvedChecks_AVerdictSurvivesTheTrim(t *testing.T) {
	m := inspectorModel(t, 144, 40)
	m.turnCount = 1
	m.appendEntry(failedTest())
	m.appendEntry(passingGate())
	settled := turnChecksRow(m.transcript, false)
	if settled == nil || settled.Failed || settled.Superseded == 0 {
		t.Fatalf("the turn closed on the suite's pass, got %+v", settled)
	}
	// The trim as it leaves the row: the body replaced, and what the row read
	// off that body kept beside it.
	gate := &m.transcript[len(m.transcript)-1]
	kept := gateVerdictLine(*gate)
	if !strings.Contains(kept, "PASS") {
		t.Fatalf("the verdict line is what the trim keeps, got %q", kept)
	}
	gate.elided = &elidedRow{verdict: kept}
	gate.toolResult = "[elided: quality gate output, evidence ev-1]"

	if after := turnChecksRow(m.transcript, false); after == nil ||
		after.Failed || after.Superseded != settled.Superseded {
		t.Fatalf("the close row reads the same verdict after the trim, got %+v", after)
	}
	if live := m.inspectorAlerts().Live(); len(live) != 0 {
		t.Fatalf("the rail cannot resurrect an answered failure, got %+v", live)
	}
	if suite := suiteOfTurn(m.transcript); suite != "default" {
		t.Fatalf("the row still knows which suite to offer again, got %q", suite)
	}
}

// The rail and the close row read the same resolution, so the cockpit cannot
// be red about a turn the transcript called green.
func TestResolvedChecks_TheRailAgreesWithTheCloseRow(t *testing.T) {
	m := inspectorModel(t, 144, 40)
	m.turnCount = 2
	m.appendEntry(failedTest())
	if live := m.inspectorAlerts().Live(); len(live) == 0 {
		t.Fatal("an unanswered failure is standing bad news")
	}
	m.appendEntry(passingGate())
	alerts := m.inspectorAlerts()
	live := alerts.Live()
	c := turnChecksRow(m.transcript, false)
	if c == nil || c.Failed {
		t.Fatalf("the close row reads the suite's pass, got %+v", c)
	}
	if len(live) != 0 {
		t.Fatalf("the rail cannot still be failing, got %+v", alerts)
	}
	// Answered rather than deleted: the block still counts what the pass
	// answered, the way the close row does.
	if len(alerts) == 0 {
		t.Fatal("the answered failures are kept rather than dropped")
	}
	for _, a := range alerts {
		if !a.Superseded {
			t.Fatalf("every failure the pass answered is marked, got %+v", alerts)
		}
	}
}

// The verdict pinned beside a review is the same reading, and the output it
// pins is the standing failure's rather than the answered one's.
func TestResolvedChecks_TheReviewVerdictReadsTheSameResolution(t *testing.T) {
	m := inspectorModel(t, 144, 40)
	m.turnCount = 1
	m.transcript = []entry{
		{kind: entryUser, text: "fix the loop", turn: 1},
		{kind: entryCommand, text: "go test ./internal/agent/...", exitCode: 1,
			toolResult: "loop_test.go:14: want 3, got 4", turn: 1},
		passingGate(),
	}
	v := m.reviewVerdict(1)
	if v == nil || v.Failed {
		t.Fatalf("the review reports the settled verdict, got %+v", v)
	}
	if len(v.Detail) != 0 {
		t.Fatalf("an answered failure is not pinned beside the files, got %+v", v.Detail)
	}
	if !strings.Contains(v.Label, "1 earlier failure superseded") {
		t.Fatalf("the review says what the pass answered, got %q", v.Label)
	}
}
