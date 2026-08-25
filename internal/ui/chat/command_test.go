package chat

// Live commands while the agent works (S-087).

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/subagent"
)

// workingModel is a model mid-turn, the state sub-agents only ever run in.
func workingModel(t *testing.T) Model {
	t.Helper()
	return steeringModel(t, mockStream)
}

func TestLiveCommand_RunsWhileWorking(t *testing.T) {
	m := workingModel(t)
	before := len(m.Messages())

	m = sendText(t, m, "/stats")

	if m.state != stateStreaming {
		t.Fatalf("a live command must not disturb the running turn, got state %d", m.state)
	}
	if len(m.steering) != 0 {
		t.Fatalf("a command is not steering text, got %v", m.steering)
	}
	if len(m.Messages()) != before {
		t.Fatal("a live command must not touch the conversation")
	}
	if !transcriptContains(m, "Context") {
		t.Fatal("expected the /stats report in the transcript")
	}
}

func TestLiveCommand_ModeChangeWhileWorking(t *testing.T) {
	m := workingModel(t)
	m = sendText(t, m, "/mode accept-edits")

	if m.mode.String() != "accept-edits" {
		t.Fatalf("mode should change mid-turn (Shift+Tab already does), got %s", m.mode)
	}
	if m.state != stateStreaming {
		t.Fatalf("turn state disturbed, got %d", m.state)
	}
}

func TestIdleOnlyCommand_RefusedWithReason(t *testing.T) {
	m := workingModel(t)
	before := len(m.Messages())

	m = sendText(t, m, "/clear")

	if len(m.Messages()) != before {
		t.Fatal("/clear must not touch the conversation while the agent works")
	}
	if len(m.steering) != 0 {
		t.Fatalf("a refused command must not queue as steering, got %v", m.steering)
	}
	notice := m.transcript[len(m.transcript)-1]
	if notice.kind != entrySystem || !strings.Contains(notice.text, "/clear") ||
		!strings.Contains(notice.text, "starts a new conversation") {
		t.Fatalf("expected a notice naming the command and why it waits, got %+v", notice)
	}
}

func TestIdleOnlyCommand_RunsOnceIdle(t *testing.T) {
	m := workingModel(t)
	updated, _ := m.Update(doneMsg{})
	m = updated.(Model)

	m = sendText(t, m, "/clear")
	if len(m.transcript) != 1 || !strings.Contains(m.transcript[0].text, "Started a new conversation") {
		t.Fatalf("/clear should run once the turn ended, got %+v", m.transcript)
	}
}

func TestCompletionMenu_OpensWhileWorking(t *testing.T) {
	m := workingModel(t)
	m.input.SetValue("/sta")
	m.syncCompletions()

	if !m.completionActive() {
		t.Fatal("the completion menu should open while the agent works")
	}
	if m.completions[0].name != "/stats" {
		t.Fatalf("expected /stats first, got %q", m.completions[0].name)
	}
}

func TestCompletionMenu_HidesIdleOnlyCommandsWhileWorking(t *testing.T) {
	m := workingModel(t)
	m.input.SetValue("/c")
	m.syncCompletions()
	for _, c := range m.completions {
		if c.name == "/clear" || c.name == "/compact" {
			t.Fatalf("%s cannot run mid-turn, so it should not be offered: %+v", c.name, m.completions)
		}
	}

	updated, _ := m.Update(doneMsg{})
	m = updated.(Model)
	m.input.SetValue("/c")
	m.syncCompletions()
	var names []string
	for _, c := range m.completions {
		names = append(names, c.name)
	}
	if !containsString(names, "/clear") {
		t.Fatalf("/clear should be back once the turn ended, got %v", names)
	}
}

