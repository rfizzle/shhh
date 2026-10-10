package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/meter"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/sandbox"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/todo/run"
	"github.com/rfizzle/shhh/internal/web"
)

// todoDriver carries out the steps the machine hands back.
type todoDriver struct {
	// root is the checkout the backlog, its checkpoints and its record
	// belong to, and tree is where the item's work is done: the same
	// directory for a run in the checkout, and a lane's own copy of it for
	// an item a parallel sprint is working beside others. Everything that
	// reads or changes the work goes to tree; everything that reads or
	// changes the backlog goes to root.
	root string
	tree string
	// lane is the parallel sprint's hold on this driver where it is working
	// one of its lanes, and nil otherwise.
	lane *todoLane
	// bin is this executable, which every stage's turn is one process of.
	bin     string
	out     io.Writer
	session string
	// gate runs the project's checks at the verify stage. Nil where the
	// checkout is untrusted, which the verify stage blocks on where the item
	// has no checks of its own rather than passing silently.
	gate *quality.Runner
	// slots are the check slots a parallel sprint's processes share, in the
	// sprint's run directory, and nil outside a parallel sprint. Everything
	// in a lane that loads the machine takes one through takeCheckSlot, and
	// every stage's process is told where they are (stageEnv).
	slots     *subagent.FileSlots
	slotCount int
	// closeGate reports that the workspace names an on-close suite, so a
	// stage's own process checks the tree as it closes and the verify stage
	// can take that verdict instead of running the same suite again.
	closeGate bool
	// checks reports the project having said what checking its work means —
	// a quality config it carries. It is read once here rather than at the
	// verify stage because a run with no way to reach a verdict is refused
	// before it spends a turn, and it is not a question about trust: an
	// untrusted checkout's config is still the project's word, and the gate
	// that will not run over it is caught at verify.
	checks      bool
	itemTimeout time.Duration
	// costCap is the setting's ceiling on a sprint and costCapFlag the one
	// the command named, both in cents; Sprint.Bound decides between them
	// and a continued sprint's own.
	costCap, costCapFlag int64
	noCommit             bool
	// secretIgnore is commit.secret_ignore, the fixtures a run's commit may
	// carry a credential shape in.
	secretIgnore []string
	// trailers is commit.trailers, the lines a run's commit ends with.
	trailers []string
	// itemTurns and itemCost are what the item being worked has spent so
	// far, one turn per stage process and its cost off that process's own
	// record row. They are the item's half of the sprint's running total,
	// added to the checkpoint when the item is over — the point a session
	// crosses its boundary on the other surface — written beside it as the
	// item's running figure at every stage boundary (Sprint.Running), and
	// read in between by the notes of a set that closes with this item.
	itemTurns int
	itemCost  float64
	// ledger names this process as the writer of the item's running figure
	// on the sprint's checkpoint (Sprint.Running). It is never the sprint's
	// Session, so a figure found when the sprint is picked up is always
	// counted — rightly, since a runner picks a sprint up only before it has
	// written a figure of its own, and one that looks like it was written by
	// this process was written by a dead one: the session name is only as
	// fine as a second, and a script restarting a runner that died can start
	// the next one inside the same second.
	ledger string
	repo   bool
	// wrote is what this run's own stages reported writing, gathered from
	// each stage process's transcript. It is the run's changeset, in the one
	// form a runner whose stages are separate processes has: the tree says
	// what is changed and not who changed it, so a file the checkout already
	// held modified is one only this list can claim for the run.
	wrote []string
	// spool stores one stage's output where the next one can read it, and is
	// nil for a run whose store would not open. The store lives under the
	// repository (run.EvidenceDir) rather than under shhh's state dir
	// because the stage that reads an id back is another process standing in
	// this checkout, and a directory is what can be handed to one.
	spool    quality.EvidenceFunc
	spoolDir string
	// wordings are the step instructions this checkout runs, read once here
	// for the same reason a session reads them at startup: a run whose
	// wording could not be read must not begin on the built-in one.
	wordings run.Wordings
	// pipeline is the steps a run of this backlog takes.
	pipeline run.Pipeline
	// db and rec are the run's half of the record: one row for the sprint
	// and every stage's own row hanging under it, so what a sprint cost is a
	// question the record can answer.
	//
	// The runner spends nothing itself — every token is spent by a stage's
	// process — so this row's totals are zero and true. What it carries is
	// the parenthood: without it a sprint is forty-eight one-shot runs the
	// record cannot tell from forty-eight unrelated ones, and the retention
	// prune, which takes a family together, has no family to take.
	//
	// It is left unstamped for the same reason. A driver resolves no
	// reasoning level, no round cap and no containment profile — the stages
	// do — and a stamp of those at their zero values would read as a
	// session that ran manual, uncapped and unconfined.
	// See docs/capabilities/sessions-and-memory.md#a-sprint-is-one-tree.
	db  *storage.DB
	rec *observeRecorder
	// item is the slug the stages now being started belong to, so a stage's
	// process can be told which item it is a stage of.
	item string
	// chats is every conversation this item's stages saved, and resume the
	// command that opens the last of them. They are gathered as the item is
	// worked and answered for when it is over (settleChats).
	chats  []string
	resume string
	// turn spends one stage as one session in the directory it is given, and
	// answers with what that session produced or with why there was no
	// answer. It is a field because the loop around it is the part worth
	// testing and a test that had to stand up a provider to reach it would
	// test neither.
	//
	// The directory is a parameter and not the driver's root because a lane
	// is spent in a copy of the checkout rather than in it: same process,
	// same reading of the answer, somewhere else (fanOut).
	//
	// Nil is the real one (ask), reached through spendTurn so that a lane's
	// copy of the driver asks as itself rather than as the driver it was
	// copied from.
	turn func(ctx context.Context, deadline time.Time, dir string, step run.Step) (todoTurn, error)
}

