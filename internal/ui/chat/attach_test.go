package chat

// Sub-agent management and steering tests: the agent list, attach /
// detach, scoped commands, steering, mode clamping, and the [g] jump from a
// routed approval.

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/ui/components"
)

func key(r rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: r, Text: string(r)} }

// spawnBlockedChild spawns one researcher whose stream blocks until the child
// is cancelled, so it stays observable as "running".
func spawnBlockedChild(t *testing.T, sup *subagent.Supervisor) {
	t.Helper()
	exec := sup.WrapExecutor("", nil)
	if _, err := exec(subagent.SpawnToolName, json.RawMessage(`{"role":"researcher","task":"long survey"}`)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		st, ok := sup.Get("researcher-1")
		return ok && st.State == subagent.StateRunning
	})
}

// spawnChild spawns one child of a role and waits for it to reach the
// running state blockingEnv holds it at, so a test can build a map of several
// sessions without racing the supervisor.
func spawnChild(t *testing.T, sup *subagent.Supervisor, role subagent.Role, name string) {
	t.Helper()
	spawnUnder(t, sup, "", role, name)
}

// spawnUnder is spawnChild one level further down: the caller is the agent
// that spawns, so a test can build a map more than one level deep. "" is the
// session itself.
func spawnUnder(t *testing.T, sup *subagent.Supervisor, caller string, role subagent.Role, name string) {
	t.Helper()
	exec := sup.WrapExecutor(caller, nil)
	args := json.RawMessage(fmt.Sprintf(`{"role":%q,"task":"long survey"}`, role))
	if _, err := exec(subagent.SpawnToolName, args); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		st, ok := sup.Get(name)
		return ok && st.State == subagent.StateRunning
	})
}

// noteChild puts one entry into a child's transcript, which is how a test
// gives a child something to have been doing without a provider behind it.
func noteChild(t *testing.T, sup *subagent.Supervisor, name string, e subagent.TranscriptEntry) {
	t.Helper()
	if err := sup.Note(name, e); err != nil {
		t.Fatal(err)
	}
}

// killChild stops one child and waits for the supervisor to settle it, which
// is the deterministic way to get a session that has finished.
func killChild(t *testing.T, sup *subagent.Supervisor, name string) {
	t.Helper()
	if err := sup.Kill(name); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		st, ok := sup.Get(name)
		return ok && st.State == subagent.StateFailed
	})
}

// TestCycleAgentWalksTheMap: the chord moves the keyboard one row along the
// rail's map from wherever it is — the orchestrator included — and wraps at
// both ends, so a session is never more than a few presses away and there is
// no end of the list to get stuck at.
func TestCycleAgentWalksTheMap(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	spawnChild(t, sup, subagent.RoleResearcher, "researcher-1")
	spawnChild(t, sup, subagent.RoleReviewer, "reviewer-1")

	next := func(m Model) Model {
		t.Helper()
		updated, _ := m.Update(tea.KeyPressMsg{Code: ']', Mod: tea.ModAlt})
		return updated.(Model)
	}
	for _, want := range []string{"researcher-1", "reviewer-1", "", "researcher-1"} {
		m = next(m)
		if m.attachedTo != want {
			t.Fatalf("the cycle should have reached %q, got %q", want, m.attachedTo)
		}
	}
	// And back the other way, from wherever it left the keyboard.
	for _, want := range []string{"", "reviewer-1", "researcher-1"} {
		updated, _ := m.Update(tea.KeyPressMsg{Code: '[', Mod: tea.ModAlt})
		m = updated.(Model)
		if m.attachedTo != want {
			t.Fatalf("the reverse cycle should have reached %q, got %q", want, m.attachedTo)
		}
	}
}

// TestCycleAgentKeepsItsStopsAcrossAKill: a killed child stays where it was
// spawned on the map, so the chord from the child before it still lands on it
// and the one after it is still one further — two presses land where they
// landed before the kill.
func TestCycleAgentKeepsItsStopsAcrossAKill(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	spawnChild(t, sup, subagent.RoleResearcher, "researcher-1")
	spawnChild(t, sup, subagent.RoleResearcher, "researcher-2")
	spawnChild(t, sup, subagent.RoleResearcher, "researcher-3")

	stops := func(m Model) []string {
		t.Helper()
		m.attach("researcher-1")
		var got []string
		for range 2 {
			updated, _ := m.Update(tea.KeyPressMsg{Code: ']', Mod: tea.ModAlt})
			m = updated.(Model)
			got = append(got, m.attachedTo)
		}
		return got
	}
	before := stops(m)
	killChild(t, sup, "researcher-2")
	after := stops(m)
	if want := []string{"researcher-2", "researcher-3"}; !slices.Equal(before, want) || !slices.Equal(after, want) {
		t.Fatalf("the chord's stops from researcher-1 should be %v before and after the kill, got %v then %v", want, before, after)
	}
}

