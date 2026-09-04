package agent

// Telling a turn that the tree moved.
//
// A session surveys the checkout once, when it starts, and reasons from that
// picture for as long as it runs. One session in one checkout can afford
// that. Two sessions on the same tree cannot, and neither can a session with
// an editor beside it or a pull in the next terminal: the branch switches,
// HEAD moves, a path the model has not read is rewritten, and nothing in the
// transcript says so until an edit is refused for touching a file that
// changed — if the model ever read that file at all.
//
// This is the reading that says so. A snapshot of the tree — HEAD, the
// branch, and every path git status names — is taken at the start of every
// turn and after every round's results are in, which are the boundaries the
// loop already takes its other readings at. The difference between two
// snapshots is attributed before it is reported: paths the session's own
// edits account for are subtracted, so the report says something the model
// could not already infer from its transcript. Commands are the one hole a
// subtraction cannot close, because a command may write anything, so a
// change that follows one is reported as "since your last command" and the
// model — which has the command in its own transcript — is left to
// reconcile.
//
// What git does not see is content. A path that was already changed when a
// stranger changed it again has the same status line before and after, and
// the status call never opens a file. That half is answered from the other
// side: the record of what the model has been shown, re-checked at these same
// boundaries, names the files whose content no longer matches what it read.
// They are reported in the same block, because a session that has to go back
// and read something should hear it in one place, with everything else that
// moved.
//
// It is never told what moved the tree. Git does not know, and a guess
// dressed as a fact is what the model would act on.
// See docs/capabilities/coding-agent.md#the-tree-can-move-under-a-session.

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/provider"
)

// DefaultTreeBudget is how long one status call may take before the reading
// stops running at every round boundary. The call sits between a round's
// results and the next request, so a slow one is paid on every round of every
// turn; past the budget the reading keeps only the turn boundary, where the
// wait is against a person typing rather than a model answering.
const DefaultTreeBudget = 300 * time.Millisecond

// treeNoticePaths bounds how many changed paths a notice names. The rest are
// counted: a list of forty paths is a list the model skims.
const treeNoticePaths = 8

// stateDir is the tool's own state directory inside a checkout. Writes
// there are shhh's, whichever session made them, and are not workspace
// changes.
const stateDir = ".shhh/"

// TreeCheck configures the reading for one agent. Dir is anywhere inside the
// checkout. Own returns the paths this session has written, in any form
// relative to the process or absolute; they are what the reading subtracts.
// IsCommand names the tools that may write anything. Budget zero is
// DefaultTreeBudget; Log, when set, takes the one line written when the
// reading downgrades.
type TreeCheck struct {
	Dir       string
	Own       func() []string
	IsCommand func(name string) bool
	Budget    time.Duration
	Log       func(msg string)
	// ReadChanged, when set, names the files the model has been shown whose
	// content has since moved, as absolute paths. It is asked at every
	// boundary alongside the status call, and answers the question the status
	// call cannot: a file that was already dirty when somebody rewrote it in
	// place is named by porcelain either way. Nil is what a surface keeping
	// no such record has to say.
	ReadChanged func() []string
	// Sibling, when set, reports whether another session is open in this
	// checkout right now. It is asked at each notice rather than once at the
	// start: the other session usually opens after this one, and the notice
	// it would explain is the one that comes after that. Nil answers no,
	// which is the honest answer for a surface with nothing to ask.
	Sibling func() bool
}

// TreeSnapshot is the tree at one moment: the commit, the branch, and every
// path git status names with its two-character state, keyed relative to the
// repository root.
type TreeSnapshot struct {
	Head     string
	Branch   string // empty when detached
	Detached bool
	Status   map[string]string
}

// TreeNotice is one report of the tree having moved, ready to deliver: the
// message that joins the conversation and the one-line account the reader is
// shown beside it. Paths is how many were reported after subtraction;
// ReadPaths is how many files the model had read hold something else now,
// which is a different claim about a possibly overlapping set.
type TreeNotice struct {
	Message     string
	Notice      string
	Paths       int
	ReadPaths   int
	HeadMoved   bool
	BranchMoved bool
	// Commands is how many command calls ran since the last snapshot. When it
	// is not zero the message attributes nothing, since a command may have
	// made any of these changes.
	Commands int
}

