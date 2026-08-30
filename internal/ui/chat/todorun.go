package chat

// The backlog runner in the session: /todo run works one item through
// research, implement, verify, review, commit and archive, one turn per
// stage, with the gates decided by internal/todo/run rather than by the
// model. The session side is thin on purpose: it sends the prompt a stage
// hands it, notices when the turn ends, reads the answer back, and does the
// two things a model must not — run the verification and make the commit.
// See docs/capabilities/todo.md#a-run-is-turns-with-gates-between-them.
//
// A run always works in auto mode, whatever the session was in, and puts
// the session's mode back when it ends. The reader steers only where the
// classifier fails closed and asks, or where the run blocks and says why.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/runner"
	"github.com/rfizzle/shhh/internal/todo"
	"github.com/rfizzle/shhh/internal/todo/run"
)

// todoVerifyMsg is the verify stage's outcome.
type todoVerifyMsg struct {
	slug   string
	ok     bool
	output string
}

// todoCommitMsg is the commit's outcome: the paths it staged, or why not.
type todoCommitMsg struct {
	slug  string
	files []string
	err   error
}

// verifyTimeout bounds the whole verify stage — every test the item lists
// plus the project's checks.
const verifyTimeout = 15 * time.Minute

// startTodoRun begins a run on an item. It refuses a second run, an item
// that is not ready, and a session without the changeset tracking the
// commit stage needs to know what it may stage.
func (m Model) startTodoRun(arg string) (tea.Model, tea.Cmd) {
	if m.todoRun != nil && !m.todoRun.Over() {
		return m.systemNotice(fmt.Sprintf("A run is already going: %s. /todo status shows it; /todo stop ends it.", m.todoRun.Summary()))
	}
	s := m.todoStore
	if s == nil {
		return m.systemNotice("No backlog to run from.")
	}
	var it todo.Item
	var ok bool
	if arg == "" || arg == "--next" {
		if it, ok = s.Next(); !ok {
			return m.systemNotice("Nothing is ready: every open item waits on another, or the backlog is empty.")
		}
	} else if it, ok = s.Find(arg); !ok || it.Archived {
		return m.systemNotice(fmt.Sprintf("No active backlog item %q; /todo lists them.", arg))
	}
	if waiting := s.Waiting(it); len(waiting) > 0 {
		return m.systemNotice(fmt.Sprintf("%s waits on %s; run those first, or take the dependency out of the file.", it.Slug, strings.Join(waiting, ", ")))
	}
	if it.Status == todo.StatusBlocked {
		return m.systemNotice(fmt.Sprintf("%s is blocked; /todo open %s reopens it once the block is settled.", it.Slug, it.Slug))
	}
	if m.changes == nil {
		return m.systemNotice("This session does not track changes, so a run could not know what to commit.")
	}
	if err := todo.SetStatus(it.Path, todo.StatusInProgress); err != nil {
		return m.systemNotice("Could not mark the item in progress: " + err.Error())
	}
	if m.turnState() != stateInput {
		return m.systemNotice("Answer the open decision first; a run starts from an idle session.")
	}
	m.todoRun = run.Start(it, m.sessionName, m.mode.String(), int(m.turnCount)+1)
	m.todoRunItem = it
	m.reloadTodos()
	return m.todoRunStep(m.todoRun.First(it, ""))
}

// todoRunStep carries out one step the machine handed back.
func (m Model) todoRunStep(step run.Step) (tea.Model, tea.Cmd) {
	st := m.todoRun
	if err := st.Save(m.todos.Root); err != nil {
		m.appendEntry(entry{kind: entrySystem, text: "The run's checkpoint could not be written — " + err.Error()})
	}
	switch step.Action {
	case run.ActionPrompt:
		mode := agent.ModeAuto
		if step.Mode == run.ModePlan {
			mode = agent.ModePlan
		}
		m.applyMode(mode)
		m.todoRunMark = len(m.transcript)
		m.todoRunTurn = int(m.turnCount) + 1
		return m.sendUserMessageAs(step.Prompt, step.Shown)
	case run.ActionVerify:
		model, _ := m.systemNotice(step.Shown + " — running the item's tests and the project's checks")
		return model, m.todoVerifyCmd()
	case run.ActionCommit:
		model, _ := m.systemNotice(step.Shown + " — staging the run's files")
		return model, m.todoCommitCmd()
	case run.ActionBlocked:
		return m.todoRunBlocked()
	case run.ActionDone:
		return m.todoRunDone()
	}
	return m, nil
}

