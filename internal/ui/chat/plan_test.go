package chat

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/provider"
)

// planModel is a ready model sitting in plan mode with a streamed planning
// response about to complete.
func planModel(t *testing.T, stream StreamFunc) Model {
	t.Helper()
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "plan the change"},
	}
	m := New(msgs, stream)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)
	m.mode = agent.ModePlan
	m.state = stateStreaming
	m.streaming = "1. edit a.go\n2. run tests"
	return m
}

// recordingStream captures the message list of each stream request.
func recordingStream(captured *[]provider.Message) StreamFunc {
	return func(msgs []provider.Message) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		*captured = msgs
		ch := make(chan provider.StreamEvent)
		close(ch)
		_, cancel := context.WithCancel(context.Background())
		return ch, cancel, nil
	}
}

// driveStream runs every cmd in a possibly-batched tea.Cmd so the stream
// request actually fires.
func driveStream(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected a command to run")
	}
	for _, c := range unwrapBatch(cmd) {
		c()
	}
}

func TestPlan_ApprovalPromptAfterPlanningResponse(t *testing.T) {
	m := planModel(t, mockStream)
	updated, _ := m.Update(doneMsg{})
	m = updated.(Model)

	if m.state != statePlanApprove {
		t.Fatalf("a completed planning response should enter the plan-approval prompt, got state %d", m.state)
	}
	view := m.View().Content
	if !strings.Contains(view, "Plan ready") {
		t.Fatalf("view should show the plan-approval card, got:\n%s", view)
	}
	for _, opt := range planApproveOptions {
		if !strings.Contains(view, opt.Label) {
			t.Errorf("view should list option %q", opt.Label)
		}
	}
}

func TestPlan_EmptyResponseSkipsPrompt(t *testing.T) {
	m := planModel(t, mockStream)
	m.streaming = ""
	updated, _ := m.Update(doneMsg{})
	m = updated.(Model)
	if m.state != stateInput {
		t.Fatalf("an empty response should not offer plan approval, got state %d", m.state)
	}
}

func TestPlan_NonPlanModeSkipsPrompt(t *testing.T) {
	m := planModel(t, mockStream)
	m.mode = agent.ModeManual
	updated, _ := m.Update(doneMsg{})
	m = updated.(Model)
	if m.state != stateInput {
		t.Fatalf("modes other than plan should finish the turn normally, got state %d", m.state)
	}
}

func TestPlan_SteeringTakesPrecedenceOverPrompt(t *testing.T) {
	m := planModel(t, mockStream)
	m.steering = []string{"also cover the CLI"}
	updated, _ := m.Update(doneMsg{})
	m = updated.(Model)
	if m.state != stateStreaming {
		t.Fatalf("queued steering should continue planning, got state %d", m.state)
	}
	if m.mode != agent.ModePlan {
		t.Fatal("steering must not leave plan mode")
	}
}

func TestPlan_ApproveExecutesInChosenMode(t *testing.T) {
	var captured []provider.Message
	m := planModel(t, recordingStream(&captured))
	updated, _ := m.Update(doneMsg{})
	m = updated.(Model)

	m = handover(t, m)
	updated, cmd := m.Update(tea.KeyPressMsg{Code: '1', Text: "1"})
	m = updated.(Model)
	if m.mode != agent.ModeAcceptEdits {
		t.Fatalf("option 1 should switch to accept-edits, got %v", m.mode)
	}
	if m.state != stateStreaming {
		t.Fatalf("approval should continue straight into execution, got state %d", m.state)
	}
	msgs := m.Messages()
	last := msgs[len(msgs)-1]
	if last.Role != provider.RoleUser || last.Content != planApprovedMessage {
		t.Fatalf("approval should append the execution turn, got %+v", last)
	}
	// The plan itself stays in context: same session, same conversation.
	foundPlan := false
	for _, msg := range msgs {
		if msg.Role == provider.RoleAssistant && strings.Contains(msg.Content, "edit a.go") {
			foundPlan = true
		}
	}
	if !foundPlan {
		t.Fatal("the plan must remain in the conversation after approval")
	}
	driveStream(t, cmd)
	if len(captured) == 0 {
		t.Fatal("approval should open the next stream request")
	}
	if strings.Contains(captured[0].Content, "# Plan mode") {
		t.Fatal("execution requests must not carry the planning instructions")
	}
	noted := false
	for _, e := range m.transcript {
		if e.kind == entrySystem && strings.Contains(e.text, "Plan approved — executing in accept-edits mode") {
			noted = true
		}
	}
	if !noted {
		t.Fatal("transcript should note the approval and chosen mode")
	}
}

