package chat

// The run's row: the projection of the machine's state the transcript draws,
// and the two ways it can lie — a tick on a stage nobody watched, and a word
// the record does not use.

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/todo"
	"github.com/rfizzle/shhh/internal/todo/run"
)

// noteText is the row's notes as a reader reads them.
func noteText(r *todoRunRow) string {
	var out []string
	for _, note := range r.notes() {
		out = append(out, stripANSI(note.text+note.painted))
	}
	return strings.Join(out, "\n")
}

// runRowOf is the row a session's run is drawn on.
func runRowOf(t *testing.T, m Model) *todoRunRow {
	t.Helper()
	idx := lastTodoRunRow(m.transcript)
	if idx < 0 {
		t.Fatal("the run should have a row in the transcript")
	}
	return m.transcript[idx].todorun
}

// stripWords reads the stage words off the rendered strip, so what the test
// asserts is what a reader sees rather than what the model holds.
func stripWords(r *todoRunRow) []string {
	var out []string
	for _, line := range r.stripLines(200) {
		for _, part := range strings.Split(stripANSI(line), " · ") {
			fields := strings.Fields(part)
			out = append(out, fields[len(fields)-1])
		}
	}
	return out
}

// The row's stage words are the words the record keys a transition on. They
// are asserted equal rather than compared by eye because the two are written
// in different files, and a row that invented a sixth word would read as a
// stage the record has never heard of.
func TestTodoRunRow_StageWordsAreTheRecordsWords(t *testing.T) {
	st := run.Start(todo.Item{Slug: "do-it", Profile: todo.BuiltinCode(), Fields: map[string]string{"size": "M"}}, "s", "manual", 1, run.Options{})
	got := stripWords(newTodoRunRow(st))
	if len(got) != len(run.Strip()) {
		t.Fatalf("the strip draws %d stages, the machine has %d: %v", len(got), len(run.Strip()), got)
	}
	for i, stage := range run.Strip() {
		want := run.Step{Action: run.ActionPrompt, Stage: stage}.Name()
		if got[i] != want {
			t.Errorf("strip word %d = %q, the record says %q", i, got[i], want)
		}
	}
}

// A run start to done leaves one row, and the row's strip follows it: the
// stage in flight is live, the ones behind it are ticked, and the report the
// run ends with is on the row rather than pasted under the notice.
func TestTodoRunRow_DrawnFromStartToDone(t *testing.T) {
	m, _ := runModel(t)
	m.input.SetValue("/todo run do-it")
	updated, _ := m.submitInput()
	m = updated.(Model)

	r := runRowOf(t, m)
	if r.marks[run.StageResearch] != runLive {
		t.Fatalf("research should be live at the start: %v", r.marks)
	}
	m = answer(t, m, runPlan)
	if r.marks[run.StageResearch] != runPassed || r.marks[run.StageImplement] != runLive {
		t.Fatalf("implement should be live with research ticked: %v", r.marks)
	}
	m = answer(t, m, "Changed a.go.")
	updated, _ = m.Update(todoVerifyMsg{slug: "do-it", ok: true, output: "ok"})
	m = updated.(Model)
	if r.marks[run.StageVerify] != runPassed || r.marks[run.StageReview] != runLive {
		t.Fatalf("a passing verify ticks and hands on to review: %v", r.marks)
	}
	m = answer(t, m, "verdict: clean")
	m = answer(t, m, "COMMIT:\nfix(a): do it\nREPORT:\nSummary: did it.\n")
	updated, _ = m.Update(todoCommitMsg{slug: "do-it", files: []string{"a.go"}})
	m = updated.(Model)

	if m.todoRunner.state != nil {
		t.Fatal("the run should be over")
	}
	if lastTodoRunRow(m.transcript) < 0 {
		t.Fatal("the row outlives the run it drew")
	}
	for _, stage := range run.Strip() {
		if r.marks[stage] != runPassed {
			t.Errorf("%s should be ticked on a finished run: %v", stage, r.marks)
		}
	}
	if r.st.Verdict != "clean" || r.st.Report == "" {
		t.Fatalf("the row should carry the review's word and the report: %+v", r.st)
	}
	if note := m.transcript[len(m.transcript)-1].text; strings.Contains(note, "Summary: did it.") {
		t.Errorf("the report belongs to the row, not under the notice: %q", note)
	}
	opened := strings.Join(strings.Split(stripANSI(m.todoRunRowView(
		entry{kind: entryTodoRun, todorun: r, expanded: true}, 130, false)), "\n"), "\n")
	for _, want := range []string{"1. Change a.go", "report", "Summary: did it.", "a.go"} {
		if !strings.Contains(opened, want) {
			t.Errorf("the opened row does not carry %q:\n%s", want, opened)
		}
	}
}

