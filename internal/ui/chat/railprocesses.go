package chat

// The rail's PROCESSES and CALLS blocks, read from what the process manager
// lists (manager.go, calls.go), so the rail and the screen it opens cannot
// come to two answers about one process or one call
// (docs/interface/surfaces.md#the-process-manager).

import "github.com/rfizzle/shhh/internal/ui/components"

// inspectorProcesses is the PROCESSES block: every process the session
// started. A failure counts as news only where it came after the last turn
// started; one the reader has already had a turn to see is history.
func (m Model) inspectorProcesses() []components.InspectorProcess {
	var out []components.InspectorProcess
	for _, row := range m.processRows("") {
		out = append(out, components.InspectorProcess{
			Name: row.Name, Word: row.Word(), Running: row.Live(),
			Failed:    row.Failed(),
			Fresh:     row.Failed() && !row.Ended.Before(m.turnStarted),
			Ended:     row.State == components.ManagerExited && row.Code == 0,
			Age:       row.Age,
			Container: row.Where == "container",
		})
	}
	return out
}

// inspectorCalls is the CALLS block: the calls of the round in flight.
func (m Model) inspectorCalls() []components.InspectorCall {
	var out []components.InspectorCall
	for _, row := range m.callRows() {
		out = append(out, components.InspectorCall{
			Name: row.Name, Summary: row.Summary, Age: row.Age,
			Stopped: row.State == components.ManagerStoppedByYou,
		})
	}
	return out
}
