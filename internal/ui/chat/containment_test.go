package chat

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/provider"
)

// containedModel is a session with both a plain runner and a containment
// wrapper, recording which one each command ran through.
func containedModel(t *testing.T, bare, contained *[]string, status string) Model {
	t.Helper()
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "run it"},
	}
	m := New(msgs, mockStream).
		WithRunner(func(ctx context.Context, cmd string) (string, int) {
			*bare = append(*bare, cmd)
			return "bare", 0
		}).
		WithContainment(Containment{
			Run: func(ctx context.Context, cmd string) (string, int) {
				*contained = append(*contained, cmd)
				return "contained", 0
			},
			Status:    status,
			Mechanism: "bwrap",
			Profile:   "workspace",
			Network:   true,
			Report:    "Command containment:\n  mechanism: bwrap",
		})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.state = stateStreaming
	return m
}

func runExecApproval(t *testing.T, m Model) Model {
	t.Helper()
	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_x", Name: "execute_command", Arguments: `{"command":"echo hi"}`},
	}})
	// The card arrives without the keyboard (S-117, §7b); ctrl+g is what
	// makes its keys — and the consequences it prints beside them — live.
	return handover(t, updated.(Model))
}

func TestConfirmPromptShowsContainmentState(t *testing.T) {
	var bare, contained []string
	m := containedModel(t, &bare, &contained, "contained: bwrap (workspace profile)")
	m = runExecApproval(t, m)

	if m.state != stateConfirmRun {
		t.Fatalf("expected confirm state, got %d", m.state)
	}
	// The containment state rides the card's title rail as a chip, and the
	// profile's network answer is a field of its own (S-101).
	view := m.View().Content
	if !strings.Contains(view, "⛨ bwrap · workspace") {
		t.Fatalf("confirm prompt should carry the containment chip:\n%s", view)
	}
	if !strings.Contains(view, "the workspace profile allows network access") {
		t.Fatalf("confirm prompt should say what the profile allows:\n%s", view)
	}
}

func TestConfirmPromptShowsUnconfinedState(t *testing.T) {
	var bare []string
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "run it"},
	}
	m := New(msgs, mockStream).
		WithRunner(func(ctx context.Context, cmd string) (string, int) {
			bare = append(bare, cmd)
			return "bare", 0
		}).
		WithContainment(Containment{
			Status:  "unconfined — bubblewrap (bwrap) not found on PATH",
			Detail:  "bubblewrap (bwrap) not found on PATH",
			Network: true,
		})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.state = stateStreaming
	m = runExecApproval(t, m)

	// An uncontained action promotes ⚠ UNCONTAINED into the title, explains the
	// missing mechanism, and offers the doctor that expands on it (S-101).
	view := m.View().Content
	for _, want := range []string{"⚠ UNCONTAINED", "bubblewrap (bwrap) not found on PATH", "/sandbox doctor"} {
		if !strings.Contains(view, want) {
			t.Fatalf("uncontained confirm prompt should contain %q:\n%s", want, view)
		}
	}

	// With no containment runner, approval falls through to the plain runner.
	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m = updated.(Model)
	drainCmdDone(t, m, cmd)
	if len(bare) != 1 || bare[0] != "echo hi" {
		t.Fatalf("expected plain runner to run the command, got %v", bare)
	}
}

func drainCmdDone(t *testing.T, m Model, cmd tea.Cmd) cmdDoneMsg {
	t.Helper()
	for _, c := range unwrapBatch(cmd) {
		if msg, ok := c().(cmdDoneMsg); ok {
			return msg
		}
	}
	t.Fatal("expected cmdDoneMsg from the approval cmd")
	return cmdDoneMsg{}
}