// A run picked up from a checkpoint draws the stages it skipped as restored
// rather than as passed: a tick claims the row watched the stage happen, and
// this row watched none of them.
func TestTodoRunRow_ContinuedDrawsRestoredNotPassed(t *testing.T) {
	m, root := runModel(t)
	it, ok := m.todoStore.Find("do-it")
	if !ok {
		t.Fatal("fixture")
	}
	if err := todo.SetStatus(it.Path, todo.StatusInProgress); err != nil {
		t.Fatal(err)
	}
	st := run.Start(it, "an earlier session", "manual", 1, run.Options{Repo: true})
	st.Stage, st.Plan, st.Steps = run.StageVerify, runPlan, []string{"Change a.go"}
	if err := st.Save(root); err != nil {
		t.Fatal(err)
	}
	m.reloadTodos()
	m.input.SetValue("/todo run do-it")
	updated, _ := m.submitInput()
	m = updated.(Model)

	r := runRowOf(t, m)
	for _, stage := range []run.Stage{run.StageResearch, run.StageImplement} {
		if r.marks[stage] != runRestored {
			t.Errorf("%s was done in another session and must not be ticked: %v", stage, r.marks)
		}
	}
	line := noteText(r)
	if !strings.Contains(line, "restored from the checkpoint: research · implement") {
		t.Errorf("the row does not say in words what the glyph means: %q", line)
	}
}

// /todo status opens the run's row and puts the keyboard on it, rather than
// printing a second account of the same facts beside it.
func TestTodoStatus_OpensTheRunRow(t *testing.T) {
	m, _ := runModel(t)
	m.input.SetValue("/todo run do-it")
	updated, _ := m.submitInput()
	m = updated.(Model)

	m.input.SetValue("/todo status")
	updated, _ = m.submitInput()
	m = updated.(Model)
	idx := lastTodoRunRow(m.transcript)
	if !m.transcript[idx].expanded {
		t.Error("status should open the row's detail")
	}
	if m.state != stateFocus || m.focusIdx != idx {
		t.Errorf("status should put the keyboard on the row: state=%d focus=%d row=%d", m.state, m.focusIdx, idx)
	}
}

// A blocked run's row names the block and the follow-up it wrote, and [o]
// reopens the item from it.
func TestTodoRunRow_BlockedNamesTheFollowUpAndReopens(t *testing.T) {
	m, root := runModel(t)
	m.input.SetValue("/todo run do-it")
	updated, _ := m.submitInput()
	m = updated.(Model)
	r := runRowOf(t, m)

	m = answer(t, m, "I had a look and there is no plan to give.\n")
	if m.state != stateTodoPropose {
		t.Fatalf("a block offers a follow-up: state=%d", m.state)
	}
	if it, _ := todo.Load(todo.BuiltinCode(), root).Find("do-it"); it.Status != todo.StatusBlocked {
		t.Fatalf("the item should be blocked, is %s", it.Status)
	}
	if got := noteText(r); !strings.Contains(got, "blocked  research produced no numbered plan") {
		t.Errorf("the row does not name the block: %q", got)
	}

	// Accept the follow-up: the item it wrote is named on the row that
	// blocked, which is where the reader is when they ask what came of it.
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if r.followUp == "" {
		t.Fatal("the accepted follow-up should be named on the blocked row")
	}
	if _, ok := m.todoStore.Find(r.followUp); !ok {
		t.Fatalf("the follow-up %q the row names should be in the backlog", r.followUp)
	}

	// The row's own offer, live under reading mode's cursor.
	idx := lastTodoRunRow(m.transcript)
	if len(r.offers()) != 1 {
		t.Fatalf("a blocked row offers the reopen and nothing else: %+v", r.offers())
	}
	next, _, claimed := m.todoRunReopen(idx)
	if !claimed {
		t.Fatal("the key should be claimed on a blocked run's row")
	}
	if it, _ := todo.Load(todo.BuiltinCode(), root).Find("do-it"); it.Status != todo.StatusOpen {
		t.Fatalf("the item should be open again, is %s", it.Status)
	}
	if note := next.(Model).transcript[len(next.(Model).transcript)-1].text; !strings.Contains(note, "is open again") {
		t.Errorf("reopening should say so: %q", note)
	}
	// A row that did not block offers nothing, and the letter goes back to
	// the draft.
	m.transcript[idx].todorun.st.Stage = run.StageDone
	if _, _, claimed := m.todoRunReopen(idx); claimed {
		t.Error("only a blocked run's row answers the key")
	}
}

