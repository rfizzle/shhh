package cli

// A parallel sprint is the sprint loop with more than one item in flight:
// `shhh todo run --all --parallel N` takes up to N ready items at once, each
// into a lane — its own copy of the checkout, seeded from the checkout the
// way a session's writer is, and a driver working the item there exactly as
// it would have been worked in place. What the loop adds to the serial one is
// a claim and a landing.
//
// The claim is the item's declared paths: an item is taken beside the lanes
// already running only where its list meets none of theirs, and an item that
// declares nothing waits for the lanes to drain and is worked alone
// (run.Sprint.TakeLane).
//
// The landing is serial and in finish order, and it is the session's own: a
// lane is a writer, so its patch lands through Worktree.Land — merged three
// ways where the checkout moved under it — and every other lane carries what
// landed into its copy at its next stage boundary (Worktree.Reseed), the way
// a writer child carries it at its next round. What differs is who answers a
// collision: where a carried landing meets the lane's work on lines no rule
// settles, the lane's own run reconciles it in a turn of its remediation
// step, judged, verified and reviewed with the item (todoDriver.reconcile),
// where a session would start an integration writer. A turn that does not
// reconcile, a landing that will not carry at all, and a landing refused at
// the end of a run without commits block that lane's item with the files
// named and free the lane. The sprint goes on with the rest.
// See docs/capabilities/subagents.md#a-writer-starts-from-your-tree and
// docs/capabilities/todo.md#a-sprint-can-work-several-items-at-once.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/subagent/worktree"
	"github.com/rfizzle/shhh/internal/todo"
	"github.com/rfizzle/shhh/internal/todo/run"
)

// todoStopPoll is how often a parallel sprint looks for a stop another
// surface asked for (run.RequestStop).
const todoStopPoll = time.Second

// todoParallelism is how many items the sprint works at once: the flag where
// one above one was given, the number a sprint being continued was started
// with where it was not, and one otherwise — which is the loop as it always
// was.
func todoParallelism(root string, flag int) int {
	if flag > 1 {
		return flag
	}
	if sp, live := run.Live(root); live && sp.Laned() {
		return sp.Parallel
	}
	return max(flag, 1)
}

// todoLanes is what a parallel sprint's lanes share: the checkpoint, behind
// its one writer, and the branch, behind the lock every landing takes.
type todoLanes struct {
	root string
	out  io.Writer
	// top is the repository's toplevel, resolved, which is what a patch's
	// paths are relative to; a checkout below it names its files from
	// further down.
	top string

	// mu is the checkpoint's one writer. Every lane reports through it and
	// saves under it, so two lanes finishing together cannot each write a
	// file that forgets the other.
	mu sync.Mutex
	sp *run.Sprint

	// land is held by everything that writes the checkout or copies it: a
	// landing, a lane carrying one into its copy, and a copy being made or
	// torn down. Worktree administration is serial because git's own is not
	// safe run concurrently in one repository, and a copy made mid-landing
	// would be seeded with a patch that is about to become a commit.
	land sync.Mutex
	// landings is every patch that has landed on the checkout this sprint,
	// in the order it landed, for each lane to carry into its copy.
	landings []todoLanding
}

// todoLanding is one lane's patch as it landed on the checkout.
type todoLanding struct {
	slug, patch string
}

// todoLane is one lane: the item, the copy it is worked in, and where that
// copy stands against the checkout.
type todoLane struct {
	set  *todoLanes
	slug string
	wt   *worktree.Worktree
	// seen is how many of the set's landings the copy has carried.
	seen int
	// landed reports the lane's patch on the checkout; kept that the copy
	// is left standing for a person to read, because what is in it did not
	// land.
	landed, kept bool
	// reconciling is a landing the lane's next turn reconciles, from the
	// carry that marked the copy until the turn is judged; nil otherwise.
	reconciling *todoReconcile
}

// todoLaneResult is what a lane's item ended as, for the loop to record.
type todoLaneResult struct {
	slug  string
	done  bool
	why   string
	turns int
	cost  float64
}

// todoSyncWriter is one writer several lanes print to at once. Each line is
// one write, so a line is never interleaved with another lane's.
type todoSyncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *todoSyncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

