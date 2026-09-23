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
// could not already infer from its transcript. Commands are the hole a
// subtraction cannot close on its own, because a command may write anything,
// so a change that follows one is reported as "since your last command" and
// the model — which has the command in its own transcript — is left to
// reconcile.
//
// Two more subtractions keep the report about what somebody else did. What
// the tree ignores is not the tree moving: a build cache under a gitignored
// directory is the session's own scratch, and counting it is how a notice
// that exists to say a stranger was here comes to say five thousand paths
// about a cache the same turn wrote. And a directory that first appears in a
// round where a command of this session ran is that command's doing, so it
// and everything under it are the session's too. What the ignore rules
// suppressed is counted rather than dropped in silence — a reading that says
// nothing and a reading that had nothing to say are different answers.
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
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/provider"
)

// DefaultTreeBudget is how long one reading may take before it stops running
// at every round boundary. The reading sits between a round's results and the
// next request, so a slow one is paid on every round of every turn; past the
// budget the reading keeps only the turn boundary, where the wait is against a
// person typing rather than a model answering.
//
// It is one deadline for the whole reading and not one per call. The reading
// is three git calls — the status, the ignore rules, and the directories a
// command made — and three calls each allowed the budget is three times the
// wait the budget promises to a person whose checkout is large enough for any
// of it to matter.
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
// DefaultTreeBudget; Log, when set, takes the line written when the reading
// downgrades, when it recovers, and when git stops answering it.
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
	// Instructions names the project instruction files that were read into
	// the system prompt, absolute or relative to this process. They are read
	// once, at the start, and are not re-read: the block in the prompt is the
	// reading the session opened on. So a notice that names one of these
	// files says that too — a model told AGENTS.md changed and nothing else
	// goes on obeying the copy in front of it, which is the rule it is about
	// to be measured against. Nil is a surface with no such block.
	Instructions []string
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
	// Ignored is how many changed paths the tree's own ignore rules
	// suppressed. It is on the row rather than in the message: the model is
	// told what moved, and the reader is told how much of the movement was
	// scratch, which is the difference between a quiet reading and a reading
	// that found nothing.
	Ignored int
	// Unavailable is a notice that the tree could not be read at all, rather
	// than that it moved. It is not a movement, so a surface that records
	// movements records nothing for it.
	Unavailable bool
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
	// degraded is set once a reading blew the budget; from then on only the
	// turn boundary reads, until a reading there comes back well inside it.
	degraded bool
	// failed is the reason the last snapshot could not be taken, and empty
	// once one could. A git that has stopped answering goes on not answering
	// at every boundary, so the reader is told once per reason rather than
	// once per round.
	failed string
	// now is the clock the budget is spent against, so a test can spend one
	// without waiting it out. Nil is time.Now.
	now func() time.Time
	// started is when the reading in progress began, and overCall with overAt
	// is the git call that came back to find the budget gone, if one did.
	//
	// The budget is read after each call rather than enforced on it. A call
	// cut off mid-way answers nothing, and what the two calls after the status
	// answer is which of the changed paths are the session's own scratch: a
	// reading that loses that answer reports the cache instead of suppressing
	// it, which is the five-thousand-path alarm this reading was taught not to
	// raise. So an over-budget reading is finished and then stops happening
	// every round, which is where the repeated cost was anyway.
	started  time.Time
	overCall string
	overAt   time.Duration
	// instructions is cfg.Instructions keyed the way the snapshot keys
	// paths, worked out once: the set is a session's prompt and does not
	// change while it runs, and keying it per boundary would resolve the
	// same handful of paths on every round.
	instructions map[string]bool
	// made is the untracked directories a command of this session created,
	// as the status keys them, with their trailing slash. A directory that
	// was not there at the last boundary and is there at one where a command
	// ran is that command's work — nothing else in the round could have made
	// it — so it and everything under it stop being news.
	//
	// The cost of the guess is a directory a stranger created in the same
	// round as one of this session's commands, which stays subtracted. That
	// round is already the one where attribution is a guess: its notice says
	// "since your last command" precisely because the session cannot tell
	// the two apart, and a false alarm every time a build runs is the more
	// expensive of the two mistakes.
	made map[string]bool
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
	top = strings.TrimSpace(top)
	if resolved, err := filepath.EvalSymlinks(top); err == nil {
		top = resolved
	}
	t := &treeState{cfg: c, top: top}
	snap, err := TakeTreeSnapshot(t.top)
	if err != nil {
		a.tree = nil
		return
	}
	t.last = snap
	t.instructions = map[string]bool{}
	for _, p := range c.Instructions {
		if rel, ok := t.relative(p); ok {
			t.instructions[rel] = true
		}
	}
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
	t.begin()
	now, err := TakeTreeSnapshot(t.top)
	if err != nil {
		return t.unavailable(err)
	}
	t.failed = ""
	t.spent("git status")
	own := t.ownPaths()
	commands := t.commands
	last := t.last
	t.noteCommandDirs(last, now, commands)
	t.last, t.commands = now, 0

	n, ok := diffTree(last, now, treeAttribution{
		own:          own,
		made:         t.made,
		instructions: t.instructions,
		read:         t.readChanged(),
		commands:     commands,
		sibling:      t.cfg.Sibling,
		ignored:      t.ignoredPaths,
	})
	// After the comparison, because the ignore reading is made inside it: the
	// budget covers every call the reading makes, wherever it is made from.
	t.downgrade()
	t.recover()
	return n, ok
}