// TestCycleAgentWalksTheMapsRows: under a nested fan-out one press of the
// chord is one row down the rail's map — a grandchild is the stop after its
// own parent, not after the sibling spawned before it.
func TestCycleAgentWalksTheMapsRows(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	spawnChild(t, sup, subagent.RoleResearcher, "researcher-1")
	spawnChild(t, sup, subagent.RoleResearcher, "researcher-2")
	// Spawned last, drawn under its parent.
	spawnUnder(t, sup, "researcher-1", subagent.RoleReviewer, "reviewer-1")

	// The map's first row is the session itself, which the keyboard names "".
	rows := []string{""}
	for _, a := range m.inspectorAgents()[1:] {
		rows = append(rows, a.Name)
	}
	if want := []string{"", "researcher-1", "reviewer-1", "researcher-2"}; !slices.Equal(rows, want) {
		t.Fatalf("the map should draw the grandchild under its parent: %q", rows)
	}
	var stops []string
	for range len(rows) {
		updated, _ := m.Update(tea.KeyPressMsg{Code: ']', Mod: tea.ModAlt})
		m = updated.(Model)
		stops = append(stops, m.attachedTo)
	}
	// The last press wraps back to the orchestrator, the map's first row.
	if want := append(slices.Clone(rows[1:]), rows[0]); !slices.Equal(stops, want) {
		t.Fatalf("the chord stops at %q, the map draws %q", stops, rows)
	}
}

// TestCycleAgentKeepsEachSessionsScroll: moving through the map is a focus
// switch and nothing more, so every session comes back to the row it was
// left on rather than to the bottom of a transcript nobody asked to be at.
func TestCycleAgentKeepsEachSessionsScroll(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	spawnChild(t, sup, subagent.RoleResearcher, "researcher-1")
	for i := range 60 {
		m.appendEntry(entry{kind: entrySystem, text: fmt.Sprintf("row %d", i)})
	}
	m.viewport.SetLines(m.renderHistoryLines())
	m.viewport.GotoBottom()
	m.viewport.SetYOffset(7)
	m.atBottom = false

	updated, _ := m.Update(tea.KeyPressMsg{Code: ']', Mod: tea.ModAlt})
	m = updated.(Model)
	if m.attachedTo != "researcher-1" {
		t.Fatalf("the cycle should have attached to the child, got %q", m.attachedTo)
	}
	if m.parentView.yoffset != 7 || m.parentView.atBottom {
		t.Fatalf("the orchestrator's scroll should have been saved: %+v", m.parentView)
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: '[', Mod: tea.ModAlt})
	m = updated.(Model)
	if m.attachedTo != "" || m.viewport.YOffset() != 7 {
		t.Fatalf("coming back restores the row it was left on: %q at %d",
			m.attachedTo, m.viewport.YOffset())
	}
}

// A session on its own is not a map: the chord has nowhere to go and does
// nothing, rather than wrapping the orchestrator onto itself.
func TestCycleAgentIsInertWithoutChildren(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	updated, _ := m.Update(tea.KeyPressMsg{Code: ']', Mod: tea.ModAlt})
	if got := updated.(Model).attachedTo; got != "" {
		t.Fatalf("the keyboard should not have moved, got %q", got)
	}
}