// sprintParallel works the ready list up to n items at once and reports
// whether the sprint ended blocked.
func (d *todoDriver) sprintParallel(ctx context.Context, maxItems, n int) bool {
	sp, live := run.Live(d.root)
	if live {
		sp.Session, sp.NoCommit = d.session, d.noCommit
		if maxItems > 0 {
			sp.Max = maxItems
		}
		sp.Bound(d.costCapFlag, d.costCap)
		fmt.Fprintln(d.out, "continuing the sprint from its checkpoint — "+sp.Summary())
	} else {
		sp = run.StartSprint(d.session, "", maxItems, d.noCommit)
		sp.Bound(d.costCapFlag, d.costCap)
	}
	sp.Parallel = n
	// A stop left over from an earlier sprint is not this one's to answer.
	run.ClearStop(d.root)
	d.out = &todoSyncWriter{w: d.out}
	// The sprint's checks take turns across every process it starts, under
	// the run directory.
	d.slots = subagent.OpenFileSlots(filepath.Join(run.Dir(d.root), todoSlotsDir), d.slotCount, "")
	set := &todoLanes{root: d.root, out: d.out, sp: sp, top: todoRepoTop(d.root)}
	for _, l := range sp.Orphans() {
		d.orphaned(sp, l)
	}
	set.save()

	// A stop is an interrupt to every lane's step in flight, from a signal
	// at the terminal or from a surface that asked through the file.
	ctx, stopSignals := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		tick := time.NewTicker(todoStopPoll)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				if run.StopRequested(d.root) {
					cancel()
					return
				}
			}
		}
	}()

	go set.watchSlots(ctx, d.slots.Dir())
	results := make(chan todoLaneResult)
	running := 0
	for {
		for ctx.Err() == nil {
			it, ok := set.take()
			if !ok {
				break
			}
			running++
			go func() { results <- d.runLane(ctx, set, it) }()
		}
		if running == 0 {
			break
		}
		r := <-results
		running--
		set.ended(r)
	}
	set.mu.Lock()
	if ctx.Err() != nil {
		sp.Stop()
	}
	set.mu.Unlock()
	run.DiscardSprint(d.root)
	run.ClearStop(d.root)
	fmt.Fprintln(d.out, todoSprintEnding(sp))
	return sp.Ended == run.SprintBlocked
}

// orphaned answers for a lane a dead process left on the checkpoint. Its
// copy of the checkout went with the process that was working it, so the run
// cannot be continued; the item blocks with the copy's place named, because
// whatever the lane had built before the process died may still be there.
func (d *todoDriver) orphaned(sp *run.Sprint, l run.SprintLane) {
	why := "the sprint's process ended while this item was being worked in its own copy of the checkout, and nothing from that copy landed"
	if l.Tree != "" {
		why += "; the copy was " + l.Tree + ", and `git worktree list` says whether it is still there"
	}
	if it, ok := todo.Load(todoProfile(), d.root).Find(l.Slug); ok && !it.Archived {
		_ = todo.SetStatus(it.Path, todo.StatusBlocked)
		_ = todo.Append(it.Path, fmt.Sprintf("## Blocked\n%s\n\n_run in session %s, stage %s, %s_",
			why, sp.Session, l.Stage, time.Now().Format("2006-01-02 15:04")))
	}
	run.Discard(d.root, l.Slug)
	run.ClearSpool(d.root, l.Slug)
	sp.LaneEnded(l.Slug, false, why, 0, 0)
	fmt.Fprintf(d.out, "✗ todo run %s blocked — %s\n", l.Slug, why)
}

// take is the next item a free lane starts, taken and written down.
func (s *todoLanes) take() (todo.Item, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.sp.TakeLane(todo.Load(todoProfile(), s.root))
	s.saveLocked()
	return it, ok
}

// ended records a lane's item as over and says what the set has spent where
// a ceiling is set, in the words the serial loop says it between two items.
func (s *todoLanes) ended(r todoLaneResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sp.LaneEnded(r.slug, r.done, r.why, r.turns, r.cost)
	s.saveLocked()
	if words := run.SpendWords(s.sp.Cost, s.sp.CapCents); words != "" {
		fmt.Fprintln(s.out, "sprint · "+s.sp.Count()+" · "+words)
	}
}

// placed writes where a lane's item is being worked.
func (s *todoLanes) placed(slug, tree string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if l, ok := s.sp.Lane(slug); ok {
		l.Tree = tree
		l.Checkpoint = filepath.Join(run.Dir(s.root), slug+".json")
	}
	s.saveLocked()
}

func (s *todoLanes) save() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saveLocked()
}

func (s *todoLanes) saveLocked() {
	if err := s.sp.Save(s.root); err != nil {
		fmt.Fprintln(s.out, "the sprint's checkpoint could not be written — "+err.Error())
	}
}

