package chat

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/todo"
	"github.com/rfizzle/shhh/internal/todo/run"
)

const runPlan = "## Plan: do it\n\n1. Change a.go\n   files: a.go\n   action: edit\n\nsize: S\nquestions: none\n"

func runModel(t *testing.T) (Model, string) {
	t.Helper()
	return runModelAt(t, t.TempDir())
}

// runModelAt is runModel with the session's root chosen by the caller, so a
// test can hand it a root that reaches the same directory by another name.
//
// The root is made to look like a repository. A run ends in a commit and
// refuses a directory with none, and the .git entry is what that refusal
// reads — an empty directory is enough for it, so the fixture costs no git
// binary and no seeded history.
func runModelAt(t *testing.T, root string) (Model, string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	dir := todo.Dir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "do-it.md"), []byte("---\ntitle: Do it\nsize: M\n---\n## Tests\n- true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := frameModel(t, 130, 40)
	m.changes = changeset.New(1 << 20)
	m.policy.mode = agent.ModeManual
	m = m.WithTodos(Todos{Profile: todo.BuiltinCode(), Root: root, Manage: func([]string) string { return "" }, Detail: func(*todo.Store, todo.Item) string { return "" }})
	return m, root
}

// answer ends the in-flight stage turn with text as the assistant's reply.
func answer(t *testing.T, m Model, text string) Model {
	t.Helper()
	if !m.working() {
		t.Fatalf("no stage turn in flight (state %d)", m.state)
	}
	m.streaming = text
	updated, _ := m.Update(doneMsg{})
	return updated.(Model)
}

func TestTodoRun_StagesInOrder(t *testing.T) {
	m, root := runModel(t)
	m.input.SetValue("/todo run do-it")
	updated, _ := m.submitInput()
	m = updated.(Model)
	if m.todoRunner.state == nil || m.todoRunner.state.Stage != run.StageResearch || m.policy.mode != agent.ModePlan || !m.working() {
		t.Fatalf("research should be in flight in plan mode: run=%+v mode=%s", m.todoRunner.state, m.policy.mode)
	}
	if it, _ := todo.Load(todo.BuiltinCode(), root).Find("do-it"); it.Status != todo.StatusInProgress {
		t.Fatal("the item should be in progress")
	}
	if _, err := run.Load(root, "do-it"); err != nil {
		t.Fatal("no checkpoint written")
	}
	if last := m.transcript[len(m.transcript)-1]; last.kind != entryUser || !strings.Contains(last.text, "research") {
		t.Fatalf("the shown line should be the stage label, got %+v", last)
	}

	m = answer(t, m, runPlan)
	if m.todoRunner.state.Stage != run.StageImplement || m.policy.mode != agent.ModeAuto || !m.working() || m.state == statePlanApprove {
		t.Fatalf("implement should follow research in auto mode, no plan card: stage=%s mode=%s state=%d", m.todoRunner.state.Stage, m.policy.mode, m.state)
	}
	if m.todoRunner.state.Grade != "S" {
		t.Fatalf("size should be re-graded from research, got %s", m.todoRunner.state.Grade)
	}

	m = answer(t, m, "Changed a.go.")
	if m.todoRunner.state.Stage != run.StageVerify || m.working() {
		t.Fatalf("verify should follow implement: %s", m.todoRunner.state.Stage)
	}
	updated, _ = m.Update(todoVerifyMsg{slug: "do-it", ok: true, output: "$ true → exit 0"})
	m = updated.(Model)
	if m.todoRunner.state.Stage != run.StageReview || !m.working() {
		t.Fatalf("review should follow a passing verify: %s", m.todoRunner.state.Stage)
	}
	m = answer(t, m, "verdict: clean")
	if m.todoRunner.state.Stage != run.StageCommit || !m.working() {
		t.Fatalf("commit turn should follow a clean review: %s", m.todoRunner.state.Stage)
	}
	m = answer(t, m, "COMMIT:\nDo it\n\nBody.\nREPORT:\n## Report\nSummary: did it")
	if m.todoRunner.state.Message != "Do it\n\nBody." || m.working() {
		t.Fatalf("the commit turn should be read and the commit started: %+v", m.todoRunner.state)
	}
	updated, _ = m.Update(todoCommitMsg{slug: "do-it", files: []string{"a.go"}})
	m = updated.(Model)
	if m.todoRunner.state != nil || m.policy.mode != agent.ModeManual {
		t.Fatalf("done should end the run and restore the mode: run=%v mode=%s", m.todoRunner.state, m.policy.mode)
	}
	s := todo.Load(todo.BuiltinCode(), root)
	done, ok := s.Find("do-it")
	if !ok || !done.Archived || !strings.Contains(done.Body, "Summary: did it") || !strings.Contains(done.Body, "Committed: a.go") {
		t.Fatalf("the item should be archived with its report: %+v", done)
	}
	if _, err := run.Load(root, "do-it"); err == nil {
		t.Fatal("the checkpoint should be retired")
	}
	if !strings.Contains(m.transcript[len(m.transcript)-1].text, "✓ todo run do-it done") {
		t.Fatal("the close should be announced")
	}
}

func TestTodoRun_BlocksWithEvidenceAndKeepsTheTree(t *testing.T) {
	m, root := runModel(t)
	m.input.SetValue("/todo run")
	updated, _ := m.submitInput()
	m = updated.(Model)
	m = answer(t, m, "## Plan: x\n\n1. a\n\nsize: S\nquestions:\n- keep the flag?\n")
	if m.todoRunner.state != nil || m.policy.mode != agent.ModeManual {
		t.Fatalf("an open question should block and restore the mode: %v %s", m.todoRunner.state, m.policy.mode)
	}
	it, _ := todo.Load(todo.BuiltinCode(), root).Find("do-it")
	if it.Status != todo.StatusBlocked || !strings.Contains(it.Body, "## Blocked\nopen questions after research:\n- keep the flag?") {
		t.Fatalf("evidence not written: %+v", it)
	}
	if note := m.transcript[len(m.transcript)-1].text; !strings.Contains(note, "✗ todo run do-it blocked") || !strings.Contains(note, "/todo open do-it") {
		t.Fatalf("note = %q", note)
	}
	m.input.SetValue("/todo run do-it")
	updated, _ = m.submitInput()
	if note := updated.(Model).transcript[len(updated.(Model).transcript)-1].text; !strings.Contains(note, "is blocked") {
		t.Fatalf("a blocked item must not run: %q", note)
	}
}

func TestTodoRun_StopReopensTheItem(t *testing.T) {
	m, root := runModel(t)
	m.input.SetValue("/todo run do-it")
	updated, _ := m.submitInput()
	m = updated.(Model)
	m = answer(t, m, runPlan)
	m = answer(t, m, "done")
	m.input.SetValue("/todo stop")
	updated, _ = m.submitInput()
	m = updated.(Model)

	if m.todoRunner.state != nil || m.policy.mode != agent.ModeManual {
		t.Fatal("run should end and mode restore")
	}
	if it, _ := todo.Load(todo.BuiltinCode(), root).Find("do-it"); it.Status != todo.StatusOpen {
		t.Fatalf("item should be open again, is %s", it.Status)
	}
	if _, err := run.Load(root, "do-it"); err == nil {
		t.Fatal("stopping abandons the run, so nothing is left to continue")
	}
}

// The session boundary is the other half of that: a run in progress keeps
// its checkpoint, and the row the new session opens on says how to pick it
// up. The stages already done are in the tree, so putting the item back to
// open would throw that work away.
func TestTodoRun_ANewSessionKeepsTheRunsCheckpoint(t *testing.T) {
	m, root := runModel(t)
	m.input.SetValue("/todo run do-it")
	updated, _ := m.submitInput()
	m = updated.(Model)
	m = answer(t, m, runPlan)
	m = answer(t, m, "done")

	note, _ := m.startNewSession()

	if m.todoRunner.state != nil || m.policy.mode != agent.ModeManual {
		t.Fatal("the run should be let go of and the mode restored")
	}
	if it, _ := todo.Load(todo.BuiltinCode(), root).Find("do-it"); it.Status != todo.StatusInProgress {
		t.Fatalf("the item stays in progress with its checkpoint, is %s", it.Status)
	}
	st, err := run.Load(root, "do-it")
	if err != nil {
		t.Fatalf("the checkpoint should survive the boundary: %v", err)
	}
	if st.Over() {
		t.Fatalf("the checkpoint should be continuable, stage %s", st.Stage)
	}
	if !strings.Contains(note, "/todo run do-it") {
		t.Fatalf("the new session's first row should offer to continue it, got %q", note)
	}
}