// todoRunAfter is the turn-end hook, derived from the model before against
// the model after the way the summary's close is: a stage's turn ending is
// a transition, not a message any one handler could be trusted to send.
// It waits out a round-limit pause and a decision card — those are the
// reader's — and reads the answer only when the turn is truly over.
func (m Model) todoRunAfter(prev Model) (Model, tea.Cmd) {
	st := m.todoRun
	if st == nil || st.Over() || !prev.working() || m.working() {
		return m, nil
	}
	if m.turnState() != stateInput || m.pausedAtRoundLimit() {
		return m, nil
	}
	switch st.Stage {
	case run.StageResearch, run.StageImplement, run.StageRemediate, run.StageReview, run.StageCommit:
	default:
		return m, nil
	}
	if st.Stage == run.StageCommit && st.Message != "" {
		// The commit turn was already read; the commit itself is in flight.
		return m, nil
	}
	if int(m.turnCount) != m.todoRunTurn {
		// The turn that ended is not the stage's — a message got in ahead
		// of it. Its answer is not the stage's answer, so the run stops.
		next, cmd := m.todoRunStep(st.Block("the " + string(st.Stage) + " turn was displaced by another message"))
		return next.(Model), cmd
	}
	if m.todoRunCancelled {
		// esc ended the stage turn with a partial answer. A cancel is the
		// reader stopping the run, not evidence to grade.
		m.todoRunCancelled = false
		next, cmd := m.stopTodoRun()
		return next.(Model), cmd
	}
	next, cmd := m.todoRunStep(st.Observe(m.todoRunItem, m.todoStageAnswer()))
	return next.(Model), cmd
}

// todoRunHoldsInput is why a plain message is refused while a run is
// going: text typed mid-stage would be steering the model out of its
// stage, and text typed between stages would start a turn whose edits the
// run would then commit as its own.
func (m Model) todoRunHoldsInput() (string, bool) {
	if m.todoRun == nil || m.todoRun.Over() {
		return "", false
	}
	return fmt.Sprintf("a backlog run is going (%s · %s) — /todo stop ends it, /todo status shows it; commands still work", m.todoRun.Slug, m.todoRun.Stage), true
}

// todoStageAnswer is the assistant's last message since the stage began.
func (m Model) todoStageAnswer() string {
	for i := len(m.transcript) - 1; i >= m.todoRunMark && i >= 0; i-- {
		if m.transcript[i].kind == entryAssistant {
			return m.transcript[i].text
		}
	}
	return ""
}

// todoVerifyCmd runs the item's listed tests, then the project's checks,
// in the background, and reports the tails. The test commands are the
// ones the item held when the run started, before any model turn could
// have edited the file — the model is told to tick boxes in it, and a
// command it wrote itself must not be one shhh runs unasked.
func (m Model) todoVerifyCmd() tea.Cmd {
	root := m.todos.Root
	slug := m.todoRunItem.Slug
	tests := m.todoRun.Tests
	gate := m.gate.Run
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), verifyTimeout)
		defer cancel()
		var b strings.Builder
		ok := true
		for _, cmd := range tests {
			out, code := runner.RunCaptureIn(ctx, root, cmd)
			fmt.Fprintf(&b, "$ %s → exit %d\n%s\n", cmd, code, tail(out, 40))
			if code != 0 {
				ok = false
			}
		}
		if gate != nil {
			res, err := gate(ctx, "")
			switch {
			case err != nil:
				fmt.Fprintf(&b, "quality gate: %v\n", err)
				ok = false
			case res.Verdict != quality.VerdictPass:
				b.WriteString(res.Format(quality.TakeFingerprint(root)) + "\n")
				ok = false
			default:
				fmt.Fprintf(&b, "quality gate %q: pass\n", res.Suite)
			}
		}
		if len(tests) == 0 && gate == nil {
			b.WriteString("nothing to verify: the item lists no tests and the project has no quality gate\n")
		}
		return todoVerifyMsg{slug: slug, ok: ok, output: strings.TrimRight(b.String(), "\n")}
	}
}