// runLane works one item in a lane of its own and answers with how it ended.
func (d *todoDriver) runLane(ctx context.Context, set *todoLanes, it todo.Item) todoLaneResult {
	set.land.Lock()
	// Seeded from the checkout as it stands, which is what a writer child
	// starts from too: the lane's patch is then measured against the
	// checkout's own text rather than the last commit's.
	// See docs/capabilities/subagents.md#a-writer-starts-from-your-tree.
	wt, err := worktree.NewWorktree(d.root, nil)
	seen := len(set.landings)
	set.land.Unlock()
	if err != nil {
		why := "no copy of the checkout could be made for the item's lane: " + todoFirstProblem(err.Error())
		_ = todo.SetStatus(it.Path, todo.StatusBlocked)
		_ = todo.Append(it.Path, "## Blocked\n"+why)
		fmt.Fprintf(d.out, "✗ todo run %s blocked — %s\n", it.Slug, why)
		return todoLaneResult{slug: it.Slug, why: why}
	}
	// A generated file in the lane's patch, or in one it carries, is
	// regenerated rather than merged, as a writer's is.
	if d.gate != nil {
		wt.UseGenerators(d.gate)
	}
	lane := &todoLane{set: set, slug: it.Slug, wt: wt, seen: seen}
	set.placed(it.Slug, wt.Root())
	ld := d.laneDriver(lane)
	st := ld.work(ctx, it, nil)
	if !lane.kept {
		set.land.Lock()
		wt.Remove()
		set.land.Unlock()
	}
	return todoLaneResult{slug: it.Slug, done: st.Stage == run.StageDone, why: st.Blocked,
		turns: ld.itemTurns, cost: ld.itemCost}
}

// laneDriver is this driver standing in a lane: the same run, the same
// answers and the same record, with the work done in the lane's copy and a
// ledger of its own for the lane's running figure.
func (d *todoDriver) laneDriver(l *todoLane) *todoDriver {
	c := *d
	c.tree, c.lane = l.wt.Root(), l
	c.ledger = d.ledger + "/" + l.slug
	c.wrote, c.chats, c.resume = nil, nil, ""
	// The checks run over the lane's copy, which is the tree the lane will
	// land; the checkout's own would be checking the work of whoever landed
	// last. A generator the lane's own copies run (a fan-out's landing) is
	// contained in the tree it writes, as the checkout's is.
	if d.gate != nil {
		c.gate = &quality.Runner{Workspace: c.tree, WrapIn: d.gate.WrapIn, Slot: c.takeCheckSlot}
		// A flake in the lane's copy is the checkout's flake: the ledger is
		// keyed on the checkout the copy was made from, never the copy,
		// which is gone once the lane lands.
		recordGateFlakes(c.gate, d.root, func() string { return recordedSession(d.rec) })
	}
	return &c
}

// todoSlotsDir is where, under the run directory, a parallel sprint's check
// slots are locked.
const (
	todoSlotsDir  = "slots"
	todoSlotsPoll = 500 * time.Millisecond
)

// takeCheckSlot is the one place a lane takes one of the sprint's check
// slots, for anything of its own that loads the machine: its quality gate
// takes it through quality.Runner.Slot, and a step that runs a suite itself
// calls it too. A stage's process takes its slots in the process, from the
// same directory (subagent.SlotDirEnv). It waits while every slot is held,
// answers the release, and false where ctx ended first. Outside a parallel
// sprint there are no slots and it answers at once.
func (d *todoDriver) takeCheckSlot(ctx context.Context) (func(), bool) {
	if d.slots == nil {
		return func() {}, true
	}
	lane := ""
	if d.lane != nil {
		lane = d.lane.slug
	}
	return d.slots.ForLane(lane).Take(ctx, nil)
}

// watchSlots reads which lanes are waiting for a check slot and says so on
// the board, through the lane's entry in the checkpoint, and in the sprint's
// log, once as each wait begins. It runs until the sprint's context ends.
func (s *todoLanes) watchSlots(ctx context.Context, dir string) {
	tick := time.NewTicker(todoSlotsPoll)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		waits := subagent.SlotWaits(dir)
		s.mu.Lock()
		changed := false
		for i := range s.sp.Lanes {
			l := &s.sp.Lanes[i]
			want := ""
			if running, ok := waits[l.Slug]; ok {
				want = fmt.Sprintf("waiting for a check slot (%d running)", running)
			}
			if want == l.Wait {
				continue
			}
			l.Wait, changed = want, true
			if want != "" {
				fmt.Fprintf(s.out, "… %s %s\n", l.Slug, want)
			}
		}
		if changed {
			s.saveLocked()
		}
		s.mu.Unlock()
	}
}