// The remediation note outlives the stage it is about — a run that spent a
// round says so after it finishes — so it says which round is in flight only
// while one is. `round 1/2` beside a finished run would claim one still was.
func TestTodoRunRow_RoundNoteSaysSpentOnceTheStageIsPast(t *testing.T) {
	st := run.Start(todo.Item{Slug: "do-it", Profile: todo.BuiltinCode(), Fields: map[string]string{"size": "M"}}, "s", "manual", 1, run.Options{})
	st.Round, st.Stage = 1, run.StageRemediate
	r := newTodoRunRow(st)
	if got := noteText(r); !strings.Contains(got, "remediate  round 1/2") {
		t.Errorf("mid-round the note names the round in flight: %q", got)
	}
	st.Stage = run.StageDone
	if got := noteText(r); !strings.Contains(got, "remediate  1/2 rounds spent") {
		t.Errorf("after the round the note is what it cost: %q", got)
	}
}

// An opened stage answer is bounded and says what it left out. A failing
// verify's output runs to forty lines and already has a row of its own; the
// row that opens must not become the wall of text it replaced.
func TestTodoRunRow_OpenedAnswersAreBounded(t *testing.T) {
	st := run.Start(todo.Item{Slug: "do-it", Profile: todo.BuiltinCode(), Fields: map[string]string{"size": "M"}}, "s", "manual", 1, run.Options{})
	lines := make([]string, 40)
	for i := range lines {
		lines[i] = fmt.Sprintf("--- FAIL: TestCase%d", i)
	}
	st.Findings = "Verification failed:\n" + strings.Join(lines, "\n")
	r := newTodoRunRow(st)

	var body []string
	for _, l := range r.answers() {
		body = append(body, l.text)
	}
	if len(body) > runAnswerLines+2 {
		t.Fatalf("the opened answer is unbounded: %d lines", len(body))
	}
	if last := body[len(body)-1]; !strings.HasPrefix(last, "… ") || !strings.Contains(last, "more lines") {
		t.Errorf("a bounded answer has to count what it left out, got %q", last)
	}
}

// The row folds and opens the way a step does: one press of reading mode's
// expand key on the row under the cursor, and the same press again gives the
// fold back.
func TestTodoRunRow_EnterFoldsAndOpensLikeAStep(t *testing.T) {
	m, _ := runModel(t)
	m.input.SetValue("/todo run do-it")
	updated, _ := m.submitInput()
	m = updated.(Model)
	idx := lastTodoRunRow(m.transcript)

	m.state = stateFocus
	m.focusIdx = idx
	if !slices.Contains(m.expandableIndices(), idx) {
		t.Fatal("the run's row should be a row the reading cursor can stand on")
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if !m.transcript[idx].expanded {
		t.Fatal("the first press should open the row")
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m := updated.(Model); m.transcript[idx].expanded {
		t.Fatal("the same press should fold it back")
	}
}

// The rail draws the item a run is working first, whatever its place in the
// backlog's order, and a large item's lanes as the meter the fan-out uses.
func TestInspectorTodo_RunningItemFirstWithItsLanes(t *testing.T) {
	m, root := runModel(t)
	for _, name := range []string{"aaa-first.md", "bbb-second.md"} {
		if err := os.WriteFile(filepath.Join(todo.Dir(root), name),
			[]byte("---\ntitle: Another\npriority: high\nsize: S\n---\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m.reloadTodos()
	if m.todoStore.Items[0].Slug == "do-it" {
		t.Fatal("fixture: do-it should not already be first in backlog order")
	}
	m.input.SetValue("/todo run do-it")
	updated, _ := m.submitInput()
	m = updated.(Model)
	m.todoRunner.state.Lanes = []run.Lane{{Name: "a", Done: true}, {Name: "b"}, {Name: "c"}}

	block := m.inspectorTodo()
	if block == nil || block.Rows[0].Slug != "do-it" {
		t.Fatalf("the running item should lead the block: %+v", block)
	}
	if block.Rows[0].LanesDone != 1 || block.Rows[0].LanesTotal != 3 {
		t.Errorf("the row should carry the lanes: %+v", block.Rows[0])
	}
	for _, row := range block.Rows[1:] {
		if row.LanesTotal != 0 {
			t.Errorf("an item no run is working has no lanes: %+v", row)
		}
	}
}
