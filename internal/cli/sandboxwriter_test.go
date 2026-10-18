//go:build !windows

package cli

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/rfizzle/llm"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/tools"
)

// sandboxWriterRepo is a checkout a writer can take a copy of.
func sandboxWriterRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"-c", "user.email=t@shhh", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Skipf("git %v failed (%v): %s", args, err, out)
		}
	}
	return root
}

// containerNamed is the name the engine's log gave the container whose one
// mount is dir, or "" where none was made over it.
func containerNamed(calls, dir string) string {
	// A container mounts the resolved path; a temp directory on macOS sits
	// behind a symlink.
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	for _, line := range strings.Split(calls, "\n") {
		if !strings.HasPrefix(line, "run --detach") || !strings.Contains(line, "--volume "+dir+":/workspace") {
			continue
		}
		if m := regexp.MustCompile(`--name (shhh-sbx-[0-9a-f]+)`).FindStringSubmatch(line); m != nil {
			return m[1]
		}
	}
	return ""
}

// A writer spawned from a sandbox session gets a container of its own: its
// one mount is the writer's copy, it is made from the image and with the
// flags the session's container was, and the writer's command execs there
// through the helper with the stop beside it. The container goes when the
// copy does — the supervisor's close here — and one still standing when the
// session's cleanup runs goes with the session's own.
func TestSandboxWriterGetsItsOwnContainer(t *testing.T) {
	log := fakeSandboxEngine(t, helperAnswers)
	cfg, sc, ws := sandboxTestSetup(t)
	repo := sandboxWriterRepo(t)
	_, writers, cleanup, err := sandboxContainment(context.Background(), cfg, ws, sc, nil, nil)
	if err != nil {
		t.Fatalf("sandboxContainment: %v", err)
	}
	t.Cleanup(cleanup)

	var mu sync.Mutex
	var roots []string
	ran := make(chan tools.ExecResult, 1)
	steps := []llm.StreamEvent{
		{ToolCalls: []llm.ToolCall{{ID: "c1", Name: tools.ExecCommandName, Arguments: `{"command":"echo from-the-writer"}`}}},
		{Token: "done"},
	}
	newEnv := func(ctx context.Context, spec subagent.Spec) (subagent.Env, error) {
		run, refusal, err := sandboxWriterCommandsIn(ctx, writers, spec, repo)
		if err != nil {
			return subagent.Env{}, err
		}
		if spec.Root != repo {
			mu.Lock()
			roots = append(roots, spec.Root)
			mu.Unlock()
		}
		next := 0
		return subagent.Env{
			SystemPrompt: "a writer in a sandbox session",
			Stream: func([]llm.Message, string) (<-chan llm.StreamEvent, context.CancelFunc, error) {
				ch := make(chan llm.StreamEvent, 2)
				if next < len(steps) {
					ch <- steps[next]
					next++
				}
				ch <- llm.StreamEvent{Done: true}
				close(ch)
				return ch, func() {}, nil
			},
			Executor:     func(_ context.Context, name string, _ json.RawMessage) (string, error) { return "auto:" + name, nil },
			ExecuteGated: func(_ context.Context, name string, _ json.RawMessage) (string, error) { return "gated:" + name, nil },
			RunCommand: func(ctx context.Context, command string) tools.ExecResult {
				if run == nil {
					return tools.ExecResult{Output: refusal, ExitCode: -1, Outcome: tools.ExecDidNotStart}
				}
				result := run(ctx, command)
				ran <- result
				return result
			},
			CommandRefusal: refusal,
			Gated:          map[string]bool{tools.ExecCommandName: true},
		}, nil
	}
	sup := subagent.New(t.Context(), subagent.Options{Root: repo, NewEnv: newEnv, Removed: writers.releaser()})
	answerChildAsks(sup, true, nil, func(*subagent.Ask) bool { return true }, nil)
	spawn := sup.WrapExecutor("", func(context.Context, string, json.RawMessage) (string, error) { return "", context.Canceled })
	if _, err := spawn(context.Background(), subagent.SpawnToolName, json.RawMessage(`{"role":"writer","task":"run it","name":"writer-1"}`)); err != nil {
		t.Fatalf("spawn: %v", err)
	}

	got := <-ran
	if got.Outcome != tools.ExecSucceeded || !strings.Contains(got.Output, "from-the-writer") {
		t.Fatalf("the writer's command = %+v", got)
	}
	mu.Lock()
	if len(roots) == 0 {
		mu.Unlock()
		t.Fatal("the writer was never given a copy of its own")
	}
	copyRoot := roots[0]
	mu.Unlock()

	calls := engineCalls(t, log)
	session := containerNamed(calls, ws)
	writer := containerNamed(calls, copyRoot)
	if session == "" || writer == "" || session == writer {
		t.Fatalf("want a container over the session's workspace and another over the writer's copy %s:\n%s", copyRoot, calls)
	}
	if strings.Count(calls, "run --detach") != 2 {
		t.Errorf("want two containers made, the session's and the writer's:\n%s", calls)
	}
	for _, line := range strings.Split(calls, "\n") {
		if strings.Contains(line, "--name "+writer) {
			for _, want := range []string{"--network none", "--cap-drop ALL", "--init", sandboxTestImage} {
				if !strings.Contains(line, want) {
					t.Errorf("the writer's container is not made as the session's is (%q):\n%s", want, line)
				}
			}
		}
	}
	if !helperCall(" --stdin=null --hangup=stop -- /bin/sh -c echo from-the-writer").MatchString(calls) ||
		!strings.Contains(calls, "exec -i --workdir /workspace "+writer+" ") {
		t.Errorf("the writer's command did not exec through the helper in its own container:\n%s", calls)
	}
	if strings.Contains(calls, "rm --force "+writer) {
		t.Errorf("the writer's container went before its copy did:\n%s", calls)
	}

	sup.Close()
	if calls := engineCalls(t, log); !strings.Contains(calls, "rm --force "+writer) || strings.Contains(calls, "rm --force "+session) {
		t.Errorf("the copy went and its container did not, or the session's went with it:\n%s", calls)
	}

	// A writer whose container is still standing when the session ends goes
	// with the session's, and no container is made after that.
	late := t.TempDir()
	box, err := writers.open(context.Background(), late)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	cleanup()
	calls = engineCalls(t, log)
	if !strings.Contains(calls, "rm --force "+box.Record.Name) || !strings.Contains(calls, "rm --force "+session) {
		t.Errorf("the session's cleanup left a container behind:\n%s", calls)
	}
	if _, err := writers.open(context.Background(), filepath.Join(t.TempDir(), "after")); err == nil {
		t.Error("a writer's container was made after the session's sandbox closed")
	}
}

