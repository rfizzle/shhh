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
//
// The run is one row in the transcript, appended when it starts and redrawn
// on every transition (docs/interface/surfaces.md#the-backlog-runs-row). It
// used to be a scatter of one-line notices — a stage started, a verify
// passed, a lane landed — that a reader had to put back together by
// scrolling. It is a row because a run is a step of steps, and a step is
// what the transcript already has a shape for.
//
// The row stores a handle on the machine's own state rather than a copy of
// it, the way the fan-out block stores a batch number: everything it draws
// is read at render time, so it moves as the run moves and re-renders at any
// width. What it does keep is the strip's own memory — which stages this row
// watched go by — because a tick claims the row saw a stage finish, and a
// run picked up from a checkpoint saw nothing of the stages before it.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/diff"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/project"
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

// todoNoRepoNotice is what a run that would end in a commit is refused with
// outside a repository. It names the directory, what is missing and the two
// ways of asking for the run anyway, in one sentence, because the refusal
// has to arrive before the research turn and a reader stopped at the start
// of something they wanted is owed the way through it.
func todoNoRepoNotice(root, slug string) string {
	return fmt.Sprintf("%s is not in a git repository and a run ends in a commit — /todo run %s --no-commit runs it without one, or todo.commit = false makes that the default.", root, slug)
}

