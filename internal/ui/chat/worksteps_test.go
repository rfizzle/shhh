package chat

import (
	"testing"

	"github.com/rfizzle/shhh/internal/plan"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// callRound delivers one round that opens with text and goes on to a call,
// the way a stream ending in tool calls arrives.
func callRound(t *testing.T, m Model, text string) Model {
	t.Helper()
	m.setTurnState(stateStreaming)
	m.streaming = text
	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call-" + text[:1], Name: "read_file", Arguments: `{"path":"x.go"}`},
	}})
	return updated.(Model)
}

// closingMessage delivers a message that ends the turn.
func closingMessage(t *testing.T, m Model, text string) Model {
	t.Helper()
	m.setTurnState(stateStreaming)
	m.streaming = text
	updated, _ := m.Update(doneMsg{})
	return updated.(Model)
}

func stepsOnRail(m Model) *components.InspectorSteps { return m.resolveInspector().Steps }

// No list draws nothing: a short task declares none, and the rail has no
// STEPS block rather than one reading zero of zero.
func TestWorkSteps_NoListDrawsNothing(t *testing.T) {
	m := progressModel(t, mockStream)
	m = callRound(t, m, "Reading the loop first.")
	if s := stepsOnRail(m); s != nil {
		t.Fatalf("no list should draw no block, got %+v", s)
	}
}

// A list before a call is the session's; a mark moves it; a revision under
// `steps:` keeps the finished steps; a numbered list without the marker is
// text; and a list in a message that ends the turn is a report.
func TestWorkSteps_DeclaredMarkedAndRevised(t *testing.T) {
	m := progressModel(t, mockStream)
	m = callRound(t, m, "1. Read the loop\n2. Patch it\n3. Test it")
	if s := stepsOnRail(m); s == nil || *s != (components.InspectorSteps{Total: 3, Current: "Read the loop"}) {
		t.Fatalf("declared list = %+v, want 0 of 3", s)
	}
	m = callRound(t, m, "progress: 1\nFound it.")
	m = callRound(t, m, "Files:\n1. loop.go\n2. round.go")
	if s := stepsOnRail(m); *s != (components.InspectorSteps{Done: 1, Total: 3, Current: "Patch it"}) {
		t.Fatalf("after a mark and an unmarked list = %+v, want 1 of 3 on the patch", *s)
	}
	m = callRound(t, m, "The flag is the problem.\nsteps:\n1. Add the flag\n2. Test it")
	if s := stepsOnRail(m); *s != (components.InspectorSteps{Done: 1, Total: 3, Current: "Add the flag"}) {
		t.Fatalf("after the revision = %+v, want 1 of 3 on the flag", *s)
	}
	m = closingMessage(t, m, "Changed:\n1. loop.go\n2. loop_test.go\nprogress: 2\nprogress: 3")
	if s := stepsOnRail(m); *s != (components.InspectorSteps{Done: 3, Total: 3}) {
		t.Fatalf("after the closing message = %+v, want its marks counted and its list not taken", *s)
	}
	// Every step marked is the agent's account of its list, not a finished
	// task: the turn ended because the stream did, and the block names no
	// step and says nothing more than the count.
	if m.turnState() != stateInput {
		t.Fatalf("turn state = %v", m.turnState())
	}
}

// While an approved plan is being executed the plan is the checklist: STEPS
// is not drawn, no list is read, and nothing an agent writes reaches the
// plan's record, its steps or its approval.
func TestWorkSteps_NeverTouchesTheApprovedPlan(t *testing.T) {
	m := progressModel(t, mockStream)
	m = callRound(t, m, "1. Read\n2. Patch")
	m.planRun = newPlanRun(plan.Parse(planFixture), 0)
	m.planned = plan.Record{Task: "the approved task", Steps: []plan.RecordStep{{Number: 1, Title: "one"}, {Number: 2, Title: "two"}}}
	before := m.planRun.doc.Text
	m = callRound(t, m, "steps:\n1. Something else entirely\nprogress: 1")
	if s := stepsOnRail(m); s != nil {
		t.Fatalf("STEPS drew beside an approved plan: %+v", s)
	}
	if m.planRun.doc.Text != before || len(m.planRun.doc.Steps) != len(plan.Parse(planFixture).Steps) {
		t.Fatal("the agent's list moved the approved plan's steps")
	}
	if m.planned.Task != "the approved task" || len(m.planned.Steps) != 2 {
		t.Fatalf("the agent's list moved the plan record: %+v", m.planned)
	}
	if done, total, _ := m.workSteps.Tally(); done != 0 || total != 2 {
		t.Fatalf("a list was read while the plan ran: %d/%d", done, total)
	}
}

