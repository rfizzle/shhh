package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/rfizzle/shhh/internal/evidence"
	"github.com/rfizzle/shhh/internal/todo"
	"github.com/rfizzle/shhh/internal/todo/run"
)

// sprint works the ready list one item at a time, each in a session of its
// own, and reports whether it stopped on a block. A sprint left behind by a
// process that died is continued rather than replaced: its checkpoint names
// the item it was on, and that item's checkpoint names the stage.
func (d *todoDriver) sprint(ctx context.Context, max int) bool {
	sp, live := run.Live(d.root)
	switch {
	case live:
		sp.Session, sp.NoCommit = d.session, d.noCommit
		if max > 0 {
			sp.Max = max
		}
		sp.Bound(d.costCapFlag, d.costCap)
		fmt.Fprintln(d.out, "continuing the sprint from its checkpoint — "+sp.Summary())
	default:
		sp = run.StartSprint(d.session, "", max, d.noCommit)
		sp.Bound(d.costCapFlag, d.costCap)
	}
	for {
		it, ok := d.sprintItem(sp)
		if !ok {
			break
		}
		if err := sp.Save(d.root); err != nil {
			sp.Stop()
			fmt.Fprintln(d.out, "the sprint's checkpoint could not be written — "+err.Error())
			break
		}
		st := d.work(ctx, it, sp)
		// What the item cost joins the set's total whichever way it ended,
		// and before anything reads the total again: the next item is taken
		// against it, and a blocked item's spend is spend the ceiling has to
		// see as much as a finished one's.
		sp.Spent(d.itemTurns, d.itemCost)
		blocked := st.Stage == run.StageBlocked
		if blocked {
			sp.Blocks(it.Slug, st.Blocked)
		} else {
			sp.Finished(it.Slug)
		}
		// The line between two items says what the set has spent against
		// its ceiling in the words the board says it in, so a log read the
		// next morning shows how close each item brought the set to it.
		if words := run.SpendWords(sp.Cost, sp.CapCents); words != "" {
			fmt.Fprintln(d.out, "sprint · "+sp.Count()+" · "+words)
		}
		if blocked {
			break
		}
	}
	run.DiscardSprint(d.root)
	fmt.Fprintln(d.out, todoSprintEnding(sp))
	return sp.Ended == run.SprintBlocked
}

// sprintItem is the item the sprint works next: the one it was interrupted
// on, or the next ready one.
func (d *todoDriver) sprintItem(sp *run.Sprint) (todo.Item, bool) {
	store := todo.Load(todoProfile(), d.root)
	if slug, resuming := sp.Resume(); resuming {
		it, ok := store.Find(slug)
		if !ok || it.Archived {
			sp.Blocks(slug, "the checkpoint names an item the backlog no longer holds")
			return todo.Item{}, false
		}
		return it, true
	}
	return sp.Next(store)
}

// todoSprintEnding is the sprint's last line: how much it got through and
// which of the closed reasons stopped it. The word matters more than the
// sentence — a sprint that ran out of ready items and one that stopped on a
// block leave the same quiet terminal, and only one of them is finished.
func todoSprintEnding(sp *run.Sprint) string {
	line := fmt.Sprintf("sprint over — %s · %s: %s", sp.Count(), sp.Ended, sp.Reason)
	// A sprint working several items at once went on past each block with
	// the items that did not rest on it, so the sentence is the serial one's.
	if sp.Ended == run.SprintBlocked && !sp.Laned() {
		line += "\nnothing further was attempted: a sprint stops on the first block, because what comes next may rest on the work that did not land"
	}
	return line
}

// work runs one item to its end and answers with the state it stopped in.
// sp is the sprint driving it, or nil for a single item asked for by name.
func (d *todoDriver) work(ctx context.Context, it todo.Item, sp *run.Sprint) *run.State {
	d.wrote, d.chats, d.resume = nil, nil, ""
	d.itemTurns, d.itemCost = 0, 0
	d.item = it.Slug
	d.openSpool(it.Slug)
	st, step := d.begin(it, sp != nil)
	deadline := time.Time{}
	if d.itemTimeout > 0 {
		from := time.Now()
		if sp != nil && !sp.ItemStarted.IsZero() {
			from = sp.ItemStarted
		}
		deadline = from.Add(d.itemTimeout)
	}
	for {
		// What the run may stage is read off the tree at every transition,
		// the way the session reads it off its changeset: an earlier process
		// of the same run left its paths in the checkpoint, and this one adds
		// whatever has changed since it started.
		st.Paths = d.paths(st)
		if err := st.Save(d.root); err != nil {
			fmt.Fprintln(d.out, "the run's checkpoint could not be written — "+err.Error())
		}
		// What the item has spent so far goes on the sprint's checkpoint at
		// the same boundary, so a process that dies mid-item leaves the
		// stages it paid for where the next one picks the sprint up.
		if sp != nil {
			sp.Running(d.ledger, d.itemTurns, d.itemCost)
			if err := sp.Save(d.root); err != nil {
				fmt.Fprintln(d.out, "the sprint's checkpoint could not be written — "+err.Error())
			}
		}
		d.lane.boundary(d, st)
		step.Model = st.ModelFor(step)
		d.say(st, step)
		if st.Over() {
			break
		}
		if !deadline.IsZero() && !time.Now().Before(deadline) {
			step = st.Block(run.TimedOut(d.itemTimeout))
			continue
		}
		// A lane told the branch moved under it catches up before its next
		// step, so the step reads the tree the lane will land into.
		why, carried, r := d.lane.catchUp()
		if why != "" {
			step = st.Block(why)
			continue
		}
		// A landing that met the lane's work in regions no rule settles is
		// a turn of the run's remediation step, which returns to verify.
		if r != nil {
			step = d.reconcile(st, it, r)
			continue
		}
		// A carry changed the copy, so a verdict reached before it is about
		// another tree: back to verify, without a fix round.
		if again, ok := d.verifyAfterCarry(ctx, st, it, carried, false); ok {
			step = again
			continue
		}
		step = d.carry(ctx, deadline, st, it, step)
	}
	if d.lane.end(ctx, d, st, it) {
		return st
	}
	d.finish(st, it, sp)
	d.settleChats(st)
	return st
}

