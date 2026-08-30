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
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/diff"
	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/runner"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/todo"
	"github.com/rfizzle/shhh/internal/todo/run"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
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
	if m.turnState() != stateInput {
		return m.systemNotice("Answer the open decision first; a run starts from an idle session.")
	}
	// An item left in progress with a checkpoint is a run that died with
	// its session. It continues from the stage it was at rather than
	// starting over: the plan and the rounds spent are in the checkpoint,
	// and the work of the stages before it is in the tree.
	if it.Status == todo.StatusInProgress {
		if st, err := run.Load(m.todos.Root, it.Slug); err == nil && !st.Over() {
			from := st.Session
			st.Session = m.sessionName
			st.PrevMode = m.mode.String()
			st.Turn = int(m.turnCount) + 1
			st.Reviewer = ""
			m.todoRun = st
			m.todoRunItem = it
			model, _ := m.systemNotice(fmt.Sprintf("Continuing the run on %s from its %s stage (checkpoint from session %s).", it.Slug, st.Stage, orDash(from)))
			return model.(Model).todoRunStep(st.Continue(it))
		}
		return m.systemNotice(fmt.Sprintf("%s is in progress with no checkpoint to continue from; /todo open %s puts it back to open and a run can start over.", it.Slug, it.Slug))
	}
	if err := todo.SetStatus(it.Path, todo.StatusInProgress); err != nil {
		return m.systemNotice("Could not mark the item in progress: " + err.Error())
	}
	m.todoRun = run.Start(it, m.sessionName, m.mode.String(), int(m.turnCount)+1)
	m.todoRunItem = it
	m.reloadTodos()
	return m.todoRunStep(m.todoRun.First(it, ""))
}

