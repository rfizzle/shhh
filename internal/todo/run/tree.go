package run

// The tree as a run reads it: what the run may commit, and the change it
// hands a reader.
//
// Both questions were answered twice — once in the session and once in the
// unattended runner — and the two answers disagreed. The session read its
// changeset and missed a file a shell command had written; the runner read
// git and missed the run's own work on a file somebody had left modified,
// while carrying that somebody's other edits. A commit is the one act of a
// run that cannot be taken back, so what it holds is defined here and read
// from here.
// See docs/capabilities/todo.md#a-run-is-turns-with-gates-between-them.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rfizzle/shhh/internal/todo"
)

// evidenceSubdir is where a run spools what its stages produced, under the
// state directory and beside nothing else: a directory of its own per run,
// so the stage after the one that spooled a check's output can be pointed at
// it and clearing it is one removal.
const evidenceSubdir = "run"

// EvidenceDir is where one run's evidence lives. It is under the repository
// rather than under shhh's state dir because the stages that read it are
// separate processes standing in this checkout, and a path they can be
// handed is what makes an id one of them can resolve.
func EvidenceDir(root, slug string) string {
	return filepath.Join(root, todo.StateDir, evidenceSubdir, slug, "evidence")
}

// RunStateDir is the whole of what one run leaves under the repository, for
// the ending that clears it.
func RunStateDir(root, slug string) string {
	return filepath.Join(root, todo.StateDir, evidenceSubdir, slug)
}

// MakeSpool creates the directory a run spools into and answers with it.
//
// The spool declares itself untracked on the way up, with an ignore file
// covering everything under it. Without one the run would watch its own
// bookkeeping: `git status` would name the spool as an untracked path, the
// tree fingerprint would move on every entry written into it, and a turn
// that changed nothing would close on a gate run started by the evidence of
// the last one.
func MakeSpool(root, slug string) (string, error) {
	dir := EvidenceDir(root, slug)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	ignore := filepath.Join(root, todo.StateDir, evidenceSubdir, ".gitignore")
	if _, err := os.Stat(ignore); err != nil {
		if err := os.WriteFile(ignore, []byte("*\n"), 0o600); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// ClearSpool removes what a run spooled. A run that has ended has nothing to
// continue and nothing left to read, and the evidence its stages quoted was
// quoted where it mattered — on the item, in the report.
func ClearSpool(root, slug string) { _ = os.RemoveAll(RunStateDir(root, slug)) }

// Contents is what a run's commit holds, in the one definition both surfaces
// use: what the run has already recorded, plus what its own calls wrote,
// plus everything the tree now reports changed that it did not already hold
// when the item started.
//
// The run's own writes are in it whatever the tree said at the start,
// because a file somebody had left modified is a file the run may still have
// worked on, and the alternative — subtracting the baseline from the run's
// own work — leaves the item's change half committed. The rest of the
// baseline stays out for the reason the baseline is taken at all: a file
// somebody else left modified is not the run's work and must not ride along.
//
// held is the checkpoint's own list, wrote is what this process's calls
// wrote, dirty is what git reports now and atStart is what it reported when
// the item began. The first two keep the order they were recorded in; what
// the tree adds is sorted, so two identical runs produce the same list.
func Contents(held, wrote, dirty, atStart []string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(list []string) {
		for _, rel := range list {
			if rel = Committable(rel); rel != "" && !seen[rel] {
				seen[rel] = true
				out = append(out, rel)
			}
		}
	}
	add(held)
	add(wrote)
	was := map[string]bool{}
	for _, rel := range atStart {
		was[rel] = true
	}
	var found []string
	for _, rel := range dirty {
		if rel = Committable(rel); rel == "" || was[rel] || seen[rel] {
			continue
		}
		seen[rel] = true
		found = append(found, rel)
	}
	sort.Strings(found)
	return append(out, found...)
}

// Committable is a path in the shape a commit names it, or empty for one no
// run may stage: the backlog, whose being committed at all is the project's
// call and not a run's, and the run's own spool beside it.
// See docs/capabilities/todo.md#where-the-backlog-lives.
func Committable(rel string) string {
	rel = strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(rel)), "./")
	switch {
	case rel == "":
		return ""
	case under(rel, todo.StateDir+"/"+todo.Subdir):
		return ""
	case under(rel, todo.StateDir+"/"+evidenceSubdir):
		return ""
	}
	return rel
}

// under reports whether a path is the named directory or something inside
// it. The separator is part of the question: a prefix test alone puts
// `.shhh/todoist.md` inside the backlog.
func under(rel, dir string) bool {
	return rel == dir || strings.HasPrefix(rel, dir+"/")
}

// DirtyPaths is what git reports as changed under root, in the shape a
// commit names them. A checkout git cannot read answers with nothing, which
// stops a commit rather than staging a guess.
func DirtyPaths(root string) []string {
	out, code := gitLines(root, "status", "--porcelain", "--untracked-files=all")
	if code != 0 {
		return nil
	}
	return PorcelainPaths(out)
}

// PorcelainPaths reads `git status --porcelain` into the paths it names,
// sorted, with what no run may stage left out.
func PorcelainPaths(status string) []string {
	seen := map[string]bool{}
	var out []string
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
		rel = Committable(strings.Trim(rel, `"`))
		if rel == "" || seen[rel] {
			continue
		}
		seen[rel] = true
		out = append(out, rel)
	}
	sort.Strings(out)
	return out
}

// ReviewDiffLines is the budget the change handed to a reader is bounded to.
// It is a bound at all because a reader's task is a prompt, and a
// thousand-file diff in one would spend the child's whole window on the
// change before it had read the item.
const ReviewDiffLines = 600

// ReviewFileFloor is the least any one file's diff is cut to, whatever the
// budget divided by the file count says. Below a few dozen lines a diff
// stops being a change and becomes a hint that one happened.
const ReviewFileFloor = 40

// BoundDiff is the change as a reader is handed it: every file that changed,
// each cut to its share of the budget, with a line on any file that was cut
// saying what is missing from it.
//
// The budget is divided rather than spent in order because of what a reader
// is being asked. A single tail of the whole diff hands over the last two
// files whole and does not mention the other eighteen, so a reviewer's "the
// change looks right" is a statement about a tenth of it — and neither the
// reader nor the run can tell, because a diff that stops looks exactly like
// a change that ended.
//
// The floor wins over the budget where a change touches enough files to take
// every share below it, so a very large fan-out hands over more than the
// budget says. That is the trade this makes on purpose: a share too small to
// read is not a cheaper reading, it is no reading at all.
func BoundDiff(files []string, budget, floor int) string {
	if len(files) == 0 {
		return ""
	}
	per := budget / len(files)
	if per < floor {
		per = floor
	}
	var b strings.Builder
	for _, d := range files {
		b.WriteString(cutTo(d, per))
	}
	return b.String()
}

// cutTo keeps the first n lines of one file's diff and says how many it
// dropped. The head is what is kept because a diff reads from the top: the
// hunks are in file order, and a tail would start halfway through one.
func cutTo(d string, n int) string {
	lines := strings.Split(strings.TrimRight(d, "\n"), "\n")
	if len(lines) <= n {
		return strings.Join(lines, "\n") + "\n"
	}
	return fmt.Sprintf("%s\n… %d more lines of this file's diff, not shown\n",
		strings.Join(lines[:n], "\n"), len(lines)-n)
}
