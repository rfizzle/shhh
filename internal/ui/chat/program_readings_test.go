package chat

// The readings screen, from the command line to its preview (program_routes_test.go
// says what these are for).

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
)

// `/readings` reaches the screen through the whole program, newest first, and
// the pointer's key reaches it too: moving to the older reading puts that
// reading whole in the preview, the instruction it was read against included.
func TestProgram_ReadingsOpensEveryReadingTheSessionTook(t *testing.T) {
	m := summaryModel(t, &readingProvider{text: "Reading."})
	landReading(&m, agent.SummaryVerdict{Text: "Reading the loop and its counter.", State: agent.SummaryOnTarget, Round: 3})
	landReading(&m, agent.SummaryVerdict{Text: "Rewriting the README.", State: agent.SummaryOffTarget,
		Reason: "docs were not asked for", Round: 9})
	tm := runProgramAt(t, m, 130, 40)

	send(tm, "/readings")
	waitForText(t, tm, "/readings · 2 readings")
	waitForText(t, tm, "docs were not asked for")

	tm.Send(tea.KeyPressMsg{Code: tea.KeyDown})
	waitForText(t, tm, "Reading the loop and its counter.")

	frameHas(t, finalFrame(t, tm), "r 3 · on target", "read against: make the round limit a checkpoint")
}