// todoRunStep carries out one step the machine handed back.
func (m Model) todoRunStep(step run.Step) (tea.Model, tea.Cmd) {
	st := m.todoRun
	st.Paths = m.todoRunPaths()
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
	case run.ActionPause:
		return m.openTodoPause(step)
	case run.ActionReview:
		return m.startTodoReview()
	case run.ActionFanOut:
		return m.startTodoFanOut()
	case run.ActionWait:
		if step.Shown != "" {
			return m.systemNotice(step.Shown)
		}
		return m, nil
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
	case run.StageResearch, run.StageSplit, run.StageImplement, run.StageRemediate, run.StageReview, run.StageCommit:
	default:
		return m, nil
	}
	if st.Stage == run.StageCommit && st.Message != "" {
		// The commit turn was already read; the commit itself is in flight.
		return m, nil
	}
	if int(m.turnCount) != m.todoRunTurn {
		// The turn that ended is not the stage's — a compaction, a skill
		// activation, something a command started. Its answer is not the
		// stage's answer and the stage cannot be judged, but nothing about
		// the item is wrong, so the run pauses rather than blocks: the item
		// stays in progress with its checkpoint, and /todo run picks it up
		// from this stage.
		next, cmd := m.stopTodoRunKeeping(fmt.Sprintf("the %s turn was displaced by another message", st.Stage))
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
	// What earlier sessions of this run changed comes from the checkpoint;
	// this session's own records are added to it.
	if m.todoRun != nil {
		for _, rel := range m.todoRun.Paths {
			if !seen[rel] {
				seen[rel] = true
				out = append(out, rel)
			}
		}
	}
	for _, t := range m.changes.Turns() {
		if int(t.N) < m.todoRun.Turn {
			continue
		}
		for _, r := range t.Records {
			if !r.Changed() {
				continue
			}
			rel := runRelPath(root, r.Path)
			if rel == "" {
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
	model, _ := m.systemNotice(note)
	// What is left is offered as a follow-up item, after this one; accepting
	// it is what lets the blocked item be archived once the rest lands.
	return model.(Model).openTodoProposals([]todo.Proposal{todoFollowUp(it, st)}, "a follow-up for "+it.Slug)
}

// openTodoPause shows the pause card: why the run stopped, the questions,
// the size, the plan, and three answers — go ahead, re-plan with a note,
// or stop. It borrows the bottom panel the way the memory prompt does.
func (m Model) openTodoPause(step run.Step) (tea.Model, tea.Cmd) {
	st := m.todoRun
	m.appendEntry(entry{kind: entrySystem, text: step.Shown + "\n\n" + strings.TrimSpace(st.Plan)})
	opts := []components.SelectOption{
		{Label: "Go ahead", Desc: "build the plan as it stands"},
		{Label: "Re-plan with my note", Desc: "answer the questions or steer; research runs again", RequireNote: true},
		{Label: "Stop the run", Desc: "the item goes back to open; nothing is built"},
	}
	ns := components.NewNoteSelect("Run paused — "+st.Paused, opts)
	ns.Select.MaxLines = m.maxConfirmPanelHeight() - 1
	m.todoPause = ns
	m.enterSurface(stateTodoPause)
	m.syncViewport()
	m.viewport.SetLines(m.renderHistoryLines())
	m.viewport.GotoBottom()
	return m, nil
}

// todoPauseLines renders the pause card: the questions and the size
// above the selector, so the choice is made with the facts in view.
func (m Model) todoPauseLines() []string {
	if m.todoPause == nil || m.todoRun == nil {
		return nil
	}
	st := m.todoRun
	var lines []string
	head := fmt.Sprintf("%s · size %s", st.Slug, orDash(string(st.Size)))
	if st.SizeBefore != st.Size {
		head += fmt.Sprintf(" (was %s)", orDash(string(st.SizeBefore)))
	}
	if len(st.Steps) > 0 {
		head += fmt.Sprintf(" · %s", plural(len(st.Steps), "step"))
	}
	lines = append(lines, sty.User.Render(head))
	for _, q := range st.Questions {
		lines = append(lines, strings.Split(m.wordWrap("? "+q, m.contentWidth()), "\n")...)
	}
	return append(lines, strings.Split(m.todoPause.View(m.contentWidth()), "\n")...)
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// updateTodoPause routes keys while the pause card shows.
func (m Model) updateTodoPause(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if keys.Match(msg, keys.Draft.Quit) {
		m.quitting = true
		return m, m.quitCmd()
	}
	done, result := m.todoPause.Update(msg)
	if !done {
		return m, nil
	}
	res := result.(components.NoteSelectResult)
	m.todoPause = nil
	m.leaveSurface()
	m.syncViewport()
	st, it := m.todoRun, m.todoRunItem
	if st == nil {
		return m, nil
	}
	switch {
	case res.Canceled, res.Index == 2:
		return m.stopTodoRun()
	case res.Index == 1:
		note := strings.TrimSpace(res.Note)
		_ = todo.Append(it.Path, "## Answers\n"+note)
		m.reloadTodos()
		return m.todoRunStep(st.Replan(it, note))
	}
	if note := strings.TrimSpace(res.Note); note != "" {
		_ = todo.Append(it.Path, "## Answers\n"+note)
		st.Answers = append(st.Answers, note)
		m.reloadTodos()
	}
	return m.todoRunStep(st.Resume(it))
}

// startTodoReview hands the change to a reviewer child. The child cannot
// run commands, so the diff is read here and put in its task, bounded.
// With no supervisor, or a spawn the supervisor refuses, the orchestrator
// reviews in its own turn and the step label says so.
func (m Model) startTodoReview() (tea.Model, tea.Cmd) {
	st, it := m.todoRun, m.todoRunItem
	if len(m.todoRunPaths()) == 0 {
		return m.todoRunStep(st.Block("the run changed no files under the repository, so there is nothing to review"))
	}
	if m.subagents == nil {
		return m.todoRunStep(st.SelfReview(it))
	}
	args, _ := json.Marshal(map[string]any{
		"role": string(subagent.RoleReviewer),
		"name": st.Reviewer,
		"task": st.ReviewTask(it, tail(m.todoRunDiff(), 600)),
	})
	if _, err := m.subagents.Spawn(args); err != nil {
		model, _ := m.systemNotice("No reviewer agent could be spawned — " + err.Error())
		return model.(Model).todoRunStep(st.SelfReview(it))
	}
	_ = st.Save(m.todos.Root)
	return m.systemNotice(fmt.Sprintf("▸ todo run %s · review by %s", st.Slug, st.Reviewer))
}

// todoRunDiff is the run's change as the changeset recorded it — before
// and after for every path, which is what shows a file the run created,
// where git's own diff of the tree would show nothing for it.
func (m Model) todoRunDiff() string {
	root := m.todos.Root
	var b strings.Builder
	seen := map[string]bool{}
	// Paths from an earlier session have no record here; git's diff of
	// the tree stands in, with an untracked file shown whole.
	recorded := map[string]bool{}
	for _, t := range m.changes.Turns() {
		if int(t.N) < m.todoRun.Turn {
			continue
		}
		for _, r := range t.Records {
			if rel := runRelPath(root, r.Path); rel != "" && r.Changed() {
				recorded[rel] = true
			}
		}
	}
	for _, rel := range m.todoRun.Paths {
		if recorded[rel] || seen[rel] {
			continue
		}
		seen[rel] = true
		// Only a diff counts; git's complaint about a path or a tree is
		// not one, and must not reach the reviewer as if it were.
		if out, code := git(root, "diff", "--", rel); code == 0 && strings.HasPrefix(out, "diff --git") {
			b.WriteString(out + "\n")
			continue
		}
		if out, _ := git(root, "diff", "--no-index", os.DevNull, rel); strings.HasPrefix(out, "diff --git") {
			b.WriteString(out + "\n")
		}
	}
	for _, t := range m.changes.Turns() {
		if int(t.N) < m.todoRun.Turn {
			continue
		}
		for _, r := range t.Records {
			rel := runRelPath(root, r.Path)
			if rel == "" || seen[rel] || !r.Changed() {
				continue
			}
			seen[rel] = true
			b.WriteString(recordDiff(rel, r))
		}
	}
	return b.String()
}

// recordDiff renders one record as a unified diff.
func recordDiff(rel string, r changeset.Record) string {
	var b strings.Builder
	old, now := "a/"+rel, "b/"+rel
	if !r.BeforeExists {
		old = "/dev/null"
	}
	if !r.AfterExists {
		now = "/dev/null"
	}
	fmt.Fprintf(&b, "--- %s\n+++ %s\n", old, now)
	for _, h := range diff.Compute(r.Before, r.After) {
		b.WriteString(h.Header() + "\n")
		for _, l := range h.Lines {
			prefix := " "
			switch l.Kind {
			case diff.Add:
				prefix = "+"
			case diff.Del:
				prefix = "-"
			}
			b.WriteString(prefix + l.Text + "\n")
		}
	}
	return b.String()
}

// runRelPath is a record's path relative to the root, or "" when it is
// outside the root or a backlog file.
func runRelPath(root, p string) string {
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	rel, err := filepath.Rel(root, p)
	if err != nil || strings.HasPrefix(rel, "..") || strings.HasPrefix(rel, filepath.Join(todo.StateDir, todo.Subdir)) {
		return ""
	}
	return rel
}

// todoReviewDone is the reviewer child finishing: its own final message is
// the review's answer. A child that did not finish — killed, failed, out
// of rounds — has no answer, and the run blocks on that rather than on
// whatever the placeholder for a failed child happens to say.
func (m Model) todoReviewDone(status subagent.Status) (tea.Model, tea.Cmd, bool) {
	st := m.todoRun
	if st == nil || st.Over() || st.Reviewer == "" || status.Name != st.Reviewer {
		return m, nil, false
	}
	report, state, ok := m.subagents.FinalReport(st.Reviewer)
	if !ok || state != subagent.StateDone {
		next, cmd := m.todoRunStep(st.Block(fmt.Sprintf("the reviewer %s did not finish: %s", st.Reviewer, status.Detail)))
		return next, cmd, true
	}
	next, cmd := m.todoRunStep(st.ReviewResult(m.todoRunItem, report))
	return next, cmd, true
}

// todoFollowUp is the item proposed when a run blocks: what is left of the
// blocked item, after it. It is a proposal, not a file — the person
// accepts it on the same card the session's own proposals use.
func todoFollowUp(it todo.Item, st *run.State) todo.Proposal {
	var criteria []string
	in := false
	for _, line := range strings.Split(it.Body, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "## ") {
			in = strings.EqualFold(strings.TrimSpace(t[3:]), "acceptance criteria")
			continue
		}
		if in && strings.HasPrefix(t, "- [ ] ") {
			criteria = append(criteria, strings.TrimSpace(t[6:]))
		}
	}
	if len(criteria) == 0 {
		criteria = []string{"What " + it.Slug + " left undone is done"}
	}
	return todo.Proposal{
		Title:     "Follow up " + it.Slug + ": " + it.Title,
		Kind:      string(it.Kind),
		Priority:  string(it.Priority),
		Size:      string(st.Size),
		Story:     "Continue " + it.Slug + ", which blocked at " + string(st.Stage) + ".",
		Criteria:  criteria,
		Notes:     []string{"Blocked because: " + strings.ReplaceAll(st.Blocked, "\n", " ")},
		DependsOn: []string{it.Slug},
	}
}

// renderTodoPause renders the pause card padded to the bottom panel.
func (m Model) renderTodoPause() string {
	lines := m.todoPauseLines()
	h := m.bottomPanelHeight()
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines[:h], "\n")
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
	// A reviewer still reading, or a writer still building, is spending on
	// a run that is over.
	m.killTodoAgents(st)
	run.Discard(m.todos.Root, st.Slug)
	m.todoRun = nil
	m.todoRunItem = todo.Item{}
	if m.todoPause != nil {
		m.todoPause = nil
		m.leaveSurface()
	}
	m.reloadTodos()
}

// stopTodoRunKeeping ends the run but keeps the checkpoint and the item in
// progress, so /todo run continues it from the stage it was at.
func (m Model) stopTodoRunKeeping(why string) (tea.Model, tea.Cmd) {
	st, it := m.todoRun, m.todoRunItem
	if prev, err := agent.ParseMode(st.PrevMode); err == nil {
		m.applyMode(prev)
	}
	m.killTodoAgents(st)
	st.Reviewer = ""
	for i := range st.Lanes {
		st.Lanes[i].Agent = ""
	}
	st.Paths = m.todoRunPaths()
	_ = st.Save(m.todos.Root)
	m.todoRun = nil
	m.todoRunItem = todo.Item{}
	m.reloadTodos()
	return m.systemNotice(fmt.Sprintf("Paused the run on %s at %s — %s. /todo run %s continues it from there.", it.Slug, st.Stage, why, it.Slug))
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

// killTodoAgents ends every child the run has in flight.
func (m *Model) killTodoAgents(st *run.State) {
	if m.subagents == nil {
		return
	}
	for _, name := range st.LiveAgents() {
		_ = m.subagents.Kill(name)
	}
}

// startTodoFanOut spawns a writer per lane. The lanes share one batch so
// the rail draws them as one fan-out, and each declares its paths so the
// supervisor refuses the overlap the split stage already checked for. A
// session without a supervisor, or a spawn it refuses, does not block the
// item: the session builds it whole and the step label says why.
// See docs/capabilities/todo.md#a-large-item-is-built-in-lanes.
func (m Model) startTodoFanOut() (tea.Model, tea.Cmd) {
	st, it := m.todoRun, m.todoRunItem
	if m.subagents == nil {
		return m.todoRunStep(st.NoLanes(it, "no agent supervisor; building in this session"))
	}
	// The split turn left the session read-only, and a child's mode is
	// clamped to its parent's: writers spawned now would be writers that
	// cannot write. The fan-out is the working stage.
	m.applyMode(agent.ModeAuto)
	m.subagents.BeginBatch()
	var spawned []string
	for _, lane := range st.Lanes {
		if lane.Done || lane.Agent == "" {
			continue
		}
		args, _ := json.Marshal(map[string]any{
			"role":  string(subagent.RoleWriter),
			"name":  lane.Agent,
			"task":  st.LaneTask(it, lane),
			"paths": lane.Paths,
		})
		if _, err := m.subagents.Spawn(args); err != nil {
			for _, name := range spawned {
				_ = m.subagents.Kill(name)
			}
			for i := range st.Lanes {
				st.Lanes[i].Agent = ""
			}
			model, _ := m.systemNotice(fmt.Sprintf("Lane %s could not be spawned — %s", lane.Name, err.Error()))
			return model.(Model).todoRunStep(st.NoLanes(it, "writers refused; building in this session"))
		}
		spawned = append(spawned, lane.Agent)
	}
	_ = st.Save(m.todos.Root)
	return m.systemNotice(fmt.Sprintf("▸ todo run %s · fan-out: %s", st.Slug, strings.Join(spawned, ", ")))
}

// todoLaneAsk is a routed approval from one of the run's writers. The
// patch is the run's own to take — the lanes were checked disjoint before
// they were spawned, and the tree is verified and reviewed after — so it
// is applied without a card; one the supervisor flags as overlapping an
// earlier patch is refused, and the run blocks on it. Anything else a
// writer asks — a command the classifier could not decide — goes to the
// person the way every child's ask does: that is the steering.
func (m Model) todoLaneAsk(ask *subagent.Ask) (tea.Model, tea.Cmd, bool) {
	st := m.todoRun
	if st == nil || st.Over() || ask == nil || ask.Kind != subagent.AskPatch {
		return m, nil, false
	}
	lane, ok := st.LaneByAgent(ask.Agent)
	if !ok {
		return m, nil, false
	}
	if len(ask.Warnings) > 0 {
		ask.Respond(false)
		next, cmd := m.todoRunStep(st.LaneFailed(ask.Agent, "its patch was refused: "+strings.Join(ask.Warnings, "; ")))
		return next, cmd, true
	}
	ask.Respond(true)
	model, _ := m.systemNotice(fmt.Sprintf("▸ todo run %s · lane %s: %s", st.Slug, lane.Name, ask.Title))
	return model, nil, true
}

// todoLanePatched is a writer's patch landing on the tree.
func (m Model) todoLanePatched(p *subagent.PatchApplied) {
	if st := m.todoRun; st != nil && !st.Over() && p != nil {
		st.LanePatched(p.Agent)
		_ = st.Save(m.todos.Root)
	}
}

// todoWriterDone is a lane's writer finishing: its report goes on the lane
// and, when it is the last, the integration turn starts. A writer that
// did not finish blocks the run the way a failed reviewer does.
func (m Model) todoWriterDone(status subagent.Status) (tea.Model, tea.Cmd, bool) {
	st := m.todoRun
	if st == nil || st.Over() {
		return m, nil, false
	}
	if _, ok := st.LaneByAgent(status.Name); !ok {
		return m, nil, false
	}
	report, state, ok := m.subagents.FinalReport(status.Name)
	if !ok || state != subagent.StateDone {
		report = status.Detail
	}
	next, cmd := m.todoRunStep(st.LaneDone(m.todoRunItem, status.Name, ok && state == subagent.StateDone, report))
	return next, cmd, true
}