func TestTodoRun_Guards(t *testing.T) {
	m, _ := runModel(t)
	m.input.SetValue("/todo run nope")
	updated, _ := m.submitInput()
	if note := updated.(Model).transcript[len(updated.(Model).transcript)-1].text; !strings.Contains(note, `No active backlog item "nope"`) {
		t.Fatalf("unknown slug: %q", note)
	}
	m.input.SetValue("/todo status")
	updated, _ = m.submitInput()
	if note := updated.(Model).transcript[len(updated.(Model).transcript)-1].text; !strings.HasPrefix(note, "No run is going") {
		t.Fatalf("status idle: %q", note)
	}
	m.input.SetValue("/todo run do-it")
	updated, _ = m.submitInput()
	m = updated.(Model)
	m.input.SetValue("/todo run do-it")
	updated, _ = m.submitInput()
	if note := updated.(Model).transcript[len(updated.(Model).transcript)-1].text; !strings.Contains(note, "Not while the turn is running") {
		t.Fatalf("second run mid-turn: %q", note)
	}
	// Mid-run, status is the run's row opened rather than a sentence about
	// it; the row's own test covers what it says.
	m.input.SetValue("/todo status")
	updated, _ = m.submitInput()
	if next := updated.(Model); next.state != stateFocus || lastTodoRunRow(next.transcript) < 0 {
		t.Fatalf("status mid-run should open the row: state=%d", next.state)
	}
	block := m.inspectorTodo()
	if block == nil || block.Rows[0].Note != "research" {
		t.Fatalf("the rail should name the stage: %+v", block)
	}
	m.changes = nil
	m.todoRunner.state = nil
	m.input.SetValue("/todo run do-it")
	m.state = stateInput
	updated, _ = m.submitInput()
	if note := updated.(Model).transcript[len(updated.(Model).transcript)-1].text; !strings.Contains(note, "does not track changes") {
		t.Fatalf("no changeset: %q", note)
	}
}

// A profile may state no run at all — a checklist is a list of things to do
// rather than a thing to work — and asking for one on such a backlog says so
// and offers the verb that files the item, rather than sending it into the
// run some other profile has.
func TestTodoRun_AProfileWithNoRunSaysSoAndOffersDone(t *testing.T) {
	m, _ := runModel(t)
	m.todos.Profile.Name = "checklist"
	m.todos.Pipeline = run.Pipeline{Name: "checklist"}
	m.input.SetValue("/todo run do-it")
	updated, _ := m.submitInput()
	next := updated.(Model)
	note := next.transcript[len(next.transcript)-1].text
	if !strings.Contains(note, "checklist profile has no run") || !strings.Contains(note, "/todo done do-it") {
		t.Fatalf("the refusal does not say what to do instead: %q", note)
	}
	if next.todoRunner.state != nil {
		t.Fatal("a run started under a profile that states none")
	}
	// And the same for a whole set: there is no item to name, so the
	// sentence is about the backlog rather than about one row.
	m.input.SetValue("/todo run --all")
	updated, _ = m.submitInput()
	next = updated.(Model)
	if note := next.transcript[len(next.transcript)-1].text; !strings.Contains(note, "no run, so there is no set to work") {
		t.Fatalf("a sprint under a profile with no run: %q", note)
	}
}

func TestTodoVerifyCmd_RunsSnapshotAndReportsFailure(t *testing.T) {
	m, root := runModel(t)
	m.todoRunner.state = &run.State{Slug: "do-it", Tests: []string{"true", "exit 3"}}
	m.todoRunner.item = todo.Item{Slug: "do-it", Body: "## Tests\n- echo MODEL-WROTE-THIS\n"}
	msg := m.todoVerifyCmd("")().(todoVerifyMsg)
	if msg.ok || !strings.Contains(msg.output, "$ exit 3 → exit 3") || strings.Contains(msg.output, "MODEL-WROTE-THIS") {
		t.Fatalf("verify = %+v", msg)
	}
	m.todoRunner.state = &run.State{Slug: "do-it"}
	msg = m.todoVerifyCmd("")().(todoVerifyMsg)
	if !msg.ok || !strings.Contains(msg.output, "nothing to verify") {
		t.Fatalf("empty verify = %+v", msg)
	}
	_ = root
}

func TestTodoRun_TextIsRefusedAndStaleResultsIgnored(t *testing.T) {
	m, _ := runModel(t)
	m.input.SetValue("/todo run do-it")
	updated, _ := m.submitInput()
	m = updated.(Model)
	m.input.SetValue("also check the tests")
	updated, _ = m.submitInput()
	m = updated.(Model)
	if len(m.steering) != 0 || !strings.Contains(m.transcript[len(m.transcript)-1].text, "Not sent: a backlog run is going") {
		t.Fatal("text mid-run should be refused, not queued as steering")
	}
	m = answer(t, m, runPlan)
	m = answer(t, m, "done")
	if m.todoRunner.state.Stage != run.StageVerify {
		t.Fatal("should be verifying")
	}
	m.input.SetValue("quick question")
	updated, _ = m.submitInput()
	m = updated.(Model)
	if m.working() {
		t.Fatal("text between stages should not start a turn")
	}
	updated, _ = m.Update(todoVerifyMsg{slug: "other", ok: true})
	m = updated.(Model)
	updated, _ = m.Update(todoCommitMsg{slug: "do-it", files: []string{"x"}})
	m = updated.(Model)
	if m.todoRunner.state.Stage != run.StageVerify {
		t.Fatalf("a stale verify or a commit in the wrong stage must be ignored, stage=%s", m.todoRunner.state.Stage)
	}
}

func TestTodoRun_CancelStopsTheRun(t *testing.T) {
	m, root := runModel(t)
	m.input.SetValue("/todo run do-it")
	updated, _ := m.submitInput()
	m = updated.(Model)
	m.streaming = "## Plan: half"
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	m = updated.(Model)
	if m.todoRunner.state != nil {
		t.Fatal("a cancelled stage turn should stop the run, not be graded")
	}
	if it, _ := todo.Load(todo.BuiltinCode(), root).Find("do-it"); it.Status != todo.StatusOpen {
		t.Fatalf("item should be open, is %s", it.Status)
	}
}

func TestTodoRun_ArchiveFailureAfterCommitReopensWithReport(t *testing.T) {
	m, root := runModel(t)
	must(t, os.MkdirAll(filepath.Join(todo.Dir(root), todo.DoneSubdir), 0o755))
	must(t, os.WriteFile(filepath.Join(todo.Dir(root), todo.DoneSubdir, "do-it.md"), []byte("---\ntitle: old\nstatus: done\n---\n"), 0o644))
	m.input.SetValue("/todo run do-it")
	updated, _ := m.submitInput()
	m = updated.(Model)
	m = answer(t, m, runPlan)
	m = answer(t, m, "done")
	updated, _ = m.Update(todoVerifyMsg{slug: "do-it", ok: true})
	m = answer(t, updated.(Model), "verdict: clean")
	m = answer(t, m, "COMMIT:\nDo it\nREPORT:\n## Report\nSummary: did it")
	updated, _ = m.Update(todoCommitMsg{slug: "do-it", files: []string{"a.go"}})
	m = updated.(Model)
	it, _ := todo.Load(todo.BuiltinCode(), root).Find("do-it")
	if it.Archived || it.Status != todo.StatusOpen || !strings.Contains(it.Body, "Summary: did it") {
		t.Fatalf("item after failed archive = %+v", it)
	}
	if !strings.Contains(m.transcript[len(m.transcript)-1].text, "could not be archived") {
		t.Fatal("the note should say the archive failed")
	}
}

func TestTodoRunPaths_OnlyThisRunUnderRootNeverBacklog(t *testing.T) {
	m, root := runModel(t)
	m.todoRunner.state = &run.State{Slug: "do-it", Turn: 3}
	add := func(turn int64, path string) {
		m.changes.Add(turn, changeset.Record{Path: path, Before: "a", After: "b", BeforeExists: true, AfterExists: true})
	}
	add(2, filepath.Join(root, "old.go"))
	add(3, filepath.Join(root, "a.go"))
	add(4, "b/c.go")
	add(4, filepath.Join(root, ".shhh", "todo", "do-it.md"))
	add(4, filepath.Join(filepath.Dir(root), "outside.go"))
	m.changes.Add(4, changeset.Record{Path: filepath.Join(root, "same.go"), Before: "x", After: "x", BeforeExists: true, AfterExists: true})
	if got := strings.Join(m.todoRunPaths(), "|"); got != "a.go|b/c.go" {
		t.Errorf("paths = %q", got)
	}
}

func TestTodoCommitCmd_StagesByNameAndRefusesForeignIndex(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	gitc := func(args ...string) string {
		out, code := git(root, args...)
		if code != 0 {
			t.Fatalf("git %v: %s", args, out)
		}
		return out
	}
	gitc("init", "-q")
	gitc("config", "user.email", "t@example.com")
	gitc("config", "user.name", "t")
	must(t, os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(root, "stray.go"), []byte("package a\n"), 0o644))
	gitc("add", "a.go")
	gitc("commit", "-q", "-m", "seed")
	must(t, os.WriteFile(filepath.Join(root, "a.go"), []byte("package a // changed\n"), 0o644))

	m := frameModel(t, 130, 40)
	m.changes = changeset.New(1 << 20)
	m = m.WithTodos(Todos{Profile: todo.BuiltinCode(), Root: root, Manage: func([]string) string { return "" }, Detail: func(*todo.Store, todo.Item) string { return "" }})
	m.todoRunner.state = &run.State{Slug: "x", Turn: 1, Message: "Change a\n\nBecause."}
	m.changes.Add(1, changeset.Record{Path: filepath.Join(root, "a.go"), Before: "package a\n", After: "package a // changed\n", BeforeExists: true, AfterExists: true})

	gitc("add", "stray.go")
	msg := m.todoCommitCmd()().(todoCommitMsg)
	if msg.err == nil || !strings.Contains(msg.err.Error(), "already holds staged changes") {
		t.Fatalf("a foreign index should refuse the commit: %v", msg.err)
	}
	gitc("reset", "-q", "stray.go")

	msg = m.todoCommitCmd()().(todoCommitMsg)
	if msg.err != nil || strings.Join(msg.files, ",") != "a.go" {
		t.Fatalf("commit = %+v", msg)
	}
	if subject := gitc("log", "-1", "--format=%s"); subject != "Change a" {
		t.Fatalf("subject = %q", subject)
	}
	if status := gitc("status", "--porcelain"); status != "?? stray.go" {
		t.Fatalf("the stray file must be left alone, status = %q", status)
	}
}

