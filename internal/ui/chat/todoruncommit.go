package chat

// The run's commit stage: which paths a finished item may stage, and the git
// it is staged and committed with. It is its own file for the reason the
// verify beside it is — the model does not make the commit — and because git
// is reached from here and from nowhere else in the runner.

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/runner"
	"github.com/rfizzle/shhh/internal/todo/run"
)

// todoCommitMsg is the commit's outcome: the paths it staged, or why not.
type todoCommitMsg struct {
	slug  string
	files []string
	err   error
}

// todoRunPaths is what the run may stage, in the definition both surfaces
// share (run.Contents): what an earlier session of this run recorded, plus
// every path this session's changeset saw change since the run's first turn,
// plus everything the tree now reports changed that it did not already hold
// when the item started — and never a backlog file, because the backlog is
// never committed on the project's behalf.
//
// The tree is read as well as the changeset because the changeset is not the
// whole of what a run changes: a `gofmt -w`, a generator, a `make` that
// writes its own output are all the run's work, and none of them goes
// through a write tool. The baseline is what keeps somebody else's edits out
// of the commit, and the changeset is what claims a file the tree already
// held modified for the run.
// See docs/capabilities/todo.md#where-the-backlog-lives.
func (m Model) todoRunPaths() []string {
	root := m.todos.Root
	st := m.todoRunner.state
	if st == nil {
		return nil
	}
	var wrote []string
	for _, t := range m.changes.Turns() {
		if int(t.N) < st.Turn {
			continue
		}
		for _, r := range t.Records {
			if !r.Changed() {
				continue
			}
			if rel := runRelPath(root, r.Path); rel != "" {
				wrote = append(wrote, rel)
			}
		}
	}
	return run.Contents(st.Paths, wrote, run.DirtyPaths(root), st.Prestart)
}

// todoCommitCmd makes the run's commit, which is the run package's to make:
// the same staging, the same refusals and the same message file the
// unattended runner uses, so the one act of a run that cannot be taken back
// cannot mean two things depending on who asked for it.
func (m Model) todoCommitCmd() tea.Cmd {
	root := m.todos.Root
	slug := m.todoRunner.state.Slug
	message := m.todoRunner.state.Message
	paths := m.todoRunPaths()
	// A commit hook is a program the checkout can point git at, so it runs
	// under the same answer everything else the checkout declares runs under.
	hooks := m.trust().Granted
	without := fmt.Sprintf("/todo run %s --no-commit runs it without one, or todo.commit = false makes that the default", slug)
	return func() tea.Msg {
		files, err := run.Commit(root, paths, message, without, hooks)
		return todoCommitMsg{slug: slug, files: files, err: err}
	}
}

// gitNotInstalled is the shell's own code for a command that never started,
// which is what this reports for a git that is not there rather than some
// real exit code a caller might read a meaning out of.
const gitNotInstalled = 127

// git runs one git command in root and reports its output and its exit code.
// It is the reading side only — the diff a reviewer child is handed; the
// commit a run makes is the run package's (run.Commit).
func git(root string, args ...string) (string, int) {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Env = runner.Environ()
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else {
			code = gitNotInstalled
			out = append(out, err.Error()...)
		}
	}
	return strings.TrimSpace(string(out)), code
}

// finishTodoCommit applies the commit outcome.
func (m Model) finishTodoCommit(msg todoCommitMsg) (tea.Model, tea.Cmd) {
	st := m.todoRunner.state
	if st == nil || st.Over() || msg.slug != st.Slug {
		return m, nil
	}
	if kind, ok := st.StepKind(); !ok || kind != run.KindFinish {
		return m, nil
	}
	if msg.err != nil {
		return m.todoRunStep(st.Block("the commit could not be made: " + msg.err.Error()))
	}
	return m.todoRunStep(st.Committed(msg.files))
}
