package chat

import (
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/rpc"
)

// inboundProgram is a session under the real program with its listener's
// channel in the test's hand: a line put on it arrives the way the host's
// socket delivers one, on a goroutine of its own.
func inboundProgram(t *testing.T, mode agent.Mode, p *programProvider) (*program, chan<- InboundLine) {
	t.Helper()
	lines := make(chan InboundLine, 1)
	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, streamOf(p)).
		WithApprovalMode(mode, nil).
		WithInbound(Inbound{Lines: lines})
	return runProgramAt(t, m, 110, 40), lines
}

func sendOver(t *testing.T, lines chan<- InboundLine, from, text string) <-chan string {
	t.Helper()
	taken := make(chan string, 1)
	lines <- InboundLine{From: from, Text: text, Taken: taken}
	return taken
}

// An idle session handed a line from another one starts a turn on it: the
// steer row names the slot it came from, the model is asked with the line in
// the session's own voice, and its answer reaches the frame.
func TestProgram_ALineFromAnotherSessionStartsATurn(t *testing.T) {
	p := &programProvider{turns: []programTurn{{text: "rebased onto master"}}}
	tm, lines := inboundProgram(t, agent.ModeManual, p)

	taken := sendOver(t, lines, "2026-09-23 10:41:07", "master moved: rebase onto it")
	waitForText(t, tm, "from session 2026-09-23 10:41:07")
	waitForText(t, tm, "rebased onto master")
	select {
	case word := <-taken:
		if word != rpc.TakenDelivered {
			t.Errorf("the sender was told %q", word)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the sender was never told what became of its line")
	}

	finalFrame(t, tm)
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.asked) == 0 {
		t.Fatal("the line never reached the model")
	}
	asked := p.asked[0]
	if !asked.Machine || !strings.Contains(asked.Content, "session 2026-09-23 10:41:07") ||
		!strings.HasSuffix(asked.Content, "master moved: rebase onto it") {
		t.Fatalf("the model was asked with %+v", asked)
	}
}

// Under auto the line is held: its card comes up naming the sender, and [y]
// is what passes it to the turn.
func TestProgram_AHeldLineIsPassedOnFromItsCard(t *testing.T) {
	p := &programProvider{turns: []programTurn{{text: "rebased onto master"}}}
	tm, lines := inboundProgram(t, agent.ModeAuto, p)

	sendOver(t, lines, "2026-09-23 10:41:07", "master moved: rebase onto it")
	waitForText(t, tm, "a line from session 2026-09-23 10:41:07")
	waitForText(t, tm, "pass it to the turn")
	tm.Send(programAllow)
	waitForText(t, tm, "rebased onto master")
	frame := finalFrame(t, tm)
	if strings.Contains(frame, "pass it to the turn") {
		t.Fatalf("the answered card is still on the screen:\n%s", frame)
	}
}