func tail(s string, lines int) string {
	parts := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(parts) > lines {
		parts = append([]string{fmt.Sprintf("… %d lines above", len(parts)-lines)}, parts[len(parts)-lines:]...)
	}
	return strings.Join(parts, "\n")
}

// finishTodoVerify applies the verify outcome.
func (m Model) finishTodoVerify(msg todoVerifyMsg) (tea.Model, tea.Cmd) {
	st := m.todoRun
	if st == nil || st.Over() || msg.slug != st.Slug || st.Stage != run.StageVerify {
		return m, nil
	}
	label := "passed"
	if !msg.ok {
		label = "failed"
	}
	model, _ := m.systemNotice(fmt.Sprintf("▸ todo run %s · verify %s\n%s", st.Slug, label, msg.output))
	return model.(Model).todoRunStep(st.VerifyResult(m.todoRunItem, msg.ok, msg.output))
}

// todoRunPaths is what the run may stage: every path the changeset saw
// change since the run's first turn, under the root, and not a backlog
// file — the backlog is never committed on the project's behalf.
// See docs/capabilities/todo.md#where-the-backlog-lives.
func (m Model) todoRunPaths() []string {
	root := m.todos.Root
	seen := map[string]bool{}
	var out []string
	for _, t := range m.changes.Turns() {
		if int(t.N) < m.todoRun.Turn {
			continue
		}
		for _, r := range t.Records {
			if !r.Changed() {
				continue
			}
			p := r.Path
			if !filepath.IsAbs(p) {
				p = filepath.Join(root, p)
			}
			rel, err := filepath.Rel(root, p)
			if err != nil || strings.HasPrefix(rel, "..") || strings.HasPrefix(rel, filepath.Join(todo.StateDir, todo.Subdir)) {
				continue
			}
			if !seen[rel] {
				seen[rel] = true
				out = append(out, rel)
			}
		}
	}
	return out
}

// todoCommitCmd stages the run's paths by name and commits with the
// message the commit turn wrote. It refuses a tree that already has staged
// changes it did not make: a commit that carries a stranger cannot be
// reverted, cited or read as a unit.
func (m Model) todoCommitCmd() tea.Cmd {
	root := m.todos.Root
	slug := m.todoRun.Slug
	message := m.todoRun.Message
	paths := m.todoRunPaths()
	return func() tea.Msg {
		if len(paths) == 0 {
			return todoCommitMsg{slug: slug, err: fmt.Errorf("the run changed no files under the repository")}
		}
		if out, code := git(root, "diff", "--cached", "--quiet"); code != 0 {
			return todoCommitMsg{slug: slug, err: fmt.Errorf("the index already holds staged changes this run did not make; commit or unstage them first\n%s", out)}
		}
		if out, code := git(root, append([]string{"add", "--"}, paths...)...); code != 0 {
			return todoCommitMsg{slug: slug, err: fmt.Errorf("git add: %s", out)}
		}
		f, err := os.CreateTemp("", "shhh-todo-commit-*.txt")
		if err != nil {
			return todoCommitMsg{slug: slug, err: err}
		}
		defer os.Remove(f.Name())
		if _, err := f.WriteString(message + "\n"); err != nil {
			f.Close()
			return todoCommitMsg{slug: slug, err: err}
		}
		f.Close()
		if out, code := git(root, "commit", "-F", f.Name()); code != 0 {
			return todoCommitMsg{slug: slug, err: fmt.Errorf("git commit: %s", out)}
		}
		return todoCommitMsg{slug: slug, files: paths}
	}
}

func git(root string, args ...string) (string, int) {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Env = runner.Environ()
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		code = 1
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
	}
	return strings.TrimSpace(string(out)), code
}

