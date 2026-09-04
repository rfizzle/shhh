package cli

// `shhh todo run` is the backlog runner with nobody in front of it: the same
// state machine the session drives, driven from a command instead, one item
// at a time and one session per item.
//
// The session is a process. Every stage a run takes is a turn, and a turn
// here is one `shhh code --print` in the checkout — the shape the eval runner
// already uses, for the same reason: what a stage produced is read out of the
// transcript whatever the process's exit status, because a stage that ran out
// of rounds still did work and the machine judges it on its answer. Nothing
// carries between two stages except the checkpoint, which is what the
// checkpoint has always been for: every stage prompt states the item, the
// plan and the findings it needs, so a stage is startable from nothing.
//
// The two things a model must not do, it does not do here either. shhh runs
// the verification and shhh makes the commit.
// See docs/capabilities/todo.md#a-sprint-is-runs-with-a-session-between-them.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/runner"
	"github.com/rfizzle/shhh/internal/todo"
	"github.com/rfizzle/shhh/internal/todo/run"
	"github.com/spf13/cobra"
)

// exitBlocked is the one code the backlog runner adds to the closed set an
// unattended run leaves behind. It is the runner's own terminal state and not
// a second reading of a turn: an item that blocked was worked as far as it
// could go and stopped with evidence written on it, which is a different fact
// from every code above and the only one they cannot express.
// See docs/capabilities/headless.md#the-exit-code-is-the-contract.
const exitBlocked = 7

// todoVerifyTimeout bounds the whole verify stage — every test the item lists
// plus the project's checks.
const todoVerifyTimeout = 15 * time.Minute

// todoRunFlags are the answers the command was given.
type todoRunFlags struct {
	all      bool
	next     bool
	noCommit bool
	max      int
}

func newTodoRunCmd() *cobra.Command {
	var flags todoRunFlags
	cmd := &cobra.Command{
		Use:   "run [<slug>]",
		Short: "Work a backlog item, or the whole ready list, with nobody watching",
		Long: "Work one backlog item through research, implement, verify, review and commit, " +
			"in a session of its own. With --all, work the ready list one item at a time — the " +
			"sprint file's set where the backlog holds one — stopping when nothing is ready, when " +
			"--max is reached, or on the first item that blocks.",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: todoSlugs,
		RunE: func(cmd *cobra.Command, args []string) error {
			slug := ""
			if len(args) == 1 {
				slug = args[0]
			}
			return todoRunHeadless(cmd, slug, flags)
		},
	}
	cmd.Flags().BoolVar(&flags.all, "all", false, "work the whole ready list, one item per session, stopping on the first block")
	cmd.Flags().BoolVar(&flags.next, "next", false, "work the next ready item")
	cmd.Flags().BoolVar(&flags.noCommit, "no-commit", false, "end each run after the review, leaving the change in the working tree")
	cmd.Flags().IntVar(&flags.max, "max", 0, "with --all, how many items the sprint may start (0 for as many as are ready)")
	return cmd
}

// todoRunHeadless answers the command: it resolves what to work, builds the
// driver, and turns the run's terminal state into the process's status.
func todoRunHeadless(cmd *cobra.Command, slug string, flags todoRunFlags) error {
	switch {
	case flags.all && slug != "":
		return fmt.Errorf("--all works the ready list, so it does not take an item as well")
	case flags.all && flags.next:
		return fmt.Errorf("--all and --next are two different requests; ask for one")
	case flags.next && slug != "":
		return fmt.Errorf("--next is the next ready item, so it does not take one as well")
	case flags.max > 0 && !flags.all:
		return fmt.Errorf("--max bounds how many items a sprint works, so it needs --all")
	case flags.max < 0:
		return fmt.Errorf("--max %d: a sprint works whole items", flags.max)
	}
	cfg := ConfigFrom(cmd.Context())
	d, err := newTodoDriver(cmd.OutOrStdout(), todo.Root(todoCwd()), cfg, flags.noCommit)
	if err != nil {
		return err
	}
	if !d.noCommit && !d.repo {
		return fmt.Errorf("%s is not in a git repository and a run ends in a commit — --no-commit runs it without one, or todo.commit = false makes that the default", d.root)
	}
	if flags.all {
		return exitOf(d.sprint(cmd.Context(), flags.max))
	}
	store := todo.Load(d.root)
	it, err := todoRunTarget(store, slug)
	if err != nil {
		return err
	}
	return exitOf(d.work(cmd.Context(), it, nil).Stage == run.StageBlocked)
}

