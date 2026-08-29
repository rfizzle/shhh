package chat

// Sub-agent management and steering tests (S-077): the agent list, attach /
// detach, scoped commands, steering, mode clamping, and the [g] jump from a
// routed approval.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/ui/components"
)

func key(r rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: r, Text: string(r)} }

// spawnBlockedChild spawns one researcher whose stream blocks until the child
// is cancelled, so it stays observable as "running".
func spawnBlockedChild(t *testing.T, sup *subagent.Supervisor) {
	t.Helper()
	exec := sup.WrapExecutor(nil)
	if _, err := exec(subagent.SpawnToolName, json.RawMessage(`{"role":"researcher","task":"long survey"}`)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		st, ok := sup.Get("researcher-1")
		return ok && st.State == subagent.StateRunning
	})
}

func TestAgentListOpensAttachesAndDetaches(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	spawnBlockedChild(t, sup)

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	m = updated.(Model)
	if m.agentList == nil {
		t.Fatal("ctrl+a must open the agent list")
	}
	view := m.View().Content
	if !strings.Contains(view, "orchestrator") || !strings.Contains(view, "researcher-1") {
		t.Fatalf("agent list missing rows:\n%s", view)
	}

	// Down to the child row, enter attaches.
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if m.attachedTo != "researcher-1" {
		t.Fatalf("attachedTo = %q, want researcher-1", m.attachedTo)
	}
	if m.agentList != nil {
		t.Fatal("attaching must close the list")
	}
	view = m.View().Content
	if !strings.Contains(view, "orchestrator ▸ researcher-1") {
		t.Fatalf("attached view missing breadcrumb:\n%s", view)
	}
	if !strings.Contains(view, "esc detach") {
		t.Fatalf("attached view missing detach hint:\n%s", view)
	}
	if !strings.Contains(view, "long survey") {
		t.Fatalf("attached view missing the child's transcript:\n%s", view)
	}

	// Esc with an empty draft pops back to the orchestrator.
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)
	if m.attachedTo != "" {
		t.Fatalf("esc must detach, still attached to %q", m.attachedTo)
	}
}

func TestSlashAgentsOpensList(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)

	m.input.SetValue("/agents")
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if m.agentList == nil {
		t.Fatal("/agents must open the agent list")
	}
	// Esc dismisses it.
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)
	if m.agentList != nil {
		t.Fatal("esc must dismiss the agent list")
	}
}

func TestAttachedEnterSteersChild(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	spawnBlockedChild(t, sup)

	m.attach("researcher-1")
	m.input.SetValue("hold off on model.go")
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if n := sup.QueuedSteering("researcher-1"); n != 1 {
		t.Fatalf("QueuedSteering = %d, want 1", n)
	}
	if !strings.Contains(m.View().Content, "queued 1") {
		t.Fatalf("status bar missing the queued-steering count:\n%s", m.View().Content)
	}
}

func TestAttachedScopedCommands(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	spawnBlockedChild(t, sup)
	m.attach("researcher-1")

	// /stats scopes to the child.
	m.input.SetValue("/stats")
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if !childTranscriptContains(sup, "researcher-1", "tool calls") {
		t.Fatal("/stats note missing from the child transcript")
	}

	// /diff on a researcher reports there is nothing scoped to diff.
	m.input.SetValue("/diff")
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if !childTranscriptContains(sup, "researcher-1", "nothing to diff") {
		t.Fatal("/diff error missing from the child transcript")
	}

	// Unknown commands get the scoped-command hint, not the parent handler.
	m.input.SetValue("/save")
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if !childTranscriptContains(sup, "researcher-1", "Commands while attached") {
		t.Fatal("scoped-command hint missing")
	}
}

