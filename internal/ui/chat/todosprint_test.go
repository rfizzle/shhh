package chat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/reports"
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
	if next.state != stateBacklog || next.backlog == nil || next.backlog.Plan == nil {
		t.Fatalf("state = %v; the plan opens the sprint tab", next.state)
	}
	if got := strings.Join(planSlugs(next), ","); got != "a-high,b-second,c-third" {
		t.Fatalf("proposal = %v, want two S and one M in backlog order", got)
	}
	for i, r := range next.backlog.Plan.Rows {
		if r.Dropped {
			t.Fatalf("row %d starts dropped", i)
		}
	}
	// Each row states why it is in the set, in the facts a filter has.
	if note := next.backlog.Plan.Rows[0].Note; note != "high · S · unblocks 1" {
		t.Fatalf("first row's reason = %q", note)
	}
	if r := next.backlog.Plan.Rows[0]; r.Slug != "a-high" || r.Title != "High one" {
		t.Fatalf("first row = %+v", r)
	}
	// The budget is in the header, because a proposal whose filter is not
	// stated reads as a recommendation.
	if view := ansi.Strip(next.backlog.View(110)); !strings.Contains(view, "S=2 M=1") {
		t.Fatalf("the header never states the budget:\n%s", view)
	}

	// Trimming the set writes exactly what was left, in order.
	dropped, _ := next.updateTodoScreen(key('j'))
	dropped2, _ := dropped.(Model).updateTodoScreen(key(' '))
	final, _ := dropped2.(Model).updateTodoScreen(tea.KeyPressMsg{Code: tea.KeyEnter})
	done := final.(Model)
	if done.backlog.Plan != nil || done.sprintPlan != nil {
		t.Fatal("taking the card left the proposal up")
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
	if got := strings.Join(planSlugs(updated.(Model)), ","); got != "a-high,b-second,c-third,d-fourth" {
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
	final, _ := updated.(Model).updateTodoScreen(tea.KeyPressMsg{Code: tea.KeyEscape})
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
	var published reports.Document
	m.todos.PublishReport = func(doc reports.Document) (string, error) {
		published = doc
		return "http://127.0.0.1:8731/r/rp-0123456789abcdef", nil
	}
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
	// The page is written from the backlog as the archive left it, not from
	// the copy this session was holding a line earlier.
	if len(published.Blocks) < 3 || published.Blocks[1].Stats[0].Value != "1" {
		t.Fatalf("the page did not count the item the run just finished: %+v", published.Blocks)
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
	final, _ := m.updateTodoScreen(tea.KeyPressMsg{Code: tea.KeyEnter})
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
	final, _ = m.updateTodoScreen(tea.KeyPressMsg{Code: tea.KeyEnter})
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

// planSlugs is the proposal's slugs in the order the card draws them.
func planSlugs(m Model) []string {
	if m.sprintPlan == nil {
		return nil
	}
	out := make([]string, len(m.sprintPlan.Rows))
	for i, r := range m.sprintPlan.Rows {
		out[i] = r.Slug
	}
	return out
}

// The proposals card and the plan are two surfaces now, and taking one
// answers only its own list: a reading that landed while a plan was up used
// to write the sprint's slugs against the proposals card's rows.
func TestSprintPlan_AndProposalsAnswerTheirOwnLists(t *testing.T) {
	m, root := sprintModel(t, "")
	planned, _ := m.startTodoSprintPlan(nil)
	m = planned.(Model)
	if m.todoProposals != nil {
		t.Fatal("the plan left proposals behind")
	}
	over, _ := m.openTodoProposals([]todo.Proposal{{Title: "Write the doc"}}, "1 proposed")
	m = over.(Model)
	final, _ := m.routeOverlay(overlayFor(stateTodoPropose), tea.KeyPressMsg{Code: tea.KeyEnter})
	if _, err := os.Stat(todo.SprintPath(root)); !os.IsNotExist(err) {
		t.Fatal("accepting the proposals card wrote a sprint")
	}
	if last := final.(Model).transcript[len(final.(Model).transcript)-1].text; !strings.Contains(last, "Wrote 1 backlog item") {
		t.Fatalf("note = %q", last)
	}
	if got := strings.Join(planSlugs(final.(Model)), ","); got == "" {
		t.Fatal("the proposals card took the plan with it")
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

// The board is the sprint file placed against the backlog: the goal, the
// set's slugs in the file's order with where each stands, and the progress
// the meter draws.
func TestSprintBoard_IsTheFileAgainstTheBacklog(t *testing.T) {
	m, root := sprintModel(t, "---\nname: caching\n---\nMake it fast.\n\n## Items\n- a-high\n- e-waits\n- f-blocked\n- gone\n")
	board := m.sprintBoard()
	if board == nil || board.Name != "caching" || board.Goal != "Make it fast." {
		t.Fatalf("board = %+v", board)
	}
	if board.Done != 0 || board.Total != 3 {
		t.Fatalf("progress = %d of %d; a slug the backlog dropped leaves the ratio", board.Done, board.Total)
	}
	var notes []string
	for _, r := range board.Rows {
		notes = append(notes, r.Slug+"="+r.Note)
	}
	if strings.Join(notes, ",") != "a-high=ready,e-waits=waits on a-high,f-blocked=blocked,gone=dropped from the backlog" {
		t.Fatalf("rows = %v", notes)
	}
	// A blocked item puts its evidence on the board, because a sprint stops
	// on the first block and attempts nothing after it.
	must(t, todo.Append(filepath.Join(todo.Dir(root), "f-blocked.md"), "## Blocked\nThe API is not decided.\n"))
	m.reloadTodos()
	if got := m.sprintBoard().Stopped; got != "f-blocked blocked — The API is not decided." {
		t.Fatalf("stopped = %q", got)
	}

	bare, _ := sprintModel(t, "")
	if bare.sprintBoard() != nil {
		t.Fatal("a project with no sprint drew a board")
	}
}

// The tab says how far through the set the sprint is and which item it is
// on, because those are the two things a reader with eight windows open is
// asking of the one they left running.
func TestSprintTitle_SaysTheCountAndTheItem(t *testing.T) {
	m, _ := sprintModel(t, "---\nname: caching\n---\ngoal\n\n## Items\n- a-high\n- c-third\n- d-fourth\n")
	if got := m.sprintTitle(); got != "" {
		t.Fatalf("title with no run = %q", got)
	}
	it, _ := m.todoStore.Find("a-high")
	m.todoRunner.state = run.Start(it, "sess", "manual", 1, run.Options{Sprint: "goal", InSprint: true})
	m.todoRunner.state.Stage = run.StageImplement
	if got := m.sprintTitle(); got != "sprint 0/3 · a-high" {
		t.Fatalf("title = %q", got)
	}
	m.windowTitleOn = true
	m.caps.Asked = true
	if got := m.windowTitle(); !strings.HasPrefix(got, "sprint 0/3 · a-high") {
		t.Fatalf("window title = %q", got)
	}
}

// The summons names the item that finished and how far the set has got: a
// reader who left a sprint running and came back to one line about a turn
// would have to look up which of thirty items it was.
func TestSprintCloseWords_NameTheItemThatFinished(t *testing.T) {
	m, root := sprintModel(t, "---\nname: caching\n---\ngoal\n\n## Items\n- a-high\n")
	it, _ := m.todoStore.Find("a-high")
	m.todoRunner.state = run.Start(it, "sess", "manual", 1, run.Options{InSprint: true})
	m.todoRunner.state.Stage = run.StageDone
	sp := run.StartSprint("sess", "manual", 0, true)
	sp.Finished("a-high")
	must(t, sp.Save(root))
	if got := m.sprintCloseWords(); got != "finished a-high · sprint 1 item done" {
		t.Fatalf("words = %q", got)
	}
}

// The first row on the far side of a session boundary says which item comes
// next, because everything else the boundary carried is gone by design.
func TestSprintNextNote_NamesTheItemComingUp(t *testing.T) {
	m, _ := sprintModel(t, "")
	sp := run.StartSprint("sess", "manual", 0, true)
	if got := sprintNextNote(sp, m.todoStore); !strings.Contains(got, "Next in the sprint: a-high · High one") {
		t.Fatalf("note = %q", got)
	}
	capped := run.StartSprint("sess", "manual", 1, true)
	capped.Attempts = []string{"a-high"}
	if got := sprintNextNote(capped, m.todoStore); !strings.Contains(got, "Nothing is left") {
		t.Fatalf("capped note = %q", got)
	}
}

// A closed sprint's report is a page: the goal, what it cost, every item
// with what it produced, and what stopped the rest.
func TestSprintReport_IsAPageOfTheSameBlocks(t *testing.T) {
	m, root := sprintModel(t, "---\nname: caching\n---\nMake it fast.\n\n## Items\n- a-high\n- f-blocked\n")
	must(t, todo.Append(filepath.Join(todo.Dir(root), "f-blocked.md"), "## Blocked\nThe API is not decided.\n"))
	must(t, todo.Append(filepath.Join(todo.Dir(root), "a-high.md"), "## Report\nAdded the lifetime.\n"))
	m.reloadTodos()
	if _, err := todo.Archive(root, "a-high", ""); err != nil {
		t.Fatal(err)
	}
	m.reloadTodos()
	doc := sprintReportDoc(m.todoStore.Sprint, m.todoStore.SprintEntries(), 12, 1.42)
	if err := doc.Validate(); err != nil {
		t.Fatalf("the page does not validate: %v", err)
	}
	if doc.Title != "Sprint caching" {
		t.Fatalf("title = %q", doc.Title)
	}
	var kinds []string
	for _, b := range doc.Blocks {
		kinds = append(kinds, b.Type)
	}
	if strings.Join(kinds, ",") != "prose,stats,table,prose,prose" {
		t.Fatalf("blocks = %v", kinds)
	}
	table := doc.Blocks[2]
	if len(table.Rows) != 2 || table.Rows[0][0] != "a-high" || table.Rows[0][2] != "Added the lifetime." {
		t.Fatalf("items table = %v", table.Rows)
	}
	// The report goes on the page whole as well as one line into the cell,
	// because a run names its commit in the prose of it.
	if !strings.Contains(doc.Blocks[3].Text, "a-high — Added the lifetime.") {
		t.Fatalf("what each item produced = %q", doc.Blocks[3].Text)
	}
	if !strings.Contains(doc.Blocks[4].Text, "The API is not decided.") {
		t.Fatalf("what stopped = %q", doc.Blocks[4].Text)
	}
	// It is a page and not only a document: what the reader opens has to
	// carry the goal, the items and the evidence through the renderer.
	page, err := reports.Render(doc, reports.Meta{Title: doc.Title}, "rp-0123456789abcdef")
	if err != nil {
		t.Fatalf("the page would not render: %v", err)
	}
	for _, want := range []string{"Sprint caching", "Make it fast.", "a-high", "f-blocked", "The API is not decided."} {
		if !strings.Contains(string(page), want) {
			t.Errorf("the page never says %q", want)
		}
	}
}

// Closing the sprint publishes the page and the board's last row offers it,
// which is the only place the link survives the file being archived.
func TestSprintClose_PublishesThePageAndTheBoardOffersIt(t *testing.T) {
	m, root := sprintModel(t, "---\nname: caching\n---\nMake it fast.\n\n## Items\n- a-high\n")
	// No reload between the archive and the close: that is the sequence the
	// run itself takes, and a page built from the session's stale copy of
	// the backlog would report the item that just finished as unfinished.
	var published reports.Document
	m.todos.PublishReport = func(doc reports.Document) (string, error) {
		published = doc
		return "http://127.0.0.1:8731/r/rp-0123456789abcdef", nil
	}
	if _, err := todo.Archive(root, "a-high", "## Report\nDone.\n"); err != nil {
		t.Fatal(err)
	}
	note := m.closeFinishedSprint()
	if !strings.Contains(note, "http://127.0.0.1:8731/r/rp-0123456789abcdef") {
		t.Fatalf("close note = %q", note)
	}
	if len(published.Blocks) < 3 || published.Blocks[1].Stats[0].Value != "1" {
		t.Fatalf("the page did not count the item that closed the sprint: %+v", published.Blocks)
	}
	if row := published.Blocks[2].Rows[0]; row[1] != string(todo.SprintItemDone) || row[2] != "Done." {
		t.Fatalf("the finished item's row = %v", row)
	}
	board := m.sprintBoard()
	if board == nil || !board.Closed || board.Report != "http://127.0.0.1:8731/r/rp-0123456789abcdef" {
		t.Fatalf("board = %+v", board)
	}
	opened, _ := m.openTodoScreen()
	view := ansi.Strip(opened.(Model).backlogPane(110, 24))
	if !strings.Contains(view, "[tab] the sprint") {
		t.Fatalf("the closed sprint has no tab to step onto:\n%s", view)
	}
}

// The goal key hands the keyboard back with the command in the box, and the
// sentence goes on the proposal: there is no sprint file to edit until the
// card is taken.
func TestSprintPlan_GoalGoesOnTheProposal(t *testing.T) {
	m, root := sprintModel(t, "")
	opened, _ := m.startTodoSprintPlan(nil)
	m = opened.(Model)
	asked, _ := m.updateTodoScreen(key('g'))
	m = asked.(Model)
	if m.input.Value() != sprintGoalPrefix {
		t.Fatalf("draft = %q", m.input.Value())
	}
	if m.sprintPlan == nil {
		t.Fatal("the proposal died with the surface")
	}
	m.input.SetValue(sprintGoalPrefix + "Make the cache trustworthy.")
	back, _ := m.submitInput()
	m = back.(Model)
	if m.backlog == nil || m.backlog.Plan == nil || m.backlog.Plan.Goal != "Make the cache trustworthy." {
		t.Fatalf("the goal did not land on the proposal: %+v", m.backlog)
	}
	taken, _ := m.updateTodoScreen(tea.KeyPressMsg{Code: tea.KeyEnter})
	sp, err := todo.LoadSprint(root)
	if err != nil || sp == nil {
		t.Fatalf("sprint = %v %v", sp, err)
	}
	if sp.Goal != "Make the cache trustworthy." {
		t.Fatalf("written goal = %q", sp.Goal)
	}
	if taken.(Model).sprintPlan != nil {
		t.Fatal("taking the card left the proposal on the session")
	}
}