// A surface opened mid-turn borrows the screen, not the turn: results that
// arrive while it is up are still routed (S-087).
func TestSurface_TurnKeepsRunningUnderneath(t *testing.T) {
	executor := func(name string, args json.RawMessage) (string, error) { return "read 3 lines", nil }
	m := gatedModel(t, executor, nil)
	m.changes.Add(1, changeset.Record{
		Path: "x.go", Before: "one\n", After: "one\ntwo\n",
		BeforeExists: true, AfterExists: true,
	})

	updated, cmd := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_r", Name: "read_file", Arguments: `{"path":"x.go"}`},
	}})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("an ungated tool call should run in the background")
	}

	m = sendText(t, m, "/diff")
	if m.state != stateReview {
		t.Fatalf("/diff should open the session's changes mid-turn, got state %d", m.state)
	}
	if m.turnState() != stateStreaming {
		t.Fatalf("the tool round should still be in flight, got turn state %d", m.turnState())
	}

	updated, _ = m.Update(cmd())
	m = updated.(Model)
	if m.state != stateReview {
		t.Fatal("a result arriving mid-surface must not close the surface")
	}
	var sawTool bool
	for _, e := range m.transcript {
		if e.kind == entryTool && e.toolName == "read_file" {
			sawTool = true
		}
	}
	if !sawTool {
		t.Fatal("the tool result must still be routed while a surface is up")
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.state != stateStreaming {
		t.Fatalf("esc should hand the screen back to the running turn, got %d", m.state)
	}
}

func TestAgents_OpensWhileWorking(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	m = sendText(t, m, "do the task")
	if m.state != stateStreaming {
		t.Fatalf("expected a turn in flight, got %d", m.state)
	}

	m = sendText(t, m, "/agents")
	if m.agentList == nil {
		t.Fatal("/agents must open the manager while the agent works — that is the only time children exist")
	}
	if m.state != stateStreaming {
		t.Fatalf("the agent list must not disturb the turn, got %d", m.state)
	}
}

func TestAttachCommand_SteersAChildMidTurn(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	m = sendText(t, m, "do the task")

	exec := sup.WrapExecutor(nil)
	if _, err := exec(subagent.SpawnToolName, json.RawMessage(`{"role":"researcher","task":"long survey"}`)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { a, _ := sup.ActiveCounts(); return a == 1 })

	m = sendText(t, m, "/attach researcher-1")
	if m.attachedTo != "researcher-1" {
		t.Fatalf("/attach should jump into the child's session, got %q", m.attachedTo)
	}

	m = sendText(t, m, "skip the vendor directory")
	waitFor(t, func() bool { return sup.QueuedSteering("researcher-1") == 1 })

	m = sendText(t, m, "/detach")
	if m.attachedTo != "" {
		t.Fatalf("/detach should return to the orchestrator, got %q", m.attachedTo)
	}
	if m.state != stateStreaming {
		t.Fatalf("the parent turn should still be running, got %d", m.state)
	}
}

func TestAttachCommand_UnknownAgent(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)

	m = sendText(t, m, "/attach nobody-1")
	if m.attachedTo != "" {
		t.Fatal("an unknown agent must not attach")
	}
	if !transcriptContains(m, "No agent named nobody-1") {
		t.Fatal("expected a notice naming the missing agent")
	}
}

func TestSubmit_PlainTextStillSteers(t *testing.T) {
	m := workingModel(t)
	m = sendText(t, m, "/etc/hosts is the file I mean")
	if len(m.steering) != 1 {
		t.Fatalf("a path is text, not a command; expected it queued, got %v", m.steering)
	}
}

// Attached, /attach hops sideways to another agent without going back out to
// the orchestrator first (S-087).
func TestAttachCommand_HopsBetweenAgentsWhileAttached(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	m = sendText(t, m, "do the task")

	exec := sup.WrapExecutor(nil)
	for _, task := range []string{"survey one", "survey two"} {
		if _, err := exec(subagent.SpawnToolName, json.RawMessage(`{"role":"researcher","task":"`+task+`"}`)); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, func() bool { a, _ := sup.ActiveCounts(); return a == 2 })

	m = sendText(t, m, "/attach researcher-1")
	m = sendText(t, m, "/attach researcher-2")
	if m.attachedTo != "researcher-2" {
		t.Fatalf("expected a sideways hop to researcher-2, got %q", m.attachedTo)
	}

	// An answer while attached belongs to the child's transcript, not the
	// orchestrator's, where it would be invisible.
	before := len(m.transcript)
	m = sendText(t, m, "/attach nobody-1")
	if len(m.transcript) != before {
		t.Fatal("an attached answer must not land on the orchestrator transcript")
	}
	var found bool
	for _, te := range sup.Transcript("researcher-2") {
		if strings.Contains(te.Text, "No agent named nobody-1") {
			found = true
		}
	}
	if !found {
		t.Fatal("expected the notice in the attached child's transcript")
	}
}
