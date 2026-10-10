package chat

// The key list, from the chord over a half-written draft to the draft again
// (program_routes_test.go says what these are for).

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// The chord opens the key list over a sentence being typed, typing filters
// it, esc puts it away, and the sentence is in the box as it was with the
// transcript above it unchanged.
func TestProgram_TheKeyListOpensOverADraftAndLeavesItStanding(t *testing.T) {
	m, _ := scriptedSession(programTurn{text: "Hello from the scripted provider."})
	tm := runProgramAt(t, m, 80, 40)
	send(tm, "say hi")
	waitForText(t, tm, "Hello from the scripted provider")

	tm.Send(tea.PasteMsg{Content: "why does the parser"})
	waitForText(t, tm, "why does the parser")
	programPress(t, tm, "ctrl+]")
	waitForAll(t, tm, "first or last", "DRAFT")
	programPress(t, tm, "r", "e", "a", "d", "i", "n", "g")
	waitForAll(t, tm, "READING", "copy")
	programPress(t, tm, "esc", "esc")
	waitForGone(t, tm, "first or last")

	frame := finalFrame(t, tm)
	frameHas(t, frame, "why does the parser", "Hello from the scripted provider")
	if strings.Contains(frame, "READING") || strings.Contains(frame, "send the message") {
		t.Fatalf("the key list left something on the screen:\n%s", frame)
	}
}