func TestApprovedCommandRunsContained(t *testing.T) {
	var bare, contained []string
	m := containedModel(t, &bare, &contained, "contained: bwrap")
	m = runExecApproval(t, m)

	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m = updated.(Model)
	done := drainCmdDone(t, m, cmd)

	if len(contained) != 1 || contained[0] != "echo hi" {
		t.Fatalf("approved assistant command should run contained, got contained=%v bare=%v", contained, bare)
	}
	if len(bare) != 0 {
		t.Fatalf("plain runner must not run the assistant command, got %v", bare)
	}

	updated, _ = m.Update(done)
	m = updated.(Model)
	last := m.Messages()[len(m.Messages())-1]
	if last.Role != provider.RoleTool || !strings.Contains(last.Content, "contained") {
		t.Fatalf("tool result should carry the contained output, got %+v", last)
	}
}

func TestWavedThroughCommandRunsContained(t *testing.T) {
	// Accept-edits/auto-style waves: the config allowlist auto-approves the
	// command without a prompt, and it must still run contained.
	var bare, contained []string
	m := containedModel(t, &bare, &contained, "contained: bwrap")
	m = m.WithCommandAllowlist([]string{"echo"})
	m = m.WithApprovalMode(agent.ModeAuto, nil)
	updated, cmd := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_x", Name: "execute_command", Arguments: `{"command":"echo hi"}`},
	}})
	m = updated.(Model)

	if m.state != stateRunningCmd {
		t.Fatalf("allowlisted command should be waved through, got state %d", m.state)
	}
	drainCmdDone(t, m, cmd)
	if len(contained) != 1 || contained[0] != "echo hi" {
		t.Fatalf("waved-through command should run contained, got contained=%v bare=%v", contained, bare)
	}
}

func TestRunCommandStaysUnconfined(t *testing.T) {
	// /run executes the user's own command; containment applies only to the
	// assistant's commands.
	var bare, contained []string
	m := containedModel(t, &bare, &contained, "contained: bwrap")
	m.state = stateConfirmRun
	m.pendingRun = "echo mine"
	m.pendingApproval = nil
	m = handover(t, m)

	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m = updated.(Model)
	drainCmdDone(t, m, cmd)

	if len(bare) != 1 || bare[0] != "echo mine" {
		t.Fatalf("/run should use the plain runner, got bare=%v contained=%v", bare, contained)
	}
	if len(contained) != 0 {
		t.Fatalf("/run must not be contained, got %v", contained)
	}
}

func TestSandboxSlashCommandShowsReport(t *testing.T) {
	var bare, contained []string
	m := containedModel(t, &bare, &contained, "contained: bwrap")
	handled, out := m.handleSlashCommand("/sandbox")
	if !handled || !strings.Contains(out, "mechanism: bwrap") {
		t.Fatalf("/sandbox should print the doctor report, got %q", out)
	}
	handled, out = m.handleSlashCommand("/sandbox doctor")
	if !handled || !strings.Contains(out, "mechanism: bwrap") {
		t.Fatalf("/sandbox doctor should print the doctor report, got %q", out)
	}

	empty := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream)
	handled, out = empty.handleSlashCommand("/sandbox")
	if !handled || !strings.Contains(out, "not configured") {
		t.Fatalf("/sandbox without containment should say so, got %q", out)
	}

	// Management subcommands need the wired manager (S-063).
	handled, out = m.handleSlashCommand("/sandbox list")
	if !handled || !strings.Contains(out, "unavailable") {
		t.Fatalf("/sandbox list without a manager should say it is unavailable, got %q", out)
	}
}

func TestSandboxSlashCommandDispatchesToManager(t *testing.T) {
	var got [][]string
	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream).
		WithContainment(Containment{
			Report: "proc report",
			Manage: func(args []string) string {
				got = append(got, args)
				return "managed: " + strings.Join(args, " ")
			},
		})

	for input, wantArgs := range map[string]string{
		"/sandbox":             "doctor",
		"/sandbox doctor":      "doctor",
		"/sandbox list":        "list",
		"/sandbox status":      "status",
		"/sandbox destroy abc": "destroy abc",
		"/sandbox prune":       "prune",
	} {
		handled, out := m.handleSlashCommand(input)
		if !handled || out != "managed: "+wantArgs {
			t.Errorf("%s = %q (handled=%v), want dispatch of %q", input, out, handled, wantArgs)
		}
	}
	if len(got) != 6 {
		t.Errorf("manager should have been called 6 times, got %d", len(got))
	}
}
