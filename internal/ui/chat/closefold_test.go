package chat

// A steer folds into the run it steers: the run closes once, on every turn
// since the last close.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// steerRun moves the run on a turn the way an injected steer does: the number
// advances and nothing closes.
func steerRun(m Model) Model {
	m.turnCount++
	m.agent.SetTurn(m.turnCount)
	return m
}

func closeCount(m Model) int {
	n := 0
	for _, e := range m.transcript {
		if e.kind == entryTurnClose {
			n++
		}
	}
	return n
}

// steeredWrite is a run that wrote a file and then took a steer.
func steeredWrite(t *testing.T) Model {
	t.Helper()
	m := turnModel(t)
	m = sendText(t, m, "write the file")
	m = applyWrite(t, m, filepath.Join(t.TempDir(), "main.go"), "package main\n", "y")
	return steerRun(m)
}

func TestClose_ASteeredRunShowsWhatItWrote(t *testing.T) {
	m := finishTurn(t, steeredWrite(t))

	if got := closeCount(m); got != 1 {
		t.Fatalf("a steered run closes once, got %d close rows", got)
	}
	c := lastClose(t, m)
	if c.Changes == nil || c.Changes.Files != 1 || c.Changes.Added != 1 {
		t.Fatalf("the run's close shows what the turn before the steer wrote, got %+v", c.Changes)
	}
	if c.WroteNothing {
		t.Fatal("the run wrote a file, so it never says it wrote nothing")
	}
	if len(c.Changes.Keys) == 0 {
		t.Fatal("the folded close still offers the commit")
	}
}

func TestClose_AStoppedSteerStillClosesOnce(t *testing.T) {
	m := steeredWrite(t)
	m.setTurnState(stateStreaming)
	m.cancelStreaming()

	if got := closeCount(m); got != 1 {
		t.Fatalf("a stopped steer closes the run once, got %d close rows", got)
	}
	c := lastClose(t, m)
	if c.State != components.TurnCancelled || c.Changes == nil || c.Changes.Files != 1 {
		t.Fatalf("the stopped run says so and still shows its write, got %v %+v", c.State, c.Changes)
	}
}

func TestClose_TheStatusLineAndTheCloseAgreeAfterASteer(t *testing.T) {
	m := finishTurn(t, steeredWrite(t))

	files, added, removed := m.changes.Totals()
	c := lastClose(t, m).Changes
	if c == nil || c.Files != files || c.Added != added || c.Removed != removed {
		t.Fatalf("the close %+v and the session totals %d +%d −%d should agree", c, files, added, removed)
	}
	if got := ansi.Strip(m.statusChanges()); !strings.Contains(got, "session · ") {
		t.Fatalf("the idle status line states the session's change, got %q", got)
	}
}

func TestClose_TheTurnCountIsUntouchedByTheFold(t *testing.T) {
	m := turnModel(t)
	m = sendText(t, m, "write the file")
	first := m.turnCount
	m = applyWrite(t, m, filepath.Join(t.TempDir(), "main.go"), "package main\n", "y")
	m = finishTurn(t, steerRun(m))

	if m.turnCount != first+1 {
		t.Fatalf("a steer still counts as a turn: want %d, got %d", first+1, m.turnCount)
	}
	for i := len(m.transcript) - 1; i >= 0; i-- {
		if m.transcript[i].kind == entryTurnClose {
			if m.transcript[i].turn != first+1 {
				t.Fatalf("the close is stamped with the run's last turn %d, got %d", first+1, m.transcript[i].turn)
			}
			return
		}
	}
	t.Fatal("no close row")
}
