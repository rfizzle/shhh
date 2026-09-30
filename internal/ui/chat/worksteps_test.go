package chat

import (
	"encoding/json"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/plan"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// stepsCalled is the checklist a steps call names, failing the test where
// the tool would have refused it.
func stepsCalled(t *testing.T, args string) plan.Checklist {
	t.Helper()
	l, err := plan.ParseStepsCall(json.RawMessage(args))
	if err != nil {
		t.Fatalf("ParseStepsCall(%s): %v", args, err)
	}
	return l
}

// stepsCall is a steps call as a round's result delivers it.
func stepsCall(args string) provider.ToolCall {
	return provider.ToolCall{ID: "steps-call", Name: plan.StepsToolName, Arguments: args}
}

func stepsOnRail(m Model) *components.InspectorSteps { return m.resolveInspector().Steps }

// No list draws nothing: a short task declares none, and the rail has no
// STEPS block rather than one reading zero of zero — which is also what an
// empty call leaves.
func TestWorkSteps_NoListDrawsNothing(t *testing.T) {
	m := progressModel(t, mockStream)
	if s := stepsOnRail(m); s != nil {
		t.Fatalf("no list should draw no block, got %+v", s)
	}
	m.noteStepsCall(stepsCall(`{"steps":[{"title":"Read"}]}`))
	m.noteStepsCall(stepsCall(`{"steps":[]}`))
	if s := stepsOnRail(m); s != nil {
		t.Fatalf("a cleared list should draw no block, got %+v", s)
	}
}

// A call names the whole list, the next replaces it — a mark taken back and a
// step reworded are that same call — and a call the tool refused changes
// nothing, since it is read again with the refusal it was answered with.
func TestWorkSteps_ACallIsTheWholeList(t *testing.T) {
	m := progressModel(t, mockStream)
	m.noteStepsCall(stepsCall(`{"steps":[{"title":"Read the loop","done":true},{"title":"Patch it"},{"title":"Test it"}]}`))
	if s := stepsOnRail(m); s == nil || *s != (components.InspectorSteps{Done: 1, Total: 3, Current: "Patch it"}) {
		t.Fatalf("declared list = %+v, want 1 of 3 on the patch", s)
	}
	m.noteStepsCall(stepsCall(`{"steps":[{"title":"Read the loop"},{"title":"Add the flag"},{"title":"Test it"}]}`))
	if s := stepsOnRail(m); *s != (components.InspectorSteps{Total: 3, Current: "Read the loop"}) {
		t.Fatalf("a taken-back mark and a reworded step = %+v, want 0 of 3", *s)
	}
	m.noteStepsCall(stepsCall(`{"steps":[{"title":""}]}`))
	m.noteStepsCall(provider.ToolCall{Name: "read_file", Arguments: `{"steps":[]}`})
	if s := stepsOnRail(m); s == nil || s.Total != 3 {
		t.Fatalf("a refused call or another tool moved the list: %+v", s)
	}
}

// While an approved plan is being executed the plan is the checklist: STEPS
// is not drawn, no call moves the list, and nothing reaches the plan's
// record, its steps or its approval.
func TestWorkSteps_NeverTouchesTheApprovedPlan(t *testing.T) {
	m := progressModel(t, mockStream)
	m.noteStepsCall(stepsCall(`{"steps":[{"title":"Read"},{"title":"Patch"}]}`))
	m.planRun = newPlanRun(plan.Parse(planFixture), 0)
	m.planned = plan.Record{Task: "the approved task", Steps: []plan.RecordStep{{Number: 1, Title: "one"}, {Number: 2, Title: "two"}}}
	before := m.planRun.doc.Text
	m.noteStepsCall(stepsCall(`{"steps":[{"title":"Something else entirely","done":true}]}`))
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
		t.Fatalf("a call moved the list while the plan ran: %d/%d", done, total)
	}
}