// openSpool opens the store this item's stages spool into. A store that will
// not open costs the run its evidence ids and nothing else: the excerpts
// still reach the next stage, and a run that stopped because a directory
// would not be made would be refusing work over its own bookkeeping.
func (d *todoDriver) openSpool(slug string) {
	d.spool, d.spoolDir = nil, ""
	dir, err := run.MakeSpool(d.root, slug)
	if err != nil {
		fmt.Fprintln(d.out, "the run's evidence is not being kept — "+err.Error())
		return
	}
	store, err := evidence.OpenAt(dir, slug)
	if err != nil {
		fmt.Fprintln(d.out, "the run's evidence is not being kept — "+err.Error())
		return
	}
	d.spool, d.spoolDir = store.Put, dir
	if d.gate != nil {
		// The gate's checks are the ones a remediation turn is asked to fix,
		// so their whole output is what the store is for.
		d.gate.Evidence = store.Put
	}
}

// keep spools one stage's output and answers with the citation the next
// stage follows, in the wording the gate already uses for its own checks
// (quality.Format). Nothing is said where there is no store: an id nobody
// can resolve is worse than no id.
func (d *todoDriver) keep(tool string, content string) string {
	if d.spool == nil || content == "" {
		return ""
	}
	id, err := d.spool(tool, []byte(content))
	if err != nil {
		return ""
	}
	return " [full output: evidence " + id + "]"
}

// begin starts the item, or picks up the checkpoint an earlier process left.
// The stage the checkpoint names starts over, because the conversation that
// was part-way through it belonged to a process that is gone and a stage is
// the smallest thing the machine can judge.
func (d *todoDriver) begin(it todo.Item, inSprint bool) (*run.State, run.Step) {
	opt := run.Options{
		NoCommit: d.noCommit, Repo: d.repo, Sprint: d.sprintGoal(),
		CloseGate: d.closeGate, InSprint: inSprint || d.lane != nil,
		Groomed:  todo.GroomingBlock(d.root, it),
		Wordings: d.wordings,
		Pipeline: d.pipeline,
	}
	if it.Status == todo.StatusInProgress {
		if st, err := run.Load(d.root, it.Slug); err == nil && !st.Over() {
			st.Session, st.Reviewer = d.session, ""
			st.NoCommit, st.Repo, st.Sprint = opt.NoCommit, opt.Repo, opt.Sprint
			st.CloseGate, st.InSprint = opt.CloseGate, opt.InSprint
			st.Groomed, st.Wordings = opt.Groomed, opt.Wordings
			// The steps are re-stamped like the session and the mode, and
			// not read back out of the checkpoint, which does not carry
			// them: a state handed none reads as the profile a checkout of
			// code runs, so a continued reading would be worked through
			// stages it never had — and refused for having changed shape,
			// which is the sentence a run that really did change gets.
			st.Pipeline = opt.Steps()
			return st, st.Continue(it)
		}
		// An item left in progress by something that wrote no checkpoint is
		// not a run to continue and not one to start over either: starting
		// over would redo work that may be in the tree already.
		st := run.Start(it, d.session, "", 0, opt)
		return st, st.Block("the item is in progress with no checkpoint to continue from; `/todo open " + it.Slug + "` puts it back to open and a run can start over")
	}
	if err := todo.SetStatus(it.Path, todo.StatusInProgress); err != nil {
		st := run.Start(it, d.session, "", 0, opt)
		return st, st.Block("the item could not be marked in progress: " + err.Error())
	}
	st := run.Start(it, d.session, "", 0, opt)
	// The baseline is the tree as this item found it, and it is taken here
	// — once, where the item starts — rather than at every process that
	// works it. A sprint asked for without commits leaves each item's work
	// in the tree, so a baseline from before the sprint would hand every one
	// of those files to the next item as its own; and a run picked up after
	// a process died must subtract the baseline it began with, not the one
	// its second process finds, which by then holds the first one's work.
	// An empty baseline is a clean tree and is meant to stay empty.
	st.Prestart = run.DirtyPaths(d.tree)
	return st, st.First(it, "")
}

