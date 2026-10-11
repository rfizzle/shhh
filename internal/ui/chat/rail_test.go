package chat

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestCommand_RailOpensTheWholeRail: /rail is the foot line's key twin. It
// opens the rail with nothing shed — on a terminal too narrow to draw one, and
// on one too short for its blocks — scrolls it, leaves it on esc, and answers
// in a row where the rail has nothing to say.
func TestCommand_RailOpensTheWholeRail(t *testing.T) {
	for _, size := range []struct{ width, height int }{{80, 40}, {160, 20}} {
		m := railDoorModel(t)
		next, _ := m.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
		m = next.(Model)
		m.input.SetValue("half a sentence")
		typed, _ := m.runCommand("/rail", "/rail")
		opened := typed.(Model)
		if opened.state != stateRail || railShowing(opened) == nil {
			t.Fatalf("%dx%d: /rail should open the rail in full, got state %d", size.width, size.height, opened.state)
		}
		if got := opened.input.Value(); got != "half a sentence" {
			t.Fatalf("/rail must not take the draft, got %q", got)
		}
		// Every block the rail holds is on the screen, read through its pager.
		seen := map[string]bool{}
		for range 40 {
			for _, line := range strings.Split(stripANSI(opened.View().Content), "\n") {
				for _, block := range []string{"THIS TURN", "ALERTS", "CHANGES", "AGENTS", "CONTEXT", "SPEND"} {
					if strings.Contains(line, "  "+block) {
						seen[block] = true
					}
				}
			}
			next, _ := opened.Update(tea.KeyPressMsg{Code: tea.KeyDown})
			opened = next.(Model)
		}
		if len(seen) != 6 {
			t.Fatalf("%dx%d: the rail in full should hold every block, saw %v:\n%s",
				size.width, size.height, seen, stripANSI(opened.View().Content))
		}
		left, _ := opened.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
		if left.(Model).state == stateRail {
			t.Fatalf("%dx%d: esc should leave the rail in full", size.width, size.height)
		}
	}

	empty := frameModel(t, 160, 40)
	empty.TotalTokensIn, empty.TotalTokensOut = 0, 0
	if rail := empty.inspectorData(); rail.Empty() {
		typed, _ := empty.runCommand("/rail", "/rail")
		if typed.(Model).state == stateRail {
			t.Fatal("a rail with nothing to say answers in a row, not an empty screen")
		}
	}
}
