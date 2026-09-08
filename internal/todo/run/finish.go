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
	"github.com/rfizzle/shhh/internal/structural"
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
//
// The argv is the write tool's, not a second spelling of it: this used to
// write out `git add --` and `git commit -F` by hand, and a commit is the one
// act of a run that cannot be taken back, so two spellings were two places
// the rule about what a commit may carry could quietly disagree. hooks is the
// checkout's trust answer, which is what decides whether the checkout's own
// commit hooks run.
func Commit(root string, paths []string, message, without string, hooks bool) ([]string, error) {
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
	add, err := structural.AddArgv(paths)
	if err != nil {
		return nil, err
	}
	if out, code := git(root, add...); code != 0 {
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
	commit, err := structural.CommitArgv(f.Name(), hooks)
	if err != nil {
		return nil, err
	}
	if out, code := git(root, commit...); code != 0 {
		return nil, fmt.Errorf("git commit: %s", out)
	}
	return paths, nil
}

// Source is one page a run's write-up rests on: the URL that answered and
// the page's own title. Read says the fetch really happened — the session's
// own record of it, not the model's account — and a source that is not read
// is one the write-up cited and nobody opened.
//
// The run is handed these rather than finding them: which URLs a session
// fetched is the session's business, and this package builds prompts and
// reads answers.
type Source struct {
	URL   string
	Title string
	Read  bool
}

// SourcesSection is the block a write-up ends with: the pages that were
// actually read, and under them the ones the write-up cited and nobody
// opened. It is empty when there is nothing to say.
//
// It is built from the session's record of its own fetches and never from
// the write-up's prose. A sources list a model writes is a claim like any
// other; one built from what the fetcher returned is a fact, and the second
// list is where a source that was never read shows up — in the write-up the
// reviewer reads, rather than in the reader's browser.
// See docs/capabilities/chat.md#what-was-read.
func SourcesSection(list []Source) string {
	var read, cited []Source
	for _, s := range list {
		if s.Read {
			read = append(read, s)
			continue
		}
		cited = append(cited, s)
	}
	if len(read) == 0 && len(cited) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n## Sources\n\n")
	if len(read) == 0 {
		b.WriteString("Nothing was read: this write-up rests on what was already known.\n")
	}
	for _, s := range read {
		b.WriteString(sourceLine(s))
	}
	if len(cited) > 0 {
		b.WriteString("\nCited, not read:\n\n")
		for _, s := range cited {
			b.WriteString(sourceLine(s))
		}
	}
	return b.String()
}

// sourceLine is one row of the block: the address first, because that is
// what a reader checks, and the title after it.
func sourceLine(s Source) string {
	if strings.TrimSpace(s.Title) == "" {
		return "- " + s.URL + "\n"
	}
	return "- " + s.URL + " — " + strings.TrimSpace(s.Title) + "\n"
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
	report := s.Report + SourcesSection(s.Sources)
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

// NoteWriter puts one write-up in the session's shared notebook and answers
// with how a reader names it there. It is a function rather than the store
// because a run knows it has somewhere to be read and nothing more; which
// notebook, and whether there is one at all, is the session's.
type NoteWriter func(author, title, body string) (string, error)

// NoteAuthor signs what a run writes in the notebook. Every note carries the
// agent that wrote it so a reader can weigh it, and a run is not one of the
// session's colleagues: it is the machine, working an item the person put on
// the backlog. See docs/capabilities/chat.md#what-they-share.
const NoteAuthor = "todo run"

// FileNote is File for the finish whose record is the write-up: the report
// goes into the session's notebook first, and the item is archived with a
// line saying where it went. A later reader of the archive can then find the
// writing itself, rather than only the fact that there was some.
//
// A notebook that will not take it is not a failure of the work. The report
// still goes onto the item, which is the record that outlives the session,
// and the line says what could not be done instead of where to look.
func FileNote(root string, s *State, it todo.Item, write NoteWriter) (string, error) {
	if write != nil && strings.TrimSpace(s.Report) != "" {
		ref, err := write(NoteAuthor, s.Slug, s.Report+SourcesSection(s.Sources))
		if err != nil {
			s.Report += "\nThe write-up is not in the session notebook: " + err.Error() + "\n"
		} else {
			s.Report += "\nWritten up in the session notebook as " + ref + ".\n"
		}
	}
	return File(root, s, it)
}

// git runs one git command in root and reports its output and its exit code.
func git(root string, args ...string) (string, int) {
	out, code := gitLines(root, args...)
	return strings.TrimSpace(out), code
}

// gitLines is that without the trim, for a command whose output is read by
// column. `git status --porcelain` states a path's staged mark in the first
// column and its unstaged mark in the second, so a line about a file changed
// in the tree and not in the index begins with a space — and a trimmed line
// puts the path three characters to the left of where every reader of that
// format looks for it.
func gitLines(root string, args ...string) (string, int) {
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
	return string(out), code
}