func TestAgentListOpensAttachesAndDetaches(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	spawnBlockedChild(t, sup)

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'a', Mod: tea.ModAlt})
	m = updated.(Model)
	if m.agentList == nil {
		t.Fatal("the manager chord must open the agent list")
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
	if !strings.Contains(ansi.Strip(view), "[esc] detach") {
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

// TestEscPopsOneLevelOfTheSpawnTree: esc goes back to the agent that spawned
// the one you are in, not out of the tree altogether. From a child's child
// that is two presses, and the session the first one lands on is the one the
// breadcrumb names beside it and the one the map draws it under — three
// readings of the same link, asserted together so a spawn that stopped
// writing it could not leave any of them looking right on its own.
func TestEscPopsOneLevelOfTheSpawnTree(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	spawnChild(t, sup, subagent.RoleResearcher, "researcher-1")
	spawnUnder(t, sup, "researcher-1", subagent.RoleReviewer, "reviewer-1")

	m.attach("reviewer-1")
	if got, want := m.breadcrumb(), "orchestrator ▸ researcher-1 ▸ reviewer-1"; got != want {
		t.Fatalf("breadcrumb = %q, want %q", got, want)
	}
	if got := railDepth(m, "reviewer-1"); got != 2 {
		t.Fatalf("the map puts the grandchild at depth %d, want 2", got)
	}

	esc := func(m Model) Model {
		t.Helper()
		updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
		return updated.(Model)
	}
	if m = esc(m); m.attachedTo != "researcher-1" {
		t.Fatalf("the first esc should reach the agent that spawned it, got %q", m.attachedTo)
	}
	if got, want := m.breadcrumb(), "orchestrator ▸ researcher-1"; got != want {
		t.Fatalf("breadcrumb after one pop = %q, want %q", got, want)
	}
	if m = esc(m); m.attachedTo != "" {
		t.Fatalf("the second esc should reach the orchestrator, got %q", m.attachedTo)
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
	// In words on the rail, because what is waiting is a sentence the reader
	// typed here and not a queue of whatever else the frame counts.
	if !strings.Contains(m.View().Content, "queued steering: 1") {
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
	if !childTranscriptContains(sup, "researcher-1", "commands while attached") {
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
	// lands on read-only, the next mode in the cycle the ceiling allows; the
	// skipped modes are named as disabled.
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	m = updated.(Model)
	if mode, _ := sup.AgentMode("researcher-1"); mode != agent.ModeReadOnly {
		t.Fatalf("child mode = %s, want read-only", mode)
	}
	if !childTranscriptContains(sup, "researcher-1", "disabled") {
		t.Fatal("disabled over-ceiling modes not surfaced")
	}

	// /mode with an over-ceiling mode refuses instead of clamping silently.
	m.input.SetValue("/mode auto")
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if mode, _ := sup.AgentMode("researcher-1"); mode != agent.ModeReadOnly {
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

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'a', Mod: tea.ModAlt})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = updated.(Model)
	updated, _ = m.Update(key('X'))
	m = updated.(Model)
	if m.killConfirm == nil || len(m.killTargets) != 1 || m.killTargets[0] != "researcher-1" {
		t.Fatalf("X must arm the inline kill confirm (targets %q)", m.killTargets)
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

// [K] arms one confirm over every child that is still going, and taking it
// kills them all. The manager's two kill keys differ in how many names they
// hand over and in nothing else
// (docs/interface/surfaces.md#the-agent-manager).
func TestKillAllFromListWithOneConfirm(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	spawnChild(t, sup, subagent.RoleResearcher, "researcher-1")
	spawnChild(t, sup, subagent.RoleResearcher, "researcher-2")

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'a', Mod: tea.ModAlt})
	m = updated.(Model)
	updated, _ = m.Update(key('K'))
	m = updated.(Model)
	if len(m.killTargets) != 2 {
		t.Fatalf("K must arm the confirm over both children, got %q", m.killTargets)
	}
	if !strings.Contains(m.View().Content, "Kill all 2 agents?") {
		t.Fatalf("the confirm should name the count:\n%s", m.View().Content)
	}
	updated, _ = m.Update(key('y'))
	m = updated.(Model)
	for _, name := range []string{"researcher-1", "researcher-2"} {
		waitFor(t, func() bool {
			st, ok := sup.Get(name)
			return ok && st.State == subagent.StateFailed
		})
	}
	// One child left running is not two, so the key is not offered at all.
	if _, res := (&components.AgentList{Rows: []components.AgentRow{{State: components.AgentCurrent, Name: "orchestrator"}}}).
		Update(key('K')); res.Action != components.AgentNone {
		t.Fatalf("[K] with no children = %#v, want nothing", res)
	}
}

// [K] over a parent and the agent it spawned names the parent alone and lets
// the kill's cascade take the child, so the child ends once, as the parent's
// casualty, while the confirm still counts both
// (docs/capabilities/subagents.md#what-nesting-does-to-the-rest-of-it).
func TestKillAllHandsTheCascadeItsRoots(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	spawnChild(t, sup, subagent.RoleResearcher, "researcher-1")
	exec := sup.WrapExecutor("researcher-1", nil)
	if _, err := exec(subagent.SpawnToolName, json.RawMessage(`{"role":"researcher","task":"read it"}`)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		st, ok := sup.Get("researcher-2")
		return ok && st.State == subagent.StateRunning
	})

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'a', Mod: tea.ModAlt})
	m = updated.(Model)
	updated, _ = m.Update(key('K'))
	m = updated.(Model)
	if got := strings.Join(m.killTargets, ","); got != "researcher-1" {
		t.Fatalf("K must name the root alone, got %q", got)
	}
	if !strings.Contains(m.View().Content, "Kill all 2 agents?") {
		t.Fatalf("the confirm should count the whole roster:\n%s", m.View().Content)
	}
	updated, _ = m.Update(key('y'))
	_ = updated.(Model)
	waitFor(t, func() bool {
		st, ok := sup.Get("researcher-2")
		return ok && st.State == subagent.StateFailed
	})
	if st, _ := sup.Get("researcher-2"); st.Detail != "cancelled · researcher-1 was killed" {
		t.Fatalf("the child's ending = %q, want it named as the parent's casualty", st.Detail)
	}
	var cascade, direct int
	for _, e := range sup.Transcript("researcher-2") {
		switch e.Text {
		case "researcher-1 was killed, so this agent ends with it.":
			cascade++
		case "Killed by the user.":
			direct++
		}
	}
	if cascade != 1 || direct != 0 {
		t.Fatalf("the child's transcript says it was taken %d times and killed directly %d times, want 1 and 0",
			cascade, direct)
	}
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
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'a', Mod: tea.ModAlt})
	m = updated.(Model)
	if view := m.View().Content; !strings.Contains(view, "2 needs you") {
		t.Fatalf("the manager's title rail must state who needs you:\n%s", view)
	}
}

