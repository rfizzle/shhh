package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/meter"
	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/runner"
	"github.com/rfizzle/shhh/internal/todo/run"
)

// ask spends one stage as one session: a print run in the checkout, with the
// stage's prompt and the permissions its mode asks for. The working stages
// auto-approve, because there is nobody to approve for them; the reading
// stages do not, which is what keeps a review from editing the tree it is
// reviewing.
//
// The answer is read out of the transcript whatever the process's status. A
// stage that ran out of rounds or lost the provider still produced whatever
// it produced, and the machine judges a stage on its answer — an empty one is
// what blocks the item, not a non-zero status.
//
// The transcript is also where the process says whether the answer it quotes
// is a whole one, because the status cannot: a turn that ended at the model's
// output ceiling ended the way turns end.
func (d *todoDriver) ask(ctx context.Context, deadline time.Time, dir string, step run.Step) (todoTurn, error) {
	args := append(todoStageArgs(d.steps().Writes(), step.Mode), step.Prompt)
	if !deadline.IsZero() {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, deadline)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, d.bin, args...)
	cmd.Dir = dir
	cmd.Env = d.stageEnv(dir, step)
	// A sprint that is cancelled, by its deadline or by the driver's own
	// context ending, interrupts the stage's turn rather than killing it, so
	// the child writes its record and leaves a slot the way the contract
	// promises; the delay is the grace before the kill that a second signal
	// would have delivered at a terminal.
	// See docs/capabilities/headless.md#what-a-signal-does-to-a-run.
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 10 * time.Second
	var out, errOut strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errOut
	runErr := cmd.Run()
	code := exitDone
	var ee *exec.ExitError
	if errors.As(runErr, &ee) {
		code = ee.ExitCode()
	}
	var t struct {
		Final     string       `json:"final"`
		Error     string       `json:"error"`
		Truncated bool         `json:"truncated"`
		Gate      string       `json:"gate"`
		Written   []string     `json:"written"`
		Chat      string       `json:"chat"`
		Session   string       `json:"session"`
		Resume    string       `json:"resume"`
		Sources   []jsonSource `json:"sources"`
	}
	_ = json.Unmarshal([]byte(out.String()), &t)
	// The paths are relative to the directory the stage stood in, which is
	// the run's root for every stage but a lane — and a lane's patch is
	// recorded where it lands, not here.
	written := t.Written
	if dir != d.tree {
		written = nil
	}
	turn := todoTurn{code: code, truncated: t.Truncated, chat: t.Chat, resume: t.Resume,
		gate: quality.Closing(t.Gate), written: todoWritten(d.tree, written),
		sources: ledgerRows(t.Sources), cost: d.stageCost(t.Session)}
	// A refusal at the cap is read before the answer is, because the turn it
	// ended is not one the machine may judge: whatever the stage wrote
	// before the refusal is half of a step, the same way a reply cut at the
	// output ceiling is.
	if c, ok := meter.ReadCap(t.Error); ok {
		turn.overSpend = c
		return turn, nil
	}
	if strings.TrimSpace(t.Final) != "" {
		turn.text = t.Final
		return turn, nil
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return turn, errors.New(run.TimedOut(d.itemTimeout))
	}
	return turn, fmt.Errorf("the %s turn produced no answer (exit %d): %s",
		step.Stage, code, todoFirstProblem(t.Error, errOut.String(), errString(runErr)))
}

// stageCost is what one stage's process spent, read off the record row its
// transcript named. The row is the join the transcript states that field
// for (docs/capabilities/headless.md#the-run-says-where-it-left-off), and
// the process wrote the ledger's own priced total into it, so the figure is
// the one the process's cap was measured against rather than a second
// pricing of its tokens. A run with no record, or a row it cannot read,
// costs nothing here — the sprint's ceiling then sees less than was spent,
// and each item's own session cap still stands.
func (d *todoDriver) stageCost(session string) float64 {
	id, err := strconv.ParseInt(session, 10, 64)
	if err != nil || id <= 0 || d.db == nil {
		return 0
	}
	s, ok, err := d.db.AgentSession(id)
	if err != nil || !ok {
		return 0
	}
	return s.Cost
}

// spent adds one stage process to what the item has cost. Every caller of
// turn goes through it on the loop's own goroutine — a fan-out's lanes are
// added after they are waited on — so the two fields need no lock.
func (d *todoDriver) spent(t todoTurn) {
	d.itemTurns++
	d.itemCost += max(t.cost, 0)
}

// spendFigure is what the set has spent, for the notes of a sprint file this
// item's archive closes: the checkpoint's total plus this item, which is not
// added to it until the item is over, against the ceiling where the loop has
// one. A single item run with no loop around it has no ceiling and no
// earlier items, so its figure is its own.
func (d *todoDriver) spendFigure(sp *run.Sprint) string {
	if d.lane != nil {
		return d.lane.spendFigure(d.itemCost)
	}
	if sp == nil {
		return run.SpendFigure(d.itemCost, 0)
	}
	return run.SpendFigure(sp.Cost+d.itemCost, sp.CapCents)
}

