package chat

// The toolchain card, from the command line to the lines it runs
// (program_routes_test.go says what these are for).

import (
	"context"
	"sync"
	"testing"

	"github.com/rfizzle/shhh/internal/tools"
)

// `/setup` reaches the card through the whole program, esc leaves with
// nothing run, and the yes runs every declared line off the UI goroutine and
// lands the reading taken after them on the transcript.
func TestProgram_SetupPutsTheDeclaredLinesOnOneCard(t *testing.T) {
	var mu sync.Mutex
	var ran []string
	tc := toolchainFixture(nil)
	tc.Install = func(_ context.Context, line string) tools.ExecResult {
		mu.Lock()
		defer mu.Unlock()
		ran = append(ran, line)
		return tools.ExecResult{Outcome: tools.ExecSucceeded}
	}
	m, _ := scriptedSession(programTurn{text: "nobody asked the model"})
	m = m.WithContainment(Containment{Status: "contained", Mechanism: "bwrap", Profile: "workspace", Network: true, Toolchain: tc})
	tm := runProgramAt(t, m, 110, 40)

	send(tm, setupCommandName)
	waitForAll(t, tm, "Approve toolchain install", "install them", "2 hosts")
	programPress(t, tm, "esc")
	waitForGone(t, tm, "Approve toolchain install")
	waitForText(t, tm, "offers it again")
	mu.Lock()
	if len(ran) != 0 {
		t.Fatalf("esc ran %v", ran)
	}
	mu.Unlock()

	send(tm, setupCommandName)
	waitForText(t, tm, "install them")
	tm.Send(programAllow)
	waitForText(t, tm, "every declared tool is on PATH")
	mu.Lock()
	defer mu.Unlock()
	if len(ran) != 2 {
		t.Fatalf("the yes ran %v, want both lines", ran)
	}
}