// spendTurn spends one stage through the turn seam, or as a process where
// nothing replaced it.
func (d *todoDriver) spendTurn(ctx context.Context, deadline time.Time, dir string, step run.Step) (todoTurn, error) {
	if d.turn != nil {
		return d.turn(ctx, deadline, dir, step)
	}
	return d.ask(ctx, deadline, dir, step)
}

// todoTurn is what one stage's process produced: the answer it wrote, the
// status it left, and whether that answer is a whole one.
type todoTurn struct {
	text string
	code int
	// truncated reports an answer the stage's own process ended on after the
	// model's output ceiling cut it and the one continuation a round allows
	// had already been spent. It is stated rather than inferred because
	// nothing in the words says it: the sentence stops, and a stage graded
	// on half a review or half an implementation is graded on half the work
	// in the one place nobody is watching.
	truncated bool
	// gate is what the stage's own close did about the project's checks, in
	// the three answers its transcript states. It is read rather than
	// inferred from the status for the same reason truncated is: a turn that
	// checked nothing and a turn whose checks passed leave the same exit
	// code behind.
	gate quality.Closing
	// written is the paths the stage's own calls wrote.
	written []string
	// sources is what the stage's process read, off its transcript's
	// sources field — that process's own ledger, never the answer's prose.
	sources []web.Source
	// chat is the slot the stage's process left its conversation in, and
	// resume the command that opens it again — both off the same transcript,
	// so the run never composes a command for a slot it only guessed at.
	// What becomes of them is settleChats.
	chat, resume string
	// cost is what the stage's process spent, off the record row its
	// transcript names — the ledger's own total, which that process wrote
	// there as it went. Zero where the run keeps no record.
	cost float64
	// overSpend is the session's cost cap refusing one of the stage's
	// requests, with the ledger's figures. The process reports it as a
	// failed turn, which is true of the process and not of the item: the
	// item stopped at a ceiling somebody set, and it blocks on the figures.
	overSpend *meter.CapError
}

