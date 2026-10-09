package chat

import (
	"testing"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/provider"
)

// A turn's time is split by what it waited on — the model before its first
// event, the model writing, the tools, and the person at a card — from the
// stamps the turn already takes, and the four add up to the duration the
// turn closes with. The clock is held and moved by hand, so every figure
// below is exact.
func TestTurnTime_ASplitSumsToTheTurn(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	was := clock
	clock = func() time.Time { return now }
	t.Cleanup(func() { clock = was })
	step := func(d time.Duration) { now = now.Add(d) }

	type closed struct {
		duration time.Duration
		outcome  string
		split    agent.TurnSplit
	}
	var got []closed
	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream, Wiring{
		Observer: observe.Observer{
			TurnTimed: func(_, _ int64, d time.Duration, outcome string, s agent.TurnSplit) {
				got = append(got, closed{d, outcome, s})
			},
			Turn: func(int64, int64, time.Duration, string) { t.Fatal("a timed turn was reported untimed") },
		},
	})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = next.(Model)
	next, _ = m.sendUserMessage("look at main.go")
	m = next.(Model)

	update := func(msg tea.Msg) {
		t.Helper()
		next, _ := m.Update(msg)
		m = next.(Model)
	}
	// Two seconds before the model says anything, one of it writing.
	step(2 * time.Second)
	update(tokenMsg{text: "Reading it."})
	step(time.Second)
	call := provider.ToolCall{ID: "c1", Name: "read_file", Arguments: `{"path":"main.go"}`}
	update(toolCallsMsg{calls: []provider.ToolCall{call}})
	// Half a second of tools, five at a card, one more running what was
	// allowed.
	step(500 * time.Millisecond)
	m.setTurnState(stateConfirmRun)
	step(5 * time.Second)
	m.setTurnState(stateRunningCmd)
	step(time.Second)
	m.state = stateStreaming
	update(toolResultsMsg{runID: m.agent.RunID(), results: []agent.ToolResult{{Call: call, Result: "package main"}}})
	// Three seconds before the next request's first event — one that drew
	// nothing — and one second of it writing.
	step(3 * time.Second)
	update(tokenMsg{})
	step(time.Second)
	update(doneMsg{})

	if len(got) != 1 {
		t.Fatalf("turn closes = %+v, want one", got)
	}
	c := got[0]
	want := agent.TurnSplit{ModelFirst: 5 * time.Second, ModelStream: 2 * time.Second,
		Tool: 1500 * time.Millisecond, Person: 5 * time.Second}
	if c.split.ModelFirst != want.ModelFirst || c.split.ModelStream != want.ModelStream ||
		c.split.Tool != want.Tool || c.split.Person != want.Person {
		t.Fatalf("split = %+v, want %+v", c.split, want)
	}
	if c.duration != 13500*time.Millisecond || c.split.Total() != c.duration {
		t.Fatalf("parts add to %s, the turn took %s", c.split.Total(), c.duration)
	}
	ms := observe.TurnMillis(c.duration, c.split)
	if ms[0]+ms[1]+ms[2]+ms[3] != c.duration.Milliseconds() {
		t.Fatalf("millis %v do not add to %d", ms, c.duration.Milliseconds())
	}
	// The longest stretch with nothing new on the screen is the card's
	// wait and the run after it, and nothing arrived during it.
	if q := c.split.Quiet; q.Took != 6*time.Second || q.On != agent.WaitPerson || q.Delivered != 0 {
		t.Fatalf("quiet stretch = %+v", q)
	}
}

// A command that is still printing is not a stretch in which the screen had
// nothing: each new line of its live tail is counted the way a stream event
// that drew nothing is, so the long build reads as quiet with a count and
// the one that printed nothing reads as waiting.
func TestQuietStretch_APrintingCommandIsNotWaiting(t *testing.T) {
	run := func(lines ...string) agent.Quiet {
		now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
		was := clock
		clock = func() time.Time { return now }
		t.Cleanup(func() { clock = was })

		var got []agent.TurnSplit
		m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream, Wiring{
			Observer: observe.Observer{
				TurnTimed: func(_, _ int64, _ time.Duration, _ string, s agent.TurnSplit) { got = append(got, s) },
				Turn:      func(int64, int64, time.Duration, string) { t.Fatal("a timed turn was reported untimed") },
			},
		})
		next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
		m = next.(Model)
		next, _ = m.sendUserMessage("build it")
		m = next.(Model)
		update := func(msg tea.Msg) {
			t.Helper()
			next, _ := m.Update(msg)
			m = next.(Model)
		}
		now = now.Add(time.Second)
		update(tokenMsg{text: "Building."})
		call := provider.ToolCall{ID: "c1", Name: "read_file", Arguments: `{"path":"main.go"}`}
		update(toolCallsMsg{calls: []provider.ToolCall{call}})
		m.setTurnState(stateRunningCmd)
		tail := &commandTail{}
		m.runTail = tail
		for _, l := range lines {
			now = now.Add(2 * time.Second)
			tail.Set(l)
			update(spinner.TickMsg{})
			// The same line on the next frame is the same line.
			update(spinner.TickMsg{})
		}
		now = now.Add(2 * time.Second)
		m.state = stateStreaming
		update(toolResultsMsg{runID: m.agent.RunID(), results: []agent.ToolResult{{Call: call, Result: "ok"}}})
		update(doneMsg{})
		if len(got) != 1 {
			t.Fatalf("turn closes = %d, want one", len(got))
		}
		return got[0].Quiet
	}

	if q := run("compiling a", "compiling b", "linking"); q.On != agent.WaitTool || q.Delivered != 3 {
		t.Fatalf("a printing command's stretch = %+v, want the tool's with 3 delivered", q)
	}
	if q := run(); q.On != agent.WaitTool || q.Delivered != 0 {
		t.Fatalf("a silent command's stretch = %+v, want the tool's with none delivered", q)
	}
}
