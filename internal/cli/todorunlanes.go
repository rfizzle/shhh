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
// The landing is serial and in finish order. A lane that reaches its commit
// puts its patch onto the checkout and commits it there under one lock, so
// two lanes never write the branch at once; every other lane, told the
// branch moved, rebases its own copy onto the new commit before its next
// step, and a rebase that conflicts blocks that lane's item with git's words
// as its evidence and frees the lane. The sprint goes on with the rest.
// See docs/capabilities/todo.md#a-sprint-can-work-several-items-at-once.

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/subagent"
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

	// land is held by everything that writes the branch or copies it: a
	// landing, a lane catching up with one, and a copy being made or torn
	// down. Worktree administration is serial because git's own is not safe
	// run concurrently in one repository, and a copy made mid-landing would
	// be seeded with a patch that is about to become a commit.
	land sync.Mutex
	// moved is how many commits have landed on the branch this sprint.
	moved int
}

// todoLane is one lane: the item, the copy it is worked in, and where that
// copy stands against the branch.
type todoLane struct {
	set  *todoLanes
	slug string
	wt   *subagent.Worktree
	// base is the branch commit the copy's own history was made from, and
	// seen how many landings it has caught up with.
	base string
	seen int
	// landed reports the lane's patch on the checkout; kept that the copy
	// is left standing for a person to read, because what is in it did not
	// land.
	landed, kept bool
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
	base, _ := todoGit(d.root, "rev-parse", "HEAD")
	// Seeded from the checkout as it stands, which is what a writer child
	// starts from too: the lane's patch is then measured against the
	// checkout's own text rather than the last commit's.
	// See docs/capabilities/subagents.md#a-writer-starts-from-your-tree.
	wt, err := subagent.NewWorktree(d.root, nil)
	seen := set.moved
	set.land.Unlock()
	if err != nil {
		why := "no copy of the checkout could be made for the item's lane: " + todoFirstProblem(err.Error())
		_ = todo.SetStatus(it.Path, todo.StatusBlocked)
		_ = todo.Append(it.Path, "## Blocked\n"+why)
		fmt.Fprintf(d.out, "✗ todo run %s blocked — %s\n", it.Slug, why)
		return todoLaneResult{slug: it.Slug, why: why}
	}
	lane := &todoLane{set: set, slug: it.Slug, wt: wt, base: base, seen: seen}
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
	// last.
	if d.gate != nil {
		c.gate = &quality.Runner{Workspace: c.tree}
	}
	return &c
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

// catchUp rebases the lane's copy onto the branch where a landing has moved
// it since the lane last looked, and answers with the evidence to block on
// where the rebase conflicts. The lane's own work is parked as a commit for
// the length of the rebase and put back as uncommitted work afterwards, which
// is the shape the rest of the run reads it in.
func (l *todoLane) catchUp() string {
	if l == nil {
		return ""
	}
	l.set.land.Lock()
	defer l.set.land.Unlock()
	if l.seen == l.set.moved {
		return ""
	}
	head, code := todoGit(l.set.root, "rev-parse", "HEAD")
	if code != 0 {
		return "the branch moved under the lane and its new head could not be read: " + head
	}
	if err := todoRebase(l.wt.Root(), l.base, head); err != nil {
		return "the branch moved under the lane when another item landed, and the lane's work does not rebase onto it: " + err.Error()
	}
	l.base, l.seen = head, l.set.moved
	return ""
}

// todoLaneIdentity is who the commits a lane makes in its own copy are made
// by. They are dangling in a directory that is about to be thrown away, so
// the identity is forced — a machine with none configured would otherwise
// refuse a commit nobody will read — and hooks and signing are skipped for
// the same reason a writer's seed commit skips them.
var todoLaneIdentity = []string{"-c", "user.name=shhh", "-c", "user.email=shhh@localhost", "-c", "commit.gpgSign=false"}

// todoRebase moves a lane's copy from base onto head, carrying the lane's
// uncommitted work across. A conflict is aborted and the work put back as it
// was, and git's own words are the answer.
func todoRebase(dir, base, head string) error {
	git := func(args ...string) (string, int) {
		return todoGit(dir, append(append([]string{}, todoLaneIdentity...), args...)...)
	}
	if out, code := git("add", "-A"); code != 0 {
		return fmt.Errorf("git add: %s", out)
	}
	parked := false
	if _, code := git("diff", "--cached", "--quiet"); code == 1 {
		if out, code := git("commit", "--quiet", "--no-verify", "-m", "lane work in progress"); code != 0 {
			return fmt.Errorf("git commit: %s", out)
		}
		parked = true
	}
	unpark := func() {
		if parked {
			_, _ = git("reset", "--quiet", "HEAD~1")
		}
	}
	if out, code := git("rebase", "--quiet", "--no-verify", "--onto", head, base); code != 0 {
		_, _ = git("rebase", "--abort")
		unpark()
		return fmt.Errorf("%s", todoFirstProblem(out))
	}
	unpark()
	return nil
}

// landCommit is a lane's commit: its patch applied to the checkout and
// committed there, while no other lane may write the branch. What the commit
// holds is what the patch touched, re-expressed from the checkout, and never
// the backlog (run.Committable).
func (l *todoLane) landCommit(d *todoDriver, st *run.State) ([]string, error) {
	l.set.land.Lock()
	defer l.set.land.Unlock()
	files, err := l.wt.Land()
	if err != nil {
		return nil, fmt.Errorf("the lane's patch would not apply onto the checkout: %s", todoFirstProblem(err.Error()))
	}
	l.landed = true
	committed, err := run.Commit(d.root, l.set.fromTop(files), st.Message,
		"--no-commit runs an item without one, or todo.commit = false makes that the default",
		projectTrust().RunsOwnPrograms())
	if err != nil {
		return nil, fmt.Errorf("%w — the lane's patch is on the checkout, uncommitted", err)
	}
	l.set.moved++
	l.seen = l.set.moved
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
	top, code := todoGit(root, "rev-parse", "--show-toplevel")
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
	if ctx.Err() != nil && st.Stage != run.StageDone {
		_ = todo.SetStatus(it.Path, todo.StatusOpen)
		run.Discard(d.root, it.Slug)
		run.ClearSpool(d.root, it.Slug)
		l.kept = len(run.DirtyPaths(d.tree)) > 0
		line := fmt.Sprintf("■ todo run %s stopped at %s; the item is open again", it.Slug, st.Stage)
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
		files, err := l.wt.Land()
		l.set.land.Unlock()
		if err != nil {
			st.Block("the lane's patch would not apply onto the checkout: " + todoFirstProblem(err.Error()))
		} else if len(files) > 0 {
			l.landed = true
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