func TestPlan_ApproveOtherModes(t *testing.T) {
	cases := []struct {
		key  rune
		want agent.Mode
	}{
		{'2', agent.ModeAuto},
		{'3', agent.ModeManual},
	}
	for _, c := range cases {
		m := planModel(t, mockStream)
		updated, _ := m.Update(doneMsg{})
		m = updated.(Model)
		m = handover(t, m)
		updated, _ = m.Update(tea.KeyPressMsg{Code: c.key, Text: string(c.key)})
		m = updated.(Model)
		if m.mode != c.want || m.state != stateStreaming {
			t.Errorf("option %c: mode %v state %d, want mode %v streaming", c.key, m.mode, m.state, c.want)
		}
	}
}

func TestPlan_KeepPlanningReturnsToInput(t *testing.T) {
	m := planModel(t, mockStream)
	updated, _ := m.Update(doneMsg{})
	m = updated.(Model)

	m = handover(t, m)
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)
	if m.state != stateInput {
		t.Fatalf("esc should dismiss to keep planning, got state %d", m.state)
	}
	if m.mode != agent.ModePlan {
		t.Fatal("keep planning must stay in plan mode")
	}
	found := false
	for _, e := range m.transcript {
		if e.kind == entrySystem && strings.Contains(e.text, "Keep planning") {
			found = true
		}
	}
	if !found {
		t.Fatal("transcript should note the keep-planning choice")
	}
}

func TestPlan_RejectStaysInPlanMode(t *testing.T) {
	m := planModel(t, mockStream)
	updated, _ := m.Update(doneMsg{})
	m = updated.(Model)

	m = handover(t, m)
	updated, _ = m.Update(tea.KeyPressMsg{Code: '5', Text: "5"})
	m = updated.(Model)
	if m.state != stateInput || m.mode != agent.ModePlan {
		t.Fatalf("reject should return to input in plan mode, got state %d mode %v", m.state, m.mode)
	}
	found := false
	for _, e := range m.transcript {
		if e.kind == entrySystem && strings.Contains(e.text, "Plan rejected") {
			found = true
		}
	}
	if !found {
		t.Fatal("transcript should note the rejection")
	}
}

func TestPlan_NavigationAndEnterSelect(t *testing.T) {
	m := planModel(t, mockStream)
	updated, _ := m.Update(doneMsg{})
	m = updated.(Model)

	// k at the top stays put.
	m = handover(t, m)
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	m = updated.(Model)
	if m.planChoice != 0 {
		t.Fatalf("k at the top should stay at 0, got %d", m.planChoice)
	}
	for range 10 {
		updated, _ = m.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
		m = updated.(Model)
	}
	if m.planChoice != len(planApproveOptions)-1 {
		t.Fatalf("j should clamp at the last option, got %d", m.planChoice)
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	m = updated.(Model)
	if m.planChoice != 2 {
		t.Fatalf("expected focus on option 3, got %d", m.planChoice)
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if m.mode != agent.ModeManual || m.state != stateStreaming {
		t.Fatalf("enter should select the focused option (manual execution), got mode %v state %d", m.mode, m.state)
	}
}

func TestPlan_RequestStreamInjectsInstructions(t *testing.T) {
	var captured []provider.Message
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, recordingStream(&captured))
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)
	m.mode = agent.ModePlan

	updated, cmd := m.sendUserMessage("plan the change")
	m = updated.(Model)
	driveStream(t, cmd)

	if len(captured) == 0 || !strings.Contains(captured[0].Content, "# Plan mode") {
		t.Fatal("plan-mode requests should carry the planning instructions in the system prompt")
	}
	if strings.Contains(m.Messages()[0].Content, "# Plan mode") {
		t.Fatal("the stored conversation's system prompt must stay untouched")
	}
}