// exitOf turns a blocked run into the status the process leaves behind. A
// block is not an error in the way a bad flag is — the run did what it could
// and wrote down why it stopped — so it carries no message of its own beyond
// the report already printed.
func exitOf(blocked bool) error {
	if !blocked {
		return nil
	}
	return exitError{code: exitBlocked, err: errTodoRunBlocked}
}

// errTodoRunBlocked is the exit-7 run stated in words, for the stderr line a
// non-zero status is dressed with.
var errTodoRunBlocked = errors.New("the run stopped with the evidence written on the item; `shhh todo` lists it")

// todoRunTarget is the item a run without --all works: the one named, or the
// next ready one.
func todoRunTarget(s *todo.Store, slug string) (todo.Item, error) {
	if slug == "" {
		it, ok := s.Next()
		if !ok {
			return todo.Item{}, fmt.Errorf("nothing is ready: every open item waits on another, or the backlog is empty")
		}
		return it, nil
	}
	it, ok := s.Find(slug)
	if !ok || it.Archived {
		return todo.Item{}, fmt.Errorf("no active backlog item %q; `shhh todo` lists them", slug)
	}
	if waiting := s.Waiting(it); len(waiting) > 0 {
		return todo.Item{}, fmt.Errorf("%s waits on %s; run those first, or take the dependency out of the file", it.Slug, strings.Join(waiting, ", "))
	}
	if it.Status == todo.StatusBlocked {
		return todo.Item{}, fmt.Errorf("%s is blocked; `shhh todo` shows the evidence, and /todo open %s reopens it", it.Slug, it.Slug)
	}
	return it, nil
}

// todoDriver carries out the steps the machine hands back.
type todoDriver struct {
	root string
	// bin is this executable, which every stage's turn is one process of.
	bin     string
	out     io.Writer
	session string
	// gate runs the project's checks at the verify stage. Nil where the
	// checkout is untrusted or names no suites, which the verify stage says
	// out loud rather than passing silently.
	gate *quality.Runner
	// closeGate reports that the workspace names an on-close suite, so a
	// stage's own process checks the tree as it closes and the verify stage
	// can take that verdict instead of running the same suite again.
	closeGate   bool
	itemTimeout time.Duration
	noCommit    bool
	repo        bool
	// dirty is what the tree already held when the item now being worked
	// started. Only what changed after that is the run's to stage, which is
	// the same rule the session applies with its changeset: a file somebody
	// left modified is not the run's work and must not ride along in its
	// commit. It is retaken per item, not once per process — a sprint asked
	// for without commits leaves each item's work in the tree, and a
	// baseline from before the sprint would hand every one of those files to
	// the next item as its own.
	dirty map[string]bool
	// turn spends one stage as one session and answers with what the model
	// said, the status the process left and why there was no answer. It is a
	// field because the loop around it is the part worth testing and a test
	// that had to stand up a provider to reach it would test neither.
	turn func(ctx context.Context, deadline time.Time, step run.Step) (text string, code int, err error)
}

