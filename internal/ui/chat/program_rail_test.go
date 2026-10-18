package chat

import (
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/llm"
	"github.com/rfizzle/shhh/internal/tools"
)

// railFootCell is the screen cell of the rail's foot line on a frame: the row
// carrying `↓ N more ·`, at the column its arrow is drawn in.
func railFootCell(frame string) (x, y int, ok bool) {
	for i, line := range strings.Split(frame, "\n") {
		j := strings.Index(line, "↓ ")
		if j < 0 || !strings.Contains(line[j:], " more ·") {
			continue
		}
		return len([]rune(line[:j])), i, true
	}
	return 0, 0, false
}

// On a twenty-two-row terminal with the session's turn running and a child
// waiting on the reader, the rail drops what nobody is waiting on to the
// foot line; a click on that line opens the rail in full over the pane, and
// a click on the same cell puts the short rail back — through the runtime,
// with the terminal's own mouse reports.
func TestProgram_TheRailsFootLineOpensTheWholeRail(t *testing.T) {
	hold, release := quietHold(t)
	open := make(chan struct{})
	t.Cleanup(sync.OnceFunc(func() { close(open) }))
	root := programRepo(t, nil)
	lead := spawns("One writer on the round accounting.\n", "writer", "writer-1")
	lead.hold = hold
	m, _ := agentSession(t, root, nil, children{
		"writer-1": {
			{calls: []llm.ToolCall{call("w1", tools.ExecCommandName, `{"command":"echo counted"}`)}},
			{text: "The counter is read at the top of the loop, and nowhere else."},
		},
	}, lead, programTurn{text: "The writer is on it.", hold: open})
	m.pointer.mouseOn = true
	tm := runProgramAt(t, m, 130, 22)

	send(tm, "survey the round accounting")
	release()
	waitForText(t, tm, "Start a writer")
	tm.Send(programAllow)
	waitForText(t, tm, "writer-1 · Approve command")
	// The card hands the keyboard back and stays standing: the writer goes on
	// waiting on the reader.
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEscape})
	waitForText(t, tm, " more ·")

	var x, y int
	if !eventually(func() bool {
		f := tm.frame.Load()
		if f == nil {
			return false
		}
		var ok bool
		x, y, ok = railFootCell(*f)
		return ok
	}) {
		t.Fatalf("the short rail drew no foot line:\n%s", *tm.frame.Load())
	}
	tm.Send(mousePress(x, y))
	tm.Send(mouseRelease(x, y))
	waitForText(t, tm, "/rail · the rail in full")
	tm.Send(mousePress(x, y))
	tm.Send(mouseRelease(x, y))
	waitForText(t, tm, " more ·")

	frame := finalFrame(t, tm)
	if strings.Contains(frame, "/rail · the rail in full") {
		t.Fatalf("the cell that opened the rail in full should close it:\n%s", frame)
	}
}