func TestPlan_InspectionCommandRunsWithoutPrompt(t *testing.T) {
	var ran []string
	m := execModel(t, &ran)
	m.mode = agent.ModePlan

	updated, cmd := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_i", Name: "execute_command", Arguments: `{"command":"git status"}`},
	}})
	m = updated.(Model)
	if m.state != stateRunningCmd {
		t.Fatalf("plan mode should run inspection commands without a prompt, got state %d", m.state)
	}
	updated, _ = m.Update(driveCmdDone(t, cmd))
	m = updated.(Model)
	if len(ran) != 1 || ran[0] != "git status" {
		t.Fatalf("expected git status to run, got %v", ran)
	}
	found := false
	for _, e := range m.transcript {
		if e.kind == entrySystem && strings.Contains(e.text, "Auto-approved (plan mode inspection): git status") {
			found = true
		}
	}
	if !found {
		t.Fatal("transcript should note the inspection auto-approval")
	}
}

func TestPlan_SlashPlanSave(t *testing.T) {
	t.Chdir(t.TempDir())
	m := runCapableModel("1. edit a.go\n2. run tests")

	handled, result := m.handleSlashCommand("/plan save my plan")
	if !handled || !strings.Contains(result, "Plan saved to") {
		t.Fatalf("/plan save should save, got %q", result)
	}
	path := filepath.Join(".shhh", "plans", "my-plan.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected %s to exist: %v", path, err)
	}
	if !strings.Contains(string(data), "edit a.go") {
		t.Fatalf("saved plan should contain the plan text, got %q", data)
	}

	// A default name is generated when none is given.
	_, result = m.handleSlashCommand("/plan save")
	if !strings.Contains(result, "Plan saved to") {
		t.Fatalf("/plan save without a name should still save, got %q", result)
	}

	// Bare /plan is the checklist now; with no plan approved it says
	// so, and still names the save form it replaced as the bare command.
	_, bare := m.handleSlashCommand("/plan")
	if !strings.Contains(bare, "No approved plan is running") || !strings.Contains(bare, "/plan save [name]") {
		t.Fatalf("/plan with no approved plan should say so and name the forms, got %q", bare)
	}
}

func TestPlan_SlashPlanSaveWithoutPlan(t *testing.T) {
	t.Chdir(t.TempDir())
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream)
	_, result := m.handleSlashCommand("/plan save x")
	if !strings.Contains(result, "No plan to save yet") {
		t.Fatalf("saving with no assistant response should refuse, got %q", result)
	}
}

