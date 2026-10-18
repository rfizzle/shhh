package chat

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/process"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/golden"
)

// callsModel is a session whose round is running two calls: a search, and a
// command printing as it goes, each registered as the round registers them.
func callsModel(t *testing.T, width int) (Model, *callTable) {
	t.Helper()
	m, _ := managerModel(t, width)
	m.wiring.Processes = Processes{}
	m.calls = callFixture(t)
	return m, m.calls
}

// callFixture is the round's two calls, started at fixed moments.
func callFixture(t *testing.T) *callTable {
	t.Helper()
	table := &callTable{}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	table.add(ctx, call("s1", tools.SearchName, `{"pattern":"round limit","path":"internal/agent"}`), cancel)
	tail := &commandTail{}
	tail.Set("ok  	internal/agent	3.2s")
	cmdCtx, cmdCancel := context.WithCancel(context.Background())
	t.Cleanup(cmdCancel)
	table.put(cmdCtx, call("c1", tools.ExecCommandName, `{"command":"go test ./..."}`), cmdCancel, tail, "container")
	table.calls[0].started = goldenNow.Add(-41 * time.Second)
	table.calls[1].started = goldenNow.Add(-8 * time.Second)
	return table
}

// A call opens onto its arguments whole and, for a command, what it has
// printed so far; esc backs out to the list.
func TestManager_OpensACall(t *testing.T) {
	m, _ := callsModel(t, 120)
	m = sendText(t, m, "/ps")
	list := managerView(m)
	for _, want := range []string{"processes · nothing running", "tools", "41s", "execute_command", "container", "2 calls running"} {
		if !strings.Contains(list, want) {
			t.Errorf("the manager lacks %q:\n%s", want, list)
		}
	}
	m, _ = onManager(t, m, keyEnter)
	opened := managerView(m)
	for _, want := range []string{"arguments", `"pattern": "round limit"`, "output so far", "this tool shows nothing until it returns"} {
		if !strings.Contains(opened, want) {
			t.Errorf("the opened search lacks %q:\n%s", want, opened)
		}
	}
	m, _ = onManager(t, m, keyEsc, keyDown, keyEnter)
	if opened := managerView(m); !strings.Contains(opened, `"command": "go test ./..."`) || !strings.Contains(opened, "internal/agent") {
		t.Errorf("the opened command lacks its arguments or its output:\n%s", opened)
	}
	m, _ = onManager(t, m, keyEsc)
	if m.screens.manager() == nil || m.screens.manager().Opened != "" {
		t.Fatal("esc over an opened call should go back to the list")
	}
}

// The route through the runtime to a process's stop: /ps typed into the draft
// puts the manager up, enter opens the process onto its output and esc backs
// out, d asks, y stops it off the screen's goroutine, and the row reads
// stopped on the frame that follows.
func TestProgram_TheManagerStopsAProcess(t *testing.T) {
	holdClock(t)
	m, _ := scriptedSession()
	h := newProcessHost()
	m = withProcesses(m, h)
	tm := runProgram(t, m)

	send(tm, "/ps")
	waitForText(t, tm, "2 processes running")
	tm.Send(keyEnter)
	waitForText(t, tm, "listening on :3000")
	tm.Send(keyEsc)
	waitForText(t, tm, "tools · nothing running")
	tm.Send(letter('d'))
	waitForText(t, tm, "stop the process web?")
	tm.Send(letter('y'))
	waitForText(t, tm, "1 process running")
	frameHas(t, finalFrame(t, tm), "cancelled")
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.stopped) != 1 || h.stopped[0] != "web" {
		t.Fatalf("the stop reached %v, want web", h.stopped)
	}
}

// One call of a round stopped from the manager returns as stopped while the
// other runs on: the model is answered for the stopped one as for any call a
// stop caught, the other's result stands, and the round goes on to the next
// request.
func TestProgram_TheManagerStopsOneCallAndTheRoundGoesOn(t *testing.T) {
	m, _ := scriptedSession(
		programTurn{calls: []provider.ToolCall{
			call("s1", tools.SearchName, `{"pattern":"rounds"}`),
			call("s2", tools.SearchName, `{"pattern":"limits"}`),
		}},
		programTurn{text: "The round went on."},
	)
	// Which of the two the pointer opens on is the order they registered in,
	// which is theirs to race for; each answers with its own words where it
	// runs to the end, and says which it was where it was stopped.
	started, stopped, release := make(chan struct{}, 2), make(chan string, 2), make(chan struct{})
	m.agent.SetExecutor(func(ctx context.Context, _ string, args json.RawMessage) (string, error) {
		started <- struct{}{}
		select {
		case <-release:
			return "found " + string(args), nil
		case <-ctx.Done():
			stopped <- string(args)
			return "", ctx.Err()
		}
	})
	tm := runProgram(t, m)

	send(tm, "where are the rounds counted")
	for range 2 {
		select {
		case <-started:
		case <-time.After(factBound):
			t.Fatal("the round's calls never started")
		}
	}
	send(tm, "/ps")
	waitForText(t, tm, "2 calls running")
	tm.Send(letter('d'))
	waitForText(t, tm, "stop the call")
	tm.Send(letter('y'))
	var which string
	select {
	case which = <-stopped:
	case <-time.After(factBound):
		t.Fatal("the stop did not reach the one call")
	}
	waitForText(t, tm, "cancelled by you")
	close(release)
	tm.Send(keyEsc)
	waitForText(t, tm, "The round went on.")
	frameHas(t, finalFrame(t, tm), "The round went on.")

	results := map[string]string{}
	for _, msg := range m.agent.Messages() {
		if msg.Role == provider.RoleTool {
			results[msg.ToolCallID] = msg.Content
		}
	}
	stoppedID, ranID := "s1", "s2"
	if strings.Contains(which, "limits") {
		stoppedID, ranID = "s2", "s1"
	}
	if results[stoppedID] != agent.CancelledResult {
		t.Errorf("the stopped call answers %q, want %q", results[stoppedID], agent.CancelledResult)
	}
	if !strings.HasPrefix(results[ranID], "found ") {
		t.Errorf("the other call answers %q, want its own result", results[ranID])
	}
	if len(stopped) != 0 {
		t.Error("the stop reached both calls")
	}
}