// unavailable is what a boundary owes when git would not give a snapshot: a
// broken index or a repository removed from under the session is otherwise
// silence, and silence is what a tree that has not moved sounds like. The
// reason is said once, and again only when it changes.
func (t *treeState) unavailable(err error) (TreeNotice, bool) {
	reason := err.Error()
	if reason == t.failed {
		return TreeNotice{}, false
	}
	t.failed = reason
	if t.cfg.Log != nil {
		t.cfg.Log("tree check: git status failed: " + reason)
	}
	// The model is told as well as the reader, because a turn that hears
	// nothing about the tree takes that to mean it has not moved.
	return TreeNotice{
		Message: "[tree: check unavailable · " + reason + "]\n" +
			"The tree is not being read, so changes made outside this session will not be reported until git answers again.",
		Notice:      "tree check unavailable · " + reason,
		Unavailable: true,
	}, true
}

// clock is what the budget is spent against.
func (t *treeState) clock() time.Time {
	if t.now != nil {
		return t.now()
	}
	return time.Now()
}

// begin starts the deadline this reading's git calls share.
func (t *treeState) begin() {
	t.started, t.overCall, t.overAt = t.clock(), "", 0
}

// spent is called as each git call of the reading comes back, and remembers
// the first one that found the budget gone — the call worth naming, since a
// reading three calls long has three answers to "which one was slow".
func (t *treeState) spent(call string) {
	if t.overCall != "" {
		return
	}
	if took := t.clock().Sub(t.started); took > t.cfg.Budget {
		t.overCall, t.overAt = call, took
	}
}

// downgrade keeps only the turn boundary once a reading has run past the
// budget, and says so once. A reading already downgraded says nothing: the
// line is about the change of behaviour, and repeating it every round would
// cost the reader more than the reading does.
func (t *treeState) downgrade() {
	if t.overCall == "" || t.degraded {
		return
	}
	t.degraded = true
	if t.cfg.Log != nil {
		t.cfg.Log(fmt.Sprintf("tree check: %s took the reading to %s, over the %s budget; reading at turn boundaries only from here",
			t.overCall, t.overAt.Round(time.Millisecond), t.cfg.Budget))
	}
}

// recover goes back to reading at every round boundary once a degraded
// reading has come back well inside the budget — at most half of it, so a
// reading hovering at the line does not flip back and forth every turn. One
// stall under a `git gc` is not the checkout's size, and without this it cost
// the rest of the session its round-boundary readings. A later reading over
// the budget degrades again through downgrade.
func (t *treeState) recover() {
	if !t.degraded || t.overCall != "" {
		return
	}
	took := t.clock().Sub(t.started)
	if took > t.cfg.Budget/2 {
		return
	}
	t.degraded = false
	if t.cfg.Log != nil {
		t.cfg.Log(fmt.Sprintf("tree check: the reading took %s, inside the %s budget; reading at round boundaries again",
			took.Round(time.Millisecond), t.cfg.Budget))
	}
}