// carry does one step and answers with the next.
func (d *todoDriver) carry(ctx context.Context, deadline time.Time, st *run.State, it todo.Item, step run.Step) run.Step {
	switch step.Action {
	case run.ActionPrompt:
		t, err := d.spendTurn(ctx, deadline, d.tree, step)
		// What the stage wrote is the run's however the stage ended: a turn
		// that was cut off still edited the files it edited, and they are
		// what stays in the tree for the next process or for the person
		// reading the block.
		d.wrote = append(d.wrote, t.written...)
		d.keepChat(t)
		d.spent(t)
		readSources(st, t.sources)
		if t.overSpend != nil {
			return st.Block(run.OverSpend(step.Stage, t.overSpend.Spent, t.overSpend.Cap))
		}
		if err != nil {
			return st.Block(err.Error())
		}
		if t.truncated {
			// The stage's process already asked the model to finish the
			// sentence once, which is the whole of what a continuation is
			// worth here, and got another cut one back. The answer is not
			// read: a stage judged on half of one is how a run advances
			// past work that was never done.
			return st.Block(run.CutAtCeiling(step.Stage))
		}
		// The stage's own process ran the workspace's checks as it closed and
		// said so in its status, so the verify stage takes that verdict
		// instead of paying for the same suite over a tree that has not moved
		// between them. Only a clean exit carries: every other code is a turn
		// that ended some other way, and whether the checks ran at all before
		// it did is not something the status says. Reading one of those as a
		// pass would skip the verify stage's own run over a tree nothing has
		// checked.
		if st.ClosesWithGate() {
			st.Checks(t.gate)
		}
		// A turn that was reconciling a landing is judged on the copy it
		// left before its answer is read.
		if why := d.lane.judged(d); why != "" {
			return st.Block(why)
		}
		return st.Observe(it, t.text)
	case run.ActionVerify:
		v := d.verify(ctx, st, step.Command)
		d.lane.again(nil)
		d.lane.reconcileWords("")
		if v.output != "" {
			fmt.Fprintln(d.out, v.output)
		}
		if v.blocked != "" {
			return st.Block(v.blocked)
		}
		return st.VerifyResult(it, v.ok, v.output)
	case run.ActionPause:
		// The pause is the one gate that asks a person, and there is nobody
		// here to ask. Guessing the answer is the one thing a deterministic
		// runner must not do, so the item stops with the questions on it.
		return st.Block("the run reached a decision and there is nobody to ask — " + st.Paused)
	case run.ActionReview:
		// A stage that was meant to change the tree and left it exactly as it
		// found it has produced nothing to review and nothing to commit, and
		// another round over the same plan would produce the same nothing. A
		// run whose steps only read has no change to point at by design, and
		// the reader is given what the run gathered instead.
		if st.Pipeline.Writes() && len(st.Paths) == 0 {
			return st.Block("the run changed no files under the repository, so there is nothing to review")
		}
		return d.review(ctx, deadline, st, it, step)
	case run.ActionFanOut:
		return d.fanOut(ctx, deadline, st, it, step)
	case run.ActionCommit:
		// A lane's commit is its landing: the patch goes onto the checkout
		// and the commit is made there, one lane at a time, after a last
		// carry and, if it changed the copy, a verify (laneCommit).
		if d.lane != nil {
			return d.laneCommit(ctx, st, it)
		}
		files, err := d.commit(st)
		if err != nil {
			return st.Block("the commit could not be made: " + err.Error())
		}
		return st.Committed(files)
	case run.ActionWait:
		// The children this runner has are waited on where they are started
		// — a lane inside the fan-out, a reader inside the review — so a
		// wait that reaches the loop is a wait on nothing.
		return st.Block("the run is waiting on a child it did not start")
	}
	// Every action the machine has is answered above. Handing the same step
	// back would be an unattended process spinning on it forever, which is
	// the one failure here nobody would be watching for.
	return st.Block("the run reached a step this runner has no answer for: " + step.Name())
}

// say prints one transition, in the words the stage gave it.
func (d *todoDriver) say(st *run.State, step run.Step) {
	note := ""
	if step.Action == run.ActionPrompt || step.Action == run.ActionReview || step.Action == run.ActionFanOut {
		note = st.ModelNote(step)
	}
	if step.Shown != "" {
		fmt.Fprintln(d.out, step.Shown+note)
		return
	}
	fmt.Fprintf(d.out, "▸ todo run %s · %s%s\n", st.Slug, step.Name(), note)
}