func TestSanitizePlanName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"my-plan", "my-plan"},
		{"my plan", "my-plan"},
		{"../../etc/passwd", "etc-passwd"},
		{"Plan_2.0", "Plan_2.0"},
	}
	for _, c := range cases {
		if got := sanitizePlanName(c.in); got != c.want {
			t.Errorf("sanitizePlanName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := sanitizePlanName("///"); !strings.HasPrefix(got, "plan-") {
		t.Errorf("all-unsafe name should fall back to a timestamp, got %q", got)
	}
}

// structuredPlan is the shape internal/prompt asks plan mode to emit.
const structuredPlan = `## Plan: make the round limit recoverable

1. Locate the round accounting
   files: internal/agent/loop.go
   action: read
2. Add a RoundsExhausted sentinel
   files: internal/agent/errors.go
   action: create
   note: new type, no signature changes
3. Offer more rounds in the chat model
   files: internal/ui/chat/model.go
`

// plannedModel drives a planning response to completion so the card is armed
// with it, in dir as the work tree.
func plannedModel(t *testing.T, response string) Model {
	t.Helper()
	m := planModel(t, mockStream)
	m.streaming = response
	updated, _ := m.Update(doneMsg{})
	m = updated.(Model)
	if m.state != statePlanApprove {
		t.Fatalf("expected the plan card, got state %d", m.state)
	}
	// The card arrives without the keyboard; the handover is what
	// makes its keys mean anything.
	return handover(t, m)
}

func TestPlanCard_StepsCarryTheFilesTheyTouch(t *testing.T) {
	m := plannedModel(t, structuredPlan)
	view := m.View().Content
	for _, want := range []string{
		"Plan · make the round limit recoverable",
		"3 steps",
		"1 Locate the round accounting",
		"internal/agent/loop.go",
		"2 Add a RoundsExhausted sentinel",
		"internal/agent/errors.go · new type, no signature changes",
		"3 Offer more rounds in the chat model",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the plan card should carry %q:\n%s", want, view)
		}
	}
}

func TestPlanCard_StepIntentComesFromTheAction(t *testing.T) {
	m := plannedModel(t, structuredPlan)
	view := m.View().Content
	for _, want := range []string{"read only", "✎ creates 1 file", "✎ edits 1 file"} {
		if !strings.Contains(view, want) {
			t.Errorf("the plan card should rate the step %q:\n%s", want, view)
		}
	}
}

func TestPlanCard_SummaryIsComputedFromTheSteps(t *testing.T) {
	m := plannedModel(t, structuredPlan)
	view := m.View().Content
	// The read step's file is not a write target, so two files are touched.
	for _, want := range []string{"2 files touched", "no deletes", "no network"} {
		if !strings.Contains(view, want) {
			t.Errorf("the summary should state %q:\n%s", want, view)
		}
	}
}

func TestPlanCard_SummaryReadsTheSameGitCheckAsApprovals(t *testing.T) {
	dir := chdir(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if err := os.WriteFile(filepath.Join(dir, "tracked.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "tracked.go"}} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git setup failed: %v (%s)", err, out)
		}
	}

	tracked := "1. Change it\n   files: tracked.go\n   action: edit\n"
	both := tracked + "2. And a new one\n   files: fresh.go\n   action: create\n"

	m := planModel(t, mockStream)
	m.tracker = changeset.NewTracker(dir)

	m.streaming = tracked
	updated, _ := m.Update(doneMsg{})
	armed := updated.(Model)
	if view := armed.View().Content; !strings.Contains(view, "reversible") {
		t.Errorf("a wholly tracked plan is reversible:\n%s", view)
	}
	if armed.planDetail != "every file is tracked in git" {
		t.Errorf("the card should say why: %q", armed.planDetail)
	}

	m.state, m.streaming = stateStreaming, both
	updated, _ = m.Update(doneMsg{})
	armed = updated.(Model)
	if view := armed.View().Content; !strings.Contains(view, "partly reversible") {
		t.Errorf("a plan naming an untracked file is only partly reversible:\n%s", view)
	}
	if armed.planDetail != "1 of 2 files tracked in git" {
		t.Errorf("the card should count them: %q", armed.planDetail)
	}
}

func TestPlanCard_OutsideARepositoryNothingIsClaimed(t *testing.T) {
	chdir(t)
	m := plannedModel(t, structuredPlan)
	if view := m.View().Content; !strings.Contains(view, "not reversible") {
		t.Errorf("outside a work tree the card says so rather than claiming undo:\n%s", view)
	}
}