// TestManagerRowsFollowTheSpawnTree: the manager lists each agent followed by
// the agents it spawned, so a grandchild is the row under its own parent's
// rather than a sibling at the end of a flat list, and the depth it is
// indented by is the one the rail's map already draws it at.
func TestManagerRowsFollowTheSpawnTree(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	spawnChild(t, sup, subagent.RoleResearcher, "researcher-1")
	spawnChild(t, sup, subagent.RoleResearcher, "researcher-2")
	// Spawned last and drawn second: the tree and not the roster.
	spawnUnder(t, sup, "researcher-1", subagent.RoleReviewer, "reviewer-1")

	rows, names := m.buildAgentRows()
	if got, want := strings.Join(names, ","), ",researcher-1,reviewer-1,researcher-2"; got != want {
		t.Fatalf("rows in order %q, want %q", got, want)
	}
	for i, want := range []int{0, 1, 2, 1} {
		if rows[i].Depth != want {
			t.Fatalf("%s sits at depth %d, want %d", names[i], rows[i].Depth, want)
		}
	}
	// The same depth the rail's map indents the same session by, so the two
	// drawings of one tree cannot come to disagree.
	if got := railDepth(m, "reviewer-1"); got != rows[2].Depth {
		t.Fatalf("the map puts the grandchild at depth %d and the manager at %d", got, rows[2].Depth)
	}
	// And in the same order: the map is the manager's tree, so the corner the
	// grandchild draws hangs off its own parent's row.
	var mapped []string
	for _, a := range m.inspectorAgents() {
		mapped = append(mapped, a.Name)
	}
	if got, want := strings.Join(mapped[1:], ","), strings.Join(names[1:4], ","); got != want {
		t.Fatalf("the map draws %q, the manager lists %q", got, want)
	}
}

// railDepth is the depth the rail's map draws a session at, or -1 where the
// map has no row for it.
func railDepth(m Model, name string) int {
	for _, a := range m.inspectorAgents() {
		if a.Name == name {
			return a.Depth
		}
	}
	return -1
}

