package chat

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/golden"
)

// sandboxCardModel is a sandbox session whose container runs commands through
// run, with the host runner there to be told apart from it.
func sandboxCardModel(t *testing.T, run TailFunc) Model {
	t.Helper()
	m := New([]provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "sync it"},
	}, mockStream, Wiring{
		Runner: legacyRunner(func(context.Context, string) (string, int) { return "host", 0 }),
		Containment: Containment{
			Mechanism: ContainerSandbox,
			Run: legacyRunner(func(context.Context, string) (string, int) {
				return "container", 0
			}),
			TailRun: run,
			Status:  "contained: container",
		},
	})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 48})
	m = updated.(Model)
	m.state = stateStreaming
	return m
}

// finishedInBox is a container whose command prints a line and exits clean.
func finishedInBox() TailFunc {
	return legacyTailRunner(func(_ context.Context, _ string, onLine func(string)) (string, int) {
		onLine("sending incremental file list")
		return "sending incremental file list\nsent 0 bytes", 0
	})
}

// stuckInBox is a container whose command runs until it is stopped.
func stuckInBox(ctx context.Context, _ string, _ func(string)) tools.ExecResult {
	<-ctx.Done()
	return tools.InferExecResult("", -1)
}

// The dry run's row says where it ran, as the real command's does, and the
// run is a call of the table while it is in flight.
func TestApprovalCard_DryRunInASandboxSessionSaysContainer(t *testing.T) {
	m := sandboxCardModel(t, finishedInBox())
	m = execApproval(t, m, "rsync --delete src/ dst/")
	updated, cmd := m.Update(tea.KeyPressMsg{Code: 't', Text: "t"})
	m = updated.(Model)

	live := m.calls.liveCalls()
	if len(live) != 1 || live[0].call.ID != dryRunCallPrefix+"call_x" || live[0].where != ranInContainer {
		t.Fatalf("the dry run should be a call of the table in the container, got %+v", live)
	}
	if rows := m.callRows(); len(rows) != 1 || rows[0].Where != ranInContainer || !strings.Contains(rows[0].Summary, "--dry-run") {
		t.Fatalf("the manager should list the dry run, got %+v", rows)
	}

	done := drainDryRun(t, cmd)
	updated, _ = m.Update(done)
	m = updated.(Model)
	if len(m.calls.liveCalls()) != 0 {
		t.Fatalf("a dry run that returned should leave the manager, got %+v", m.calls.liveCalls())
	}
	last := m.transcript[len(m.transcript)-1]
	if !last.localRun || last.ranIn != ranInContainer {
		t.Fatalf("the row should be local and in the container, got %+v", last)
	}
	if got := m.activityRowFor(last).Outcome; got != "ok · local · container" {
		t.Fatalf("outcome = %q, want %q", got, "ok · local · container")
	}
	// Nothing was decided.
	if m.state != stateOutputFull || m.approval.request == nil {
		t.Fatalf("the card should still be asking, got state %d", m.state)
	}
}

// A stop on the manager ends the dry run alone, with the runner's sentence,
// and the card goes on asking.
func TestApprovalCard_ADryRunStoppedOnTheManagerSaysSo(t *testing.T) {
	m := sandboxCardModel(t, stuckInBox)
	m = execApproval(t, m, "rsync --delete src/ dst/")
	updated, cmd := m.Update(tea.KeyPressMsg{Code: 't', Text: "t"})
	m = updated.(Model)

	id := m.callRows()[0].Key
	if !m.calls.stop(id) {
		t.Fatal("the manager's stop should reach the running dry run")
	}
	if rows := m.callRows(); len(rows) != 1 || rows[0].State != components.ManagerStoppedByYou {
		t.Fatalf("the row should read stopped by you, got %+v", rows)
	}
	done := drainDryRun(t, cmd)
	if done.result.Outcome != tools.ExecStopped || done.end.outcome != components.OutcomeStopped {
		t.Fatalf("the dry run should end stopped, got %+v", done)
	}
	if got := tools.FormatExecResult(done.result); !strings.HasPrefix(got, "error: command was stopped before it finished") {
		t.Fatalf("the runner's sentence, got %q", got)
	}
	updated, _ = m.Update(done)
	m = updated.(Model)
	if len(m.calls.liveCalls()) != 0 || m.approval.request == nil {
		t.Fatalf("the stopped dry run should leave the table and decide nothing")
	}
	if view := m.View().Content; !strings.Contains(view, "command was stopped before it finished") {
		t.Fatalf("the screen should carry the sentence:\n%s", view)
	}
	if got := m.activityRowFor(m.transcript[len(m.transcript)-1]).Outcome; got != "stopped · local · container" {
		t.Fatalf("outcome = %q", got)
	}
}

// TestGolden_DryRunRow captures the row a dry run leaves: in a sandbox
// session it says it ran in the container, stopped or finished, and on a host
// it says only that it stayed local.
func TestGolden_DryRunRow(t *testing.T) {
	captureGolden(t, "dry-run-row", "the row a dry run leaves at a command card", goldenWidths, func(width int) []golden.Panel {
		finish := func(m Model, stop bool) Model {
			m = execApproval(t, m, "rsync --delete src/ dst/")
			updated, cmd := m.Update(tea.KeyPressMsg{Code: 't', Text: "t"})
			m = updated.(Model)
			if stop {
				m.calls.stop(m.callRows()[0].Key)
			}
			updated, _ = m.Update(drainDryRun(t, cmd))
			return updated.(Model)
		}
		inBox := finish(sandboxCardModel(t, finishedInBox()), false)
		stopped := finish(sandboxCardModel(t, stuckInBox), true)
		var bare, contained []string
		onHost := finish(containedModel(t, &bare, &contained, "contained: bwrap"), false)
		return []golden.Panel{
			{Label: "a sandbox session · the dry run ran in the container", View: rowsAt(inBox, width)},
			{Label: "a sandbox session · the dry run stopped from the manager", View: rowsAt(stopped, width)},
			{Label: "a host session · the dry run says only that it stayed local", View: rowsAt(onHost, width)},
		}
	})
}