// A conversation keeps no list.
func TestWorkSteps_AConversationKeepsNone(t *testing.T) {
	m := progressModel(t, mockStream)
	m.conversation = true
	m.noteStepsCall(stepsCall(`{"steps":[{"title":"Read"},{"title":"Answer"}]}`))
	if _, total, _ := m.workSteps.Tally(); total != 0 {
		t.Fatal("a conversation kept a working list")
	}
}

// The list is saved with the slot and comes back with the conversation; a new
// session drops it with the rest.
func TestWorkSteps_SavedResumedAndDroppedAtTheBoundary(t *testing.T) {
	db := rewindTestDB(t)
	m := progressModel(t, mockStream).WithDB(db)
	m.noteStepsCall(stepsCall(`{"steps":[{"title":"Read","done":true},{"title":"Patch"},{"title":"Test"}]}`))
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
	// The model is told the list too, since the call that set it is far
	// behind — and the copy it is told is never saved with the conversation.
	carried := func(msgs []provider.Message) bool {
		for _, msg := range msgs {
			if strings.HasPrefix(msg.Content, plan.CarriedStepsPrefix) && strings.Contains(msg.Content, "→ [ ] 2. Patch") {
				return true
			}
		}
		return false
	}
	if !carried(back.agent.Messages()) {
		t.Fatal("the resumed conversation was not told its working list")
	}
	if carried(stripResumeContext(back.agent.Messages())) {
		t.Fatal("the carried list would be saved with the conversation")
	}

	back.startNewSession()
	if s := stepsOnRail(back); s != nil {
		t.Fatalf("a new session kept the list: %+v", s)
	}
}

// stepsListModel is stepsModel's two titled runs under a three-step list whose
// first step is marked: the first step shares its run's title once a number
// and a full stop are set aside, the second is the one the agent is on and
// shares nothing, and the third names the files it will touch.
func stepsListModel(t *testing.T) Model {
	t.Helper()
	m := stepsModel(t)
	m.workSteps = stepsCalled(t, `{"steps":[{"title":"Locate the round accounting.","done":true},{"title":"Patch the limit"},`+
		`{"title":"Test it","paths":["internal/agent/loop_test.go"]}]}`)
	return m
}

// The list is the checklist and each step carries the run the transcript
// titled for it; a step no run is titled for is not started, and the one the
// agent is on is marked as the rail marks it.
func TestStepsScreen_JoinsTheListToTheRunsByTitle(t *testing.T) {
	m := stepsListModel(t)
	opened, _ := m.runCommand("/steps", "/steps")
	got := opened.(Model)
	if got.state != stateSteps || got.screens.steps() == nil {
		t.Fatalf("/steps should open the screen, got state %d", got.state)
	}
	s := got.screens.steps()
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
	if left := next.(Model); left.state == stateSteps || left.screens.steps() != nil {
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

// A compaction replaces the conversation that held the last steps call, so
// the list is put back under the summary the model carries on from.
func TestWorkSteps_ACompactionCarriesTheList(t *testing.T) {
	requests := 0
	m := compactingModel(&requests)
	m.noteStepsCall(stepsCall(`{"steps":[{"title":"Read","done":true},{"title":"Patch"}]}`))
	m.compacting, m.compactRun, m.streaming = true, &compactStart{pct: 83}, "the summary"
	updated, _ := m.finishCompact()
	m = updated.(Model)
	var summary string
	for _, msg := range m.agent.Messages() {
		if strings.HasPrefix(msg.Content, agent.CompactSummaryPrefix) {
			summary = msg.Content
		}
	}
	if !strings.Contains(summary, "the summary\n\n"+plan.CarriedStepsPrefix) || !strings.Contains(summary, "→ [ ] 2. Patch") {
		t.Fatalf("the list should stand under the summary, got %q", summary)
	}
	if m.compactSummary != "the summary" {
		t.Fatalf("the stored summary should be the model's own, got %q", m.compactSummary)
	}
}