// Signal is what the notice reported, as the observability recorder's closed
// set: "head" when only the commit or branch moved, "paths" when only the
// changed set did, "both" otherwise.
func (n TreeNotice) Signal() string {
	moved := n.HeadMoved || n.BranchMoved
	// A file whose content moved is a path change to the recorder, whatever
	// porcelain made of it: the set is closed at three, and reporting a
	// content-only notice as "head" would be the one wrong answer available.
	paths := n.Paths > 0 || n.ReadPaths > 0
	switch {
	case moved && paths:
		return "both"
	case moved:
		return "head"
	}
	return "paths"
}

// treeState is what an Agent knows about the tree between two boundaries.
type treeState struct {
	cfg      TreeCheck
	top      string
	last     TreeSnapshot
	commands int
	// degraded is set once a status call blew the budget; from then on only
	// the turn boundary reads.
	degraded bool
	// reported is the set of stale readings the notices already named. The
	// path half of this reading reports each change once because it compares
	// two snapshots, and the content half has to be made to: a file the model
	// has not re-read yet is still stale at the next boundary and at the one
	// after that, and a clause that repeats every round is what teaches the
	// model to skip the block it is in.
	reported map[string]bool
}

// SetTreeCheck turns the reading on. A directory that is not inside a git
// repository leaves it off: there is no status to read, and a reading that
// shells out on every round to be told so would be paying for nothing.
func (a *Agent) SetTreeCheck(c TreeCheck) {
	top, err := gitOut(c.Dir, "rev-parse", "--show-toplevel")
	if err != nil {
		a.tree = nil
		return
	}
	if c.Budget <= 0 {
		c.Budget = DefaultTreeBudget
	}
	t := &treeState{cfg: c, top: strings.TrimSpace(top)}
	snap, err := TakeTreeSnapshot(t.top)
	if err != nil {
		a.tree = nil
		return
	}
	t.last = snap
	a.tree = t
}

// RestartTreeCheck takes the baseline again, for a front-end that ended one
// session and began another in the same process. What the tree looked like
// when the old conversation opened is not what the new one should be told
// about: without this the first reading of the new session would report
// every change the last one made as somebody else's work, since the
// changeset that subtracts a session's own edits started over too. A reading
// that is off stays off.
func (a *Agent) RestartTreeCheck() {
	if a.tree == nil {
		return
	}
	a.SetTreeCheck(a.tree.cfg)
}

// TreeChecking reports whether the reading is on.
func (a *Agent) TreeChecking() bool { return a.tree != nil }

// noteTreeCalls counts the calls of a round that may write anywhere, so the
// next notice can say a command ran rather than claim the changes are
// somebody else's.
func (a *Agent) noteTreeCalls(calls []provider.ToolCall) {
	if a.tree == nil || a.tree.cfg.IsCommand == nil {
		return
	}
	for _, tc := range calls {
		if a.tree.cfg.IsCommand(tc.Name) {
			a.tree.commands++
		}
	}
}

// NextTreeNotice takes a snapshot, compares it with the last one, and returns
// what the turn should be told, if anything. turnStart says which boundary
// this is: a degraded reading answers only at a turn start. The caller
// appends Message and shows Notice; nothing here touches the conversation,
// for the same reason NextIntervention does not.
func (a *Agent) NextTreeNotice(turnStart bool) (TreeNotice, bool) {
	t := a.tree
	if t == nil || (t.degraded && !turnStart) {
		return TreeNotice{}, false
	}
	start := time.Now()
	now, err := TakeTreeSnapshot(t.top)
	if err != nil {
		return TreeNotice{}, false
	}
	if took := time.Since(start); took > t.cfg.Budget && !t.degraded {
		t.degraded = true
		if t.cfg.Log != nil {
			t.cfg.Log(fmt.Sprintf("tree check: git status took %s, over the %s budget; reading at turn boundaries only from here",
				took.Round(time.Millisecond), t.cfg.Budget))
		}
	}
	own := t.ownPaths()
	commands := t.commands
	last := t.last
	t.last, t.commands = now, 0

	n, ok := diffTree(last, now, own, t.readChanged(), commands, t.cfg.Sibling)
	return n, ok
}

// ownPaths is what the session has written, keyed the way the snapshot keys
// paths.
func (t *treeState) ownPaths() map[string]bool {
	if t.cfg.Own == nil {
		return nil
	}
	own := map[string]bool{}
	for _, p := range t.cfg.Own() {
		if rel, ok := t.relative(p); ok {
			own[rel] = true
		}
	}
	return own
}

