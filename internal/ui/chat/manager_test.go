package chat

import (
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/rfizzle/shhh/internal/process"
	"github.com/rfizzle/shhh/internal/ui/golden"
)

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
	if view := managerView(m); !strings.Contains(view, "stopped") {
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
		return []golden.Panel{
			{Label: "as it opens · the pointer on the first process running", View: list},
			{Label: "d · the stop's question", View: asked},
			{Label: "esc, then enter · the process opened onto its output and its errors", View: opened},
			{Label: "the panel it leaves · the way out, on one row", View: m.takeoverPanel(m.contentWidth())},
		}
	})
}
