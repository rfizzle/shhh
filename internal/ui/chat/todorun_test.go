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
	root := t.TempDir()
	dir := todo.Dir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "do-it.md"), []byte("---\ntitle: Do it\nsize: M\n---\n## Tests\n- true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := frameModel(t, 130, 40)
	m.changes = changeset.New(1 << 20)
	m.mode = agent.ModeManual
	m = m.WithTodos(Todos{Root: root, Manage: func([]string) string { return "" }, Detail: func(*todo.Store, todo.Item) string { return "" }})
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
	if m.todoRun == nil || m.todoRun.Stage != run.StageResearch || m.mode != agent.ModePlan || !m.working() {
		t.Fatalf("research should be in flight in plan mode: run=%+v mode=%s", m.todoRun, m.mode)
	}
	if it, _ := todo.Load(root).Find("do-it"); it.Status != todo.StatusInProgress {
		t.Fatal("the item should be in progress")
	}
	if _, err := run.Load(root, "do-it"); err != nil {
		t.Fatal("no checkpoint written")
	}
	if last := m.transcript[len(m.transcript)-1]; last.kind != entryUser || !strings.Contains(last.text, "research") {
		t.Fatalf("the shown line should be the stage label, got %+v", last)
	}

	m = answer(t, m, runPlan)
	if m.todoRun.Stage != run.StageImplement || m.mode != agent.ModeAuto || !m.working() || m.state == statePlanApprove {
		t.Fatalf("implement should follow research in auto mode, no plan card: stage=%s mode=%s state=%d", m.todoRun.Stage, m.mode, m.state)
	}
	if m.todoRun.Size != todo.SizeS {
		t.Fatalf("size should be re-graded from research, got %s", m.todoRun.Size)
	}

	m = answer(t, m, "Changed a.go.")
	if m.todoRun.Stage != run.StageVerify || m.working() {
		t.Fatalf("verify should follow implement: %s", m.todoRun.Stage)
	}
	updated, _ = m.Update(todoVerifyMsg{slug: "do-it", ok: true, output: "$ true → exit 0"})
	m = updated.(Model)
	if m.todoRun.Stage != run.StageReview || !m.working() {
		t.Fatalf("review should follow a passing verify: %s", m.todoRun.Stage)
	}
	m = answer(t, m, "verdict: clean")
	if m.todoRun.Stage != run.StageCommit || !m.working() {
		t.Fatalf("commit turn should follow a clean review: %s", m.todoRun.Stage)
	}
	m = answer(t, m, "COMMIT:\nDo it\n\nBody.\nREPORT:\n## Report\nSummary: did it")
	if m.todoRun.Message != "Do it\n\nBody." || m.working() {
		t.Fatalf("the commit turn should be read and the commit started: %+v", m.todoRun)
	}
	updated, _ = m.Update(todoCommitMsg{slug: "do-it", files: []string{"a.go"}})
	m = updated.(Model)
	if m.todoRun != nil || m.mode != agent.ModeManual {
		t.Fatalf("done should end the run and restore the mode: run=%v mode=%s", m.todoRun, m.mode)
	}
	s := todo.Load(root)
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
	if m.todoRun != nil || m.mode != agent.ModeManual {
		t.Fatalf("an open question should block and restore the mode: %v %s", m.todoRun, m.mode)
	}
	it, _ := todo.Load(root).Find("do-it")
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

func TestTodoRun_StopAndClearReopenTheItem(t *testing.T) {
	for _, way := range []string{"stop", "clear"} {
		m, root := runModel(t)
		m.input.SetValue("/todo run do-it")
		updated, _ := m.submitInput()
		m = updated.(Model)
		m = answer(t, m, runPlan)
		m = answer(t, m, "done")
		if way == "stop" {
			m.input.SetValue("/todo stop")
			updated, _ = m.submitInput()
			m = updated.(Model)
		} else {
			m.clearConversation()
		}
		if m.todoRun != nil || m.mode != agent.ModeManual {
			t.Fatalf("%s: run should end and mode restore", way)
		}
		if it, _ := todo.Load(root).Find("do-it"); it.Status != todo.StatusOpen {
			t.Fatalf("%s: item should be open again, is %s", way, it.Status)
		}
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
	m.input.SetValue("/todo status")
	updated, _ = m.submitInput()
	if note := updated.(Model).transcript[len(updated.(Model).transcript)-1].text; !strings.Contains(note, "do-it · research") {
		t.Fatalf("status mid-run: %q", note)
	}
	block := m.inspectorTodo()
	if block == nil || block.Rows[0].Note != "research" {
		t.Fatalf("the rail should name the stage: %+v", block)
	}
	m.changes = nil
	m.todoRun = nil
	m.input.SetValue("/todo run do-it")
	m.state = stateInput
	updated, _ = m.submitInput()
	if note := updated.(Model).transcript[len(updated.(Model).transcript)-1].text; !strings.Contains(note, "does not track changes") {
		t.Fatalf("no changeset: %q", note)
	}
}

func TestTodoVerifyCmd_RunsSnapshotAndReportsFailure(t *testing.T) {
	m, root := runModel(t)
	m.todoRun = &run.State{Slug: "do-it", Tests: []string{"true", "exit 3"}}
	m.todoRunItem = todo.Item{Slug: "do-it", Body: "## Tests\n- echo MODEL-WROTE-THIS\n"}
	msg := m.todoVerifyCmd()().(todoVerifyMsg)
	if msg.ok || !strings.Contains(msg.output, "$ exit 3 → exit 3") || strings.Contains(msg.output, "MODEL-WROTE-THIS") {
		t.Fatalf("verify = %+v", msg)
	}
	m.todoRun = &run.State{Slug: "do-it"}
	msg = m.todoVerifyCmd()().(todoVerifyMsg)
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
	if m.todoRun.Stage != run.StageVerify {
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
	if m.todoRun.Stage != run.StageVerify {
		t.Fatalf("a stale verify or a commit in the wrong stage must be ignored, stage=%s", m.todoRun.Stage)
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
	if m.todoRun != nil {
		t.Fatal("a cancelled stage turn should stop the run, not be graded")
	}
	if it, _ := todo.Load(root).Find("do-it"); it.Status != todo.StatusOpen {
		t.Fatalf("item should be open, is %s", it.Status)
	}
}

func TestTodoRun_ArchiveFailureAfterCommitReopensWithReport(t *testing.T) {
	m, root := runModel(t)
	os.MkdirAll(filepath.Join(todo.Dir(root), todo.DoneSubdir), 0o755)
	os.WriteFile(filepath.Join(todo.Dir(root), todo.DoneSubdir, "do-it.md"), []byte("---\ntitle: old\nstatus: done\n---\n"), 0o644)
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
	it, _ := todo.Load(root).Find("do-it")
	if it.Archived || it.Status != todo.StatusOpen || !strings.Contains(it.Body, "Summary: did it") {
		t.Fatalf("item after failed archive = %+v", it)
	}
	if !strings.Contains(m.transcript[len(m.transcript)-1].text, "could not be archived") {
		t.Fatal("the note should say the archive failed")
	}
}

func TestTodoRunPaths_OnlyThisRunUnderRootNeverBacklog(t *testing.T) {
	m, root := runModel(t)
	m.todoRun = &run.State{Slug: "do-it", Turn: 3}
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
	os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0o644)
	os.WriteFile(filepath.Join(root, "stray.go"), []byte("package a\n"), 0o644)
	gitc("add", "a.go")
	gitc("commit", "-q", "-m", "seed")
	os.WriteFile(filepath.Join(root, "a.go"), []byte("package a // changed\n"), 0o644)

	m := frameModel(t, 130, 40)
	m.changes = changeset.New(1 << 20)
	m = m.WithTodos(Todos{Root: root, Manage: func([]string) string { return "" }, Detail: func(*todo.Store, todo.Item) string { return "" }})
	m.todoRun = &run.State{Slug: "x", Turn: 1, Message: "Change a\n\nBecause."}
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
	if m.state != stateTodoPause || m.todoPause == nil || m.todoRun.Paused == "" {
		t.Fatalf("an L plan should pause: state=%d", m.state)
	}
	lines := strings.Join(m.todoPauseLines(), "\n")
	if !strings.Contains(lines, "size L (was M)") || !strings.Contains(lines, "? keep the flag?") || !strings.Contains(lines, "Go ahead") {
		t.Fatalf("pause card = %q", lines)
	}
	if m.inspectorHidden() != true {
		t.Fatal("the pause takes the panel like the other cards")
	}

	// Re-plan with a note: research runs again with the answer in front.
	m = press(t, m, "down")
	m.todoPause.Note.SetValue("keep it")
	m = press(t, m, "enter")
	if m.state == stateTodoPause || m.todoRun.Stage != run.StageResearch || !m.working() || m.mode != agent.ModeManual && m.mode != agent.ModePlan {
		t.Fatalf("replan should send research again: stage=%s state=%d", m.todoRun.Stage, m.state)
	}
	if it, _ := todo.Load(root).Find("do-it"); !strings.Contains(it.Body, "## Answers\nkeep it") {
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
	if m.todoRun.Stage != run.StageImplement || !m.working() || m.mode != agent.ModeAuto {
		t.Fatalf("go ahead should implement in auto: stage=%s mode=%s", m.todoRun.Stage, m.mode)
	}

	// Stop from the card.
	m2, root2 := runModel(t)
	m2.input.SetValue("/todo run do-it")
	updated, _ = m2.submitInput()
	m2 = answer(t, updated.(Model), largePlan)
	m2 = press(t, m2, "esc")
	if m2.todoRun != nil || m2.state != stateInput {
		t.Fatal("esc on the pause should stop the run")
	}
	if it, _ := todo.Load(root2).Find("do-it"); it.Status != todo.StatusOpen {
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
	if m.todoRun == nil || m.todoRun.Stage != run.StageReview || !m.working() || m.todoRun.Reviewer != "" {
		t.Fatalf("no supervisor: the session should review itself: %+v", m.todoRun)
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
	if m.todoRun == nil || m.todoRun.Reviewer != "todo-review-do-it-1" || m.working() {
		t.Fatalf("a reviewer child should be spawned: %+v", m.todoRun)
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
	if m.todoRun.Stage != run.StageCommit || !m.working() || m.todoRun.Reviewer != "" {
		t.Fatalf("a clean verdict from the child should go to the commit turn: %+v", m.todoRun)
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
	s := todo.Load(root)
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
		stream := func(msgs []provider.Message) (<-chan provider.StreamEvent, context.CancelFunc, error) {
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
	if m.todoRun != nil {
		t.Fatalf("a killed reviewer should block the run, got stage %s", m.todoRun.Stage)
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
	if m.todoRun != nil {
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
	if m.todoRun != nil {
		t.Fatal("nothing changed: the run should block rather than review the whole tree")
	}
}

func TestTodoRun_GoAheadNoteReachesImplement(t *testing.T) {
	m, _ := runModel(t)
	m.input.SetValue("/todo run do-it")
	updated, _ := m.submitInput()
	m = answer(t, updated.(Model), largePlan)
	m.todoPause.Note.SetValue("use the old flag")
	m = press(t, m, "enter")
	if !m.working() || m.todoRun.Stage != run.StageImplement {
		t.Fatal("go ahead should implement")
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
	if m.todoRun.Stage != run.StageImplement {
		t.Fatal("should be implementing")
	}
	// Another turn gets in ahead of the stage: a compaction-like user turn.
	updated, _ = m.sendUserMessage("summarise")
	m = answer(t, updated.(Model), "summary")
	if m.todoRun != nil {
		t.Fatal("a displaced stage should pause the run")
	}
	it, _ := todo.Load(root).Find("do-it")
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
	m2.mode = agent.ModeManual
	m2 = m2.WithTodos(Todos{Root: root, Manage: func([]string) string { return "" }, Detail: func(*todo.Store, todo.Item) string { return "" }})
	m2.input.SetValue("/todo run do-it")
	updated, _ = m2.submitInput()
	m2 = updated.(Model)
	if m2.todoRun == nil || m2.todoRun.Stage != run.StageImplement || !m2.working() || m2.mode != agent.ModeAuto {
		t.Fatalf("should continue at implement in auto: %+v", m2.todoRun)
	}
	if len(m2.todoRun.Steps) != 1 {
		t.Fatal("the plan should come back with the checkpoint")
	}
	m2 = answer(t, m2, "done")
	if m2.todoRun.Stage != run.StageVerify {
		t.Fatal("the continued run should carry on")
	}

	// In progress with no checkpoint: told how to start over.
	run.Discard(root, "do-it")
	m3 := frameModel(t, 130, 40)
	m3.changes = changeset.New(1 << 20)
	m3 = m3.WithTodos(Todos{Root: root, Manage: func([]string) string { return "" }, Detail: func(*todo.Store, todo.Item) string { return "" }})
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
	m2 = m2.WithTodos(Todos{Root: root, Manage: func([]string) string { return "" }, Detail: func(*todo.Store, todo.Item) string { return "" }})
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
	if m.todoRun.Reviewer != "todo-review-do-it-1" {
		t.Fatalf("reviewer = %q", m.todoRun.Reviewer)
	}
	// Displace: a user turn while the reviewer reads pauses the run.
	updated, _ := m.sendUserMessage("x")
	m = answer(t, updated.(Model), "y")
	if m.todoRun != nil {
		t.Fatal("should have paused")
	}
	waitDone(t, sup)
	m.input.SetValue("/todo run do-it")
	updated, _ = m.submitInput()
	m = updated.(Model)
	if m.todoRun == nil || m.todoRun.Reviewer != "todo-review-do-it-2" {
		t.Fatalf("a continued review should spawn a fresh child: %+v", m.todoRun)
	}
	if _, ok := sup.Get("todo-review-do-it-2"); !ok {
		t.Fatal("the second child should exist")
	}
}