// gatedDescendantEnv runs a child of the session forever and parks anything
// deeper on an approval. It is the one shape a single behaviour cannot reach:
// a parent still working with a request waiting under it.
func gatedDescendantEnv() subagent.EnvFactory {
	blocking, gated := blockingEnv(), gatedEnv()
	return func(ctx context.Context, spec subagent.Spec) (subagent.Env, error) {
		if spec.Depth > subagent.SessionDepth+1 {
			return gated(ctx, spec)
		}
		return blocking(ctx, spec)
	}
}

// TestManagerFloatsABlockedGrandchildUnderItsParent: a request under a
// grandchild floats the whole group, so the row the reader has to answer is
// at the top of the list and still directly under the row it belongs to —
// never lifted out of the tree, where its corner would hang off nothing.
func TestManagerFloatsABlockedGrandchildUnderItsParent(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: gatedDescendantEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	spawnChild(t, sup, subagent.RoleResearcher, "researcher-1")
	spawnChild(t, sup, subagent.RoleResearcher, "researcher-2")
	// The second of them delegates, and the delegate parks on an approval.
	exec := sup.WrapExecutor("researcher-2", nil)
	if _, err := exec(subagent.SpawnToolName, json.RawMessage(`{"role":"researcher","task":"read it"}`)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { _, blocked := sup.ActiveCounts(); return blocked == 1 })

	rows, names := m.buildAgentRows()
	if got, want := strings.Join(names, ","), ",researcher-2,researcher-3,researcher-1"; got != want {
		t.Fatalf("rows in order %q, want %q", got, want)
	}
	if rows[1].State != components.AgentRunning || rows[2].State != components.AgentBlocked {
		t.Fatalf("the parent should still be running with the request under it: %v, %v",
			rows[1].State, rows[2].State)
	}
}

// TestAnswerBlockedChildFromTheList is the list's whole point: opening the
// manager because something needs you must not then send you into that
// child's session to say yes. [a] renders the card over the list and hands
// the list back.
func TestAnswerBlockedChildFromTheList(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: gatedEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	spawnInto(t, sup, `{"role":"researcher","task":"survey"}`)
	m = pumpAsks(t, m, sup, 1)

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'a', Mod: tea.ModAlt})
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
	if strings.Contains(ansi.Strip(view), "[enter] attach") {
		t.Fatalf("the list must step aside while the card is up:\n%s", view)
	}
	// Esc here leaves rather than declines, and the card says so once: the
	// surface underneath is the list, and the row the reader lands back on
	// goes on saying the child needs them
	// (docs/interface/principles.md#esc-is-always-the-safe-answer).
	plain := ansi.Strip(view)
	if strings.Count(plain, "[esc]") != 1 ||
		!strings.Contains(plain, "[esc] back to the agents — the decision stays waiting") {
		t.Fatalf("the card over the list states its own esc, once:\n%s", plain)
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
	if !strings.Contains(ansi.Strip(m.View().Content), "[enter] attach") {
		t.Fatalf("the list did not come back:\n%s", m.View().Content)
	}
}

// TestAnswerFromTheListLeavesTheDecisionWaitingOnEsc: esc on the card picked
// off the manager is a way back and not an answer. The request stays queued
// and the row it returns to still says the child needs somebody, which is
// what makes leaving safe — the decision is as visible on the list as it was
// on the card (docs/interface/principles.md#esc-is-always-the-safe-answer).
func TestAnswerFromTheListLeavesTheDecisionWaitingOnEsc(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: gatedEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	spawnInto(t, sup, `{"role":"researcher","task":"survey"}`)
	m = pumpAsks(t, m, sup, 1)

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'a', Mod: tea.ModAlt})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = updated.(Model)
	updated, _ = m.Update(key('a'))
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)

	if transcriptContains(m, "Declined researcher-1 ▸ run echo hi") {
		t.Fatal("esc is a way back, not a denial")
	}
	if m.agentList == nil || m.answerAgent != "" {
		t.Fatal("esc must return to the list")
	}
	if m.pendingAskFor("researcher-1") == nil {
		t.Fatal("the request must still be queued after esc")
	}
	if !strings.Contains(ansi.Strip(m.View().Content), "⚠ needs you") {
		t.Fatalf("the row must still say the child needs you:\n%s", ansi.Strip(m.View().Content))
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

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'a', Mod: tea.ModAlt})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = updated.(Model)
	view := m.View().Content
	if !strings.Contains(ansi.Strip(view), "[r] retry") {
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
	if !transcriptContains(m, "retrying researcher-1 on its original task") {
		t.Fatal("transcript missing the retry entry")
	}
}

