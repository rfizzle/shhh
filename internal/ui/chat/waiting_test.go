package chat

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/provider"
)

// waitingModel is a turn whose request has just gone out, on a held clock
// the returned step moves; update feeds it a message the way the program
// would.
func waitingModel(t *testing.T) (m *Model, step func(time.Duration), update func(tea.Msg)) {
	t.Helper()
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	was := clock
	clock = func() time.Time { return now }
	t.Cleanup(func() { clock = was })
	model := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream, Wiring{})
	next, _ := model.Update(tea.WindowSizeMsg{Width: 110, Height: 40})
	model = next.(Model)
	next, _ = model.sendUserMessage("run the chat tests")
	model = next.(Model)
	m = &model
	return m, func(d time.Duration) { now = now.Add(d) }, func(msg tea.Msg) {
		t.Helper()
		next, _ := m.Update(msg)
		*m = next.(Model)
	}
}

// statusLine is the frame's top-rail status after its spinner frame, unclipped
// by any slot.
func statusLine(t *testing.T, m Model) string {
	t.Helper()
	s, ok := m.turnStatus()
	if !ok {
		t.Fatal("a running turn has no status line")
	}
	_, line, _ := strings.Cut(ansi.Strip(s.View(200)), " ")
	return strings.TrimSpace(line)
}

// Between a request going out and anything drawable coming back, the line
// says the turn is waiting on the model, for how long, and what has arrived:
// nothing, or events that draw nothing — a live stream that is quiet, which
// reads differently from a silent one. Inside the last thirty seconds before
// the stream's idle deadline a silent wait says the retry is coming; a quiet
// one never does, because its events hold the deadline off. It never reads
// `acting…` (docs/interface/surfaces.md#the-input-frame).
func TestWaitingState_QuietIsNotSilent(t *testing.T) {
	m, step, update := waitingModel(t)

	step(14 * time.Second)
	if got, want := statusLine(t, *m), "waiting… · model 14s · silent — nothing arrived · turn 14s"; got != want {
		t.Errorf("a silent request reads %q, want %q", got, want)
	}

	// The default deadline is two minutes: 12 seconds short of it, the
	// retry is coming.
	step(94 * time.Second)
	if got, want := statusLine(t, *m), "waiting… · model 1m 48s · silent — retry in 12s · turn 1m 48s"; got != want {
		t.Errorf("a silent request near its deadline reads %q, want %q", got, want)
	}

	// A setting of its own moves the deadline, and a negative one removes it.
	m.timing.idle = 300 * time.Second
	if got := statusLine(t, *m); !strings.Contains(got, "silent — nothing arrived") {
		t.Errorf("under a five-minute deadline the request reads %q, want no retry yet", got)
	}
	m.timing.idle = -time.Second
	step(time.Hour)
	if got := statusLine(t, *m); strings.Contains(got, "retry") {
		t.Errorf("with no deadline the request reads %q, want no retry", got)
	}
	m.timing.idle = 0

	// A second request, which hears events that draw nothing.
	m.setTurnState(stateStreaming)
	step(45 * time.Second)
	update(keepaliveMsg{})
	step(3 * time.Second)
	got := statusLine(t, *m)
	if want := "waiting… · model 48s · quiet — keepalives only, last 3s ago · turn "; !strings.HasPrefix(got, want) {
		t.Errorf("a quiet request reads %q, want it to begin %q", got, want)
	}
	step(70 * time.Second)
	if got := statusLine(t, *m); strings.Contains(got, "retry") || !strings.Contains(got, "quiet — keepalives only, last 1m 13s ago") {
		t.Errorf("a quiet request past the silent deadline reads %q, want quiet and no retry", got)
	}
	// After a round of calls the next request is a wait on the model too,
	// and not the calls' phase.
	call := provider.ToolCall{ID: "c1", Name: "read_file", Arguments: `{"path":"main.go"}`}
	update(toolCallsMsg{calls: []provider.ToolCall{call}})
	m.state = stateStreaming
	update(toolResultsMsg{runID: m.agent.RunID(), results: []agent.ToolResult{{Call: call, Result: "package main"}}})
	step(3 * time.Second)
	if got := statusLine(t, *m); strings.Contains(got, "acting…") || !strings.HasPrefix(got, "waiting… · model 3.0s · silent") {
		t.Errorf("the request after a round of calls reads %q, want waiting on the model", got)
	}
}

