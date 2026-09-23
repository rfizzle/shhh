package chat

// Routes into the transcript that has already been written: reading mode,
// the pointer, the folds, the receipts a session leaves, and the surfaces
// opened from the draft (program_routes_test.go says what these are for).

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/provider"
)

// One applied edit opened to its depths from reading mode: the row, the
// change in place under it, and the whole screen — and back to in place.
func TestProgram_AnEditRowOpensToEachDepth(t *testing.T) {
	dir := fixtureDir(t, map[string]string{
		"loop.go":  "package agent\n\nconst limit = 25\n\nfunc round(a *Agent) error {\n    if a.round >= limit {\n        return ErrRoundLimit\n    }\n    a.round++\n    return nil\n}\n",
		"notes.md": "The cap is read from one place.\n",
	})
	loop := filepath.Join(dir, "loop.go")
	tm := runProgramAt(t, readingSession(dir,
		programTurn{calls: reads("loop.go")},
		programTurn{calls: []provider.ToolCall{call("e1", "edit_file", fmt.Sprintf(`{"path":%q,"old_text":"        return ErrRoundLimit","new_text":"        return &RoundsExhausted{Round: a.round, Max: limit}"}`, loop))}},
		programTurn{calls: reads("notes.md")},
		programTurn{text: "The cap is where the file says it is."},
	), 130, 40)

	send(tm, "raise the round cap")
	waitForText(t, tm, "RoundsExhausted")
	tm.Send(programHandover)
	tm.Send(programAllow)
	waitForText(t, tm, "where the file says")
	programPress(t, tm, "ctrl+o", "k", "k", "k")
	waitForText(t, tm, "row 2 of 5")
	programPress(t, tm, "enter")
	waitForText(t, tm, "@@ -4,7 +4,7 @@")
	programPress(t, tm, "enter")
	waitForText(t, tm, "[j/k] scroll")
	programPress(t, tm, "esc")

	frame := finalFrame(t, tm)
	frameHas(t, frame, "@@ -4,7 +4,7 @@", "RoundsExhausted")
	if strings.Contains(frame, "[j/k] scroll") {
		t.Fatalf("esc did not come back from the full screen:\n%s", frame)
	}
}

// The pointer reads the pane while the draft keeps the keyboard: shift+↑
// walks to a read and shift+→ opens it, shift+↓ and shift+→ open the next,
// and esc on the empty draft folds every row the reader opened — then,
// pressed twice with nothing left to fold, opens the rewind.
func TestProgram_EscFoldsWhatThePointerOpened(t *testing.T) {
	dir := fixtureDir(t, map[string]string{
		"loop.go":  "package agent\n\nfunc loop() {}\n",
		"round.go": "package agent\n\nconst limit = 25\n",
	})
	tm := runProgram(t, readingSession(dir,
		programTurn{calls: reads("loop.go", "round.go")},
		programTurn{text: "The limit is a checkpoint, not a wall."},
	))

	send(tm, "how is the round limit counted")
	waitForText(t, tm, "not a wall")
	programPress(t, tm, "shift+up", "shift+up", "shift+up", "shift+up", "shift+right", "shift+down", "shift+right")
	waitForText(t, tm, "const limit = 25")
	programPress(t, tm, "esc")
	programPress(t, tm, "esc")
	waitForText(t, tm, "folded 2 rows")
	programPress(t, tm, "esc", "esc")
	waitForText(t, tm, "pick a turn to return to")

	frame := finalFrame(t, tm)
	frameHas(t, frame, "pick a turn to return to")
	if strings.Contains(frame, "const limit = 25") {
		t.Fatalf("the rows the pointer opened are still open:\n%s", frame)
	}
}

// /compact leaves a receipt on the grid and marks the turns it replaced as
// out of the window.
func TestProgram_CompactLeavesAReceiptOverTheTurnsItFolded(t *testing.T) {
	dir := fixtureDir(t, map[string]string{"loop.go": "package agent\n", "round.go": "package agent\n"})
	tm := runProgram(t, readingSession(dir,
		programTurn{text: "Locate the round accounting\n", calls: reads("loop.go", "round.go")},
		programTurn{text: "The limit is read from a constant in loop.go."},
		programTurn{text: "round.go declares the constant, so the two files disagree about who owns it."},
		programTurn{text: "The turns so far established loop.go as the owner."},
	))

	send(tm, "where is the round limit counted")
	waitForText(t, tm, "constant in loop.go")
	send(tm, "and who declares it")
	waitForText(t, tm, "who owns it")
	send(tm, "/compact")
	waitForAll(t, tm, "folded turn", "out of the window")

	frame := finalFrame(t, tm)
	frameHas(t, frame, "folded turn", "out of the window")
}

// A paste too big for the draft is staged as a token, the token opens onto
// the paste, and the sent message keeps it as a row that expands.
func TestProgram_APasteTooBigForTheDraftIsAToken(t *testing.T) {
	var log strings.Builder
	log.WriteString("=== RUN   TestRoundLimit\n")
	for i := 1; i <= 211; i++ {
		fmt.Fprintf(&log, "    loop_test.go:44: round %d reached after 2.1s\n", i)
	}
	log.WriteString("--- FAIL: TestRoundLimit (2.11s)\n")
	m, _ := scriptedSession(programTurn{text: "The loop never leaves the round."})
	tm := runProgram(t, m)

	tm.Send(tea.PasteMsg{Content: "this test log says the loop never stops "})
	tm.Send(tea.PasteMsg{Content: log.String()})
	waitForText(t, tm, "will cost")
	programPress(t, tm, "alt+v")
	waitForText(t, tm, "PASTE 1")
	programPress(t, tm, "q")
	waitForText(t, tm, "open paste 1")
	programPress(t, tm, "enter")
	waitForText(t, tm, "never leaves the round")

	frame := finalFrame(t, tm)
	frameHas(t, frame, "paste 1", "never leaves the round")
}

// The palette and a picker both open from the draft and both give it back:
// ctrl+/ lists the commands, and /model opens the list of models to pick.
func TestProgram_ThePaletteAndTheModelPickerOpenFromTheDraft(t *testing.T) {
	m, _ := scriptedSession(programTurn{text: "nothing to do"})
	// The palette lists recent files; this one has none, so what it lists
	// does not depend on the directory the suite runs in.
	m.recentFiles = func() []project.RecentFile { return nil }
	m = m.WithModelOptions([]string{"scripted-large", "scripted-mini"}).WithModelSwitcher(func(string) {})
	tm := runProgram(t, m)

	programPress(t, tm, "ctrl+/")
	waitForText(t, tm, "COMMANDS")
	programPress(t, tm, "esc")
	send(tm, "/model")
	waitForText(t, tm, "scripted-mini")

	frame := finalFrame(t, tm)
	frameHas(t, frame, "scripted-large", "scripted-mini")
	if strings.Contains(frame, "COMMANDS") {
		t.Fatalf("esc did not close the palette:\n%s", frame)
	}
}