const largePlan = "## Plan: big\n\n1. a\n   files: a.go\n\nsize: L\nquestions:\n- keep the flag?\n"

func TestTodoRun_PauseCardGoAheadReplanStop(t *testing.T) {
	m, root := runModel(t)
	m.input.SetValue("/todo run do-it")
	updated, _ := m.submitInput()
	m = answer(t, updated.(Model), largePlan)
	if m.state != stateTodoPause || m.todoRunner.pause == nil || m.todoRunner.state.Paused == "" {
		t.Fatalf("an L plan should pause: state=%d", m.state)
	}
	lines := stripANSI(strings.Join(m.todoPauseLines(), "\n"))
	// The size against the item's, the plan as a checklist and the question
	// research could not settle, all above the answers: the choice is made
	// with the facts in view rather than with a scroll back.
	for _, want := range []string{"size L (was M)", "1 step", "○ 1. a", "? keep the flag?", "Go ahead"} {
		if !strings.Contains(lines, want) {
			t.Fatalf("the pause card does not carry %q:\n%s", want, lines)
		}
	}
	if m.inspectorHidden() != true {
		t.Fatal("the pause takes the panel like the other cards")
	}

	// Re-plan with a note: research runs again with the answer in front.
	m = press(t, m, "down")
	m.todoRunner.pause.Note.SetValue("keep it")
	m = press(t, m, "enter")
	if m.state == stateTodoPause || m.todoRunner.state.Stage != run.StageResearch || !m.working() || m.policy.mode != agent.ModeManual && m.policy.mode != agent.ModePlan {
		t.Fatalf("replan should send research again: stage=%s state=%d", m.todoRunner.state.Stage, m.state)
	}
	if it, _ := todo.Load(todo.BuiltinCode(), root).Find("do-it"); !strings.Contains(it.Body, "## Answers\nkeep it") {
		t.Fatalf("the answer should be on the item: %q", it.Body)
	}
	if last := m.transcript[len(m.transcript)-1]; !strings.Contains(last.text, "research again") {
		t.Fatalf("shown = %q", last.text)
	}
	m = answer(t, m, largePlan)
	if m.state != stateTodoPause {
		t.Fatal("L pauses again after re-plan")
	}
	m = press(t, m, "enter")
	// A large item is divided before it is built; the split reads only.
	if m.todoRunner.state.Stage != run.StageSplit || !m.working() || m.policy.mode != agent.ModePlan {
		t.Fatalf("go ahead on L should split in plan mode: stage=%s mode=%s", m.todoRunner.state.Stage, m.policy.mode)
	}

	// Stop from the card.
	m2, root2 := runModel(t)
	m2.input.SetValue("/todo run do-it")
	updated, _ = m2.submitInput()
	m2 = answer(t, updated.(Model), largePlan)
	m2 = press(t, m2, "esc")
	if m2.todoRunner.state != nil || m2.state != stateInput {
		t.Fatal("esc on the pause should stop the run")
	}
	if it, _ := todo.Load(todo.BuiltinCode(), root2).Find("do-it"); it.Status != todo.StatusOpen {
		t.Fatal("stopped item should be open")
	}
}

func TestTodoRun_ReviewFallsBackWithoutASupervisor(t *testing.T) {
	m, _ := runModel(t)
	m.input.SetValue("/todo run do-it")
	updated, _ := m.submitInput()
	m = answer(t, updated.(Model), strings.Replace(runPlan, "size: S", "size: M", 1))
	m.changes.Add(m.turnCount, changeset.Record{Path: filepath.Join(m.todos.Root, "a.go"), Before: "a", After: "b", BeforeExists: true, AfterExists: true})
	m = answer(t, m, "done")
	updated, _ = m.Update(todoVerifyMsg{slug: "do-it", ok: true})
	m = updated.(Model)
	if m.todoRunner.state == nil || m.todoRunner.state.Stage != run.StageReview || !m.working() || m.todoRunner.state.Reviewer != "" {
		t.Fatalf("no supervisor: the session should review itself: %+v", m.todoRunner.state)
	}
	if !strings.Contains(m.transcript[len(m.transcript)-1].text, "no reviewer agent") {
		t.Fatal("the fallback should say so")
	}
}

func TestTodoRun_ReviewerChildAnswersTheStage(t *testing.T) {
	m, _ := runModel(t)
	sup := subagent.New(context.Background(), subagent.Options{Root: m.todos.Root, NewEnv: reportingEnv("Looked.\nverdict: clean")})
	t.Cleanup(sup.Close)
	m = m.WithSubagents(sup)
	m.input.SetValue("/todo run do-it")
	updated, _ := m.submitInput()
	m = answer(t, updated.(Model), strings.Replace(runPlan, "size: S", "size: M", 1))
	m.changes.Add(m.turnCount, changeset.Record{Path: filepath.Join(m.todos.Root, "a.go"), Before: "a", After: "b", BeforeExists: true, AfterExists: true})
	m = answer(t, m, "done")
	updated, _ = m.Update(todoVerifyMsg{slug: "do-it", ok: true})
	m = updated.(Model)
	if m.todoRunner.state == nil || m.todoRunner.state.Reviewer != "todo-review-do-it-1" || m.working() {
		t.Fatalf("a reviewer child should be spawned: %+v", m.todoRunner.state)
	}
	if st, ok := sup.Get("todo-review-do-it-1"); !ok || st.Role != subagent.RoleReviewer {
		t.Fatalf("child = %+v %v", st, ok)
	}
	var ev subagent.Event
	deadline := time.After(5 * time.Second)
	for ev.Kind != subagent.EventDone {
		select {
		case ev = <-sup.Events():
		case <-deadline:
			t.Fatal("the child never finished")
		}
	}
	updated, _ = m.handleSubagentEvent(ev)
	m = updated.(Model)
	if m.todoRunner.state.Stage != run.StageCommit || !m.working() || m.todoRunner.state.Reviewer != "" {
		t.Fatalf("a clean verdict from the child should go to the commit turn: %+v", m.todoRunner.state)
	}
}

func TestTodoRun_BlockOffersAFollowUp(t *testing.T) {
	m, root := runModel(t)
	m.input.SetValue("/todo run do-it")
	updated, _ := m.submitInput()
	m = answer(t, updated.(Model), "## Plan: x\n\n1. a\n\nsize: S\nquestions:\n- which?\n")
	if m.state != stateTodoPropose || len(m.todoProposals) != 1 {
		t.Fatalf("a block should offer a follow-up: state=%d", m.state)
	}
	p := m.todoProposals[0]
	if !strings.HasPrefix(p.Title, "Follow up do-it") || p.DependsOn[0] != "do-it" || !strings.Contains(p.Notes[0], "which?") {
		t.Fatalf("follow-up = %+v", p)
	}
	m = press(t, m, "enter")
	s := todo.Load(todo.BuiltinCode(), root)
	if s.Len() != 2 {
		t.Fatalf("the follow-up should be written: %d items", s.Len())
	}
	if len(s.Ready()) != 0 {
		t.Fatal("the follow-up waits on the blocked item")
	}
}

// reportingEnv is a child that answers every request with text.
func reportingEnv(text string) subagent.EnvFactory {
	return func(ctx context.Context, spec subagent.Spec) (subagent.Env, error) {
		stream := func(msgs []provider.Message, _ string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
			ch := make(chan provider.StreamEvent, 2)
			ch <- provider.StreamEvent{Token: text}
			ch <- provider.StreamEvent{Done: true}
			close(ch)
			return ch, func() {}, nil
		}
		return subagent.Env{SystemPrompt: "sys", Stream: stream,
			Executor: func(string, json.RawMessage) (string, error) { return "", errors.New("unused") }}, nil
	}
}

func reviewReadyModel(t *testing.T, env subagent.EnvFactory) (Model, *subagent.Supervisor) {
	t.Helper()
	m, root := runModel(t)
	sup := subagent.New(context.Background(), subagent.Options{Root: root, NewEnv: env})
	t.Cleanup(sup.Close)
	m = m.WithSubagents(sup)
	m.input.SetValue("/todo run do-it")
	updated, _ := m.submitInput()
	m = answer(t, updated.(Model), strings.Replace(runPlan, "size: S", "size: M", 1))
	// The implement turn "created" a file: the changeset records it.
	m.changes.Add(m.turnCount, changeset.Record{Path: filepath.Join(root, "new.go"), After: "package new\n", AfterExists: true})
	m = answer(t, m, "done")
	updated, _ = m.Update(todoVerifyMsg{slug: "do-it", ok: true})
	return updated.(Model), sup
}

func waitDone(t *testing.T, sup *subagent.Supervisor) subagent.Event {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-sup.Events():
			if ev.Kind == subagent.EventDone {
				return ev
			}
		case <-deadline:
			t.Fatal("the child never finished")
		}
	}
}

func TestTodoRun_ReviewerTaskCarriesCreatedFiles(t *testing.T) {
	m, sup := reviewReadyModel(t, reportingEnv("verdict: clean"))
	st, ok := sup.Get("todo-review-do-it-1")
	if !ok || !strings.Contains(st.Task, "+++ b/new.go") || !strings.Contains(st.Task, "+package new") {
		t.Fatalf("the task should carry the created file's content: %q", st.Task)
	}
	_ = m
}

