package chat

import (
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// turnsModel is a session three turns in: turn 1 wrote a file and closed,
// turn 2 ran a command and wrote nothing, and turn 3 is still running with a
// tool call and a file of its own.
func turnsModel(t *testing.T) Model {
	t.Helper()
	m := inspectorModel(t, 160, 50)
	m.transcript = []entry{
		{kind: entryUser, text: "raise the cap", turn: 1},
		{kind: entryTool, toolName: "read_file", toolArgs: `{"path":"loop.go"}`, toolResult: "ok", turn: 1},
		{kind: entryTurnClose, turn: 1, close: &components.TurnClose{State: components.TurnDone, Steps: 1, Tools: 1,
			Elapsed: "9.0s", Spend: "$0.0100", Changes: &components.TurnChanges{Files: 1, Added: 2, Removed: 1}}},
		{kind: entryUser, text: "run the tests", turn: 2},
		{kind: entryCommand, text: "go test ./...", toolResult: "ok", turn: 2},
		{kind: entryTurnClose, turn: 2, close: &components.TurnClose{State: components.TurnDone, Tools: 1,
			Elapsed: "3.0s", Spend: "$0.0040", WroteNothing: true}},
		{kind: entryUser, text: "and the docs", turn: 3},
		{kind: entryTool, toolName: "read_file", toolArgs: `{"path":"README.md"}`, toolResult: "ok", turn: 3},
	}
	m.changes.Add(3, changeset.Record{Path: "README.md", AfterExists: true, After: "# loop\n"})
	m.turnCount, m.turnOpen = 3, true
	return m
}

// Each closed turn is its close row's own block — the same figures, not a
// count of them made again — and the running turn is the rail's reading.
func TestTurnsScreen_ReadsTheCloseRowsAndTheRunningTurn(t *testing.T) {
	m := turnsModel(t)
	s := m.turnsScreenData()
	if len(s.Turns) != 3 {
		t.Fatalf("want three turns, got %d", len(s.Turns))
	}
	running, wroteNothing, first := s.Turns[0], s.Turns[1], s.Turns[2]
	if running.N != 3 || running.Running == nil || running.Close != nil || running.Running.Tools != 1 || running.Running.Files != 1 {
		t.Fatalf("the running turn is not the rail's reading of it: %+v", running)
	}
	if wroteNothing.N != 2 || wroteNothing.Close != m.transcript[5].close || wroteNothing.Reviewable {
		t.Fatalf("turn 2 is not its close row, or offers a review of nothing: %+v", wroteNothing)
	}
	if first.N != 1 || first.Close != m.transcript[2].close {
		t.Fatalf("turn 1 is not its close row: %+v", first)
	}
	if s.Subject != "3 turns" || s.Tools != "3 tools" || !strings.HasSuffix(s.Spend, " spent") {
		t.Fatalf("the header's tally: %q, %q", s.Subject, s.Spend)
	}
}

// A resumed conversation holds no close for its earlier turns, and the one
// restoreTurnClose puts back carries no figures: both are drawn as their
// number and their files, and a turn with neither is left out rather than
// drawn as a row of zeros.
func TestTurnsScreen_AResumedTurnIsItsFilesAndNoFigures(t *testing.T) {
	m := inspectorModel(t, 160, 50)
	m.transcript = []entry{
		{kind: entryUser, text: "first", turn: 1},
		{kind: entryUser, text: "second", turn: 2},
		{kind: entryUser, text: "third", turn: 3},
	}
	m.changes.Reset()
	m.changes.Add(1, changeset.Record{Path: "a.go", AfterExists: true, After: "package a\n"})
	m.changes.Add(3, changeset.Record{Path: "b.go", AfterExists: true, After: "package b\n"})
	m.turnCount, m.turnOpen = 3, false
	m.restoreTurnClose()

	s := m.turnsScreenData()
	if len(s.Turns) != 2 || s.Turns[0].N != 3 || s.Turns[1].N != 1 {
		t.Fatalf("want turns 3 and 1, the two with files, got %+v", s.Turns)
	}
	for _, turn := range s.Turns {
		if turn.Close != nil || turn.Running != nil || len(turn.Files) != 1 || !turn.Reviewable {
			t.Errorf("turn %d should be its files and no figures: %+v", turn.N, turn)
		}
	}
	screen := s
	if view := stripANSI(screen.View(130)); !strings.Contains(view, "no figures kept") || strings.Contains(view, "Done") {
		t.Errorf("a restored turn claimed figures it does not have:\n%s", view)
	}
}

// A turn that stopped at its round limit and was never given more has no
// close, because its pause row stands in for one; the screen draws it from
// the figures that row kept — the rounds, the wall time and the cost — and
// never as a turn whose figures were not kept.
func TestTurns_APausedTurnKeepsItsFigures(t *testing.T) {
	m := turnModel(t).WithMaxToolRounds(1)
	m = sendText(t, m, "fix the round accounting")
	m.accumulateUsage(&provider.Usage{PromptTokens: 1200, CompletionTokens: 80})
	spend := m.totalsLabel(m.turnSpend())
	m = applyWrite(t, m, filepath.Join(t.TempDir(), "loop.go"), "package agent\n", "y")
	if m.roundPause == nil || m.turnOpen {
		t.Fatalf("the turn should have stopped at its ceiling and closed, open %v", m.turnOpen)
	}
	pause := pauseEntry(t, m)

	s := m.turnsScreenData()
	if len(s.Turns) != 1 {
		t.Fatalf("want the paused turn, got %+v", s.Turns)
	}
	turn := s.Turns[0]
	if turn.Close != nil || turn.Running != nil || turn.Paused == nil {
		t.Fatalf("the paused turn is not its pause row's figures: %+v", turn)
	}
	p := turn.Paused
	if p.Used != pause.pause.used || p.Limit != pause.pause.limit || p.Spend != spend || spend == "" ||
		p.Elapsed != components.FormatElapsed(pause.duration) {
		t.Fatalf("the paused turn's figures: %+v, want %d of %d, %q, %s", p, pause.pause.used,
			pause.pause.limit, spend, components.FormatElapsed(pause.duration))
	}
	view := stripANSI(s.View(130))
	for _, want := range []string{"⚠ turn 1", "paused", spend, "Paused at its round limit"} {
		if !strings.Contains(view, want) {
			t.Errorf("the screen is missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "no figures kept") {
		t.Errorf("a paused turn was drawn as one whose figures were not kept:\n%s", view)
	}
}

// `[enter]` on a turn with files opens that turn's review, and the review's
// own way out comes back to the screen rather than to the prompt.
func TestTurnsScreen_EnterOpensTheTurnsReviewAndComesBack(t *testing.T) {
	m := turnsModel(t)
	m.changes.Add(1, changeset.Record{Path: "loop.go", AfterExists: true, After: "const limit = 50\n"})
	opened, _ := m.openTurns()
	m = opened.(Model)
	if m.state != stateTurns || !m.inspectorHidden() {
		t.Fatalf("/turns should take the screen over the rail, got state %d", m.state)
	}
	for _, k := range []tea.KeyPressMsg{{Code: tea.KeyDown}, {Code: tea.KeyDown}, {Code: tea.KeyEnter}} {
		next, _ := m.updateTurns(k)
		m = next.(Model)
	}
	if m.state != stateReview || m.reviewTurnN != 1 {
		t.Fatalf("enter on turn 1 should review it, got state %d, turn %d", m.state, m.reviewTurnN)
	}
	back, _ := m.closeReview()
	if m = back.(Model); m.state != stateTurns || m.screens.turns() == nil {
		t.Fatalf("leaving the review should come back to the turns, got state %d", m.state)
	}
	gone, _ := m.updateTurns(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m = gone.(Model); m.state == stateTurns || m.screens.turns() != nil {
		t.Fatalf("esc should leave the screen, got state %d", m.state)
	}
}

// Enter on a turn that changed nothing does nothing: the offer is grey.
func TestTurnsScreen_EnterOnATurnThatWroteNothingStays(t *testing.T) {
	opened, _ := turnsModel(t).openTurns()
	m := opened.(Model)
	next, _ := m.updateTurns(tea.KeyPressMsg{Code: tea.KeyDown})
	next, _ = next.(Model).updateTurns(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m = next.(Model); m.state != stateTurns {
		t.Fatalf("enter on a turn with nothing to review left the screen, state %d", m.state)
	}
}

// A session that has run nothing says so rather than opening an empty table.
func TestTurnsScreen_NoTurnsSaysSo(t *testing.T) {
	m := inspectorModel(t, 160, 50)
	m.transcript, m.turnCount = nil, 0
	next, _ := m.openTurns()
	if m = next.(Model); m.state == stateTurns {
		t.Fatal("/turns opened over a session with no turns")
	}
}