// A writer's container that cannot be made fails the writer with the
// engine's own sentence, and the writer is given no runner anywhere else.
// The environment measured before a spawn is admitted names the session's
// checkout, not a copy, and is never run: it gets the refusal and no
// container.
func TestSandboxWriterWithoutAContainerRunsNowhere(t *testing.T) {
	log := fakeSandboxEngine(t, helperAnswers)
	cfg, sc, ws := sandboxTestSetup(t)
	_, writers, cleanup, err := sandboxContainment(context.Background(), cfg, ws, sc, nil, nil)
	if err != nil {
		t.Fatalf("sandboxContainment: %v", err)
	}
	defer cleanup()

	run, refusal, err := sandboxWriterCommandsIn(context.Background(), writers, subagent.Spec{Root: ws, Worktree: true}, ws)
	if err != nil || run != nil || refusal != sandboxChildRefusal {
		t.Errorf("a preflight got run %v, refusal %q, err %v", run != nil, refusal, err)
	}
	if strings.Count(engineCalls(t, log), "run --detach") != 1 {
		t.Errorf("a preflight made a container:\n%s", engineCalls(t, log))
	}

	// The engine now refuses every create.
	writers.eng.Path = filepath.Join(t.TempDir(), "no-engine")
	run, _, err = sandboxWriterCommandsIn(context.Background(), writers, subagent.Spec{Root: t.TempDir(), Worktree: true}, ws)
	if err == nil || run != nil || !strings.Contains(err.Error(), "the writer's sandbox container could not be made") ||
		!strings.Contains(err.Error(), "create failed") {
		t.Errorf("a create that failed gave run %v, err %v", run != nil, err)
	}
}

// What the writer is told, the spawn card and /status all read one answer:
// a container of its own over its copy, in the session indicator's words
// for the mechanism and the profile, with the network the profile gives.
func TestSandboxWriterIsToldAndShownItsContainer(t *testing.T) {
	fakeSandboxEngine(t, helperAnswers)
	cfg, sc, ws := sandboxTestSetup(t)
	c, writers, cleanup, err := sandboxContainment(context.Background(), cfg, ws, sc, nil, nil)
	if err != nil {
		t.Fatalf("sandboxContainment: %v", err)
	}
	defer cleanup()

	field := writers.words.field()
	if field.Label != "commands" || field.Value != "contained · container sandbox" || field.Open ||
		field.Detail != "required · its own container over its worktree · workspace-netless · network closed" {
		t.Errorf("the card's row = %+v", field)
	}
	if !strings.Contains(c.Writers, "a writer's commands: contained · container sandbox — ") {
		t.Errorf("/status says %q", c.Writers)
	}
	said := commandEnvironmentBlock(writers.said)
	for _, want := range []string{
		"contained by a disposable container of its own, whose one mount is your copy of the checkout under the workspace-netless profile",
		"no network at all",
	} {
		if !strings.Contains(said, want) {
			t.Errorf("the writer is not told %q:\n%s", want, said)
		}
	}
}

// The card a writer is approved on in a sandbox session says what contains
// its commands before anyone answers it, and a researcher's says nothing of
// commands it does not run.
// See docs/capabilities/containment.md#containment-can-be-required.
func TestSandboxSpawnCardSaysWhatContainsAWriter(t *testing.T) {
	fakeSandboxEngine(t, helperAnswers)
	w, built, out, err := assembleSessionWith(t, sandboxTestConfig, "code", "--sandbox")
	if err != nil || !built {
		t.Fatalf("shhh code --sandbox did not open a session (built %v): %v\n%s", built, err, out)
	}
	preview := w.GatedTools[subagent.SpawnToolName]
	if preview == nil {
		t.Fatal("the session draws no spawn card")
	}
	commands := func(args string) (string, bool) {
		p, err := preview(json.RawMessage(args))
		if err != nil {
			t.Fatalf("preview %s: %v", args, err)
		}
		for _, f := range p.Fields {
			if f.Label == "commands" {
				return f.Value + " — " + f.Detail, true
			}
		}
		return "", false
	}
	got, ok := commands(`{"role":"writer","task":"run it","name":"writer-1"}`)
	if !ok || !strings.HasPrefix(got, "contained · container sandbox — ") || !strings.Contains(got, "its own container over its worktree · workspace-netless") {
		t.Errorf("the writer's card says %q", got)
	}
	if got, ok := commands(`{"role":"researcher","task":"read it","name":"researcher-1"}`); ok {
		t.Errorf("a researcher's card names commands: %q", got)
	}
}
