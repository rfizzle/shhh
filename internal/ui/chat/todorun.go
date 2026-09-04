package chat

// The backlog runner in the session: /todo run works one item through the
// steps the profile states, with the gates decided by internal/todo/run
// rather than by the model. The session side is thin on purpose: it sends
// the prompt a step hands it, notices when the turn ends, reads the answer
// back, and does the two things a model must not — run the verification and
// make the commit.
// See docs/capabilities/todo.md#a-run-is-turns-with-gates-between-them.
//
// Nothing here names the steps. Which ones a run has is the profile's, so
// what this file asks about a run is the *kind* of step it stands at — does
// it await a turn, a command, a child — and never which of five words the
// stage happens to be. A driver that switched on the words would work only
// for the one profile whose words they are.
//
// A step that writes runs in auto mode, whatever the session was in, and the
// session's mode is put back when the run ends. The reader steers only where
// the classifier fails closed and asks, or where the run blocks and says why.
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
// has to arrive before the first turn and a reader stopped at the start of
// something they wanted is owed the way through it.
func todoNoRepoNotice(root, slug string) string {
	return fmt.Sprintf("%s is not in a git repository and a run ends in a commit — /todo run %s --no-commit runs it without one, or todo.commit = false makes that the default.", root, slug)
}

// todoNoRepoSprintNotice is the same for a whole set, where there is no one
// item to name.
func todoNoRepoSprintNotice(root string) string {
	return fmt.Sprintf("%s is not in a git repository and a run ends in a commit — /todo run --all --no-commit works the set without commits, or todo.commit = false makes that the default.", root)
}

// todoRunCan is what this session is able to do, which is what the run's
// steps are put to before the first of them is taken.
func (m Model) todoRunCan(repo bool) run.Can {
	return run.Can{
		Changeset:  m.changes != nil,
		Supervisor: m.subagents != nil,
		// A session always has somewhere to run a command; whether the
		// project names any is the command step's own business.
		Runner: true,
		Repo:   repo,
	}
}

// todoRunRefusal is the sentence a session is turned away with. What the step
// wanted decides it, because each need has its own way through and only this
// surface knows what to offer: a repository has a flag and a setting behind
// it, and the rest are facts about the session that nothing here can change.
// slug is empty for a refusal about a whole set.
func (m Model) todoRunRefusal(ref run.Refusal, slug string) string {
	if ref.Need == run.NeedRepo {
		root := project.Abbreviate(m.todos.Root)
		if slug == "" {
			return todoNoRepoSprintNotice(root)
		}
		return todoNoRepoNotice(root, slug)
	}
	return "This backlog's run cannot start here: " + ref.Why + "."
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
	return m.beginTodoRun(arg, noCommit, false)
}