// readChanged is what the record of shown files says has moved since the last
// boundary, keyed the same way. The call is made at every boundary and is
// answered from a stat per file in the common case, which is why it rides
// here rather than behind a budget of its own.
//
// A file already named stays out until it leaves the set — which it does when
// the model reads it again, or when somebody puts the old content back — and
// is named afresh if it moves after that.
func (t *treeState) readChanged() []string {
	if t.cfg.ReadChanged == nil {
		return nil
	}
	stale := map[string]bool{}
	var fresh []string
	for _, p := range t.cfg.ReadChanged() {
		rel, ok := t.relative(p)
		if !ok {
			continue
		}
		stale[rel] = true
		if !t.reported[rel] {
			fresh = append(fresh, rel)
		}
	}
	t.reported = stale
	return fresh
}

// relative keys a path the way the snapshot keys them: from the repository
// root, forward slashes. A path outside the repository is dropped — it cannot
// appear in the status, so it can neither be subtracted from one nor named
// beside one.
func (t *treeState) relative(p string) (string, bool) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(t.top, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// diffTree is the comparison itself, separated from the git calls so it can
// be tested on snapshots built by hand. read is what the record of shown
// files says has moved, already keyed to the root. sibling may be nil and is
// asked only once there is something to report — it is a store read, and a
// boundary where nothing moved has nothing to attribute to anybody.
func diffTree(last, now TreeSnapshot, own map[string]bool, read []string, commands int, sibling func() bool) (TreeNotice, bool) {
	var changed []string
	for p, st := range now.Status {
		if last.Status[p] != st {
			changed = append(changed, p)
		}
	}
	for p := range last.Status {
		if _, still := now.Status[p]; !still {
			changed = append(changed, p)
		}
	}
	changed = foreign(changed, own)
	sort.Strings(changed)
	// The session's own writes are not subtracted from the read set, because
	// they are already absent from it: a tool that writes a file records what
	// it wrote, so the picture the model holds of that file is current. What
	// is dropped is the tool's own state directory, which is bookkeeping
	// rather than the tree moving.
	read = foreign(read, nil)
	sort.Strings(read)

	n := TreeNotice{
		Paths:       len(changed),
		ReadPaths:   len(read),
		HeadMoved:   last.Head != now.Head,
		BranchMoved: last.Branch != now.Branch || last.Detached != now.Detached,
		Commands:    commands,
	}
	if !n.HeadMoved && !n.BranchMoved && n.Paths == 0 && n.ReadPaths == 0 {
		return TreeNotice{}, false
	}

	var parts []string
	if n.HeadMoved {
		parts = append(parts, fmt.Sprintf("HEAD %s → %s", shortHead(last.Head), shortHead(now.Head)))
	}
	if n.BranchMoved {
		parts = append(parts, fmt.Sprintf("branch %s → %s", branchName(last), branchName(now)))
	}
	attribution := "outside this session"
	if commands > 0 {
		attribution = "since your last command"
	}
	count := ""
	if n.Paths > 0 {
		count = fmt.Sprintf("%d %s changed %s", n.Paths, plural(n.Paths, "path"), attribution)
		parts = append(parts, count+": "+pathList(changed))
	}
	// The read set is named on its own clause rather than folded into the
	// count above, because it is a different sentence: those paths moved,
	// these are files whose content is no longer what this session was shown.
	// A path can honestly be in both.
	readCount := ""
	if n.ReadPaths > 0 {
		readCount = fmt.Sprintf("%d %s you have read changed", n.ReadPaths, plural(n.ReadPaths, "file"))
		parts = append(parts, readCount+": "+pathList(read))
	}
	var b strings.Builder
	b.WriteString("[tree: " + strings.Join(parts, " · ") + "]\n")
	if commands > 0 {
		b.WriteString("A command of yours ran since the tree was last read, so some of this may be its doing; " +
			"whatever it did not do is somebody else's. Re-read a file before editing it, and do not revert or explain changes you did not make")
	} else {
		b.WriteString("This session did not make these changes. Re-read a file before editing it, and do not revert or explain them")
	}
	// The likely author, where there is one to name. It goes last because it
	// is the answer to the question the sentence before it raises, and it
	// names no transcript and no slot: which conversation the other session
	// is having is its own, and this one is being told only that somebody is
	// there to ask.
	if sibling != nil && sibling() {
		b.WriteString(" — another session is open in this checkout")
	}
	b.WriteString(".")
	n.Message = b.String()

	var row []string
	if n.HeadMoved {
		row = append(row, fmt.Sprintf("HEAD %s → %s", shortHead(last.Head), shortHead(now.Head)))
	}
	if n.BranchMoved {
		row = append(row, fmt.Sprintf("branch %s → %s", branchName(last), branchName(now)))
	}
	if count != "" {
		row = append(row, count)
	}
	if readCount != "" {
		row = append(row, readCount)
	}
	n.Notice = "Tree moved — " + strings.Join(row, ", ") + "."
	return n, true
}

// foreign drops the paths the session accounts for: its own, anything under
// the tool's state directory, and an untracked directory entry that one of
// its own files lives under (git collapses a new directory to one line).
func foreign(paths []string, own map[string]bool) []string {
	var out []string
	for _, p := range paths {
		if strings.HasPrefix(p, stateDir) || own[p] {
			continue
		}
		if strings.HasSuffix(p, "/") && anyUnder(own, p) {
			continue
		}
		out = append(out, p)
	}
	return out
}

func anyUnder(own map[string]bool, dir string) bool {
	for p := range own {
		if strings.HasPrefix(p, dir) {
			return true
		}
	}
	return false
}

func pathList(paths []string) string {
	if len(paths) <= treeNoticePaths {
		return strings.Join(paths, ", ")
	}
	return strings.Join(paths[:treeNoticePaths], ", ") + fmt.Sprintf(" (+%d more)", len(paths)-treeNoticePaths)
}

func shortHead(h string) string {
	if h == "" {
		return "(none)"
	}
	if len(h) > 7 {
		return h[:7]
	}
	return h
}

func branchName(s TreeSnapshot) string {
	if s.Detached {
		return "(detached)"
	}
	if s.Branch == "" {
		return "(none)"
	}
	return s.Branch
}

// TakeTreeSnapshot reads the tree in one git call: porcelain v2 with the
// branch header, NUL-terminated so a path is never quoted. Paths come back
// relative to the directory git ran in, which is why it is run at the root.
func TakeTreeSnapshot(top string) (TreeSnapshot, error) {
	out, err := gitOut(top, "status", "--porcelain=v2", "--branch", "-z")
	if err != nil {
		return TreeSnapshot{}, err
	}
	return parseStatusV2(out), nil
}

// parseStatusV2 reads `git status --porcelain=v2 --branch -z`. Entry kinds:
// `1` ordinary, `2` renamed or copied (the original path follows as its own
// field), `u` unmerged, `?` untracked, `!` ignored; `#` lines are headers.
func parseStatusV2(out string) TreeSnapshot {
	snap := TreeSnapshot{Status: map[string]string{}}
	fields := strings.Split(strings.TrimRight(out, "\x00"), "\x00")
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if f == "" {
			continue
		}
		switch {
		case strings.HasPrefix(f, "# branch.oid "):
			if oid := strings.TrimPrefix(f, "# branch.oid "); oid != "(initial)" {
				snap.Head = oid
			}
		case strings.HasPrefix(f, "# branch.head "):
			if name := strings.TrimPrefix(f, "# branch.head "); name == "(detached)" {
				snap.Detached = true
			} else {
				snap.Branch = name
			}
		case strings.HasPrefix(f, "# "):
		case strings.HasPrefix(f, "1 "):
			if parts := strings.SplitN(f, " ", 9); len(parts) == 9 {
				snap.Status[parts[8]] = parts[1]
			}
		case strings.HasPrefix(f, "2 "):
			if parts := strings.SplitN(f, " ", 10); len(parts) == 10 {
				snap.Status[parts[9]] = parts[1]
			}
			i++ // the original path rides in the next field
		case strings.HasPrefix(f, "u "):
			if parts := strings.SplitN(f, " ", 11); len(parts) == 11 {
				snap.Status[parts[10]] = parts[1]
			}
		case strings.HasPrefix(f, "? "):
			snap.Status[f[2:]] = "??"
			// `!` (ignored) entries are not shown unless asked for, and are
			// not workspace changes when they are.
		}
	}
	return snap
}

func gitOut(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return out.String(), nil
}
