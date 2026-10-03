package cli

// `shhh todo run` is the backlog runner with nobody in front of it: the same
// state machine the session drives, driven from a command instead, one item
// at a time and one session per item.
//
// The session is a process. Every step that spends a turn spends it as one
// print run in the checkout — `shhh code` where the work changes the tree and
// `shhh chat` where it only reads — the shape the eval runner already uses,
// for the same reason: what a step produced is read out of the transcript
// whatever the process's exit status, because a step that ran out of rounds
// still did work and the machine judges it on its answer. Nothing
// carries between two steps except the checkpoint, which is what the
// checkpoint has always been for: every step prompt states the item, the plan
// and the findings it needs, so a step is startable from nothing. A command
// step spends no turn at all and starts no process that loads a model.
//
// The two things a model must not do, it does not do here either. shhh runs
// the verification and shhh makes the commit.
// See docs/capabilities/todo.md#a-sprint-is-runs-with-a-session-between-them.

import (
	"errors"
	"fmt"
	"strings"
	"time"

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
	// costCap is the most the sprint may spend, in cents; 0 leaves the
	// project's todo.sprint_cost_cap_cents in force.
	costCap int64
	// parallel is how many items the sprint works at once. One is the loop
	// as it always was; 0 is unset, which is one for a new sprint and the
	// checkpoint's own number for a sprint being continued.
	parallel int
}

func newTodoRunCmd() *cobra.Command {
	var flags todoRunFlags
	cmd := &cobra.Command{
		Use:   "run [<slug>]",
		Short: "Work a backlog item, or the whole ready list, with nobody watching",
		Long: "Work one backlog item through research, implement, verify, review and commit, " +
			"in a session of its own. With --all, work the ready list one item at a time — the " +
			"sprint file's set where the backlog holds one — stopping when nothing is ready, when " +
			"--max or --cost-cap is reached, or on the first item that blocks.",
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
	cmd.Flags().Int64Var(&flags.costCap, "cost-cap", 0, "with --all, the most the sprint may spend in cents before it starts no further item (0 keeps todo.sprint_cost_cap_cents)")
	cmd.Flags().IntVar(&flags.parallel, "parallel", 0, "with --all, how many items to work at once, each in its own copy of the checkout (default 1)")
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
	case flags.costCap > 0 && !flags.all:
		return fmt.Errorf("--cost-cap bounds what a sprint spends across its items, so it needs --all")
	case flags.costCap < 0:
		return fmt.Errorf("--cost-cap %d: a ceiling is an amount in cents, or 0 for the setting's", flags.costCap)
	case flags.parallel > 1 && !flags.all:
		return fmt.Errorf("--parallel works several items of a sprint at once, so it needs --all")
	case flags.parallel < 0:
		return fmt.Errorf("--parallel %d: a sprint works at least one item at a time", flags.parallel)
	}
	cfg := ConfigFrom(cmd.Context())
	d, err := newTodoDriver(cmd.OutOrStdout(), todo.Root(todoCwd()), cfg, flags.noCommit)
	if err != nil {
		return err
	}
	defer d.close()
	d.costCapFlag = flags.costCap
	// A profile may state no run at all, and its items are still items: what
	// one needs is a person doing it, so the offer is the verb that files it
	// rather than a run that would describe the work instead of doing it.
	if !d.steps().Runs() {
		return fmt.Errorf("the %s profile has no run: its items are worked by hand, and `shhh todo done <item>` files one", todoProfile().Name)
	}
	// What this process must be able to do is what the run's steps ask for,
	// step by step: a pipeline that never commits wants no repository, and a
	// division into lanes is the one step an unattended run falls back from
	// rather than refuses.
	if ref, refused := d.steps().Refuse(d.can()); refused {
		return todoRunRefusal(d.root, ref)
	}
	if flags.all {
		if n := todoParallelism(d.root, flags.parallel); n > 1 {
			// An item a serial sprint was working when it stopped is in the
			// checkout itself, not in a copy of it, and a lane beside it would
			// be seeded with its half-done work.
			if sp, live := run.Live(d.root); live && sp.Current != "" {
				return fmt.Errorf("the sprint on disk is working %s one item at a time; `shhh todo run --all` finishes that one, and --parallel applies to the sprint after it", sp.Current)
			}
			return exitOf(d.sprintParallel(cmd.Context(), flags.max, n))
		}
		return exitOf(d.sprint(cmd.Context(), flags.max))
	}
	store := todo.Load(todoProfile(), d.root)
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