func newTodoDriver(out io.Writer, root string, cfg config.Config, noCommit bool) (*todoDriver, error) {
	bin, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("cannot find the shhh binary a stage runs as: %w", err)
	}
	prompts, err := loadPrompts(cfg.Prompts, projectPrompts())
	if err != nil {
		return nil, err
	}
	d := &todoDriver{
		root: root, tree: root, bin: bin, out: out,
		session:     "todo-run-" + time.Now().UTC().Format("20060102-150405"),
		itemTimeout: cfg.TodoItemTimeout(),
		costCap:     cfg.TodoSprintCostCap(),
		noCommit:    noCommit || !cfg.TodoCommitEnabled(),
		repo:        project.InRepo(root),
		wordings:    prompts.todo,
		pipeline:    todoPipeline(),
	}
	d.secretIgnore = cfg.Commit.SecretIgnore
	d.trailers = cfg.Commit.Trailers
	d.slotCount = cfg.Agents.CheckSlots
	// The suites are command text out of a file that arrived with the clone
	// and the runner spends no approval on them, so an untrusted checkout
	// gets no gate at all rather than one that refuses when it is reached.
	if projectTrust().Allows() {
		d.gate = &quality.Runner{Workspace: root}
		recordGateFlakes(d.gate, root, func() string { return recordedSession(d.rec) })
		_, _, d.closeGate = onCloseGate(d.gate)
		// A generated file is regenerated in the copy it lands through
		// rather than merged, and the generator writes that copy — so it is
		// contained there, under the policy a session's gate stands in a
		// writer's copy.
		if avail := sandbox.Detect(); avail.OK {
			if policy, err := sandboxPolicy(cfg); err == nil {
				policy.PrivateGoCache = true
				d.gate.WrapIn = func(dir string, argv []string) ([]string, error) {
					p := policy
					p.Workspace, p.ReadOnlyWorkspace = dir, false
					return sandbox.WrapArgv(avail, p, argv)
				}
			}
		}
	}
	// A config that is present but broken is still the project saying what
	// checking means, and the gate says what is wrong with it where it runs.
	// Only "there is no file" is the absence a run is refused for.
	_, cfgErr := quality.LoadConfig(root)
	d.checks = !os.IsNotExist(cfgErr)
	// The record is opened last and never refuses the run: a sprint that
	// would not start because a table could not be written is a runner whose
	// bookkeeping outranks the work. A nil store is a run with no record,
	// which is what every stage already falls back to on its own. The row
	// names no model: the driver asks none, and each stage's own row carries
	// the model its surface resolved, so a comparison split on the model
	// counts the stages and never a guess made on the driver's behalf.
	if db, err := openStore(); err == nil {
		d.db = db
		d.rec = startObserveRecorder(db, "todo", cfg.Provider.Default, "", nil)
	}
	d.ledger = d.session + "#" + strconv.Itoa(os.Getpid())
	return d, nil
}

// close ends the run's row and lets the store go, in that order: the row is
// written on the connection this closes.
func (d *todoDriver) close() {
	d.rec.end()
	if d.db != nil {
		d.db.Close()
	}
}

// todoRunRefusal is what the command exits with when the run's steps ask for
// something this checkout cannot give. What the step wanted decides the
// sentence, because each need has its own way through and only the surface
// knows what to offer; the rest are facts about the process that no flag can
// change, and the pipeline's own clause is already the whole of them.
func todoRunRefusal(root string, ref run.Refusal) error {
	switch ref.Need {
	case run.NeedRepo:
		return fmt.Errorf("%s is not in a git repository and a run ends in a commit — --no-commit runs it without one, or todo.commit = false makes that the default", root)
	case run.NeedChecks:
		// The verify step is what the review, the commit and the archive all
		// happen because of, so a project that never said what checking its
		// work means would archive every item with nothing checked. Both ways
		// through are named: the file the gate reads, and the profile step
		// that carries its own command instead.
		return fmt.Errorf("%s has no %s, so the %s step would have nothing to run — define named suites there, or give the step a command of its own in the profile", root, quality.ConfigRelPath, ref.Step)
	}
	return errors.New(ref.Why)
}

// steps is the pipeline a run in this checkout takes, with the finish the
// invocation asked for.
func (d *todoDriver) steps() run.Pipeline {
	return run.Options{NoCommit: d.noCommit, Pipeline: d.pipeline}.Steps()
}

// can is what this process is able to do, which is what the run's steps are
// put to before the first of them is taken.
//
// The supervisor this runner has is not a session's: a child here is a
// process of its own — a lane standing in an isolated copy of the checkout, a
// reader given the change and none of the conversation that made it — and
// both of those need git, which is why the repository is the whole of the
// answer. A step that only sometimes happens and has somewhere to fall back
// to asks for nothing up front either way (Pipeline.Refuse).
func (d *todoDriver) can() run.Can {
	return run.Can{Changeset: true, Supervisor: d.repo, Runner: true, Repo: d.repo, Checks: d.checks}
}