// stageEnv is the environment one stage's process runs in: this run's, plus
// where the stage's record row hangs, which item and stage it is, and the
// store its stages share where there is one.
//
// The record variables go to every stage, a lane included: a lane is a stage
// of this item worked somewhere else, and a tree that lost its four most
// expensive branches would be a tree of the cheap half of the work. The
// evidence store is the one that does not, and only a stage standing in the
// checkout is pointed at it. A lane stands in a copy of the tree and writes
// its own evidence, and several lanes sharing one store would be several
// processes writing one index at once — the run would lose entries to
// whichever wrote last, which is worse than a lane keeping its evidence to
// itself.
func (d *todoDriver) stageEnv(dir string, step run.Step) []string {
	env := runner.Environ()
	if env == nil {
		// A session with nothing to add to the environment answers with
		// nothing at all, which exec reads as "inherit"; appending to that
		// would hand the stage these variables and no PATH. It is taken
		// before the branches rather than inside them, so every stage is
		// started from one reading of the environment — the snapshot and
		// the inherit are the same environment either way, since nothing
		// here changes it between this line and the exec.
		env = os.Environ()
	}
	if id := d.rec.sessionID(); id > 0 {
		env = append(env, parentSessionEnv+"="+strconv.FormatInt(id, 10))
	}
	if d.item != "" {
		env = append(env, todoItemEnv+"="+d.item)
	}
	if step.Stage != "" {
		env = append(env, todoStageEnv+"="+string(step.Stage))
	}
	if d.spoolDir == "" || dir != d.tree {
		return env
	}
	return append(env, evidenceStoreEnv+"="+d.spoolDir)
}

// keepChat gathers the conversation a stage's process saved, and the command
// that opens it, for the item to answer for when it is over.
//
// It is gathered rather than answered for on the spot because a stage is not
// where an item stops. A turn that came back clean hands the run to the
// checks, and it is the checks that fail and the remediation rounds that run
// out — several transitions later, in a step that spends no turn of its own.
// A rule applied at the stage would therefore delete the implement
// conversation the moment its own answer parsed, which is precisely the one
// the block is about.
func (d *todoDriver) keepChat(t todoTurn) {
	if t.chat == "" {
		return
	}
	d.chats = append(d.chats, t.chat)
	if t.resume != "" {
		d.resume = t.resume
	}
}

// settleChats is what becomes of them once the item is over: taken away where
// the item went through, kept whole where it stopped.
//
// A sprint is one item after another and an item is a handful of stages, so a
// run that kept every one of them would leave a person's saved-chat list as
// forty-eight timestamps a day, none of which they had anything to do with —
// and the list is the one place in the product that holds their own work. A
// blocked item is the exception, and all of its conversations are kept rather
// than the last: the item says what stopped the run and the evidence spool is
// gone with it, so what is left to read is how the run got there, and which
// stage that reading starts at is not something the runner knows.
//
// Best effort and quiet. A conversation that could not be deleted is a row in
// a table with a window on it, not a run to stop
// (docs/capabilities/sessions-and-memory.md#a-conversation-is-kept-for-a-window).
func (d *todoDriver) settleChats(st *run.State) {
	if st.Stage == run.StageBlocked {
		// Said out loud, because a conversation nobody can name is one
		// nobody opens: the block is written for a person, and this is the
		// half of it the item file cannot carry.
		if d.resume != "" {
			fmt.Fprintln(d.out, "the run's conversations are kept — the last stage's is "+d.resume)
		}
		return
	}
	if d.db == nil {
		return
	}
	for _, chat := range d.chats {
		_ = d.db.DeleteChat(chat)
	}
}

// todoWritten is a stage's own written paths in the shape a commit names
// them: relative to the run's root, with anything outside it and anything no
// run may stage left out.
func todoWritten(root string, paths []string) []string {
	var out []string
	for _, p := range paths {
		if filepath.IsAbs(p) {
			rel, err := filepath.Rel(root, p)
			if err != nil || strings.HasPrefix(rel, "..") {
				continue
			}
			p = rel
		}
		if rel := run.Committable(p); rel != "" {
			out = append(out, rel)
		}
	}
	return out
}

// todoStageArgs is the process a stage's turn is spent as, and the whole of
// the choice. A step that changes the tree is a coding session in auto: it
// needs the editor, the runner and the changeset, and there is nobody to
// approve each edit for it. A step that only reads needs none of those, so a
// run whose every step reads is a conversation from end to end and never
// loads a coding agent's containment or toolset for work that would not
// touch them.
//
// The reading steps of a run that does write stay with the coding agent, and
// that is what `writes` is for. What those steps read is the change the run
// made — the diff a review is of, the history a commit message is written
// into — and reading it takes tools a conversation does not have.
// See docs/capabilities/headless.md#the-backlog-worked-without-you.
func todoStageArgs(writes bool, mode run.Mode) []string {
	if mode == run.ModeAuto {
		return []string{"code", "--print", "--output", "json", "--yes"}
	}
	if writes {
		return []string{"code", "--print", "--output", "json"}
	}
	return []string{"chat", "--print", "--output", "json"}
}

// todoFirstProblem is the first of the places a failed stage says why, in the
// order they are worth reading: the transcript's own error field, then what
// the process wrote to stderr, then the exit itself.
func todoFirstProblem(candidates ...string) string {
	for _, c := range candidates {
		if line := strings.TrimSpace(c); line != "" {
			if i := strings.IndexByte(line, '\n'); i >= 0 {
				line = strings.TrimSpace(line[:i])
			}
			return line
		}
	}
	return "the process said nothing"
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