// beginTodoRun is that with the one thing the command cannot say: whether the
// sprint took this item or a person named it. It is on the run rather than
// beside it because the surfaces that read it — the gate a stage closes on,
// the words a notification uses — are handed the run and nothing else.
func (m Model) beginTodoRun(arg string, noCommit, inSprint bool) (tea.Model, tea.Cmd) {
	// The flag is one run's answer and the setting is the standing one, so
	// either is enough. There is no flag the other way: a person who set
	// the project to make no commits and wants one on this item can make
	// it themselves, and the run that made one against the setting would
	// be the surprise worth avoiding.
	noCommit = noCommit || m.todos.NoCommit
	if m.todoRunner.state != nil && !m.todoRunner.state.Over() {
		return m.systemNotice(fmt.Sprintf("A run is already going: %s. /todo status shows it; /todo stop ends it.", m.todoRunner.state.Summary()))
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
	if m.turnState() != stateInput {
		return m.systemNotice("Answer the open decision first; a run starts from an idle session.")
	}
	repo := project.InRepo(m.todos.Root)
	opt := run.Options{NoCommit: noCommit, Repo: repo, Sprint: m.sprintGoal(),
		CloseGate: m.workspaceClosesGate(), InSprint: inSprint,
		// A reading the person accepted and has not edited past is what the
		// run's first step is told instead of taking the same reading again
		// several steps before it is needed.
		Groomed:  todo.GroomingBlock(m.todos.Root, it.Slug),
		Wordings: m.todos.Wordings,
		Pipeline: m.todos.Pipeline,
		// A write-up is read in the session's shared notebook, so a finish
		// that spends a turn on one asks whether there is a notebook first.
		Notebook: m.notebook != nil}
	// A profile may state no run at all, and the item is still an item: what
	// it needs is a person doing it, so the offer is the one verb that files
	// it rather than a run that would describe the work instead of doing it.
	if !opt.Steps().Runs() {
		return m.systemNotice(fmt.Sprintf("The %s profile has no run: its items are worked by hand. /todo done %s files this one.", m.todos.Profile.Name, it.Slug))
	}
	// What this session must be able to do is what the run's steps ask for,
	// step by step: a pipeline that never writes wants no changeset and one
	// that never commits wants no repository.
	if ref, refused := opt.Steps().Refuse(m.todoRunCan(repo)); refused {
		return m.systemNotice(m.todoRunRefusal(ref, it.Slug))
	}
	// An item left in progress with a checkpoint is a run that died with
	// its session. It continues from the stage it was at rather than
	// starting over: the plan and the rounds spent are in the checkpoint,
	// and the work of the stages before it is in the tree.
	if it.Status == todo.StatusInProgress {
		if st, err := run.Load(m.todos.Root, it.Slug); err == nil && !st.Over() {
			from := st.Session
			st.Session = m.sessionName
			st.PrevMode = m.policy.mode.String()
			st.Turn = int(m.turnCount) + 1
			st.Reviewer = ""
			// The invocation's answer stands over the checkpoint's, the
			// same way the session and the mode do: continuing a run is
			// asking for it again, and the repository may not be the one
			// the run started in.
			st.NoCommit, st.Repo, st.Sprint = noCommit, repo, opt.Sprint
			st.InSprint = inSprint
			st.Groomed = opt.Groomed
			st.Wordings, st.Pipeline = opt.Wordings, opt.Steps()
			m.todoRunner.state = st
			m.todoRunner.item = it
			m.openTodoRunRow()
			model, _ := m.systemNotice(fmt.Sprintf("Continuing the run on %s from its %s stage (checkpoint from session %s).", it.Slug, st.Stage, orDash(from)))
			return model.(Model).todoRunStep(st.Continue(it))
		}
		return m.systemNotice(fmt.Sprintf("%s is in progress with no checkpoint to continue from; /todo open %s puts it back to open and a run can start over.", it.Slug, it.Slug))
	}
	if err := todo.SetStatus(it.Path, todo.StatusInProgress); err != nil {
		return m.systemNotice("Could not mark the item in progress: " + err.Error())
	}
	m.todoRunner.state = run.Start(it, m.sessionName, m.policy.mode.String(), int(m.turnCount)+1, opt)
	m.todoRunner.item = it
	m.openTodoRunRow()
	m.reloadTodos()
	return m.todoRunStep(m.todoRunner.state.First(it, ""))
}

// todoRunStep carries out one step the machine handed back.
func (m Model) todoRunStep(step run.Step) (tea.Model, tea.Cmd) {
	st := m.todoRunner.state
	if capped, ok := m.sprintCap(step); ok {
		step = capped
	}
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
		m.todoRunner.mark = len(m.transcript)
		m.todoRunner.turn = int(m.turnCount) + 1
		// Every stage gets its own continuation, and this is the stage
		// starting.
		m.todoRunner.continued, m.todoRunner.carried = false, ""
		return m.sendUserMessageAs(step.Prompt, step.Shown)
	case run.ActionVerify:
		// The row already says the run is verifying; what a notice would add
		// is the output, and that arrives with the verdict.
		return m, m.todoVerifyCmd(step.Command)
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
	st := m.todoRunner.state
	if st == nil || st.Over() || !prev.working() || m.working() {
		return m, nil
	}
	if m.turnState() != stateInput || m.pausedAtRoundLimit() || m.heldAtBoundary() {
		return m, nil
	}
	// Which steps a run has is the profile's; which of them a turn's answer
	// belongs to is the kind's, so the session asks the kind rather than
	// naming stages it would have to be told about again.
	kind, known := st.StepKind()
	if !known || !st.AwaitsTurn() {
		return m, nil
	}
	if kind == run.KindFinish && st.Message != "" {
		// The finish turn was already read; the commit itself is in flight.
		return m, nil
	}
	if int(m.turnCount) != m.todoRunner.turn {
		// The turn that ended is not the stage's — a compaction, a skill
		// activation, something a command started. Its answer is not the
		// stage's answer and the stage cannot be judged, but nothing about
		// the item is wrong, so the run pauses rather than blocks: the item
		// stays in progress with its checkpoint, and /todo run picks it up
		// from this stage.
		next, cmd := m.stopTodoRunKeeping(fmt.Sprintf("the %s turn was displaced by another message", st.Stage))
		return next.(Model), cmd
	}
	if m.todoRunner.cancelled {
		// The cancel chord ended the stage turn with a partial answer. A
		// cancel is the reader stopping the run, not evidence to grade.
		m.todoRunner.cancelled = false
		next, cmd := m.stopTodoRun()
		return next.(Model), cmd
	}
	if res, ok := m.todoStageStopped(); ok {
		return m.todoRunUnfinished(res)
	}
	next, cmd := m.todoRunStep(st.Observe(m.todoRunner.item, m.todoStageAnswer()))
	return next.(Model), cmd
}

// todoStageStopped is the recovery row the stage's turn ended on, when it
// ended on one at all. Both rows it can be — a reply cut at the model's
// output ceiling and a reply the wire dropped mid-sentence — say the same
// thing about the answer: it is not the whole of one, and it reads like the
// whole of one, because a sentence that stops is all either of them leaves
// on screen (resume.go).
//
// The search stops at the last thing the model said. A turn continued past a
// ceiling has a whole reply under its row, and that reply is the answer; a
// row already acted on stops it for the same reason.
func (m Model) todoStageStopped() (*streamResume, bool) {
	for i := len(m.transcript) - 1; i >= m.todoRunner.mark && i >= 0; i-- {
		switch e := m.transcript[i]; e.kind {
		case entryStreamDrop:
			if e.resume == nil || e.resume.spent {
				return nil, false
			}
			return e.resume, true
		case entryAssistant:
			return nil, false
		}
	}
	return nil, false
}

// todoRunUnfinished is what the run does about a stage turn whose reply is
// not a whole one. Which of the two it is decides everything: a ceiling is
// arithmetic and the model can write past it, so the run has it finished; a
// dropped wire left half a sentence and whether that half is worth keeping
// is a judgement the row offers a reader and a run may not make for itself.
// See docs/capabilities/todo.md#a-run-is-turns-with-gates-between-them.
func (m Model) todoRunUnfinished(res *streamResume) (Model, tea.Cmd) {
	st := m.todoRunner.state
	if !res.truncated {
		// Nothing about the item is wrong — the transport failed — so the
		// run lets go at its checkpoint the way a displaced turn does,
		// leaving the item in progress for /todo run to pick up.
		next, cmd := m.stopTodoRunKeeping(fmt.Sprintf("the %s turn dropped mid-reply", st.Stage))
		return next.(Model), cmd
	}
	if m.todoRunner.continued {
		next, cmd := m.todoRunStep(st.Block(run.CutAtCeiling(st.Stage)))
		return next.(Model), cmd
	}
	// The half is kept because it is half of the stage's answer and not a
	// draft of it: the model was told to carry on from where it stopped
	// rather than to write the answer again, so what comes back is the rest
	// and the stage is judged on the two together.
	m.todoRunner.continued, m.todoRunner.carried = true, res.text
	next, cmd := m.continueStream(res)
	return next.(Model), cmd
}

// todoRunHoldsInput is why a plain message is refused while a run is
// going: text typed mid-stage would be steering the model out of its
// stage, and text typed between stages would start a turn whose edits the
// run would then commit as its own.
func (m Model) todoRunHoldsInput() (string, bool) {
	// A grooming reading is a turn of the same kind and is held for the
	// same reason: text typed into it steers the reading, and text typed
	// between two of them starts a turn the pass would then read as one.
	if m.todoGroomer.going() {
		return fmt.Sprintf("a backlog item is being read against the tree (%s) — the card opens when the turn is over; commands still work", m.todoGroomer.slug), true
	}
	if m.todoPlanner.going {
		return "a sprint is being planned — the proposal opens when the turn is over; commands still work", true
	}
	if m.todoRunner.state == nil || m.todoRunner.state.Over() {
		return "", false
	}
	return fmt.Sprintf("a backlog run is going (%s · %s) — /todo stop ends it, /todo status shows it; commands still work", m.todoRunner.state.Slug, m.todoRunner.state.Stage), true
}

// todoStageAnswer is the assistant's last message since the stage began,
// behind whatever a ceiling cut off the front of it. A continued reply
// arrives as two entries and is one answer: the model was asked to carry on
// from where it stopped, so the second entry starts mid-thought and means
// nothing on its own.
//
// The walk stops at the recovery row between the halves, because everything
// above it is already in hand and reading it twice would hand the stage its
// own first half again.
func (m Model) todoStageAnswer() string {
	for i := len(m.transcript) - 1; i >= m.todoRunner.mark && i >= 0; i-- {
		switch e := m.transcript[i]; e.kind {
		case entryStreamDrop:
			return m.todoRunner.carried
		case entryAssistant:
			return m.todoRunner.carried + e.text
		}
	}
	return m.todoRunner.carried
}

// todoVerifyCmd runs the item's listed tests, then the project's checks,
// in the background, and reports the tails. The test commands are the
// ones the item held when the run started, before any model turn could
// have edited the file — the model is told to tick boxes in it, and a
// command it wrote itself must not be one shhh runs unasked.
func (m Model) todoVerifyCmd(named string) tea.Cmd {
	root := m.todos.Root
	slug := m.todoRunner.item.Slug
	tests := m.todoRunner.state.Tests
	gate := m.gate.Run
	if named != "" {
		// A step that names its own command runs that and nothing else: the
		// project said what checking this work means, and the item's own
		// tests and the workspace's suite are the answer for the step that
		// did not.
		tests, gate = []string{named}, nil
	}
	// A run whose implement stage closed on a passing gate carries that
	// verdict here rather than paying for the suite twice over a tree that
	// did not move between the two (run.State.Checks).
	checked := m.todoRunner.state.Checked
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
	st := m.todoRunner.state
	if st == nil || st.Over() || msg.slug != st.Slug {
		return m, nil
	}
	if !st.AwaitsCommand() {
		return m, nil
	}
	label := "passed"
	if !msg.ok {
		label = "failed"
	}
	model, _ := m.systemNotice(fmt.Sprintf("▸ todo run %s · verify %s\n%s", st.Slug, label, msg.output))
	return model.(Model).todoRunStep(st.VerifyResult(m.todoRunner.item, msg.ok, msg.output))
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
	if m.todoRunner.state != nil {
		for _, rel := range m.todoRunner.state.Paths {
			if !seen[rel] {
				seen[rel] = true
				out = append(out, rel)
			}
		}
	}
	for _, t := range m.changes.Turns() {
		if int(t.N) < m.todoRunner.state.Turn {
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

// todoCommitCmd makes the run's commit, which is the run package's to make:
// the same staging, the same refusals and the same message file the
// unattended runner uses, so the one act of a run that cannot be taken back
// cannot mean two things depending on who asked for it.
func (m Model) todoCommitCmd() tea.Cmd {
	root := m.todos.Root
	slug := m.todoRunner.state.Slug
	message := m.todoRunner.state.Message
	paths := m.todoRunPaths()
	without := fmt.Sprintf("/todo run %s --no-commit runs it without one, or todo.commit = false makes that the default", slug)
	return func() tea.Msg {
		files, err := run.Commit(root, paths, message, without)
		return todoCommitMsg{slug: slug, files: files, err: err}
	}
}

// gitNotInstalled is the shell's own code for a command that never started,
// which is what this reports for a git that is not there rather than some
// real exit code a caller might read a meaning out of.
const gitNotInstalled = 127

// git runs one git command in root and reports its output and its exit code.
// It is the reading side only — the diff a reviewer child is handed; the
// commit a run makes is the run package's (run.Commit).
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
	st := m.todoRunner.state
	if st == nil || st.Over() || msg.slug != st.Slug {
		return m, nil
	}
	if kind, ok := st.StepKind(); !ok || kind != run.KindFinish {
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
	st := m.todoRunner.state
	to, err := run.File(m.todos.Root, st, m.todoRunner.item)
	note := todoRunDoneNote(st, to) + m.closeFinishedSprint()
	if err != nil {
		// The work is finished and the run package has already put the item
		// back to open with the report on it; what is left is telling the
		// reader what to do about it.
		did := "committed " + plural(len(st.Files), "file")
		if st.NoCommit {
			did = fmt.Sprintf("made no commit and left %s in the working tree", plural(len(st.Files), "file"))
		}
		note = fmt.Sprintf("✓ todo run %s %s, but the item could not be archived — %v. The report is on the item and it is open; /todo done %s archives it once that is settled.", st.Slug, did, err, st.Slug)
	}
	sprinting, slug := st.Sprinting(), st.Slug
	m.endTodoRun()
	// The report is the row's final state and opens from it; a copy of it
	// under the notice would be the same paragraphs twice, once where they
	// can be folded and once where they cannot.
	model, _ := m.systemNotice(note)
	if sprinting {
		return model.(Model).advanceSprint(slug)
	}
	return model, nil
}

// todoRunBlocked ends the run with its evidence on the item. The work
// already done stays in the tree, uncommitted, and the note says so.
func (m Model) todoRunBlocked() (tea.Model, tea.Cmd) {
	st := m.todoRunner.state
	it := m.todoRunner.item
	_ = todo.SetStatus(it.Path, todo.StatusBlocked)
	_ = todo.Append(it.Path, fmt.Sprintf("## Blocked\n%s\n\n_run in session %s, stage %s, %s_", st.Blocked, st.Session, st.Stage, time.Now().Format("2006-01-02 15:04")))
	paths := []string{}
	if m.changes != nil {
		paths = m.todoRunPaths()
	}
	blockedRow := m.todoRunner.rowIdx
	m.endTodoRun()
	// The proposal card that follows writes the follow-up item; the row that
	// blocked is where it belongs, so the reader finds the block and what
	// was written about it in one place.
	m.todoRunner.followUpRow = blockedRow
	note := fmt.Sprintf("✗ todo run %s blocked — %s", it.Slug, st.Blocked)
	if len(paths) > 0 {
		note += "\nWork so far stays in the tree, uncommitted: " + strings.Join(paths, ", ")
	}
	note += fmt.Sprintf("\nThe evidence is on the item; /todo open %s reopens it when it is settled.", it.Slug)
	model, _ := m.systemNotice(note)
	// A sprint stops here and does not go on to the next ready item: the
	// blocked item is about to be offered a follow-up, and what comes after
	// it in the backlog may be resting on the work that did not land. The
	// end is said before the follow-up card opens, because the card takes
	// the screen and a sentence behind it is a sentence nobody read.
	if st.Sprinting() {
		if sp, live := run.Live(m.todos.Root); live {
			sp.Blocks(it.Slug, st.Blocked)
			ended, _ := model.(Model).endTodoSprint(sp)
			model = ended
		}
	}
	// What is left is offered as a follow-up item, after this one; accepting
	// it is what lets the blocked item be archived once the rest lands.
	return model.(Model).openTodoProposals([]todo.Proposal{todoFollowUp(it, st)}, "a follow-up for "+it.Slug)
}

// openTodoPause shows the pause card: why the run stopped, the questions,
// the size, the plan, and three answers — go ahead, re-plan with a note,
// or stop. It borrows the bottom panel the way the memory prompt does.
func (m Model) openTodoPause(step run.Step) (tea.Model, tea.Cmd) {
	st := m.todoRunner.state
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
	m.todoRunner.pause = ns
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
	if m.todoRunner.pause == nil || m.todoRunner.state == nil {
		return nil
	}
	st := m.todoRunner.state
	width := m.contentWidth()
	var lines []string
	head := fmt.Sprintf("%s · %s %s", st.Slug, st.Profile.Grade, orDash(st.Grade))
	if st.GradeBefore != st.Grade {
		head += fmt.Sprintf(" (was %s)", orDash(st.GradeBefore))
	}
	if len(st.Steps) > 0 {
		head += fmt.Sprintf(" · %s", plural(len(st.Steps), "step"))
	}
	lines = append(lines, sty.User.Render(head))
	card := strings.Split(m.todoRunner.pause.View(width), "\n")
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
	done, res := m.todoRunner.pause.Update(msg)
	if !done {
		return m, nil
	}
	m.todoRunner.pause = nil
	m.leaveSurface()
	m.syncViewport()
	st, it := m.todoRunner.state, m.todoRunner.item
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
	st, it := m.todoRunner.state, m.todoRunner.item
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
		if int(t.N) < m.todoRunner.state.Turn {
			continue
		}
		for _, r := range t.Records {
			if rel := runRelPath(root, r.Path); rel != "" && r.Changed() {
				recorded[rel] = true
			}
		}
	}
	for _, rel := range m.todoRunner.state.Paths {
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
		if int(t.N) < m.todoRunner.state.Turn {
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
	st := m.todoRunner.state
	if st == nil || st.Over() || st.Reviewer == "" || status.Name != st.Reviewer {
		return m, nil, false
	}
	report, state, ok := m.subagents.FinalReport(st.Reviewer)
	if !ok || state != subagent.StateDone {
		next, cmd := m.todoRunStep(st.Block(fmt.Sprintf("the reviewer %s did not finish: %s", st.Reviewer, status.Detail)))
		return next, cmd, true
	}
	next, cmd := m.todoRunStep(st.ReviewResult(m.todoRunner.item, report))
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
	// The follow-up carries the blocked item's own header words, with the
	// grade the run was working at rather than the one the file still says:
	// research may have raised it, and what is left to do is the size of
	// the work as the run found it.
	fields := map[string]string{todo.PriorityField().Name: string(it.Priority)}
	for name, value := range it.Fields {
		fields[name] = value
	}
	if st.Profile.Grade != "" {
		fields[st.Profile.Grade] = st.Grade
	}
	return todo.Proposal{
		Title:     "Follow up " + it.Slug + ": " + it.Title,
		Fields:    fields,
		Story:     "Continue " + it.Slug + ", which blocked at " + string(st.Stage) + ".",
		Criteria:  criteria,
		Notes:     []string{"Blocked because: " + strings.ReplaceAll(st.Blocked, "\n", " ")},
		DependsOn: []string{it.Slug},
	}
}

// endTodoRun restores the session's mode and retires the checkpoint.
func (m *Model) endTodoRun() {
	st := m.todoRunner.state
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
	m.todoRunner.rowIdx = 0
	m.todoRunner.state = nil
	m.todoRunner.item = todo.Item{}
	if m.todoRunner.pause != nil {
		m.todoRunner.pause = nil
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
	st, it := m.todoRunner.state, m.todoRunner.item
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
	m.todoRunner.state = nil
	m.todoRunner.item = todo.Item{}
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
	st := m.todoRunner.state
	// A sprint ends at its checkpoint rather than by abandoning the item in
	// flight: the stages already done are in the tree, and the sprint is the
	// one caller that started the item without being asked about it, so
	// throwing its work away on a stop nobody aimed at that item would be
	// the surprise.
	if sp, live := run.Live(m.todos.Root); live {
		kept := ""
		if st != nil && !st.Over() {
			kept = m.keepTodoRun("the sprint was stopped")
		}
		sp.Stop()
		next, _ := m.endTodoSprint(sp)
		if kept == "" {
			return next, nil
		}
		return next.(Model).systemNotice(kept)
	}
	if st == nil || st.Over() {
		return m.systemNotice("No run is going.")
	}
	it := m.todoRunner.item
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
	// The sprint is not on the row: a row is one run, and how far through the
	// set that run is belongs to the loop above it. It is said first, so the
	// row the answer opens on is still the last thing on screen.
	if sp, live := run.Live(m.todos.Root); live {
		model, _ := m.systemNotice("▸ " + sp.Summary())
		m = model.(Model)
	}
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
	st, it := m.todoRunner.state, m.todoRunner.item
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
	st := m.todoRunner.state
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
	if st := m.todoRunner.state; st != nil && !st.Over() && p != nil {
		st.LanePatched(p.Agent)
		_ = st.Save(m.todos.Root)
	}
}

// todoWriterDone is a lane's writer finishing: its report goes on the lane
// and, when it is the last, the integration turn starts. A writer that
// did not finish blocks the run the way a failed reviewer does.
func (m Model) todoWriterDone(status subagent.Status) (tea.Model, tea.Cmd, bool) {
	st := m.todoRunner.state
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
	next, cmd := m.todoRunStep(st.LaneDone(m.todoRunner.item, status.Name, ok && state == subagent.StateDone, report))
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
	for i, stage := range st.Shape().Strip() {
		if i < st.Shape().Place(st.Stage) {
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
	place := r.st.Shape().Place(step.Stage)
	if place < 0 {
		// An ended run: the strip keeps what it has, and the end state below
		// settles the stage the run stopped in.
		r.settle(step)
		return
	}
	for i, stage := range r.st.Shape().Strip() {
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
	if r.st.Grade != "" {
		t += " · " + r.st.Profile.Grade + " " + r.st.Grade
		if r.st.GradeBefore != r.st.Grade {
			t += " (was " + orDash(r.st.GradeBefore) + ")"
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
	strip := r.st.Shape().Strip()
	segs := make([]string, 0, len(strip))
	for _, stage := range strip {
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
	n, of := r.st.Round, r.st.Rounds()
	if r.st.Remediating() {
		return fmt.Sprintf("round %d/%d", n, of)
	}
	return fmt.Sprintf("%d/%d rounds spent", n, of)
}

// restoredStages names the stages the row did not watch happen, in strip
// order. A glyph on the strip says a stage is not a tick; this says in words
// what it is instead (invariant 1).
func (r *todoRunRow) restoredStages() []string {
	var out []string
	for _, stage := range r.st.Shape().Strip() {
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
	m.appendEntry(entry{kind: entryTodoRun, todorun: newTodoRunRow(m.todoRunner.state)})
	m.todoRunner.rowIdx = len(m.transcript)
}

// todoRunRowEntry is the run's row, or nil where no run is drawn.
func (m Model) todoRunRowEntry() *todoRunRow {
	if m.todoRunner.rowIdx <= 0 || m.todoRunner.rowIdx > len(m.transcript) {
		return nil
	}
	return m.transcript[m.todoRunner.rowIdx-1].todorun
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
	m.todoRunner.rowIdx = 0
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
	if m.todoRunner.state != nil && !m.todoRunner.state.Over() && m.todoRunner.state.Slug == slug {
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

// A sprint is the same runner over more than one item: `/todo run --all`
// works the ready list — the sprint file's set where the backlog holds one —
// starting each item in a session of its own, and stops when the list is
// empty, when the cap is reached, or on the first block.
//
// What makes it a sprint rather than a loop is that nothing about an item's
// end is inferred from what the model said. An item is finished when the
// machine reached done, which is after a real commit and an archive with a
// report; an item is blocked when the machine reached blocked. The sprint
// reads those two transitions and nothing else.
// See docs/capabilities/todo.md#a-sprint-is-runs-with-a-session-between-them.

// startTodoSprint is `/todo run --all`. A sprint left behind by a process
// that died is continued rather than replaced: its checkpoint names the item
// it was on, and that item's own checkpoint names the stage.
func (m Model) startTodoSprint(opt todoRunArgs) (tea.Model, tea.Cmd) {
	if m.todoRunner.state != nil && !m.todoRunner.state.Over() {
		return m.systemNotice(fmt.Sprintf("A run is already going: %s. /todo status shows it; /todo stop ends it.", m.todoRunner.state.Summary()))
	}
	if m.todoStore == nil {
		return m.systemNotice("No backlog to run from.")
	}
	if m.turnState() != stateInput {
		return m.systemNotice("Answer the open decision first; a sprint starts from an idle session.")
	}
	noCommit := opt.noCommit || m.todos.NoCommit
	steps := run.Options{NoCommit: noCommit, Pipeline: m.todos.Pipeline, Notebook: m.notebook != nil}.Steps()
	if !steps.Runs() {
		return m.systemNotice(fmt.Sprintf("The %s profile has no run, so there is no set to work: its items are worked by hand.", m.todos.Profile.Name))
	}
	if ref, refused := steps.Refuse(m.todoRunCan(project.InRepo(m.todos.Root))); refused {
		return m.systemNotice(m.todoRunRefusal(ref, ""))
	}
	if sp, live := run.Live(m.todos.Root); live {
		// The invocation's answers stand over the checkpoint's, the same way
		// a continued run's do: asking for the sprint again is asking for it
		// under the answers given now, and the session it is picked up in is
		// this one.
		sp.Session, sp.PrevMode, sp.NoCommit = m.sessionName, m.policy.mode.String(), noCommit
		if opt.max > 0 {
			sp.Max = opt.max
		}
		model, _ := m.systemNotice("Continuing the sprint from its checkpoint — " + sp.Summary() + ".")
		next := model.(Model)
		if slug, ok := sp.Resume(); ok {
			if err := sp.Save(next.todos.Root); err != nil {
				return next.systemNotice("The sprint's checkpoint could not be written — " + err.Error())
			}
			return next.sprintRun(sp, slug)
		}
		return next.sprintNext(sp)
	}
	sp := run.StartSprint(m.sessionName, m.policy.mode.String(), opt.max, noCommit)
	m.signal(observe.SignalRun, "sprint")
	model, _ := m.systemNotice(todoSprintStartNote(sp, len(m.todoStore.Ready())))
	return model.(Model).sprintNext(sp)
}

// todoSprintStartNote says what the sprint is about to work and how it ends,
// because `--all` over a backlog is the one command here whose scope the
// person cannot see from the command they typed.
func todoSprintStartNote(sp *run.Sprint, ready int) string {
	scope := plural(ready, "item") + " ready"
	if sp.Max > 0 {
		scope += fmt.Sprintf(", at most %d of them", sp.Max)
	}
	note := "Sprint started — " + scope + ", one item per session. /todo stop ends it."
	if sp.NoCommit {
		note += " No run in it makes a commit."
	}
	return note
}

// sprintNext starts the sprint's next item, or ends the sprint when there is
// none.
func (m Model) sprintNext(sp *run.Sprint) (tea.Model, tea.Cmd) {
	it, ok := sp.Next(m.todoStore)
	if !ok {
		return m.endTodoSprint(sp)
	}
	if err := sp.Save(m.todos.Root); err != nil {
		sp.Stop()
		model, _ := m.systemNotice("The sprint's checkpoint could not be written — " + err.Error())
		return model.(Model).endTodoSprint(sp)
	}
	return m.sprintRun(sp, it.Slug)
}

// sprintRun starts one item under the sprint. A run the session refuses —
// an item another surface put in an impossible state between the two
// readings — ends the sprint rather than leaving a checkpoint nothing is
// driving and a command that refuses the same way every time it is retried.
func (m Model) sprintRun(sp *run.Sprint, slug string) (tea.Model, tea.Cmd) {
	next, cmd := m.beginTodoRun(slug, sp.NoCommit, true)
	started := next.(Model)
	if started.todoRunner.state == nil || started.todoRunner.state.Over() {
		sp.Blocks(slug, "the run could not be started; the notice above says why")
		ended, _ := started.endTodoSprint(sp)
		return ended, cmd
	}
	return started, cmd
}

// advanceSprint is the step between two items: the finished one is recorded,
// the session boundary is crossed, and the next item's run starts in the
// conversation on the other side.
//
// The boundary is the point of the loop. The previous item's conversation is
// cost and noise to the next one — the checkpoint already carries everything
// a stage needs — and a session per item is also what makes the record one
// row per item rather than one row for the night.
func (m Model) advanceSprint(done string) (tea.Model, tea.Cmd) {
	sp, live := run.Live(m.todos.Root)
	if !live {
		return m, nil
	}
	sp.Finished(done)
	// What the item cost is added to the set's total here, on the last line
	// before the boundary resets the ledger it is read from: the checkpoint
	// is the only thing that outlives the session it was spent in.
	sp.Spent(int(m.turnCount), m.sessionSpend().Cost)
	if err := sp.Save(m.todos.Root); err != nil {
		sp.Stop()
		model, _ := m.systemNotice("The sprint's checkpoint could not be written — " + err.Error())
		return model.(Model).endTodoSprint(sp)
	}
	// The same boundary /new crosses, through the same function: one
	// definition of what a session ending and another beginning resets
	// (model.go).
	note, save := m.startNewSession()
	// The new session's first row says which item comes next. Everything
	// else the boundary carried is gone by design, so a reader who comes
	// back to a fresh transcript would otherwise have to open the board to
	// find out what the sprint is about to work.
	note += sprintNextNote(sp, m.todoStore)
	model, _ := m.systemNotice(note)
	next, cmd := model.(Model).sprintNext(sp)
	return next, tea.Batch(save, cmd)
}

// sprintNextNote is the line the first row on the far side of a session
// boundary carries: which item the sprint takes next, or that there is none
// left. It reads the choice rather than making it, so the row and the run
// that follows it cannot name different items.
func sprintNextNote(sp *run.Sprint, store *todo.Store) string {
	if next, ok := sp.Peek(store); ok {
		return "\nNext in the sprint: " + next.Slug + " · " + next.Title
	}
	return "\nNothing is left that the sprint can start; it ends here."
}

// endTodoSprint retires the sprint: the mode the session was in before it
// goes back, the checkpoint is removed, and the row says which of the closed
// reasons stopped it.
//
// The checkpoint goes rather than being kept with its ending in it. A sprint
// that has stopped has nothing left to continue — the item it stopped on
// keeps its own checkpoint, and the note names the command that picks that
// one up — and a file left behind saying "ended" is a file the next `--all`
// has to decide about.
func (m Model) endTodoSprint(sp *run.Sprint) (tea.Model, tea.Cmd) {
	if sp.Ended == "" {
		sp.Stop()
	}
	run.DiscardSprint(m.todos.Root)
	if prev, err := agent.ParseMode(sp.PrevMode); err == nil {
		m.applyMode(prev)
	}
	m.signal(observe.SignalRun, "sprint-"+sp.Ended)
	return m.systemNotice(todoSprintEndNote(sp))
}

// todoSprintEndNote is what a finished sprint says: how much it got through,
// and which of the four endings it was. The word matters more than the
// sentence — a sprint that ran out of ready items and one that stopped on a
// block leave the same quiet screen, and only one of them is finished.
func todoSprintEndNote(sp *run.Sprint) string {
	note := fmt.Sprintf("Sprint over — %s · %s: %s", sp.Count(), sp.Ended, sp.Reason)
	if sp.Ended == run.SprintBlocked {
		note += "\nNothing further was attempted: a sprint stops on the first block, because what comes next may rest on the work that did not land."
	}
	return note
}

// sprintCap is the sprint's wall-clock cap on one item, read at the boundary
// between two stages rather than by a clock of its own. A stage is the
// smallest thing the runner can judge, so it is also the smallest thing the
// cap can end: cutting a turn in half would leave a tree nothing has read.
func (m Model) sprintCap(step run.Step) (run.Step, bool) {
	st := m.todoRunner.state
	if m.todos.ItemTimeout <= 0 || !st.Sprinting() || st.Over() {
		return step, false
	}
	switch step.Action {
	case run.ActionBlocked, run.ActionDone:
		return step, false
	}
	sp, live := run.Live(m.todos.Root)
	if !live || !sp.Expired(m.todos.ItemTimeout) {
		return step, false
	}
	return st.Block(run.TimedOut(m.todos.ItemTimeout)), true
}

// sprintCloseWords name the item a sprint's turn was spent on and how far the
// sprint has got, for the notification a finished turn raises. A reader who
// left a sprint running and came back to one line about a turn would have to
// go and look up which of thirty items it was.
func (m Model) sprintCloseWords() string {
	st := m.todoRunner.state
	if !st.Sprinting() {
		return ""
	}
	// An item that reached done is named as finished rather than as being
	// at a stage called done: the reader being called back is asking which
	// item ended, and "done" as a stage word reads as one more step.
	words := st.Slug + " · " + string(st.Stage)
	if st.Stage == run.StageDone {
		words = "finished " + st.Slug
	}
	if sp, live := run.Live(m.todos.Root); live {
		words += " · sprint " + sp.Count()
	}
	return words
}

// todoRunState is the backlog run in progress. It is one struct because the
// stage, the item and the row it is drawn on are read together at every step
// of the run, and because a run ends by being cleared whole — a stage left
// behind by a partial reset is a run that keeps answering turns nobody
// started.
type todoRunState struct {
	// state is the run itself, item the backlog item it works, and mark
	// where in the transcript the current stage began.
	state *run.State
	item  todo.Item
	mark  int
	// turn is the session turn the current stage sent, so a turn that ended
	// without being the stage's is told apart; cancelled marks a stage turn
	// ended by the cancel chord rather than by an answer.
	turn      int
	cancelled bool
	// continued marks a stage that has spent its one continuation past the
	// model's output ceiling, and carried is the half it was spent on — what
	// the stage's answer has to be read behind, because the model was told
	// to carry on from where it stopped rather than to write the answer
	// again. They are two fields and not one because the bound is a count
	// and not a length: a half that happened to be empty must still not buy
	// the stage a second attempt.
	continued bool
	carried   string
	// followUpRow is 1 + the transcript index of the run row a blocked run
	// left, while its follow-up proposal is still on the card.
	followUpRow int
	// rowIdx is 1 + the transcript index of the run's row, or 0 with no run
	// drawn. The row is addressed by index rather than held as a pointer
	// because transcript indices are what focus mode, the render cache and
	// reading mode all address entries by.
	rowIdx int
	// pause is the open pause card while a run waits on the person.
	pause *components.NoteSelect
}
