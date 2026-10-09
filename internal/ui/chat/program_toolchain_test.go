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
	m.containment = Containment{Status: "contained", Mechanism: "bwrap", Profile: "workspace", Network: true, Toolchain: tc}
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

// `/toolchain` reaches the drafting through the whole program: its answer
// arrives off the UI goroutine and lands on the card, esc leaves with the
// draft waiting and nothing written, and only the yes writes.
func TestProgram_ToolchainDraftLandsOnACardAndWritesOnlyOnYes(t *testing.T) {
	var mu sync.Mutex
	var written [][]byte
	tc := Toolchain{
		Draft: func(context.Context, bool) ToolchainDraft { return draftedDeclaration() },
		WriteDraft: func(content []byte) (string, error) {
			mu.Lock()
			defer mu.Unlock()
			written = append(written, content)
			return ".shhh/toolchain.toml", nil
		},
	}
	m, _ := scriptedSession(programTurn{text: "nobody asked the model"})
	m.containment = Containment{Status: "contained", Mechanism: "bwrap", Profile: "workspace", Network: true, Toolchain: tc}
	tm := runProgramAt(t, m, 110, 40)

	send(tm, toolchainCommandName)
	waitForAll(t, tm, "Approve toolchain declaration", "provides golangci-lint", "edit first")
	programPress(t, tm, "esc")
	waitForText(t, tm, "opens the draft again")
	mu.Lock()
	if len(written) != 0 {
		t.Fatalf("esc wrote %q", written)
	}
	mu.Unlock()

	send(tm, toolchainCommandName)
	waitForText(t, tm, "write it")
	tm.Send(programAllow)
	waitForText(t, tm, "wrote .shhh/toolchain.toml")
	mu.Lock()
	defer mu.Unlock()
	if len(written) != 1 {
		t.Fatalf("the yes wrote %d files, want one", len(written))
	}
}