func TestTodoRun_FailedReviewerBlocksInsteadOfGrading(t *testing.T) {
	m, sup := reviewReadyModel(t, blockingEnv())
	if err := sup.Kill("todo-review-do-it-1"); err != nil {
		t.Fatal(err)
	}
	ev := waitDone(t, sup)
	updated, _ := m.handleSubagentEvent(ev)
	m = updated.(Model)
	if m.todoRunner.state != nil {
		t.Fatalf("a killed reviewer should block the run, got stage %s", m.todoRunner.state.Stage)
	}
	found := false
	for _, e := range m.transcript {
		if strings.Contains(e.text, "did not finish") {
			found = true
		}
	}
	if !found {
		t.Fatal("the evidence should say the reviewer did not finish")
	}
}

func TestTodoRun_StopKillsTheReviewer(t *testing.T) {
	m, sup := reviewReadyModel(t, blockingEnv())
	m.input.SetValue("/todo stop")
	updated, _ := m.submitInput()
	m = updated.(Model)
	ev := waitDone(t, sup)
	if ev.Status.Name != "todo-review-do-it-1" || ev.Status.State == subagent.StateDone {
		t.Fatalf("stop should kill the reviewer: %+v", ev.Status)
	}
	if m.todoRunner.state != nil {
		t.Fatal("run should be over")
	}
}

func TestTodoRun_ReviewWithNoChangesBlocks(t *testing.T) {
	m, _ := runModel(t)
	m.input.SetValue("/todo run do-it")
	updated, _ := m.submitInput()
	m = answer(t, updated.(Model), strings.Replace(runPlan, "size: S", "size: M", 1))
	m = answer(t, m, "done")
	updated, _ = m.Update(todoVerifyMsg{slug: "do-it", ok: true})
	m = updated.(Model)
	if m.todoRunner.state != nil {
		t.Fatal("nothing changed: the run should block rather than review the whole tree")
	}
}

func TestTodoRun_GoAheadNoteReachesImplement(t *testing.T) {
	m, _ := runModel(t)
	m.input.SetValue("/todo run do-it")
	updated, _ := m.submitInput()
	m = answer(t, updated.(Model), largePlan)
	m.todoRunner.pause.Note.SetValue("use the old flag")
	m = press(t, m, "enter")
	if !m.working() || m.todoRunner.state.Stage != run.StageSplit {
		t.Fatal("go ahead on L should split")
	}
	if msgs := m.agent.Messages(); !strings.Contains(msgs[len(msgs)-1].Content, "use the old flag") {
		t.Fatal("the note should be in front of the split stage")
	}
	// And in front of the lanes and the integration after them.
	m = answer(t, m, "lanes: none")
	if !m.working() || m.todoRunner.state.Stage != run.StageImplement {
		t.Fatal("no lanes should implement whole")
	}
	if msgs := m.agent.Messages(); !strings.Contains(msgs[len(msgs)-1].Content, "use the old flag") {
		t.Fatal("the note should be in front of the implement stage")
	}
}

func TestTodoFollowUp_CriteriaOnly(t *testing.T) {
	it := todo.Item{Slug: "x", Title: "X", Body: "## Acceptance criteria\n- [x] done\n- [ ] left\n\n## Tasks\n- [ ] a task\n"}
	p := todoFollowUp(it, &run.State{Stage: run.StageVerify, Blocked: "why"})
	if strings.Join(p.Criteria, "|") != "left" {
		t.Errorf("criteria = %v", p.Criteria)
	}
}

func TestTodoRun_ContinuesFromCheckpointAndDisplacementPauses(t *testing.T) {
	m, root := runModel(t)
	m.input.SetValue("/todo run do-it")
	updated, _ := m.submitInput()
	m = answer(t, updated.(Model), runPlan)
	if m.todoRunner.state.Stage != run.StageImplement {
		t.Fatal("should be implementing")
	}
	// Another turn gets in ahead of the stage: a compaction-like user turn.
	updated, _ = m.sendUserMessage("summarise")
	m = answer(t, updated.(Model), "summary")
	if m.todoRunner.state != nil {
		t.Fatal("a displaced stage should pause the run")
	}
	it, _ := todo.Load(todo.BuiltinCode(), root).Find("do-it")
	if it.Status != todo.StatusInProgress {
		t.Fatalf("item should stay in progress, is %s", it.Status)
	}
	if st, err := run.Load(root, "do-it"); err != nil || st.Stage != run.StageImplement {
		t.Fatalf("checkpoint should be kept at implement: %+v %v", st, err)
	}
	if !strings.Contains(m.transcript[len(m.transcript)-1].text, "/todo run do-it continues it") {
		t.Fatal("the note should say how to continue")
	}

	// A fresh session continues from the checkpoint.
	m2 := frameModel(t, 130, 40)
	m2.changes = changeset.New(1 << 20)
	m2.policy.mode = agent.ModeManual
	m2 = m2.WithTodos(Todos{Profile: todo.BuiltinCode(), Root: root, Manage: func([]string) string { return "" }, Detail: func(*todo.Store, todo.Item) string { return "" }})
	m2.input.SetValue("/todo run do-it")
	updated, _ = m2.submitInput()
	m2 = updated.(Model)
	if m2.todoRunner.state == nil || m2.todoRunner.state.Stage != run.StageImplement || !m2.working() || m2.policy.mode != agent.ModeAuto {
		t.Fatalf("should continue at implement in auto: %+v", m2.todoRunner.state)
	}
	if len(m2.todoRunner.state.Steps) != 1 {
		t.Fatal("the plan should come back with the checkpoint")
	}
	m2 = answer(t, m2, "done")
	if m2.todoRunner.state.Stage != run.StageVerify {
		t.Fatal("the continued run should carry on")
	}

	// In progress with no checkpoint: told how to start over.
	run.Discard(root, "do-it")
	m3 := frameModel(t, 130, 40)
	m3.changes = changeset.New(1 << 20)
	m3 = m3.WithTodos(Todos{Profile: todo.BuiltinCode(), Root: root, Manage: func([]string) string { return "" }, Detail: func(*todo.Store, todo.Item) string { return "" }})
	m3.input.SetValue("/todo run do-it")
	updated, _ = m3.submitInput()
	if note := updated.(Model).transcript[len(updated.(Model).transcript)-1].text; !strings.Contains(note, "no checkpoint") {
		t.Fatalf("note = %q", note)
	}
}

func TestTodoRun_ContinuedRunKeepsEarlierPaths(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	m, root := runModel(t)
	if out, code := git(root, "init", "-q"); code != 0 {
		t.Fatal(out)
	}
	m.input.SetValue("/todo run do-it")
	updated, _ := m.submitInput()
	m = answer(t, updated.(Model), runPlan)
	m.changes.Add(m.turnCount, changeset.Record{Path: filepath.Join(root, "a.go"), Before: "a", After: "b", BeforeExists: true, AfterExists: true})
	// Displace the stage so the checkpoint is kept.
	updated, _ = m.sendUserMessage("x")
	m = answer(t, updated.(Model), "y")
	st, err := run.Load(root, "do-it")
	if err != nil || strings.Join(st.Paths, ",") != "a.go" {
		t.Fatalf("checkpoint paths = %v %v", st, err)
	}
	m2 := frameModel(t, 130, 40)
	m2.changes = changeset.New(1 << 20)
	m2 = m2.WithTodos(Todos{Profile: todo.BuiltinCode(), Root: root, Manage: func([]string) string { return "" }, Detail: func(*todo.Store, todo.Item) string { return "" }})
	m2.input.SetValue("/todo run do-it")
	updated, _ = m2.submitInput()
	m2 = updated.(Model)
	m2.changes.Add(m2.turnCount, changeset.Record{Path: filepath.Join(root, "b.go"), After: "n", AfterExists: true})
	if got := strings.Join(m2.todoRunPaths(), ","); got != "a.go,b.go" {
		t.Fatalf("continued paths = %q", got)
	}
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("whole file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if d := m2.todoRunDiff(); !strings.Contains(d, "+whole file") || !strings.Contains(d, "+++ b/b.go") {
		t.Fatalf("diff should cover both sessions' paths: %q", d)
	}
}

func TestTodoRun_SameSessionContinueRespawnsAReviewer(t *testing.T) {
	m, sup := reviewReadyModel(t, blockingEnv())
	if m.todoRunner.state.Reviewer != "todo-review-do-it-1" {
		t.Fatalf("reviewer = %q", m.todoRunner.state.Reviewer)
	}
	// Displace: a user turn while the reviewer reads pauses the run.
	updated, _ := m.sendUserMessage("x")
	m = answer(t, updated.(Model), "y")
	if m.todoRunner.state != nil {
		t.Fatal("should have paused")
	}
	waitDone(t, sup)
	m.input.SetValue("/todo run do-it")
	updated, _ = m.submitInput()
	m = updated.(Model)
	if m.todoRunner.state == nil || m.todoRunner.state.Reviewer != "todo-review-do-it-2" {
		t.Fatalf("a continued review should spawn a fresh child: %+v", m.todoRunner.state)
	}
	if _, ok := sup.Get("todo-review-do-it-2"); !ok {
		t.Fatal("the second child should exist")
	}
}

