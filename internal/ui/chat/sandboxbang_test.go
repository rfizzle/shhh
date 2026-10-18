package chat

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/rfizzle/llm"
	"github.com/rfizzle/shhh/internal/ui/golden"
)

// ranWhere records which runner a command went to, and whether it was handed
// a deadline, which is the ceiling an assistant's command gets and the
// reader's never does.
type ranWhere struct {
	host, container []string
	deadline        bool
}

// whereModel is a session with a host runner and a container runner that each
// say which one answered, whose last assistant message carries a block for
// /run, and which has a command ceiling set.
func whereModel(t *testing.T, mechanism string, rec *ranWhere) Model {
	t.Helper()
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "how?"},
		{Role: llm.RoleAssistant, Content: "Try:\n```bash\necho hi\n```"},
	}
	m := New(msgs, mockStream, Wiring{
		Runner: legacyRunner(func(ctx context.Context, cmd string) (string, int) {
			_, d := ctx.Deadline()
			rec.host, rec.deadline = append(rec.host, cmd), rec.deadline || d
			return "host: " + cmd, 0
		}),
		Containment: Containment{
			Mechanism: mechanism,
			Run: legacyRunner(func(ctx context.Context, cmd string) (string, int) {
				_, d := ctx.Deadline()
				rec.container, rec.deadline = append(rec.container, cmd), rec.deadline || d
				return "container: " + cmd, 0
			}),
			TailRun: legacyTailRunner(func(ctx context.Context, cmd string, onLine func(string)) (string, int) {
				_, d := ctx.Deadline()
				rec.container, rec.deadline = append(rec.container, cmd), rec.deadline || d
				onLine("container: " + cmd)
				return "container: " + cmd, 0
			}),
		},
	})
	m.policy.timeout = 30e9
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	return updated.(Model)
}

// typedRun puts a line the reader typed through its card and its run, and
// answers the model it leaves and the row it landed.
func typedRun(t *testing.T, m Model, line string) (Model, entry) {
	t.Helper()
	m = sendText(t, m, line)
	if m.state != stateConfirmRun {
		t.Fatalf("%q should reach the run card, got state %d", line, m.state)
	}
	m = handover(t, m)
	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m = updated.(Model)
	var done cmdDoneMsg
	found := false
	for _, c := range unwrapBatch(cmd) {
		if msg, ok := c().(cmdDoneMsg); ok {
			done, found = msg, true
		}
	}
	if !found {
		t.Fatalf("%q should run, and none did", line)
	}
	updated, _ = m.Update(done)
	m = updated.(Model)
	return m, m.transcript[len(m.transcript)-1]
}

func TestSandboxBangRunsInTheContainer(t *testing.T) {
	for _, line := range []string{"/run", "!echo hi", "!!echo hi"} {
		t.Run(line, func(t *testing.T) {
			var rec ranWhere
			m := whereModel(t, ContainerSandbox, &rec)
			_, row := typedRun(t, m, line)
			if len(rec.container) != 1 || rec.container[0] != "echo hi" || len(rec.host) != 0 {
				t.Fatalf("%q should run once in the container and never on the host: container %q host %q",
					line, rec.container, rec.host)
			}
			if rec.deadline {
				t.Fatalf("%q is the reader's command and carries no ceiling", line)
			}
			if strings.Contains(row.toolResult, "host:") || row.exitCode != 0 {
				t.Fatalf("the row should hold the container's output, got %+v", row)
			}
			if got := stripANSI(m.renderEntry(row, 100)); !strings.Contains(got, ranInContainer) {
				t.Fatalf("the row should say where it ran:\n%s", got)
			}
		})
	}
}

func TestBangStaysOnTheHostWithoutSandbox(t *testing.T) {
	// A bwrap-style mechanism contains the assistant's commands and leaves the
	// reader's alone, as before; so does a session with no mechanism at all.
	for _, mechanism := range []string{"", "bubblewrap"} {
		for _, line := range []string{"/run", "!echo hi", "!!echo hi"} {
			var rec ranWhere
			m := whereModel(t, mechanism, &rec)
			_, row := typedRun(t, m, line)
			if len(rec.host) != 1 || len(rec.container) != 0 {
				t.Fatalf("%q with mechanism %q should stay on the host: host %q container %q",
					line, mechanism, rec.host, rec.container)
			}
			if got := stripANSI(m.renderEntry(row, 100)); strings.Contains(got, ranInContainer) {
				t.Fatalf("a host run must not claim the container:\n%s", got)
			}
		}
	}
}

func TestSandboxBangIsNeverRefusedByTheRequirement(t *testing.T) {
	var rec ranWhere
	m := whereModel(t, ContainerSandbox, &rec)
	m.containment.Required = true
	m.containment.Refusal = "this session requires containment and nothing here can"
	_, row := typedRun(t, m, "!echo hi")
	if len(rec.container) != 1 || strings.Contains(row.toolResult, "requires containment") {
		t.Fatalf("a required session still runs the reader's own command: %+v ran %q", row, rec.container)
	}
}

func TestSandboxBangTellsTheModelWhatItAlwaysDid(t *testing.T) {
	var rec ranWhere
	m := whereModel(t, ContainerSandbox, &rec)
	before := len(m.Messages())
	m, _ = typedRun(t, m, "!echo hi")
	msgs := m.Messages()
	if len(msgs) != before+1 {
		t.Fatalf("a ! run adds exactly the one context message it always did: %d messages, had %d", len(msgs), before)
	}
	last := msgs[len(msgs)-1].Content
	want := commandContextMessage("echo hi", "container: echo hi", 0, nil)
	if last != want {
		t.Fatalf("the context message should be unchanged in shape:\n got %q\nwant %q", last, want)
	}
}

// TestGolden_SandboxBang captures the row of a `!` and a `!!` run in a sandbox
// session, which says where they ran, beside the row of the same `!` on a
// host, which says nothing.
func TestGolden_SandboxBang(t *testing.T) {
	captureGolden(t, "sandbox-bang", "a command the reader typed, run in the sandbox container", goldenWidths, func(width int) []golden.Panel {
		var rec ranWhere
		inBox := whereModel(t, ContainerSandbox, &rec)
		inBox, _ = typedRun(t, inBox, "!echo hi")
		inBox, _ = typedRun(t, inBox, "!!echo hi")
		onHost := whereModel(t, "", &rec)
		onHost, _ = typedRun(t, onHost, "!echo hi")
		return []golden.Panel{
			{Label: "! and !! in a sandbox session · the row says it ran in the container", View: rowsAt(inBox, width)},
			{Label: "! on a host · the row says nothing of where", View: rowsAt(onHost, width)},
		}
	})
}

// rowsAt is the command rows of a model, drawn at a width.
func rowsAt(m Model, width int) string {
	var rows []string
	for _, e := range m.transcript {
		if e.kind == entryCommand {
			rows = append(rows, m.renderEntry(e, width))
		}
	}
	return strings.Join(rows, "\n")
}