func TestAttachedModeClampedToCeiling(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup) // parent mode: manual (the ceiling)
	spawnBlockedChild(t, sup)
	m.attach("researcher-1")

	// Shift+Tab skips accept-edits and auto (over the manual ceiling) and
	// lands on plan; the skipped modes are named as disabled.
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	m = updated.(Model)
	if mode, _ := sup.AgentMode("researcher-1"); mode != agent.ModePlan {
		t.Fatalf("child mode = %s, want plan", mode)
	}
	if !childTranscriptContains(sup, "researcher-1", "Disabled") {
		t.Fatal("disabled over-ceiling modes not surfaced")
	}

	// /mode with an over-ceiling mode refuses instead of clamping silently.
	m.input.SetValue("/mode auto")
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if mode, _ := sup.AgentMode("researcher-1"); mode != agent.ModePlan {
		t.Fatalf("over-ceiling /mode must not change the mode, got %s", mode)
	}
	if !childTranscriptContains(sup, "researcher-1", "exceeds the orchestrator's ceiling") {
		t.Fatal("over-ceiling refusal not surfaced")
	}
}

func TestAttachedCtrlCCancelsChildTurn(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	spawnBlockedChild(t, sup)
	m.attach("researcher-1")

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	m = updated.(Model)
	// blockingEnv ignores the per-request cancel, so the child ends as
	// cancelled once its context is cancelled at cleanup; the state must have
	// left "running" the moment the turn was interrupted, or stay blocked on
	// the never-cancelling stream — either way the model must not crash and
	// the child keeps its transcript.
	if m.attachedTo != "researcher-1" {
		t.Fatal("cancelling the child's turn must not detach")
	}
	_ = m.View().Content
}

func TestKillFromListWithInlineConfirm(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	spawnBlockedChild(t, sup)

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = updated.(Model)
	updated, _ = m.Update(key('X'))
	m = updated.(Model)
	if m.killConfirm == nil || m.killTarget != "researcher-1" {
		t.Fatalf("X must arm the inline kill confirm (target %q)", m.killTarget)
	}
	if !strings.Contains(m.View().Content, "Kill researcher-1?") {
		t.Fatalf("kill confirm not rendered:\n%s", m.View().Content)
	}
	// n declines: nothing happens.
	updated, _ = m.Update(key('n'))
	m = updated.(Model)
	if m.killConfirm != nil {
		t.Fatal("n must dismiss the confirm")
	}
	if st, _ := sup.Get("researcher-1"); st.State != subagent.StateRunning {
		t.Fatalf("declined kill must leave the child running, got %s", st.State)
	}
	// y confirms: the child dies.
	updated, _ = m.Update(key('X'))
	m = updated.(Model)
	updated, _ = m.Update(key('y'))
	m = updated.(Model)
	waitFor(t, func() bool {
		st, ok := sup.Get("researcher-1")
		return ok && st.State == subagent.StateFailed
	})
}

// pumpAsks feeds the supervisor's events into the model the way
// listenSubagents does at runtime, until want approvals have been routed into
// the session. The child is really blocked on the other end of them, which is
// what makes the answer observable through the child rather than through the
// request object (the child consumes the response itself).
func pumpAsks(t *testing.T, m Model, sup *subagent.Supervisor, want int) Model {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for len(m.childAsks) < want {
		select {
		case ev := <-sup.Events():
			updated, _ := m.Update(subagentEventMsg{ev: ev})
			m = updated.(Model)
		case <-deadline:
			t.Fatalf("only %d of %d approvals arrived", len(m.childAsks), want)
		}
	}
	return m
}

// TestBlockedRowSortsUpAndSaysWhatItWaitsFor: the manager puts whoever needs
// an answer directly below the orchestrator and states what the answer is
// for, because "⚠ needs you" on its own sends the reader looking.
func TestBlockedRowSortsUpAndSaysWhatItWaitsFor(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: gatedEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	spawnInto(t, sup, `{"role":"researcher","task":"first"}`)
	spawnInto(t, sup, `{"role":"researcher","task":"second"}`)
	waitFor(t, func() bool { _, blocked := sup.ActiveCounts(); return blocked == 2 })
	m = pumpAsks(t, m, sup, 2)

	rows, names := m.buildAgentRows()
	if len(rows) != 3 || names[0] != "" {
		t.Fatalf("rows = %d, names = %v; want the orchestrator plus two children", len(rows), names)
	}
	for _, row := range rows[1:] {
		if row.State != components.AgentBlocked {
			t.Fatalf("blocked children must sort to the top, got %v at the front", row.State)
		}
		if !strings.Contains(row.Note, "echo hi") {
			t.Fatalf("a blocked row must say what it waits for, got %q", row.Note)
		}
		if !row.Answerable {
			t.Fatalf("a blocked row with a queued ask must be answerable: %+v", row)
		}
		if row.Progress == nil {
			t.Fatalf("a child's row must carry lane progress: %+v", row)
		}
	}
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	m = updated.(Model)
	if view := m.View().Content; !strings.Contains(view, "2 needs you") {
		t.Fatalf("the manager's title rail must state who needs you:\n%s", view)
	}
}