func newTodoDriver(out io.Writer, root string, cfg config.Config, noCommit bool) (*todoDriver, error) {
	bin, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("cannot find the shhh binary a stage runs as: %w", err)
	}
	d := &todoDriver{
		root: root, bin: bin, out: out,
		session:     "todo-run-" + time.Now().UTC().Format("20060102-150405"),
		itemTimeout: cfg.TodoItemTimeout(),
		noCommit:    noCommit || !cfg.TodoCommitEnabled(),
		repo:        project.InRepo(root),
	}
	// The suites are command text out of a file that arrived with the clone
	// and the runner spends no approval on them, so an untrusted checkout
	// gets no gate at all rather than one that refuses when it is reached.
	if projectTrust().Allows() {
		d.gate = &quality.Runner{Workspace: root}
		_, _, d.closeGate = onCloseGate(d.gate)
	}
	d.turn = d.ask
	return d, nil
}

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
		fmt.Fprintln(d.out, "continuing the sprint from its checkpoint — "+sp.Summary())
	default:
		sp = run.StartSprint(d.session, "", max, d.noCommit)
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
		if st.Stage == run.StageBlocked {
			sp.Blocks(it.Slug, st.Blocked)
			break
		}
		sp.Finished(it.Slug)
	}
	run.DiscardSprint(d.root)
	fmt.Fprintln(d.out, todoSprintEnding(sp))
	return sp.Ended == run.SprintBlocked
}

// sprintItem is the item the sprint works next: the one it was interrupted
// on, or the next ready one.
func (d *todoDriver) sprintItem(sp *run.Sprint) (todo.Item, bool) {
	store := todo.Load(d.root)
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
	if sp.Ended == run.SprintBlocked {
		line += "\nnothing further was attempted: a sprint stops on the first block, because what comes next may rest on the work that did not land"
	}
	return line
}

// work runs one item to its end and answers with the state it stopped in.
// sp is the sprint driving it, or nil for a single item asked for by name.
func (d *todoDriver) work(ctx context.Context, it todo.Item, sp *run.Sprint) *run.State {
	d.dirty = todoDirtyPaths(d.root)
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
		d.say(st, step)
		if st.Over() {
			break
		}
		if !deadline.IsZero() && !time.Now().Before(deadline) {
			step = st.Block(run.TimedOut(d.itemTimeout))
			continue
		}
		step = d.carry(ctx, deadline, st, it, step)
	}
	d.finish(st, it)
	return st
}