// boundary writes where the lane's item is and what it has spent, at each
// step boundary, through the checkpoint's one writer: the figure a process
// that dies mid-item leaves for the next one to count.
func (l *todoLane) boundary(d *todoDriver, st *run.State) {
	if l == nil {
		return
	}
	l.set.mu.Lock()
	defer l.set.mu.Unlock()
	if lane, ok := l.set.sp.Lane(l.slug); ok {
		lane.Stage, lane.Ledger = st.Stage, d.ledger
		lane.Turns, lane.Cost = d.itemTurns, d.itemCost
	}
	l.set.saveLocked()
}

// catchUp carries every patch that has landed on the checkout since the lane
// last looked into its copy, and answers with the evidence to block on where
// one will not carry. It is the writer's reseed at the writer's boundary — a
// stage here is what a round is to a child — so the copy's base moves by
// exactly what landed and the lane's own work stays its own. Where a landing
// meets that work on the same lines and no rule settles a region, the session
// would hand the collision to an integration writer; here the lane's own run
// takes it, as a turn of its remediation step, and catchUp answers with the
// reconciliation for that turn (todoDriver.reconcile).
// See docs/capabilities/subagents.md#a-writer-starts-from-your-tree and
// docs/capabilities/todo.md#a-sprint-can-work-several-items-at-once.
//
// It answers too with the slugs whose landings it carried, which are the
// lane's copy having changed: a verdict reached before them is about another
// tree (State.VerifyAgain).
func (l *todoLane) catchUp() (string, []string, *todoReconcile) {
	if l == nil {
		return "", nil, nil
	}
	l.set.land.Lock()
	defer l.set.land.Unlock()
	return l.catchUpLocked()
}

// catchUpLocked is catchUp for a caller that holds the land lock. Nothing is
// carried while a reconciliation's turn is owed: the copy holds marks, and
// what landed since is carried at the boundary after the turn is judged.
func (l *todoLane) catchUpLocked() (string, []string, *todoReconcile) {
	if l.reconciling != nil {
		return "", nil, nil
	}
	var carried []string
	for l.seen < len(l.set.landings) {
		landed := l.set.landings[l.seen]
		err := l.wt.Reseed(landed.patch)
		var clash *worktree.ReseedCollision
		if errors.As(err, &clash) {
			// The landing meets work this lane has: a rule settles the two
			// shapes it can and the carry goes on, and anything else is a
			// turn of the lane's own.
			var r *todoReconcile
			if r, err = l.merging(landed); r != nil {
				l.seen++
				return "", carried, r
			}
		}
		if err != nil {
			return carryRefusal(landed.slug, err), carried, nil
		}
		carried = append(carried, landed.slug)
		l.seen++
	}
	return "", carried, nil
}

// merging carries a landing that meets this lane's work by merging it three
// ways (worktree.ReseedMerging). It answers with the reconciliation a turn
// owes where a region no rule settles is left, the copy holding the regions
// marked, and with the error where the landing cannot be carried at all,
// which is the refusal a plain carry gives.
func (l *todoLane) merging(landed todoLanding) (*todoReconcile, error) {
	rec, err := l.wt.ReseedMerging(landed.patch, landed.slug, l.slug)
	if err != nil {
		return nil, err
	}
	if len(rec.Unsettled) == 0 {
		return nil, rec.RegenFailed()
	}
	patch, err := worktree.WorktreePatch(rec.Dir())
	if err != nil {
		if back := rec.PutBack(); back != nil {
			return nil, fmt.Errorf("the copy could not be put back after a merge left %s marked: %w", strings.Join(rec.Unsettled, ", "), back)
		}
		return nil, err
	}
	return &todoReconcile{slug: landed.slug, rec: rec, at: l.seen, before: patchByFile(patch),
		findings: run.CollisionFindings(landed.slug, l.slug, rec.Unsettled, rec.Evidence)}, nil
}

// todoReconcile is a landing that met a lane's work in regions no rule
// settles, owed a turn of the lane's remediation step: the landing's slug,
// the merge that marked the copy, the landing's place in the set's list, the
// lane's patch file by file as the merge left it, and what the turn is told.
type todoReconcile struct {
	slug     string
	rec      *worktree.Reconciliation
	at       int
	before   map[string]string
	findings string
}

// reconcile is the one place a lane takes a reconciling turn: the run enters
// its remediation step for the landing (State.Reconcile), which blocks where
// the item's reconciliations are spent, and the lane keeps the
// reconciliation to judge the turn by (judged). A run that blocks here is put
// back at its end, so a kept copy never holds marks.
// See docs/capabilities/todo.md#a-sprint-can-work-several-items-at-once.
func (d *todoDriver) reconcile(st *run.State, it todo.Item, r *todoReconcile) run.Step {
	l := d.lane
	l.reconciling = r
	step := st.Reconcile(it, r.slug, r.findings)
	if step.Action != run.ActionBlocked {
		l.reconcileWords("reconciling " + r.slug + "'s landing")
	}
	return step
}