// processHost stands in for the supervisor: three processes started at fixed
// moments — a dev server on the host, a watcher in the container and a test
// run that failed — with their output, and a record of what was stopped.
type processHost struct {
	mu      sync.Mutex
	infos   []process.Info
	stopped []string
	killed  []string
}

func newProcessHost() *processHost {
	return &processHost{infos: []process.Info{
		{Name: "web", Command: "npm run dev", Running: true, Started: goldenNow.Add(-12*time.Minute - 4*time.Second)},
		{Name: "watch", Command: "go run ./cmd/watch", Running: true, Started: goldenNow.Add(-90 * time.Second), Container: true},
		{Name: "tests", Command: "go test ./...", ExitCode: 1, Started: goldenNow.Add(-5 * time.Minute), Ended: goldenNow.Add(-4*time.Minute - 31*time.Second)},
	}}
}

func (h *processHost) snapshot() []process.Info {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]process.Info(nil), h.infos...)
}

func (h *processHost) tail(name string) (string, string, error) {
	return "process " + name + " (stdout): bytes 0-40 of 40 (running)\nlistening on :3000\nGET / 200 4ms",
		"process " + name + " (stderr): bytes 0-31 of 31 (running)\nwarn: a deprecated option", nil
}

func (h *processHost) end(into *[]string) func(string) (string, error) {
	return func(name string) (string, error) {
		h.mu.Lock()
		defer h.mu.Unlock()
		*into = append(*into, name)
		for i := range h.infos {
			if h.infos[i].Name == name {
				h.infos[i].Running, h.infos[i].Stopped, h.infos[i].ExitCode = false, true, -1
				h.infos[i].Ended = goldenNow
			}
		}
		return "process " + name + ": exited (code -1) after 12m4s\n  command: npm run dev", nil
	}
}

// withProcesses wires the host into a model.
func withProcesses(m Model, h *processHost) Model {
	m.wiring.Processes = Processes{
		Manage:   func([]string) string { return "" },
		Snapshot: h.snapshot,
		Tail:     h.tail,
		Stop:     h.end(&h.stopped),
		Kill:     h.end(&h.killed),
	}
	return m
}

// managerModel is a session with the host wired and the clock held.
func managerModel(t *testing.T, width int) (Model, *processHost) {
	t.Helper()
	holdClock(t)
	h := newProcessHost()
	return withProcesses(frameModel(t, width, 40), h), h
}

func managerView(m Model) string {
	return stripANSI(strings.Join(m.managerLines(m.contentWidth(), 30), "\n"))
}

func onManager(t *testing.T, m Model, keys ...tea.KeyPressMsg) (Model, tea.Cmd) {
	t.Helper()
	var cmd tea.Cmd
	for _, k := range keys {
		var next tea.Model
		next, cmd = m.Update(k)
		m = next.(Model)
	}
	return m, cmd
}

func letter(r rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: r, Text: string(r)} }

// /ps opens the manager on the processes; enter opens one onto its output
// and its errors as the process tool's read returns them, esc backs out to
// the list, and esc again leaves.
func TestManager_OpensAProcess(t *testing.T) {
	m, _ := managerModel(t, 120)
	m = sendText(t, m, "/ps")
	if m.state != stateManager || m.screens.manager() == nil {
		t.Fatalf("/ps should put the manager up, state %d", m.state)
	}
	list := managerView(m)
	for _, want := range []string{"/ps", "processes", "web", "running", "12m 04s", "container", "exited 1", "tools · nothing running"} {
		if !strings.Contains(list, want) {
			t.Errorf("the manager lacks %q:\n%s", want, list)
		}
	}
	m, _ = onManager(t, m, keyEnter)
	opened := managerView(m)
	for _, want := range []string{"npm run dev", "stdout", "listening on :3000", "stderr", "warn: a deprecated option"} {
		if !strings.Contains(opened, want) {
			t.Errorf("the opened process lacks %q:\n%s", want, opened)
		}
	}
	m, _ = onManager(t, m, keyEsc)
	if m.state != stateManager || m.screens.manager().Opened != "" {
		t.Fatalf("esc over an opened row should go back to the list, state %d", m.state)
	}
	m, _ = onManager(t, m, keyEsc)
	if m.state == stateManager || m.screens.manager() != nil {
		t.Fatalf("esc over the list should leave the manager, state %d", m.state)
	}
}