// `thinking…` is drawn only once reasoning has arrived on the stream, with
// how many reasoning events and how long since the last. With nothing
// arrived the line says nothing arrived, and a request whose first events
// were the answer itself goes straight to `streaming…` without ever saying
// the model thought (docs/interface/surfaces.md#the-input-frame).
func TestWaitingState_NeverClaimsThinkingWithNothingArrived(t *testing.T) {
	m, step, update := waitingModel(t)
	step(5 * time.Second)
	if got := statusLine(t, *m); strings.Contains(got, "thinking") || !strings.Contains(got, "nothing arrived") {
		t.Errorf("with nothing arrived the line reads %q", got)
	}

	for range 38 {
		step(100 * time.Millisecond)
		update(tokenMsg{think: "weighing the flake"})
	}
	step(400 * time.Millisecond)
	if got, want := statusLine(t, *m), "thinking… · model 9.2s · 38 reasoning events, last 0.4s ago · turn 9.2s"; got != want {
		t.Errorf("with reasoning arrived the line reads %q, want %q", got, want)
	}
	update(tokenMsg{text: "The flake is the clock."})
	if got := statusLine(t, *m); !strings.HasPrefix(got, "streaming…") {
		t.Errorf("with prose arriving the line reads %q, want streaming…", got)
	}

	// The next request starts over: nothing has arrived from it.
	m.streaming = ""
	m.setTurnState(stateStreaming)
	step(time.Second)
	if got := statusLine(t, *m); strings.Contains(got, "thinking") || !strings.Contains(got, "nothing arrived") {
		t.Errorf("a fresh request reads %q, want nothing arrived and no thinking", got)
	}
	// A call being written is the answer, drawn or not.
	update(toolDeltaMsg{delta: provider.ToolCallDelta{ID: "c1", Arguments: `{"command":"go`}})
	if got := statusLine(t, *m); !strings.HasPrefix(got, "streaming…") {
		t.Errorf("with a call being written the line reads %q, want streaming…", got)
	}
}

// A gateway's ping is heard and nothing more: the frame calls the stream
// quiet and not silent, the record's quiet stretch counts it as delivered,
// and it draws nothing, stores nothing and leaves the stall's bound alone.
func TestWaitingState_AKeepaliveIsHeardAndDrawsNothing(t *testing.T) {
	m, step, update := waitingModel(t)
	retries := 2
	m.backoff.SetLimit(&retries)
	m.backoff.Next(&provider.Failure{Class: provider.ClassNetwork})
	before := m.backoff.Attempt()

	step(5 * time.Second)
	update(keepaliveMsg{})
	step(2 * time.Second)

	if got := statusLine(t, *m); !strings.Contains(got, "quiet — keepalives only, last 2s ago") {
		t.Errorf("a pinged request reads %q, want quiet", got)
	}
	if r := m.timing.request; r.events != 1 || r.answering || r.reasoning != 0 {
		t.Errorf("the screen heard %+v, want one event that is neither reasoning nor answer", r)
	}
	if got := m.timing.turn.Split(clock()).Quiet.Delivered; got != 1 {
		t.Errorf("the quiet stretch counts %d delivered events, want 1", got)
	}
	if m.streaming != "" || m.thinkIdx != 0 || m.streamDirty {
		t.Error("a keepalive stored or marked something to draw")
	}
	if m.backoff.Attempt() != before {
		t.Errorf("a ping ended the stall: attempt %d, was %d", m.backoff.Attempt(), before)
	}
}
