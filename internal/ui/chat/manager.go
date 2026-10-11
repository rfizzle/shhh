package chat

// The process manager (docs/interface/surfaces.md#the-process-manager): `/ps`,
// and the rail's PROCESSES and CALLS headings. It lists what the session left
// running — the processes it started, and the calls of the round in flight —
// opens one onto what it is doing, and stops it without stopping the turn,
// the way the agent manager does for a child.
//
// A process is stopped through the supervisor, as the process tool's stop
// does it; nothing is said to the model, which reads the process as exited
// the next time it asks. A call is stopped through its own context, and the
// model is answered for it as for any call a stop caught.

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// managerStoppedMsg is what a stop or kill run off the screen's goroutine
// came to: the supervisor's own first line about the process after it.
type managerStoppedMsg struct{ said string }

// managerRows is what the screen lists, read again at every paint. Only the
// row being read — the one opened, or under the pointer — carries its
// output: the others' would be read and thrown away on every frame.
func (m Model) managerRows(reading string) []components.ManagerRow {
	return m.processRows(reading)
}

// processRows is every process the session started, as the manager and the
// rail read it, with the output of the one named reading.
func (m Model) processRows(reading string) []components.ManagerRow {
	p := m.wiring.Processes
	if p.Snapshot == nil {
		return nil
	}
	var rows []components.ManagerRow
	for _, info := range p.Snapshot() {
		row := components.ManagerRow{
			Section: components.ManagerProcesses, Key: info.Name, Name: info.Name,
			Summary: firstLine(info.Command), Where: "host",
		}
		if info.Container {
			row.Where = "container"
		}
		switch {
		case info.Running:
			row.Age = clock().Sub(info.Started)
		case info.Stopped:
			row.State, row.Age = components.ManagerStopped, info.Ended.Sub(info.Started)
		default:
			row.State, row.Code, row.Age = components.ManagerExited, info.ExitCode, info.Ended.Sub(info.Started)
		}
		if p.Tail != nil && info.Name == reading {
			row.Output, row.Errors, _ = p.Tail(info.Name)
		}
		rows = append(rows, row)
	}
	return rows
}

// openManager puts the manager up, on the first row that can be stopped.
func (m Model) openManager() (tea.Model, tea.Cmd) {
	screen := &components.ManagerScreen{Rows: m.managerRows("")}
	for i, r := range screen.Rows {
		if r.Live() {
			screen.Focus = i
			break
		}
	}
	m.screens = m.screens.with(stateManager, screen)
	m.enterSurface(stateManager)
	return m, nil
}

// managerLines draws the screen over the rows as they read at this paint, so
// a process that exits while it is up reads exited on the next frame.
func (m Model) managerLines(width, height int) []string {
	screen := m.screens.manager()
	if screen == nil {
		return nil
	}
	screen.Rows = m.managerRows(screen.Reading())
	screen.SetSize(width, height)
	return strings.Split(screen.View(width), "\n")
}

// updateManager routes keys while the screen is up, and carries out a stop
// the screen's confirm let through.
func (m Model) updateManager(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	screen := m.screens.manager()
	if screen == nil {
		return m.closeManager()
	}
	screen.Rows = m.managerRows(screen.Reading())
	done, result := screen.Update(msg)
	if done {
		return m.closeManager()
	}
	if result.Act == components.ManagerNothing {
		return m, nil
	}
	return m, m.stopProcess(result)
}

// stopProcess ends a process off the UI goroutine, since the supervisor waits
// for it to be gone — two seconds of grace on a host, longer for the two
// legs of a stop in a container.
func (m Model) stopProcess(result components.ManagerResult) tea.Cmd {
	end := m.wiring.Processes.Stop
	if result.Act == components.ManagerKill {
		end = m.wiring.Processes.Kill
	}
	if end == nil {
		return nil
	}
	name := result.Row.Key
	return func() tea.Msg {
		said, err := end(name)
		if err != nil {
			return managerStoppedMsg{said: err.Error()}
		}
		return managerStoppedMsg{said: firstLine(said)}
	}
}

// finishManagerStop says in the transcript what the stop came to. It is the
// reader's own row: the model is not told, and reads the process as exited
// the next time it asks the process tool.
func (m Model) finishManagerStop(msg managerStoppedMsg) (tea.Model, tea.Cmd) {
	return m.systemNotice(msg.said)
}

// closeManager hands the screen back to the turn, the way its own esc does
// and the way the rail cell that opened it does.
func (m Model) closeManager() (tea.Model, tea.Cmd) {
	m.screens = m.screens.without(stateManager)
	m.leaveSurface()
	m.syncViewport()
	return m, nil
}

// managerShowing is the door's reading of whether its surface is up.
func managerShowing(m Model) any {
	if m.state != stateManager || m.screens.manager() == nil {
		return nil
	}
	return m.screens.manager()
}

// renderManagerHint is the one line the screen leaves where the draft box
// was: the way out and nothing else.
func (m Model) renderManagerHint() string {
	return sty.SystemMsg.Render("ps · ") + segAs(keys.Agent.Back, "back to the prompt").render()
}