// largeRunModel is a run on a large item at its fan-out: research answered
// L, the pause was taken, and the split named two lanes. The root is a
// git repository with a commit, because writers work in worktrees.
func largeRunModel(t *testing.T, env subagent.EnvFactory) (Model, *subagent.Supervisor) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	// The session reaches its checkout through a symlink, which is not an
	// exotic setup to arrange for: it is what every macOS session under a
	// TMPDIR already does, /var being a link to /private/var. It belongs to
	// the fan-out in particular because git answers `rev-parse
	// --show-toplevel` with the link followed, and a lane's patch recorded
	// against the unresolved root lands on disk and then belongs to no path
	// the run can name (internal/subagent/rooted.go).
	link := filepath.Join(t.TempDir(), "workspace")
	if err := os.Symlink(t.TempDir(), link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	m, root := runModelAt(t, link)
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "a.go")
	git("commit", "-q", "-m", "seed")
	sup := subagent.New(context.Background(), subagent.Options{Root: root, NewEnv: env})
	t.Cleanup(sup.Close)
	m = m.WithSubagents(sup)
	m.input.SetValue("/todo run do-it")
	updated, _ := m.submitInput()
	m = answer(t, updated.(Model), strings.Replace(runPlan, "size: S", "size: L", 1))
	if m.todoRunner.pause == nil {
		t.Fatal("L should pause")
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if m.todoRunner.state == nil || m.todoRunner.state.Stage != run.StageSplit || !m.working() {
		t.Fatalf("go ahead should start the split turn: %+v", m.todoRunner.state)
	}
	m = answer(t, m, "LANE: alpha\npaths: a.go\ntask: change a\n\nLANE: beta\npaths: b.go\ntask: create b\n")
	if m.todoRunner.state.Stage != run.StageFanOut || m.working() {
		t.Fatalf("the split should fan out: %+v", m.todoRunner.state)
	}
	return m, sup
}

// writingEnv builds writers that write one file into their own copy of the
// tree, named after their lane's first path, then report. The write is
// gated, as it is in production, so the child's mode is what decides
// whether it happens: a writer spawned read-only would write nothing.
func writingEnv(content string) subagent.EnvFactory {
	return func(ctx context.Context, spec subagent.Spec) (subagent.Env, error) {
		calls := 0
		stream := func(msgs []provider.Message, _ string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
			ch := make(chan provider.StreamEvent, 2)
			calls++
			if calls == 1 {
				ch <- provider.StreamEvent{ToolCalls: []provider.ToolCall{{ID: "1", Name: "write_file", Arguments: `{"path":"` + spec.Paths[0] + `"}`}}}
			} else {
				ch <- provider.StreamEvent{Token: "Wrote " + spec.Paths[0] + ". Wire it up."}
				ch <- provider.StreamEvent{Done: true}
			}
			close(ch)
			return ch, func() {}, nil
		}
		write := func(name string, args json.RawMessage) (string, error) {
			var a struct{ Path string }
			_ = json.Unmarshal(args, &a)
			return "ok", os.WriteFile(filepath.Join(spec.Root, filepath.Base(a.Path)), []byte(content), 0o644)
		}
		return subagent.Env{SystemPrompt: "sys", Stream: stream, Executor: write, ExecuteGated: write,
			Gated: map[string]bool{"write_file": true}}, nil
	}
}

// pumpSubagents feeds supervisor events to the model until every lane has
// reported or the deadline passes.
func pumpSubagents(t *testing.T, m Model, sup *subagent.Supervisor, until func(Model) bool) Model {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for !until(m) {
		select {
		case ev := <-sup.Events():
			updated, _ := m.handleSubagentEvent(ev)
			m = updated.(Model)
		case <-deadline:
			t.Fatalf("timed out; run = %+v", m.todoRunner.state)
		}
	}
	return m
}

func TestTodoRun_LargeItemLanesLandAndIntegrate(t *testing.T) {
	m, sup := largeRunModel(t, writingEnv("package a\n\nvar changed = true\n"))
	if st, ok := sup.Get("tw1-alpha"); !ok || st.Role != subagent.RoleWriter || st.Paths[0] != "a.go" || st.Batch == 0 {
		t.Fatalf("alpha = %+v %v", st, ok)
	}
	if mode, _ := sup.AgentMode("tw1-alpha"); mode != agent.ModeAuto {
		t.Fatalf("a writer must be spawned in the working mode, got %v", mode)
	}
	if _, ok := sup.Get("tw1-beta"); !ok {
		t.Fatal("beta should be spawned")
	}
	m = pumpSubagents(t, m, sup, func(m Model) bool { return m.todoRunner.state == nil || m.todoRunner.state.Stage != run.StageFanOut })
	if m.todoRunner.state == nil || m.todoRunner.state.Stage != run.StageImplement || !m.working() || !m.todoRunner.state.AllLanesDone() {
		t.Fatalf("both lanes landing should start the integration turn: %+v", m.todoRunner.state)
	}
	if len(m.childAsks) != 0 {
		t.Fatal("a lane's patch is the run's to take, never a card")
	}
	// The patches are on the real tree, recorded in the changeset, and so
	// in what the run may stage.
	if data, _ := os.ReadFile(filepath.Join(m.todos.Root, "b.go")); !strings.Contains(string(data), "changed") {
		t.Fatalf("beta's patch should have landed: %q", data)
	}
	paths := m.todoRunPaths()
	if len(paths) != 2 {
		t.Fatalf("paths = %v", paths)
	}
	if m.policy.mode != agent.ModeAuto {
		t.Fatalf("integration runs in auto mode, got %v", m.policy.mode)
	}
	// The reports reached the integration prompt.
	if msgs := m.agent.Messages(); !strings.Contains(msgs[len(msgs)-1].Content, "INTEGRATE stage") || !strings.Contains(msgs[len(msgs)-1].Content, "Wrote a.go. Wire it up.") {
		t.Fatal("the integration prompt should carry the lane reports")
	}
}

// laneNamed finds one of the run's lanes by the name the split gave it.
func laneNamed(t *testing.T, m Model, name string) run.Lane {
	t.Helper()
	for _, l := range m.todoRunner.state.Lanes {
		if l.Name == name {
			return l
		}
	}
	t.Fatalf("no lane %s in %+v", name, m.todoRunner.state)
	return run.Lane{}
}

// A lane's patch reaches the session one event ahead of the writer that
// wrote it, so the last patch to land and the last writer to report are
// routinely different lanes. Holding one writer's done event back until
// the other lane has reported makes that crossing happen on purpose: the
// run has to wait for the report it was promised rather than take the
// integration turn with a lane that has nothing to say.
func TestTodoRun_LargeItemLanesIntegrateOnReportsNotPatches(t *testing.T) {
	m, sup := largeRunModel(t, writingEnv("package a\n\nvar changed = true\n"))
	var held []subagent.Event
	crossed := func(m Model) bool {
		return m.todoRunner.state != nil && laneNamed(t, m, "alpha").Done && laneNamed(t, m, "beta").Agent == ""
	}
	deadline := time.After(10 * time.Second)
	for !crossed(m) {
		select {
		case ev := <-sup.Events():
			if ev.Kind == subagent.EventDone && ev.Status.Name == "tw1-alpha" {
				held = append(held, ev)
				continue
			}
			updated, _ := m.handleSubagentEvent(ev)
			m = updated.(Model)
		case <-deadline:
			t.Fatalf("timed out; run = %+v", m.todoRunner.state)
		}
	}
	if m.todoRunner.state.Stage != run.StageFanOut {
		t.Fatalf("a landed patch is not a finished lane: %+v", m.todoRunner.state)
	}

	for _, ev := range held {
		updated, _ := m.handleSubagentEvent(ev)
		m = updated.(Model)
	}
	m = pumpSubagents(t, m, sup, func(m Model) bool {
		return m.todoRunner.state == nil || m.todoRunner.state.Stage != run.StageFanOut
	})
	if m.todoRunner.state == nil || m.todoRunner.state.Stage != run.StageImplement || !m.working() {
		t.Fatalf("the held report should start the integration turn: %+v", m.todoRunner.state)
	}
	if got := laneNamed(t, m, "alpha").Report; got != "Wrote a.go. Wire it up." {
		t.Errorf("the lane should carry the writer's report, not the event's detail line: %q", got)
	}
	if msgs := m.agent.Messages(); !strings.Contains(msgs[len(msgs)-1].Content, "Wrote a.go. Wire it up.") {
		t.Error("the integration prompt should carry the waited-for report")
	}
}

func TestTodoRun_WriterWithoutAPatchBlocksTheRun(t *testing.T) {
	m, sup := largeRunModel(t, reportingEnv("I looked and changed nothing."))
	m = pumpSubagents(t, m, sup, func(m Model) bool { return m.todoRunner.state == nil })
	found := false
	for _, e := range m.transcript {
		if strings.Contains(e.text, "patch did not land") {
			found = true
		}
	}
	if !found {
		t.Fatal("the evidence should say the lane's patch did not land")
	}
	it, _ := m.todoStore.Find("do-it")
	if it.Status != todo.StatusBlocked {
		t.Fatalf("item should be blocked, is %s", it.Status)
	}
	// The other writer is not left running on a run that is over.
	deadline := time.After(5 * time.Second)
	for {
		active, _ := sup.ActiveCounts()
		if active == 0 {
			break
		}
		select {
		case <-sup.Events():
		case <-deadline:
			t.Fatal("the surviving writer should have been killed")
		}
	}
}

