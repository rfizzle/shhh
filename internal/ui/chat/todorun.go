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
// This file is the driver: the stages a run passes through are one file each
// beside it (todorunverify.go, todoruncommit.go, todorunreview.go,
// todorunfanout.go, todorunpause.go, todorunend.go), the transcript row is
// todorunrow.go, and the loop over a whole set is todorunsprint.go.

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/todo"
	"github.com/rfizzle/shhh/internal/todo/run"
	"github.com/rfizzle/shhh/internal/ui/components"
)

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
		// A conversation keeps a changeset the way it keeps a transcript,
		// and it stays empty for the same reason there is no editor: nothing
		// in the session can change a file, so a step that would has no
		// record to make and nothing for a later step to read back.
		Changeset:  m.changes != nil && m.codingSurfaces(),
		Supervisor: m.subagents != nil,
		// A conversation registered no command tool at all, and no key
		// reaches one, so a step whose verdict is an exit status has no way
		// to get one here. In a session that can run commands, whether the
		// project names any is the command step's own business.
		Runner: m.codingSurfaces(),
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
		Groomed:  todo.GroomingBlock(m.todos.Root, it),
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