// judged answers for the turn a reconciliation was owed, once it has ended,
// with why the lane blocks where the turn did not reconcile: a file still
// holding a mark, one left as the merge wrote it, or one that is either side
// of the merge whole, which drops the other's change
// (worktree.Unreconciled, worktree.PickedSide). The copy is then put back to
// the lane's own patch on the base it had, both patches kept as a block
// keeps them. A reconciliation that holds is written on the row and in the
// log, with any file the turn changed outside the regions, and the landing's
// generated paths are regenerated over it. Nothing is owed outside a lane,
// or where no reconciliation was.
func (l *todoLane) judged(d *todoDriver) string {
	if l == nil || l.reconciling == nil {
		return ""
	}
	r := l.reconciling
	l.set.land.Lock()
	defer l.set.land.Unlock()
	patch, err := worktree.WorktreePatch(r.rec.Dir())
	if err != nil {
		return l.putBack("the reconciling turn's work could not be read: " + todoFirstProblem(err.Error()))
	}
	files := r.rec.Unsettled
	also := r.alsoChanged(patch)
	// Marks are looked for only where the turn wrote: a line of the lane's
	// own work elsewhere that quotes a conflict is not the turn's to answer.
	byFile := patchByFile(patch)
	var turned strings.Builder
	for _, p := range append(slices.Clone(files), also...) {
		turned.WriteString(byFile[p])
	}
	marked := worktree.Unreconciled(r.rec.Dir(), turned.String(), files, r.rec.Seeded)
	picked := worktree.PickedSide(r.rec.Dir(), r.rec.Sides)
	if len(marked) > 0 || len(picked) > 0 {
		return l.putBack(fmt.Sprintf("%s landed on the checkout over %s, which this lane changed too, and the turn given the regions did not reconcile them: %s",
			r.slug, strings.Join(files, ", "), r.refusals(marked, picked)))
	}
	words := "reconciled " + r.slug + "'s landing in " + strings.Join(files, ", ")
	if len(also) > 0 {
		words += "; also changed " + strings.Join(also, ", ")
	}
	if err := r.rec.Regenerate(context.Background()); err != nil {
		return l.putBack(carryRefusal(r.slug, err))
	}
	l.reconciling = nil
	l.reconcileWords(words)
	fmt.Fprintf(d.out, "… %s %s\n", l.slug, words)
	return ""
}

// refusals says, file by file, why a turn's work is not a reconciliation.
func (r *todoReconcile) refusals(marked, picked []string) string {
	var out []string
	said := map[string]bool{}
	for _, p := range marked {
		said[p] = true
		if worktree.ReadSeeded(r.rec.Dir(), p) == r.rec.Seeded[p] {
			out = append(out, p+" is as the merge left it")
		} else {
			out = append(out, p+" still holds a conflict mark")
		}
	}
	for _, p := range picked {
		if said[p] {
			continue
		}
		if worktree.ReadSeeded(r.rec.Dir(), p) == r.rec.Sides[p][0] {
			out = append(out, p+" is "+r.slug+"'s side whole, which drops this lane's change")
		} else {
			out = append(out, p+" is this lane's side whole, which drops "+r.slug+"'s change")
		}
	}
	return strings.Join(out, "; ") + "; the copy is put back to this lane's own work on the base it had, and " + r.slug + "'s stays on the checkout"
}

