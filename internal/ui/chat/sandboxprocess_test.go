package chat

import (
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/ui/golden"
)

// containerProcessList is the supervisor's listing (process.Supervisor.List)
// for a session whose one process was started in the sandbox container, with
// the pid and the uptime fixed so the capture is stable.
const containerProcessList = "Processes this session owns:\n" +
	"  ticker           running (pid 4127, up 12s)       echo ready; sleep 300\n" +
	"  web              exited (code 0) after 3s         npm test\n" +
	"Manage with the process tool; stop everything by ending the session."

// TestGolden_SandboxProcesses captures /ps in a sandbox session, where a
// started process is held by the exec client and runs in the container. The
// listing is the supervisor's own and says nothing of where a process runs,
// as it does not on a host: the session's containment is the chip's to say.
func TestGolden_SandboxProcesses(t *testing.T) {
	captureGolden(t, "sandbox-processes", "/ps in a sandbox session, listing a process started in the container", goldenWidths, func(width int) []golden.Panel {
		var rec ranWhere
		m := whereModel(t, ContainerSandbox, &rec)
		m.wiring.Processes = Processes{
			Manage:    func([]string) string { return containerProcessList },
			Contained: func() string { return ContainerSandbox },
		}
		m = sendText(t, m, "/ps")
		var rows []string
		for _, e := range m.transcript {
			if strings.Contains(e.text+e.toolResult, "Processes this session owns") {
				rows = append(rows, m.renderEntry(e, width))
			}
		}
		if len(rows) == 0 {
			t.Fatal("/ps left no listing in the transcript")
		}
		return []golden.Panel{{Label: "/ps · a process started in the container", View: strings.Join(rows, "\n")}}
	})
}