// TestSteerAChildFromTheList: the redirect is typed on the row and sent from
// there, which is the same message attaching and typing sends — opening the
// manager because a child has drifted should not then send you into its
// session to say so.
func TestSteerAChildFromTheList(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	spawnBlockedChild(t, sup)

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'a', Mod: tea.ModAlt})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = updated.(Model)
	if view := m.View().Content; !strings.Contains(ansi.Strip(view), "[s] steer") {
		t.Fatalf("a live child's row must offer the redirect:\n%s", view)
	}

	updated, _ = m.Update(key('s'))
	m = updated.(Model)
	if view := m.View().Content; !strings.Contains(view, "steer researcher-1") {
		t.Fatalf("the field must name the child it will reach:\n%s", view)
	}
	for _, r := range "read the exporter instead" {
		updated, _ = m.Update(key(r))
		m = updated.(Model)
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)

	if m.agentList == nil {
		t.Fatal("sending a redirect must leave the list open")
	}
	if n := sup.QueuedSteering("researcher-1"); n != 1 {
		t.Fatalf("QueuedSteering = %d, want the redirect queued once", n)
	}
	if st, _ := sup.Get("researcher-1"); st.SteerFrom != subagent.SteerFromLane {
		t.Fatalf("the redirect came from %q, want the person's own source", st.SteerFrom)
	}
}