// alsoChanged is the files the turn changed outside the ones it was given:
// not refused, since they are verified and reviewed with the item, but named
// on the row so a widened edit is seen.
func (r *todoReconcile) alsoChanged(patch string) []string {
	given := map[string]bool{}
	for _, p := range r.rec.Unsettled {
		given[p] = true
	}
	after := patchByFile(patch)
	var out []string
	for p, text := range after {
		if !given[p] && r.before[p] != text {
			out = append(out, p)
		}
	}
	for p := range r.before {
		if _, still := after[p]; !still && !given[p] {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return out
}

// putBack puts the copy back to the lane's own patch on the base it had
// before the reconciliation's merge, and answers with why, which the run
// blocks on. The caller holds the land lock.
func (l *todoLane) putBack(why string) string {
	r := l.reconciling
	l.reconciling = nil
	l.seen = r.at
	l.reconcileWords("")
	if err := r.rec.PutBack(); err != nil {
		why += "; the copy could not be put back to this lane's own work, so it may still hold the marks: " + todoFirstProblem(err.Error())
	}
	return why
}

// patchByFile is a patch's text for each file it touches.
func patchByFile(patch string) map[string]string {
	out := map[string]string{}
	file := ""
	var b strings.Builder
	flush := func() {
		if file != "" {
			out[file] = b.String()
		}
		b.Reset()
	}
	for _, line := range strings.SplitAfter(patch, "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			flush()
			file = worktree.ParseGitDiffPath(strings.TrimRight(line, "\n"))
		}
		b.WriteString(line)
	}
	flush()
	return out
}

// reconcileWords writes on the lane's row what a reconciliation is doing, and
// clears it with none. The row is the checkpoint's.
func (l *todoLane) reconcileWords(words string) {
	if l == nil {
		return
	}
	l.set.mu.Lock()
	defer l.set.mu.Unlock()
	lane, ok := l.set.sp.Lane(l.slug)
	if !ok || lane.Reconcile == words {
		return
	}
	lane.Reconcile = words
	l.set.saveLocked()
}

// carryRefusal is the evidence a lane blocks on where a landing will not
// carry into its copy.
func carryRefusal(slug string, err error) string {
	var clash *worktree.ReseedCollision
	if errors.As(err, &clash) {
		return fmt.Sprintf("%s landed on the checkout (%s) and its patch does not carry into this lane's copy over %s, which this lane changed too: %s",
			slug, strings.Join(clash.Landed, ", "), strings.Join(clash.Files, ", "), clash.Reason)
	}
	return fmt.Sprintf("%s landed on the checkout and its patch could not be carried into this lane's copy: %s",
		slug, todoFirstProblem(err.Error()))
}

// again writes on the lane's row that it is verifying a tree the named
// landings changed, and clears it with no slugs. The row is the checkpoint's.
func (l *todoLane) again(slugs []string) {
	if l == nil {
		return
	}
	words := ""
	if len(slugs) > 0 {
		words = "verifying again · " + strings.Join(slugs, ", ") + " landed"
	}
	l.set.mu.Lock()
	defer l.set.mu.Unlock()
	if lane, ok := l.set.sp.Lane(l.slug); ok {
		lane.Again = words
	}
	l.set.saveLocked()
}

// land puts the lane's patch onto the checkout and adds what landed to the
// patches every other lane carries. The caller holds the land lock. A merge
// that leaves a conflict region lands nothing and is answered by
// integrateConflict.
//
// A verified lane lands plain: only where the checkout still holds what the
// copy's base holds for every file the patch touches. Everything other lanes
// landed was carried into the copy, so a checkout that differs was moved by a
// hand outside the sprint, and a merge would land a tree no gate ran over.
func (l *todoLane) land(plain bool) ([]string, error) {
	land := l.wt.LandPatch
	if plain {
		land = l.wt.LandPlain
	}
	patch, err := land()
	if err != nil {
		var conflict *worktree.MergeConflict
		if errors.As(err, &conflict) {
			return nil, l.integrateConflict(conflict)
		}
		var moved *worktree.CheckoutMoved
		if errors.As(err, &moved) {
			l.kept = true
			return nil, fmt.Errorf("the checkout moved outside the sprint in %s since this lane's copy was verified, so nothing was landed; this lane's work is kept in %s",
				strings.Join(moved.Files, ", "), l.wt.Root())
		}
		return nil, fmt.Errorf("the lane's patch would not apply onto the checkout: %s", todoFirstProblem(err.Error()))
	}
	files := worktree.PatchFiles(patch)
	if len(files) > 0 {
		l.landed = true
		// What landed is the patch the landing applied rather than the one
		// the lane wrote: a merge or a regenerated file lands something else,
		// and the other copies are owed what the checkout now holds.
		l.set.landings = append(l.set.landings, todoLanding{slug: l.slug, patch: patch})
	}
	// The lane's own landing is not one it has to carry.
	l.seen = len(l.set.landings)
	return files, nil
}

// integrateConflict answers a landing whose three-way merge left a conflict
// region. Reconciling two intentions over one file is a model's judgement
// under review, and never the merge's. A session hands it to an integration
// writer its supervisor starts and puts its patch to the person; a lane
// reconciles a carry in a turn of its own run (todoDriver.reconcile), but a
// landing that conflicts here comes after the run's last step, with no turn
// left to take, so the ending here is the session's when an integration
// cannot reconcile: both patches kept — the one that landed first on the
// checkout, this lane's in its copy — nothing committed with conflict
// markers, and the item blocked with the files named for the person to
// reconcile. This is the one place a lane's landing conflict is answered.
// See docs/capabilities/subagents.md#a-conflict-is-a-task-for-a-writer.
func (l *todoLane) integrateConflict(conflict *worktree.MergeConflict) error {
	l.kept = true
	return fmt.Errorf("the lane's patch conflicts with what landed on the checkout before it, in %s; nothing was written, the landed work stays on the checkout and this lane's is kept in %s",
		strings.Join(conflict.Files, ", "), l.wt.Root())
}

// laneCommit is a lane's commit step. The lane takes the land lock and holds
// it from the last look at the branch to the commit: it carries whatever
// landed since its last boundary, verifies again in its copy if anything
// carried, and lands only then. A failure in that verify is a fix round and
// not a commit, and a carry that left regions for a turn is a reconciliation
// and not a commit; the lock is let go for either, so a landing during the
// turn is carried at the next boundary. Holding the lock for one verify serialises
// landings, which landing them was already.
// See docs/capabilities/todo.md#a-sprint-can-work-several-items-at-once.
func (d *todoDriver) laneCommit(ctx context.Context, st *run.State, it todo.Item) run.Step {
	l := d.lane
	l.set.land.Lock()
	defer l.set.land.Unlock()
	why, carried, r := l.catchUpLocked()
	if why != "" {
		return st.Block(why)
	}
	// A collision no rule settles is a turn, and the lock is let go for it
	// as it is for a fix round: the run verifies, is reviewed and commits
	// again after it.
	if r != nil {
		return d.reconcile(st, it, r)
	}
	if step, redirected := d.verifyAfterCarry(ctx, st, it, carried, true); redirected {
		return step
	}
	files, err := l.landCommit(d, st)
	if err != nil {
		return st.Block("the commit could not be made: " + err.Error())
	}
	return st.Committed(files)
}

// verifyAfterCarry is the one place a lane answers a carry that changed its
// copy: the tree a passed verdict was about is not the tree the lane would
// review or land. Nothing failed, so no fix round is spent.
//
// At a step boundary (atCommit false) the run goes back to its verify step
// and the loop runs it, and redirected is whether there was a passed verdict
// to go back over. At the commit the lock is held and the verify runs here;
// redirected is true with the step to take instead of committing when it
// failed (a fix round) or could not run (a block), and false when it passed.
func (d *todoDriver) verifyAfterCarry(ctx context.Context, st *run.State, it todo.Item, carried []string, atCommit bool) (run.Step, bool) {
	if len(carried) == 0 {
		return run.Step{}, false
	}
	l := d.lane
	if !atCommit {
		step, ok := st.VerifyAgain()
		if ok {
			l.again(carried)
			fmt.Fprintf(d.out, "… %s verifying again · %s landed\n", l.slug, strings.Join(carried, ", "))
		}
		return step, ok
	}
	command, ok := st.VerifyCommand()
	if !ok {
		return run.Step{}, false
	}
	l.again(carried)
	fmt.Fprintf(d.out, "… %s verifying again · %s landed\n", l.slug, strings.Join(carried, ", "))
	st.Checked = false
	v := d.verify(ctx, st, command)
	l.again(nil)
	if v.output != "" {
		fmt.Fprintln(d.out, v.output)
	}
	switch {
	case v.blocked != "":
		return st.Block(v.blocked), true
	case !v.ok:
		return st.VerifyResult(it, false, v.output), true
	}
	return run.Step{}, false
}

// landCommit is a lane's commit: its patch applied to the checkout and
// committed there, while no other lane may write the branch. What the commit
// holds is what the patch touched, re-expressed from the checkout, and never
// the backlog (run.Committable). The caller holds the land lock.
func (l *todoLane) landCommit(d *todoDriver, st *run.State) ([]string, error) {
	files, err := l.land(true)
	if err != nil {
		return nil, err
	}
	committed, err := run.Commit(d.root, l.set.fromTop(files), st.Message,
		"--no-commit runs an item without one, or todo.commit = false makes that the default",
		projectTrust().RunsOwnPrograms(), run.Secrets{Ignore: d.secretIgnore, Trailers: d.trailers})
	if err != nil {
		return nil, fmt.Errorf("%w — the lane's patch is on the checkout, uncommitted", err)
	}
	fmt.Fprintf(d.out, "lane %s landed %s\n", l.slug, countOf(len(committed), "file", "files"))
	return committed, nil
}

// fromTop is a patch's repository-relative paths as the checkout names them.
func (s *todoLanes) fromTop(files []string) []string {
	root := s.root
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	var out []string
	for _, f := range files {
		rel, err := filepath.Rel(root, filepath.Join(s.top, filepath.FromSlash(f)))
		if err != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		if rel = run.Committable(rel); rel != "" {
			out = append(out, rel)
		}
	}
	return out
}

// todoRepoTop is the repository's toplevel with its links resolved, which is
// how git names it and so how a patch's paths are anchored.
func todoRepoTop(root string) string {
	top, code := run.Git(root, "rev-parse", "--show-toplevel")
	if code != 0 {
		return root
	}
	if resolved, err := filepath.EvalSymlinks(top); err == nil {
		return resolved
	}
	return top
}

// end is what a lane does where its item's run is over, and reports that it
// has finished the item itself. A stop leaves the item open with its copy
// kept — the steps already done are in the copy, and the stop was aimed at
// the loop. A run that ended done without a commit lands its patch
// uncommitted. Either way the archive and the close of the sprint file are
// the backlog's writes and happen one lane at a time.
func (l *todoLane) end(ctx context.Context, d *todoDriver, st *run.State, it todo.Item) bool {
	if l == nil {
		return false
	}
	// A reconciliation whose turn never passed its judge leaves the copy
	// holding marks; it is put back before anything reads what is kept.
	if l.reconciling != nil {
		l.set.land.Lock()
		if why := l.putBack(""); why != "" {
			fmt.Fprintln(d.out, "lane "+l.slug+":"+strings.TrimPrefix(why, ";"))
		}
		l.set.land.Unlock()
	}
	if ctx.Err() != nil && st.Stage != run.StageDone {
		_ = todo.SetStatus(it.Path, todo.StatusOpen)
		run.Discard(d.root, it.Slug)
		run.ClearSpool(d.root, it.Slug)
		l.kept = len(run.DirtyPaths(d.tree)) > 0
		line := fmt.Sprintf("⊘ todo run %s stopped at %s; the item is open again", it.Slug, st.Stage)
		if l.kept {
			line += " and its work so far is kept in " + d.tree
		}
		fmt.Fprintln(d.out, line)
		d.settleChats(st)
		st.Stage, st.Blocked = run.StageBlocked, "the sprint was stopped"
		return true
	}
	if st.Stage == run.StageDone && !l.landed {
		l.set.land.Lock()
		files, err := l.land(false)
		l.set.land.Unlock()
		if err != nil {
			st.Block(err.Error())
		} else if len(files) > 0 {
			fmt.Fprintf(d.out, "lane %s landed %s, uncommitted\n", l.slug, countOf(len(files), "file", "files"))
		}
	}
	if st.Stage == run.StageBlocked && len(run.DirtyPaths(d.tree)) > 0 {
		l.kept = true
		fmt.Fprintf(d.out, "the lane's work so far is kept in %s; `git worktree list` names it\n", d.tree)
	}
	l.set.land.Lock()
	d.finish(st, it, nil)
	l.set.land.Unlock()
	d.settleChats(st)
	return true
}

// admin holds the lock worktree administration takes, for a step inside a
// lane that makes, lands or removes copies of its own (a fan-out), and
// answers with the release. Outside a lane there is nothing to hold.
func (l *todoLane) admin() func() {
	if l == nil {
		return func() {}
	}
	l.set.land.Lock()
	return l.set.land.Unlock
}

// spendFigure is what the set has spent with this lane's item added, for the
// notes of a sprint file its archive closes.
func (l *todoLane) spendFigure(itemCost float64) string {
	l.set.mu.Lock()
	defer l.set.mu.Unlock()
	return run.SpendFigure(l.set.sp.Cost+itemCost, l.set.sp.CapCents)
}

// todoRunnerBin is the executable a session starts a parallel sprint as. It
// is a variable so a test can start something harmless in its place.
var todoRunnerBin = os.Executable

// todoParallelStarter is how a session starts a parallel sprint: as this
// runner, in the checkout, in a process of its own. The lanes are processes
// already, and a sprint that outlives the session that asked for it is the
// same sprint `shhh todo run --all --parallel N` would have been — with its
// lines in a log beside the checkpoint, since there is no terminal for them.
// It answers with where the log is.
func todoParallelStarter(root string) func(args []string) (string, error) {
	return func(args []string) (string, error) {
		bin, err := todoRunnerBin()
		if err != nil {
			return "", fmt.Errorf("cannot find the shhh binary a sprint runs as: %w", err)
		}
		if err := os.MkdirAll(run.Dir(root), 0o755); err != nil {
			return "", err
		}
		log := filepath.Join(run.Dir(root), "sprint.log")
		f, err := os.OpenFile(log, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return "", err
		}
		cmd := exec.Command(bin, append([]string{"todo", "run"}, args...)...)
		cmd.Dir = root
		cmd.Stdout, cmd.Stderr = f, f
		if err := cmd.Start(); err != nil {
			f.Close()
			return "", err
		}
		go func() {
			_ = cmd.Wait()
			f.Close()
		}()
		return log, nil
	}
}