// startTodoRun begins a run on an item. It refuses a second run, an item
// that is not ready, a session without the changeset tracking the commit
// stage needs to know what it may stage, and a run that would commit in a
// directory with no repository.
//
// noCommit is `--no-commit` on the command or the setting behind it. It is
// answered here, before anything is written, rather than at the commit
// stage: every stage before that one spends turns, and a run that did all
// of them and then found it had nowhere to put the result has spent them
// for an item it leaves in progress.
func (m Model) startTodoRun(arg string, noCommit bool) (tea.Model, tea.Cmd) {
	// The flag is one run's answer and the setting is the standing one, so
	// either is enough. There is no flag the other way: a person who set
	// the project to make no commits and wants one on this item can make
	// it themselves, and the run that made one against the setting would
	// be the surprise worth avoiding.
	noCommit = noCommit || m.todos.NoCommit
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
	repo := project.InRepo(m.todos.Root)
	if !noCommit && !repo {
		return m.systemNotice(todoNoRepoNotice(project.Abbreviate(m.todos.Root), it.Slug))
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
			// The invocation's answer stands over the checkpoint's, the
			// same way the session and the mode do: continuing a run is
			// asking for it again, and the repository may not be the one
			// the run started in.
			st.NoCommit, st.Repo, st.Sprint = noCommit, repo, m.sprintGoal()
			m.todoRun = st
			m.todoRunItem = it
			m.openTodoRunRow()
			model, _ := m.systemNotice(fmt.Sprintf("Continuing the run on %s from its %s stage (checkpoint from session %s).", it.Slug, st.Stage, orDash(from)))
			return model.(Model).todoRunStep(st.Continue(it))
		}
		return m.systemNotice(fmt.Sprintf("%s is in progress with no checkpoint to continue from; /todo open %s puts it back to open and a run can start over.", it.Slug, it.Slug))
	}
	if err := todo.SetStatus(it.Path, todo.StatusInProgress); err != nil {
		return m.systemNotice("Could not mark the item in progress: " + err.Error())
	}
	m.todoRun = run.Start(it, m.sessionName, m.mode.String(), int(m.turnCount)+1,
		run.Options{NoCommit: noCommit, Repo: repo, Sprint: m.sprintGoal(),
			CloseGate: m.workspaceClosesGate()})
	m.todoRunItem = it
	m.openTodoRunRow()
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
	// One vocabulary for the record and for the row: the stage where the
	// step is a turn in one, and the action everywhere else (run.Step.Name).
	m.signal(observe.SignalRun, step.Name())
	m.observeTodoRunRow(step)
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
		// The row already says the run is verifying; what a notice would add
		// is the output, and that arrives with the verdict.
		return m, m.todoVerifyCmd()
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
		return m, m.todoCommitCmd()
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
// It waits out a round-limit pause, a hold and a decision card — those are
// the reader's — and reads the answer only when the turn is truly over.
func (m Model) todoRunAfter(prev Model) (Model, tea.Cmd) {
	st := m.todoRun
	if st == nil || st.Over() || !prev.working() || m.working() {
		return m, nil
	}
	if m.turnState() != stateInput || m.pausedAtRoundLimit() || m.heldAtBoundary() {
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
		// The cancel chord ended the stage turn with a partial answer. A
		// cancel is the reader stopping the run, not evidence to grade.
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
	// A run whose implement stage closed on a passing gate carries that
	// verdict here rather than paying for the suite twice over a tree that
	// did not move between the two (run.State.Checks).
	checked := m.todoRun.Checked
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
		if gate != nil && !checked {
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
		if checked {
			// The implement stage's own close already ran the suite over
			// this tree and it passed; nothing has changed since, so a
			// second run would spend a build to reach the same verdict.
			b.WriteString("quality gate: passed as the implement turn closed\n")
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
		// Four different failures came back as one sentence about the
		// person's index, and three of them were not about it. `--quiet`
		// exits 1 for a difference, and that is the only exit this check
		// may read as staged changes: telling someone outside a repository
		// that their index holds changes sends them looking for an index
		// that does not exist.
		//
		// The repository itself is read off the filesystem rather than out
		// of an exit code, because git's own code for it moves: it was 128,
		// the refusal, and is 129 on git 2.51, where `--cached` is a usage
		// error against the `--no-index` fallback the missing repository
		// leaves behind. The directory either holds a repository or it does
		// not, and that answer is the same on every version.
		out, code := git(root, "diff", "--cached", "--quiet")
		switch {
		case code == 0:
		case code == 1:
			return todoCommitMsg{slug: slug, err: fmt.Errorf("the index already holds staged changes this run did not make; commit or unstage them first\n%s", out)}
		case code == gitNotInstalled:
			return todoCommitMsg{slug: slug, err: fmt.Errorf("git is not on the path, so no commit can be made; install it, or run the item with /todo run --no-commit")}
		case !project.InRepo(root):
			return todoCommitMsg{slug: slug, err: fmt.Errorf("%s is not a git repository, so there is nothing to commit into; /todo run --no-commit, or todo.commit = false, runs an item without one", root)}
		default:
			return todoCommitMsg{slug: slug, err: fmt.Errorf("git diff --cached exited %d: %s", code, out)}
		}
		if out, code := git(root, append([]string{"add", "--"}, paths...)...); code != 0 {
			return todoCommitMsg{slug: slug, err: fmt.Errorf("git add: %s", out)}
		}
		f, err := os.CreateTemp("", "shhh-todo-commit-*.txt")
		if err != nil {
			return todoCommitMsg{slug: slug, err: err}
		}
		defer func() { _ = os.Remove(f.Name()) }()
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

// gitNotInstalled is the shell's exit code for a command that could not be
// run, which is what this package reports for a git that is not there.
const gitNotInstalled = 127

// git runs one git command in root and reports its output and its exit code.
// A command that never started is 127, the shell's own answer for it: the
// alternative is reporting some real exit code for a git that was never
// there, and every caller that reads a code by name would then read the
// wrong sentence out of it.
func git(root string, args ...string) (string, int) {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Env = runner.Environ()
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else {
			code = gitNotInstalled
			out = append(out, err.Error()...)
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

// todoRunDoneNote is the row a finished run closes with: what happened to
// the work, and where the item went.
//
// A run that made no commit says so rather than saying nothing about it.
// "done" beside an uncommitted tree reads as a commit that was made, and
// the reader's next act is to go looking for one — so the row names the
// files instead, which is the only place the change now is.
func todoRunDoneNote(st *run.State, to string) string {
	files := plural(len(st.Files), "file")
	if st.NoCommit {
		return fmt.Sprintf("✓ todo run %s done — not committed; %s in the working tree, and the item is archived to %s.", st.Slug, files, to)
	}
	return fmt.Sprintf("✓ todo run %s done — committed %s and archived the item to %s.", st.Slug, files, to)
}

// todoRunDone archives the item with its report and ends the run.
func (m Model) todoRunDone() (tea.Model, tea.Cmd) {
	st := m.todoRun
	report := st.Report
	if len(st.Files) > 0 && !st.NoCommit {
		report += "\nCommitted: " + strings.Join(st.Files, ", ") + "\n"
	}
	to, err := todo.Archive(m.todos.Root, st.Slug, report)
	note := todoRunDoneNote(st, to) + m.closeFinishedSprint()
	if err != nil {
		// The work is finished; the item must not stay in progress with its
		// report only on screen. It goes back to open with the report on
		// it, and the note says what to do.
		it := m.todoRunItem
		_ = todo.SetStatus(it.Path, todo.StatusOpen)
		_ = todo.Append(it.Path, report)
		did := "committed " + plural(len(st.Files), "file")
		if st.NoCommit {
			did = fmt.Sprintf("made no commit and left %s in the working tree", plural(len(st.Files), "file"))
		}
		note = fmt.Sprintf("✓ todo run %s %s, but the item could not be archived — %v. The report is on the item and it is open; /todo done %s archives it once that is settled.", st.Slug, did, err, st.Slug)
	}
	m.endTodoRun()
	// The report is the row's final state and opens from it; a copy of it
	// under the notice would be the same paragraphs twice, once where they
	// can be folded and once where they cannot.
	return m.systemNotice(note)
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
	blockedRow := m.todoRunRowIdx
	m.endTodoRun()
	// The proposal card that follows writes the follow-up item; the row that
	// blocked is where it belongs, so the reader finds the block and what
	// was written about it in one place.
	m.todoFollowUpRow = blockedRow
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
	// The plan is on the card as a checklist and on the run's row as the
	// research stage's answer. It used to be pasted into the transcript here
	// as well, which put the same paragraphs in a third place — the one
	// place they could be neither folded nor answered.
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

// todoPauseLines renders the pause card: the plan as a checklist, the
// questions, the lanes a large item was divided into, and the re-graded size
// against the item's — all above the selector, so the choice is made with
// the facts in view rather than with a scroll back through the transcript.
//
// The plan is bounded rather than complete. The card's height comes out of
// the bottom panel's budget, and a twenty-step plan that pushed the answers
// off the screen would be the facts hiding the choice; what does not fit is
// counted, which is what every other fold in the product does
// (docs/interface/principles.md#fold-never-hide).
func (m Model) todoPauseLines() []string {
	if m.todoPause == nil || m.todoRun == nil {
		return nil
	}
	st := m.todoRun
	width := m.contentWidth()
	var lines []string
	head := fmt.Sprintf("%s · size %s", st.Slug, orDash(string(st.Size)))
	if st.SizeBefore != st.Size {
		head += fmt.Sprintf(" (was %s)", orDash(string(st.SizeBefore)))
	}
	if len(st.Steps) > 0 {
		head += fmt.Sprintf(" · %s", plural(len(st.Steps), "step"))
	}
	lines = append(lines, sty.User.Render(head))
	card := strings.Split(m.todoPause.View(width), "\n")
	// What the plan may take: the panel less the head, the questions, the
	// lanes and the selector itself.
	room := m.maxConfirmPanelHeight() - len(lines) - len(card) - len(st.Questions) - len(st.Lanes)
	lines = append(lines, m.todoPlanChecklist(st.Steps, room, width)...)
	for _, q := range st.Questions {
		lines = append(lines, clipRow(sty.Step.Stats.Render("? "+q), width))
	}
	for _, lane := range st.Lanes {
		lines = append(lines, clipRow(sty.Step.Stats.Render("lane "+lane.Name+"  "+strings.Join(lane.Paths, ", ")), width))
	}
	return append(lines, card...)
}

// todoPlanChecklist is the plan as the checklist the rail draws a running
// plan as: one row per step, numbered as the plan numbered them, with the
// to-do glyph, because nothing here has been built yet.
func (m Model) todoPlanChecklist(steps []string, room, width int) []string {
	if len(steps) == 0 || room < 1 {
		return nil
	}
	shown := steps
	if len(shown) > room {
		// One of the rows goes to the count of what did not fit.
		shown = shown[:max(room-1, 0)]
	}
	lines := make([]string, 0, room)
	for i, step := range shown {
		lines = append(lines, clipRow(sty.Step.Dim.Render("○ ")+
			sty.Step.Stats.Render(fmt.Sprintf("%d. %s", i+1, step)), width))
	}
	if n := len(steps) - len(shown); n > 0 {
		lines = append(lines, clipRow(sty.Step.Dim.Render(fmt.Sprintf("… %d more", n)), width))
	}
	return lines
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
		m.signal(observe.SignalRun, "replan")
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
	// The row keeps the state it ended on and is not the next run's; the
	// state itself is no longer written to, so the row is frozen by the run
	// being over rather than by a copy being taken.
	m.todoRunRowIdx = 0
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
	note := m.keepTodoRun(why)
	return m.systemNotice(note)
}

// keepTodoRun is that without the row, answering with the sentence instead.
// The session boundary needs the two apart: the run is let go of while the
// old conversation is still standing, and the offer to continue it belongs to
// the new one's transcript rather than to the transcript being dropped
// (model.go).
func (m *Model) keepTodoRun(why string) string {
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
	m.signal(observe.SignalRun, "kept")
	m.closeTodoRunRow("kept")
	m.todoRun = nil
	m.todoRunItem = todo.Item{}
	m.reloadTodos()
	return todoRunKeptNote(it, st, why)
}

// todoRunKeptNote is what a run let go of at its checkpoint says: where it
// stopped, why, and the command that picks it up from there.
func todoRunKeptNote(it todo.Item, st *run.State, why string) string {
	return fmt.Sprintf("Paused the run on %s at %s — %s. /todo run %s continues it from there.", it.Slug, st.Stage, why, it.Slug)
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
	m.signal(observe.SignalRun, "stopped")
	m.closeTodoRunRow("stopped")
	m.endTodoRun()
	return m.systemNotice(fmt.Sprintf("Stopped the run on %s at %s; the item is open again and the tree is as the run left it.", it.Slug, st.Stage))
}

// todoRunStatus is /todo status: the run's row, opened, with the keyboard on
// it. The row is where the run already is — the stages, the plan, the rounds
// spent, what the review said — so the answer to "where is it" is to open
// that rather than to print a second account of the same facts beside it.
// The keyboard goes with it because the offers a finished run makes are the
// row's, and a key is inert until its surface holds the keyboard
// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
func (m Model) todoRunStatus() (tea.Model, tea.Cmd) {
	idx := lastTodoRunRow(m.transcript)
	if idx < 0 {
		return m.systemNotice("No run is going. /todo run [slug|--next] starts one.")
	}
	if m.attachedTo != "" {
		// The keyboard is in a child's transcript, and the row is in this
		// one. The sentence is what is left, and it is the whole summary.
		return m.systemNotice("▸ " + m.transcript[idx].todorun.st.Summary())
	}
	m.transcript[idx].expanded = true
	m.enterSurface(stateFocus)
	m.focusIdx = idx
	m.invalidateRenderCache()
	m.refreshFocusView()
	return m, nil
}

// lastTodoRunRow is the most recent run drawn in a transcript, or -1. The
// most recent rather than the running one: /todo status after a run ended is
// asking about the one that just finished, and its row is still there.
func lastTodoRunRow(es []entry) int {
	for i := len(es) - 1; i >= 0; i-- {
		if es[i].kind == entryTodoRun && es[i].todorun != nil {
			return i
		}
	}
	return -1
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
		m.signal(observe.SignalRun, "lane-refused")
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

// runMark is one strip stage's state on the row.
type runMark int

const (
	runPending  runMark = iota // · not reached
	runRestored                // ↺ done in an earlier session; this row did not watch it
	runLive                    // ▸ where the run is now
	runPassed                  // ✓ watched finish
	runStopped                 // ✗ where the run blocked
)

// todoRunRow is the transcript's handle on one run.
type todoRunRow struct {
	// st is the machine's state, shared with the session while the run goes
	// and left behind as the row's final state once it ends.
	st *run.State
	// marks is the strip's record per stage; see the file comment.
	marks map[run.Stage]runMark
	// closed is how the session ended a run the machine did not finish:
	// `kept` for one let go of at its checkpoint, `stopped` for one
	// abandoned. Either is a different thing from done and from blocked,
	// and the row has to say which rather than freeze on whatever stage it
	// happened to be in.
	closed string
	// followUp is the item a blocked run's follow-up proposal was written
	// as, empty until the reader accepts it.
	followUp string
}

// newTodoRunRow starts a row on a state. A state that is already past the
// first stage is a run continued from a checkpoint: everything below where it
// resumed happened in a session this row never saw, so it starts as restored
// rather than as passed.
func newTodoRunRow(st *run.State) *todoRunRow {
	r := &todoRunRow{st: st, marks: map[run.Stage]runMark{}}
	for i, stage := range run.Strip() {
		if i < run.Place(st.Stage) {
			r.marks[stage] = runRestored
		}
	}
	return r
}

// observe records a transition. Every strip stage below the step's own is
// passed if this row watched it and restored if it did not; the step's own
// stage is where the run is now.
func (r *todoRunRow) observe(step run.Step) {
	if r == nil {
		return
	}
	place := run.Place(step.Stage)
	if place < 0 {
		// An ended run: the strip keeps what it has, and the end state below
		// settles the stage the run stopped in.
		r.settle(step)
		return
	}
	for i, stage := range run.Strip() {
		switch {
		case i > place:
			// A run that went back — a remediation round returns to
			// implement — has the stages above it ahead of it again, and a
			// tick on one of them would say it is behind.
			r.marks[stage] = runPending
		case i == place:
			r.marks[stage] = runLive
		case r.marks[stage] == runPending, r.marks[stage] == runRestored:
			// A stage below the run that this row never watched happen: it
			// was done in the session the checkpoint came from, and a tick
			// would claim otherwise.
			r.marks[stage] = runRestored
		default:
			r.marks[stage] = runPassed
		}
	}
	r.settle(step)
}

// settle marks where a run that has ended stopped: passed for the stage it
// finished in, broken for the one it blocked in.
func (r *todoRunRow) settle(step run.Step) {
	switch step.Action {
	case run.ActionBlocked:
		for stage, mark := range r.marks {
			if mark == runLive {
				r.marks[stage] = runStopped
			}
		}
	case run.ActionDone:
		for stage, mark := range r.marks {
			if mark == runLive {
				r.marks[stage] = runPassed
			}
		}
	}
}

// live reports whether the run is still moving, which is what keeps the
// block it sits in out of the render cache.
func (r *todoRunRow) live() bool {
	return r != nil && r.st != nil && !r.st.Over() && r.closed == ""
}

// liveTodoRunBlock is the earliest block holding a run that is still going.
// A run's row changes on transitions, and a transition lands no row of its
// own, so nothing from there on may be frozen.
func (m Model) liveTodoRunBlock(blocks []transcriptBlock) int {
	for i, blk := range blocks {
		for j := blk.start; j < blk.end && j < len(m.transcript); j++ {
			if e := m.transcript[j]; e.kind == entryTodoRun && e.todorun.live() {
				return i
			}
		}
	}
	return len(blocks)
}

// runRowGlyph pairs every mark with a glyph of its own, so a monochrome
// terminal keeps them apart (docs/interface/principles.md#colour-never-carries-meaning-alone).
func runRowGlyph(mark runMark) string {
	switch mark {
	case runPassed:
		return sty.Step.Done.Render("✓")
	case runLive:
		return sty.Step.Run.Render("▸")
	case runStopped:
		return sty.Step.Fail.Render("✗")
	case runRestored:
		return sty.Step.Dim.Render("↺")
	}
	return sty.Step.Dim.Render("·")
}

// outcome is what the row's stats field states: where the run is, in the
// words the record keys a transition on (run.Step.Name), with the one thing
// no stage word can carry said beside it — a run finished without a commit,
// where "done" alone would send the reader looking for one.
func (r *todoRunRow) outcome() string {
	switch {
	case r.closed != "":
		return r.closed
	case r.st.Stage == run.StageDone && r.st.NoCommit:
		return "done · not committed"
	case r.st.Paused != "":
		return "paused"
	}
	return string(r.st.Stage)
}

// glyph is the row's own state mark: running, finished, broken, or stopped
// where it stands.
func (r *todoRunRow) glyph() string {
	switch {
	case r.st.Stage == run.StageBlocked:
		return sty.Step.Fail.Render("✗")
	case r.st.Stage == run.StageDone:
		return sty.Step.Done.Render("✓")
	case r.closed != "", r.st.Paused != "":
		return sty.Step.Dim.Render("·")
	}
	return sty.Step.Run.Render("▸")
}

// elapsed is the run's span as of its last transition, in the whole-turn
// form: a run spends a model turn on most of its stages, and past a minute a
// count of seconds stops reading as a duration. It is measured between the
// two ends the checkpoint already stamps rather than against the clock,
// because a run advances in stages and a figure ticking between them would
// be the one thing on the row that is not a fact about the run.
func (r *todoRunRow) elapsed() time.Duration {
	if r.st.Updated.Before(r.st.Started) {
		return 0
	}
	return r.st.Updated.Sub(r.st.Started)
}

// title is the row's growing field.
func (r *todoRunRow) title() string {
	t := "todo run " + r.st.Slug
	if r.st.Size != "" {
		t += " · size " + string(r.st.Size)
		if r.st.SizeBefore != r.st.Size {
			t += " (was " + orDash(string(r.st.SizeBefore)) + ")"
		}
	}
	return t
}

// headerLine is the row's own line, on the step header's grid so a run and
// the steps around it share one edge: the fold state, the state glyph in the
// ordinal column, the title, a faint rule, where the run is, and its span.
func (r *todoRunRow) headerLine(width int, folded bool) string {
	fold := "▾"
	if folded {
		fold = "▸"
	}
	lead := sty.Step.Dim.Render(fold) + " " + r.glyph() + " " + " "
	leadW := components.GridPointerWidth + stepOrdinalWidth + 1

	label := r.outcome()
	stats := sty.Step.Stats.Render(label)
	statsW := lipgloss.Width(label)

	titleStyle := sty.Step.Title
	if r.live() {
		titleStyle = sty.Step.LiveTitle
	}
	fixed := leadW + statsW + components.GridDurationWidth + 3
	title := clipRow(r.title(), width-fixed)
	rule := width - leadW - lipgloss.Width(title) - statsW - components.GridDurationWidth - 2
	if rule < 1 {
		rule = 1
	}
	line := lead + titleStyle.Render(title) + " " +
		sty.Step.Rule.Render(strings.Repeat("─", rule)) + " " + stats +
		stepDurationField(turnDuration(r.elapsed()), sty.Step.Stats)
	return strings.TrimRight(line, " ")
}

// stripSegments draws the stages the run passes through, in order, each with
// its own glyph and its own word. The word is the stage's, which is the word
// the record keys the transition on, so the row and the record cannot
// disagree about what happened (run.Step.Name).
func (r *todoRunRow) stripSegments() []string {
	segs := make([]string, 0, len(run.Strip()))
	for _, stage := range run.Strip() {
		mark := r.marks[stage]
		style := sty.Step.Dim
		switch mark {
		case runLive:
			style = sty.Step.LiveTitle
		case runPassed, runStopped:
			style = sty.Step.Title
		}
		segs = append(segs, runRowGlyph(mark)+" "+style.Render(string(stage)))
	}
	return segs
}

// stripLines packs the stages into as many lines as the width needs. The
// strip is a closed set of five words and the row exists to show all of
// them, so it wraps where every other growing field clips: a terminal too
// narrow for one line would otherwise be told the run has four stages
// (docs/interface/principles.md#fold-never-hide).
func (r *todoRunRow) stripLines(width int) []string {
	sep := sty.Step.Dim.Render(" · ")
	var lines []string
	line, room := "", 0
	for _, seg := range r.stripSegments() {
		w := lipgloss.Width(seg)
		switch {
		case line == "":
			line, room = seg, w
		case room+3+w <= width:
			line, room = line+sep+seg, room+3+w
		default:
			lines = append(lines, line)
			line, room = seg, w
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

// notes are the lines under the strip: what a stage the strip cannot say in
// one word is doing. Each names its stage, because the strip is a row of
// words and a number floating under it belongs to whichever one the reader
// guesses.
func (r *todoRunRow) notes() []runRowLine {
	var out []runRowLine
	if restored := r.restoredStages(); len(restored) > 0 {
		out = append(out, runRowLine{text: "restored from the checkpoint: " + strings.Join(restored, " · ")})
	}
	if n := len(r.st.Lanes); n > 0 {
		out = append(out, runRowLine{painted: sty.Step.Stats.Render("implement  ") + r.laneMeter().View() +
			sty.Step.Stats.Render("  "+strings.Join(laneNames(r.st), " · "))})
	}
	if r.st.Round > 0 {
		out = append(out, runRowLine{text: "remediate  " + r.rounds()})
	}
	if r.st.Verdict != "" {
		out = append(out, runRowLine{text: "review  " + r.st.Verdict})
	}
	if r.st.Paused != "" {
		out = append(out, runRowLine{text: "paused  " + r.st.Paused})
	}
	if r.st.Blocked != "" {
		out = append(out, runRowLine{text: "blocked  " + firstLine(r.st.Blocked)})
		if r.followUp != "" {
			out = append(out, runRowLine{text: "follow-up  " + r.followUp})
		}
	}
	return out
}

// runRowLine is one line under the row. Its text is plain, so the row can
// wrap it at whatever width it is drawn at; painted is the alternative for a
// line whose colouring is not the row's to decide — the lane meter, whose bar
// and number are one field and turn colour together — and it clips, because
// it is a row of fields rather than prose.
type runRowLine struct {
	text    string
	painted string
	// head marks the word that names what the lines under it are, which is
	// the only thing under a run's row drawn in body weight: everything else
	// there is the detail it heads.
	head bool
	// under is a line belonging to the heading above it. The step it takes
	// is the row's, applied after the wrap rather than written into the
	// text, so a wrapped path lands under the one before it and not back at
	// the heading's own column.
	under bool
}

// rounds is the remediation note: which round is being spent while one is,
// and how many were spent once the run has moved on. A round count is a fact
// about a run that outlives the round — the note stays on the row after the
// stage is ticked — and `round 1/2` read beside a finished run would say a
// round is in flight when none is.
func (r *todoRunRow) rounds() string {
	n, of := r.st.Round, run.Rounds(r.st.Size)
	if r.st.Stage == run.StageRemediate {
		return fmt.Sprintf("round %d/%d", n, of)
	}
	return fmt.Sprintf("%d/%d rounds spent", n, of)
}

// restoredStages names the stages the row did not watch happen, in strip
// order. A glyph on the strip says a stage is not a tick; this says in words
// what it is instead (invariant 1).
func (r *todoRunRow) restoredStages() []string {
	var out []string
	for _, stage := range run.Strip() {
		if r.marks[stage] == runRestored {
			out = append(out, string(stage))
		}
	}
	return out
}

// laneMeter is a large item's lanes as one meter: how many of the writers'
// patches have landed against how many were spawned.
func (r *todoRunRow) laneMeter() components.Meter {
	done, total := laneProgress(r.st)
	// The lane meter, which is what a fan-out's own lanes are drawn with: a
	// run's lanes are that fan-out, and one count drawn two ways would be two
	// answers to how far along it is.
	m, _ := components.AgentMeter(done, total)
	m.Text = fmt.Sprintf("%d/%d landed", done, total)
	return m
}

// laneProgress counts the lanes whose patch has landed.
func laneProgress(st *run.State) (done, total int) {
	for _, l := range st.Lanes {
		if l.Done {
			done++
		}
	}
	return done, len(st.Lanes)
}

// laneNames is the lanes in the order they were divided.
func laneNames(st *run.State) []string {
	out := make([]string, 0, len(st.Lanes))
	for _, l := range st.Lanes {
		out = append(out, l.Name)
	}
	return out
}

// runAnswerLines bounds one stage answer under an opened row.
const runAnswerLines = 12

// answers are what the row opens to: the stage answers the run has, in the
// order the run produced them. This is the fold's other half — folded, the
// row says a run happened and where it got to; opened, it says what each
// stage answered (docs/interface/principles.md#fold-never-hide).
func (r *todoRunRow) answers() []runRowLine {
	var out []runRowLine
	head := func(word string) { out = append(out, runRowLine{text: word, head: true}) }
	body := func(lines ...string) {
		for i, l := range lines {
			if i == runAnswerLines && len(lines) > runAnswerLines+1 {
				// The bound is a fold and not a loss: a failing verify's
				// output runs to forty lines and reaches the transcript on
				// its own row, so what this one owes the reader is the head
				// of it and an honest count of the rest
				// (docs/interface/principles.md#fold-never-hide).
				out = append(out, runRowLine{
					text:  fmt.Sprintf("… %d more lines", len(lines)-runAnswerLines),
					under: true,
				})
				return
			}
			out = append(out, runRowLine{text: l, under: true})
		}
	}
	if len(r.st.Steps) > 0 {
		head("plan")
		for i, step := range r.st.Steps {
			body(fmt.Sprintf("%d. %s", i+1, step))
		}
	}
	if len(r.st.Questions) > 0 {
		head("questions")
		for _, q := range r.st.Questions {
			body("? " + q)
		}
	}
	if len(r.st.Lanes) > 0 {
		head("lanes")
		for _, lane := range r.st.Lanes {
			body(lane.Name + "  " + strings.Join(lane.Paths, ", "))
		}
	}
	if r.st.Findings != "" {
		head("findings")
		body(strings.Split(strings.TrimRight(r.st.Findings, "\n"), "\n")...)
	}
	if r.st.Blocked != "" {
		head("blocked")
		body(strings.Split(strings.TrimRight(r.st.Blocked, "\n"), "\n")...)
	}
	if r.st.Report != "" {
		head("report")
		body(strings.Split(strings.TrimRight(r.st.Report, "\n"), "\n")...)
	}
	if len(r.st.Files) > 0 {
		head("files")
		body(strings.Join(r.st.Files, ", "))
	}
	return out
}

// offers are the keys the row carries: a blocked run's item can be reopened
// from the row it blocked on, which is where the reader is when they decide
// to.
func (r *todoRunRow) offers() []components.TurnKey {
	if r.st.Stage != run.StageBlocked {
		return nil
	}
	return []components.TurnKey{{Key: keys.Bracket(keys.Row.Reopen), Label: keys.Words(keys.Row.Reopen)}}
}

// todoRunRowView renders the row: its header, the strip, the notes each
// stage adds, and — opened — the answers the stages gave.
func (m Model) todoRunRowView(e entry, width int, keysLive bool) string {
	r := e.todorun
	indent := strings.Repeat(" ", components.GridDetailIndent)
	inner := max(width-components.GridDetailIndent, 1)
	lines := []string{r.headerLine(width, !e.expanded)}
	clipped := func(s string) { lines = append(lines, indent+clipRow(s, inner)) }
	// Wrapped rather than clipped, for the reason the notices with bodies
	// wrap: these are sentences and paths, and half of either is worse than
	// a line that costs two.
	wrapped := func(l runRowLine) {
		if l.painted != "" {
			clipped(l.painted)
			return
		}
		style, step := sty.Step.Stats, ""
		if l.head {
			style = sty.Step.Title
		}
		if l.under {
			step = "  "
		}
		for _, text := range strings.Split(m.wordWrap(l.text, max(inner-len(step), 1)), "\n") {
			lines = append(lines, indent+step+style.Render(text))
		}
	}
	for _, line := range r.stripLines(inner) {
		clipped(line)
	}
	for _, note := range r.notes() {
		wrapped(note)
	}
	if e.expanded {
		for _, answer := range r.answers() {
			wrapped(answer)
		}
	}
	if offers := components.KeyRun(r.offers(), !keysLive, m.rowHandover(keysLive)); offers != "" {
		clipped(offers)
	}
	return strings.Join(lines, "\n")
}

// openTodoRunRow appends the row a run is drawn on and remembers where it
// landed. One row per run: it is appended when the run starts — including a
// run continued from a checkpoint, which is a second sitting of the same run
// and gets a second row, because the first one is in a transcript this
// session no longer has.
func (m *Model) openTodoRunRow() {
	m.appendEntry(entry{kind: entryTodoRun, todorun: newTodoRunRow(m.todoRun)})
	m.todoRunRowIdx = len(m.transcript)
}

// todoRunRowEntry is the run's row, or nil where no run is drawn.
func (m Model) todoRunRowEntry() *todoRunRow {
	if m.todoRunRowIdx <= 0 || m.todoRunRowIdx > len(m.transcript) {
		return nil
	}
	return m.transcript[m.todoRunRowIdx-1].todorun
}

// observeTodoRunRow tells the row a transition happened. The row is redrawn
// from the machine's state, so the cache that has already seen this entry has
// to be told it changed.
func (m *Model) observeTodoRunRow(step run.Step) {
	r := m.todoRunRowEntry()
	if r == nil {
		return
	}
	r.observe(step)
	m.invalidateRenderCache()
}

// closeTodoRunRow says how the session ended a run the machine did not
// finish, and lets the row go: the next run opens its own.
func (m *Model) closeTodoRunRow(how string) {
	if r := m.todoRunRowEntry(); r != nil {
		r.closed = how
		m.invalidateRenderCache()
	}
	m.todoRunRowIdx = 0
}

// todoRunRowIndexOf finds the transcript index of a run row, or -1.
func todoRunRowIndexOf(es []entry, idx int) int {
	if idx < 0 || idx >= len(es) || es[idx].kind != entryTodoRun || es[idx].todorun == nil {
		return -1
	}
	return idx
}

// todoRunReopen is `[o]` on a blocked run's row: the item it blocked goes
// back to open. It is refused on a row whose item another run is working —
// reopening it under a run would put the run's own item back to a state the
// run does not expect.
func (m Model) todoRunReopen(idx int) (tea.Model, tea.Cmd, bool) {
	if todoRunRowIndexOf(m.transcript, idx) < 0 {
		return m, nil, false
	}
	r := m.transcript[idx].todorun
	if r.st.Stage != run.StageBlocked {
		return m, nil, false
	}
	slug := r.st.Slug
	if m.todoRun != nil && !m.todoRun.Over() && m.todoRun.Slug == slug {
		next, cmd := m.systemNotice(fmt.Sprintf("%s is being run again; /todo stop ends that run first.", slug))
		return next, cmd, true
	}
	it, ok := m.todoStore.Find(slug)
	if !ok {
		next, cmd := m.systemNotice(fmt.Sprintf("No backlog item %q; it may have been archived or renamed since the run blocked.", slug))
		return next, cmd, true
	}
	if err := todo.SetStatus(it.Path, todo.StatusOpen); err != nil {
		next, cmd := m.systemNotice("Could not reopen " + slug + " — " + err.Error())
		return next, cmd, true
	}
	m.reloadTodos()
	next, cmd := m.systemNotice(fmt.Sprintf("%s is open again; /todo run %s starts it over.", slug, slug))
	return next, cmd, true
}
