package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/rfizzle/shhh/internal/subagent/worktree"
	"github.com/rfizzle/shhh/internal/todo"
	"github.com/rfizzle/shhh/internal/todo/run"
)

// review hands the change to a reader that did not write it: a process of its
// own, given the item, the plan and the run's diff, and none of the
// conversation that produced the work. That is the whole of what a reviewer
// child is worth here — a second opinion is only a second one where the
// reader has not spent the last ten rounds convincing itself the work is
// right — and it is why SelfReview below is the fallback rather than the
// ordinary path this surface takes.
//
// The fallback stays for the checkout that cannot produce a change to hand
// over. Outside a repository there is no diff, and a reader given the item
// and nothing else would be grading the plan rather than the work; there the
// orchestrator reads the tree in its own turn, and the step label says so.
// See docs/capabilities/todo.md#the-reading-is-done-by-somebody-else.
func (d *todoDriver) review(ctx context.Context, deadline time.Time, st *run.State, it todo.Item, step run.Step) run.Step {
	if !d.repo {
		return st.SelfReview(it)
	}
	task := st.ReviewTask(it, run.BoundDiff(d.reviewDiff(st), run.ReviewDiffLines, run.ReviewFileFloor))
	if strings.TrimSpace(task) == "" {
		return st.SelfReview(it)
	}
	t, err := d.spendTurn(ctx, deadline, d.tree,
		run.Step{Action: run.ActionPrompt, Stage: step.Stage, Mode: step.Mode, Prompt: task})
	d.keepChat(t)
	d.spent(t)
	readSources(st, t.sources)
	// A reader stopped by the cap is not a reader that is merely missing:
	// the reading in this session would be one more request against a
	// ceiling the item has already reached.
	if t.overSpend != nil {
		return st.Block(run.OverSpend(step.Stage, t.overSpend.Spent, t.overSpend.Cap))
	}
	// A reader that did not finish is a reader the run did not get, which is
	// what SelfReview is for. Blocking on it stops a finished, verified piece
	// of work over the one stage that was always allowed to be missing — the
	// checkout without a repository has never had a reader either — and the
	// step label is what says which reading this was.
	switch {
	case err != nil:
		fmt.Fprintf(d.out, "the reviewer %s did not finish (%s); reading it in this session instead\n",
			st.Reviewer, todoFirstProblem(err.Error()))
		return st.SelfReview(it)
	case t.truncated:
		fmt.Fprintf(d.out, "the reviewer %s was cut at the model's output ceiling; reading it in this session instead\n", st.Reviewer)
		return st.SelfReview(it)
	}
	return st.ReviewResult(it, t.text)
}

// reviewDiff is the change the reader is handed, one file at a time: `git
// diff` over the paths the run holds, with a file git has never heard of
// shown whole against nothing. It is the reading a session takes off its
// changeset, in the one form a runner that keeps no changeset has — and a
// path git will say nothing about is left out rather than reported as an
// empty change.
//
// It answers with the files rather than with one string because what the
// reader is handed is bounded per file (run.BoundDiff): a budget spent in
// order hands over the first files whole and never mentions the rest.
func (d *todoDriver) reviewDiff(st *run.State) []string {
	var out []string
	for _, rel := range st.Paths {
		if d, code := run.Git(d.tree, "diff", "--", rel); code == 0 && strings.HasPrefix(d, "diff --git") {
			out = append(out, d)
			continue
		}
		if d, _ := run.Git(d.tree, "diff", "--no-index", os.DevNull, rel); strings.HasPrefix(d, "diff --git") {
			out = append(out, d)
		}
	}
	return out
}

