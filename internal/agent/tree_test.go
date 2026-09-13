package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/provider"
)

// treeFixture is a repository with one commit and a clean tree, and a git
// runner bound to it. Config is pinned so a developer's own hooks and
// identity never reach the test.
func treeFixture(t *testing.T) (string, func(args ...string)) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	ws := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", ws}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	write(t, ws, "a.txt", "hello\n")
	git("add", ".")
	git("commit", "-q", "-m", "init")
	return ws, git
}

func write(t *testing.T, ws, rel, content string) string {
	t.Helper()
	p := filepath.Join(ws, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func treeAgent(t *testing.T, ws string, own func() []string) *Agent {
	t.Helper()
	a := New(nil, nil)
	a.SetTreeCheck(TreeCheck{
		Dir:       ws,
		Own:       own,
		IsCommand: func(name string) bool { return name == "execute_command" },
	})
	if !a.TreeChecking() {
		t.Fatal("the reading should be on inside a repository")
	}
	return a
}

func TestTree_AForeignWriteIsReported(t *testing.T) {
	ws, _ := treeFixture(t)
	a := treeAgent(t, ws, nil)
	if _, ok := a.NextTreeNotice(false); ok {
		t.Fatal("an unchanged tree owes nothing")
	}

	write(t, ws, "b.txt", "new\n")
	n, ok := a.NextTreeNotice(false)
	if !ok {
		t.Fatal("a path written by somebody else should be reported")
	}
	if !strings.Contains(n.Message, "[tree: 1 path changed outside this session: b.txt]") {
		t.Errorf("message should name the path and attribute it, got:\n%s", n.Message)
	}
	if !strings.Contains(n.Message, "did not make these changes") {
		t.Errorf("message should say the changes are not the session's, got:\n%s", n.Message)
	}
	if n.Notice != "tree moved — 1 path changed outside this session" {
		t.Errorf("notice = %q", n.Notice)
	}
	if n.Signal() != "paths" || n.Paths != 1 || n.HeadMoved {
		t.Errorf("notice fields: %+v", n)
	}
	// The snapshot moved with the report: the same change is not reported twice.
	if _, ok := a.NextTreeNotice(false); ok {
		t.Fatal("a change already reported should not be reported again")
	}
}

// A session boundary takes the baseline again. The changeset that subtracts a
// session's own edits starts over with it, so a baseline kept would report the
// last conversation's work to the new one as a stranger's.
func TestTree_RestartTakesTheBaselineAgain(t *testing.T) {
	ws, _ := treeFixture(t)
	a := treeAgent(t, ws, nil)
	// Everything that happened to the tree before the boundary, reported or
	// not, is where the next conversation starts from.
	write(t, ws, "b.txt", "new\n")

	a.RestartTreeCheck()

	if n, ok := a.NextTreeNotice(true); ok {
		t.Fatalf("the tree as it stands is the new session's starting point, got:\n%s", n.Message)
	}
	write(t, ws, "c.txt", "later\n")
	if _, ok := a.NextTreeNotice(true); !ok {
		t.Fatal("a change after the new baseline is still reported")
	}
}

// A reading that is off has no baseline to take.
func TestTree_RestartOnASessionWithoutTheReading(t *testing.T) {
	a := New(nil, nil)
	a.RestartTreeCheck()
	if a.TreeChecking() {
		t.Fatal("restarting must not turn the reading on")
	}
}

func TestTree_TheSessionsOwnEditIsSubtracted(t *testing.T) {
	ws, _ := treeFixture(t)
	own := write(t, ws, "mine.txt", "mine\n")
	a := treeAgent(t, ws, func() []string { return []string{own} })
	// Written after the baseline was taken, so it is a change — but the
	// session's own.
	write(t, ws, "mine.txt", "mine again\n")
	if n, ok := a.NextTreeNotice(false); ok {
		t.Fatalf("the session's own edit should not be reported, got:\n%s", n.Message)
	}
}

func TestTree_ANewDirectoryOfTheSessionsOwnIsSubtracted(t *testing.T) {
	ws, _ := treeFixture(t)
	a := treeAgent(t, ws, func() []string { return []string{filepath.Join(ws, "pkg", "new.go")} })
	write(t, ws, "pkg/new.go", "package pkg\n")
	// git collapses an untracked directory to `pkg/`; the file under it is
	// the session's.
	if n, ok := a.NextTreeNotice(false); ok {
		t.Fatalf("a directory holding only the session's own file should not be reported, got:\n%s", n.Message)
	}
}

func TestTree_ACommandChangesTheAttribution(t *testing.T) {
	ws, _ := treeFixture(t)
	a := treeAgent(t, ws, nil)
	a.BeginToolRound("", []provider.ToolCall{{ID: "c1", Name: "execute_command", Arguments: `{"command":"touch x"}`}}, nil)
	write(t, ws, "x", "")

	n, ok := a.NextTreeNotice(false)
	if !ok {
		t.Fatal("a change after a command is still reported")
	}
	if !strings.Contains(n.Message, "1 path changed since your last command: x") {
		t.Errorf("a change after a command must not be called somebody else's, got:\n%s", n.Message)
	}
	if n.Commands != 1 {
		t.Errorf("commands = %d, want 1", n.Commands)
	}
	// The count is consumed with the snapshot.
	write(t, ws, "y", "")
	if n, _ := a.NextTreeNotice(false); n.Commands != 0 || !strings.Contains(n.Message, "outside this session") {
		t.Errorf("a later change with no command between is somebody else's again, got:\n%s", n.Message)
	}
}

func TestTree_HeadAndBranchMovesAreReported(t *testing.T) {
	ws, git := treeFixture(t)
	a := treeAgent(t, ws, nil)
	git("checkout", "-q", "-b", "feature")
	write(t, ws, "a.txt", "changed\n")
	git("commit", "-q", "-am", "move")

	n, ok := a.NextTreeNotice(false)
	if !ok {
		t.Fatal("a moved head should be reported")
	}
	if !strings.Contains(n.Message, "HEAD ") || !strings.Contains(n.Message, " → ") {
		t.Errorf("message should name both commits, got:\n%s", n.Message)
	}
	if !strings.Contains(n.Message, "branch main → feature") {
		t.Errorf("message should name both branches, got:\n%s", n.Message)
	}
	if !n.HeadMoved || !n.BranchMoved || n.Paths != 0 || n.Signal() != "head" {
		t.Errorf("notice fields: %+v", n)
	}
	if strings.Contains(n.Message, "paths changed") {
		t.Errorf("a commit of a clean tree changes no paths, got:\n%s", n.Message)
	}
}

func TestTree_DetachedHeadIsNamed(t *testing.T) {
	ws, git := treeFixture(t)
	a := treeAgent(t, ws, nil)
	git("checkout", "-q", "--detach")
	n, ok := a.NextTreeNotice(false)
	if !ok || !strings.Contains(n.Message, "branch main → (detached)") {
		t.Errorf("detaching is a branch move, got ok=%v:\n%s", ok, n.Message)
	}
}

func TestTree_OutsideARepositoryTheReadingIsOff(t *testing.T) {
	a := New(nil, nil)
	a.SetTreeCheck(TreeCheck{Dir: t.TempDir()})
	if a.TreeChecking() {
		t.Fatal("no repository, no reading")
	}
	if _, ok := a.NextTreeNotice(true); ok {
		t.Fatal("an agent without the reading owes nothing")
	}
}

func TestTree_TheToolsOwnStateIsNotAChange(t *testing.T) {
	ws, _ := treeFixture(t)
	a := treeAgent(t, ws, nil)
	write(t, ws, ".shhh/todo/.run/x.json", "{}")
	if n, ok := a.NextTreeNotice(false); ok {
		t.Fatalf("writes under .shhh/ are the tool's, got:\n%s", n.Message)
	}
}

func TestTree_ThePathListIsBounded(t *testing.T) {
	ws, _ := treeFixture(t)
	a := treeAgent(t, ws, nil)
	for i := 0; i < treeNoticePaths+3; i++ {
		write(t, ws, "f"+string(rune('a'+i))+".txt", "")
	}
	n, ok := a.NextTreeNotice(false)
	if !ok {
		t.Fatal("expected a notice")
	}
	if !strings.Contains(n.Message, "(+3 more)") || n.Paths != treeNoticePaths+3 {
		t.Errorf("the overflow should be counted, got:\n%s", n.Message)
	}
	if !strings.Contains(n.Notice, "11 paths changed outside this session") {
		t.Errorf("the row carries the count, not the list: %q", n.Notice)
	}
}

func TestTree_ASlowStatusKeepsOnlyTheTurnBoundary(t *testing.T) {
	ws, _ := treeFixture(t)
	var logged []string
	a := New(nil, nil)
	a.SetTreeCheck(TreeCheck{Dir: ws, Budget: time.Nanosecond, Log: func(s string) { logged = append(logged, s) }})

	a.NextTreeNotice(false) // over budget: this one downgrades
	if len(logged) != 1 || !strings.Contains(logged[0], "turn boundaries only") {
		t.Fatalf("the downgrade is logged once, got %q", logged)
	}
	write(t, ws, "b.txt", "")
	if _, ok := a.NextTreeNotice(false); ok {
		t.Fatal("a degraded reading does not run between rounds")
	}
	n, ok := a.NextTreeNotice(true)
	if !ok || !strings.Contains(n.Message, "b.txt") {
		t.Fatalf("a degraded reading still runs at the turn boundary, got ok=%v:\n%s", ok, n.Message)
	}
	if len(logged) != 1 {
		t.Errorf("the downgrade is logged once, not per call: %q", logged)
	}
}

// The budget is one deadline for the reading rather than one per call: a
// status that came back well inside it and an ignore reading that ran long is
// a reading that ran over, and it downgrades exactly as a slow status does.
func TestTree_ABudgetSpentAfterTheStatusStillDowngrades(t *testing.T) {
	ws, _ := treeFixture(t)
	var logged []string
	a := New(nil, nil)
	a.SetTreeCheck(TreeCheck{Dir: ws, Budget: 300 * time.Millisecond, Log: func(s string) { logged = append(logged, s) }})
	if !a.TreeChecking() {
		t.Fatal("the reading should be on inside a repository")
	}
	// A clock the reading spends rather than one it waits out. It is read at
	// the start of the reading and as each git call comes back: the status
	// inside the budget, the ignore rules well past it.
	base, reads := time.Now(), 0
	at := []time.Duration{0, 100 * time.Millisecond, 900 * time.Millisecond}
	a.tree.now = func() time.Time {
		d := at[min(reads, len(at)-1)]
		reads++
		return base.Add(d)
	}

	write(t, ws, "b.txt", "")
	if _, ok := a.NextTreeNotice(false); !ok {
		t.Fatal("a reading that ran over still reports what it read")
	}
	if len(logged) != 1 || !strings.Contains(logged[0], "git check-ignore") ||
		!strings.Contains(logged[0], "turn boundaries only") {
		t.Fatalf("the call that found the budget gone is named, once: %q", logged)
	}
	write(t, ws, "c.txt", "")
	if _, ok := a.NextTreeNotice(false); ok {
		t.Error("a reading that ran over does not run between rounds")
	}
	if n, ok := a.NextTreeNotice(true); !ok || !strings.Contains(n.Message, "c.txt") {
		t.Errorf("it still runs at the turn boundary, got ok=%v:\n%s", ok, n.Message)
	}
}

// The reading asks for the untracked mode it wants. A person's own
// `status.showUntrackedFiles=all` is about their `git status` and not about
// what the model is told: under it git names every file of a new directory,
// and a build cache reaches the notice as thousands of paths rather than one.
func TestTree_TheUntrackedModeIsTheReadingsAndNotTheCheckouts(t *testing.T) {
	ws, run := treeFixture(t)
	run("config", "status.showUntrackedFiles", "all")
	for _, p := range []string{"cache/aa/one", "cache/bb/two", "cache/three"} {
		write(t, ws, p, "x\n")
	}

	snap, err := TakeTreeSnapshot(ws)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Status) != 1 || snap.Status["cache/"] != "??" {
		t.Errorf("a new untracked directory is one entry whatever the checkout asks for, got %v", snap.Status)
	}
}

func TestTree_ParseStatusV2(t *testing.T) {
	out := strings.Join([]string{
		"# branch.oid abc123",
		"# branch.head main",
		"1 .M N... 100644 100644 100644 aaaa bbbb path with space.txt",
		"2 R. N... 100644 100644 100644 aaaa bbbb R100 new.txt", "old.txt",
		"u UU N... 100644 100644 100644 100644 aaaa bbbb cccc conflict.txt",
		"? untracked/",
		"? .shhh/x",
	}, "\x00") + "\x00"
	snap := parseStatusV2(out)
	if snap.Head != "abc123" || snap.Branch != "main" || snap.Detached {
		t.Errorf("header: %+v", snap)
	}
	want := map[string]string{
		"path with space.txt": ".M",
		"new.txt":             "R.",
		"conflict.txt":        "UU",
		"untracked/":          "??",
		".shhh/x":             "??",
	}
	for p, st := range want {
		if snap.Status[p] != st {
			t.Errorf("status[%q] = %q, want %q", p, snap.Status[p], st)
		}
	}
	if len(snap.Status) != len(want) {
		t.Errorf("status = %v", snap.Status)
	}
	if got := parseStatusV2("# branch.oid (initial)\x00# branch.head (detached)\x00"); got.Head != "" || !got.Detached {
		t.Errorf("initial detached: %+v", got)
	}
}

func TestTree_DiffOnSnapshotsBuiltByHand(t *testing.T) {
	last := TreeSnapshot{Head: "1111111aaaa", Branch: "main", Status: map[string]string{"a": ".M", "gone": "??"}}
	now := TreeSnapshot{Head: "2222222bbbb", Branch: "main", Status: map[string]string{"a": "M.", "new": "??", "mine": "??"}}
	n, ok := diffTree(last, now, treeAttribution{own: map[string]bool{"mine": true}})
	if !ok {
		t.Fatal("expected a notice")
	}
	if want := "[tree: HEAD 1111111 → 2222222 · 3 paths changed outside this session: a, gone, new]"; !strings.HasPrefix(n.Message, want) {
		t.Errorf("message:\n%s\nwant prefix:\n%s", n.Message, want)
	}
	if n.Signal() != "both" {
		t.Errorf("signal = %q", n.Signal())
	}
	if _, ok := diffTree(now, now, treeAttribution{}); ok {
		t.Error("identical snapshots owe nothing")
	}
}

// A round's command calls are counted where the round is recorded, so no
// surface has to remember to count them.
func TestTree_BeginToolRoundCountsCommands(t *testing.T) {
	ws, _ := treeFixture(t)
	a := treeAgent(t, ws, nil)
	calls := []provider.ToolCall{
		{ID: "1", Name: "read_file", Arguments: `{}`},
		{ID: "2", Name: "execute_command", Arguments: `{}`},
		{ID: "3", Name: "execute_command", Arguments: `{}`},
	}
	a.BeginToolRound("", calls, nil)
	if a.tree.commands != 2 {
		t.Errorf("commands = %d, want 2", a.tree.commands)
	}
}

// The block says who most likely moved the tree when there is somebody to
// name, and never which conversation they are having.
func TestTree_BlockNamesTheOtherSessionInThisCheckout(t *testing.T) {
	last := TreeSnapshot{Head: "1111111aaaa", Branch: "main", Status: map[string]string{}}
	now := TreeSnapshot{Head: "1111111aaaa", Branch: "main", Status: map[string]string{"a": ".M"}}

	alone, ok := diffTree(last, now, treeAttribution{sibling: func() bool { return false }})
	if !ok {
		t.Fatal("expected a notice")
	}
	if strings.Contains(alone.Message, "another session") {
		t.Errorf("nobody else is here to name:\n%s", alone.Message)
	}

	shared, ok := diffTree(last, now, treeAttribution{sibling: func() bool { return true }})
	if !ok {
		t.Fatal("expected a notice")
	}
	// The clause is the whole of the difference, which is how this says
	// nothing else about the other session came with it: not its
	// conversation, not the slot it is writing, not what it is doing.
	want := strings.TrimSuffix(alone.Message, ".") + " — another session is open in this checkout."
	if shared.Message != want {
		t.Errorf("message:\n%s\nwant:\n%s", shared.Message, want)
	}

	// The same clause after a command of the session's own, where the block
	// attributes nothing: it still says who else is here to ask.
	afterCommand, _ := diffTree(last, now, treeAttribution{commands: 1, sibling: func() bool { return true }})
	if !strings.HasSuffix(afterCommand.Message, "another session is open in this checkout.") {
		t.Errorf("the clause is owed on both wordings:\n%s", afterCommand.Message)
	}
}

// Nil is the honest answer for a surface with nothing to ask, and it costs
// the clause rather than the block.
func TestTree_NoSiblingReadingCostsOnlyTheClause(t *testing.T) {
	last := TreeSnapshot{Head: "1111111aaaa", Branch: "main", Status: map[string]string{}}
	now := TreeSnapshot{Head: "1111111aaaa", Branch: "main", Status: map[string]string{"a": ".M"}}
	n, ok := diffTree(last, now, treeAttribution{})
	if !ok || !strings.HasSuffix(n.Message, "do not revert or explain them.") {
		t.Errorf("message:\n%s", n.Message)
	}
}

// The case the status call is blind to: a file that was already dirty when
// somebody rewrote it in place. Porcelain says the same thing either side of
// the change, so the block would have nothing to report without the other
// reading.
func TestTree_AFileTheModelReadIsNamedWhenItsContentMoved(t *testing.T) {
	ws, _ := treeFixture(t)
	// Dirty before the baseline is taken, so the status line does not move.
	write(t, ws, "a.txt", "mine\n")
	read := []string{
		filepath.Join(ws, "a.txt"),
		// The tool's own state is not the tree moving, and a file outside
		// the checkout cannot be named beside a status line.
		filepath.Join(ws, ".shhh", "run.json"),
		filepath.Join(t.TempDir(), "elsewhere.txt"),
	}
	a := New(nil, nil)
	a.SetTreeCheck(TreeCheck{Dir: ws, ReadChanged: func() []string { return read }})
	if !a.TreeChecking() {
		t.Fatal("the reading should be on inside a repository")
	}

	n, ok := a.NextTreeNotice(false)
	if !ok {
		t.Fatal("a file whose content moved is worth the block on its own")
	}
	if n.Message != "[tree: 1 file you have read changed: a.txt]\n"+
		"This session did not make these changes. Re-read a file before editing it, and do not revert or explain them." {
		t.Errorf("message:\n%s", n.Message)
	}
	if n.Notice != "tree moved — 1 file you have read changed" {
		t.Errorf("notice = %q", n.Notice)
	}
	if n.Paths != 0 || n.ReadPaths != 1 || n.HeadMoved || n.Signal() != "paths" {
		t.Errorf("notice fields: %+v, signal %q", n, n.Signal())
	}

	// The file is still stale at the next boundary and at every one after it
	// until the model reads it again. Saying so every round is how a block
	// becomes the thing the model skips, so it is said once.
	if n, ok := a.NextTreeNotice(false); ok {
		t.Errorf("a stale reading already named is not named again:\n%s", n.Message)
	}

	// Once it leaves the set — the model read it again, or the old content
	// came back — a later move is news again.
	read = nil
	if _, ok := a.NextTreeNotice(false); ok {
		t.Fatal("a file that is no longer stale owes nothing")
	}
	read = []string{filepath.Join(ws, "a.txt")}
	if n, ok := a.NextTreeNotice(false); !ok || n.ReadPaths != 1 {
		t.Errorf("a file that moved again is named again, got ok=%v:\n%s", ok, n.Message)
	}
}

// The instruction files are read into the prompt once and never again, so a
// notice that names one has to say that the block the model is holding is the
// older reading — otherwise it goes on obeying a rule the file no longer
// carries, and nothing on the record disagrees with it.
func TestTree_AChangedInstructionFileSaysThePromptIsTheOlderReading(t *testing.T) {
	ws, git := treeFixture(t)
	write(t, ws, "AGENTS.md", "the old rule\n")
	git("add", ".")
	git("commit", "-q", "-m", "instructions")

	a := New(nil, nil)
	a.SetTreeCheck(TreeCheck{Dir: ws, Instructions: []string{
		filepath.Join(ws, "AGENTS.md"),
		// A file the walk found outside this checkout — the user's own, on a
		// surface that passes it — cannot be named beside a status line.
		filepath.Join(t.TempDir(), "AGENTS.md"),
	}})
	if !a.TreeChecking() {
		t.Fatal("the reading should be on inside a repository")
	}

	// Somebody else's edit to a file this session never read.
	write(t, ws, "AGENTS.md", "the new rule\n")
	n, ok := a.NextTreeNotice(false)
	if !ok {
		t.Fatal("expected a notice")
	}
	const want = " The project instructions changed (AGENTS.md): the block in your prompt is " +
		"the reading this session opened on and is not re-read, so read the file before relying on it."
	if !strings.HasSuffix(n.Message, want) {
		t.Errorf("message:\n%s\nwant it to end with:\n%s", n.Message, want)
	}

	// A change anywhere else says nothing about the prompt.
	write(t, ws, "other.txt", "x\n")
	other, ok := a.NextTreeNotice(false)
	if !ok {
		t.Fatal("expected a notice")
	}
	if strings.Contains(other.Message, "project instructions") {
		t.Errorf("only an instruction file is one:\n%s", other.Message)
	}
}

// The sentence rides on whichever half of the reading names the file: git
// sees a path it has an opinion about, and the record of what was shown sees
// a rewrite of a file that was already dirty.
func TestTree_TheInstructionSentenceRidesOnEitherHalf(t *testing.T) {
	last := TreeSnapshot{Head: "1111111aaaa", Branch: "main", Status: map[string]string{}}
	now := TreeSnapshot{Head: "1111111aaaa", Branch: "main", Status: map[string]string{"docs/x.md": "??"}}
	instructions := map[string]bool{"AGENTS.md": true}

	n, ok := diffTree(last, now, treeAttribution{instructions: instructions, read: []string{"AGENTS.md"}})
	if !ok {
		t.Fatal("expected a notice")
	}
	if !strings.Contains(n.Message, "The project instructions changed (AGENTS.md)") {
		t.Errorf("a stale reading of an instruction file is one:\n%s", n.Message)
	}
	// Named once, whichever lists hold it.
	if c := strings.Count(n.Message, "The project instructions changed"); c != 1 {
		t.Errorf("said %d times:\n%s", c, n.Message)
	}
	both := TreeSnapshot{Head: "1111111aaaa", Branch: "main", Status: map[string]string{"AGENTS.md": ".M"}}
	n, _ = diffTree(last, both, treeAttribution{instructions: instructions, read: []string{"AGENTS.md"}})
	if strings.Count(n.Message, "AGENTS.md)") != 1 {
		t.Errorf("a file in both lists is named once in the sentence:\n%s", n.Message)
	}
	// And a session with no instruction block hears nothing about one.
	n, _ = diffTree(last, both, treeAttribution{})
	if strings.Contains(n.Message, "project instructions") {
		t.Errorf("no block, no sentence:\n%s", n.Message)
	}
}

// The state directory holds shhh's own bookkeeping, which is not the tree
// moving — and it may hold the project's instruction file, which is.
func TestTree_AnInstructionFileInTheStateDirectoryIsStillNews(t *testing.T) {
	last := TreeSnapshot{Head: "1111111aaaa", Branch: "main", Status: map[string]string{}}
	now := TreeSnapshot{Head: "1111111aaaa", Branch: "main", Status: map[string]string{
		".shhh/project.md": ".M",
		".shhh/run.json":   ".M",
	}}

	n, ok := diffTree(last, now, treeAttribution{instructions: map[string]bool{".shhh/project.md": true}})
	if !ok {
		t.Fatal("expected a notice")
	}
	if !strings.Contains(n.Message, "1 path changed outside this session: .shhh/project.md") {
		t.Errorf("the project's own file is named:\n%s", n.Message)
	}
	if strings.Contains(n.Message, "run.json") {
		t.Errorf("a checkpoint is not the tree moving:\n%s", n.Message)
	}
	if !strings.Contains(n.Message, "The project instructions changed (.shhh/project.md)") {
		t.Errorf("and it carries the sentence:\n%s", n.Message)
	}
	if _, ok := diffTree(last, now, treeAttribution{}); ok {
		t.Error("with no instruction block, the state directory is all there is")
	}
}

// The two halves are different claims about possibly overlapping sets, so
// they are two clauses rather than one count.
func TestTree_MovedPathsAndStaleReadsAreReportedTogether(t *testing.T) {
	last := TreeSnapshot{Head: "1111111aaaa", Branch: "main", Status: map[string]string{"a": ".M"}}
	now := TreeSnapshot{Head: "1111111aaaa", Branch: "main", Status: map[string]string{"a": ".M", "new": "??"}}

	n, ok := diffTree(last, now, treeAttribution{read: []string{"b", "a"}})
	if !ok {
		t.Fatal("expected a notice")
	}
	if want := "[tree: 1 path changed outside this session: new · 2 files you have read changed: a, b]"; !strings.HasPrefix(n.Message, want) {
		t.Errorf("message:\n%s\nwant prefix:\n%s", n.Message, want)
	}
	if n.Notice != "tree moved — 1 path changed outside this session, 2 files you have read changed" {
		t.Errorf("notice = %q", n.Notice)
	}
	if n.Signal() != "paths" {
		t.Errorf("signal = %q", n.Signal())
	}
	// A commit and a stale reading together are still both halves to the
	// recorder, whose set of three has no fourth answer.
	moved := TreeSnapshot{Head: "2222222bbbb", Branch: "main", Status: map[string]string{"a": ".M"}}
	if s := mustDiff(t, last, moved, []string{"a"}).Signal(); s != "both" {
		t.Errorf("signal = %q, want both", s)
	}
}

func mustDiff(t *testing.T, last, now TreeSnapshot, read []string) TreeNotice {
	t.Helper()
	n, ok := diffTree(last, now, treeAttribution{read: read})
	if !ok {
		t.Fatal("expected a notice")
	}
	return n
}

// What the tree ignores is not the tree moving. The suppressed paths are
// counted on the row rather than dropped in silence, so a reading of fourteen
// in a checkout where six thousand paths moved says which reading it is.
func TestTree_TheIgnoredAreCountedNotReported(t *testing.T) {
	last := TreeSnapshot{Head: "1111111aaaa", Branch: "main", Status: map[string]string{}}
	now := TreeSnapshot{Head: "1111111aaaa", Branch: "main", Status: map[string]string{
		"src/handler.go":      ".M",
		".cache/gocache/aa/x": "??",
		".cache/gocache/aa/y": "??",
	}}
	asked := 0
	ignore := func(paths []string) map[string]bool {
		asked++
		out := map[string]bool{}
		for _, p := range paths {
			if strings.HasPrefix(p, ".cache/") {
				out[p] = true
			}
		}
		return out
	}

	n, ok := diffTree(last, now, treeAttribution{ignored: ignore})
	if !ok {
		t.Fatal("a tracked file somebody else changed is still news")
	}
	if n.Paths != 1 || n.Ignored != 2 {
		t.Errorf("paths = %d, ignored = %d; want 1 and 2", n.Paths, n.Ignored)
	}
	if n.Notice != "tree moved — 1 path changed outside this session · 2 ignored" {
		t.Errorf("notice = %q", n.Notice)
	}
	if strings.Contains(n.Message, ".cache/") {
		t.Errorf("the model is told what moved, not what was ignored:\n%s", n.Message)
	}
	// One question for the whole reading: the paths this exists to throw away
	// are exactly the ones a call per path would be paid for.
	if asked != 1 {
		t.Errorf("the ignore rules were asked %d times, want once", asked)
	}

	// A reading whose only movement was ignored is not a reading at all.
	cache := TreeSnapshot{Head: "1111111aaaa", Branch: "main", Status: map[string]string{
		".cache/gocache/aa/x": "??",
		".cache/gocache/aa/y": "??",
	}}
	if n, ok := diffTree(last, cache, treeAttribution{ignored: ignore}); ok {
		t.Errorf("an all-ignored reading draws nothing, got:\n%s", n.Notice)
	}
}

// The row's numbers are read rather than computed with.
func TestTree_CountsAreGrouped(t *testing.T) {
	for n, want := range map[int]string{0: "0", 14: "14", 999: "999", 5811: "5,811", 1234567: "1,234,567"} {
		if got := grouped(n); got != want {
			t.Errorf("grouped(%d) = %q, want %q", n, got, want)
		}
	}
}

// The rules are the tree's own, asked of git, which is what makes a tracked
// file that happens to match a pattern still news: somebody changed a file
// git is keeping.
func TestTree_TheTreesOwnIgnoreRulesAreAsked(t *testing.T) {
	ws, _ := treeFixture(t)
	write(t, ws, "logs/run.log", "old\n")
	a := treeAgent(t, ws, nil)

	// Somebody adds the rule that covers the directory already sitting there:
	// the status stops naming it, which is a difference between the two
	// snapshots and is not the tree moving.
	write(t, ws, ".gitignore", "logs/\n")
	n, ok := a.NextTreeNotice(false)
	if !ok {
		t.Fatal("the new .gitignore is itself a path somebody else wrote")
	}
	if n.Paths != 1 || n.Ignored != 1 {
		t.Errorf("paths = %d, ignored = %d; want 1 and 1 (%s)", n.Paths, n.Ignored, n.Notice)
	}
	if n.Notice != "tree moved — 1 path changed outside this session · 1 ignored" {
		t.Errorf("notice = %q", n.Notice)
	}
	if strings.Contains(n.Message, "logs/") {
		t.Errorf("an ignored path is not named to the model:\n%s", n.Message)
	}
}

// The cache a command of this session wrote is the session's own scratch.
// This is the notice the reading cried wolf with: a build cache under a
// directory the same turn created, counted as somebody else's work.
func TestTree_ACacheACommandWroteIsNotReported(t *testing.T) {
	for _, tc := range []struct {
		name      string
		untracked string
	}{
		// The reading asks git for the untracked mode, so a checkout
		// configured to name every file under a new directory and one that
		// collapses it are the same reading and the same subtraction.
		{"the default", "normal"},
		{"a checkout that asks for every file", "all"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws, run := treeFixture(t)
			run("config", "status.showUntrackedFiles", tc.untracked)
			write(t, ws, "src/keep.go", "package src\n")
			run("add", ".")
			run("commit", "-q", "-m", "src")
			a := treeAgent(t, ws, nil)

			a.BeginToolRound("", []provider.ToolCall{{ID: "c1", Name: "execute_command",
				Arguments: `{"command":"GOCACHE=$PWD/.cache/gocache go build ./..."}`}}, nil)
			for _, p := range []string{".cache/gocache/aa/one", ".cache/gocache/bb/two", ".cache/trim.txt"} {
				write(t, ws, p, "x\n")
			}
			// Somebody else, meanwhile, in a directory git already keeps
			// files in: the line the subtraction may not cross.
			write(t, ws, "src/theirs.go", "package src\n")

			n, ok := a.NextTreeNotice(false)
			if !ok {
				t.Fatal("a file in a tracked directory is still reported")
			}
			if n.Paths != 1 || !strings.Contains(n.Message, "src/theirs.go") {
				t.Errorf("paths = %d, want the one foreign file:\n%s", n.Paths, n.Message)
			}
			if strings.Contains(n.Message, ".cache") {
				t.Errorf("the cache the command wrote is not somebody else's:\n%s", n.Message)
			}

			// It goes on growing after the round that made it, and goes on
			// being the session's.
			write(t, ws, ".cache/gocache/cc/three", "x\n")
			if n, ok := a.NextTreeNotice(false); ok {
				t.Errorf("the cache is still the session's at a later boundary:\n%s", n.Message)
			}
		})
	}
}

// Without a command in the round there is nothing to attribute a new
// directory to, and a stranger's new directory is exactly what the reading
// exists to report.
func TestTree_ANewDirectoryWithNoCommandIsStillReported(t *testing.T) {
	ws, _ := treeFixture(t)
	a := treeAgent(t, ws, nil)
	write(t, ws, "vendor/theirs/x.go", "package theirs\n")
	n, ok := a.NextTreeNotice(false)
	if !ok || n.Paths != 1 {
		t.Fatalf("a directory nobody here made is news, got ok=%v:\n%s", ok, n.Message)
	}
}
