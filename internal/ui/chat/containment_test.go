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
	// The card arrives without the keyboard; the handover is what
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
	// profile's network answer is a field of its own.
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
	// missing mechanism, and offers the doctor that expands on it.
	view := m.View().Content
	for _, want := range []string{"⚠ UNCONTAINED", "/sandbox doctor"} {
		if !strings.Contains(view, want) {
			t.Fatalf("uncontained confirm prompt should contain %q:\n%s", want, view)
		}
	}
	// At this height the blast-radius block runs one row past the panel; the
	// card counts what the bound swallowed and shift+↓ brings it into view —
	// nothing is merely clipped (docs/interface/surfaces.md#the-approval-card).
	if !strings.Contains(view, "more lines · shift+↓") {
		t.Fatalf("the bounded card should count its scrolled-off rows:\n%s", view)
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModShift})
	m = updated.(Model)
	if view := m.View().Content; !strings.Contains(view, "bubblewrap (bwrap) not found on PATH") {
		t.Fatalf("shift+↓ should bring the missing-mechanism row into view:\n%s", view)
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

	// Management subcommands need the wired manager.
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

// A session told to require containment on a host that has none refuses the
// assistant's commands, and refuses them before the card: there is nothing to
// decide when the answer is the same whichever key the reader presses, and
// the model is the one that has to read what happened.
func TestRequiredContainmentRefusesWithoutACard(t *testing.T) {
	const refusal = "error: this session requires containment and no mechanism is in force: " +
		"bubblewrap (bwrap) not found on PATH\n  sudo apt install bubblewrap"
	var bare []string
	m := New([]provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "run it"},
	}, mockStream).
		WithRunner(func(ctx context.Context, cmd string) (string, int) {
			bare = append(bare, cmd)
			return "bare", 0
		}).
		WithContainment(Containment{
			Status:  "unconfined — bubblewrap (bwrap) not found on PATH",
			Detail:  "bubblewrap (bwrap) not found on PATH",
			Network: true,
			Refusal: refusal,
		})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.state = stateStreaming

	updated, _ = m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_x", Name: "execute_command", Arguments: `{"command":"echo hi"}`},
	}})
	m = updated.(Model)

	if m.state == stateConfirmRun {
		t.Fatalf("a refused command must not draw a card:\n%s", m.View().Content)
	}
	if len(bare) != 0 {
		t.Fatalf("nothing may run: %v", bare)
	}
	var result string
	for _, msg := range m.Messages() {
		if msg.Role == provider.RoleTool {
			result = msg.Content
		}
	}
	if !strings.Contains(result, "requires containment") {
		t.Fatalf("the model should read the refusal as the call's result, got %q", result)
	}
	// And the fix for this host rides with it: the reader is the one who can
	// act on it, and the doctor is where that wording lives.
	if !strings.Contains(result, "sudo apt install bubblewrap") {
		t.Fatalf("the refusal should carry the doctor's fix, got %q", result)
	}
	// And "unconfined" is not the whole answer where a requirement stands:
	// nothing of the assistant's is going to run at all.
	if text, _ := m.statusCommand(); !strings.Contains(text, "the assistant's commands are refused") {
		t.Fatalf("/status should say the commands are refused:\n%s", text)
	}
}

// A required session that has its mechanism says so where it is read: the
// chip on the card, and the same clause in `/status` for a terminal with no
// room for a card's title rail.
func TestRequiredContainmentSaysSoOnTheChipAndInStatus(t *testing.T) {
	var bare, contained []string
	m := containedModel(t, &bare, &contained, "contained: bwrap (workspace profile)")
	c := m.containment
	c.Required = true
	m = m.WithContainment(c)
	m = runExecApproval(t, m)

	if view := m.View().Content; !strings.Contains(view, "required · bwrap") {
		t.Fatalf("the chip should say the containment was required:\n%s", view)
	}
	text, _ := m.statusCommand()
	if !strings.Contains(text, "required · bwrap") {
		t.Fatalf("/status should say the same:\n%s", text)
	}
}

// Without the knob the chip is the mechanism alone, and a session with no
// containment wiring claims neither state.
func TestContainmentStatusWithoutTheKnob(t *testing.T) {
	var bare, contained []string
	m := containedModel(t, &bare, &contained, "contained: bwrap (workspace profile)")
	text, _ := m.statusCommand()
	if strings.Contains(text, "required") {
		t.Fatalf("/status must not claim a requirement nobody made:\n%s", text)
	}
	if !strings.Contains(text, "bwrap · workspace") {
		t.Fatalf("/status should name the mechanism and the profile:\n%s", text)
	}

	empty := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream)
	if text, _ := empty.statusCommand(); strings.Contains(text, "Containment") {
		t.Fatalf("a session with no containment wiring says nothing:\n%s", text)
	}
}

// /run is the user's own command: the requirement is about the assistant's,
// and a session that refuses those still runs this one.
func TestRequiredContainmentNeverRefusesTheUsersOwnCommand(t *testing.T) {
	var bare []string
	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream).
		WithRunner(func(ctx context.Context, cmd string) (string, int) {
			bare = append(bare, cmd)
			return "bare", 0
		}).
		WithContainment(Containment{
			Status:  "unconfined — bubblewrap (bwrap) not found on PATH",
			Detail:  "bubblewrap (bwrap) not found on PATH",
			Network: true,
			Refusal: "error: this session requires containment and no mechanism is in force",
		})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.state = stateConfirmRun
	m.pendingRun = "echo mine"
	m.pendingApproval = nil
	m = handover(t, m)

	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m = updated.(Model)
	drainCmdDone(t, m, cmd)
	if len(bare) != 1 || bare[0] != "echo mine" {
		t.Fatalf("/run is never contained and never refused, got %v", bare)
	}
}