// noteCommandDirs remembers the directories a command of this session
// created. The evidence is untracked content that was not there at the last
// boundary, in a round where a command ran, under a directory git tracks
// nothing in: git collapses a new untracked directory to one entry and lists
// its files one by one where a configuration asks it to, so the candidates
// are every ancestor of every new untracked entry and the question is asked
// of all of them in one call.
//
// A directory git already keeps files in is never one of these. That is the
// line that matters: a file a stranger drops into a source directory while a
// build runs is still reported, and it is only the scratch directory nothing
// tracks that goes quiet.
func (t *treeState) noteCommandDirs(last, now TreeSnapshot, commands int) {
	if commands == 0 {
		return
	}
	cand := map[string]bool{}
	for p, st := range now.Status {
		if st != "??" {
			continue
		}
		if _, was := last.Status[p]; was {
			continue
		}
		for _, d := range ancestors(p) {
			if !inAny(t.made, d) {
				cand[d] = true
			}
		}
	}
	// Shallowest first, and a directory already covered by one kept is not
	// kept: the subtraction is by prefix, so remembering the cache's own
	// hundreds of subdirectories under it would only cost the next reading
	// the same answer over again.
	for _, d := range t.untrackedDirs(cand) {
		if inAny(t.made, d) {
			continue
		}
		if t.made == nil {
			t.made = map[string]bool{}
		}
		t.made[d] = true
	}
}

// treeDirProbe bounds how many directories one reading asks git about. The
// shallowest are kept, which is the truncation that loses nothing: the
// subtraction is by prefix, so the top of a cache covers everything under it.
const treeDirProbe = 256

// untrackedDirs is which of these directories git tracks nothing in, in one
// call. An empty answer, and any failure, keeps every path in the notice:
// this subtraction exists to stop a false alarm, and guessing it while git is
// unavailable would trade that for a silence nothing can see.
func (t *treeState) untrackedDirs(dirs map[string]bool) []string {
	if len(dirs) == 0 {
		return nil
	}
	ask := make([]string, 0, len(dirs))
	for d := range dirs {
		ask = append(ask, d)
	}
	sort.Slice(ask, func(i, j int) bool {
		di, dj := strings.Count(ask[i], "/"), strings.Count(ask[j], "/")
		if di != dj {
			return di < dj
		}
		return ask[i] < ask[j]
	})
	if len(ask) > treeDirProbe {
		ask = ask[:treeDirProbe]
	}
	out, err := gitOut(t.top, append([]string{"ls-files", "-z", "--"}, ask...)...)
	t.spent("git ls-files")
	if err != nil {
		return nil
	}
	var untracked []string
	for _, d := range ask {
		if !anyWithPrefix(out, d) {
			untracked = append(untracked, d)
		}
	}
	return untracked
}

// anyWithPrefix reports whether any of the NUL-separated paths in out is
// under dir.
func anyWithPrefix(out, dir string) bool {
	for _, p := range strings.Split(out, "\x00") {
		if p != "" && strings.HasPrefix(p, dir) {
			return true
		}
	}
	return false
}

// ancestors is every directory a status path lies in, shallowest first, with
// the trailing slash the status itself uses for a collapsed directory. A path
// at the root has none, which is why a file a command wrote beside the readme
// is still reported: there is no directory to attribute it to.
func ancestors(p string) []string {
	var out []string
	for i, c := range p {
		if c == '/' && i+1 < len(p) {
			out = append(out, p[:i+1])
		}
	}
	if strings.HasSuffix(p, "/") {
		out = append(out, p)
	}
	return out
}