func TestPlanCard_OptionsNameTheModeTheyEnter(t *testing.T) {
	m := plannedModel(t, structuredPlan)
	view := m.View().Content
	// Accepting a plan is a mode change, so every execution option says which.
	for _, want := range []string{"accept-edits mode", "auto mode", "manual approvals"} {
		if !strings.Contains(view, want) {
			t.Errorf("the options should name the mode %q:\n%s", want, view)
		}
	}
}

func TestPlanCard_OnlyTheFocusedOptionExplainsItself(t *testing.T) {
	m := plannedModel(t, structuredPlan)
	if view := m.View().Content; !strings.Contains(view, planApproveOptions[0].Desc) {
		t.Fatalf("the focused option should explain itself:\n%s", view)
	}
	for _, opt := range planApproveOptions[1:] {
		if strings.Contains(m.View().Content, opt.Desc) {
			t.Errorf("an unfocused option must not explain itself: %q", opt.Desc)
		}
	}
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	view := updated.(Model).View().Content
	if !strings.Contains(view, planApproveOptions[1].Desc) {
		t.Errorf("moving the focus should move the explanation:\n%s", view)
	}
	if strings.Contains(view, planApproveOptions[0].Desc) {
		t.Errorf("only one option explains itself at a time:\n%s", view)
	}
}

func TestPlanCard_UnstructuredPlanStillRenders(t *testing.T) {
	prose := "I'd add a sentinel error to the agent package and return it from the round loop."
	m := plannedModel(t, prose)
	view := m.View().Content
	if !strings.Contains(view, "Plan ready") {
		t.Errorf("a plan with no structure still gets a card:\n%s", view)
	}
	if !strings.Contains(view, "add a sentinel error") {
		t.Errorf("the prose should render:\n%s", view)
	}
	if !strings.Contains(view, planApproveOptions[0].Label) {
		t.Errorf("the options belong below it:\n%s", view)
	}
	// Nothing was parsed, so nothing is claimed about the radius.
	for _, unwanted := range []string{"files touched", "no deletes", "reversible"} {
		if strings.Contains(view, unwanted) {
			t.Errorf("an unparsed plan must not be priced, found %q:\n%s", unwanted, view)
		}
	}
}

func TestPlanCard_SaveKeyWritesThePlanAndKeepsTheCard(t *testing.T) {
	t.Chdir(t.TempDir())
	m := plannedModel(t, structuredPlan)

	updated, _ := m.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	m = updated.(Model)
	if m.state != statePlanApprove {
		t.Fatalf("saving is not a decision and must not answer one, got state %d", m.state)
	}
	saved := ""
	for _, e := range m.transcript {
		if e.kind == entrySystem && strings.HasPrefix(e.text, "Plan saved to ") {
			saved = strings.TrimPrefix(e.text, "Plan saved to ")
		}
	}
	if saved == "" {
		t.Fatalf("[s] should note where the plan was saved, got transcript %+v", m.transcript)
	}
	data, err := os.ReadFile(saved)
	if err != nil {
		t.Fatalf("expected %s to exist: %v", saved, err)
	}
	if !strings.Contains(string(data), "RoundsExhausted sentinel") {
		t.Errorf("the saved file should hold the plan as written, got %q", data)
	}
	// /plan save is the same path and is unchanged by the key.
	handled, result := m.handleSlashCommand("/plan save named")
	if !handled || !strings.Contains(result, filepath.Join(".shhh", "plans", "named.md")) {
		t.Errorf("/plan save should still write its own name, got %q", result)
	}
}

func TestPlanCard_AnsweringTheCardDropsTheArmedPlan(t *testing.T) {
	m := plannedModel(t, structuredPlan)
	if !m.planDoc.Structured() {
		t.Fatal("the plan should be armed while the card is up")
	}
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if got := updated.(Model).planDoc; got.Structured() || got.Text != "" {
		t.Errorf("answering the card should drop the armed plan, got %+v", got)
	}
}