// A conversation asks for no list, so none is read out of its answers.
func TestWorkSteps_AConversationKeepsNone(t *testing.T) {
	m := progressModel(t, mockStream)
	m.conversation = true
	m = callRound(t, m, "1. Read\n2. Answer")
	if _, total, _ := m.workSteps.Tally(); total != 0 {
		t.Fatal("a conversation read a working list")
	}
}

// The list is saved with the slot and comes back with the conversation; a new
// session drops it with the rest.
func TestWorkSteps_SavedResumedAndDroppedAtTheBoundary(t *testing.T) {
	db := rewindTestDB(t)
	m := progressModel(t, mockStream).WithDB(db)
	m = callRound(t, m, "1. Read\n2. Patch\n3. Test\nprogress: 1")
	if msg := m.autosaveCmd()(); msg != nil {
		t.Fatalf("autosave: %#v", msg)
	}
	slot := m.sessionName

	saved, err := db.LoadChat(slot)
	if err != nil {
		t.Fatal(err)
	}
	back := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream).
		WithDB(db).WithResumedMessages(slot, saved)
	if s := stepsOnRail(back); s == nil || *s != (components.InspectorSteps{Done: 1, Total: 3, Current: "Patch"}) {
		t.Fatalf("resumed list = %+v, want 1 of 3 on the patch", s)
	}

	back.startNewSession()
	if s := stepsOnRail(back); s != nil {
		t.Fatalf("a new session kept the list: %+v", s)
	}
}

// A new turn may declare its own list in its first message that goes on to a
// call; until then the last one stands on the rail.
func TestWorkSteps_ANewTurnMayDeclareAgain(t *testing.T) {
	m := progressModel(t, mockStream)
	m = callRound(t, m, "1. Read\n2. Patch")
	m = closingMessage(t, m, "Done for now.")
	updated, _ := m.sendUserMessage("now the docs")
	m = updated.(Model)
	if s := stepsOnRail(m); s == nil || s.Total != 2 {
		t.Fatalf("the last list should stand until a new one is declared, got %+v", s)
	}
	m = callRound(t, m, "1. Docs\n2. Changelog\n3. Release note")
	if s := stepsOnRail(m); s == nil || *s != (components.InspectorSteps{Total: 3, Current: "Docs"}) {
		t.Fatalf("the new turn's list = %+v", s)
	}
}

// A steer moves the turn onto the task the person gave, so the first message
// after it that goes on to a call may declare a list of its own — from the
// reader's box or from another session — while a machine message the
// session queued for itself is no new task and opens nothing.
func TestWorkSteps_ASteerMayDeclareAgain(t *testing.T) {
	for _, tc := range []struct {
		name  string
		item  steeringItem
		fresh bool
	}{
		{"typed", steeringItem{text: "leave the loop, do the docs"}, true},
		{"sent", steeringItem{text: "leave the loop, do the docs", sent: true, from: "other"}, true},
		{"machine", steeringItem{text: "a note the session wrote", machine: true}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := progressModel(t, mockStream)
			m = callRound(t, m, "1. Read\n2. Patch")
			m.steering = []steeringItem{tc.item}
			if !m.injectSteering() {
				t.Fatal("the steer was not injected")
			}
			if s := stepsOnRail(m); s == nil || s.Total != 2 {
				t.Fatalf("the last list should stand until a new one is declared, got %+v", s)
			}
			m = callRound(t, m, "1. Docs\n2. Changelog\n3. Release note")
			s := stepsOnRail(m)
			if tc.fresh && (s == nil || *s != (components.InspectorSteps{Total: 3, Current: "Docs"})) {
				t.Fatalf("the steered turn's list = %+v, want 0 of 3 on the docs", s)
			}
			if !tc.fresh && (s == nil || s.Total != 2) {
				t.Fatalf("a machine message opened a fresh list: %+v", s)
			}
		})
	}
}
