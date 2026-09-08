package subagent

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/tools"
)

// A surface's seams reach both of a child's dispatchers — the calls it runs
// on its own and the calls it is decided about — and each of them is handed
// the child's own position, transcript and recorder. Everything a session
// puts on its own tool seams goes through here, so a wrap that reached one
// dispatcher and not the other would be a rule that held for a child's reads
// and not for its commands.
func TestASurfacesSeamsReachBothOfAChildsDispatchers(t *testing.T) {
	env := &scriptedEnv{
		steps: []streamStep{
			{calls: []provider.ToolCall{{ID: "r1", Name: "read_file", Arguments: `{"path":"x"}`}}},
			{calls: []provider.ToolCall{{ID: "c1", Name: tools.ExecCommandName, Arguments: `{"command":"echo hi"}`}}},
			{text: "task complete"},
		},
		gated:   map[string]bool{tools.ExecCommandName: true},
		execOut: "hi",
	}

	var mu sync.Mutex
	var auto, gated []string
	var at []observe.Pos
	var decisions []string
	base := env.factory()
	factory := func(ctx context.Context, spec Spec) (Env, error) {
		e, err := base(ctx, spec)
		if err != nil {
			return e, err
		}
		e.WrapAuto = func(s Seam, next agent.ToolExecutor) agent.ToolExecutor {
			return func(name string, args json.RawMessage) (string, error) {
				mu.Lock()
				auto = append(auto, name)
				mu.Unlock()
				return next(name, args)
			}
		}
		e.WrapGated = func(s Seam, next func(provider.ToolCall) string) func(provider.ToolCall) string {
			return func(tc provider.ToolCall) string {
				mu.Lock()
				gated = append(gated, tc.Name)
				at = append(at, s.At())
				mu.Unlock()
				s.Note("hook — a rule looked at this")
				s.Record(observe.DecisionAllow, observe.ReasonHook)
				return next(tc)
			}
		}
		return e, nil
	}

	sup := New(context.Background(), Options{
		Root:             t.TempDir(),
		NewEnv:           factory,
		CommandAllowlist: []string{"echo"},
		Record: func(Spec, string) Recorder {
			return Recorder{Observer: observe.Observer{
				Decision: func(_ observe.Pos, decision, code string) {
					mu.Lock()
					decisions = append(decisions, decision+"/"+code)
					mu.Unlock()
				},
			}}
		},
	})
	t.Cleanup(sup.Close)
	sup.SetParentMode(agent.ModeAuto)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"read then run"}`)
	execTool(t, sup, ReportToolName, `{"name":"researcher-1"}`)

	mu.Lock()
	defer mu.Unlock()
	if len(auto) == 0 || auto[0] != "read_file" {
		t.Errorf("the auto-run dispatcher was not wrapped, it saw %v", auto)
	}
	if len(gated) == 0 || gated[0] != tools.ExecCommandName {
		t.Errorf("the gated dispatcher was not wrapped, it saw %v", gated)
	}
	// The seam is around the whole resolution, so the position it reports is
	// the child's own — a turn it is in and a round it has reached.
	if len(at) == 0 || at[0].Turn < 1 || at[0].Round < 1 {
		t.Errorf("the seam was given no position for the child: %+v", at)
	}
	if len(decisions) == 0 || decisions[0] != observe.DecisionAllow+"/"+observe.ReasonHook {
		t.Errorf("what the seam decided was not recorded: %v", decisions)
	}
	var noted bool
	for _, e := range sup.Transcript("researcher-1") {
		if strings.Contains(e.Text, "a rule looked at this") {
			noted = true
		}
	}
	if !noted {
		t.Error("the line the seam wrote never reached the child's own transcript")
	}
}

// A surface that installs nothing leaves the dispatchers exactly as its Env
// built them, so a session with no seams of its own pays nothing for these.
func TestAChildWithoutSeamsRunsTheEnvsOwnDispatchers(t *testing.T) {
	e := Env{Executor: func(name string, _ json.RawMessage) (string, error) { return "auto:" + name, nil }}
	got, err := e.autoExecutor(Seam{})("read_file", nil)
	if err != nil || got != "auto:read_file" {
		t.Fatalf("an unwrapped child got %q, %v", got, err)
	}
}

// What a child has written is subtracted from its own reading, and it is
// named in the tree the reading is taken in: a writer's model spells a path
// against the worktree it is standing in, which is not the directory this
// process is standing in.
func TestAChildsWritesAreNamedInTheTreeItsReadingIsTakenIn(t *testing.T) {
	c := &child{root: filepath.Join("/work", "wt-1"), wrote: map[string]bool{
		"internal/a.go":               true,
		"/elsewhere/absolute/path.go": true,
	}}
	got := c.ownPaths()
	if len(got) != 2 {
		t.Fatalf("got %v", got)
	}
	var joined, kept bool
	for _, p := range got {
		switch p {
		case filepath.Join("/work", "wt-1", "internal/a.go"):
			joined = true
		case "/elsewhere/absolute/path.go":
			kept = true
		}
	}
	if !joined {
		t.Errorf("a relative path was not resolved against the child's own root: %v", got)
	}
	if !kept {
		t.Errorf("an absolute path was rewritten: %v", got)
	}
}

// The reading itself is turned on where the surface asked for one, and the
// child's own edits are what it subtracts. A child that reported every file
// it had just written as somebody else's work would spend its rounds
// explaining itself to itself.
func TestAChildsTreeReadingSubtractsItsOwnWrites(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(ws, "a.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-q", "-m", "init")

	c := &child{root: ws}
	a := agent.New(nil, nil)
	c.watchTree(a, Env{TreeCheck: &agent.TreeCheck{Dir: ws}})
	if !a.TreeChecking() {
		t.Fatal("the surface asked for a reading and the child took none")
	}

	// One file the child wrote, one somebody else did.
	if err := os.WriteFile(filepath.Join(ws, "mine.txt"), []byte("the child\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "theirs.txt"), []byte("somebody else\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c.noteWrite(provider.ToolCall{Name: tools.WriteFileName,
		Arguments: `{"path":"mine.txt","content":"the child\n"}`}, "wrote mine.txt")

	n, ok := a.NextTreeNotice(false)
	if !ok {
		t.Fatal("a changed tree told the child nothing")
	}
	if strings.Contains(n.Message, "mine.txt") {
		t.Errorf("the child was told about its own write:\n%s", n.Message)
	}
	if !strings.Contains(n.Message, "theirs.txt") {
		t.Errorf("the child was not told about somebody else's:\n%s", n.Message)
	}
}

// A child whose surface asked for no reading takes none, which is what a
// checkout that is not a repository and a config that turned it off both
// come out as.
func TestAChildWithoutATreeReadingTakesNone(t *testing.T) {
	c := &child{root: t.TempDir()}
	a := agent.New(nil, nil)
	c.watchTree(a, Env{})
	if a.TreeChecking() {
		t.Fatal("a child took a reading nobody asked for")
	}
}