// d asks before it stops; y stops the pointed process through the
// supervisor's stop, off the screen's goroutine, and the row reads stopped.
// k on the same question kills instead. A row that has already ended is not
// asked about at all.
func TestManager_StopsAProcess(t *testing.T) {
	m, h := managerModel(t, 120)
	m = sendText(t, m, "/ps")
	m, _ = onManager(t, m, letter('d'))
	if view := managerView(m); !strings.Contains(view, "stop the process web?") || !strings.Contains(view, "[y] stop") || !strings.Contains(view, "[k] kill") {
		t.Fatalf("d should ask with the agent manager's two answers:\n%s", view)
	}
	m, cmd := onManager(t, m, letter('y'))
	if cmd == nil {
		t.Fatal("y should hand the stop to a command")
	}
	msg := cmd()
	if len(h.stopped) != 1 || h.stopped[0] != "web" {
		t.Fatalf("the stop reached %v, want web", h.stopped)
	}
	m, _ = onManager(t, m)
	next, _ := m.Update(msg)
	m = next.(Model)
	if view := managerView(m); !strings.Contains(view, "cancelled") {
		t.Errorf("the stopped process should read stopped:\n%s", view)
	}
	if last := m.transcript[len(m.transcript)-1]; last.kind != entrySystem || !strings.Contains(last.text, "process web: exited") {
		t.Errorf("the stop should be said in the transcript, got %+v", last)
	}

	// The watcher, killed.
	m, _ = onManager(t, m, keyDown, letter('d'))
	if _, cmd = onManager(t, m, letter('k')); cmd == nil {
		t.Fatal("k on the question should hand the kill to a command")
	}
	cmd()
	if len(h.killed) != 1 || h.killed[0] != "watch" {
		t.Fatalf("the kill reached %v, want watch", h.killed)
	}

	// The failed test run has nothing left to stop.
	m, _ = onManager(t, m, keyDown, keyDown, letter('d'))
	if m.screens.manager() == nil || strings.Contains(managerView(m), "stop the process tests?") {
		t.Error("d over an exited process should be silent")
	}
}

// TestGolden_Manager captures the process manager: the list with a process on
// the host, one in the container and one that failed, a process opened onto
// its output, and the stop's question.
func TestGolden_Manager(t *testing.T) {
	captureGolden(t, "manager", "the process manager: what the session left running", goldenWidths, func(width int) []golden.Panel {
		m, _ := managerModel(t, width)
		m = sendText(t, m, "/ps")
		lines := func(m Model) string { return strings.Join(m.managerLines(m.contentWidth(), 22), "\n") }
		list := lines(m)
		m, _ = onManager(t, m, letter('d'))
		asked := lines(m)
		m, _ = onManager(t, m, keyEsc, keyEnter)
		opened := lines(m)
		m, _ = onManager(t, m, keyEsc)
		m.calls = callFixture(t)
		m.calls.stop("s1")
		m, _ = onManager(t, m, keyDown, keyDown, keyDown)
		calls := lines(m)
		m, _ = onManager(t, m, keyDown, keyEnter)
		command := lines(m)
		return []golden.Panel{
			{Label: "as it opens · the pointer on the first process running", View: list},
			{Label: "d · the stop's question", View: asked},
			{Label: "esc, then enter · the process opened onto its output and its errors", View: opened},
			{Label: "a round's two calls · the search stopped by you, the command running", View: calls},
			{Label: "the command opened · its arguments whole and its output so far", View: command},
			{Label: "the panel it leaves · the way out, on one row", View: m.takeoverPanel(m.contentWidth())},
		}
	})
}

// TestGolden_RailProcessesBlock captures the rail's PROCESSES and CALLS
// blocks beside AGENTS' place: a dev server and a watcher running, a test run
// that failed, and a round of two calls, one stopped by you; the same rail
// short of height, where the static blocks go to the foot line before either
// of these loses a row; and a session with no process and no call in flight,
// which draws neither.
func TestGolden_RailProcessesBlock(t *testing.T) {
	captureGolden(t, "rail-processes", "the rail's PROCESSES and CALLS blocks", goldenWidths, func(width int) []golden.Panel {
		m, _ := managerModel(t, width)
		m.turnStarted = goldenNow.Add(-6 * time.Minute)
		m.calls = callFixture(t)
		m.calls.stop("s1")
		railWidth := components.InspectorWidthFor(width)
		rail := m.inspectorData()
		bare := frameModel(t, width, 40)
		return []golden.Panel{
			{Label: "three processes and a round of two calls", View: rail.View(railWidth, 0)},
			{Label: "the same rail in 11 rows · what is static goes first", View: rail.View(railWidth, 11)},
			{Label: "no process started and no call running · neither block", View: bare.inspectorData().View(railWidth, 0)},
		}
	})
}