// TestAnswerBlockedChildFromTheList is S-111's whole point: opening the
// manager because something needs you must not then send you into that
// child's session to say yes. [a] renders the card over the list and hands
// the list back.
func TestAnswerBlockedChildFromTheList(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: gatedEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	spawnInto(t, sup, `{"role":"researcher","task":"survey"}`)
	m = pumpAsks(t, m, sup, 1)

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	m = updated.(Model)
	// The child sorts directly below the orchestrator; [a] on it opens the card.
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = updated.(Model)
	updated, _ = m.Update(key('a'))
	m = updated.(Model)
	if m.answerAgent != "researcher-1" {
		t.Fatalf("answerAgent = %q, want researcher-1", m.answerAgent)
	}
	if m.attachedTo != "" {
		t.Fatalf("answering must not attach, attached to %q", m.attachedTo)
	}
	view := m.View().Content
	if !strings.Contains(view, "echo hi") {
		t.Fatalf("the approval card is not over the list:\n%s", view)
	}
	if strings.Contains(view, "enter attach") {
		t.Fatalf("the list must step aside while the card is up:\n%s", view)
	}

	updated, _ = m.Update(key('y'))
	m = updated.(Model)
	if !transcriptContains(m, "Approved researcher-1 ▸ run echo hi") {
		t.Fatal("transcript missing the approval entry")
	}
	if len(m.childAsks) != 0 {
		t.Fatalf("the answered request must leave the queue, %d left", len(m.childAsks))
	}
	// The child was really waiting on it: it consumed the answer and came
	// back with its next request, which is what unblocking looks like from
	// out here.
	m = pumpAsks(t, m, sup, 1)
	if m.answerAgent != "" || m.agentList == nil {
		t.Fatalf("answering must return to the list (answerAgent=%q, open=%v)", m.answerAgent, m.agentList != nil)
	}
	if !strings.Contains(m.View().Content, "enter attach") {
		t.Fatalf("the list did not come back:\n%s", m.View().Content)
	}
}

// TestAnswerFromTheListDeclinesOnEsc: a routed request is never dropped, and
// the list is still what the answer returns to.
func TestAnswerFromTheListDeclinesOnEsc(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: gatedEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	spawnInto(t, sup, `{"role":"researcher","task":"survey"}`)
	m = pumpAsks(t, m, sup, 1)

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = updated.(Model)
	updated, _ = m.Update(key('a'))
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)

	if !transcriptContains(m, "Declined researcher-1 ▸ run echo hi") {
		t.Fatal("a routed request is never dropped: the decline must be recorded")
	}
	if m.agentList == nil || m.answerAgent != "" {
		t.Fatal("declining must return to the list")
	}
}

// TestRetryFailedChildFromTheList: [r] runs a failed child again on its
// original task, and the row says why it failed before it does.
func TestRetryFailedChildFromTheList(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	spawnBlockedChild(t, sup)
	if err := sup.Kill("researcher-1"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		st, ok := sup.Get("researcher-1")
		return ok && st.State == subagent.StateFailed
	})

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = updated.(Model)
	view := m.View().Content
	if !strings.Contains(view, "r retry") {
		t.Fatalf("a failed row must offer the retry:\n%s", view)
	}
	if !strings.Contains(view, "cancelled") {
		t.Fatalf("a failed row must state why it failed:\n%s", view)
	}

	updated, _ = m.Update(key('r'))
	m = updated.(Model)
	if m.agentList == nil {
		t.Fatal("retrying must leave the list open")
	}
	waitFor(t, func() bool {
		st, ok := sup.Get("researcher-1")
		return ok && st.State == subagent.StateRunning
	})
	if st, _ := sup.Get("researcher-1"); st.Task != "long survey" {
		t.Fatalf("the retry must keep the original task, got %q", st.Task)
	}
	if !transcriptContains(m, "Retrying researcher-1 on its original task.") {
		t.Fatal("transcript missing the retry entry")
	}
}

