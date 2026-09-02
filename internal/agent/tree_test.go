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
	n, ok := diffTree(last, now, map[string]bool{"mine": true}, 0)
	if !ok {
		t.Fatal("expected a notice")
	}
	if want := "[tree: HEAD 1111111 → 2222222 · 3 paths changed outside this session: a, gone, new]"; !strings.HasPrefix(n.Message, want) {
		t.Errorf("message:\n%s\nwant prefix:\n%s", n.Message, want)
	}
	if n.Signal() != "both" {
		t.Errorf("signal = %q", n.Signal())
	}
	if _, ok := diffTree(now, now, nil, 0); ok {
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
