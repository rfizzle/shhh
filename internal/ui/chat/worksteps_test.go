package chat

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
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

// stepsListModel is stepsModel's two titled runs under a three-step list whose
// first step is marked: the first step shares its run's title once a number
// and a full stop are set aside, the second is the one the agent is on and
// shares nothing, and the third names the files it will touch.
func stepsListModel(t *testing.T) Model {
	t.Helper()
	m := stepsModel(t)
	m.workSteps.Note("1. Locate the round accounting.\n2. Patch the limit\n3. Test it\n   files: internal/agent/loop_test.go", true)
	m.workSteps.Note("progress: 1", true)
	return m
}

// The list is the checklist and each step carries the run the transcript
// titled for it; a step no run is titled for is not started, and the one the
// agent is on is marked as the rail marks it.
func TestStepsScreen_JoinsTheListToTheRunsByTitle(t *testing.T) {
	m := stepsListModel(t)
	opened, _ := m.runCommand("/steps", "/steps")
	got := opened.(Model)
	if got.state != stateSteps || got.stepsScreen == nil {
		t.Fatalf("/steps should open the screen, got state %d", got.state)
	}
	s := got.stepsScreen
	if s.Subject != "1 of 3" || s.Focus != 1 {
		t.Fatalf("subject %q focus %d, want the rail's count and the current step", s.Subject, s.Focus)
	}
	first, second, third := s.Steps[0], s.Steps[1], s.Steps[2]
	if !first.Done || !first.Started || first.Count != "2 tools" || len(first.Rows) != 2 {
		t.Fatalf("the first step should carry its run: %+v", first)
	}
	if first.Rows[0].Verb != "read" || first.Rows[0].Expanded || len(first.Rows[0].Detail) != 0 {
		t.Fatalf("a run's row should be the transcript's own, folded to one line: %+v", first.Rows[0])
	}
	if !second.Current || second.Started {
		t.Fatalf("the second step is the current one and nothing is titled for it: %+v", second)
	}
	if len(third.Paths) != 1 || third.Paths[0] != "internal/agent/loop_test.go" || third.Current {
		t.Fatalf("the third step should carry the path it named: %+v", third)
	}
	if !got.inspectorHidden() {
		t.Fatal("the screen should stand over the rail")
	}
	next, _ := got.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if left := next.(Model); left.state == stateSteps || left.stepsScreen != nil {
		t.Fatalf("esc should leave the screen, got state %d", left.state)
	}
}

// Where there is no list to show, the command says why in a line instead of
// opening an empty screen.
func TestStepsScreen_NoListSaysWhy(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(*Model)
		want string
	}{
		{"no list", func(*Model) {}, "declared no working steps"},
		{"a conversation", func(m *Model) { m.conversation = true }, "not part of this session"},
		{"an approved plan", func(m *Model) { m.planRun = newPlanRun(plan.Parse(planFixture), 0) }, "its steps are the checklist"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := stepsModel(t)
			tc.set(&m)
			next, _ := m.runCommand("/steps", "/steps")
			got := next.(Model)
			if got.state == stateSteps {
				t.Fatal("the screen opened over nothing")
			}
			last := got.transcript[len(got.transcript)-1]
			if last.kind != entrySystem || !strings.Contains(last.text, tc.want) {
				t.Fatalf("the notice = %q, want %q", last.text, tc.want)
			}
		})
	}
}

// The join compares the words, not how they were written down.
func TestStepKey_SetsTheWritingAside(t *testing.T) {
	for _, pair := range [][2]string{
		{"Read the loop", "read  the loop."},
		{"1. Read the loop", "Read the loop"},
		{"Step 2: Patch it", "patch it"},
	} {
		if stepKey(pair[0]) != stepKey(pair[1]) {
			t.Errorf("%q and %q should be one step: %q vs %q", pair[0], pair[1], stepKey(pair[0]), stepKey(pair[1]))
		}
	}
	if stepKey("2 files changed") != "2 files changed" {
		t.Errorf("a number that is the title's own words was taken off: %q", stepKey("2 files changed"))
	}
}