// fanOut builds a large item in lanes, all of them at once. A lane is a
// process — the same one a working stage is spent as, standing in an isolated
// copy of the checkout rather than in the checkout — because this runner has
// no supervisor to spawn a child from, and because isolation is what a lane
// actually needs: writers that cannot see or overwrite each other's files,
// and a patch each that lands whole or not at all.
//
// The copies are made before any lane starts, so a checkout that cannot give
// one is a fall back to building the plan whole rather than a half-started
// fan-out with a process already writing. The patches land afterwards, one at
// a time and in lane order, which is what makes two lanes over one file an
// ending with evidence on it instead of a race: the first lands, the second
// is refused whole, and the run blocks naming the lane.
// See docs/capabilities/todo.md#a-large-item-is-built-in-lanes.
func (d *todoDriver) fanOut(ctx context.Context, deadline time.Time, st *run.State, it todo.Item, step run.Step) run.Step {
	if !d.repo {
		return st.NoLanes(it, "a lane needs an isolated copy of the checkout and this is not a git repository")
	}
	var lanes []run.Lane
	for _, l := range st.Lanes {
		if l.Agent != "" {
			lanes = append(lanes, l)
		}
	}
	if len(lanes) == 0 {
		return st.NoLanes(it, "no lane is waiting to be built")
	}

	trees := make([]*worktree.Worktree, 0, len(lanes))
	// Inside a sprint's lane the copies are made, landed and removed under
	// the sprint's own worktree lock: several lanes can divide at once, and
	// git's worktree administration is not safe run concurrently in one
	// repository.
	defer func() {
		release := d.lane.admin()
		defer release()
		for _, t := range trees {
			t.Remove()
		}
	}()
	release := d.lane.admin()
	for range lanes {
		// Seeded with what the run has changed so far, the way a session
		// seeds a writer from its changeset: the earlier stages' work is in
		// this tree uncommitted, and a lane started without it writes its
		// patch against text the checkout no longer has.
		wt, err := worktree.NewWorktree(d.tree, st.Paths)
		if err != nil {
			release()
			return st.NoLanes(it, "no isolated copy of the checkout could be made: "+todoFirstProblem(err.Error()))
		}
		if d.gate != nil {
			wt.UseGenerators(d.gate)
		}
		trees = append(trees, wt)
	}
	release()

	// Every lane's step is built before any lane starts. The run's state is
	// one value and the lanes run at once, so a task read inside a goroutine
	// would be several readers of it for no gain: what a lane is asked is
	// settled here and nothing changes it while they work.
	steps := make([]run.Step, len(lanes))
	for i, lane := range lanes {
		steps[i] = run.Step{Action: run.ActionPrompt, Stage: st.Stage, Mode: step.Mode,
			Prompt: st.LaneTask(it, lane)}
	}
	turns := make([]todoTurn, len(lanes))
	errs := make([]error, len(lanes))
	// A lane's conversation is one of the item's like any other, gathered on
	// the way out however the fan-out ended.
	defer func() {
		for _, t := range turns {
			d.keepChat(t)
		}
	}()
	var wg sync.WaitGroup
	for i := range lanes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			turns[i], errs[i] = d.spendTurn(ctx, deadline, trees[i].Root(), steps[i])
		}(i)
	}
	wg.Wait()
	for _, t := range turns {
		d.spent(t)
		readSources(st, t.sources)
	}

	next := run.Step{Action: run.ActionWait, Stage: st.Stage}
	for i, lane := range lanes {
		switch {
		case turns[i].overSpend != nil:
			return st.LaneFailed(lane.Agent, run.OverSpend(st.Stage, turns[i].overSpend.Spent, turns[i].overSpend.Cap))
		case errs[i] != nil:
			return st.LaneFailed(lane.Agent, todoFirstProblem(errs[i].Error()))
		case turns[i].truncated:
			return st.LaneFailed(lane.Agent, run.CutAtCeiling(st.Stage))
		}
		release := d.lane.admin()
		files, err := trees[i].Land()
		release()
		if err != nil {
			return st.LaneFailed(lane.Agent, "its patch would not apply: "+todoFirstProblem(err.Error()))
		}
		// A lane that landed nothing is not failed here: LaneDone is where
		// "finished but its patch did not land" is said, in the words the
		// record already has for it.
		if len(files) > 0 {
			st.LanePatched(lane.Agent)
			fmt.Fprintf(d.out, "lane %s landed %s\n", lane.Name, countOf(len(files), "file", "files"))
		}
		if next = st.LaneDone(it, lane.Agent, true, turns[i].text); next.Action == run.ActionBlocked {
			return next
		}
	}
	return next
}