// ignoredPaths is which of these paths the tree's own ignore rules cover, in
// one git call for the whole reading: check-ignore answers a list, and asking
// it per path would pay a process for every entry of the set this exists to
// throw away.
//
// The index is consulted, which is the behaviour to want: a tracked file that
// also matches a pattern is not ignored — somebody changed a file git is
// keeping, and that is the notice's whole subject. Exit status 1 is
// check-ignore saying none of them, so it is an answer rather than a failure.
func (t *treeState) ignoredPaths(paths []string) map[string]bool {
	if len(paths) == 0 {
		return nil
	}
	out, err := gitIn(t.top, strings.Join(paths, "\x00")+"\x00", "check-ignore", "-z", "--stdin")
	t.spent("git check-ignore")
	if err != nil {
		return nil
	}
	set := map[string]bool{}
	for _, p := range strings.Split(strings.TrimRight(out, "\x00"), "\x00") {
		if p != "" {
			set[p] = true
		}
	}
	return set
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
	// Git resolves the checkout's physical path. The macOS temporary root is
	// reachable through /var and /private/var, so compare an existing path's
	// physical spelling or a session's own write looks outside its checkout.
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	rel, err := filepath.Rel(t.top, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// treeAttribution is everything the comparison needs to say whose work a
// change was. Every field is optional: the zero value attributes nothing and
// reports every difference between the two snapshots, which is what a
// surface with no changeset, no ignore rules and nobody to name has to say.
type treeAttribution struct {
	// own is what the session wrote, keyed to the repository root.
	own map[string]bool
	// made is the directories its commands created, keyed the same way and
	// subtracted by prefix.
	made map[string]bool
	// instructions is the project's instruction files, keyed the same way.
	instructions map[string]bool
	// read is what the record of shown files says has moved, keyed the same
	// way.
	read []string
	// commands is how many command calls ran in the interval.
	commands int
	// sibling is asked only once there is something to report — it is a
	// store read, and a boundary where nothing moved has nothing to
	// attribute to anybody.
	sibling func() bool
	// ignored answers which of a list of paths the tree ignores, and is
	// asked once per reading with everything left after the cheap
	// subtractions: it shells out, and the paths it would answer for are the
	// ones already known to be the session's.
	ignored func(paths []string) map[string]bool
}

// diffTree is the comparison itself, separated from the git calls so it can
// be tested on snapshots built by hand.
func diffTree(last, now TreeSnapshot, at treeAttribution) (TreeNotice, bool) {
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
	own, instructions, read, commands := at.own, at.instructions, at.read, at.commands
	changed = foreign(changed, own, at.made, instructions)
	changed, ignored := unignored(changed, at.ignored)
	sort.Strings(changed)
	// The session's own writes are not subtracted from the read set, because
	// they are already absent from it: a tool that writes a file records what
	// it wrote, so the picture the model holds of that file is current. What
	// is dropped is the tool's own state directory, which is bookkeeping
	// rather than the tree moving — bar an instruction file the project
	// keeps there, which is the project's word and not shhh's.
	read = foreign(read, nil, nil, instructions)
	sort.Strings(read)

	n := TreeNotice{
		Paths:       len(changed),
		ReadPaths:   len(read),
		HeadMoved:   last.Head != now.Head,
		BranchMoved: last.Branch != now.Branch || last.Detached != now.Detached,
		Commands:    commands,
		Ignored:     ignored,
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
		count = fmt.Sprintf("%s %s changed %s", grouped(n.Paths), plural(n.Paths, "path"), attribution)
		parts = append(parts, count+": "+pathList(changed))
	}
	// The read set is named on its own clause rather than folded into the
	// count above, because it is a different sentence: those paths moved,
	// these are files whose content is no longer what this session was shown.
	// A path can honestly be in both.
	readCount := ""
	if n.ReadPaths > 0 {
		readCount = fmt.Sprintf("%s %s you have read changed", grouped(n.ReadPaths), plural(n.ReadPaths, "file"))
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
	if at.sibling != nil && at.sibling() {
		b.WriteString(" — another session is open in this checkout")
	}
	b.WriteString(".")
	// The instruction files are the one part of the prompt this notice can
	// contradict. Nothing re-injects them — a block rewritten mid-conversation
	// costs the cached prefix of everything before it, and this notice is
	// already the reading the model acts on — so the older reading is named as
	// older, which costs a sentence.
	if named := namedIn(instructions, changed, read); len(named) > 0 {
		b.WriteString(" The project instructions changed (" + pathList(named) +
			"): the block in your prompt is the reading this session opened on and is not re-read, so read the file before relying on it.")
	}
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
	// The row is the product reporting, in the voice every other row uses:
	// lower case, no full stop. The ignored count rides on the end as its own
	// fact, because it is the answer to the question a count of fourteen
	// raises in a checkout where six thousand paths moved.
	// See docs/capabilities/coding-agent.md#the-tree-can-move-under-a-session.
	n.Notice = "tree moved — " + strings.Join(row, ", ")
	if n.Ignored > 0 {
		n.Notice += " · " + grouped(n.Ignored) + " ignored"
	}
	return n, true
}

// grouped is a count with its thousands separated. A notice's numbers are
// read rather than computed with, and 5811 is a number a reader has to count
// the digits of. Grouping runs from the right, so a sign in front of it is
// carried rather than counted.
func grouped(n int) string {
	s := strconv.Itoa(n)
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return b.String()
}

// namedIn is the paths of set that this notice names, in the order the notice
// names them and without repeating one that appears in both lists. An empty
// set answers nothing, which is what a surface carrying no instruction block
// has to say.
func namedIn(set map[string]bool, lists ...[]string) []string {
	var named []string
	for _, list := range lists {
		for _, p := range list {
			if set[p] && !slices.Contains(named, p) {
				named = append(named, p)
			}
		}
	}
	return named
}

// foreign drops the paths the session accounts for: its own, anything under
// a directory one of its commands created, anything under the tool's state
// directory, and an untracked directory entry that one of its own files
// lives under (git collapses a new directory to one line).
//
// keep is the exception to the state directory, and the reason there is one:
// the instruction file a project writes for its agents may live in there, and
// that file is the project's word rather than shhh's bookkeeping. Dropped
// with the checkpoints, the change the session most needs to hear about is
// the one it would never be told.
func foreign(paths []string, own, made, keep map[string]bool) []string {
	var out []string
	for _, p := range paths {
		if own[p] || (strings.HasPrefix(p, stateDir) && !keep[p]) {
			continue
		}
		if strings.HasSuffix(p, "/") && anyUnder(own, p) {
			continue
		}
		if inAny(made, p) {
			continue
		}
		out = append(out, p)
	}
	return out
}

// unignored is the paths the tree does not ignore, and how many it dropped.
// ask is called once for the whole list or not at all: it is a git call, and
// this is the reading's one chance to make it.
func unignored(paths []string, ask func([]string) map[string]bool) ([]string, int) {
	if ask == nil || len(paths) == 0 {
		return paths, 0
	}
	ignored := ask(paths)
	if len(ignored) == 0 {
		return paths, 0
	}
	var out []string
	for _, p := range paths {
		if ignored[p] {
			continue
		}
		out = append(out, p)
	}
	return out, len(paths) - len(out)
}

// inAny reports whether p is one of these directories or sits under one.
func inAny(dirs map[string]bool, p string) bool {
	for d := range dirs {
		if p == d || strings.HasPrefix(p, d) {
			return true
		}
	}
	return false
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
//
// The untracked mode is asked for rather than left to the checkout. A person
// who set `status.showUntrackedFiles=all` for their own reading of a tree has
// said nothing about this one, and the setting is not a small difference: git
// names every file under a new directory where the default names the
// directory once, which is how one notice came to count 5,825 cache files one
// by one. What the model is told is the same reading in every checkout.
func TakeTreeSnapshot(top string) (TreeSnapshot, error) {
	out, err := gitOut(top, "status", "--porcelain=v2", "--branch", "--untracked-files=normal", "-z")
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

// gitOut runs git and returns its standard output. A failure carries git's
// own first line of complaint where it gave one, since "exit status 128" is
// not a reason anybody can act on.
func gitOut(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	if err := cmd.Run(); err != nil {
		if line, _, _ := strings.Cut(strings.TrimSpace(errOut.String()), "\n"); line != "" {
			return "", errors.New(line)
		}
		return "", err
	}
	return out.String(), nil
}

// gitIn is gitOut with a list on standard input, for the query git answers
// that way. Exit status 1 is a question answered no — check-ignore's way of
// saying none of these — and comes back as an empty answer rather than an
// error; anything else is git failing.
func gitIn(dir, stdin string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Stdin = strings.NewReader(stdin)
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		err = nil
	}
	if err != nil {
		return "", err
	}
	return out.String(), nil
}