func TestTodoRun_StopKillsTheWriters(t *testing.T) {
	m, sup := largeRunModel(t, blockingEnv())
	m.input.SetValue("/todo stop")
	updated, _ := m.submitInput()
	m = updated.(Model)
	if m.todoRunner.state != nil {
		t.Fatal("run should be over")
	}
	killed := 0
	deadline := time.After(5 * time.Second)
	for killed < 2 {
		select {
		case ev := <-sup.Events():
			if ev.Kind == subagent.EventDone && ev.Status.State != subagent.StateDone {
				killed++
			}
		case <-deadline:
			t.Fatalf("stop should kill both writers, killed %d", killed)
		}
	}
}

func TestTodoRun_FanOutWithoutASupervisorBuildsWhole(t *testing.T) {
	m, _ := runModel(t)
	m.input.SetValue("/todo run do-it")
	updated, _ := m.submitInput()
	m = answer(t, updated.(Model), strings.Replace(runPlan, "size: S", "size: L", 1))
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = answer(t, updated.(Model), "LANE: alpha\npaths: a.go\ntask: change a\n\nLANE: beta\npaths: b.go\ntask: create b\n")
	if m.todoRunner.state == nil || m.todoRunner.state.Stage != run.StageImplement || !m.working() || len(m.todoRunner.state.Lanes) != 0 {
		t.Fatalf("no supervisor should build the item in this session: %+v", m.todoRunner.state)
	}
}

// runModelNoRepo is runModel in a directory that is not a repository, which
// is the case the run used to discover only at the commit stage.
func runModelNoRepo(t *testing.T) (Model, string) {
	t.Helper()
	m, root := runModel(t)
	must(t, os.Remove(filepath.Join(root, ".git")))
	return m, root
}

// A run that would end in a commit is refused before the research turn, in
// one sentence naming the directory and both ways of asking for it anyway.
// Nothing is written: the item is not marked in progress and no checkpoint
// is left behind, so the backlog reads as it did before the command.
func TestTodoRun_OutsideARepositoryRefusesBeforeResearch(t *testing.T) {
	m, root := runModelNoRepo(t)
	m.input.SetValue("/todo run do-it")
	updated, _ := m.submitInput()
	m = updated.(Model)

	if m.todoRunner.state != nil || m.working() {
		t.Fatalf("no run should have started: run=%+v working=%v", m.todoRunner.state, m.working())
	}
	if it, _ := todo.Load(todo.BuiltinCode(), root).Find("do-it"); it.Status != todo.StatusOpen {
		t.Errorf("the item should be left open, got %s", it.Status)
	}
	if _, err := run.Load(root, "do-it"); err == nil {
		t.Error("a refused run should leave no checkpoint")
	}
	notice := m.transcript[len(m.transcript)-1].text
	for _, want := range []string{"not in a git repository", "--no-commit", "todo.commit = false"} {
		if !strings.Contains(notice, want) {
			t.Errorf("the refusal does not carry %q: %q", want, notice)
		}
	}
	if strings.Contains(notice, ". ") {
		t.Errorf("the refusal is more than one sentence: %q", notice)
	}
}

// The same run asked for without a commit works the item through and
// archives it, with the row and the report both saying it was not
// committed and naming where the change is.
func TestTodoRun_NoCommitArchivesAndSaysSo(t *testing.T) {
	m, root := runModelNoRepo(t)
	m.input.SetValue("/todo run do-it --no-commit")
	updated, _ := m.submitInput()
	m = updated.(Model)
	if m.todoRunner.state == nil || !m.todoRunner.state.NoCommit || m.todoRunner.state.Repo {
		t.Fatalf("the run should have started without a commit: %+v", m.todoRunner.state)
	}

	m = answer(t, m, runPlan)
	m.changes.Add(int64(m.todoRunner.state.Turn), changeset.Record{
		Path: filepath.Join(root, "a.go"), Before: "a", After: "b", BeforeExists: true, AfterExists: true,
	})
	m = answer(t, m, "Changed a.go.")
	updated, _ = m.Update(todoVerifyMsg{slug: "do-it", ok: true, output: "$ true → exit 0"})
	m = updated.(Model)
	if m.todoRunner.state.Stage != run.StageReview {
		t.Fatalf("review should follow a passing verify: %s", m.todoRunner.state.Stage)
	}
	m = answer(t, m, "verdict: clean")
	if m.todoRunner.state != nil || m.policy.mode != agent.ModeManual {
		t.Fatalf("a clean review should end the run and restore the mode: %+v", m.todoRunner.state)
	}

	done, ok := todo.Load(todo.BuiltinCode(), root).Find("do-it")
	if !ok || !done.Archived {
		t.Fatalf("the item should be archived: %+v", done)
	}
	for _, want := range []string{"not committed", "a.go"} {
		if !strings.Contains(done.Body, want) {
			t.Errorf("the archived report does not carry %q:\n%s", want, done.Body)
		}
	}
	if strings.Contains(done.Body, "Committed:") {
		t.Errorf("a run that made no commit must not claim one:\n%s", done.Body)
	}
	row := m.transcript[len(m.transcript)-1].text
	if !strings.Contains(row, "✓ todo run do-it done") || !strings.Contains(row, "not committed") {
		t.Errorf("the run row does not say the work was not committed: %q", row)
	}
}

// A word the command does not know is refused rather than taken as a slug:
// a mistyped flag would otherwise start a committing run on an item named
// after the typo, which is the answer the flag was there to avoid.
func TestParseTodoRunArgs(t *testing.T) {
	for _, c := range []struct {
		args []string
		want todoRunArgs
		ok   bool
	}{
		{nil, todoRunArgs{}, true},
		{[]string{"do-it"}, todoRunArgs{arg: "do-it"}, true},
		{[]string{"--next"}, todoRunArgs{arg: "--next"}, true},
		{[]string{"--no-commit"}, todoRunArgs{noCommit: true}, true},
		{[]string{"do-it", "--no-commit"}, todoRunArgs{arg: "do-it", noCommit: true}, true},
		{[]string{"--no-commit", "do-it"}, todoRunArgs{arg: "do-it", noCommit: true}, true},
		{[]string{"--all"}, todoRunArgs{all: true}, true},
		{[]string{"--all", "--max", "2"}, todoRunArgs{all: true, max: 2}, true},
		{[]string{"--all", "--max=2", "--no-commit"}, todoRunArgs{all: true, max: 2, noCommit: true}, true},
		{[]string{"--no-commmit"}, todoRunArgs{}, false},
		{[]string{"do-it", "and-this"}, todoRunArgs{}, false},
		// A sprint takes its items from the ready list; naming one beside it
		// is two requests in one command, and a cap on how many items are
		// worked says nothing about a single item.
		{[]string{"--all", "do-it"}, todoRunArgs{}, false},
		{[]string{"--all", "--next"}, todoRunArgs{}, false},
		{[]string{"--max", "2"}, todoRunArgs{}, false},
		{[]string{"--all", "--max"}, todoRunArgs{}, false},
		{[]string{"--all", "--max", "0"}, todoRunArgs{}, false},
		{[]string{"--all", "--max", "two"}, todoRunArgs{}, false},
	} {
		got, ok := parseTodoRunArgs(c.args)
		if got != c.want || ok != c.ok {
			t.Errorf("parseTodoRunArgs(%v) = %+v/%v, want %+v/%v", c.args, got, ok, c.want, c.ok)
		}
	}
}

// The staged-changes check reads three different failures out of git, and
// the sentence a wrong reading produced — one about an index that does not
// exist — is the defect.
func TestTodoCommitCmd_EachGitExitDrawsItsOwnSentence(t *testing.T) {
	root := t.TempDir()
	m := frameModel(t, 130, 40)
	m.changes = changeset.New(1 << 20)
	m = m.WithTodos(Todos{Profile: todo.BuiltinCode(), Root: root, Manage: func([]string) string { return "" }, Detail: func(*todo.Store, todo.Item) string { return "" }})
	m.todoRunner.state = &run.State{Slug: "x", Turn: 1, Message: "Change a\n\nBecause."}
	m.changes.Add(1, changeset.Record{Path: filepath.Join(root, "a.go"), Before: "a", After: "b", BeforeExists: true, AfterExists: true})

	if _, err := exec.LookPath("git"); err == nil {
		msg := m.todoCommitCmd()().(todoCommitMsg)
		if msg.err == nil || !strings.Contains(msg.err.Error(), "not a git repository") {
			t.Errorf("outside a repository the commit should say so: %v", msg.err)
		}
		if msg.err != nil && strings.Contains(msg.err.Error(), "staged changes") {
			t.Errorf("outside a repository there is no index to blame: %v", msg.err)
		}
	}

	t.Setenv("PATH", "")
	msg := m.todoCommitCmd()().(todoCommitMsg)
	if msg.err == nil || !strings.Contains(msg.err.Error(), "git is not on the path") {
		t.Errorf("without git the commit should say so: %v", msg.err)
	}
}

// sprintRunModel is runModel with a second ready item, so `--all` has a set
// to work rather than one item and an ending.
func sprintRunModel(t *testing.T) (Model, string) {
	t.Helper()
	m, root := runModel(t)
	body := []byte("---\ntitle: Later\nsize: S\n---\n## Tests\n- true\n")
	must(t, os.WriteFile(filepath.Join(todo.Dir(root), "zz-later.md"), body, 0o644))
	m.reloadTodos()
	return m, root
}

