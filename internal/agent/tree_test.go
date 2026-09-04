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
	if n.Notice != "Tree moved — 1 path changed outside this session." {
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
	n, ok := diffTree(last, now, map[string]bool{"mine": true}, nil, 0, nil)
	if !ok {
		t.Fatal("expected a notice")
	}
	if want := "[tree: HEAD 1111111 → 2222222 · 3 paths changed outside this session: a, gone, new]"; !strings.HasPrefix(n.Message, want) {
		t.Errorf("message:\n%s\nwant prefix:\n%s", n.Message, want)
	}
	if n.Signal() != "both" {
		t.Errorf("signal = %q", n.Signal())
	}
	if _, ok := diffTree(now, now, nil, nil, 0, nil); ok {
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

	alone, ok := diffTree(last, now, nil, nil, 0, func() bool { return false })
	if !ok {
		t.Fatal("expected a notice")
	}
	if strings.Contains(alone.Message, "another session") {
		t.Errorf("nobody else is here to name:\n%s", alone.Message)
	}

	shared, ok := diffTree(last, now, nil, nil, 0, func() bool { return true })
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
	afterCommand, _ := diffTree(last, now, nil, nil, 1, func() bool { return true })
	if !strings.HasSuffix(afterCommand.Message, "another session is open in this checkout.") {
		t.Errorf("the clause is owed on both wordings:\n%s", afterCommand.Message)
	}
}

// Nil is the honest answer for a surface with nothing to ask, and it costs
// the clause rather than the block.
func TestTree_NoSiblingReadingCostsOnlyTheClause(t *testing.T) {
	last := TreeSnapshot{Head: "1111111aaaa", Branch: "main", Status: map[string]string{}}
	now := TreeSnapshot{Head: "1111111aaaa", Branch: "main", Status: map[string]string{"a": ".M"}}
	n, ok := diffTree(last, now, nil, nil, 0, nil)
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
	if n.Notice != "Tree moved — 1 file you have read changed." {
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

// The two halves are different claims about possibly overlapping sets, so
// they are two clauses rather than one count.
func TestTree_MovedPathsAndStaleReadsAreReportedTogether(t *testing.T) {
	last := TreeSnapshot{Head: "1111111aaaa", Branch: "main", Status: map[string]string{"a": ".M"}}
	now := TreeSnapshot{Head: "1111111aaaa", Branch: "main", Status: map[string]string{"a": ".M", "new": "??"}}

	n, ok := diffTree(last, now, nil, []string{"b", "a"}, 0, nil)
	if !ok {
		t.Fatal("expected a notice")
	}
	if want := "[tree: 1 path changed outside this session: new · 2 files you have read changed: a, b]"; !strings.HasPrefix(n.Message, want) {
		t.Errorf("message:\n%s\nwant prefix:\n%s", n.Message, want)
	}
	if n.Notice != "Tree moved — 1 path changed outside this session, 2 files you have read changed." {
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
	n, ok := diffTree(last, now, nil, read, 0, nil)
	if !ok {
		t.Fatal("expected a notice")
	}
	return n
}
