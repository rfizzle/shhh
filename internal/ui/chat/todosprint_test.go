package chat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/todo"
	"github.com/rfizzle/shhh/internal/todo/run"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// sprintModel is the backlog fixture with sizes on every item, so a size
// budget has something to spend, plus whatever sprint file the test wants.
func sprintModel(t *testing.T, sprint string) (Model, string) {
	t.Helper()
	root := t.TempDir()
	dir := todo.Dir(root)
	if err := os.MkdirAll(filepath.Join(dir, todo.DoneSubdir), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"a-high.md":    "---\ntitle: High one\npriority: high\nsize: S\n---\n",
		"b-second.md":  "---\ntitle: Second\npriority: high\nsize: M\n---\n",
		"c-third.md":   "---\ntitle: Third\npriority: medium\nsize: S\n---\n",
		"d-fourth.md":  "---\ntitle: Fourth\npriority: medium\nsize: M\n---\n",
		"e-waits.md":   "---\ntitle: Waits\npriority: low\nsize: S\ndepends_on: [a-high]\n---\n",
		"f-blocked.md": "---\ntitle: Stuck\nstatus: blocked\nsize: L\n---\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if sprint != "" {
		if err := os.WriteFile(filepath.Join(dir, todo.SprintFile), []byte(sprint), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return todoModel(t, root), root
}

func TestSprintPlan_ProposesTheReadyListUnderTheBudget(t *testing.T) {
	m, root := sprintModel(t, "")
	m.input.SetValue("/todo sprint plan --size S=2,M=1")
	updated, _ := m.submitInput()
	next := updated.(Model)
	if next.state != stateTodoPropose || next.todoPropose == nil {
		t.Fatalf("state = %v; the plan opens the card", next.state)
	}
	if strings.Join(next.todoSprintPlan, ",") != "a-high,b-second,c-third" {
		t.Fatalf("proposal = %v, want two S and one M in backlog order", next.todoSprintPlan)
	}
	for i, c := range next.todoPropose.Checked {
		if !c {
			t.Fatalf("option %d starts unchecked", i)
		}
	}
	// Each row states why it is in the set, in the facts a filter has.
	if meta := next.todoPropose.Options[0].Meta; meta != "high · S · unblocks 1" {
		t.Fatalf("first row's reason = %q", meta)
	}
	if label := next.todoPropose.Options[0].Label; label != "a-high · High one" {
		t.Fatalf("first row's label = %q", label)
	}

	// Trimming the set writes exactly what was left checked, in order.
	next.todoPropose.Checked[1] = false
	final, _ := next.routeOverlay(overlayFor(stateTodoPropose), tea.KeyPressMsg{Code: tea.KeyEnter})
	done := final.(Model)
	if done.state == stateTodoPropose {
		t.Fatal("enter should close the card")
	}
	sp, err := todo.LoadSprint(root)
	if err != nil || sp == nil {
		t.Fatalf("sprint = %v %v", sp, err)
	}
	if strings.Join(sp.Slugs, ",") != "a-high,c-third" || sp.Status != todo.SprintOpen {
		t.Fatalf("written sprint = %+v", sp)
	}
	if !strings.Contains(done.transcript[len(done.transcript)-1].text, "Wrote "+sp.Name) {
		t.Fatalf("note = %q", done.transcript[len(done.transcript)-1].text)
	}
}

func TestSprintPlan_WithNoBudgetIsTheWholeReadyList(t *testing.T) {
	m, _ := sprintModel(t, "")
	updated, _ := m.startTodoSprintPlan(nil)
	if got := strings.Join(updated.(Model).todoSprintPlan, ","); got != "a-high,b-second,c-third,d-fourth" {
		t.Fatalf("proposal = %q", got)
	}
}

func TestSprintPlan_RefusalsWriteNothing(t *testing.T) {
	m, root := sprintModel(t, "---\nname: caching\n---\ngoal\n\n## Items\n- a-high\n")
	updated, _ := m.startTodoSprintPlan(nil)
	if last := updated.(Model).transcript[len(updated.(Model).transcript)-1].text; !strings.Contains(last, "one sprint at a time") {
		t.Fatalf("second plan = %q", last)
	}

	bare, _ := sprintModel(t, "")
	updated, _ = bare.startTodoSprintPlan([]string{"--sze", "S=1"})
	if last := updated.(Model).transcript[len(updated.(Model).transcript)-1].text; !strings.HasPrefix(last, "Usage: /todo sprint plan") {
		t.Fatalf("mistyped flag = %q", last)
	}
	updated, _ = bare.startTodoSprintPlan([]string{"--size", "L=2"})
	if last := updated.(Model).transcript[len(updated.(Model).transcript)-1].text; !strings.Contains(last, "No ready item fits that budget") {
		t.Fatalf("budget nothing fits = %q", last)
	}
	if sp, _ := todo.LoadSprint(root); sp == nil || strings.Join(sp.Slugs, ",") != "a-high" {
		t.Fatal("a refusal changed the sprint on disk")
	}
}

func TestSprintPlan_CancelWritesNothing(t *testing.T) {
	m, root := sprintModel(t, "")
	updated, _ := m.startTodoSprintPlan(nil)
	final, _ := updated.(Model).routeOverlay(overlayFor(stateTodoPropose), tea.KeyPressMsg{Code: tea.KeyEscape})
	done := final.(Model)
	if !strings.Contains(done.transcript[len(done.transcript)-1].text, "no sprint was planned") {
		t.Fatalf("cancel note = %q", done.transcript[len(done.transcript)-1].text)
	}
	if _, err := os.Stat(todo.SprintPath(root)); !os.IsNotExist(err) {
		t.Fatal("cancelling wrote the sprint file")
	}
}

func TestSprintPlan_RefusedMidTurn(t *testing.T) {
	m, _ := sprintModel(t, "")
	m.setTurnState(stateStreaming)
	m.input.SetValue("/todo sprint plan")
	updated, cmd := m.submitInput()
	last := updated.(Model).transcript[len(updated.(Model).transcript)-1].text
	if cmd != nil || !strings.Contains(last, "Not while the turn is running") {
		t.Fatalf("note = %q", last)
	}
}

// The sprint goal is what an item's research stage is told the set is for.
// A goal nobody has written is the same as no sprint: nothing is sent.
func TestSprintGoal_IsSentOnlyWhenItWasWritten(t *testing.T) {
	m, root := sprintModel(t, "---\nname: caching\n---\n"+sprintGoalPlaceholder+"\n\n## Items\n- a-high\n")
	if got := m.sprintGoal(); got != "" {
		t.Fatalf("goal = %q, want nothing until one is written", got)
	}
	if err := todo.SprintSetGoal(todo.SprintPath(root), "Make the cache trustworthy."); err != nil {
		t.Fatal(err)
	}
	m.reloadTodos()
	if got := m.sprintGoal(); got != "Make the cache trustworthy." {
		t.Fatalf("goal = %q", got)
	}

	bare, _ := sprintModel(t, "")
	if got := bare.sprintGoal(); got != "" {
		t.Fatalf("goal with no sprint = %q", got)
	}
}

func TestInspectorTodo_SprintRowNamesTheSetAndItsProgress(t *testing.T) {
	m, root := sprintModel(t, "---\nname: caching\n---\ngoal\n\n## Items\n- a-high\n- c-third\n")
	block := m.inspectorTodo()
	if block.Sprint != "caching" || block.SprintDone != 0 || block.SprintTotal != 2 {
		t.Fatalf("sprint row = %+v", block)
	}
	rail := ansi.Strip(m.inspectorData().View(components.InspectorWidth, 0))
	if !strings.Contains(rail, "sprint caching") || !strings.Contains(rail, "0 of 2") {
		t.Fatalf("the rail should carry the sprint row:\n%s", rail)
	}

	if _, err := todo.Archive(root, "a-high", ""); err != nil {
		t.Fatal(err)
	}
	m.reloadTodos()
	if block := m.inspectorTodo(); block.SprintDone != 1 {
		t.Fatalf("progress = %d of %d", block.SprintDone, block.SprintTotal)
	}

	bare, _ := sprintModel(t, "")
	if block := bare.inspectorTodo(); block.Sprint != "" {
		t.Fatalf("sprint row without a sprint = %q", block.Sprint)
	}
}

// A run started under a sprint carries the goal into its checkpoint, so a
// stage sent from a later session still says what the set was for.
func TestTodoRun_CarriesTheSprintGoal(t *testing.T) {
	m, root := runModelAt(t, t.TempDir())
	if err := os.WriteFile(filepath.Join(todo.Dir(root), todo.SprintFile),
		[]byte("---\nname: caching\n---\nMake the cache trustworthy.\n\n## Items\n- do-it\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m.reloadTodos()
	m.input.SetValue("/todo run do-it")
	updated, _ := m.submitInput()
	next := updated.(Model)
	if next.todoRunner.state == nil || next.todoRunner.state.Sprint != "Make the cache trustworthy." {
		t.Fatalf("run = %+v", next.todoRunner.state)
	}
}

// Archiving the sprint's last item at the end of a run closes the sprint
// and says so on the row that closes the run.
func TestTodoRunDone_ClosesAFinishedSprint(t *testing.T) {
	m, root := runModelAt(t, t.TempDir())
	if err := os.WriteFile(filepath.Join(todo.Dir(root), todo.SprintFile),
		[]byte("---\nname: caching\n---\ngoal\n\n## Items\n- do-it\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m.reloadTodos()
	it, _ := m.todoStore.Find("do-it")
	m.todoRunner.item = it
	m.todoRunner.state = run.Start(it, "sess", "manual", 1, run.Options{NoCommit: true})
	m.todoRunner.state.Report = "## Report\nSummary: done.\n"
	m.todoRunner.state.Stage = run.StageDone
	updated, _ := m.todoRunDone()
	last := updated.(Model).transcript[len(updated.(Model).transcript)-1].text
	if !strings.Contains(last, "last item in the sprint") {
		t.Fatalf("close note = %q", last)
	}
	archived := filepath.Join(todo.Dir(root), todo.DoneSubdir, todo.SprintsSubdir, "caching.md")
	data, err := os.ReadFile(archived)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "### do-it") || !strings.Contains(string(data), "Summary: done.") {
		t.Fatalf("the archived sprint lacks the item's report:\n%s", data)
	}
}

// Bare `/todo sprint` reads and is allowed mid-turn; its verbs write and
// are not.
func TestSprintCommand_ReadsMidTurnAndItsVerbsDoNot(t *testing.T) {
	m, _ := sprintModel(t, "---\nname: caching\n---\ngoal\n\n## Items\n- a-high\n")
	m.setTurnState(stateStreaming)
	m.input.SetValue("/todo sprint")
	updated, _ := m.submitInput()
	if last := updated.(Model).transcript[len(updated.(Model).transcript)-1].text; last != "managed sprint" {
		t.Fatalf("bare sprint mid-turn = %q", last)
	}
	m.input.SetValue("/todo sprint close")
	updated, cmd := m.submitInput()
	last := updated.(Model).transcript[len(updated.(Model).transcript)-1].text
	if cmd != nil || !strings.Contains(last, "/todo sprint close changes the backlog files") {
		t.Fatalf("sprint close mid-turn = %q", last)
	}
}

// Two sprints planned on one day take different names, so the second can
// still be filed in the archive when it closes.
func TestSprintPlan_NamesTheSecondSprintOfADayApart(t *testing.T) {
	m, root := sprintModel(t, "")
	first, _ := m.startTodoSprintPlan(nil)
	m = first.(Model)
	final, _ := m.routeOverlay(overlayFor(stateTodoPropose), tea.KeyPressMsg{Code: tea.KeyEnter})
	m = final.(Model)
	sp, err := todo.LoadSprint(root)
	if err != nil || sp == nil {
		t.Fatalf("sprint = %v %v", sp, err)
	}
	name := sp.Name
	if _, err := todo.CloseSprint(root); err != nil {
		t.Fatal(err)
	}
	m.reloadTodos()

	second, _ := m.startTodoSprintPlan(nil)
	m = second.(Model)
	final, _ = m.routeOverlay(overlayFor(stateTodoPropose), tea.KeyPressMsg{Code: tea.KeyEnter})
	m = final.(Model)
	again, err := todo.LoadSprint(root)
	if err != nil || again == nil {
		t.Fatalf("second sprint = %v %v", again, err)
	}
	if again.Name == name {
		t.Fatalf("both sprints are named %q; the second could never be filed", name)
	}
	if _, err := todo.CloseSprint(root); err != nil {
		t.Fatalf("the second sprint could not be closed: %v", err)
	}
}

// The plan and the proposals share one card, so whichever opens it clears
// the other's list — a reading that landed over a plan would otherwise
// write the sprint's slugs against the proposals card's rows.
func TestSprintPlan_AndProposalsNeverHoldTheCardTogether(t *testing.T) {
	m, root := sprintModel(t, "")
	planned, _ := m.startTodoSprintPlan(nil)
	m = planned.(Model)
	if m.todoProposals != nil {
		t.Fatal("the plan card left proposals behind")
	}
	over, _ := m.openTodoProposals([]todo.Proposal{{Title: "Write the doc"}}, "1 proposed")
	m = over.(Model)
	if m.todoSprintPlan != nil {
		t.Fatal("a reading landed over the plan and kept its slugs")
	}
	final, _ := m.routeOverlay(overlayFor(stateTodoPropose), tea.KeyPressMsg{Code: tea.KeyEnter})
	if _, err := os.Stat(todo.SprintPath(root)); !os.IsNotExist(err) {
		t.Fatal("accepting the proposals card wrote a sprint")
	}
	if last := final.(Model).transcript[len(final.(Model).transcript)-1].text; !strings.Contains(last, "Wrote 1 backlog item") {
		t.Fatalf("note = %q", last)
	}
}

// A reading in flight will put its own card up the moment it finishes, so
// the plan waits rather than being replaced a moment after it appears.
func TestSprintPlan_WaitsForAReadingInFlight(t *testing.T) {
	m, _ := sprintModel(t, "")
	m.todoExtracting = true
	updated, _ := m.startTodoSprintPlan(nil)
	next := updated.(Model)
	if next.state == stateTodoPropose {
		t.Fatal("the plan card opened over a reading in flight")
	}
	if last := next.transcript[len(next.transcript)-1].text; !strings.Contains(last, "Still reading the session") {
		t.Fatalf("note = %q", last)
	}
}
