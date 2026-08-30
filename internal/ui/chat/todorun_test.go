package chat

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/changeset"
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