// Ending a child has one name. /exit while attached was a second one, spelled
// the way the whole session is quit everywhere else, so it ends nothing now
// and says where the act lives.
func TestAttachedExitEndsNothingAndSaysWhereKillIs(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	spawnBlockedChild(t, sup)
	m.attach("researcher-1")

	m.input.SetValue("/exit")
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)

	if st, ok := sup.Get("researcher-1"); !ok || st.State == subagent.StateFailed {
		t.Fatalf("/exit must not end the child, state = %v", st.State)
	}
	if m.attachedTo != "researcher-1" {
		t.Fatalf("attached to %q, want the surface unchanged", m.attachedTo)
	}
	if !childTranscriptContains(sup, "researcher-1", "agent manager") {
		t.Fatal("/exit should say where ending an agent lives")
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
	// until then [g] is a letter.
	m = handover(t, m)
	if view := m.View().Content; !strings.Contains(ansi.Strip(view), "[g] attach to researcher-1") {
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

// A child's refusal is drawn by the parent as the parent's own is: the row
// mirrors across whole, expansion and all, so a person reading the attached
// view sees the same two lines the session would have shown them.
func TestAttachedChildNoticeCarriesItsExpansion(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	spawnBlockedChild(t, sup)
	stale := tools.StaleError{Path: "/work/shhh/internal/agent/loop.go"}
	if err := sup.Note("researcher-1", subagent.TranscriptEntry{
		Kind: subagent.EntrySystem, Text: stale.Skipped("internal/agent/loop.go"), Result: stale.Error(),
	}); err != nil {
		t.Fatal(err)
	}

	m.attach("researcher-1")
	cv := m.syncChildView("researcher-1")
	idx := -1
	for i, e := range cv.entries {
		if e.kind == entrySystem && strings.HasPrefix(e.text, "skipped · ") {
			idx = i
		}
	}
	if idx == -1 {
		t.Fatalf("mirrored transcript missing the refusal row: %+v", cv.entries)
	}
	if !strings.Contains(cv.entries[idx].toolResult, "read_file it again") {
		t.Fatalf("the row lost its expansion in the mirror: %q", cv.entries[idx].toolResult)
	}
	// Folded it is one line; opened it is the sentence the model was given.
	if strings.Contains(m.renderAttachedHistory(), "read_file it again") {
		t.Error("a folded row must not already show its body")
	}
	cv.entries[idx].expanded = true
	if !strings.Contains(m.renderAttachedHistory(), "read_file it again") {
		t.Error("opening the row should show the sentence the model was given")
	}
}

// A child's auto-approval is drawn where the session's own is: on the act's
// row, in the outcome field, and nowhere else. The fan-out view was the last
// place in the interface that stated an act twice — a notice naming the call
// and then the row naming it again — and this is what says it no longer does.
func TestAttachedChildStatesAnAutoApprovedActOnce(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	spawnBlockedChild(t, sup)
	if err := sup.Note("researcher-1", subagent.TranscriptEntry{
		Kind: subagent.EntryTool, Tool: "exec_command",
		Args: `{"command":"go test ./..."}`, Result: "ok",
		AllowedBy: classifierRule, AllowElapsed: 2100 * time.Millisecond,
	}); err != nil {
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
		t.Fatalf("mirrored transcript missing the act: %+v", cv.entries)
	}
	// The same two fields the session's own call carries, so the same helper
	// draws the same account.
	if got := m.activityRowFor(cv.entries[idx]).Allowed; got != allowedLabel(classifierRule, 2100*time.Millisecond) {
		t.Fatalf("the mirrored row lost the account: %q", got)
	}
	got := stripANSI(m.renderAttachedHistory())
	if !strings.Contains(got, "auto-allowed · classifier 2.1s") {
		t.Fatalf("the attached view does not state what allowed the act:\n%s", got)
	}
	if n := strings.Count(got, "go test ./..."); n != 1 {
		t.Fatalf("the act is stated %d times, want once:\n%s", n, got)
	}
}

// A call the reader answered at the child's card is drawn with their answer
// on it, where a rule's answer would be. Without it the one decision in a
// fan-out that a person actually made is the only row with no account at all.
func TestAttachedChildNamesWhoAnsweredItsCard(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	spawnBlockedChild(t, sup)
	if err := sup.Note("researcher-1", subagent.TranscriptEntry{
		Kind: subagent.EntryTool, Tool: "exec_command",
		Args: `{"command":"go test ./..."}`, Result: "ok",
		ApprovedBy: subagent.ApprovedByUser,
	}); err != nil {
		t.Fatal(err)
	}

	m.attach("researcher-1")
	got := stripANSI(m.renderAttachedHistory())
	if !strings.Contains(got, "approved by you") {
		t.Fatalf("the mirrored row does not say who answered its card:\n%s", got)
	}
	if n := strings.Count(got, "go test ./..."); n != 1 {
		t.Fatalf("the act is stated %d times, want once:\n%s", n, got)
	}
}

// A child's public status is drawn where the session's own is: a rung under
// an answer, bounded, with every note before the current one folded to its
// first line. The mirror is rebuilt from the supervisor's entries on every
// sync rather than appended to, so which note is current is settled over the
// whole list — and a mirror that got that wrong would draw two current notes.
func TestAttachedChildDrawsItsStatusNotesAsNotes(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	spawnBlockedChild(t, sup)
	for _, e := range []subagent.TranscriptEntry{
		{Kind: subagent.EntryAssistant, Text: checkpointNote, Checkpoint: true},
		{Kind: subagent.EntryTool, Tool: "read_file", Args: `{"path":"loop.go"}`, Result: "lines"},
		{Kind: subagent.EntryAssistant, Text: checkpointNote, Checkpoint: true},
		{Kind: subagent.EntryAssistant, Text: "The counter is read at the top of the loop."},
	} {
		if err := sup.Note("researcher-1", e); err != nil {
			t.Fatal(err)
		}
	}

	m.attach("researcher-1")
	cv := m.syncChildView("researcher-1")
	var notes []entry
	for _, e := range cv.entries {
		if e.checkpoint {
			notes = append(notes, e)
		}
	}
	if len(notes) != 2 {
		t.Fatalf("the mirror carried %d status notes, want 2: %+v", len(notes), cv.entries)
	}
	if !notes[0].checkpointReplaced {
		t.Error("the first note was not retired by the one that replaced it")
	}
	if notes[1].checkpointReplaced {
		t.Error("the newest note was retired by nothing")
	}
	if cv.entries[len(cv.entries)-1].checkpoint {
		t.Error("the child's final answer was mirrored as a status note")
	}
	// A sync is a rebuild rather than an append, so the answer has to hold
	// across one: a mirror that settled it while appending would get it right
	// once and drift on every frame after.
	var again []bool
	for _, e := range m.syncChildView("researcher-1").entries {
		if e.checkpoint {
			again = append(again, e.checkpointReplaced)
		}
	}
	if len(again) != 2 || !again[0] || again[1] {
		t.Errorf("re-syncing the mirror changed which note is the current one: %v", again)
	}
	retired := lineCount(stripANSI(m.renderEntry(notes[0], 110)))
	current := lineCount(stripANSI(m.renderEntry(notes[1], 110)))
	if retired >= current {
		t.Errorf("the retired note is %d lines and the current one %d", retired, current)
	}
}

// lineCount is how many lines a rendered block spends.
func lineCount(s string) int {
	return len(strings.Split(strings.TrimRight(s, "\n"), "\n"))
}

// TestAttachedChildStreamsThroughItsOwnCache: the attached view redraws the
// message a child is writing on every frame, and parsing that message whole
// each time is quadratic in its length — the cost the parent's transcript
// stopped paying and the child went on paying. One cache per child, because
// two children write two different messages and a single cache between them
// would drop itself on every frame.
func TestAttachedChildStreamsThroughItsOwnCache(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	spawnChild(t, sup, subagent.RoleResearcher, "researcher-1")
	spawnChild(t, sup, subagent.RoleReviewer, "reviewer-1")
	m.attach("researcher-1")
	w := m.transcriptWidth()

	const opening = "First paragraph, finished.\n\nSecond paragraph, also finished.\n\n"
	const arriving = opening + "Third paragraph, still being written"

	got := m.renderChildHistory("researcher-1", opening)
	if want := renderMarkdown(opening, w); !strings.HasSuffix(got, want) {
		t.Fatalf("the child's in-flight message is not rendered as markdown:\n%q", got)
	}
	prefix := m.childViews["researcher-1"].stream.stablePrefix
	if prefix == "" {
		t.Fatal("the child's render took no stable prefix, so every frame parses the message whole")
	}

	// A second child writing a message of its own leaves the first one's
	// cache where it was.
	m.renderChildHistory("reviewer-1", "A different answer entirely.\n\nWith a second block.\n\n")
	if now := m.childViews["researcher-1"].stream.stablePrefix; now != prefix {
		t.Fatalf("one child's message dropped another's cache: %q became %q", prefix, now)
	}

	// And the glued render is the render of the whole message, which is the
	// contract the transcript depends on: the message freezes into a row
	// when the stream ends, rendered from the top.
	got = m.renderChildHistory("researcher-1", arriving)
	if want := renderMarkdown(arriving, w); !strings.HasSuffix(got, want) {
		t.Fatal("the cached prefix and the tail do not glue back to the whole message")
	}
}

// The manager over one of the session's own decisions: one panel holds one
// thing, so the list does not open — and the refusal is said, because a chord
// that does nothing at all is indistinguishable from a chord that is broken.
func TestAgentsOverAParkedCardSaysWhatHoldsThePanel(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	for _, tc := range []struct {
		state state
		names string
	}{
		{stateConfirmRun, "A command is waiting for an answer"},
		{statePlanApprove, "A plan is waiting for an answer"},
		{stateQuestion, "A question is waiting for an answer"},
	} {
		m := newSubagentModel(t, sup)
		m.state = tc.state
		opened, _ := m.openAgentList()
		m = opened.(Model)
		if m.agentList != nil {
			t.Fatalf("%v: the manager must not open over the panel's own decision", tc.state)
		}
		if !transcriptContains(m, tc.names) {
			t.Fatalf("%v: the refusal names what holds the panel", tc.state)
		}
		if !transcriptContains(m, "Answer it or press esc, then the agents open") {
			t.Fatalf("%v: the refusal names the way out", tc.state)
		}
	}
}

// Reading mode holds the panel but is no decision: the manager's chord leaves
// it and opens the list, rather than doing nothing at all.
func TestAgentsFromReadingModeOpensTheManager(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	m.appendEntry(entry{kind: entryCommand, text: "go test ./...", toolResult: "ok"})
	m.viewport.SetLines(m.renderHistoryLines())
	updated, _ := m.Update(readingChord())
	m = updated.(Model)
	if m.state != stateFocus {
		t.Fatalf("the reading chord should enter reading mode, got state %d", m.state)
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'a', Mod: tea.ModAlt})
	m = updated.(Model)
	if m.agentList == nil {
		t.Fatal("alt+a in reading mode must open the agent manager")
	}
	if m.state == stateFocus {
		t.Fatal("the manager opens once reading mode is left, not under it")
	}
}
