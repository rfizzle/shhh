package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/runner"
	"github.com/rfizzle/shhh/internal/todo"
	"github.com/rfizzle/shhh/internal/todo/run"
)

// todoVerdict is what the verify stage found: what it ran, whether that
// passed, and the reason there was nothing to reach a verdict with at all.
// The last one is separate because a run with nothing to verify is not a run
// that verified, and it is not work a fix round could answer either.
type todoVerdict struct {
	ok      bool
	output  string
	blocked string
}

// verify runs the item's listed tests, then the project's checks. The tests
// are the ones the item held when the run started, before any stage could
// have edited the file: the run tells the model to tick the item's boxes as
// it works, and a command it wrote there is not one shhh runs unasked.
func (d *todoDriver) verify(ctx context.Context, st *run.State, named string) todoVerdict {
	ctx, cancel := context.WithTimeout(ctx, todoVerifyTimeout)
	defer cancel()
	var b strings.Builder
	ok := true
	// A step that names its own command runs that and nothing else: the
	// project said what checking this work means, and the item's own tests
	// and the workspace's suite are the answer for the step that did not.
	if named != "" {
		out, code := runner.RunCaptureIn(ctx, d.tree, named)
		passed := d.ran(&b, named, out, code)
		return todoVerdict{ok: passed, output: strings.TrimRight(b.String(), "\n")}
	}
	for _, cmd := range st.Tests {
		if out, code := runner.RunCaptureIn(ctx, d.tree, cmd); !d.ran(&b, cmd, out, code) {
			ok = false
		}
	}
	// silent is why the gate reached no verdict over this tree, and empty
	// where it reached one — pass or fail. A run is refused at its start for
	// the same absence, so reaching it here means the item's own checks were
	// what verified it and they are gone: what is left is the block below.
	silent := "this checkout is not trusted, so the project's quality gate does not run here"
	switch {
	case st.Checked:
		b.WriteString("quality gate: passed as the implement turn closed\n")
		silent = ""
	case d.gate != nil:
		res, err := d.gate.Run(ctx, "")
		switch {
		case err != nil:
			fmt.Fprintf(&b, "quality gate: %v\n", err)
			ok, silent = false, ""
		case res.Unconfigured:
			// A workspace with no quality config is the one blocked verdict
			// that is not about the work. Spending a fix round on it tells a
			// model to fix findings that are not in the tree, for the one or
			// two rounds it has, and then blocks the item on a file only a
			// person writes.
			fmt.Fprintf(&b, "quality gate: %s\n", res.Reason)
			silent = "the project has no " + quality.ConfigRelPath
		case res.Verdict != quality.VerdictPass:
			b.WriteString(res.Format(quality.TakeFingerprint(d.tree)) + "\n")
			ok, silent = false, ""
		default:
			fmt.Fprintf(&b, "quality gate %q: pass\n", res.Suite)
			silent = ""
		}
	}
	out := strings.TrimRight(b.String(), "\n")
	if len(st.Tests) == 0 && silent != "" {
		return todoVerdict{output: out, blocked: run.NothingVerifies("it lists no tests and " + silent)}
	}
	return todoVerdict{ok: ok, output: out}
}

// ran writes one command's outcome into the verify's report and answers
// whether it passed.
//
// What it writes is both ends of the output and a citation for the rest,
// which is the shape the stage after this one needs. A remediation turn is
// asked to fix what this found, and a tail alone hands it the count of the
// failures and none of the failures; the excerpt is bounded because the
// report goes into a prompt, and the id is how a turn that needs more than
// the excerpt asks for it.
func (d *todoDriver) ran(b *strings.Builder, command, out string, code int) bool {
	fmt.Fprintf(b, "$ %s → exit %d%s\n%s\n", command, code,
		d.keep("verify:"+command, out), quality.Excerpt(out, quality.MaxInlineBytes))
	return code == 0
}

// commit makes the run's commit, which is the run package's to make: the
// same staging, the same refusals and the same message file the session
// uses, so the one act of a run that cannot be taken back cannot mean two
// things depending on who asked for it.
func (d *todoDriver) commit(st *run.State) ([]string, error) {
	return run.Commit(d.root, st.Paths, st.Message,
		"--no-commit runs an item without one, or todo.commit = false makes that the default",
		// A commit hook is a program the checkout can point git at, so it
		// runs under the same answer everything else the checkout declares
		// runs under.
		projectTrust().RunsOwnPrograms())
}

// paths is what the run may stage, in the definition both surfaces share
// (run.Contents): what an earlier process of the same run recorded, plus
// what this run's own stages wrote, plus everything under the root that
// changed after the item started — and never a backlog file, because the
// backlog is never committed on the project's behalf.
// See docs/capabilities/todo.md#where-the-backlog-lives.
func (d *todoDriver) paths(st *run.State) []string {
	return run.Contents(st.Paths, d.wrote, run.DirtyPaths(d.tree), st.Prestart)
}

// finish writes what the run ended as onto the item: the archive and the
// report for one that is done, the evidence for one that blocked. Either way
// the checkpoint goes, because a run that ended has nothing to continue.
func (d *todoDriver) finish(st *run.State, it todo.Item, sp *run.Sprint) {
	if st.Stage == run.StageDone {
		citeSources(st)
		to, err := run.File(d.root, st, it)
		if err != nil {
			// The work is finished and the run package has already put the
			// item back to open with the report on it; what is left is
			// saying so where somebody reading the log will find it.
			fmt.Fprintf(d.out, "✓ todo run %s finished, but the item could not be archived — %v. The report is on the item and it is open.\n", st.Slug, err)
		} else {
			fmt.Fprintln(d.out, todoRunDoneLine(st, to))
			if closed, err := todo.CloseSprintIfDone(todoProfile(), d.root, d.spendFigure(sp)); err == nil && closed != "" {
				fmt.Fprintln(d.out, "sprint file closed → "+closed)
			}
		}
		run.Discard(d.root, st.Slug)
		run.ClearSpool(d.root, st.Slug)
		return
	}
	_ = todo.SetStatus(it.Path, todo.StatusBlocked)
	_ = todo.Append(it.Path, fmt.Sprintf("## Blocked\n%s\n\n_run in session %s, stage %s, %s_",
		st.Blocked, st.Session, st.Stage, time.Now().Format("2006-01-02 15:04")))
	fmt.Fprintf(d.out, "✗ todo run %s blocked — %s\n", st.Slug, st.Blocked)
	// A lane's work is in its own copy of the checkout, and the lane says
	// where that is itself (todoLane.end).
	if paths := st.Paths; len(paths) > 0 && d.lane == nil {
		fmt.Fprintln(d.out, "work so far stays in the tree, uncommitted: "+strings.Join(paths, ", "))
	}
	run.Discard(d.root, st.Slug)
	run.ClearSpool(d.root, st.Slug)
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
func (d *todoDriver) sprintGoal() string { return todo.Load(todoProfile(), d.root).Sprint.Purpose() }