// finishTodoCommit applies the commit outcome.
func (m Model) finishTodoCommit(msg todoCommitMsg) (tea.Model, tea.Cmd) {
	st := m.todoRun
	if st == nil || st.Over() || msg.slug != st.Slug || st.Stage != run.StageCommit {
		return m, nil
	}
	if msg.err != nil {
		return m.todoRunStep(st.Block("the commit could not be made: " + msg.err.Error()))
	}
	return m.todoRunStep(st.Committed(msg.files))
}

// todoRunDone archives the item with its report and ends the run.
func (m Model) todoRunDone() (tea.Model, tea.Cmd) {
	st := m.todoRun
	report := st.Report
	if len(st.Files) > 0 {
		report += "\nCommitted: " + strings.Join(st.Files, ", ") + "\n"
	}
	to, err := todo.Archive(m.todos.Root, st.Slug, report)
	note := fmt.Sprintf("✓ todo run %s done — committed %s and archived the item to %s.", st.Slug, plural(len(st.Files), "file"), to)
	if err != nil {
		// The commit landed; the item must not stay in progress with its
		// report only on screen. It goes back to open with the report on
		// it, and the note says what to do.
		it := m.todoRunItem
		_ = todo.SetStatus(it.Path, todo.StatusOpen)
		_ = todo.Append(it.Path, report)
		note = fmt.Sprintf("✓ todo run %s committed %s, but the item could not be archived — %v. The report is on the item and it is open; /todo done %s archives it once that is settled.", st.Slug, plural(len(st.Files), "file"), err, st.Slug)
	}
	m.endTodoRun()
	return m.systemNotice(note + "\n\n" + st.Report)
}

// todoRunBlocked ends the run with its evidence on the item. The work
// already done stays in the tree, uncommitted, and the note says so.
func (m Model) todoRunBlocked() (tea.Model, tea.Cmd) {
	st := m.todoRun
	it := m.todoRunItem
	_ = todo.SetStatus(it.Path, todo.StatusBlocked)
	_ = todo.Append(it.Path, fmt.Sprintf("## Blocked\n%s\n\n_run in session %s, stage %s, %s_", st.Blocked, st.Session, st.Stage, time.Now().Format("2006-01-02 15:04")))
	paths := []string{}
	if m.changes != nil {
		paths = m.todoRunPaths()
	}
	m.endTodoRun()
	note := fmt.Sprintf("✗ todo run %s blocked — %s", it.Slug, st.Blocked)
	if len(paths) > 0 {
		note += "\nWork so far stays in the tree, uncommitted: " + strings.Join(paths, ", ")
	}
	note += fmt.Sprintf("\nThe evidence is on the item; /todo open %s reopens it when it is settled.", it.Slug)
	return m.systemNotice(note)
}

// endTodoRun restores the session's mode and retires the checkpoint.
func (m *Model) endTodoRun() {
	st := m.todoRun
	if st == nil {
		return
	}
	if prev, err := agent.ParseMode(st.PrevMode); err == nil {
		m.applyMode(prev)
	}
	run.Discard(m.todos.Root, st.Slug)
	m.todoRun = nil
	m.todoRunItem = todo.Item{}
	m.reloadTodos()
}

// stopTodoRun is /todo stop: the run is abandoned, the item goes back to
// open, and whatever was changed stays in the tree.
func (m Model) stopTodoRun() (tea.Model, tea.Cmd) {
	st := m.todoRun
	if st == nil || st.Over() {
		return m.systemNotice("No run is going.")
	}
	it := m.todoRunItem
	_ = todo.SetStatus(it.Path, todo.StatusOpen)
	m.endTodoRun()
	return m.systemNotice(fmt.Sprintf("Stopped the run on %s at %s; the item is open again and the tree is as the run left it.", it.Slug, st.Stage))
}

// todoRunStatus is /todo status.
func (m Model) todoRunStatus() string {
	if m.todoRun == nil {
		return "No run is going. /todo run [slug|--next] starts one."
	}
	return "▸ " + m.todoRun.Summary()
}