// begin starts the item, or picks up the checkpoint an earlier process left.
// The stage the checkpoint names starts over, because the conversation that
// was part-way through it belonged to a process that is gone and a stage is
// the smallest thing the machine can judge.
func (d *todoDriver) begin(it todo.Item, inSprint bool) (*run.State, run.Step) {
	opt := run.Options{
		NoCommit: d.noCommit, Repo: d.repo, Sprint: d.sprintGoal(),
		CloseGate: d.closeGate, InSprint: inSprint,
	}
	if it.Status == todo.StatusInProgress {
		if st, err := run.Load(d.root, it.Slug); err == nil && !st.Over() {
			st.Session, st.Reviewer = d.session, ""
			st.NoCommit, st.Repo, st.Sprint = opt.NoCommit, opt.Repo, opt.Sprint
			st.CloseGate, st.InSprint = opt.CloseGate, opt.InSprint
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
	return st, st.First(it, "")
}

// carry does one step and answers with the next.
func (d *todoDriver) carry(ctx context.Context, deadline time.Time, st *run.State, it todo.Item, step run.Step) run.Step {
	switch step.Action {
	case run.ActionPrompt:
		text, code, err := d.turn(ctx, deadline, step)
		if err != nil {
			return st.Block(err.Error())
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
			st.Checks(code == exitDone)
		}
		return st.Observe(it, text)
	case run.ActionVerify:
		ok, output := d.verify(ctx, st)
		fmt.Fprintln(d.out, output)
		return st.VerifyResult(it, ok, output)
	case run.ActionPause:
		// The pause is the one gate that asks a person, and there is nobody
		// here to ask. Guessing the answer is the one thing a deterministic
		// runner must not do, so the item stops with the questions on it.
		return st.Block("the run reached a decision and there is nobody to ask — " + st.Paused)
	case run.ActionReview:
		// An implement stage that left the tree exactly as it found it has
		// produced nothing to review and nothing to commit, and another round
		// over the same plan would produce the same nothing.
		if len(st.Paths) == 0 {
			return st.Block("the run changed no files under the repository, so there is nothing to review")
		}
		// A reviewer child is a spawn, and an unattended run has no
		// supervisor to spawn one from. The session already degrades this way
		// when there is none, and the step says so.
		return st.SelfReview(it)
	case run.ActionFanOut:
		return st.NoLanes(it, "no agent supervisor outside a session; building the plan whole")
	case run.ActionCommit:
		files, err := d.commit(st)
		if err != nil {
			return st.Block("the commit could not be made: " + err.Error())
		}
		return st.Committed(files)
	case run.ActionWait:
		return st.Block("the run is waiting on a child, and an unattended run has none")
	}
	// Every action the machine has is answered above. Handing the same step
	// back would be an unattended process spinning on it forever, which is
	// the one failure here nobody would be watching for.
	return st.Block("the run reached a step this runner has no answer for: " + step.Name())
}

// say prints one transition, in the words the stage gave it.
func (d *todoDriver) say(st *run.State, step run.Step) {
	if step.Shown != "" {
		fmt.Fprintln(d.out, step.Shown)
		return
	}
	fmt.Fprintf(d.out, "▸ todo run %s · %s\n", st.Slug, step.Name())
}

// ask spends one stage as one session: a `shhh code --print` in the checkout,
// with the stage's prompt and the permissions its mode asks for. The working
// stages auto-approve, because there is nobody to approve for them; the
// reading stages do not, which is what keeps a review from editing the tree
// it is reviewing.
//
// The answer is read out of the transcript whatever the process's status. A
// stage that ran out of rounds or lost the provider still produced whatever
// it produced, and the machine judges a stage on its answer — an empty one is
// what blocks the item, not a non-zero status.
func (d *todoDriver) ask(ctx context.Context, deadline time.Time, step run.Step) (text string, code int, err error) {
	args := []string{"code", "--print", "--output", "json"}
	if step.Mode == run.ModeAuto {
		args = append(args, "--yes")
	}
	args = append(args, step.Prompt)
	if !deadline.IsZero() {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, deadline)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, d.bin, args...)
	cmd.Dir = d.root
	cmd.Env = runner.Environ()
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
	code = exitDone
	var ee *exec.ExitError
	if errors.As(runErr, &ee) {
		code = ee.ExitCode()
	}
	var t struct {
		Final string `json:"final"`
		Error string `json:"error"`
	}
	_ = json.Unmarshal([]byte(out.String()), &t)
	if strings.TrimSpace(t.Final) != "" {
		return t.Final, code, nil
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "", code, errors.New(run.TimedOut(d.itemTimeout))
	}
	return "", code, fmt.Errorf("the %s turn produced no answer (exit %d): %s",
		step.Stage, code, todoFirstProblem(t.Error, errOut.String(), errString(runErr)))
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

// verify runs the item's listed tests, then the project's checks. The tests
// are the ones the item held when the run started, before any stage could
// have edited the file: the run tells the model to tick the item's boxes as
// it works, and a command it wrote there is not one shhh runs unasked.
func (d *todoDriver) verify(ctx context.Context, st *run.State) (bool, string) {
	ctx, cancel := context.WithTimeout(ctx, todoVerifyTimeout)
	defer cancel()
	var b strings.Builder
	ok := true
	for _, cmd := range st.Tests {
		out, code := runner.RunCaptureIn(ctx, d.root, cmd)
		fmt.Fprintf(&b, "$ %s → exit %d\n%s\n", cmd, code, todoTail(out, 40))
		if code != 0 {
			ok = false
		}
	}
	switch {
	case st.Checked:
		b.WriteString("quality gate: passed as the implement turn closed\n")
	case d.gate != nil:
		res, err := d.gate.Run(ctx, "")
		switch {
		case err != nil:
			fmt.Fprintf(&b, "quality gate: %v\n", err)
			ok = false
		case res.Verdict != quality.VerdictPass:
			b.WriteString(res.Format(quality.TakeFingerprint(d.root)) + "\n")
			ok = false
		default:
			fmt.Fprintf(&b, "quality gate %q: pass\n", res.Suite)
		}
	}
	if len(st.Tests) == 0 && d.gate == nil {
		b.WriteString("nothing to verify: the item lists no tests and the project has no quality gate\n")
	}
	return ok, strings.TrimRight(b.String(), "\n")
}

func todoTail(s string, lines int) string {
	parts := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(parts) > lines {
		parts = append([]string{fmt.Sprintf("… %d lines above", len(parts)-lines)}, parts[len(parts)-lines:]...)
	}
	return strings.Join(parts, "\n")
}

// commit stages the run's paths by name and commits with the message the
// commit stage wrote. It refuses a tree that already has staged changes it
// did not make: a commit carrying a stranger cannot be reverted, cited or
// read as a unit.
func (d *todoDriver) commit(st *run.State) ([]string, error) {
	paths := st.Paths
	if len(paths) == 0 {
		return nil, fmt.Errorf("the run changed no files under the repository")
	}
	// `--quiet` exits 1 for a staged difference, and that is the only exit
	// this may read as one: git's code for a missing repository moved
	// between versions, so whether there is one is read off the filesystem
	// instead, where the answer does not move.
	if out, code := todoGit(d.root, "diff", "--cached", "--quiet"); code != 0 {
		switch {
		case code == 1:
			return nil, fmt.Errorf("the index already holds staged changes this run did not make; commit or unstage them first\n%s", out)
		case !project.InRepo(d.root):
			return nil, fmt.Errorf("%s is not a git repository, so there is nothing to commit into; --no-commit runs an item without one", d.root)
		default:
			return nil, fmt.Errorf("git diff --cached exited %d: %s", code, out)
		}
	}
	if out, code := todoGit(d.root, append([]string{"add", "--"}, paths...)...); code != 0 {
		return nil, fmt.Errorf("git add: %s", out)
	}
	f, err := os.CreateTemp("", "shhh-todo-commit-*.txt")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err := f.WriteString(st.Message + "\n"); err != nil {
		f.Close()
		return nil, err
	}
	f.Close()
	if out, code := todoGit(d.root, "commit", "-F", f.Name()); code != 0 {
		return nil, fmt.Errorf("git commit: %s", out)
	}
	return paths, nil
}

// paths is what the run may stage: everything under the root that changed
// after the driver started, plus what an earlier process of the same run
// recorded, and never a backlog file — the backlog is never committed on the
// project's behalf.
// See docs/capabilities/todo.md#where-the-backlog-lives.
func (d *todoDriver) paths(st *run.State) []string {
	seen := map[string]bool{}
	var out []string
	for _, rel := range st.Paths {
		if !seen[rel] {
			seen[rel], out = true, append(out, rel)
		}
	}
	// Sorted, because what comes out of the status is a set: an unordered
	// commit list would put the report's own paths in a different order
	// every time the same run was replayed, and a report that changes
	// between two identical runs is one nothing can be compared against.
	var found []string
	for rel := range todoDirtyPaths(d.root) {
		if d.dirty[rel] || seen[rel] {
			continue
		}
		seen[rel] = true
		found = append(found, rel)
	}
	sort.Strings(found)
	return append(out, found...)
}

// todoDirtyPaths is what git reports as changed under root, as a set. A
// checkout git cannot read is an empty set, which stops a commit rather than
// staging a guess.
func todoDirtyPaths(root string) map[string]bool {
	status, code := todoGitLines(root, "status", "--porcelain", "--untracked-files=all")
	if code != 0 {
		return map[string]bool{}
	}
	return todoPorcelainPaths(status)
}

// todoPorcelainPaths reads `git status --porcelain` into the set of paths it
// names, leaving the backlog out: whether the backlog is committed is the
// project's call and nothing here stages it.
// See docs/capabilities/todo.md#where-the-backlog-lives.
func todoPorcelainPaths(status string) map[string]bool {
	out := map[string]bool{}
	backlog := filepath.Join(todo.StateDir, todo.Subdir)
	for _, line := range strings.Split(status, "\n") {
		if len(line) < 4 {
			continue
		}
		rel := strings.TrimSpace(line[3:])
		// A rename is reported as `orig -> now`; what the run holds is where
		// the content is now, and the index takes the old path with it.
		if i := strings.Index(rel, " -> "); i >= 0 {
			rel = rel[i+4:]
		}
		rel = strings.Trim(rel, `"`)
		if rel == "" || strings.HasPrefix(rel, backlog) {
			continue
		}
		out[rel] = true
	}
	return out
}

// todoGitNotInstalled is the shell's own code for a command that never
// started, which is what this reports for a git that is not there.
const todoGitNotInstalled = 127

// todoGit runs one git command in root and reports its output and its code.
func todoGit(root string, args ...string) (string, int) {
	out, code := todoGitLines(root, args...)
	return strings.TrimSpace(out), code
}

// todoGitLines is that without the trim, for a command whose output is read
// by column. `git status --porcelain` states a path's staged mark in the
// first column and its unstaged mark in the second, so a line about a file
// changed in the tree and not in the index begins with a space — and a
// trimmed line puts the path three characters to the left of where every
// reader of that format looks for it.
func todoGitLines(root string, args ...string) (string, int) {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Env = runner.Environ()
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else {
			code = todoGitNotInstalled
			out = append(out, err.Error()...)
		}
	}
	return string(out), code
}

// finish writes what the run ended as onto the item: the archive and the
// report for one that is done, the evidence for one that blocked. Either way
// the checkpoint goes, because a run that ended has nothing to continue.
func (d *todoDriver) finish(st *run.State, it todo.Item) {
	if st.Stage == run.StageDone {
		report := st.Report
		if len(st.Files) > 0 && !st.NoCommit {
			report += "\nCommitted: " + strings.Join(st.Files, ", ") + "\n"
		}
		to, err := todo.Archive(d.root, st.Slug, report)
		if err != nil {
			// The work is finished and the item must not stay in progress
			// with its report nowhere: it goes back to open with the report
			// on it, and the line says what is left to do.
			_ = todo.SetStatus(it.Path, todo.StatusOpen)
			_ = todo.Append(it.Path, report)
			fmt.Fprintf(d.out, "✓ todo run %s finished, but the item could not be archived — %v. The report is on the item and it is open.\n", st.Slug, err)
		} else {
			fmt.Fprintln(d.out, todoRunDoneLine(st, to))
			if closed, err := todo.CloseSprintIfDone(d.root); err == nil && closed != "" {
				fmt.Fprintln(d.out, "sprint file closed → "+closed)
			}
		}
		run.Discard(d.root, st.Slug)
		return
	}
	_ = todo.SetStatus(it.Path, todo.StatusBlocked)
	_ = todo.Append(it.Path, fmt.Sprintf("## Blocked\n%s\n\n_run in session %s, stage %s, %s_",
		st.Blocked, st.Session, st.Stage, time.Now().Format("2006-01-02 15:04")))
	fmt.Fprintf(d.out, "✗ todo run %s blocked — %s\n", st.Slug, st.Blocked)
	if paths := st.Paths; len(paths) > 0 {
		fmt.Fprintln(d.out, "work so far stays in the tree, uncommitted: "+strings.Join(paths, ", "))
	}
	run.Discard(d.root, st.Slug)
}

// todoRunDoneLine is what a finished item says: what happened to the work,
// and where the item went. A run that made no commit says so rather than
// saying nothing about it — "done" beside an uncommitted tree reads as a
// commit that was made, and the reader's next act is to go looking for one.
func todoRunDoneLine(st *run.State, to string) string {
	files := countOf(len(st.Files), "file", "files")
	if st.NoCommit {
		return fmt.Sprintf("✓ todo run %s done — not committed; %s in the working tree, and the item is archived to %s", st.Slug, files, to)
	}
	return fmt.Sprintf("✓ todo run %s done — committed %s and archived the item to %s", st.Slug, files, to)
}

// sprintGoal is the open sprint's goal, which rides in every item's research
// prompt so an item knows what the set it belongs to is for.
func (d *todoDriver) sprintGoal() string { return todo.Load(d.root).Sprint.Purpose() }
