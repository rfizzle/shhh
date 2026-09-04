package run

// The commit a run makes, in one place.
//
// It was written twice — once in the session and once in the unattended
// runner — in two shapes, with two readings of what `git diff --cached`
// exits with and two answers to a git that is not installed. A commit is the
// one act of a run that cannot be taken back, so the two copies were the two
// places the rule about what a commit may carry could quietly disagree.
// See docs/capabilities/todo.md#a-run-is-turns-with-gates-between-them.

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/runner"
	"github.com/rfizzle/shhh/internal/todo"
)

// gitNotInstalled is the shell's exit code for a command that could not be
// run, which is what this package reports for a git that is not there. The
// alternative is reporting some real exit code for a git that was never
// there, and every caller that reads a code by name would then read the
// wrong sentence out of it.
const gitNotInstalled = 127

// Commit stages the run's paths by name and commits with the message the
// finish turn wrote, then answers with the paths that landed.
//
// It refuses a tree that already holds staged changes it did not make: a
// commit that carries a stranger cannot be reverted, cited or read as a
// unit. without is how the surface asking for this run says "run it without
// a commit", because the answer to a repository that cannot take one is to
// ask for the archive finish instead and the person is owed the way through.
func Commit(root string, paths []string, message, without string) ([]string, error) {
	if len(paths) == 0 {
		return nil, errors.New("the run changed no files under the repository")
	}
	// Four different failures came back as one sentence about the person's
	// index, and three of them were not about it. `--quiet` exits 1 for a
	// difference, and that is the only exit this check may read as staged
	// changes: telling someone outside a repository that their index holds
	// changes sends them looking for an index that does not exist.
	//
	// The repository itself is read off the filesystem rather than out of an
	// exit code, because git's own code for it moves: it was 128, the
	// refusal, and is 129 on git 2.51, where `--cached` is a usage error
	// against the `--no-index` fallback the missing repository leaves
	// behind. The directory either holds a repository or it does not, and
	// that answer is the same on every version.
	out, code := git(root, "diff", "--cached", "--quiet")
	switch {
	case code == 0:
	case code == 1:
		return nil, fmt.Errorf("the index already holds staged changes this run did not make; commit or unstage them first\n%s", out)
	case code == gitNotInstalled:
		return nil, fmt.Errorf("git is not on the path, so no commit can be made; install it, or %s", without)
	case !project.InRepo(root):
		return nil, fmt.Errorf("%s is not a git repository, so there is nothing to commit into; %s", root, without)
	default:
		return nil, fmt.Errorf("git diff --cached exited %d: %s", code, out)
	}
	if out, code := git(root, append([]string{"add", "--"}, paths...)...); code != 0 {
		return nil, fmt.Errorf("git add: %s", out)
	}
	f, err := os.CreateTemp("", "shhh-todo-commit-*.txt")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err := f.WriteString(message + "\n"); err != nil {
		f.Close()
		return nil, err
	}
	f.Close()
	if out, code := git(root, "commit", "-F", f.Name()); code != 0 {
		return nil, fmt.Errorf("git commit: %s", out)
	}
	return paths, nil
}

// File writes a finished run onto its item — the report the run produced,
// with the paths it committed and the commit line where it made a commit —
// and archives it, answering with where the item went.
//
// A failure to archive is not a failure of the work, so the report goes onto
// the item and the item goes back to open rather than staying in progress
// with its record only on somebody's screen, which is the one state nothing
// later recovers from. What to say about that is the caller's, because the
// two drivers say it to a transcript and to a terminal.
func File(root string, s *State, it todo.Item) (string, error) {
	report := s.Report
	if len(s.Files) > 0 && !s.NoCommit {
		report += "\nCommitted: " + strings.Join(s.Files, ", ") + "\n"
		report += todo.CommitLine(project.Head(root), s.Message)
	}
	to, err := todo.Archive(root, s.Slug, report)
	if err != nil {
		_ = todo.SetStatus(it.Path, todo.StatusOpen)
		_ = todo.Append(it.Path, report)
		return "", err
	}
	return to, nil
}

// git runs one git command in root and reports its output and its exit code.
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