// finishSprintItem drives the item in flight from its research turn to the
// commit that archives it, which is the only ending the sprint counts as
// done.
func finishSprintItem(t *testing.T, m Model, root, slug string) Model {
	t.Helper()
	if m.todoRunner.state == nil || m.todoRunner.state.Slug != slug {
		t.Fatalf("expected a run on %s, got %+v", slug, m.todoRunner.state)
	}
	m = answer(t, m, runPlan)
	m.changes.Add(int64(m.todoRunner.state.Turn), changeset.Record{
		Path: filepath.Join(root, slug+".go"), Before: "a", After: "b", BeforeExists: true, AfterExists: true,
	})
	m = answer(t, m, "Changed "+slug+".go.")
	updated, _ := m.Update(todoVerifyMsg{slug: slug, ok: true, output: "$ true → exit 0"})
	m = answer(t, updated.(Model), "verdict: clean")
	m = answer(t, m, "COMMIT: Do "+slug+"\n\nBecause.\n\nREPORT: ## Report\nSummary: done.")
	updated, _ = m.Update(todoCommitMsg{slug: slug, files: []string{slug + ".go"}})
	return updated.(Model)
}

// The sprint is the whole story: two items, one session each, both archived,
// and the mode the session started in when it is over.
func TestTodoSprint_WorksTheReadyListOneItemPerSession(t *testing.T) {
	m, root := sprintRunModel(t)
	m.input.SetValue("/todo run --all")
	updated, _ := m.submitInput()
	m = updated.(Model)
	if m.todoRunner.state == nil || !m.todoRunner.state.InSprint || m.todoRunner.state.Slug != "do-it" {
		t.Fatalf("the sprint should have started the first ready item: %+v", m.todoRunner.state)
	}
	sp, live := run.Live(root)
	if !live || sp.Current != "do-it" {
		t.Fatalf("the sprint's checkpoint should name the item it is on: %+v", sp)
	}

	m = finishSprintItem(t, m, root, "do-it")

	// The boundary: turns are numbered from one again, the transcript is the
	// new session's, and the next item is already going in it.
	if m.todoRunner.state == nil || m.todoRunner.state.Slug != "zz-later" || !m.todoRunner.state.InSprint {
		t.Fatalf("the sprint should have crossed into the next item: %+v", m.todoRunner.state)
	}
	if m.todoRunner.state.Turn != 1 {
		t.Fatalf("the next item runs in a session of its own, from turn 1: turn %d", m.todoRunner.state.Turn)
	}
	sp, live = run.Live(root)
	if !live || len(sp.Done) != 1 || sp.Done[0] != "do-it" || sp.Current != "zz-later" {
		t.Fatalf("the sprint should have recorded the first item: %+v", sp)
	}

	m = finishSprintItem(t, m, root, "zz-later")

	if m.todoRunner.state != nil {
		t.Fatalf("the sprint should be over: %+v", m.todoRunner.state)
	}
	if _, live := run.Live(root); live {
		t.Fatal("a sprint that ran out of ready items leaves no checkpoint")
	}
	if m.policy.mode != agent.ModeManual {
		t.Fatalf("the starting mode should be back: %s", m.policy.mode)
	}
	store := todo.Load(todo.BuiltinCode(), root)
	for _, slug := range []string{"do-it", "zz-later"} {
		if it, ok := store.Find(slug); !ok || !it.Archived {
			t.Fatalf("%s should be archived: %+v", slug, it)
		}
	}
	if note := m.transcript[len(m.transcript)-1].text; !strings.Contains(note, "Sprint over") || !strings.Contains(note, "empty") {
		t.Fatalf("the sprint's end should say which ending it was: %q", note)
	}
}

// A block stops the sprint where it is: the item that blocked keeps its
// evidence, and the one behind it is not touched.
func TestTodoSprint_StopsOnTheFirstBlock(t *testing.T) {
	m, root := sprintRunModel(t)
	m.input.SetValue("/todo run --all")
	updated, _ := m.submitInput()
	m = updated.(Model)
	m = answer(t, m, "## Plan: x\n\n1. a\n\nsize: S\nquestions:\n- keep the flag?\n")

	if m.todoRunner.state != nil || m.policy.mode != agent.ModeManual {
		t.Fatalf("the block should have ended the run and restored the mode: %+v", m.todoRunner.state)
	}
	if _, live := run.Live(root); live {
		t.Fatal("a sprint that stopped on a block leaves no checkpoint")
	}
	store := todo.Load(todo.BuiltinCode(), root)
	if it, _ := store.Find("do-it"); it.Status != todo.StatusBlocked {
		t.Fatalf("the blocked item keeps its status: %s", it.Status)
	}
	if it, _ := store.Find("zz-later"); it.Status != todo.StatusOpen {
		t.Fatalf("nothing further is attempted: zz-later is %s", it.Status)
	}
	said := false
	for _, e := range m.transcript {
		if strings.Contains(e.text, "Sprint over") && strings.Contains(e.text, "stops on the first block") {
			said = true
		}
	}
	if !said {
		t.Fatal("the sprint's end should say it stopped on the block")
	}
}

// --max is a bound on how many items a sprint starts, not on the backlog.
func TestTodoSprint_MaxRunsOneAndStops(t *testing.T) {
	m, root := sprintRunModel(t)
	m.input.SetValue("/todo run --all --max 1")
	updated, _ := m.submitInput()
	m = finishSprintItem(t, updated.(Model), root, "do-it")

	if m.todoRunner.state != nil {
		t.Fatalf("the cap should have ended the sprint: %+v", m.todoRunner.state)
	}
	store := todo.Load(todo.BuiltinCode(), root)
	if it, _ := store.Find("zz-later"); it.Archived || it.Status != todo.StatusOpen {
		t.Fatalf("the backlog should still hold zz-later, open: %+v", it)
	}
	if note := m.transcript[len(m.transcript)-1].text; !strings.Contains(note, "capped") {
		t.Fatalf("the end should name the cap: %q", note)
	}
}

// A sprint that died with its process is picked up by the same command: its
// checkpoint names the item, and the item's checkpoint names the stage.
func TestTodoSprint_ResumesFromItsCheckpoint(t *testing.T) {
	m, root := sprintRunModel(t)
	m.input.SetValue("/todo run --all")
	updated, _ := m.submitInput()
	m = updated.(Model)
	m = answer(t, m, runPlan)
	m = answer(t, m, "Changed a.go.")
	if m.todoRunner.state.Stage != run.StageVerify {
		t.Fatalf("stage = %s", m.todoRunner.state.Stage)
	}

	// The session ends with the run part-way through, the way /new ends one.
	m.startNewSession()
	if _, live := run.Live(root); !live {
		t.Fatal("the sprint's checkpoint should survive the session")
	}

	m.input.SetValue("/todo run --all")
	updated, _ = m.submitInput()
	m = updated.(Model)
	if m.todoRunner.state == nil || m.todoRunner.state.Slug != "do-it" || m.todoRunner.state.Stage != run.StageVerify {
		t.Fatalf("the sprint should have continued the item it was on: %+v", m.todoRunner.state)
	}
	if !m.todoRunner.state.InSprint {
		t.Fatal("the continued run is still the sprint's")
	}
}

// /todo stop over a sprint keeps the item's checkpoint: the stages already
// done are in the tree, and the stop was aimed at the loop.
func TestTodoSprint_StopKeepsTheItemsCheckpoint(t *testing.T) {
	m, root := sprintRunModel(t)
	m.input.SetValue("/todo run --all")
	updated, _ := m.submitInput()
	m = updated.(Model)
	m = answer(t, m, runPlan)
	m = answer(t, m, "Changed a.go.")

	m.input.SetValue("/todo stop")
	updated, _ = m.submitInput()
	m = updated.(Model)

	if m.todoRunner.state != nil || m.policy.mode != agent.ModeManual {
		t.Fatalf("the sprint should be over and the mode back: %+v", m.todoRunner.state)
	}
	if _, live := run.Live(root); live {
		t.Fatal("a stopped sprint leaves no checkpoint")
	}
	if st, err := run.Load(root, "do-it"); err != nil || st.Over() {
		t.Fatalf("the item's own checkpoint should be kept: %v", err)
	}
	if it, _ := todo.Load(todo.BuiltinCode(), root).Find("do-it"); it.Status != todo.StatusInProgress {
		t.Fatalf("the item stays in progress with its checkpoint: %s", it.Status)
	}
	if note := m.transcript[len(m.transcript)-1].text; !strings.Contains(note, "/todo run do-it") {
		t.Fatalf("the note should name the command that continues the item: %q", note)
	}
}

// The cap is read at the boundary between two stages, which is the smallest
// thing the runner can end.
func TestTodoSprint_ItemTimeoutBlocksTheItem(t *testing.T) {
	m, root := sprintRunModel(t)
	m.todos.ItemTimeout = time.Minute
	m.input.SetValue("/todo run --all")
	updated, _ := m.submitInput()
	m = updated.(Model)

	sp, live := run.Live(root)
	if !live {
		t.Fatal("no sprint")
	}
	sp.ItemStarted = time.Now().Add(-time.Hour)
	must(t, sp.Save(root))

	m = answer(t, m, runPlan)
	if m.todoRunner.state != nil {
		t.Fatalf("the cap should have blocked the item: %+v", m.todoRunner.state)
	}
	it, _ := todo.Load(todo.BuiltinCode(), root).Find("do-it")
	if it.Status != todo.StatusBlocked || !strings.Contains(it.Body, "ran past the cap") {
		t.Fatalf("the evidence should name the cap: %+v", it)
	}
}

