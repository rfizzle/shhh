package chat

// The flakes screen, from the command line to its preview (program_routes_test.go
// says what these are for).

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/storage"
)

// `/gate flakes` reaches the screen through the whole program, the latest
// flake first, and the pointer's key reaches it too: moving to the older
// check puts its count and its command in the preview.
func TestProgram_GateFlakesListsTheCheckoutsLedger(t *testing.T) {
	now := time.Now()
	m := frameModel(t, 130, 40).WithGate(Gate{
		Manage: func([]string) string { return "" },
		Flakes: func() ([]storage.Flake, error) {
			return []storage.Flake{
				{Suite: "default", Check: "vet", Command: "./checks/vet.sh", Seen: 2, FirstExit: 1,
					FirstAt: now.Add(-time.Hour), LastAt: now},
				{Suite: "fast", Check: "lint", Command: "make lint", Seen: 5, FirstExit: 2,
					FirstAt: now.Add(-72 * time.Hour), LastAt: now.Add(-48 * time.Hour)},
			}, nil
		},
	})
	tm := runProgramAt(t, m, 130, 40)

	send(tm, "/gate flakes")
	waitForText(t, tm, "/gate flakes · 2 checks · 7 flakes")
	waitForText(t, tm, "flaked 2 times in this checkout")

	tm.Send(tea.KeyPressMsg{Code: tea.KeyDown})
	waitForText(t, tm, "flaked 5 times in this checkout")

	frameHas(t, finalFrame(t, tm), "fast · 5 times", "make lint", "the failing run exited 2")
}

// A check the ledger says has flaked three times this week reaches the rail
// through the whole program, under ALERTS, before the session has run
// anything.
func TestProgram_AFlakyCheckStandsOnTheRail(t *testing.T) {
	now := time.Now()
	m := frameModel(t, 130, 40).WithGate(Gate{
		Manage: func([]string) string { return "" },
		Flakes: func() ([]storage.Flake, error) {
			return []storage.Flake{{Suite: "default", Check: "vet", Command: "./checks/vet.sh", Seen: 3,
				FirstExit: 1, FirstAt: now.Add(-48 * time.Hour), LastAt: now.Add(-time.Hour)}}, nil
		},
	})
	tm := runProgramAt(t, m, 130, 40)

	waitForText(t, tm, "flaked 3× this week")
	frameHas(t, finalFrame(t, tm), "ALERTS", "1 standing", "~ vet")
}