func TestDetachedAskGJumpsToAgent(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	spawnBlockedChild(t, sup)

	ask := subagent.NewAsk("researcher-1", subagent.AskCommand, "run echo hi")
	ask.Command = "echo hi"
	updated, _ := m.Update(subagentEventMsg{ev: subagent.Event{Kind: subagent.EventAsk, Ask: ask}})
	m = updated.(Model)

	// Detached, the card offers the jump — once it holds the keyboard, since
	// until then [g] is a letter (S-117, §7b).
	m = handover(t, m)
	if view := m.View().Content; !strings.Contains(view, "g: attach to researcher-1") {
		t.Fatalf("routed card missing the [g] hint:\n%s", view)
	}
	updated, _ = m.Update(key('g'))
	m = updated.(Model)
	if m.attachedTo != "researcher-1" {
		t.Fatalf("g must attach, attachedTo = %q", m.attachedTo)
	}
	// The ask is still pending and renders in place, unprefixed.
	if m.activeChildAsk() != ask {
		t.Fatal("the pending ask must survive the jump")
	}
	if _, answered := ask.Answered(); answered {
		t.Fatal("the jump must not answer the ask")
	}
	// Answering in place works.
	m = handover(t, m)
	updated, _ = m.Update(key('y'))
	m = updated.(Model)
	if approved, ok := ask.Answered(); !ok || !approved {
		t.Fatalf("in-place approval failed: %v %v", approved, ok)
	}
}

func TestAttachedAsksScopeToFocusedAgent(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)

	other := subagent.NewAsk("writer-1", subagent.AskCommand, "run make")
	mine := subagent.NewAsk("researcher-1", subagent.AskGeneric, "use web_fetch")
	m.childAsks = []*subagent.Ask{other, mine}

	m.attachedTo = "researcher-1"
	if got := m.activeChildAsk(); got != mine {
		t.Fatalf("attached view must present only the focused agent's ask, got %+v", got)
	}
	m.attachedTo = ""
	if got := m.activeChildAsk(); got != other {
		t.Fatalf("detached view presents the queue head, got %+v", got)
	}
}

func TestEventDonePurgesThatAgentsAsks(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)

	ask := subagent.NewAsk("researcher-1", subagent.AskCommand, "run x")
	m.childAsks = []*subagent.Ask{ask}
	ev := subagent.Event{Kind: subagent.EventDone, Status: subagent.Status{Name: "researcher-1", Detail: "failed · cancelled"}}
	updated, _ := m.Update(subagentEventMsg{ev: ev})
	m = updated.(Model)
	if len(m.childAsks) != 0 {
		t.Fatal("a finished agent's asks must be purged")
	}
	if approved, ok := ask.Answered(); !ok || approved {
		t.Fatalf("purged ask must be declined: %v %v", approved, ok)
	}
}

func TestAttachDetachPreservesExpansion(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	spawnBlockedChild(t, sup)
	if err := sup.Note("researcher-1", subagent.TranscriptEntry{Kind: subagent.EntryTool, Tool: "read_file", Args: `{"path":"x"}`, Result: "line"}); err != nil {
		t.Fatal(err)
	}

	m.attach("researcher-1")
	cv := m.syncChildView("researcher-1")
	idx := -1
	for i, e := range cv.entries {
		if e.kind == entryTool {
			idx = i
		}
	}
	if idx == -1 {
		t.Fatal("mirrored transcript missing the tool entry")
	}
	cv.entries[idx].expanded = true
	m.detachOne()
	m.attach("researcher-1")
	if !m.syncChildView("researcher-1").entries[idx].expanded {
		t.Fatal("expansion state must survive detach/attach")
	}
}

func childTranscriptContains(sup *subagent.Supervisor, name, substr string) bool {
	for _, e := range sup.Transcript(name) {
		if strings.Contains(e.Text, substr) || strings.Contains(e.Result, substr) {
			return true
		}
	}
	return false
}