// The rail says which item the sprint is on and what stage it is at, so a
// reader who is not watching the transcript can see it moving.
func TestTodoSprint_RailNamesTheCurrentItem(t *testing.T) {
	m, _ := sprintRunModel(t)
	m.input.SetValue("/todo run --all")
	updated, _ := m.submitInput()
	m = updated.(Model)
	block := m.inspectorTodo()
	if block == nil || block.SprintItem != "do-it" || block.SprintStage != "research" {
		t.Fatalf("the rail should name the sprint's item and stage: %+v", block)
	}
	m.todoRunner.state = nil
	if block := m.inspectorTodo(); block.SprintItem != "" {
		t.Fatalf("with no run there is no current item: %+v", block)
	}
}

// A sprint is unattended by definition, so it honours the workspace's
// on-close suite without the reader having turned it on.
func TestTodoSprint_ArmsTheCloseGate(t *testing.T) {
	m, _ := sprintRunModel(t)
	if m.closeGateArmed() {
		t.Fatal("an interactive session leaves the gate off")
	}
	m.todoRunner.state = &run.State{Slug: "do-it", InSprint: true, Stage: run.StageResearch}
	if !m.closeGateArmed() {
		t.Fatal("a sprint's run arms the gate at every stage")
	}
}

// The notification a finished turn raises names the item and how far the
// sprint has got, because a reader who left one running has thirty items to
// choose from when they come back.
func TestTodoSprint_TurnCloseWordsNameTheItem(t *testing.T) {
	m, root := sprintRunModel(t)
	m.input.SetValue("/todo run --all")
	updated, _ := m.submitInput()
	m = updated.(Model)
	_, body := m.turnCloseWords()
	if !strings.Contains(body, "do-it") || !strings.Contains(body, "research") || !strings.Contains(body, "sprint 0 items done") {
		t.Fatalf("turn-close words = %q", body)
	}
	run.DiscardSprint(root)
	m.todoRunner.state = nil
	if _, body := m.turnCloseWords(); strings.Contains(body, "sprint") {
		t.Fatalf("a session with no sprint says nothing about one: %q", body)
	}
}

// /todo status over a sprint says where the loop is above the row the run is
// drawn on.
func TestTodoSprint_StatusNamesTheSprint(t *testing.T) {
	m, _ := sprintRunModel(t)
	m.input.SetValue("/todo run --all")
	updated, _ := m.submitInput()
	m = updated.(Model)
	m.input.SetValue("/todo status")
	updated, _ = m.submitInput()
	next := updated.(Model)
	said := false
	for _, e := range next.transcript {
		if strings.Contains(e.text, "sprint · 0 items done") && strings.Contains(e.text, "on do-it") {
			said = true
		}
	}
	if !said || next.state != stateFocus {
		t.Fatalf("status should say where the sprint is and open the row: state=%d", next.state)
	}
}

// cutAnswer ends the in-flight stage turn with a reply the model's output
// ceiling stopped mid-sentence. It reads on screen exactly like the answer
// above, which is the whole reason the run has to ask how the turn ended.
func cutAnswer(t *testing.T, m Model, text string) Model {
	t.Helper()
	if !m.working() {
		t.Fatalf("no stage turn in flight (state %d)", m.state)
	}
	m.streaming = text
	updated, _ := m.Update(doneMsg{stop: provider.StopLength})
	return updated.(Model)
}

// The ceiling is arithmetic and the model can write past it, so the run has
// the sentence finished before it grades anything — and grades the whole of
// what the model wrote, not the part that came back last.
func TestTodoRun_ACutStageAnswerIsFinishedBeforeItIsRead(t *testing.T) {
	m, _ := runModel(t)
	m.input.SetValue("/todo run do-it")
	updated, _ := m.submitInput()
	m = updated.(Model)

	// The size is split across the halves, so neither is a plan on its own.
	tail := "ze: S\nquestions: none\n"
	m = cutAnswer(t, m, strings.TrimSuffix(runPlan, tail))
	if m.todoRunner.state.Stage != run.StageResearch {
		t.Fatalf("half an answer is not the stage's answer, moved to %s", m.todoRunner.state.Stage)
	}
	if !m.working() {
		t.Fatal("the run should have asked for the rest of the reply itself")
	}
	msgs := m.agent.Messages()
	if last := msgs[len(msgs)-1]; last.Role != provider.RoleUser || last.Content != agent.ContinueAfterCeiling {
		t.Fatalf("the run sends the instruction the row's key sends, got %+v", last)
	}

	m = answer(t, m, tail)
	if m.todoRunner.state.Stage != run.StageImplement {
		t.Fatalf("the finished answer should be read as the plan, stage %s", m.todoRunner.state.Stage)
	}
	if m.todoRunner.state.Grade != "S" {
		t.Fatalf("the size arrived in the second half, got %q", m.todoRunner.state.Grade)
	}
}

// One continuation per stage. A second ceiling is the run's evidence rather
// than a third attempt, because a stage free to ask for one more paragraph
// every time it filled a budget would be under no ceiling at all.
func TestTodoRun_ASecondCeilingInAStageBlocksIt(t *testing.T) {
	m, root := runModel(t)
	m.input.SetValue("/todo run do-it")
	updated, _ := m.submitInput()
	m = updated.(Model)

	m = cutAnswer(t, m, "## Plan: do it\n\n1. Change a.go\n   files: a.g")
	m = cutAnswer(t, m, "o\n   action: edit\n\nsi")

	if m.todoRunner.state != nil || m.policy.mode != agent.ModeManual {
		t.Fatalf("the run should have ended and the mode restored: %v %s", m.todoRunner.state, m.policy.mode)
	}
	it, _ := todo.Load(todo.BuiltinCode(), root).Find("do-it")
	if it.Status != todo.StatusBlocked || !strings.Contains(it.Body, run.CutAtCeiling(run.StageResearch)) {
		t.Fatalf("the ceiling should be the evidence on the item: %+v", it)
	}
}

// A dropped wire is the other way a reply stops short, and it is not the
// run's to answer: what was kept is half a sentence and whether it is worth
// having is the judgement the row offers a reader. So the run lets go at its
// checkpoint, the way a displaced turn does.
func TestTodoRun_ADroppedStageTurnPausesAtTheCheckpoint(t *testing.T) {
	m, root := runModel(t)
	m.input.SetValue("/todo run do-it")
	updated, _ := m.submitInput()
	m = updated.(Model)

	m.streaming = "## Plan: do it\n\n1. Change a.g"
	updated, _ = m.Update(streamErrMsg{err: networkFailure()})
	m = updated.(Model)

	if m.todoRunner.state != nil || m.policy.mode != agent.ModeManual {
		t.Fatalf("the run should be let go of and the mode restored: %v %s", m.todoRunner.state, m.policy.mode)
	}
	if it, _ := todo.Load(todo.BuiltinCode(), root).Find("do-it"); it.Status != todo.StatusInProgress {
		t.Fatalf("nothing about the item is wrong, so it stays in progress, is %s", it.Status)
	}
	st, err := run.Load(root, "do-it")
	if err != nil || st.Stage != run.StageResearch {
		t.Fatalf("the checkpoint should be kept at research: %+v %v", st, err)
	}
	note := m.transcript[len(m.transcript)-1].text
	if !strings.Contains(note, "dropped mid-reply") || !strings.Contains(note, "/todo run do-it continues it") {
		t.Fatalf("the row should say why and how to pick it up, got %q", note)
	}
}

// The wordings the session read reach the run, so a stage sends what the
// project's file says rather than the built-in words.
func TestTodoRun_TheSessionsWordingsReachTheRun(t *testing.T) {
	m, root := runModel(t)
	m.todos.Wordings = run.Wordings{"research": "READ IT MY WAY"}
	m = m.WithTodos(m.todos)
	updated, _ := m.startTodoRun("do-it", false)
	m = updated.(Model)
	if m.todoRunner.state == nil {
		t.Fatal("the run did not start")
	}
	if m.todoRunner.state.Wordings["research"] != "READ IT MY WAY" {
		t.Fatalf("the run's wordings = %+v", m.todoRunner.state.Wordings)
	}

	// A run continued from a checkpoint takes the wordings this session
	// read: the checkpoint holds none, because they are files on disk and a
	// session that starts reads them as they now stand.
	if err := m.todoRunner.state.Save(root); err != nil {
		t.Fatal(err)
	}
	m2 := frameModel(t, 130, 40)
	m2.changes = changeset.New(1 << 20)
	m2.policy.mode = agent.ModeManual
	m2 = m2.WithTodos(Todos{
		Profile: todo.BuiltinCode(), Root: root, Manage: func([]string) string { return "" },
		Detail:   func(*todo.Store, todo.Item) string { return "" },
		Wordings: run.Wordings{"research": "READ IT ANOTHER WAY"},
	})
	updated, _ = m2.startTodoRun("do-it", false)
	m2 = updated.(Model)
	if m2.todoRunner.state == nil || m2.todoRunner.state.Stage != run.StageResearch {
		t.Fatalf("the run did not continue from the checkpoint: %+v", m2.todoRunner.state)
	}
	if m2.todoRunner.state.Wordings["research"] != "READ IT ANOTHER WAY" {
		t.Fatalf("a continued run did not take this session's wordings: %+v", m2.todoRunner.state.Wordings)
	}
}
